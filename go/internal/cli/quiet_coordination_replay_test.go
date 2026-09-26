package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// TestQuietCoordinationReplay is the one integrated replay of the quiet-coordinator program (#238 R17, #242): twelve
// dispatched workers across three enrolled projects with distinct canonical identities, two enrolled factories holding
// one lane each, driven only through the built helper against the fake Herdr and a fake GitHub CLI. It proves, on
// saved records and fake call logs:
//
//   - AE1: twelve concurrent routine reports while the adopted coordinator is idle submit exactly one routine prompt
//     before consumption; every obligation stays retrievable (`inbox`, `wake show` included/omitted,
//     returns.OpenObligations).
//   - #240b: a new decision behind the outstanding routine wake gets exactly one priority prompt; nothing is
//     suppressed; `wake consume --boundary` closes exactly the shown episode; the answer reaches the worker.
//   - #242: one factory lane progresses report -> coordinator verify -> independent review -> pipeline pr (fake gh) ->
//     merged observation -> cleanup -> lane release, and the digests (`factory status --project --since`,
//     `status --grouped --project --since`) label each step as a new outcome without performing any of them; the
//     second factory's gated issue keeps its lane; capacity and cleanup stay green.
//   - Fallbacks: with the hook disabled, metadata disabled and the native capability absent, an explicit pass still
//     delivers and `wake show`/`metadata status`/`hook status` say so honestly.
//   - Read purity: every read-only phase leaves the whole home tree byte-identical, adds no fake gh call and no
//     Herdr prompt; the fake gh log holds only the explicitly driven workflow verbs and never a merge.
//
// It records (never asserts as guarantees) baseline versus candidate prompt submissions on identical inputs, coalesced
// and deferred counts, structured stdout bytes, helper calls and lock waits into replay.json (and SUM_MEASURE_OUT when
// set). It claims nothing about tokens, latency or agent narration; the native/harness lab is separately not-run.
// Older helpers on these records are covered by TestOlderHelpersOnCandidateRecords, not rebuilt here.
func TestQuietCoordinationReplay(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	lab := newReplayLab(t)
	m := lab.measure

	// --- Setup: coordinator, projects, workers, factories -----------------------------------------------------------
	if got := asString(lab.ctl(true, "init")["role"]); got != "coordinator" {
		t.Fatalf("init role %q", got)
	}
	if owner := lab.owner(t); ownerField(owner, "wake_protocol") == "" {
		t.Fatal("init did not adopt the wake protocol on the owner record")
	}
	capacity := lab.ctl(true, "settings", "set", "--global", "14", "--per-repository", "6", "--auto-publish-evidence", "false")
	if asMap(capacity["capacity"])["global"] != float64(14) {
		t.Fatalf("capacity %v", capacity)
	}
	hook := lab.ctl(true, "hook", "enable")
	lab.pluginID = asString(hook["plugin_id"])
	if lab.pluginID == "" {
		t.Fatalf("hook enable %v", hook)
	}
	meta := lab.ctl(true, "metadata", "enable")
	if meta["error"] != nil {
		t.Fatalf("metadata enable %v", meta)
	}
	for _, p := range lab.projects {
		enrolled := lab.ctl(true, "project", "enroll", p.spec, "--path", p.repo, "--remote", p.origin)
		if enrolled["enrolled"] != true || asString(asMap(enrolled["project"])["name"]) != p.name {
			t.Fatalf("enroll %s: %v", p.spec, enrolled)
		}
	}
	for i := 0; i < 12; i++ {
		p := lab.projects[i%3]
		task := lab.ctl(true, "dispatch", "--project", p.name, "--brief", lab.brief, "--approved")
		id, pane, worktree := asString(task["id"]), asString(task["pane"]), asString(task["worktree"])
		if id == "" || pane == "" || worktree == "" {
			t.Fatalf("dispatch %d: %v", i, task)
		}
		worker := lab.ctlPane(pane, true, "init")
		if asString(worker["role"]) != "worker" || asString(worker["task"]) != id {
			t.Fatalf("worker init %v", worker)
		}
		lab.tasks = append(lab.tasks, replayTask{id: id, pane: pane, worktree: worktree, project: p})
	}
	laneA, laneB, standalone := lab.tasks[0], lab.tasks[1], lab.tasks[2] // a/repo, b/repo, ghe.example.com/b/repo
	lab.writeGH("issues.json", []any{
		map[string]any{"number": 41, "title": "Lane A issue", "state": "OPEN", "labels": []any{map[string]any{"name": "ready-a"}}},
		map[string]any{"number": 52, "title": "Lane B issue", "state": "OPEN", "labels": []any{map[string]any{"name": "ready-b"}}},
	})
	lab.ctl(true, "factory", "enable", "a/repo", "--lanes", "1", "--label", "ready-a", "--strict-cleanup")
	lab.ctl(true, "factory", "enable", "b/repo", "--lanes", "1", "--label", "ready-b")
	ticks := tickActions(lab.ctl(true, "factory", "tick"))
	if ticks["a/repo"] != "dispatch" || ticks["b/repo"] != "dispatch" {
		t.Fatalf("first tick %v", ticks)
	}
	lab.ctl(true, "factory", "claim", "a/repo", "--issue", "41", "--task", laneA.id)
	lab.ctl(true, "factory", "claim", "b/repo", "--issue", "52", "--task", laneB.id)
	if ticks := tickActions(lab.ctl(true, "factory", "tick")); ticks["a/repo"] != "occupied" || ticks["b/repo"] != "occupied" {
		t.Fatalf("occupied tick %v", ticks)
	}
	m.record("setup", map[string]any{"tasks": len(lab.tasks), "projects": 3, "factories": 2, "gh_calls": lab.ghCalls(), "herdr_calls": lab.herdrCalls()})

	// The lane-A worker finishes its change before the burst: a candidate commit and the project's own verify run, so
	// its report is a real handoff the pipeline can act on later. Nothing here touches the home.
	candidate := lab.commit(laneA, "NOTES.md", "replay note\n")
	handoff := lab.workerHandoff(laneA, candidate)

	lab.readPhase(t, "reads-before-burst")

	// --- AE1: twelve concurrent routine reports, coordinator idle, one routine prompt ------------------------------
	baseline := lab.snapshotForBaseline(t)
	handoffs := map[string]string{laneA.id: handoff}
	burst, edges := lab.arrivals(t, handoffs)
	prompts := lab.promptsTo(lab.coordinatorPane)
	if prompts != 1 {
		t.Fatalf("AE1: routine prompts across twelve arrivals and three idle edges = %d, want exactly 1 (%+v, %v)", prompts, burst, edges)
	}
	lab.coordinatorStatus(t, "idle")
	pump := lab.ctl(true, "pump")
	pumpRow := recipientRow(pump, lab.coordinatorPane)
	// The coordinator's own pass is its inline listing (never a prompt); the edges above were the non-inline passes.
	if asString(pumpRow["via"]) != "inline" || pump["prompts"] != float64(0) {
		t.Fatalf("pass after the wake: prompts=%v via=%v state=%v", pump["prompts"], pumpRow["via"], pumpRow["state"])
	}
	for i, edge := range edges {
		if edge["prompts"] != float64(0) && edge["prompts"] != nil {
			t.Fatalf("idle edge %d prompted behind the outstanding wake: %v", i, edge)
		}
	}
	fanout := asMap(pump["fanout"])
	if n := lab.openObligations(t, lab.taskIDs()); n < 12 {
		t.Fatalf("AE1: open obligations = %d, want all twelve reports retrievable", n)
	}
	inbox := lab.ctl(true, "inbox")
	if n := len(asSlice(inbox["tasks"])); n != 12 {
		t.Fatalf("inbox lists %d tasks, want 12", n)
	}
	show := lab.wakeRow(t)
	if show["outstanding"] != true || asString(show["boundary"]) == "" || len(asSlice(show["omitted"])) != 0 {
		t.Fatalf("wake show after the burst: %v", show)
	}
	if n := len(asSlice(show["included"])); n < 12 {
		t.Fatalf("wake show includes %d identities, want all twelve reports", n)
	}
	// Attention from two workers behind the wake (hook events) stays coalesced too.
	lab.coordinatorStatus(t, "idle")
	for _, task := range lab.tasks[3:5] {
		lab.setScreen(t, task.pane, "Permission needed: allow write?")
		event := lab.herdrEvent(lab.pluginID, task.pane, "blocked", "sum-test")
		if asString(event["outcome"]) != "handled" {
			t.Fatalf("blocked edge %v", event)
		}
	}
	if prompts := lab.promptsTo(lab.coordinatorPane); prompts != 1 {
		t.Fatalf("attention behind the wake made a routine prompt: %d", prompts)
	}
	candidateBurst := map[string]any{
		"prompts": prompts, "arrivals": "6 concurrent, idle edge, 6 concurrent, 2 idle edges", "submitted": burst.submitted, "coalesced": burst.coalesced, "deferred": burst.deferred,
		"other_states": burst.other, "open_obligations": lab.openObligations(t, lab.taskIDs()),
		"report_passes":   map[string]any{"lock_wait_ms_total": burst.lockWaitMS, "lock_wait_ms_max": burst.maxLockWaitMS, "herdr_calls": burst.herdrCalls},
		"pump_after_wake": map[string]any{"prompts": pump["prompts"], "state": pumpRow["state"], "via": pumpRow["via"], "deferred": fanout["deferred"], "lock_wait_ms": fanout["lock_wait_ms"], "herdr_calls": fanout["herdr_calls"]},
		"wake_included":   len(asSlice(show["included"])), "wake_omitted": len(asSlice(show["omitted"])),
	}
	lab.coordinatorStatus(t, "working")
	lab.readPhase(t, "reads-after-burst")

	// Baseline: the same twelve reports on a byte-identical copy of the inputs whose owner record never adopted the
	// wake protocol (an older helper's coordinator): legacy one-prompt-per-pass. Recorded, not asserted as a guarantee.
	baselineBurst, _ := baseline.arrivals(t, handoffs)
	baselinePrompts := baseline.promptsTo(baseline.coordinatorPane)
	m.record("burst", map[string]any{
		"candidate": candidateBurst,
		"baseline": map[string]any{"prompts": baselinePrompts, "arrivals": "6 concurrent, idle edge, 6 concurrent, 2 idle edges", "submitted": baselineBurst.submitted, "coalesced": baselineBurst.coalesced,
			"deferred": baselineBurst.deferred, "other_states": baselineBurst.other, "open_obligations": baseline.openObligations(t, baseline.taskIDs()),
			"report_passes": map[string]any{"lock_wait_ms_total": baselineBurst.lockWaitMS, "lock_wait_ms_max": baselineBurst.maxLockWaitMS, "herdr_calls": baselineBurst.herdrCalls},
			"wake_sidecar":  baseline.wakeSidecarExists()},
		"note": "identical inputs: the baseline home is a copy taken before the burst with wake_protocol removed from the owner record; both labs run six report processes started together while the coordinator is idle, one coordinator idle edge (hook event), six more concurrent reports, then two more idle edges; the fake marks a prompted pane working, as Herdr does, so per-run submitted/not-delivered splits inside a burst vary while the prompt count across arrivals and edges is the contract",
	})

	// --- #240b: a new decision behind the routine wake ----------------------------------------------------------------
	lab.coordinatorStatus(t, "idle")
	asked := lab.ctlPane(standalone.pane, true, "ask", standalone.id, "--key", "scope", "--text", "Keep the trailing newline?")
	if asked["error"] != nil {
		t.Fatalf("ask %v", asked)
	}
	if prompts := lab.promptsTo(lab.coordinatorPane); prompts != 2 {
		t.Fatalf("#240b: prompts after a new decision = %d, want the routine one plus one priority prompt", prompts)
	}
	show = lab.wakeRow(t)
	if n := len(asSlice(show["priority_prompts"])); n != 1 {
		t.Fatalf("wake show priority prompts = %d: %v", n, show)
	}
	if n := lab.openObligations(t, lab.taskIDs()); n < 13 {
		t.Fatalf("open obligations after the decision = %d, want the question kept alongside the twelve reports", n)
	}
	repeated := lab.ctl(true, "pump")
	if repeated["prompts"] != float64(0) || lab.promptsTo(lab.coordinatorPane) != 2 {
		t.Fatalf("a repeated pass re-prompted an unchanged decision: %v", repeated)
	}
	lab.coordinatorStatus(t, "working")
	lab.readPhase(t, "reads-with-decision")

	// Exact consumption: the shown boundary closes exactly that episode and nothing else changes.
	show = lab.wakeRow(t)
	openBefore := lab.openObligations(t, lab.taskIDs())
	consumed := lab.ctl(true, "wake", "consume", "--boundary", asString(show["boundary"]))
	if consumed["error"] != nil {
		t.Fatalf("consume %v", consumed)
	}
	after := lab.wakeRow(t)
	if after["outstanding"] != false {
		t.Fatalf("episode still outstanding after consume: %v", after)
	}
	if n := lab.openObligations(t, lab.taskIDs()); n != openBefore {
		t.Fatalf("consume changed open obligations %d -> %d; consumption must not answer, verify or close anything", openBefore, n)
	}
	if again := lab.ctl(false, "wake", "consume", "--boundary", asString(show["boundary"])); again["error"] == nil && asString(again["result"]) != "repeated" && again["repeated"] != true {
		t.Logf("second consume of the same boundary: %v", again)
	}
	m.record("consume", map[string]any{"included": len(asSlice(show["included"])), "omitted": len(asSlice(show["omitted"])), "result": consumed["result"], "open_obligations": openBefore})

	// The answer reaches the worker; the worker applies it.
	var questionID string
	for _, raw := range asSlice(lab.ctl(true, "show", standalone.id)["questions"]) {
		if asString(asMap(raw)["status"]) == "open" {
			questionID = asString(asMap(raw)["id"])
		}
	}
	if questionID == "" {
		t.Fatal("no open question recorded for the standalone task")
	}
	lab.paneStatus(t, standalone.pane, "idle") // the worker finished its turn and waits for the answer
	workerPromptsBefore := lab.promptsTo(standalone.pane)
	lab.ctl(true, "answer", standalone.id, questionID, "--text", "Yes, keep it.")
	if n := lab.promptsTo(standalone.pane); n != workerPromptsBefore+1 {
		t.Fatalf("worker prompts after the answer = %d, want one more than before (%d)", n, workerPromptsBefore)
	}
	lab.ctlPane(standalone.pane, true, "resolve", standalone.id, questionID)
	if grouped := lab.ctl(true, "inbox", "--grouped"); countDecisions(grouped) != 0 {
		t.Fatalf("an answered and applied question still counts as a decision: %v", grouped["counts"])
	}

	// --- #242: lane A progresses; the digest labels each step without performing it ---------------------------------
	digest := lab.digest(t, "factory status", "--project", "a/repo")
	if asString(digest.row["current_issue"]) != "41" || asString(digest.row["current_task"]) != laneA.id || !digest.has("reported") {
		t.Fatalf("lane A digest after the report: %v", digest.row)
	}
	// Cursors are bound to the command that issued them (a foreign scope resyncs), so each digest view keeps its own.
	groupedDigest := lab.digest(t, "status --grouped", "--project", "a/repo")
	if !groupedDigest.has("reported") || asString(groupedDigest.row["current_task"]) != laneA.id {
		t.Fatalf("grouped digest after the report: %v", groupedDigest.row)
	}
	if foreign := lab.digest(t, "status --grouped", "--project", "a/repo", "--since", digest.cursor); asMap(foreign.view["digest"])["resync"] == nil {
		t.Fatalf("a factory-status cursor on the grouped read must resync, got %v", asMap(foreign.view["digest"])["resync"])
	}
	steps := []struct {
		name    string
		run     func()
		outcome string
	}{
		{"verify", func() {
			out := lab.ctl(true, "verify", laneA.id, "--candidate", candidate, "--execute")
			if asString(asMap(out["evidence"])["result"]) != "pass" {
				t.Fatalf("coordinator verify %v", out)
			}
		}, "verified"},
		{"review", func() {
			lab.ctlPane("w-review:p1", true, "review", laneA.id, "--verdict", "approve", "--candidate", candidate, "--policy-reviewed", "--text", "Independent review of the replay candidate.")
		}, "review-accepted"},
		{"pipeline pr", func() {
			lab.ctl(true, "pipeline", "document", laneA.id)
			lab.ctl(true, "pipeline", "rebase", laneA.id)
			lab.writeGH("github.json", map[string]any{"version": "2.100.0", "repository": "a/repo", "visibility": "PUBLIC", "viewer_permission": "WRITE", "head_sha": candidate, "next_number": 8})
			pushed := lab.ctl(true, "pipeline", "push", laneA.id)
			if o := asString(asMap(pushed["evidence"])["outcome"]); o != "pushed" && o != "already" {
				t.Fatalf("push %v", pushed)
			}
			pr := lab.ctl(true, "pipeline", "pr", laneA.id)
			if asString(asMap(pr["pr"])["url"]) != "https://github.com/a/repo/pull/8" {
				t.Fatalf("pipeline pr %v", pr)
			}
		}, "pr-open"},
	}
	for _, step := range steps {
		ghBefore := lab.ghCalls()
		verbsBefore := lab.ghVerbs(t)
		step.run()
		if step.name == "pipeline pr" {
			// #237: the PR opens as a draft and is promoted once the gates settle; that promotion is the only
			// `pr ready` in the whole replay and belongs to this explicitly driven step.
			verbsAfter := lab.ghVerbs(t)
			if verbsAfter["pr create"]-verbsBefore["pr create"] != 1 || verbsAfter["pr ready"]-verbsBefore["pr ready"] != 1 {
				t.Fatalf("pipeline pr gh verbs: before %v after %v, want one create and one ready promotion", verbsBefore, verbsAfter)
			}
		} else if lab.ghCalls() != ghBefore {
			t.Fatalf("%s called gh %d times; only the PR step may", step.name, lab.ghCalls()-ghBefore)
		}
		next := lab.digest(t, "factory status", "--project", "a/repo", "--since", digest.cursor)
		if !next.isNew(step.outcome) {
			t.Fatalf("after %s the digest does not label %q as new: %v", step.name, step.outcome, next.row["outcomes"])
		}
		grouped := lab.digest(t, "status --grouped", "--project", "a/repo", "--since", groupedDigest.cursor)
		if !grouped.isNew(step.outcome) {
			t.Fatalf("after %s the grouped digest does not label %q as new: %v", step.name, step.outcome, grouped.row["outcomes"])
		}
		m.record("lane-a/"+step.name, map[string]any{"outcome": step.outcome, "gh_calls": lab.ghCalls() - ghBefore, "digest_bytes": next.bytes, "grouped_bytes": grouped.bytes})
		digest, groupedDigest = next, grouped
	}
	// Merging stays with the user: a/repo is outside the standing authorization, so factory merge is refused and no
	// gh mutation follows; lane B is human-gate as well and keeps its lane.
	ghBefore := lab.ghCalls()
	if check := lab.ctl(true, "factory", "merge-check", laneA.id); asString(check["confidence"]) != "human-gate" {
		t.Fatalf("merge-check a/repo = %v, want human-gate outside the standing authorization", check["confidence"])
	}
	if merge := lab.ctl(false, "factory", "merge", laneA.id); merge["error"] == nil {
		t.Fatalf("factory merge on a human-gate task succeeded: %v", merge)
	}
	if check := lab.ctl(true, "factory", "merge-check", laneB.id); asString(check["confidence"]) != "human-gate" {
		t.Fatalf("merge-check b/repo = %v", check["confidence"])
	}
	released := lab.ctl(true, "factory", "release", "b/repo", "--issue", "52", "--reason", "gated")
	if released["freed"] != false {
		t.Fatalf("a gated release freed the lane: %v", released)
	}
	if lab.ghCalls() != ghBefore {
		t.Fatalf("merge-check, refused merge and gated release called gh %d times", lab.ghCalls()-ghBefore)
	}
	lab.readPhase(t, "reads-with-open-pr")

	// The user merges on GitHub (scenario data); sum observes it, then cleans up with native operations.
	github := lab.readGH(t, "github.json")
	pr := asMap(github["pr"])
	pr["state"], pr["merged_at"], pr["merge_commit"] = "MERGED", "2026-09-26T12:00:00Z", strings.Repeat("b", 40)
	lab.writeGH("github.json", github)
	reconciled := lab.ctl(true, "pr", "reconcile", laneA.id, "--number", "8")
	if asMap(reconciled["pr"])["merged_for_task"] != true && asString(asMap(reconciled["pr"])["state"]) != "merged" {
		t.Fatalf("reconcile %v", reconciled)
	}
	next := lab.digest(t, "factory status", "--project", "a/repo", "--since", digest.cursor)
	if !next.isNew("observed-merged") || !next.isNew("cleanup-pending") {
		t.Fatalf("digest after the merged observation: %v", next.row["outcomes"])
	}
	digest = next
	lab.agentExited(t, laneA.pane)
	lab.agentExited(t, "w-review:p1")
	cleaned := lab.ctl(true, "cleanup", laneA.id, "--apply")
	if asString(cleaned["state"]) != "complete" {
		t.Fatalf("cleanup %v", cleaned)
	}
	if out, err := exec.Command("git", "-C", laneA.project.repo, "show-ref", "--verify", "--quiet", "refs/heads/"+lab.branchOf(t, laneA.id)).CombinedOutput(); err != nil {
		t.Fatalf("task branch gone after cleanup: %v %s", err, out)
	}
	lab.ctl(true, "factory", "release", "a/repo", "--issue", "41", "--reason", "merged")
	next = lab.digest(t, "factory status", "--project", "a/repo", "--since", digest.cursor)
	if !next.isNew("lane-free") || asString(next.row["lane"]) == "held" {
		t.Fatalf("digest after cleanup and release: lane=%v outcomes=%v", next.row["lane"], next.row["outcomes"])
	}
	laneRow := lab.digest(t, "factory status", "--project", "b/repo").row
	if asString(laneRow["current_issue"]) != "52" || asString(laneRow["lane"]) != "held" {
		t.Fatalf("lane B must stay occupied by its gated issue: %v", laneRow)
	}
	if ticks := tickActions(lab.ctl(true, "factory", "tick", "--project", "b/repo")); ticks["b/repo"] != "occupied" {
		t.Fatalf("tick b/repo %v", ticks)
	}
	settings := lab.ctl(true, "settings", "show")
	if asMap(asMap(settings["occupied"])["by_repository"]) == nil || asMap(settings["limits"]) == nil {
		t.Fatalf("settings after cleanup %v", settings)
	}
	m.record("lane-a/close", map[string]any{"cleanup_state": cleaned["state"], "gh_calls_total": lab.ghCalls(), "occupied": settings["occupied"]})
	lab.readPhase(t, "reads-after-cleanup")

	// --- Fallbacks: hook off, metadata off, native capability absent ---------------------------------------------------
	lab.ctl(true, "hook", "disable")
	lab.ctl(true, "metadata", "disable")
	lab.setEnv("FAKE_NO_METADATA", "1")
	lab.coordinatorStatus(t, "idle")
	fallbackTask := lab.tasks[5]
	before := lab.promptsTo(lab.coordinatorPane)
	report := lab.ctlPane(fallbackTask.pane, true, "report", fallbackTask.id, "--text", "Second report after the fallbacks were switched off.")
	if report["error"] != nil {
		t.Fatalf("fallback report %v", report)
	}
	if n := lab.promptsTo(lab.coordinatorPane); n != before+1 {
		t.Fatalf("fallback delivery prompts = %d, want one new routine prompt through the explicit pass (before %d)", n, before)
	}
	fallbackWake := lab.wakeRow(t)
	if fallbackWake["outstanding"] != true {
		t.Fatalf("fallback wake row %v", fallbackWake)
	}
	hookStatus := lab.ctl(true, "hook", "status")
	metaStatus := lab.ctl(false, "metadata", "status")
	if hookStatus["enabled"] == true {
		t.Fatalf("hook status after disable %v", hookStatus)
	}
	lab.coordinatorStatus(t, "working")
	m.record("fallback", map[string]any{"prompts_added": 1, "hook": hookStatus["enabled"], "metadata": metaStatus, "wake_outstanding": fallbackWake["outstanding"],
		"note": "older helpers on these records: TestOlderHelpersOnCandidateRecords (not rebuilt here)"})
	lab.readPhase(t, "reads-fallback")

	// --- Remote mutation audit ---------------------------------------------------------------------------------------
	verbs := lab.ghVerbs(t)
	for verb := range verbs {
		switch verb {
		case "issue list", "issue edit", "issue comment", "repo view", "pr list", "pr create", "pr view", "pr checks", "pr ready", "auth status", "--version", "pr edit --help":
		default:
			t.Fatalf("fake gh saw an unexpected verb %q: %v", verb, verbs)
		}
	}
	if verbs["pr create"] != 1 || verbs["pr ready"] != 1 || verbs["pr merge"] != 0 || verbs["pr edit"] != 0 || verbs["issue edit"] != 2 || verbs["issue comment"] != 2 {
		t.Fatalf("gh workflow verbs %v, want one create, one draft promotion, two claims, and no merge or edit", verbs)
	}
	m.record("gh_verbs", verbs)
	m.record("herdr", map[string]any{"calls": lab.herdrCalls(), "prompts_coordinator": lab.promptsTo(lab.coordinatorPane), "prompts_workers": lab.promptsToWorkers(), "prompt_log": lab.promptLog()})
	m.record("not_run", map[string]any{"native_canary": "not-run: no real Herdr session or agent harness in this lab; simulated output proves no narration compliance",
		"task_reads":     "unavailable: not strace-instrumented here; TestMeasureCoordinationPasses counts them under SUM_MEASURE_OUT",
		"tokens_latency": "not measured; stdout bytes and call counts only"})
	m.write(t, lab.base)
}

