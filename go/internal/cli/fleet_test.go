package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/updatecmd"
)

var fleetRoles = []string{
	"busy-tool-call", "dirty-checkout", "open-question", "answered-unapplied", "pending-report", "old-mcp-client",
	"closed-parent", "unknown-worker", "failed-refresh", "ignores-refresh", "cooperative-a", "cooperative-b",
}

type fleetLab struct {
	*demoLab
	source string
}

type checkoutSnap struct {
	head, branch, porcelain string
}

func TestFleetTwelveWorkers(t *testing.T) {
	f := newFleetLab(t)
	settings := f.ctl(true, "settings", "set", "--global", "12", "--per-repository", "1")
	if settings["previous"] != nil {
		t.Fatalf("previous = %v, want nil", settings["previous"])
	}
	cap := asMap(settings["capacity"])
	if asInt(cap["global"]) != 12 || asInt(cap["per_repository"]) != 1 {
		t.Fatalf("capacity = %v", settings["capacity"])
	}

	tasks := map[string]map[string]any{}
	byID := map[string]string{}
	brief := f.brief()
	for _, role := range fleetRoles {
		repo := f.project(role)
		task := f.ctl(true, "dispatch", "--repo", repo, "--brief", brief, "--harness", "codex", "--approved")
		if asString(task["status"]) != "running" {
			t.Fatalf("%s status = %v", role, task["status"])
		}
		limits := asMap(asMap(task["admission"])["limits"])
		if asInt(limits["global"]) != 12 || asInt(limits["per_repository"]) != 1 {
			t.Fatalf("%s admission.limits = %v", role, task["admission"])
		}
		tasks[role] = task
		byID[asString(task["id"])] = role
	}
	if asInt(asMap(f.ctl(true, "settings", "show")["occupied"])["global"]) != 12 {
		t.Fatalf("occupied.global = %v, want 12", f.ctl(true, "settings", "show")["occupied"])
	}
	thirteenth := f.ctl(false, "prepare", "--repo", f.project("thirteenth"), "--brief", brief, "--harness", "codex", "--approved")
	if !strings.Contains(asString(thirteenth["error"]), "12 of 12 global execution slots") {
		t.Fatalf("thirteenth = %v", thirteenth)
	}

	for role, task := range tasks {
		status := "idle"
		if role == "busy-tool-call" {
			status = "working"
		}
		f.paneState(asString(task["pane"]), map[string]any{"agent_status": status})
	}
	if err := os.WriteFile(filepath.Join(asString(tasks["dirty-checkout"]["worktree"]), "wip.txt"), []byte("uncommitted work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	openQ := asMap(f.ctl(true, "ask", asString(tasks["open-question"]["id"]), "--key", "open", "--text", "Open question?")["question"])
	answered := asMap(f.ctl(true, "ask", asString(tasks["answered-unapplied"]["id"]), "--key", "answered", "--text", "Answered question?")["question"])
	f.ctl(true, "answer", asString(tasks["answered-unapplied"]["id"]), asString(answered["id"]), "--text", "Yes.")
	f.paneState(asString(tasks["answered-unapplied"]["pane"]), map[string]any{"agent_status": "idle"})
	f.ctl(true, "report", asString(tasks["pending-report"]["id"]), "--text", "Candidate ready; not verified.")
	for role, task := range tasks {
		id := asString(task["id"])
		if role == "old-mcp-client" {
			f.patchRuntimeMCP(id, 9)
			continue
		}
		f.patchRuntimeMCP(id, contract.MCP.Tools)
	}
	closedID := asString(tasks["closed-parent"]["id"])
	closed := f.readTask(closedID)
	parent := asMap(closed["parent"])
	parent["pane"] = "w-gone:p1"
	closed["parent"] = parent
	f.writeTask(closedID, closed)
	closedQ := f.ctl(true, "ask", closedID, "--key", "orphan", "--text", "Parent gone?")
	notice := asMap(closedQ["notice"])
	if asString(notice["status"]) != "pending" {
		t.Fatalf("closed notice status = %v", notice["status"])
	}
	if !strings.Contains(fmt.Sprint(notice["error"]), "agent_not_found") {
		t.Fatalf("closed notice error = %v", notice["error"])
	}
	f.removePane(asString(tasks["unknown-worker"]["pane"]))
	f.setEnv("FAKE_FAIL_PROMPT_PANES", asString(tasks["failed-refresh"]["pane"]))

	records := map[string][]string{}
	for role, task := range tasks {
		records[role] = questionKeys(f.readTask(asString(task["id"])))
	}
	checkouts := f.checkouts(tasks)
	callsBefore := len(f.calls())

	inbox := f.ctl(true, "status", "--live")
	if inbox["live"] != true {
		t.Fatalf("status --live live = %v", inbox["live"])
	}
	if n := len(asSlice(inbox["tasks"])); n != 12 {
		t.Fatalf("status --live tasks = %d, want 12", n)
	}
	if asInt(asMap(asMap(inbox["capacity"])["occupied"])["global"]) != 12 {
		t.Fatalf("status occupied = %v", inbox["capacity"])
	}
	for _, c := range f.calls()[callsBefore:] {
		if headsEqual(c, "agent", "start") || (len(c) > 1 && (c[1] == "stop" || c[1] == "kill" || c[1] == "restart")) {
			t.Fatalf("rundown lifecycle %v", c)
		}
	}

	skill, err := os.ReadFile(filepath.Join(f.root, "skills/sum-worker/SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	sha1 := f.commitUpstream("skills/sum-worker/SKILL.md", string(skill)+"\nUpdate one: reread decisions before continuing.\n")
	applied := f.ctl(true, "update", "apply", "--no-fetch")
	if applied["changed"] != true {
		t.Fatalf("apply one changed = %v", applied["changed"])
	}
	if asString(asMap(applied["default"])["sha"]) != sha1 {
		t.Fatalf("apply one sha = %v, want %s", asMap(applied["default"])["sha"], sha1)
	}

	first, delta := f.measure(true, "refresh", "request")
	rows := f.targetRows(first, byID)
	if asString(asMap(first["runtime"])["sha"]) != sha1 {
		t.Fatalf("refresh one runtime = %v", first["runtime"])
	}
	if asString(rows["coordinator"]["state"]) != "pending-busy" {
		t.Fatalf("coordinator state = %v", rows["coordinator"]["state"])
	}
	expected := map[string]string{
		"busy-tool-call": "pending-busy",
		"unknown-worker": "pending-unreachable",
		"failed-refresh": "pending-unreachable",
	}
	for _, role := range fleetRoles {
		want := expected[role]
		if want == "" {
			want = "submitted-unconfirmed"
		}
		if asString(rows[role]["state"]) != want {
			t.Fatalf("%s state = %v, want %s", role, rows[role]["state"], want)
		}
		if asString(rows[role]["revision"]) != "r2" {
			t.Fatalf("%s revision = %v, want r2", role, rows[role]["revision"])
		}
	}
	if !strings.Contains(asString(rows["busy-tool-call"]["reason"]), "working") {
		t.Fatalf("busy reason = %v", rows["busy-tool-call"]["reason"])
	}
	if !strings.Contains(asString(rows["unknown-worker"]["reason"]), "agent_not_found") {
		t.Fatalf("unknown reason = %v", rows["unknown-worker"]["reason"])
	}
	if !strings.Contains(asString(rows["failed-refresh"]["reason"]), "prompt was not accepted") {
		t.Fatalf("failed-refresh reason = %v", rows["failed-refresh"]["reason"])
	}
	var deferredWhat []string
	for _, item := range asSlice(rows["old-mcp-client"]["deferred"]) {
		deferredWhat = append(deferredWhat, asString(asMap(item)["what"]))
	}
	if strings.Join(deferredWhat, ",") != "mcp" {
		t.Fatalf("old-mcp deferred = %v", rows["old-mcp-client"]["deferred"])
	}
	if asInt(asMap(first["counts"])["submitted-unconfirmed"]) != 9 {
		t.Fatalf("submitted-unconfirmed = %v", first["counts"])
	}
	if asInt(asMap(first["fanout"])["herdr_calls"]) != 1 || asInt(asMap(first["fanout"])["sessions"]) != 1 {
		t.Fatalf("refresh one fanout = %v", first["fanout"])
	}
	prompts := 0
	for _, c := range delta {
		if headsEqual(c, "agent", "get") {
			t.Fatalf("refresh one used agent get: %v", delta)
		}
		if headsEqual(c, "agent", "prompt") {
			prompts++
			if len(c) < 4 || !strings.HasPrefix(c[3], "sum refresh t-") {
				t.Fatalf("prompt = %v", c)
			}
			for _, prose := range []string{"Open question", "Answered question", "Yes.", "Candidate ready", "Parent gone"} {
				if strings.Contains(c[3], prose) {
					t.Fatalf("prompt leaked %q: %s", prose, c[3])
				}
			}
		}
	}
	if prompts != 10 {
		t.Fatalf("prompts = %d, want 10 in %v", prompts, delta)
	}
	if len(delta) > 1+1+10 {
		t.Fatalf("refresh one herdr calls = %d, want <= 12: %v", len(delta), delta)
	}

	f.ctl(true, "answer", asString(tasks["open-question"]["id"]), asString(openQ["id"]), "--text", "Answered after update one.")
	f.ctl(true, "resolve", asString(tasks["answered-unapplied"]["id"]), asString(answered["id"]))
	f.ctl(true, "report", asString(tasks["cooperative-a"]["id"]), "--text", "Reported during the update.")
	more := asMap(f.ctl(true, "ask", asString(tasks["busy-tool-call"]["id"]), "--key", "busy", "--text", "Asked while busy?")["question"])
	for _, role := range []string{"dirty-checkout", "cooperative-a", "cooperative-b"} {
		f.ctlPane(asString(tasks[role]["pane"]), true, "brief", "adopt", asString(tasks[role]["id"]), "r2")
	}
	if asInt(asMap(f.ctl(true, "refresh", "status")["counts"])["confirmed"]) != 3 {
		t.Fatalf("confirmed after adopt = %v", f.ctl(true, "refresh", "status")["counts"])
	}
	for role, task := range tasks {
		if role == "unknown-worker" {
			continue
		}
		status := "idle"
		if role == "busy-tool-call" {
			status = "working"
		}
		f.paneState(asString(task["pane"]), map[string]any{"agent_status": status})
	}

	agents, err := os.ReadFile(filepath.Join(f.root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	f.commitUpstream("AGENTS.md", string(agents)+"\nUpdate two.\n")
	sha2 := f.commitUpstream("skills/sum-worker/SKILL.md", string(skill)+"\nUpdate two: reread decisions before continuing.\n")
	if asString(asMap(f.ctl(true, "update", "apply", "--no-fetch")["default"])["sha"]) != sha2 {
		t.Fatalf("apply two sha = %v, want %s", f.ctl(true, "update", "status")["default"], sha2)
	}

	if err := os.Remove(filepath.Join(f.base, "fake", "prompt_count")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.base, "fake", "delivery_count")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	f.setEnv("FAKE_INTERRUPT_AFTER_PROMPTS", "5")
	f.setEnv("SUM_TEST_INTERRUPT_AFTER_DELIVERIES", "5")
	if _, err := f.ctlRaw("refresh", "request"); err == nil {
		t.Fatal("interrupted refresh request succeeded")
	}
	f.setEnv("FAKE_INTERRUPT_AFTER_PROMPTS", "")
	f.setEnv("SUM_TEST_INTERRUPT_AFTER_DELIVERIES", "")

	status := f.ctl(true, "refresh", "status")
	states := map[string]map[string]any{}
	for _, item := range asSlice(status["targets"]) {
		row := asMap(item)
		if asString(row["target"]) != "task" {
			continue
		}
		states[byID[asString(row["task"])]] = row
	}
	var orphaned []string
	requestedR3 := 0
	for role, row := range states {
		if asString(row["requested"]) == "r3" {
			requestedR3++
			if asString(row["reason"]) == "requested; no delivery attempt recorded yet" {
				orphaned = append(orphaned, role)
			}
		}
	}
	if len(orphaned) != 1 {
		t.Fatalf("orphaned = %v (want 1) states=%v", orphaned, states)
	}
	if requestedR3 < 2 || requestedR3 >= len(fleetRoles) {
		t.Fatalf("interrupt did not cut fan-out: requested r3 = %d (want a proper subset) states=%v", requestedR3, states)
	}
	shownOpen := f.ctl(true, "show", asString(tasks["open-question"]["id"]))
	if asString(asMap(asSlice(shownOpen["questions"])[0])["status"]) != "answered" {
		t.Fatalf("open-question after interrupt = %v", shownOpen["questions"])
	}
	for role, task := range tasks {
		keys := questionKeys(f.readTask(asString(task["id"])))
		want := append([]string{}, records[role]...)
		if role == "busy-tool-call" {
			want = append(want, "busy")
		}
		if strings.Join(keys, ",") != strings.Join(want, ",") {
			t.Fatalf("%s question keys = %v, want %v", role, keys, want)
		}
	}

	recovered, delta := f.measure(true, "refresh", "request")
	rows = f.targetRows(recovered, byID)
	for role, row := range rows {
		if role == "coordinator" {
			continue
		}
		if asString(row["revision"]) != "r3" {
			t.Fatalf("%s recovered revision = %v, want r3", role, row["revision"])
		}
	}
	if asString(rows["coordinator"]["revision"]) != "r2" {
		t.Fatalf("coordinator recovered revision = %v, want r2", rows["coordinator"]["revision"])
	}
	if !strings.Contains(fmt.Sprint(rows["coordinator"]["summary"]), "AGENTS.md changed") {
		t.Fatalf("coordinator summary = %v", rows["coordinator"]["summary"])
	}
	if asInt(asMap(recovered["fanout"])["sessions"]) != 1 {
		t.Fatalf("recovery fanout = %v", recovered["fanout"])
	}
	for _, c := range delta {
		if headsEqual(c, "agent", "get") {
			t.Fatalf("recovery used agent get: %v", delta)
		}
	}
	for _, role := range fleetRoles {
		vers := f.readVersions(asString(tasks[role]["id"]))
		if asString(vers["requested"]) != "r3" {
			t.Fatalf("%s requested = %v, want r3", role, vers["requested"])
		}
		revs := asSlice(vers["revisions"])
		if asString(asMap(revs[len(revs)-1])["id"]) != "r3" {
			t.Fatalf("%s latest revision = %v", role, revs[len(revs)-1])
		}
	}
	stale := f.ctlPane(asString(tasks["cooperative-b"]["pane"]), false, "brief", "adopt", asString(tasks["cooperative-b"]["id"]), "r2")
	if !strings.Contains(asString(stale["error"]), "not the requested revision") {
		t.Fatalf("stale adopt = %v", stale)
	}
	f.ctlPane(asString(tasks["cooperative-b"]["pane"]), true, "brief", "adopt", asString(tasks["cooperative-b"]["id"]), "r3")

	rolled := f.ctl(true, "update", "rollback")
	if rolled["changed"] != true || asString(asMap(rolled["default"])["sha"]) != sha1 {
		t.Fatalf("rollback = %v, want sha %s", rolled, sha1)
	}
	for role, task := range tasks {
		if role == "busy-tool-call" || role == "unknown-worker" {
			continue
		}
		f.paneState(asString(task["pane"]), map[string]any{"agent_status": "idle"})
	}
	third, _ := f.measure(true, "refresh", "request")
	rows = f.targetRows(third, byID)
	if asString(asMap(third["runtime"])["sha"]) != sha1 || asString(rows["coordinator"]["revision"]) != "r3" {
		t.Fatalf("refresh after rollback runtime=%v coordinator=%v", third["runtime"], rows["coordinator"])
	}
	for role, row := range rows {
		if role == "coordinator" {
			continue
		}
		if asString(row["revision"]) != "r4" {
			t.Fatalf("%s after rollback revision = %v, want r4", role, row["revision"])
		}
	}
	coopPath := asString(rows["cooperative-a"]["path"])
	body, err := os.ReadFile(coopPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "Update two") {
		t.Fatalf("rolled-back brief still has update two: %s", coopPath)
	}
	if !strings.Contains(string(body), "Update one") {
		t.Fatalf("rolled-back brief missing update one: %s", coopPath)
	}
	f.ctl(true, "refresh", "adopt", "--coordinator", "r3")
	for _, role := range []string{"cooperative-a", "cooperative-b", "dirty-checkout"} {
		f.ctlPane(asString(tasks[role]["pane"]), true, "brief", "adopt", asString(tasks[role]["id"]), "r4")
	}
	final := f.ctl(true, "refresh", "status")
	counts := asMap(final["counts"])
	if asInt(counts["confirmed"]) != 4 || asInt(counts["pending-busy"]) != 1 || asInt(counts["pending-unreachable"]) != 2 {
		var bits []string
		for _, item := range asSlice(final["targets"]) {
			row := asMap(item)
			role := byID[asString(row["task"])]
			if role == "" {
				role = asString(row["target"])
			}
			bits = append(bits, fmt.Sprintf("%s state=%v requested=%v reason=%v", role, row["state"], row["requested"], row["reason"]))
		}
		t.Fatalf("final refresh counts = %v\n%s", counts, strings.Join(bits, "\n"))
	}

	if err := os.WriteFile(filepath.Join(f.root, "local-only.txt"), []byte("unmerged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.git(f.root, "add", "local-only.txt")
	f.git(f.root, "commit", "-q", "-m", "unmerged local commit")
	refused := f.ctl(false, "update", "apply", "--ref", "HEAD", "--no-fetch")
	if !strings.Contains(asString(refused["error"]), "not merged on origin/main") {
		t.Fatalf("unmerged apply = %v", refused)
	}
	if asString(asMap(f.ctl(true, "update", "status")["default"])["sha"]) != sha1 {
		t.Fatalf("default after refused apply = %v", f.ctl(true, "update", "status")["default"])
	}

	st, err := store.Open(f.home)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := store.Context(f.root)
	if err != nil {
		t.Fatal(err)
	}
	previousPath := f.currentPath()
	candidatePath := filepath.Join(f.root, ".local", "releases", sha2)
	if resolved, err := filepath.EvalSymlinks(previousPath); err == nil {
		previousPath = resolved
	}
	if resolved, err := filepath.EvalSymlinks(candidatePath); err == nil {
		candidatePath = resolved
	}
	if previousPath != filepath.Join(f.root, ".local", "releases", sha1) && filepath.Base(previousPath) != sha1 {
		t.Fatalf("current before failed activation = %s, want %s", previousPath, sha1)
	}

	var selections []string
	updatecmd.TestPostCheck = func(s *store.Store, root string) *ordjson.Object {
		selected := currentRuntimePath(root)
		selections = append(selections, selected)
		if sameFile(selected, candidatePath) {
			attempt := 0
			for _, item := range selections {
				if sameFile(item, candidatePath) {
					attempt++
				}
			}
			f.ctl(true, "ask", asString(tasks["cooperative-a"]["id"]), "--key", fmt.Sprintf("activation-%d", attempt),
				"--text", fmt.Sprintf("Question during activation %d?", attempt))
			f.ctl(true, "report", asString(tasks["failed-refresh"]["id"]),
				"--text", fmt.Sprintf("Report during activation %d.", attempt))
			row := ordjson.NewObject()
			row.Set("ok", false)
			row.Set("detail", "fleet candidate-only post-check failure")
			return row
		}
		return updatecmd.PostCheck(s, root)
	}
	t.Cleanup(func() {
		updatecmd.TestPostCheck = nil
		updatecmd.TestRecoverPending = nil
	})
	updatecmd.TestRecoverPending = func(*store.Store, string, string) (*ordjson.Object, error) {
		return nil, fmt.Errorf("fleet test-only compensation fault")
	}
	_, applyErr := updatecmd.Apply(st, ctx, sha2, true)
	if applyErr == nil || !strings.Contains(applyErr.Error(), "Recovery also failed: fleet test-only compensation fault") {
		t.Fatalf("failed compensation: %v", applyErr)
	}
	pending := asMap(asMap(f.ctl(true, "update", "status")["activation"])["pending"])
	if !sameFile(f.currentPath(), candidatePath) {
		t.Fatalf("current after failed compensation = %s, want %s", f.currentPath(), candidatePath)
	}
	if asString(asMap(pending["to"])["sha"]) != sha2 {
		t.Fatalf("pending.to = %v, want %s", pending["to"], sha2)
	}
	updatecmd.TestRecoverPending = nil
	repaired, recErr := updatecmd.Recover(st, ctx, asString(pending["generation"]))
	if recErr != nil {
		t.Fatalf("recover: %v", recErr)
	}
	changed, _ := repaired.Get("changed")
	def, _ := repaired.Get("default")
	if changed != true || asString(asMapFromOrd(def)["sha"]) != sha1 {
		t.Fatalf("repaired = changed %v default %v, want sha %s", changed, def, sha1)
	}
	_, applyErr = updatecmd.Apply(st, ctx, sha2, true)
	if applyErr == nil || !strings.Contains(applyErr.Error(), "entrypoint check failed") || !strings.Contains(applyErr.Error(), "restored and verified") {
		t.Fatalf("second candidate failure: %v", applyErr)
	}
	candidateHits, previousHits := 0, 0
	for _, item := range selections {
		if sameFile(item, candidatePath) {
			candidateHits++
		}
		if sameFile(item, previousPath) {
			previousHits++
		}
	}
	if candidateHits != 2 {
		t.Fatalf("candidate post-check hits = %d, want 2 (%v)", candidateHits, selections)
	}
	if previousHits < 2 {
		t.Fatalf("previous post-check hits = %d, want >= 2 (%v)", previousHits, selections)
	}
	if !sameFile(f.currentPath(), previousPath) {
		t.Fatalf("current after recovered activation = %s, want %s", f.currentPath(), previousPath)
	}
	if asMap(f.ctl(true, "update", "status")["activation"])["pending"] != nil {
		t.Fatalf("pending after recovered activation = %v", f.ctl(true, "update", "status")["activation"])
	}
	coopQuestions := questionKeys(f.readTask(asString(tasks["cooperative-a"]["id"])))
	var activationKeys []string
	for _, key := range coopQuestions {
		if strings.HasPrefix(key, "activation-") {
			activationKeys = append(activationKeys, key)
		}
	}
	if strings.Join(activationKeys, ",") != "activation-1,activation-2" {
		t.Fatalf("activation keys = %v", activationKeys)
	}
	failedReport := asMap(f.readTask(asString(tasks["failed-refresh"]["id"]))["report"])
	if asString(failedReport["text"]) != "Report during activation 2." {
		t.Fatalf("failed-refresh report = %v", failedReport)
	}

	callbacksBefore := questionKeys(f.readTask(asString(tasks["cooperative-a"]["id"])))
	recoveredQuestion := asMap(f.ctl(true, "ask", asString(tasks["cooperative-a"]["id"]), "--key", "post-activation", "--text", "Question after recovered activation?")["question"])
	f.ctl(true, "report", asString(tasks["failed-refresh"]["id"]), "--text", "Report after recovered activation.")
	afterKeys := questionKeys(f.readTask(asString(tasks["cooperative-a"]["id"])))
	wantKeys := append(append([]string{}, callbacksBefore...), asString(recoveredQuestion["key"]))
	if strings.Join(afterKeys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("post-activation keys = %v, want %v", afterKeys, wantKeys)
	}
	if asString(asMap(f.readTask(asString(tasks["failed-refresh"]["id"]))["report"])["text"]) != "Report after recovered activation." {
		t.Fatalf("report after recovered activation = %v", f.readTask(asString(tasks["failed-refresh"]["id"]))["report"])
	}
	f.ctl(true, "answer", asString(tasks["busy-tool-call"]["id"]), asString(more["id"]), "--text", "Answered during the failed update.")
	f.ctl(true, "report", asString(tasks["cooperative-b"]["id"]), "--text", "Reported during the failed update.")
	live := f.ctl(true, "inbox", "--live")
	if live["live"] != true {
		t.Fatalf("inbox --live live = %v", live["live"])
	}
	if asInt(asMap(asMap(live["capacity"])["occupied"])["global"]) != 12 {
		t.Fatalf("inbox occupied = %v", live["capacity"])
	}

	lateRepo := f.project("late")
	refused = f.ctl(false, "prepare", "--repo", lateRepo, "--brief", brief, "--harness", "codex", "--approved")
	if !strings.Contains(asString(refused["error"]), "12 of 12 global execution slots") {
		t.Fatalf("late refuse = %v", refused)
	}
	pendingTask := f.readTask(asString(tasks["pending-report"]["id"]))
	f.paneState(asString(tasks["pending-report"]["pane"]), map[string]any{"agent": nil, "agent_status": "done"})
	attemptID := asString(asMap(asMap(pendingTask["execution"])["worker"])["id"])
	f.ctl(true, "execution", "park", asString(tasks["pending-report"]["id"]), "--attempt", attemptID)
	f.ctl(true, "archive", asString(tasks["pending-report"]["id"]), "--acknowledge")
	late := f.ctl(true, "prepare", "--repo", lateRepo, "--brief", brief, "--harness", "codex", "--approved")
	if asString(asMap(f.ctl(true, "update", "status")["default"])["sha"]) != sha1 {
		t.Fatalf("default after late prepare = %v, want %s", f.ctl(true, "update", "status")["default"], sha1)
	}
	if filepath.Base(f.currentPath()) != sha1 {
		t.Fatalf("current after late prepare = %s, want %s", f.currentPath(), sha1)
	}
	if asInt(asMap(asMap(late["admission"])["occupied_before"])["global"]) != 11 {
		t.Fatalf("late occupied_before = %v", late["admission"])
	}
	briefBody, err := os.ReadFile(asString(late["brief_path"]))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(briefBody), filepath.Join(f.root, "bin", "sumctl")) {
		t.Fatalf("late brief missing helper path: %s", briefBody)
	}

	later := f.calls()[callsBefore:]
	var creates [][]string
	var forbidden [][]string
	starts := 0
	for _, c := range f.calls() {
		if headsEqual(c, "agent", "start") {
			starts++
		}
	}
	for _, c := range later {
		if headsEqual(c, "agent", "start") || headsEqual(c, "worktree", "create") || headsEqual(c, "agent", "read") {
			if headsEqual(c, "worktree", "create") {
				creates = append(creates, c)
			} else {
				forbidden = append(forbidden, c)
			}
		}
	}
	if len(creates) != 1 || len(forbidden) != 0 {
		t.Fatalf("later start/create/read = creates %v extra %v", creates, forbidden)
	}
	if starts != 12 {
		t.Fatalf("agent start count = %d, want 12", starts)
	}
	for _, c := range f.calls() {
		if len(c) < 2 {
			continue
		}
		if c[0] != "agent" && c[0] != "pane" && c[0] != "worktree" {
			continue
		}
		switch c[1] {
		case "stop", "kill", "restart", "remove":
			t.Fatalf("lifecycle call %v", c)
		}
	}
	if got := f.checkouts(tasks); !checkoutEqual(got, checkouts) {
		t.Fatalf("checkouts changed: got %v want %v", got, checkouts)
	}
	wip, err := os.ReadFile(filepath.Join(asString(tasks["dirty-checkout"]["worktree"]), "wip.txt"))
	if err != nil || string(wip) != "uncommitted work\n" {
		t.Fatalf("dirty wip = %q err=%v", wip, err)
	}

	shown := map[string]map[string]any{}
	for role, task := range tasks {
		shown[role] = f.ctl(true, "show", asString(task["id"]))
	}
	if asString(asMap(asSlice(shown["open-question"]["questions"])[0])["status"]) != "answered" {
		t.Fatalf("open-question final = %v", shown["open-question"]["questions"])
	}
	if asString(asMap(asSlice(shown["answered-unapplied"]["questions"])[0])["status"]) != "applied" {
		t.Fatalf("answered-unapplied final = %v", shown["answered-unapplied"]["questions"])
	}
	if asString(asMap(asSlice(shown["closed-parent"]["questions"])[0])["status"]) != "open" {
		t.Fatalf("closed-parent final = %v", shown["closed-parent"]["questions"])
	}
	var busyStatuses []string
	for _, item := range asSlice(shown["busy-tool-call"]["questions"]) {
		busyStatuses = append(busyStatuses, asString(asMap(item)["status"]))
	}
	if strings.Join(busyStatuses, ",") != "answered" {
		t.Fatalf("busy-tool-call questions = %v", shown["busy-tool-call"]["questions"])
	}
	if asString(asMap(shown["pending-report"]["report"])["brief_revision"]) != "r1" {
		t.Fatalf("pending-report brief_revision = %v", shown["pending-report"]["report"])
	}
	if asString(asMap(shown["cooperative-a"]["report"])["brief_revision"]) != "r1" {
		t.Fatalf("cooperative-a brief_revision = %v", shown["cooperative-a"]["report"])
	}
	if asString(asMap(shown["cooperative-b"]["report"])["brief_revision"]) != "r4" {
		t.Fatalf("cooperative-b brief_revision = %v", shown["cooperative-b"]["report"])
	}
	if asString(shown["pending-report"]["status"]) != "archived" {
		t.Fatalf("pending-report status = %v", shown["pending-report"]["status"])
	}
	for _, role := range fleetRoles {
		if role == "pending-report" {
			continue
		}
		n := len(asSlice(asMap(shown[role]["versions"])["revisions"]))
		if n != 4 {
			t.Fatalf("%s revisions = %d, want 4", role, n)
		}
	}
	oldRevs := asSlice(asMap(shown["old-mcp-client"]["versions"])["revisions"])
	if asString(asMap(oldRevs[len(oldRevs)-1])["status"]) != "requested" {
		t.Fatalf("old-mcp latest status = %v", oldRevs[len(oldRevs)-1])
	}
}

func newFleetLab(t *testing.T) *fleetLab {
	t.Helper()
	source, _ := repoReference(t)
	base := t.TempDir()
	install := filepath.Join(base, "sum install dir")
	if err := os.MkdirAll(install, 0o755); err != nil {
		t.Fatal(err)
	}
	archive := exec.Command("git", "-C", source, "archive", "--format=tar", "HEAD")
	extract := exec.Command("tar", "-xf", "-", "-C", install)
	var extractErr bytes.Buffer
	extract.Stderr = &extractErr
	pipe, err := archive.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	extract.Stdin = pipe
	if err := extract.Start(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Start(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Wait(); err != nil {
		t.Fatalf("git archive: %v", err)
	}
	if err := extract.Wait(); err != nil {
		t.Fatalf("tar extract: %v\n%s", err, extractErr.String())
	}

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", install}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.name", "sum test")
	git("config", "user.email", "test@example.invalid")
	git("config", "commit.gpgsign", "false")
	git("config", "gc.auto", "0")
	git("add", ".")
	git("commit", "-q", "-m", "installation")
	origin := filepath.Join(base, "sum install dir origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init origin: %v\n%s", err, out)
	}
	git("remote", "add", "origin", origin)
	git("push", "-q", "-u", "origin", "main")
	git("remote", "set-head", "origin", "main")

	srcBin := filepath.Join(source, ".local", "bin")
	dstBin := filepath.Join(install, ".local", "bin")
	if err := os.MkdirAll(dstBin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sumctl", "herdr-mesh"} {
		src := filepath.Join(srcBin, name)
		data, err := os.ReadFile(src)
		if err != nil {
			if name == "herdr-mesh" {
				continue
			}
			t.Fatalf("plant %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dstBin, name), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	home := filepath.Join(install, ".sum")
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte("{\"schema\": 1, \"sum_version\": \"0.1.0\", \"created_at\": \"2026-09-05T00:00:00+00:00\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	goBin := pinnedGo(t)
	env := demoEnv(t, source, base)
	d := &demoLab{t: t, root: install, helper: filepath.Join(install, "bin", "sumctl"), home: home, base: base, env: env}
	d.setEnv("FAKE_PARENT_CWD", install)
	d.setEnv("SUM_GO_BIN", goBin)
	d.setEnv("SUM_SESSION", "sum-test")
	d.setEnv("FAKE_SESSION", "sum-test")
	if dir := filepath.Dir(goBin); dir != "." && dir != "" {
		d.setEnv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	for _, e := range d.env {
		key, value, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(key, "SUM_") || strings.HasPrefix(key, "HERDR_") || strings.HasPrefix(key, "FAKE_") || key == "PATH" {
			t.Setenv(key, value)
		}
	}

	f := &fleetLab{demoLab: d, source: source}
	if asString(f.ctl(true, "init")["role"]) != "coordinator" {
		t.Fatal("fleet init")
	}
	return f
}

func pinnedGo(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("SUM_GO_BIN"); v != "" {
		return v
	}
	out, err := exec.Command("mise", "where", "go").Output()
	if err == nil {
		p := filepath.Join(strings.TrimSpace(string(out)), "bin", "go")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	found, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("go not found: %v", err)
	}
	return found
}

func (f *fleetLab) git(cwd string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fleetLab) project(name string) string {
	f.t.Helper()
	repo := filepath.Join(f.base, "projects", name)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.git(repo, "init", "-b", "main")
	f.git(repo, "config", "user.name", "sum test")
	f.git(repo, "config", "user.email", "test@example.invalid")
	f.git(repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte(name+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.git(repo, "add", ".")
	f.git(repo, "commit", "-q", "-m", "fixture")
	return repo
}

func (f *fleetLab) brief() string {
	f.t.Helper()
	path := filepath.Join(f.base, "brief.md")
	if _, err := os.Stat(path); err != nil {
		if err := os.WriteFile(path, []byte("Do the approved thing.\n"), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	return path
}

func (f *fleetLab) commitUpstream(rel, text string) string {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, rel), []byte(text), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.git(f.root, "add", rel)
	f.git(f.root, "commit", "-q", "-m", rel)
	f.git(f.root, "push", "-q", "origin", "main")
	return f.git(f.root, "rev-parse", "HEAD")
}

func (f *fleetLab) fakeStatePath() string {
	return filepath.Join(f.base, "fake", "state.json")
}

func (f *fleetLab) readFakeState() map[string]any {
	f.t.Helper()
	data, err := os.ReadFile(f.fakeStatePath())
	if err != nil {
		f.t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		f.t.Fatal(err)
	}
	return state
}

func (f *fleetLab) writeFakeState(state map[string]any) {
	f.t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(f.fakeStatePath(), data, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fleetLab) paneState(pane string, changes map[string]any) {
	f.t.Helper()
	state := f.readFakeState()
	panes := asMap(state["panes"])
	row := asMap(panes[pane])
	for k, v := range changes {
		row[k] = v
	}
	panes[pane] = row
	state["panes"] = panes
	f.writeFakeState(state)
}

func (f *fleetLab) removePane(pane string) {
	f.t.Helper()
	state := f.readFakeState()
	panes := asMap(state["panes"])
	delete(panes, pane)
	state["panes"] = panes
	f.writeFakeState(state)
}

func (f *fleetLab) calls() [][]string {
	f.t.Helper()
	path := filepath.Join(f.base, "fake", "calls.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		f.t.Fatal(err)
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
			f.t.Fatal(err)
		}
		out = append(out, row.Args)
	}
	return out
}

func (f *fleetLab) checkouts(tasks map[string]map[string]any) map[string]checkoutSnap {
	f.t.Helper()
	out := map[string]checkoutSnap{}
	for _, task := range tasks {
		wt := asString(task["worktree"])
		out[asString(task["id"])] = checkoutSnap{
			head:      f.git(wt, "rev-parse", "HEAD"),
			branch:    f.git(wt, "branch", "--show-current"),
			porcelain: f.git(wt, "status", "--porcelain", "--untracked-files=all"),
		}
	}
	return out
}

func (f *fleetLab) readTask(id string) map[string]any {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.home, "tasks", id, "task.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var task map[string]any
	if err := json.Unmarshal(data, &task); err != nil {
		f.t.Fatal(err)
	}
	return task
}

func (f *fleetLab) writeTask(id string, task map[string]any) {
	f.t.Helper()
	data, err := json.Marshal(task)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, "tasks", id, "task.json"), data, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fleetLab) readVersions(id string) map[string]any {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.home, "tasks", id, "versions.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var vers map[string]any
	if err := json.Unmarshal(data, &vers); err != nil {
		f.t.Fatal(err)
	}
	return vers
}

func (f *fleetLab) patchRuntimeMCP(id string, tools int) {
	f.t.Helper()
	vers := f.readVersions(id)
	runtime := asMap(vers["runtime"])
	runtime["mcp"] = map[string]any{
		"server":  contract.MCP.Server,
		"version": contract.MCP.Version,
		"tools":   tools,
	}
	vers["runtime"] = runtime
	data, err := json.Marshal(vers)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, "tasks", id, "versions.json"), data, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fleetLab) measure(check bool, args ...string) (map[string]any, [][]string) {
	f.t.Helper()
	before := len(f.calls())
	value := f.ctl(check, args...)
	return value, f.calls()[before:]
}

func (f *fleetLab) ctlRaw(args ...string) (map[string]any, error) {
	f.t.Helper()
	cmd := exec.Command(f.helper, append([]string{"--format", "json", "--home", f.home}, args...)...)
	cmd.Env = f.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	raw := stdout.Bytes()
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = stderr.Bytes()
	}
	var out map[string]any
	if unmarshalErr := json.Unmarshal(raw, &out); unmarshalErr != nil {
		return map[string]any{"error": strings.TrimSpace(stderr.String() + stdout.String())}, err
	}
	return out, err
}

func (f *fleetLab) targetRows(view map[string]any, byID map[string]string) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, item := range asSlice(view["targets"]) {
		row := asMap(item)
		key := byID[asString(row["task"])]
		if key == "" {
			key = asString(row["target"])
		}
		out[key] = row
	}
	return out
}

func (f *fleetLab) currentPath() string {
	f.t.Helper()
	return currentRuntimePath(f.root)
}

func currentRuntimePath(root string) string {
	link := filepath.Join(root, ".local", "current")
	target, err := os.Readlink(link)
	if err != nil {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		return resolved
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return target
	}
	return abs
}

func questionKeys(task map[string]any) []string {
	var keys []string
	for _, item := range asSlice(task["questions"]) {
		keys = append(keys, asString(asMap(item)["key"]))
	}
	return keys
}

func headsEqual(c []string, a, b string) bool {
	return len(c) >= 2 && c[0] == a && c[1] == b
}

func checkoutEqual(a, b map[string]checkoutSnap) bool {
	if len(a) != len(b) {
		return false
	}
	for id, got := range a {
		want, ok := b[id]
		if !ok || got != want {
			return false
		}
	}
	return true
}

func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil {
		ra = a
	}
	if errB != nil {
		rb = b
	}
	return ra == rb
}

func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

func asMapFromOrd(v any) map[string]any {
	if obj, ok := v.(*ordjson.Object); ok && obj != nil {
		out := map[string]any{}
		for _, k := range obj.Keys() {
			val, _ := obj.Get(k)
			out[k] = val
		}
		return out
	}
	return asMap(v)
}
