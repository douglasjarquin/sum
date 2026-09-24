package factory

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func tickRow(t *testing.T, view *ordjson.Object) *ordjson.Object {
	t.Helper()
	ticks, _ := view.Get("ticks")
	list, _ := ticks.([]any)
	if len(list) == 0 {
		t.Fatalf("no ticks in %v", view)
	}
	row, _ := list[0].(*ordjson.Object)
	if row == nil {
		t.Fatalf("tick row = %v", list[0])
	}
	return row
}

func testdata(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	return filepath.Join(filepath.Dir(file), "testdata")
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	home := t.TempDir()
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	return st
}

func enroll(t *testing.T, st *store.Store, name string) {
	t.Helper()
	owner, repo, _ := strings.Cut(name, "/")
	registry := `{"schema": 1, "projects": {` +
		`"` + name + `": {"name": "` + name + `", "host": "github.com", "owner": "` + owner + `", "repo": "` + repo +
		`", "kind": "managed", "path": "/tmp/` + repo + `", "remote": "https://github.com/` + name + `.git",` +
		`"enrolled_at": "2026-01-01T00:00:00+00:00", "enrolled_by": {"machine": "m1", "session": "s1", "pane": "p1"},` +
		`"canonical_path": "/tmp/` + repo + `", "note": null}}}`
	if err := os.WriteFile(filepath.Join(st.Home, "projects.json"), []byte(registry), 0o600); err != nil {
		t.Fatal(err)
	}
}

func ctx() *ordjson.Object {
	o := ordjson.NewObject()
	o.Set("machine", "testhost")
	o.Set("session", "sum-test")
	o.Set("pane", "w1:p1")
	return o
}