// ---------------------------------------------------------------------------------------------------------------------

type replayProject struct {
	spec, name, repo, origin, baseSHA string
}

type replayTask struct {
	id, pane, worktree string
	project            replayProject
}

type replayLab struct {
	*demoLab
	coordinatorPane string
	brief, pluginID string
	projects        []replayProject
	tasks           []replayTask
	measure         *replayMeasure
}

func newReplayLab(t *testing.T) *replayLab {
	t.Helper()
	root, helper := repoReference(t)
	base := t.TempDir()
	home := filepath.Join(base, "state")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte("{\"schema\": 1, \"sum_version\": \"0.1.0\", \"created_at\": \"2026-09-05T00:00:00+00:00\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(base, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "tests", "fixtures", "mise.py"), filepath.Join(bin, "mise")); err != nil {
		t.Fatal(err)
	}
	// One gh on the PATH of the whole replay: factory verbs go to the factory fake, publication verbs to the
	// attach fake; both log every call into the same FAKE_GH_ROOT/calls.jsonl.
	gh := filepath.Join(bin, "gh")
	wrapper := "#!/bin/sh\ncase \"$1\" in\n  issue|project) exec python3 " + filepath.Join(root, "go", "internal", "factory", "testdata", "fake_gh.py") + " \"$@\" ;;\n  *) exec python3 " + filepath.Join(root, "tests", "fixtures", "gh_attach.py") + " \"$@\" ;;\nesac\n"
	if err := os.WriteFile(gh, []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(base, "brief.md")
	if err := os.WriteFile(brief, []byte("Add a note and verify the greeting fixture. Ask before changing punctuation.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lab := &replayLab{
		demoLab:         &demoLab{t: t, root: root, helper: helper, home: home, base: base, env: replayEnv(t, root, base, bin, gh)},
		coordinatorPane: "w-parent:p1",
		brief:           brief,
		measure:         &replayMeasure{sections: map[string]any{}, order: []string{}},
	}
	for _, p := range []struct{ spec, name, slug string }{
		{"a/repo", "a/repo", "a-repo"},
		{"b/repo", "b/repo", "b-repo"},
		{"ghe.example.com/b/repo", "ghe.example.com/b/repo", "ghe-b-repo"},
	} {
		lab.projects = append(lab.projects, replayProjectRepo(t, root, base, p.spec, p.name, p.slug))
	}
	return lab
}

func replayEnv(t *testing.T, root, base, bin, gh string) []string {
	t.Helper()
	env := demoEnv(t, root, base)
	pythonDir := filepath.Dir(mustLookPath(t, "python3"))
	path := strings.Join([]string{bin, pythonDir, os.Getenv("PATH")}, string(os.PathListSeparator))
	if goroot := os.Getenv("GOROOT"); goroot != "" {
		path = strings.Join([]string{bin, pythonDir, filepath.Join(goroot, "bin"), os.Getenv("PATH")}, string(os.PathListSeparator))
	}
	filtered := env[:0]
	for _, e := range env {
		if !strings.HasPrefix(e, "PATH=") && !strings.HasPrefix(e, "SUM_GH_BIN=") {
			filtered = append(filtered, e)
		}
	}
	return append(filtered, "PATH="+path, "SUM_GH_BIN="+gh)
}

// replayProjectRepo makes one standardized project (the verify fixture) with a local bare origin so the pipeline can
// push, enrolled later under its canonical identity with that origin as the explicit remote.
func replayProjectRepo(t *testing.T, root, base, spec, name, slug string) replayProject {
	t.Helper()
	repo := filepath.Join(base, slug)
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "sum replay")
	git("config", "user.email", "replay@example.invalid")
	copyTree(t, filepath.Join(root, "tests", "fixtures", "verify", "cli"), repo)
	copyTree(t, filepath.Join(root, ".agents", "skills", "verify"), filepath.Join(repo, ".agents", "skills", "verify"))
	if err := os.WriteFile(filepath.Join(repo, "PROJECT.md"), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "standardized fixture for "+name)
	origin := filepath.Join(base, slug+"-origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", origin).CombinedOutput(); err != nil {
		t.Fatalf("bare origin: %v\n%s", err, out)
	}
	git("remote", "add", "origin", origin)
	git("push", "-q", "origin", "main")
	return replayProject{spec: spec, name: name, repo: repo, origin: origin, baseSHA: git("rev-parse", "HEAD")}
}

// coordinatorStatus is scenario data: what Herdr reports for the coordinator pane from now on. The fake refreshes the
// scripted root pane only on a call made from that pane, so one observation from it records the new status.
func (l *replayLab) coordinatorStatus(t *testing.T, status string) {
	t.Helper()
	l.setEnv("FAKE_PARENT_STATUS", status)
	cmd := exec.Command(filepath.Join(l.root, "tests", "fixtures", "herdr.py"), "--session", "sum-test", "pane", "get", l.coordinatorPane)
	cmd.Env = l.env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fake herdr pane get: %v\n%s", err, out)
	}
}

