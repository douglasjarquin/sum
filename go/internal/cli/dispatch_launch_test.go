package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/prepare"
	"github.com/douglasjarquin/sum/go/internal/store"
)

var devinWorkerArgv = []string{"--permission-mode", "dangerous", "--respect-workspace-trust", "false"}

func herdrCalls(t *testing.T, base string) [][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(base, "fake", "calls.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var row struct {
			Args []string `json:"args"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		out = append(out, row.Args)
	}
	return out
}

func agentStartCalls(t *testing.T, base string) [][]string {
	t.Helper()
	var out [][]string
	for _, call := range herdrCalls(t, base) {
		if len(call) >= 2 && call[0] == "agent" && call[1] == "start" {
			out = append(out, call)
		}
	}
	return out
}

func startedArgv(task map[string]any) []string {
	var out []string
	for _, a := range asSlice(asMap(task["launch"])["started_argv"]) {
		out = append(out, asString(a))
	}
	return out
}

func argsAfterSeparator(call []string) ([]string, bool) {
	for i := len(call) - 1; i >= 0; i-- {
		if call[i] == "--" {
			return call[i+1:], true
		}
	}
	return nil, false
}

func TestDispatchDevinWorkerCarriesTrustGateArgv(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "devin-worker", map[string]string{"README.md": "x\n"})
	d.native.QueueFault("terminal.run", hctest.Fault{FirstMessage: &hcclient.FirstMessage{Sent: true}})
	d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "devin", "--arg=--permission-mode", "--arg=dangerous", "--arg=--respect-workspace-trust", "--arg=false", "--permission-override", "--approved")
	task := onlyNativeTask(t, d)
	var recorded []string
	for _, a := range asSlice(asMap(task["launch"])["argv"]) {
		recorded = append(recorded, asString(a))
	}
	if !reflect.DeepEqual(recorded, devinWorkerArgv) {
		t.Fatalf("recorded approved arguments %v", recorded)
	}
	if len(nativeCalls(d, "terminal.run")) != 1 || asMap(asMap(task["launch"])["permissions"])["override"] != true {
		t.Fatal("qualified launch did not retain the explicit permission authorization")
	}

}

func TestDispatchOtherHarnessHasNoDevinArgv(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "codex-worker", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if argv := startedArgv(task); len(argv) != 0 {
		t.Fatalf("Codex inherited unrelated permission arguments %v", argv)
	}
	calls := nativeCalls(d, "terminal.run")
	if len(calls) != 1 {
		t.Fatalf("native run calls %v", calls)
	}
	for _, arg := range asSlice(calls[0].Params["argv"]) {
		if strings.Contains(asString(arg), "dangerous") {
			t.Fatal("Devin-only flags reached Codex")
		}
	}
	if len(nativeCalls(d, "terminal.send")) != 0 {
		t.Fatal("first message was sent separately")
	}

}

func TestClaudeDispatchGrantsOnlyItsTaskInstructions(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "claude-task-access", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "claude", "--arg", "existing", "--approved")
	st := nativeTestStore(t, d.home)
	record, err := st.ReadTask(asString(task["id"]))
	if err != nil {
		t.Fatal(err)
	}
	args, err := prepare.TaskAccessArgs(st, record, "claude", asString(task["brief_path"]), asString(task["worktree"]))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--add-dir", filepath.Join(d.home, "tasks", asString(task["id"]))}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("task grant %v want %v", args, want)
	}
	for _, arg := range args {
		if arg == d.home || arg == filepath.Join(d.home, "tasks") || arg == filepath.Join(d.home, "tasks", "t-other") {
			t.Fatalf("statewide grant %v", args)
		}
	}
	d.native.QueueFault("terminal.run", hctest.Fault{FirstMessage: &hcclient.FirstMessage{Sent: true}})
	started := d.ctl(true, "start", asString(task["id"]))
	if len(nativeCalls(d, "terminal.run")) != 1 {
		t.Fatal("qualified Claude did not launch exactly once")
	}
	attempt, _ := launchedWorker(started)
	spec := asMap(asMap(asMap(attempt["native_launch"])["native_template"])["spec"])
	argv := anyStrings(asSlice(spec["argv"]))
	if grant, ok := flagAfter(argv, "--add-dir"); !ok || grant != want[1] || spec["submit"] != "enter" {
		t.Fatalf("Claude task-scoped launch contract changed: %v", spec)
	}
	d.ctlPane(workerAddress(started), true, "init")

}

func TestDispatchRecordsTheStableMachineIdentity(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "identity-worker", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	d.ctlPane(workerAddress(task), true, "init")
	task = d.taskRecord(asString(task["id"]))
	id, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	attempt, _ := launchedWorker(task)
	for name, recorded := range map[string]any{"task": task["machine"], "attempt owner": asMap(attempt["owner"])["machine"], "native endpoint": asMap(task["worker_endpoint"])["machine"]} {
		if recorded != id {
			t.Fatalf("%s machine %v want %s", name, recorded, id)
		}
	}

}

func launchedWorker(task map[string]any) (attempt, occupant map[string]any) {
	attempt = asMap(asMap(task["execution"])["worker"])
	return attempt, asMap(attempt["occupant"])
}

// settleFakeWorker returns the fake worker pane to idle, like a worker that
// finished its turn and waits for the next instruction.
func settleFakeWorker(t *testing.T, base, pane string) {
	t.Helper()
	path := filepath.Join(base, "fake", "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	asMap(asMap(state["panes"])[pane])["agent_status"] = "idle"
	out, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchBindsAgentThatStartsForegroundChildren(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "native-foreground", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	address := workerAddress(task)
	d.ctlPane(address, true, "init")
	task = d.taskRecord(asString(task["id"]))
	attempt, _ := launchedWorker(task)
	endpoint := store.EndpointFromContext(mapObject(t, asMap(task["worker_endpoint"])))
	st := nativeTestStore(t, d.home)
	reg, err := st.Registration(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	inc, _ := reg.Get("incarnation")
	raw, _ := ordjson.MarshalCompact(inc)
	var bound incarnation.NativeEvidence
	if err = json.Unmarshal(raw, &bound); err != nil {
		t.Fatal(err)
	}
	if attempt["state"] != "running" || bound.PID == 0 || bound.Foreground == nil || bound.Foreground.PID <= 0 {
		t.Fatalf("native leader was not receipt-bound: %v %s", attempt, raw)
	}
	d.native.SetStatus(address, "idle")
	sent := d.ctl(false, "repair", "send", asString(task["id"]), "--attempt", asString(attempt["id"]), "--key", "rebase", "--text", "Rebase onto main.")
	if msg := asString(sent["error"]); msg != "" {
		t.Fatalf("repair to receipt-bound native leader: %s", msg)
	}

}

func TestDispatchKeepsForegroundWithoutItsLeaderUncertain(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "missing-native-leader", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	address := workerAddress(task)
	for _, terminal := range d.native.Terminals() {
		if terminal.Address == address {
			pid := uint32(200000)
			terminal.PID = &pid
			terminal.Foreground = nil
			d.native.SetTerminal(terminal)
		}
	}
	refused := d.ctlPane(address, false, "init")
	if asString(refused["role"]) == "worker" {
		t.Fatalf("missing foreground leader adopted: %v", refused)
	}
	task = d.taskRecord(asString(task["id"]))
	attempt, _ := launchedWorker(task)
	if attempt["state"] == "running" {
		t.Fatal("missing leader marked running")
	}
	sent := d.ctl(false, "repair", "send", asString(task["id"]), "--attempt", asString(attempt["id"]), "--key", "rebase", "--text", "Rebase onto main.")
	if asString(sent["error"]) == "" {
		t.Fatal("repair to unverified bootstrap was accepted")
	}

}

func flagAfter(call []string, name string) (string, bool) {
	for i := 0; i < len(call)-1; i++ {
		if call[i] == name {
			return call[i+1], true
		}
	}
	return "", false
}

func TestDispatchGrokColdStartPreservesUnsentOpening(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "grok-cold-start", map[string]string{"README.md": "x\n"})
	d.native.QueueFault("terminal.run", hctest.Fault{Delay: 100 * time.Millisecond, FirstMessage: &hcclient.FirstMessage{Sent: false}})
	created := d.ctl(false, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "grok", "--approved")
	if !strings.Contains(asString(created["error"]), "without a submitted first message") || workerAddress(created) == "" || created["status"] != "needs-attention" {
		t.Fatalf("cold Grok lost its recoverable unsent opening: %v", created)
	}
	d.ctl(false, "start", asString(created["id"]))
	if len(nativeCalls(d, "terminal.run")) != 1 {
		t.Fatal("cold Grok was relaunched instead of preserving the original opening")
	}
}

func TestDispatchSwallowedBriefRecordsLaunchUncertain(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "lost-run-response", map[string]string{"README.md": "x\n"})
	d.native.QueueFault("terminal.run", hctest.Fault{LoseResponse: true})
	refused := d.ctl(false, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if !strings.Contains(asString(refused["error"]), "uncertain") {
		t.Fatalf("lost launch response %v", refused)
	}
	task := onlyNativeTask(t, d)
	attempt, _ := launchedWorker(task)
	if task["status"] != "needs-attention" || attempt["state"] != "uncertain" || asMap(attempt["native_launch"])["intent"] == nil {
		t.Fatalf("uncertainty missing %v", task)
	}
	if workerAddress(task) != "" {
		t.Fatal("lost response fabricated a terminal endpoint")
	}
	d.ctl(false, "start", asString(task["id"]))
	if len(nativeCalls(d, "terminal.run")) != 1 || len(nativeCalls(d, "terminal.send")) != 0 {
		t.Fatal("lost effect automatically replayed")
	}

}

func TestDispatchBlockedStartupNeverSubmitsTheBrief(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "blocked-worker", map[string]string{"README.md": "x\n"})
	reason := "blocked"
	d.native.QueueFault("terminal.run", hctest.Fault{FirstMessage: &hcclient.FirstMessage{Sent: false, Reason: &reason}})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if task["status"] != "needs-attention" {
		t.Fatalf("blocked initial message adopted: %v", task)
	}
	if len(nativeCalls(d, "terminal.send")) != 0 || len(nativeCalls(d, "terminal.run")) != 1 {
		t.Fatal("blocked startup received blind input or another process")
	}
	worker, _ := launchedWorker(task)
	if asMap(asMap(worker["native_launch"])["first_message"])["sent"] != false {
		t.Fatal("unsent initial message fact missing")
	}

}

func TestDispatchRetriesAgentPaneBusy(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "busy-native-run", map[string]string{"README.md": "x\n"})
	d.native.QueueFault("terminal.run", hctest.Fault{RefuseKind: "name_taken"})
	refused := d.ctl(false, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if asString(refused["error"]) == "" {
		t.Fatal("native refusal ignored")
	}
	task := onlyNativeTask(t, d)
	d.ctl(false, "start", asString(task["id"]))
	if len(nativeCalls(d, "terminal.run")) != 1 {
		t.Fatal("native creation refusal was automatically replayed")
	}

}

func TestDispatchBranchesFromTheFetchedBase(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "moved-base", map[string]string{"README.md": "x\n"})
	local := strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD"))
	writeFile(t, filepath.Join(repo, "ADVANCED.md"), "y\n")
	gitIn(t, repo, "add", "ADVANCED.md")
	gitIn(t, repo, "commit", "-q", "-m", "advance")
	gitIn(t, repo, "push", "-q", "origin", "main")
	remote := strings.TrimSpace(gitIn(t, repo, "rev-parse", "origin/main"))
	if remote == local {
		t.Fatal("origin did not move")
	}
	gitIn(t, repo, "reset", "--hard", "-q", local)
	gitIn(t, repo, "update-ref", "refs/remotes/origin/main", local)

	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if asString(task["base_sha"]) != remote {
		t.Fatalf("base_sha = %v, want the fetched remote tip %s", task["base_sha"], remote)
	}
	if head := strings.TrimSpace(gitIn(t, asString(task["worktree"]), "rev-parse", "HEAD")); head != remote {
		t.Fatalf("worktree HEAD = %s, want the fetched remote tip %s", head, remote)
	}
}

func TestDispatchRefusesABaseTheRemoteDoesNotAnswer(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "dead-remote", map[string]string{"README.md": "x\n"})
	gitIn(t, repo, "remote", "set-url", "origin", filepath.Join(d.base, "gone.git"))

	refused := d.ctl(false, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if msg := asString(refused["error"]); !strings.Contains(msg, "Could not fetch") {
		t.Fatalf("dispatch error = %q, want a fetch refusal", msg)
	}
	entries, err := os.ReadDir(filepath.Join(d.home, "tasks"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a refused dispatch left task records: %v", entries)
	}
	if len(nativeCalls(d, "terminal.run")) != 0 {
		t.Fatal("refused dispatch still launched a native terminal")
	}
	assertPrimaryCheckoutsOnly(t, d)
}

func TestDispatchHonorsAnExplicitBaseRef(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "stacked", map[string]string{"README.md": "x\n"})
	gitIn(t, repo, "checkout", "-q", "-b", "feature")
	writeFile(t, filepath.Join(repo, "FEATURE.md"), "f\n")
	gitIn(t, repo, "add", "FEATURE.md")
	gitIn(t, repo, "commit", "-q", "-m", "feature")
	gitIn(t, repo, "push", "-q", "origin", "feature")
	feature := strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD"))
	gitIn(t, repo, "checkout", "-q", "main")

	task := d.ctl(true, "dispatch", "--repo", repo, "--base", "feature", "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if asString(task["base_sha"]) != feature {
		t.Fatalf("base_sha = %v, want the fetched feature tip %s", task["base_sha"], feature)
	}
	if head := strings.TrimSpace(gitIn(t, asString(task["worktree"]), "rev-parse", "HEAD")); head != feature {
		t.Fatalf("worktree HEAD = %s, want %s", head, feature)
	}
	if asString(task["base_branch"]) != "feature" || asString(task["base_branch_source"]) != "explicit" {
		t.Fatalf("recorded base = %v/%v, want feature recorded as explicit", task["base_branch"], task["base_branch_source"])
	}
	again := d.ctl(true, "prepare", "--repo", repo, "--base", "origin/feature", "--brief", policyBrief(t, d.base), "--approved")
	if asString(again["base_sha"]) != feature {
		t.Fatalf("origin/feature base_sha = %v, want %s", again["base_sha"], feature)
	}
	if asString(again["base_branch"]) != "feature" || asString(again["base_branch_source"]) != "explicit" {
		t.Fatalf("origin/feature recorded base = %v/%v, want the bare name feature as explicit", again["base_branch"], again["base_branch_source"])
	}
}

func pushDivergentRelease(t *testing.T, repo string) string {
	t.Helper()
	gitIn(t, repo, "checkout", "-q", "-b", "release/1.2")
	writeFile(t, filepath.Join(repo, "RELEASE.md"), "1.2\n")
	gitIn(t, repo, "add", "RELEASE.md")
	gitIn(t, repo, "commit", "-q", "-m", "release work")
	gitIn(t, repo, "push", "-q", "origin", "release/1.2")
	release := strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD"))
	gitIn(t, repo, "checkout", "-q", "main")
	writeFile(t, filepath.Join(repo, "MAIN.md"), "main moves on\n")
	gitIn(t, repo, "add", "MAIN.md")
	gitIn(t, repo, "commit", "-q", "-m", "main work")
	gitIn(t, repo, "push", "-q", "origin", "main")
	return release
}

func assertNothingPrepared(t *testing.T, d *demoLab) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(d.home, "tasks"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a refused dispatch left task records: %v", entries)
	}
	if len(nativeCalls(d, "terminal.run")) != 0 {
		t.Fatal("refused dispatch still launched a native terminal")
	}
	assertPrimaryCheckoutsOnly(t, d)
}

func TestDispatchRecordsAReleaseBranchAsTheDeliveryTarget(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "release-target", map[string]string{"README.md": "x\n"})
	release := pushDivergentRelease(t, repo)

	task := d.ctl(true, "dispatch", "--repo", repo, "--base", "release/1.2", "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if asString(task["base_sha"]) != release || asString(task["base_branch"]) != "release/1.2" || asString(task["base_branch_source"]) != "explicit" {
		t.Fatalf("task base = %v/%v/%v, want the release tip recorded as release/1.2 explicit", task["base_sha"], task["base_branch"], task["base_branch_source"])
	}
	if head := strings.TrimSpace(gitIn(t, asString(task["worktree"]), "rev-parse", "HEAD")); head != release {
		t.Fatalf("worktree HEAD = %s, want the release tip %s", head, release)
	}
}

func TestDispatchResolvesTheDefaultBranchOnceAtPrepare(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "default-once", map[string]string{"README.md": "x\n"})
	pushDivergentRelease(t, repo)

	first := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if asString(first["base_branch"]) != "main" || asString(first["base_branch_source"]) != "default" {
		t.Fatalf("default base = %v/%v, want main recorded as default", first["base_branch"], first["base_branch_source"])
	}

	origin := strings.TrimSpace(gitIn(t, repo, "remote", "get-url", "origin"))
	gitIn(t, origin, "symbolic-ref", "HEAD", "refs/heads/release/1.2")
	second := d.ctl(true, "prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	if asString(second["base_branch"]) != "release/1.2" || asString(second["base_branch_source"]) != "default" {
		t.Fatalf("base after the remote default moved = %v/%v, want release/1.2 as default", second["base_branch"], second["base_branch_source"])
	}
	recorded := readTaskJSON(t, filepath.Join(d.home, "tasks", asString(first["id"]), "task.json"))
	if asString(recorded["base_branch"]) != "main" {
		t.Fatalf("first task now records %v, want its original main", recorded["base_branch"])
	}
}

func TestDispatchRefusesATagAsBaseWithoutATargetBranch(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "tag-base", map[string]string{"README.md": "x\n"})
	gitIn(t, repo, "-c", "tag.gpgsign=false", "tag", "-a", "-m", "v1.0", "v1.0")
	gitIn(t, repo, "push", "-q", "origin", "v1.0")

	refused := d.ctl(false, "dispatch", "--repo", repo, "--base", "v1.0", "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if msg := asString(refused["error"]); !strings.Contains(msg, "names no branch on origin") || !strings.Contains(msg, "--target-branch") {
		t.Fatalf("dispatch error = %q, want a non-branch refusal naming --target-branch", msg)
	}
	assertNothingPrepared(t, d)
}

func TestDispatchAcceptsATagStartOnlyWithAnAncestorTargetBranch(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "tag-start", map[string]string{"README.md": "x\n"})
	start := strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD"))
	gitIn(t, repo, "-c", "tag.gpgsign=false", "tag", "-a", "-m", "v1.0", "v1.0")
	gitIn(t, repo, "push", "-q", "origin", "v1.0")
	release := pushDivergentRelease(t, repo)
	gitIn(t, repo, "checkout", "-q", "-b", "elsewhere", start)
	writeFile(t, filepath.Join(repo, "ELSEWHERE.md"), "not on the release line\n")
	gitIn(t, repo, "add", "ELSEWHERE.md")
	gitIn(t, repo, "commit", "-q", "-m", "elsewhere")
	gitIn(t, repo, "-c", "tag.gpgsign=false", "tag", "-a", "-m", "v2.0", "v2.0")
	gitIn(t, repo, "push", "-q", "origin", "v2.0")
	gitIn(t, repo, "checkout", "-q", "main")

	task := d.ctl(true, "dispatch", "--repo", repo, "--base", "v1.0", "--target-branch", "release/1.2", "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if asString(task["base_sha"]) != start || asString(task["base_branch"]) != "release/1.2" || asString(task["base_branch_source"]) != "explicit" {
		t.Fatalf("tag-start task = %v/%v/%v, want start %s delivering to release/1.2 explicit", task["base_sha"], task["base_branch"], task["base_branch_source"], start)
	}
	if head := strings.TrimSpace(gitIn(t, asString(task["worktree"]), "rev-parse", "HEAD")); head != start {
		t.Fatalf("worktree HEAD = %s, want the tag's commit %s (not the release tip %s)", head, start, release)
	}
	nativeBefore := len(nativeCalls(d, "terminal.run"))
	gitBefore := gitIn(t, repo, "worktree", "list", "--porcelain")

	refusals := map[string][]string{
		"not an ancestor":                      {"--base", "v2.0", "--target-branch", "release/1.2"},
		"already names a branch":               {"--base", "main", "--target-branch", "release/1.2"},
		"give that start with --base":          {"--target-branch", "release/1.2"},
		"--target-branch nope names no branch": {"--base", "v1.0", "--target-branch", "nope"},
	}
	for want, flags := range refusals {
		args := append([]string{"prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved"}, flags...)
		refused := d.ctl(false, args...)
		if msg := asString(refused["error"]); !strings.Contains(msg, want) {
			t.Fatalf("prepare %v error = %q, want %q", flags, msg, want)
		}
	}
	entries, err := os.ReadDir(filepath.Join(d.home, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("task records = %d, want only the accepted tag-start task", len(entries))
	}
	if len(nativeCalls(d, "terminal.run")) != nativeBefore || gitIn(t, repo, "worktree", "list", "--porcelain") != gitBefore {
		t.Fatal("refused prepare changed native terminals or Git worktree registrations")
	}

}

func readTaskJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var task map[string]any
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}
	return task
}

// Dispatch binds the worker registration to the occupant Herdr reports for the task's pane (its terminal and shell),
// so the pane's own init and every later delivery are judged against it.
func TestDispatchRecordsTheWorkerPanesIncarnation(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "incarnation-worker", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	address := workerAddress(task)
	if worker := d.ctlPane(address, true, "init"); asString(worker["role"]) != "worker" {
		t.Fatalf("worker init %v", worker)
	}
	task = d.taskRecord(asString(task["id"]))
	endpoint := store.EndpointFromContext(mapObject(t, asMap(task["worker_endpoint"])))
	st := nativeTestStore(t, d.home)
	reg, err := st.Registration(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	var bound incarnation.NativeEvidence
	raw, _ := ordjson.MarshalCompact(func() any { v, _ := reg.Get("incarnation"); return v }())
	if err = json.Unmarshal(raw, &bound); err != nil {
		t.Fatal(err)
	}
	if !bound.Endpoint.SameAuthority(endpoint.Native()) || bound.PID == 0 || bound.Foreground == nil {
		t.Fatalf("native incarnation not persisted: %s", raw)
	}
	if receipt, _ := reg.Get("native_receipt"); receipt == nil {
		t.Fatal("actual worker init produced no receipt")
	}

}

func uncertainTask(t *testing.T, d *demoLab) (taskID string, attempt map[string]any) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(d.home, "tasks"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("tasks dir = %v, %v; want the one refused task record", entries, err)
	}
	taskID = entries[0].Name()
	recorded := readTaskJSON(t, filepath.Join(d.home, "tasks", taskID, "task.json"))
	attempt, _ = launchedWorker(recorded)
	if state := asString(attempt["state"]); state != "uncertain" {
		t.Fatalf("worker attempt state = %q, want uncertain", state)
	}
	return taskID, attempt
}

func TestRedeliverRecordsSwallowedBriefRunning(t *testing.T) {
	d, task := nativeUnsentTask(t, "redeliver-worker")
	id := asString(task["id"])
	attempt, _ := launchedWorker(task)
	attemptID := asString(attempt["id"])
	address := workerAddress(task)
	seedBootstrapProgram(t, d, task)
	sent := d.ctl(false, "repair", "send", id, "--attempt", attemptID, "--key", "rebase", "--text", "Rebase onto main.")
	if asString(sent["error"]) == "" {
		t.Fatal("repair accepted before worker receipt")
	}
	d.ctl(true, "execution", "redeliver", id, "--attempt", attemptID)
	saved := d.taskRecord(id)
	w, _ := launchedWorker(saved)
	if w["state"] == "running" {
		t.Fatal("native send result supplied an application receipt")
	}
	if len(nativeCalls(d, "terminal.run")) != 1 || len(nativeCalls(d, "terminal.send")) != 1 {
		t.Fatal("explicit redelivery duplicated launch or input")
	}
	d.ctlPane(address, true, "init")
	confirmed := d.ctl(true, "execution", "confirm", id, "--attempt", attemptID)
	if confirmed["confirmed"] != true {
		t.Fatal("matching receipt did not confirm")
	}
	d.native.SetStatus(address, "idle")
	sent = d.ctl(false, "repair", "send", id, "--attempt", attemptID, "--key", "rebase", "--text", "Rebase onto main.")
	if asString(sent["error"]) != "" {
		t.Fatalf("receipt-bound repair %v", sent)
	}

}

func TestConfirmRequiresWorkerReceipt(t *testing.T) {
	d, task := nativeUnsentTask(t, "confirm-worker")
	id := asString(task["id"])
	attempt, _ := launchedWorker(task)
	attemptID := asString(attempt["id"])
	generation := attempt["generation"]
	refused := d.ctl(false, "execution", "confirm", id, "--attempt", attemptID)
	if !strings.Contains(asString(refused["error"]), "receipt") {
		t.Fatalf("missing receipt %v", refused)
	}
	beforeReport := d.taskRecord(id)
	report := d.ctl(false, "report", id, "--text", "Coordinator-side report is not a worker bootstrap receipt.")
	if !strings.Contains(asString(report["error"]), "required task role") {
		t.Fatalf("coordinator report bypassed worker authority: %v", report)
	}
	if !reflect.DeepEqual(beforeReport, d.taskRecord(id)) {
		t.Fatal("refused coordinator report rewrote the task or worker receipt")
	}

	refused = d.ctl(false, "execution", "confirm", id, "--attempt", attemptID)
	if !strings.Contains(asString(refused["error"]), "receipt") {
		t.Fatalf("off-terminal report became receipt %v", refused)
	}
	before := d.taskRecord(id)
	w, _ := launchedWorker(before)
	if w["generation"] != generation || w["state"] == "running" {
		t.Fatal("missing receipt mutated attempt")
	}
	d.ctlPane(workerAddress(task), true, "init")
	sends := len(nativeCalls(d, "terminal.send"))
	refused = d.ctl(false, "execution", "redeliver", id, "--attempt", attemptID)
	if asString(refused["error"]) == "" || len(nativeCalls(d, "terminal.send")) != sends {
		t.Fatal("receipt-bearing worker was blindly redelivered")
	}
	confirmed := d.ctl(true, "execution", "confirm", id, "--attempt", attemptID)
	if confirmed["confirmed"] != true {
		t.Fatalf("confirmation %v", confirmed)
	}
	saved := d.taskRecord(id)
	w, _ = launchedWorker(saved)
	if w["state"] != "running" || saved["error"] != nil {
		t.Fatal("current receipt failed to discharge bootstrap")
	}

}

func TestConfirmRefusesReplacedOccupant(t *testing.T) {
	d, task := nativeUnsentTask(t, "replaced-worker")
	id := asString(task["id"])
	attempt, _ := launchedWorker(task)
	attemptID := asString(attempt["id"])
	address := workerAddress(task)
	d.ctlPane(address, true, "init")
	before := d.taskRecord(id)
	generation := asMap(asMap(before["execution"])["worker"])["generation"]
	for _, terminal := range d.native.Terminals() {
		if terminal.Address == address {
			terminal.Foreground.PID++
			d.native.SetTerminal(terminal)
		}
	}
	refused := d.ctl(false, "execution", "confirm", id, "--attempt", attemptID)
	if asString(refused["error"]) == "" {
		t.Fatal("replacement process inherited receipt")
	}
	after := d.taskRecord(id)
	if asMap(asMap(after["execution"])["worker"])["generation"] != generation {
		t.Fatal("refused confirmation changed generation")
	}

}

func nativeCalls(d *demoLab, method string) []hctest.Call {
	var out []hctest.Call
	for _, call := range d.native.Calls() {
		if call.Method == method {
			out = append(out, call)
		}
	}
	return out
}
func onlyNativeTask(t *testing.T, d *demoLab) map[string]any {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(d.home, "tasks", "*", "task.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("task records %v %v", files, err)
	}
	return readTaskJSON(t, files[0])
}
func mapObject(t *testing.T, value map[string]any) *ordjson.Object {
	t.Helper()
	out, err := ordjson.FromValue(value)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func nativeUnsentTask(t *testing.T, name string) (*demoLab, map[string]any) {
	t.Helper()
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, name, map[string]string{"README.md": "x\n"})
	d.native.QueueFault("terminal.run", hctest.Fault{FirstMessage: &hcclient.FirstMessage{Sent: false}})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if task["status"] != "needs-attention" {
		t.Fatalf("known unsent launch %v", task)
	}
	return d, task
}

func seedBootstrapProgram(t *testing.T, d *demoLab, task map[string]any) {
	t.Helper()
	endpoint := store.EndpointFromContext(mapObject(t, asMap(task["worker_endpoint"])))
	st := nativeTestStore(t, d.home)
	for _, terminal := range d.native.Terminals() {
		if terminal.Address == endpoint.Address {
			pid := uint32(200000)
			terminal.PID = &pid
			terminal.Foreground = &hcclient.Foreground{PID: 200001, Name: "codex", Argv: []string{"codex", "--no-daemon"}, Cwd: terminal.Cwd}
			d.native.SetTerminal(terminal)
			d.native.SetStatus(terminal.Address, "idle")
			bound := incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: terminal.Foreground}
			if err := st.SetIncarnation(endpoint, bound.Record(store.Now())); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func assertPrimaryCheckoutsOnly(t *testing.T, d *demoLab) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(d.base, "projects", "*", ".git"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		repo := filepath.Dir(path)
		if count := strings.Count(gitIn(t, repo, "worktree", "list", "--porcelain"), "worktree "); count != 1 {
			t.Fatalf("refused dispatch created Git worktrees in %s: %d", repo, count)
		}
	}
}