func fakeGh(t *testing.T, issues []map[string]any, bodies map[string]string) string {
	t.Helper()
	root := t.TempDir()
	script := filepath.Join(testdata(t), "fake_gh.py")
	wrapper := filepath.Join(root, "gh")
	body := "#!/bin/sh\nexec python3 " + strconv.Quote(script) + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_GH_BIN", wrapper)
	t.Setenv("FAKE_GH_ROOT", root)
	if issues == nil {
		issues = []map[string]any{}
	}
	raw, _ := json.Marshal(issues)
	if err := os.WriteFile(filepath.Join(root, "issues.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if bodies != nil {
		raw, _ = json.Marshal(bodies)
		if err := os.WriteFile(filepath.Join(root, "bodies.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func issue(number int, title string, labels ...string) map[string]any {
	labelObjs := make([]any, 0, len(labels))
	for _, name := range labels {
		labelObjs = append(labelObjs, map[string]any{"name": name})
	}
	return map[string]any{"number": number, "title": title, "state": "OPEN", "labels": labelObjs}
}

func TestEnable_refusesUnknownProject(t *testing.T) {
	st := openStore(t)
	_, err := Enable(st, ctx(), EnableArgs{Project: "owner/missing"})
	if err == nil || !strings.Contains(err.Error(), "No enrolled project") {
		t.Fatalf("err = %v", err)
	}
}

func TestTick_idleWhenNoReadyIssues(t *testing.T) {
	t.Setenv(store.ClockPinEnv, "2026-01-01T00:00:00Z")
	st := openStore(t)
	enroll(t, st, "owner/app")
	fakeGh(t, nil, nil)
	if _, err := Enable(st, ctx(), EnableArgs{Project: "owner/app", Ready: ReadyLabel}); err != nil {
		t.Fatal(err)
	}
	view, err := Tick(st, st.Home, "owner/app")
	if err != nil {
		t.Fatal(err)
	}
	ticks, _ := view.Get("ticks")
	list, _ := ticks.([]any)
	if len(list) != 1 {
		t.Fatalf("ticks = %v", ticks)
	}
	row := list[0].(*ordjson.Object)
	action, _ := row.Get("action")
	if action != ActionIdle {
		t.Fatalf("action = %v", action)
	}
	next, _ := row.Get("next_tick_at")
	if next != "2026-01-01T00:05:00+00:00" {
		t.Fatalf("next_tick_at = %v", next)
	}
}

func TestTick_dispatchThenOccupied(t *testing.T) {
	t.Setenv(store.ClockPinEnv, "2026-01-01T00:00:00Z")
	st := openStore(t)
	enroll(t, st, "owner/app")
	fakeGh(t, []map[string]any{issue(7, "First ready", "ready"), issue(9, "Second ready", "ready")}, nil)
	if _, err := Enable(st, ctx(), EnableArgs{Project: "owner/app", Ready: ReadyLabel, Label: "ready"}); err != nil {
		t.Fatal(err)
	}
	view, err := Tick(st, st.Home, "owner/app")
	if err != nil {
		t.Fatal(err)
	}
	row := tickRow(t, view)
	if got, _ := row.Get("action"); got != ActionDispatch {
		t.Fatalf("action = %v", got)
	}
	if got := asInt(get(row, "issue")); got != 7 {
		t.Fatalf("issue = %d", got)
	}
	if _, err := Claim(st, ctx(), st.Home, ClaimArgs{Project: "owner/app", Issue: 7, Task: "t-aaaaaaaaaaaa"}); err != nil {
		t.Fatal(err)
	}
	view, err = Tick(st, st.Home, "owner/app")
	if err != nil {
		t.Fatal(err)
	}
	row = tickRow(t, view)
	if got, _ := row.Get("action"); got != ActionOccupied {
		t.Fatalf("after claim action = %v", got)
	}
}

func TestTick_issuesPicksOldestOpen(t *testing.T) {
	st := openStore(t)
	enroll(t, st, "owner/app")
	root := fakeGh(t, []map[string]any{issue(40, "newer"), issue(12, "oldest"), issue(25, "middle", ClaimLabel)}, nil)
	if _, err := Enable(st, ctx(), EnableArgs{Project: "owner/app", Ready: ReadyIssues}); err != nil {
		t.Fatal(err)
	}
	view, err := Tick(st, st.Home, "owner/app")
	if err != nil {
		t.Fatal(err)
	}
	row := tickRow(t, view)
	if got, _ := row.Get("action"); got != ActionDispatch {
		t.Fatalf("action = %v", got)
	}
	if got := asInt(get(row, "issue")); got != 12 {
		t.Fatalf("issue = %d want 12", got)
	}
	if got, _ := row.Get("ready_signal"); got != ReadyIssues {
		t.Fatalf("ready_signal = %v", got)
	}
	calls, _ := os.ReadFile(filepath.Join(root, "calls.jsonl"))
	text := string(calls)
	if !strings.Contains(text, "--sort") || !strings.Contains(text, "created") || !strings.Contains(text, "--order") || !strings.Contains(text, "asc") || !strings.Contains(text, "--paginate") {
		t.Fatalf("issue list missing created-asc pagination: %s", text)
	}
}

func TestGhProjectReady_keepsOnlyEnrolledRepository(t *testing.T) {
	st := openStore(t)
	root := fakeGh(t, nil, nil)
	items := []map[string]any{
		{"status": "Ready", "number": 54, "title": "photo", "repository": "https://github.com/cofactorworks/ilovethatphoto"},
		{"status": "Ready", "number": 146, "title": "tape", "repository": "cofactorworks/cuttingtape"},
		{"status": "Ready", "number": 960, "title": "uptime", "repository": "https://github.com/cofactorworks/niceuptime"},
		{"status": "Ready", "number": 1, "title": "missing-repo"},
		{"status": "Ready", "number": 200, "title": "baas", "repository": "https://github.com/CofactorWorks/NiceBaaS"},
	}
	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "project_items.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ghProjectReady(st.Home, "cofactorworks/nicebaas", 7, "Ready")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Number != 200 {
		t.Fatalf("got %#v, want only enrolled #200", got)
	}
}

func TestTick_roadmapOrderSkipsClosedAndSkipped(t *testing.T) {
	st := openStore(t)
	enroll(t, st, "owner/app")
	body := `| Order | Issue |\n| --- | --- |\n| 01 | #10 |\n| 02 | #11 |\n| 03 | #12 |\n`
	fakeGh(t, []map[string]any{issue(11, "second"), issue(12, "third")}, map[string]string{"10": body})
	if _, err := Enable(st, ctx(), EnableArgs{Project: "owner/app", Ready: ReadyRoadmap, RoadmapIssue: 10, Skip: []int{11}}); err != nil {
		t.Fatal(err)
	}
	view, err := Tick(st, st.Home, "owner/app")
	if err != nil {
		t.Fatal(err)
	}
	row := tickRow(t, view)
	if got, _ := row.Get("action"); got != ActionDispatch {
		t.Fatalf("action = %v want dispatch: %v", got, row)
	}
	if got := asInt(get(row, "issue")); got != 12 {
		t.Fatalf("issue = %d want 12", got)
	}
}

func TestClaim_writesLabelAndComment(t *testing.T) {
	st := openStore(t)
	enroll(t, st, "owner/app")
	ghRoot := fakeGh(t, []map[string]any{issue(3, "ready", "ready")}, nil)
	if _, err := Enable(st, ctx(), EnableArgs{Project: "owner/app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(st, ctx(), st.Home, ClaimArgs{Project: "owner/app", Issue: 3}); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(filepath.Join(ghRoot, "calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(calls)
	if !strings.Contains(text, `"issue", "edit"`) || !strings.Contains(text, ClaimLabel) {
		t.Fatalf("missing edit/label in %s", text)
	}
	if !strings.Contains(text, `"issue", "comment"`) {
		t.Fatalf("missing comment in %s", text)
	}
	status, err := Status(st, "owner/app")
	if err != nil {
		t.Fatal(err)
	}
	proj := asObject(get(status, "project"))
	if asInt(get(proj, "held")) != 1 {
		t.Fatalf("held = %v", get(proj, "held"))
	}
}

func TestRelease_continueFreesGatedLane(t *testing.T) {
	st := openStore(t)
	enroll(t, st, "owner/app")
	fakeGh(t, []map[string]any{issue(4, "ready", "ready")}, nil)
	if _, err := Enable(st, ctx(), EnableArgs{Project: "owner/app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(st, ctx(), st.Home, ClaimArgs{Project: "owner/app", Issue: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := Release(st, ReleaseArgs{Project: "owner/app", Issue: 4, Reason: ReasonGated}); err != nil {
		t.Fatal(err)
	}
	status, _ := Status(st, "owner/app")
	if asInt(get(asObject(get(status, "project")), "held")) != 1 {
		t.Fatal("gated lane should stay occupied without --continue")
	}
	if _, err := Release(st, ReleaseArgs{Project: "owner/app", Issue: 4, Reason: ReasonGated, Continue: true}); err != nil {
		t.Fatal(err)
	}
	status, _ = Status(st, "owner/app")
	if asInt(get(asObject(get(status, "project")), "held")) != 0 {
		t.Fatal("continue should free the gated lane")
	}
}

func TestLaneLimit_refusesSecondClaim(t *testing.T) {
	st := openStore(t)
	enroll(t, st, "owner/app")
	fakeGh(t, []map[string]any{issue(1, "a", "ready"), issue(2, "b", "ready")}, nil)
	if _, err := Enable(st, ctx(), EnableArgs{Project: "owner/app", Lanes: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(st, ctx(), st.Home, ClaimArgs{Project: "owner/app", Issue: 1}); err != nil {
		t.Fatal(err)
	}
	_, err := Claim(st, ctx(), st.Home, ClaimArgs{Project: "owner/app", Issue: 2})
	if err == nil || !strings.Contains(err.Error(), "lane limit") {
		t.Fatalf("err = %v", err)
	}
}

func TestMergeCheck_unauthorizedIsHumanGate(t *testing.T) {
	st := openStore(t)
	task := ordjson.NewObject()
	task.Set("schema", json.Number("1"))
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("status", "running")
	task.Set("evidence", []any{})
	policy := ordjson.NewObject()
	identity := ordjson.NewObject()
	identity.Set("owner", "other")
	identity.Set("repo", "app")
	identity.Set("name", "other/app")
	policy.Set("project_identity", identity)
	policy.Set("status", "standardized")
	task.Set("verification_policy", policy)
	if err := os.MkdirAll(filepath.Join(st.Home, "tasks", "t-aaaaaaaaaaaa"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	view, err := MergeCheck(st, "t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if asString(get(view, "confidence")) != "human-gate" {
		t.Fatalf("confidence = %v", get(view, "confidence"))
	}
}

func gitWorktree(t *testing.T) (path, sha string) {
	t.Helper()
	path = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", path}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "--quiet")
	if err := os.WriteFile(filepath.Join(path, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README")
	run("commit", "--quiet", "-m", "init")
	return path, run("rev-parse", "HEAD")
}

func TestMergeCheck_highConfidenceAuthorized(t *testing.T) {
	st := openStore(t)
	worktree, sha := gitWorktree(t)
	task := ordjson.NewObject()
	task.Set("schema", json.Number("1"))
	task.Set("id", "t-bbbbbbbbbbbb")
	task.Set("status", "running")
	task.Set("worktree", worktree)
	task.Set("candidate", sha)
	policy := ordjson.NewObject()
	identity := ordjson.NewObject()
	identity.Set("owner", "cofactorworks")
	identity.Set("repo", "nicebaas")
	identity.Set("name", "cofactorworks/nicebaas")
	policy.Set("project_identity", identity)
	policy.Set("status", "not-yet-standardized")
	task.Set("verification_policy", policy)
	pr := ordjson.NewObject()
	prIdent := ordjson.NewObject()
	prIdent.Set("number", json.Number("12"))
	prIdent.Set("repository", "cofactorworks/nicebaas")
	prIdent.Set("head_sha", sha)
	pr.Set("identity", prIdent)
	pr.Set("complete", true)
	pr.Set("number", json.Number("12"))
	pr.Set("repository", "cofactorworks/nicebaas")
	task.Set("pr", pr)
	rel := filepath.Join(".artifacts", "evidence", "r1", "demo", "comparison.json")
	writeComparison(t, worktree, rel, sha, "red-green")
	handoff := ordjson.NewObject()
	handoff.Set("kind", "handoff")
	handoff.Set("source", "worker")
	handoff.Set("current", true)
	handoff.Set("candidate", sha)
	handoff.Set("artifacts", []any{filepath.ToSlash(rel)})
	root := ordjson.NewObject()
	root.Set("kind", "verification")
	root.Set("source", "coordinator")
	root.Set("current", true)
	root.Set("candidate", sha)
	root.Set("result", "pass")
	root.Set("outcome", "pass")
	review := ordjson.NewObject()
	review.Set("kind", "review")
	review.Set("source", "reviewer")
	review.Set("current", true)
	review.Set("candidate", sha)
	review.Set("verdict", "approve")
	task.Set("evidence", []any{handoff, root, review})
	if err := os.MkdirAll(filepath.Join(st.Home, "tasks", "t-bbbbbbbbbbbb"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	pipe := ordjson.NewObject()
	pipe.Set("schema", json.Number("1"))
	pipe.Set("task", "t-bbbbbbbbbbbb")
	pipe.Set("candidate", sha)
	var rows []any
	for _, stage := range []string{"intent", "rebase", "review", "test", "document", "lint", "push", "pr", "ci"} {
		row := ordjson.NewObject()
		row.Set("stage", stage)
		row.Set("status", "pass")
		row.Set("result", "Passed")
		rows = append(rows, row)
	}
	pipe.Set("rows", rows)
	if err := ordjson.WriteFile(filepath.Join(st.Home, "tasks", "t-bbbbbbbbbbbb", "pipeline.json"), pipe); err != nil {
		t.Fatal(err)
	}
	view, err := MergeCheck(st, "t-bbbbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	if asString(get(view, "confidence")) != "high" {
		t.Fatalf("confidence = %v view=%v", get(view, "confidence"), view)
	}
}

func writeComparison(t *testing.T, worktree, rel, sha, verdict string) {
	t.Helper()
	path := filepath.Join(worktree, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"schema": 1, "scenario": "demo", "verdict": verdict,
		"after": map[string]any{"sha": sha}, "candidate": map[string]any{"sha": sha},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePipeline(t *testing.T, st *store.Store, taskID, sha string, statuses map[string]string) {
	t.Helper()
	pipe := ordjson.NewObject()
	pipe.Set("schema", json.Number("1"))
	pipe.Set("task", taskID)
	pipe.Set("candidate", sha)
	var rows []any
	for _, stage := range []string{"intent", "rebase", "review", "test", "document", "lint", "push", "pr", "ci"} {
		status := "pass"
		if statuses != nil && statuses[stage] != "" {
			status = statuses[stage]
		}
		row := ordjson.NewObject()
		row.Set("stage", stage)
		row.Set("status", status)
		row.Set("result", status)
		rows = append(rows, row)
	}
	pipe.Set("rows", rows)
	if err := ordjson.WriteFile(filepath.Join(st.Home, "tasks", taskID, "pipeline.json"), pipe); err != nil {
		t.Fatal(err)
	}
}

func TestMergeCheck_missingComparisonIsHumanGate(t *testing.T) {
	st := openStore(t)
	worktree, sha := gitWorktree(t)
	task := mergeTask(t, st, worktree, sha, "t-cccccccccccc")
	if err := os.Remove(filepath.Join(worktree, ".artifacts", "evidence", "r1", "demo", "comparison.json")); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	view, err := MergeCheck(st, "t-cccccccccccc")
	if err != nil {
		t.Fatal(err)
	}
	if asString(get(view, "confidence")) != "human-gate" {
		t.Fatalf("confidence = %v", get(view, "confidence"))
	}
}

func TestMergeCheck_unboundReviewIsHumanGate(t *testing.T) {
	st := openStore(t)
	worktree, sha := gitWorktree(t)
	task := mergeTask(t, st, worktree, sha, "t-dddddddddddd")
	raw, _ := task.Get("evidence")
	list, _ := raw.([]any)
	for _, item := range list {
		row := asObject(item)
		if asString(get(row, "kind")) == "review" {
			row.Set("candidate", "")
		}
	}
	if err := st.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	view, err := MergeCheck(st, "t-dddddddddddd")
	if err != nil {
		t.Fatal(err)
	}
	if asString(get(view, "confidence")) != "human-gate" {
		t.Fatalf("confidence = %v", get(view, "confidence"))
	}
}

func TestMergeCheck_failedTestGateIsHumanGate(t *testing.T) {
	st := openStore(t)
	worktree, sha := gitWorktree(t)
	mergeTask(t, st, worktree, sha, "t-eeeeeeeeeeee")
	writePipeline(t, st, "t-eeeeeeeeeeee", sha, map[string]string{"test": "fail"})
	view, err := MergeCheck(st, "t-eeeeeeeeeeee")
	if err != nil {
		t.Fatal(err)
	}
	if asString(get(view, "confidence")) != "human-gate" {
		t.Fatalf("confidence = %v", get(view, "confidence"))
	}
}

func TestMerge_humanGateDoesNotCallGh(t *testing.T) {
	st := openStore(t)
	root := fakeGh(t, nil, nil)
	task := ordjson.NewObject()
	task.Set("schema", json.Number("1"))
	task.Set("id", "t-ffffffffffff")
	task.Set("status", "running")
	task.Set("evidence", []any{})
	policy := ordjson.NewObject()
	identity := ordjson.NewObject()
	identity.Set("owner", "other")
	identity.Set("repo", "app")
	identity.Set("name", "other/app")
	policy.Set("project_identity", identity)
	task.Set("verification_policy", policy)
	if err := os.MkdirAll(filepath.Join(st.Home, "tasks", "t-ffffffffffff"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	_, err := Merge(st, st.Home, "t-ffffffffffff")
	if err == nil || !strings.Contains(err.Error(), "human-gate") {
		t.Fatalf("err = %v", err)
	}
	calls, _ := os.ReadFile(filepath.Join(root, "calls.jsonl"))
	if strings.Contains(string(calls), `"pr", "merge"`) {
		t.Fatalf("human-gate invoked gh pr merge: %s", calls)
	}
}

func TestMerge_highPassesMatchHeadCommit(t *testing.T) {
	st := openStore(t)
	worktree, sha := gitWorktree(t)
	enroll(t, st, "cofactorworks/nicebaas")
	root := fakeGh(t, []map[string]any{issue(12, "ready", "ready")}, nil)
	if _, err := Enable(st, ctx(), EnableArgs{Project: "cofactorworks/nicebaas"}); err != nil {
		t.Fatal(err)
	}
	mergeTask(t, st, worktree, sha, "t-bbbbbbbbbbbb")
	if _, err := Claim(st, ctx(), st.Home, ClaimArgs{Project: "cofactorworks/nicebaas", Issue: 12, Task: "t-bbbbbbbbbbbb"}); err != nil {
		t.Fatal(err)
	}
	view, err := Merge(st, st.Home, "t-bbbbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	if !asBool(get(view, "merged")) {
		t.Fatalf("merged = %v", get(view, "merged"))
	}
	calls, err := os.ReadFile(filepath.Join(root, "calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(calls)
	if !strings.Contains(text, `"pr", "merge"`) || !strings.Contains(text, "--match-head-commit") || !strings.Contains(text, sha) {
		t.Fatalf("missing match-head-commit in %s", text)
	}
}

func TestMerge_refusesWithoutLane(t *testing.T) {
	st := openStore(t)
	worktree, sha := gitWorktree(t)
	enroll(t, st, "cofactorworks/nicebaas")
	root := fakeGh(t, nil, nil)
	if _, err := Enable(st, ctx(), EnableArgs{Project: "cofactorworks/nicebaas"}); err != nil {
		t.Fatal(err)
	}
	mergeTask(t, st, worktree, sha, "t-bbbbbbbbbbbb")
	_, err := Merge(st, st.Home, "t-bbbbbbbbbbbb")
	if err == nil || !strings.Contains(err.Error(), "not occupying a factory lane") {
		t.Fatalf("err = %v", err)
	}
	calls, _ := os.ReadFile(filepath.Join(root, "calls.jsonl"))
	if strings.Contains(string(calls), `"pr", "merge"`) {
		t.Fatalf("lane refusal invoked gh pr merge: %s", calls)
	}
}

func TestClaim_commentFailureRemovesLabel(t *testing.T) {
	st := openStore(t)
	enroll(t, st, "owner/app")
	root := fakeGh(t, []map[string]any{issue(3, "ready", "ready")}, nil)
	if err := os.WriteFile(filepath.Join(root, "comment_error.json"), []byte(`{"message":"boom"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Enable(st, ctx(), EnableArgs{Project: "owner/app"}); err != nil {
		t.Fatal(err)
	}
	_, err := Claim(st, ctx(), st.Home, ClaimArgs{Project: "owner/app", Issue: 3})
	if err == nil || !strings.Contains(err.Error(), "removed") {
		t.Fatalf("err = %v", err)
	}
	calls, _ := os.ReadFile(filepath.Join(root, "calls.jsonl"))
	if !strings.Contains(string(calls), "--remove-label") {
		t.Fatalf("missing remove-label in %s", calls)
	}
}

func mergeTask(t *testing.T, st *store.Store, worktree, sha, taskID string) *ordjson.Object {
	t.Helper()
	task := ordjson.NewObject()
	task.Set("schema", json.Number("1"))
	task.Set("id", taskID)
	task.Set("status", "running")
	task.Set("worktree", worktree)
	task.Set("candidate", sha)
	policy := ordjson.NewObject()
	identity := ordjson.NewObject()
	identity.Set("owner", "cofactorworks")
	identity.Set("repo", "nicebaas")
	identity.Set("name", "cofactorworks/nicebaas")
	policy.Set("project_identity", identity)
	policy.Set("status", "not-yet-standardized")
	task.Set("verification_policy", policy)
	pr := ordjson.NewObject()
	prIdent := ordjson.NewObject()
	prIdent.Set("number", json.Number("12"))
	prIdent.Set("repository", "cofactorworks/nicebaas")
	prIdent.Set("head_sha", sha)
	pr.Set("identity", prIdent)
	pr.Set("complete", true)
	pr.Set("number", json.Number("12"))
	pr.Set("repository", "cofactorworks/nicebaas")
	task.Set("pr", pr)
	rel := filepath.Join(".artifacts", "evidence", "r1", "demo", "comparison.json")
	writeComparison(t, worktree, rel, sha, "red-green")
	handoff := ordjson.NewObject()
	handoff.Set("kind", "handoff")
	handoff.Set("source", "worker")
	handoff.Set("current", true)
	handoff.Set("candidate", sha)
	handoff.Set("artifacts", []any{filepath.ToSlash(rel)})
	root := ordjson.NewObject()
	root.Set("kind", "verification")
	root.Set("source", "coordinator")
	root.Set("current", true)
	root.Set("candidate", sha)
	root.Set("result", "pass")
	root.Set("outcome", "pass")
	review := ordjson.NewObject()
	review.Set("kind", "review")
	review.Set("source", "reviewer")
	review.Set("current", true)
	review.Set("candidate", sha)
	review.Set("verdict", "approve")
	task.Set("evidence", []any{handoff, root, review})
	if err := os.MkdirAll(filepath.Join(st.Home, "tasks", taskID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	writePipeline(t, st, taskID, sha, nil)
	return task
}

func TestRoadmapOrder(t *testing.T) {
	body := "start here\n| Order | Issue |\n| --- | --- |\n| 01 | #137 |\n| 02 | #138 |\n"
	got := roadmapOrder(body)
	if len(got) != 2 || got[0] != 137 || got[1] != 138 {
		t.Fatalf("got %v", got)
	}
}