// idleEdges replays the coordinator finishing turns: each idle edge is the hook event Herdr would send, which runs
// one delivery pass for the coordinator. A legacy coordinator is prompted again on each; an adopted one is not.
func (l *replayLab) idleEdges(t *testing.T, n int) []map[string]any {
	t.Helper()
	var rows []map[string]any
	for i := 0; i < n; i++ {
		l.setEnv("FAKE_PARENT_STATUS", "idle")
		rows = append(rows, l.herdrEvent(l.pluginID, l.coordinatorPane, "idle", "sum-test"))
	}
	return rows
}

func (l *replayLab) taskIDs() []string {
	ids := make([]string, 0, len(l.tasks))
	for _, task := range l.tasks {
		ids = append(ids, task.id)
	}
	return ids
}

func (l *replayLab) ghRoot() string    { return filepath.Join(l.base, "fake-gh") }
func (l *replayLab) herdrRoot() string { return filepath.Join(l.base, "fake") }
func (l *replayLab) ghCalls() int      { return measureLines(filepath.Join(l.ghRoot(), "calls.jsonl")) }
func (l *replayLab) herdrCalls() int {
	return measureLines(filepath.Join(l.herdrRoot(), "calls.jsonl"))
}

func (l *replayLab) writeGH(name string, value any) {
	l.t.Helper()
	if err := os.MkdirAll(l.ghRoot(), 0o700); err != nil {
		l.t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.ghRoot(), name), raw, 0o600); err != nil {
		l.t.Fatal(err)
	}
}

func (l *replayLab) readGH(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(l.ghRoot(), name))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// herdrPrompts returns every `agent prompt` the fake Herdr logged, as pane ids in order.
func (l *replayLab) herdrPrompts() []string {
	var panes []string
	for _, p := range l.promptLog() {
		panes = append(panes, p["pane"])
	}
	return panes
}

// promptLog is every prompt submission with the head of its message, the evidence behind every prompt count.
func (l *replayLab) promptLog() []map[string]string {
	raw, err := os.ReadFile(filepath.Join(l.herdrRoot(), "calls.jsonl"))
	if err != nil {
		return nil
	}
	var out []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var call struct {
			Args []string `json:"args"`
		}
		if json.Unmarshal([]byte(line), &call) != nil || len(call.Args) < 4 {
			continue
		}
		if call.Args[0] == "agent" && call.Args[1] == "prompt" {
			head := call.Args[3]
			if len(head) > 90 {
				head = head[:90] + "..."
			}
			out = append(out, map[string]string{"pane": call.Args[2], "message": head})
		}
	}
	return out
}

func (l *replayLab) promptsTo(pane string) int {
	n := 0
	for _, p := range l.herdrPrompts() {
		if p == pane {
			n++
		}
	}
	return n
}

func (l *replayLab) promptsToWorkers() int {
	return len(l.herdrPrompts()) - l.promptsTo(l.coordinatorPane)
}

// ghVerbs counts the fake gh calls by their first two words (`pr create`, `issue list`, ...).
func (l *replayLab) ghVerbs(t *testing.T) map[string]int {
	t.Helper()
	out := map[string]int{}
	raw, err := os.ReadFile(filepath.Join(l.ghRoot(), "calls.jsonl"))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var call struct {
			Args []string `json:"args"`
		}
		if json.Unmarshal([]byte(line), &call) != nil || call.Args == nil {
			continue // the attach fake's `created` audit line
		}
		verb := strings.Join(call.Args[:min(2, len(call.Args))], " ")
		if len(call.Args) >= 3 && call.Args[0] == "pr" && call.Args[1] == "edit" && call.Args[2] == "--help" {
			verb = "pr edit --help"
		}
		out[verb]++
	}
	return out
}

func (l *replayLab) owner(t *testing.T) *ordjson.Object {
	t.Helper()
	st, err := store.Open(l.home)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := st.Owner()
	if err != nil || owner == nil {
		t.Fatalf("owner %v %v", owner, err)
	}
	return owner
}

func ownerField(owner *ordjson.Object, key string) string {
	v, ok := owner.Get(key)
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func (l *replayLab) openObligations(t *testing.T, ids []string) int {
	t.Helper()
	st, err := store.Open(l.home)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, id := range ids {
		task, err := st.ReadTask(id)
		if err != nil {
			t.Fatal(err)
		}
		obligations, err := returns.OpenObligations(st, task)
		if err != nil {
			t.Fatal(err)
		}
		n += len(obligations)
	}
	return n
}

func (l *replayLab) wakeSidecarExists() bool {
	matches, _ := filepath.Glob(filepath.Join(l.home, "*"+store.WakeSuffix))
	if len(matches) > 0 {
		return true
	}
	matches, _ = filepath.Glob(filepath.Join(l.home, "*", "*"+store.WakeSuffix))
	return len(matches) > 0
}

func (l *replayLab) wakeRow(t *testing.T) map[string]any {
	t.Helper()
	view := l.ctl(true, "wake", "show")
	rows := asSlice(view["recipients"])
	for _, raw := range rows {
		row := asMap(raw)
		if asString(asMap(row["recipient"])["pane"]) == l.coordinatorPane {
			return row
		}
	}
	if len(rows) == 1 {
		return asMap(rows[0])
	}
	t.Fatalf("wake show has no row for %s: %v", l.coordinatorPane, view)
	return nil
}

func (l *replayLab) branchOf(t *testing.T, id string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(l.home, "tasks", id, "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	var task map[string]any
	if err := json.Unmarshal(raw, &task); err != nil {
		t.Fatal(err)
	}
	return asString(task["branch"])
}

func (l *replayLab) commit(task replayTask, name, text string) string {
	l.t.Helper()
	if err := os.WriteFile(filepath.Join(task.worktree, name), []byte(text), 0o644); err != nil {
		l.t.Fatal(err)
	}
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", task.worktree}, args...)...).CombinedOutput()
		if err != nil {
			l.t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("-c", "user.name=sum replay", "-c", "user.email=replay@example.invalid", "add", name)
	git("-c", "user.name=sum replay", "-c", "user.email=replay@example.invalid", "commit", "-q", "-m", "change "+name)
	return git("rev-parse", "HEAD")
}

// workerHandoff runs the project's own verify runner in the worker checkout and writes the handoff the worker reports.
func (l *replayLab) workerHandoff(task replayTask, candidate string) string {
	l.t.Helper()
	cmd := exec.Command(mustLookPath(l.t, "python3"), filepath.Join(task.worktree, verifyRunner), "--json", "--base", task.project.baseSHA)
	cmd.Dir = task.worktree
	cmd.Env = l.env
	out, err := cmd.Output()
	if err != nil {
		stderr := []byte{}
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		l.t.Fatalf("worker run: %v\n%s%s", err, out, stderr)
	}
	var run map[string]any
	if err := json.Unmarshal(out, &run); err != nil {
		l.t.Fatalf("worker run json: %v\n%s", err, out)
	}
	record := filepath.Join(task.worktree, asString(asMap(run["artifacts"])["run_dir"]), "run.json")
	handoff := filepath.Join(l.base, "handoff-"+task.id+".json")
	body, err := json.Marshal(map[string]any{
		"outcome": "done", "candidate": candidate, "next_action": "review",
		"verification": map[string]any{
			"run_id": run["run_id"], "outcome": run["outcome"], "record": record, "candidate": candidate,
			"certifies": run["certifies"], "requires_root_review": run["requires_root_review"],
			"contract_sha256": asMap(run["contract"])["sha256"],
		},
	})
	if err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(handoff, body, 0o644); err != nil {
		l.t.Fatal(err)
	}
	return handoff
}

// setScreen plants what `agent read` shows for a worker pane, so a blocked edge has an excerpt.
func (l *replayLab) setScreen(t *testing.T, pane, text string) {
	t.Helper()
	l.editFakePane(t, pane, func(row map[string]any) { row["screen"] = text })
}

// paneStatus is scenario data for a pane the fake created (a worker or reviewer): what Herdr reports for its agent.
func (l *replayLab) paneStatus(t *testing.T, pane, status string) {
	t.Helper()
	l.editFakePane(t, pane, func(row map[string]any) { row["agent_status"] = status })
}

func (l *replayLab) editFakePane(t *testing.T, pane string, change func(row map[string]any)) {
	t.Helper()
	path := filepath.Join(l.herdrRoot(), "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	row := asMap(asMap(state["panes"])[pane])
	if row == nil {
		t.Fatalf("no fake pane %s", pane)
	}
	change(row)
	out, _ := json.Marshal(state)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// agentExited is scenario data: the pane's harness (worker or reviewer) has ended and only its shell remains.
func (l *replayLab) agentExited(t *testing.T, pane string) {
	t.Helper()
	l.editFakePane(t, pane, func(row map[string]any) {
		row["agent"] = nil
		row["agent_status"] = "unknown"
		delete(row, "agent_pid")
		delete(row, "name")
		row["processes"] = []any{}
	})
}

type burstResult struct {
	submitted, coalesced, deferred int
	other                          map[string]int
	lockWaitMS, maxLockWaitMS      int // summed and largest recipient-lock wait the report passes reported in their fanout
	herdrCalls                     int
}

func (b burstResult) add(o burstResult) burstResult {
	b.submitted += o.submitted
	b.coalesced += o.coalesced
	b.deferred += o.deferred
	b.lockWaitMS += o.lockWaitMS
	b.herdrCalls += o.herdrCalls
	if o.maxLockWaitMS > b.maxLockWaitMS {
		b.maxLockWaitMS = o.maxLockWaitMS
	}
	for k, v := range o.other {
		b.other[k] += v
	}
	return b
}

// arrivals is the AE1 shape across processes and passes: six concurrent reports, a coordinator idle edge, six more
// concurrent reports, then two more idle edges. The same sequence runs on the candidate and the baseline.
func (l *replayLab) arrivals(t *testing.T, handoffs map[string]string) (burstResult, []map[string]any) {
	t.Helper()
	l.coordinatorStatus(t, "idle")
	result := l.burst(t, l.tasks[:6], handoffs)
	edges := l.idleEdges(t, 1)
	l.coordinatorStatus(t, "idle")
	result = result.add(l.burst(t, l.tasks[6:], handoffs))
	edges = append(edges, l.idleEdges(t, 2)...)
	return result, edges
}

// burst submits the twelve workers' reports from twelve helper processes started together. Each report's own
// notice row says what its pass did for the coordinator; the fake Herdr log is the arbiter of prompts.
func (l *replayLab) burst(t *testing.T, tasks []replayTask, handoffs map[string]string) burstResult {
	t.Helper()
	results := make([]map[string]any, len(tasks))
	var wg sync.WaitGroup
	for i, task := range tasks {
		wg.Add(1)
		go func(i int, task replayTask) {
			defer wg.Done()
			args := []string{"report", task.id, "--text", fmt.Sprintf("Worker for %s finished its slice.", task.project.name)}
			if handoff := handoffs[task.id]; handoff != "" {
				args = append(args, "--handoff", handoff)
			}
			results[i] = l.ctlPaneNoFatal(task.pane, args...)
		}(i, task)
	}
	wg.Wait()
	out := burstResult{other: map[string]int{}}
	for i, result := range results {
		if result["error"] != nil {
			t.Fatalf("report %d (%s): %v", i, tasks[i].id, result["error"])
		}
		pass := asMap(asMap(result["notice"])["returns"])
		row := recipientRow(pass, l.coordinatorPane)
		if fanout := asMap(pass["fanout"]); fanout != nil {
			wait, _ := fanout["lock_wait_ms"].(float64)
			calls, _ := fanout["herdr_calls"].(float64)
			out.lockWaitMS += int(wait)
			out.herdrCalls += int(calls)
			if int(wait) > out.maxLockWaitMS {
				out.maxLockWaitMS = int(wait)
			}
		}
		switch state := asString(row["state"]); state {
		case "submitted":
			out.submitted++
		case "coalesced":
			out.coalesced++
		case "deferred":
			out.deferred++
		default:
			out.other[state]++
		}
	}
	return out
}

// ctlPaneNoFatal is ctlPane for goroutines: a failure is returned as an error row instead of ending the test.
func (l *replayLab) ctlPaneNoFatal(pane string, args ...string) map[string]any {
	cmd := exec.Command(l.helper, append([]string{"--format", "json", "--home", l.home}, args...)...)
	cmd.Env = append(append([]string{}, l.env...), "HERDR_PANE_ID="+pane)
	out, err := cmd.Output()
	var view map[string]any
	if jsonErr := json.Unmarshal(out, &view); jsonErr != nil {
		detail := string(out)
		if ee, ok := err.(*exec.ExitError); ok {
			detail += string(ee.Stderr)
		}
		return map[string]any{"error": strings.TrimSpace(detail)}
	}
	if err != nil {
		view["error"] = err.Error()
	}
	return view
}

func recipientRow(view map[string]any, pane string) map[string]any {
	for _, raw := range asSlice(view["recipients"]) {
		row := asMap(raw)
		if asString(asMap(row["recipient"])["pane"]) == pane {
			return row
		}
	}
	return map[string]any{}
}

func tickActions(view map[string]any) map[string]string {
	out := map[string]string{}
	for _, raw := range asSlice(view["ticks"]) {
		row := asMap(raw)
		out[asString(row["name"])] = asString(row["action"])
	}
	return out
}

func countDecisions(view map[string]any) int {
	counts := asMap(view["counts"])
	if counts == nil {
		return -1
	}
	n, _ := counts["decisions"].(float64)
	return int(n)
}

// snapshotForBaseline copies the whole lab (home, fakes, repos) so the baseline burst runs on identical inputs; the
// copy's owner record then loses wake_protocol, exactly what an older helper's coordinator looks like.
func (l *replayLab) snapshotForBaseline(t *testing.T) *replayLab {
	t.Helper()
	base := filepath.Join(t.TempDir(), "baseline")
	if out, err := exec.Command("cp", "-a", l.base, base).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v\n%s", err, out)
	}
	copy := &replayLab{
		demoLab:         &demoLab{t: t, root: l.root, helper: l.helper, home: filepath.Join(base, "state"), base: base, env: replayEnv(t, l.root, base, filepath.Join(base, "bin"), filepath.Join(base, "bin", "gh"))},
		coordinatorPane: l.coordinatorPane, brief: l.brief, pluginID: l.pluginID, projects: l.projects, tasks: l.tasks,
		measure: &replayMeasure{sections: map[string]any{}},
	}
	owner := copy.owner(t)
	owner.Delete("wake_protocol")
	if err := ordjson.WriteFile(filepath.Join(copy.home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	return copy
}

// --- read purity ------------------------------------------------------------------------------------------------------

var replayReads = [][]string{
	{"status"}, {"inbox"}, {"status", "--grouped"}, {"inbox", "--compact"}, {"wake", "show"}, {"factory", "status"},
	{"status", "--grouped", "--project", "a/repo"}, {"factory", "status", "--project", "b/repo"},
}

// readPhase runs every coordinator-facing read and proves it wrote nothing under the home, called gh not at all and
// prompted nothing through Herdr; it records the structured stdout bytes of each command.
func (l *replayLab) readPhase(t *testing.T, name string) {
	t.Helper()
	before := hashTree(t, l.home)
	ghBefore, herdrBefore, promptsBefore := l.ghCalls(), l.herdrCalls(), len(l.herdrPrompts())
	bytes := map[string]int{}
	reads := append([][]string{}, replayReads...)
	if len(l.tasks) > 0 {
		reads = append(reads, []string{"context", l.tasks[0].id, "--role", "coordinator"})
	}
	for _, args := range reads {
		cmd := exec.Command(l.helper, append([]string{"--format", "json", "--home", l.home}, args...)...)
		cmd.Env = l.env
		out, err := cmd.Output()
		if err != nil {
			detail := ""
			if ee, ok := err.(*exec.ExitError); ok {
				detail = string(ee.Stderr)
			}
			t.Fatalf("%s: %v failed: %v\n%s", name, args, err, detail)
		}
		bytes[strings.Join(args, " ")] = len(out)
	}
	if after := hashTree(t, l.home); !reflect.DeepEqual(before, after) {
		t.Fatalf("%s: read-only commands changed the home: %v", name, treeDiff(before, after))
	}
	if l.ghCalls() != ghBefore {
		t.Fatalf("%s: reads called gh %d times", name, l.ghCalls()-ghBefore)
	}
	if len(l.herdrPrompts()) != promptsBefore {
		t.Fatalf("%s: reads prompted through Herdr", name)
	}
	l.measure.record("reads/"+name, map[string]any{"stdout_bytes": bytes, "herdr_calls": l.herdrCalls() - herdrBefore, "gh_calls": 0, "home_changed": false})
}

func hashTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if info.IsDir() {
			out[rel+"/"] = "dir"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func treeDiff(before, after map[string]string) []string {
	var diff []string
	for k, v := range after {
		if before[k] != v {
			diff = append(diff, k)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			diff = append(diff, "-"+k)
		}
	}
	sort.Strings(diff)
	return diff
}

// --- digests ------------------------------------------------------------------------------------------------------------

type digestView struct {
	row    map[string]any
	cursor string
	bytes  int
	view   map[string]any
}

func (d digestView) has(kind string) bool {
	for _, raw := range asSlice(d.row["outcomes"]) {
		if asString(asMap(raw)["kind"]) == kind {
			return true
		}
	}
	return false
}

func (d digestView) isNew(kind string) bool {
	for _, raw := range asSlice(d.row["outcomes"]) {
		o := asMap(raw)
		if asString(o["kind"]) == kind && o["new"] == true {
			return true
		}
	}
	return false
}

// digest reads one project's factory digest through `factory status` or `status --grouped` and returns its row.
func (l *replayLab) digest(t *testing.T, command string, args ...string) digestView {
	t.Helper()
	argv := append(strings.Split(command, " "), args...)
	cmd := exec.Command(l.helper, append([]string{"--format", "json", "--home", l.home}, argv...)...)
	cmd.Env = l.env
	out, err := cmd.Output()
	if err != nil {
		detail := ""
		if ee, ok := err.(*exec.ExitError); ok {
			detail = string(ee.Stderr)
		}
		t.Fatalf("%v: %v\n%s", argv, err, detail)
	}
	var view map[string]any
	if err := json.Unmarshal(out, &view); err != nil {
		t.Fatalf("%v json: %v\n%s", argv, err, out)
	}
	digest := asMap(view["digest"])
	if digest == nil {
		t.Fatalf("%v has no digest: %v", argv, view)
	}
	rows := asSlice(digest["rows"])
	if len(rows) != 1 {
		t.Fatalf("%v: %d digest rows, want the one focused project: %v", argv, len(rows), digest)
	}
	return digestView{row: asMap(rows[0]), cursor: asString(digest["cursor"]), bytes: len(out), view: view}
}

// --- measurements --------------------------------------------------------------------------------------------------------

type replayMeasure struct {
	sections map[string]any
	order    []string
}

func (m *replayMeasure) record(name string, value any) {
	if _, seen := m.sections[name]; !seen {
		m.order = append(m.order, name)
	}
	m.sections[name] = value
}

func (m *replayMeasure) write(t *testing.T, base string) {
	t.Helper()
	doc := map[string]any{"schema": 1, "generated_at": time.Now().UTC().Format(time.RFC3339), "scenario": "12 workers / 3 projects / 2 factory lanes", "sections": m.sections}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dirs := []string{filepath.Join(base, "measure")}
	if out := os.Getenv("SUM_MEASURE_OUT"); out != "" {
		dirs = append(dirs, out)
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "replay.json"), append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	burst := asMap(m.sections["burst"])
	c, b := asMap(burst["candidate"]), asMap(burst["baseline"])
	t.Logf("replay measurements (recorded, not guarantees) -> %s", filepath.Join(dirs[len(dirs)-1], "replay.json"))
	t.Logf("| metric | baseline (not adopted) | candidate (adopted) |")
	t.Logf("| routine prompts across 12 arrivals (6 concurrent, idle edge, 6 concurrent, 2 idle edges), before consumption | %v | %v |", b["prompts"], c["prompts"])
	t.Logf("| submitted / coalesced / deferred / other | %v / %v / %v / %v | %v / %v / %v / %v |", b["submitted"], b["coalesced"], b["deferred"], b["other_states"], c["submitted"], c["coalesced"], c["deferred"], c["other_states"])
	t.Logf("| open obligations after the burst | %v | %v |", b["open_obligations"], c["open_obligations"])
	bp, cp := asMap(b["report_passes"]), asMap(c["report_passes"])
	t.Logf("| report passes: lock_wait_ms total / max / herdr calls | %v / %v / %v | %v / %v / %v |", bp["lock_wait_ms_total"], bp["lock_wait_ms_max"], bp["herdr_calls"], cp["lock_wait_ms_total"], cp["lock_wait_ms_max"], cp["herdr_calls"])
	pump := asMap(c["pump_after_wake"])
	t.Logf("| pump after the wake: prompts / state / deferred / lock_wait_ms / herdr calls | - | %v / %v / %v / %v / %v |", pump["prompts"], pump["state"], pump["deferred"], pump["lock_wait_ms"], pump["herdr_calls"])
	for _, name := range m.order {
		if strings.HasPrefix(name, "reads/") {
			section := asMap(m.sections[name])
			t.Logf("| %s stdout bytes | - | %v (herdr calls %v, gh calls %v) |", name, section["stdout_bytes"], section["herdr_calls"], section["gh_calls"])
		}
	}
	t.Logf("| gh verbs | - | %v |", m.sections["gh_verbs"])
	herdr := asMap(m.sections["herdr"])
	t.Logf("| herdr calls / prompts to the coordinator / prompts to workers | - | %v / %v / %v |", herdr["calls"], herdr["prompts_coordinator"], herdr["prompts_workers"])
	if log, ok := herdr["prompt_log"].([]map[string]string); ok {
		for _, p := range log {
			t.Logf("|   prompt -> %s | - | %s |", p["pane"], p["message"])
		}
	}
	t.Logf("| not run / unavailable | - | %v |", m.sections["not_run"])
}
