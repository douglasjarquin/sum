package lifecycle

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/panes"
	"github.com/douglasjarquin/sum/go/internal/pipeline"
	"github.com/douglasjarquin/sum/go/internal/prcmd"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	sweepTaskID   = "t-cccccccccccc"
	sweepWorkerID = "x-cccccccccccc"
)

type sweepLab struct {
	t        *testing.T
	store    *store.Store
	ctx      *ordjson.Object
	host     string
	home     string
	repo     string
	checkout string
	head     string
	runtime  string
	ghRoot   string
	herdr    string
}

func newSweepLab(t *testing.T) *sweepLab {
	t.Helper()
	t.Setenv("SUM_HOME", "")
	t.Setenv("SUM_SESSION", "sum-test")
	t.Setenv("HERDR_SESSION", "sum-test")
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w-parent:p1")
	home := t.TempDir()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	herdrRoot := filepath.Join(home, "fake-herdr")
	lsofRoot := filepath.Join(home, "fake-lsof")
	ghRoot := filepath.Join(home, "fake-gh")
	for _, dir := range []string{herdrRoot, lsofRoot, ghRoot} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SUM_HERDR_BIN", filepath.Join(root, "tests", "fixtures", "herdr.py"))
	t.Setenv("SUM_PS_BIN", filepath.Join(root, "tests", "fixtures", "ps.py"))
	t.Setenv("SUM_LSOF_BIN", filepath.Join(root, "tests", "fixtures", "lsof.py"))
	t.Setenv("SUM_GH_BIN", filepath.Join(root, "tests", "fixtures", "gh.py"))
	t.Setenv("FAKE_HERDR_ROOT", herdrRoot)
	t.Setenv("FAKE_SESSION", "sum-test")
	t.Setenv("FAKE_PARENT_CWD", home)
	t.Setenv("FAKE_PARENT_STATUS", "idle")
	t.Setenv("FAKE_PARENT_KIND", "claude")
	t.Setenv("FAKE_LSOF_ROOT", lsofRoot)
	t.Setenv("FAKE_GH_ROOT", ghRoot)
	if err := os.WriteFile(filepath.Join(lsofRoot, "cwds.json"), []byte(`{"processes":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	ctx := ordjson.NewObject()
	ctx.Set("session", "sum-test")
	ctx.Set("pane", "w-parent:p1")
	ctx.Set("machine", host)
	ctx.Set("cwd", home)
	owner := ordjson.NewObject()
	owner.Set("session", "sum-test")
	owner.Set("pane", "w-parent:p1")
	owner.Set("machine", host)
	owner.Set("role", "coordinator")
	// The occupant the fake Herdr reports for the coordinator pane, as init would have recorded it.
	owner.Set("incarnation", incarnation.Evidence{Terminal: "term-w-parent:p1"}.Record(store.Now()))
	if err := ordjson.WriteFile(filepath.Join(home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Register(store.EndpointFromContext(ctx), "coordinator", nil, nil); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	for _, argv := range [][]string{
		{"git", "-C", repo, "init", "-b", "main"},
		{"git", "-C", repo, "config", "user.email", "test@example.invalid"},
		{"git", "-C", repo, "config", "user.name", "sum test"},
		{"git", "-C", repo, "commit", "--allow-empty", "-m", "base"},
	} {
		if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, out)
		}
	}
	checkout := filepath.Join(t.TempDir(), "checkout")
	add := exec.Command("git", "-C", repo, "worktree", "add", checkout, "-b", "sum/t-cccccccccccc")
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	head, err := exec.Command("git", "-C", checkout, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return &sweepLab{t: t, store: st, ctx: ctx, host: host, home: home, repo: repo,
		checkout: checkout, head: strings.TrimSpace(string(head)), runtime: root, ghRoot: ghRoot, herdr: herdrRoot}
}

// saveTask writes a task whose pane and workspace are already gone: the worker
// attempt is released, the handoff is recorded, and the PR record carries the
// given state. With state "merged" it is cleanup-pending; with "open" the sweep
// must first observe the merge through gh.
func (l *sweepLab) saveTask(pr string) {
	l.t.Helper()
	raw := fmt.Sprintf(`{
"schema": 1, "id": %q, "status": "running", "repository": %q,
"machine": %q, "session": "sum-test", "pane": "w-worker:p1", "workspace": "w-worker",
"worktree": %q, "branch": "sum/t-cccccccccccc",
"questions": [],
"evidence": [
  {"kind": "handoff", "source": "worker", "candidate": %q, "at": "2026-01-01T00:00:00+00:00",
   "endpoint": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1"}},
  {"kind": "report", "source": "worker", "candidate": %q, "at": "2026-01-01T00:00:00+00:00",
   "endpoint": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1"}}
],
"report": {"text": "done", "candidate": %q, "submitted_at": "2026-01-01T00:00:00+00:00"}, "notice": null, "attention": [],
"brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship",
"pr": %s,
"execution": {"schema": 1,
  "worker": {"id": %q, "kind": "worker", "state": "released", "generation": 1,
             "owner": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1"}, "checkout": %q,
             "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": []},
  "verifiers": []}
}`, sweepTaskID, l.repo, l.host, l.checkout, l.head, l.host, l.head, l.host, l.head, pr, sweepWorkerID, l.host, l.checkout)
	value, err := ordjson.Decode([]byte(raw))
	if err != nil {
		l.t.Fatal(err)
	}
	obj, _ := value.(*ordjson.Object)
	if err := l.store.SaveTask(obj); err != nil {
		l.t.Fatal(err)
	}
}

func (l *sweepLab) mergedPR() string {
	return fmt.Sprintf(`{"complete": true, "merged_for_task": true, "state": "merged",
		"observed_at": "2026-01-02T00:00:00+00:00",
		"identity": {"number": 7, "url": "https://github.com/douglasjarquin/project/pull/7",
			"head_sha": %q, "head_branch": "sum/t-cccccccccccc", "base_branch": "main"},
		"merge_commit": %q, "findings": []}`, l.head, l.head)
}

func (l *sweepLab) openPR() string {
	return fmt.Sprintf(`{"complete": true, "merged_for_task": false, "state": "open",
		"observed_at": "2026-01-02T00:00:00+00:00",
		"identity": {"number": 7, "url": "https://github.com/douglasjarquin/project/pull/7",
			"head_sha": %q, "head_branch": "sum/t-cccccccccccc", "base_branch": "main"},
		"findings": []}`, l.head)
}

func (l *sweepLab) writeGHScenario(state, mergeCommit string) {
	l.t.Helper()
	scenario := fmt.Sprintf(`{"number": 7, "repository": "douglasjarquin/project", "state": %q,
		"head_branch": "sum/t-cccccccccccc", "head_sha": %q, "base_branch": "main",
		"merge_commit": %q, "merged_at": "2026-01-02T00:00:00+00:00"}`, state, l.head, mergeCommit)
	if err := os.WriteFile(filepath.Join(l.ghRoot, "pr.json"), []byte(scenario), 0o600); err != nil {
		l.t.Fatal(err)
	}
}

func sweepRow(rows []any, action string) *ordjson.Object {
	for _, raw := range rows {
		row, _ := raw.(*ordjson.Object)
		if a, _ := row.Get("action"); a == action {
			return row
		}
	}
	return nil
}

func (l *sweepLab) sweep(opts SweepOpts) (rows, deferred []any) {
	l.t.Helper()
	opts.SumctlPath = "sumctl"
	result, err := Sweep(l.store, l.ctx, l.runtime, opts)
	if err != nil {
		l.t.Fatal(err)
	}
	r, _ := result.Get("rows")
	d, _ := result.Get("deferred")
	return r.([]any), d.([]any)
}

func (l *sweepLab) writeLivePanes(extra map[string]any) {
	l.t.Helper()
	panes := map[string]any{
		"w-parent:p1": map[string]any{
			"pane_id": "w-parent:p1", "cwd": l.home, "workspace_id": "w-parent",
			"agent_status": "idle", "agent": "claude", "terminal_id": "term-w-parent:p1",
		},
		"w-worker:p1": map[string]any{
			"pane_id": "w-worker:p1", "cwd": l.checkout, "workspace_id": "w-worker",
			"agent_status": "idle", "agent": "codex", "name": "worker",
			"shell_pid": 4242, "created": true, "terminal_id": "term-w-worker:p1",
			"processes": []any{},
		},
	}
	for id, pane := range extra {
		panes[id] = pane
	}
	state := map[string]any{
		"panes": panes,
		"workspaces": map[string]any{
			"w-parent": map[string]any{"workspace_id": "w-parent", "label": "coordinator", "worktree": nil},
			"w-worker": map[string]any{
				"workspace_id": "w-worker", "label": "task",
				"worktree": map[string]any{"checkout_path": l.checkout, "repo_root": l.repo},
			},
		},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.herdr, "state.json"), raw, 0o600); err != nil {
		l.t.Fatal(err)
	}
}

func (l *sweepLab) paneExists(id string) bool {
	l.t.Helper()
	raw, err := os.ReadFile(filepath.Join(l.herdr, "state.json"))
	if err != nil {
		return false
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		return false
	}
	panes, _ := state["panes"].(map[string]any)
	_, ok := panes[id]
	return ok
}

func (l *sweepLab) ghCalls() int {
	raw, err := os.ReadFile(filepath.Join(l.ghRoot, "calls.jsonl"))
	if err != nil {
		return 0
	}
	return strings.Count(string(raw), "\n")
}

// The sweep is the coordinator's explicit command: any other context is refused and changes nothing.
func TestSweep_refusesNonCoordinatorContext(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.mergedPR())
	worker := ordjson.NewObject()
	worker.Set("session", "sum-test")
	worker.Set("pane", "w-worker:p1")
	worker.Set("machine", l.host)
	if _, err := Sweep(l.store, worker, l.runtime, SweepOpts{}); err == nil {
		t.Fatal("worker-context sweep was not refused")
	}
	if _, err := Sweep(l.store, nil, l.runtime, SweepOpts{}); err == nil {
		t.Fatal("context-less sweep was not refused")
	}
	if _, err := os.Stat(l.checkout); err != nil {
		t.Fatal("a refused sweep removed the checkout")
	}
}

// A task whose recorded PR is already merged is cleaned up by the explicit sweep.
func TestSweep_appliesCleanupForMergedTask(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.mergedPR())
	rows, _ := l.sweep(SweepOpts{})
	row := sweepRow(rows, "cleanup")
	if row == nil {
		t.Fatalf("no cleanup row in %v", rows)
	}
	if state, _ := row.Get("state"); state != "complete" {
		t.Fatalf("cleanup state = %v err=%v", state, func() any { v, _ := row.Get("error"); return v }())
	}
	if _, err := os.Stat(l.checkout); !os.IsNotExist(err) {
		t.Fatalf("checkout still exists: %v", err)
	}
	task, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := task.Get("status"); status != "archived" {
		t.Fatalf("task status = %v, want archived", status)
	}
	// Idempotent: a second sweep finds nothing to do.
	rows, deferred := l.sweep(SweepOpts{})
	if len(rows) != 0 || len(deferred) != 0 {
		t.Fatalf("second sweep = %v %v", rows, deferred)
	}
}

// A recorded open PR gets one structured observation; when gh reports the merge, cleanup runs in the same sweep.
func TestSweep_observesOpenPRThenCleans(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	l.writeGHScenario("MERGED", "0123456789abcdef0123456789abcdef01234567")
	rows, _ := l.sweep(SweepOpts{})
	obs := sweepRow(rows, "pr-observe")
	if obs == nil {
		t.Fatalf("no pr-observe row in %v", rows)
	}
	if state, _ := obs.Get("state"); state != "merged" {
		t.Fatalf("pr-observe state = %v err=%v", state, func() any { v, _ := obs.Get("error"); return v }())
	}
	clean := sweepRow(rows, "cleanup")
	if clean == nil {
		t.Fatalf("no cleanup row after merge observation in %v", rows)
	}
	if state, _ := clean.Get("state"); state != "complete" {
		t.Fatalf("cleanup state = %v err=%v", state, func() any { v, _ := clean.Get("error"); return v }())
	}
}

// A still-open PR is observed but never triggers cleanup.
func TestSweep_openPRStaysUnclean(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	l.writeGHScenario("OPEN", "")
	rows, _ := l.sweep(SweepOpts{})
	if obs := sweepRow(rows, "pr-observe"); obs == nil {
		t.Fatalf("no pr-observe row in %v", rows)
	}
	if clean := sweepRow(rows, "cleanup"); clean != nil {
		t.Fatalf("cleanup ran for an open PR: %v", clean)
	}
	if _, err := os.Stat(l.checkout); err != nil {
		t.Fatal("checkout was removed for an open PR")
	}
}

// A zero budget starts nothing: every task is deferred with its exact command and nothing is observed or removed.
func TestSweep_zeroBudgetDefersEverything(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.mergedPR())
	rows, deferred := l.sweep(SweepOpts{Budget: -1})
	if len(rows) != 0 || len(deferred) != 1 {
		t.Fatalf("rows=%v deferred=%v", rows, deferred)
	}
	next, _ := deferred[0].(*ordjson.Object).Get("next")
	if !strings.Contains(fmt.Sprint(next), "sweep --task "+sweepTaskID) {
		t.Fatalf("deferred next = %v", next)
	}
	if _, err := os.Stat(l.checkout); err != nil {
		t.Fatal("a deferred task's checkout was removed")
	}
	if l.ghCalls() != 0 {
		t.Fatal("a deferred sweep called gh")
	}
}

// After gh times out once, the sweep observes no further PR in that pass, and cleanup rows still run.
func TestSweep_hangingGHDefersLaterObservations(t *testing.T) {
	l := newSweepLab(t)
	old := [2]time.Duration{prcmd.GHBound, pipeline.DefaultGHBound}
	prcmd.GHBound, pipeline.DefaultGHBound = time.Second, time.Second
	t.Cleanup(func() { prcmd.GHBound, pipeline.DefaultGHBound = old[0], old[1] })
	l.saveTask(l.openPR())
	second := l.cloneTask("t-dddddddddddd", l.openPR(), "2026-01-03T00:00:00+00:00")
	merged := l.cloneTask("t-eeeeeeeeeeee", l.mergedPR(), "2026-01-04T00:00:00+00:00")
	if err := os.WriteFile(filepath.Join(l.ghRoot, "pr.json"), []byte(`{"hang": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, deferred := l.sweep(SweepOpts{Budget: time.Minute})
	obs := sweepRow(rows, "pr-observe")
	if obs == nil {
		t.Fatalf("no pr-observe row: %v", rows)
	}
	if state, _ := obs.Get("state"); state != "error" {
		t.Fatalf("hung observation state = %v", state)
	}
	var deferredIDs []string
	for _, raw := range deferred {
		row := raw.(*ordjson.Object)
		id, _ := row.Get("task")
		next, _ := row.Get("next")
		if !strings.Contains(fmt.Sprint(next), "pr reconcile") {
			t.Fatalf("deferred next = %v", next)
		}
		deferredIDs = append(deferredIDs, fmt.Sprint(id))
	}
	if len(deferredIDs) != 1 || deferredIDs[0] != second {
		t.Fatalf("deferred = %v, want only %s", deferredIDs, second)
	}
	if clean := sweepRow(rows, "cleanup"); clean == nil {
		t.Fatalf("cleanup for %s did not run after the gh timeout: %v", merged, rows)
	}
}

// A PR whose observation keeps failing does not head every sweep: the failed attempt is recorded, so the next sweep
// observes the PR it has not tried yet before retrying the unreachable one.
func TestSweep_failedObservationMovesBehindUntriedPRs(t *testing.T) {
	l := newSweepLab(t)
	old := [2]time.Duration{prcmd.GHBound, pipeline.DefaultGHBound}
	prcmd.GHBound, pipeline.DefaultGHBound = time.Second, time.Second
	t.Cleanup(func() { prcmd.GHBound, pipeline.DefaultGHBound = old[0], old[1] })
	l.saveTask(l.openPR()) // observed_at 2026-01-02: first in line
	other := l.cloneTask("t-dddddddddddd", l.openPR(), "2026-01-03T00:00:00+00:00")
	if err := os.WriteFile(filepath.Join(l.ghRoot, "pr.json"), []byte(`{"hang": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	firstRows, firstDeferred := l.sweep(SweepOpts{Budget: time.Minute})
	if obs := sweepRow(firstRows, "pr-observe"); obs == nil || fmt.Sprint(func() any { v, _ := obs.Get("task"); return v }()) != sweepTaskID {
		t.Fatalf("first sweep rows = %v, want the older PR observed first", firstRows)
	}
	if len(firstDeferred) != 1 {
		t.Fatalf("first sweep deferred = %v", firstDeferred)
	}
	task, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		t.Fatal(err)
	}
	pr := asObject(field(task, "pr"))
	if asString(pr, "observe_failed_at") == "" || asString(pr, "observed_at") != "2026-01-02T00:00:00+00:00" || asString(pr, "state") != "open" {
		t.Fatalf("failed observation record = %v", pr)
	}
	secondRows, _ := l.sweep(SweepOpts{Budget: time.Minute})
	obs := sweepRow(secondRows, "pr-observe")
	if obs == nil || fmt.Sprint(func() any { v, _ := obs.Get("task"); return v }()) != other {
		t.Fatalf("second sweep rows = %v, want %s observed first", secondRows, other)
	}
}

// The sweep re-reads each task before acting: a task changed after the snapshot is judged on its fresh record.
func TestSweep_actsOnlyOnTheFreshRecord(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.mergedPR())
	l.writeGHScenario("OPEN", "")
	beforeTask = func(id string) {
		if id == sweepTaskID {
			l.saveTask(l.openPR())
		}
	}
	t.Cleanup(func() { beforeTask = func(string) {} })
	rows, _ := l.sweep(SweepOpts{})
	if clean := sweepRow(rows, "cleanup"); clean != nil {
		t.Fatalf("cleanup ran on the stale merged snapshot: %v", clean)
	}
	if _, err := os.Stat(l.checkout); err != nil {
		t.Fatal("checkout removed on stale eligibility")
	}
}

// Pending lists open PRs and cleanup from records alone, with exact commands, and reads nothing else.
func TestPending_listsMaintenanceFromRecords(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	merged := l.cloneTask("t-dddddddddddd", l.mergedPR(), "2026-01-03T00:00:00+00:00")
	tasks, err := l.store.AllTasks()
	if err != nil {
		t.Fatal(err)
	}
	view := Pending(l.store, "sumctl", tasks)
	prs, _ := view.Get("open_prs")
	cleanups, _ := view.Get("cleanup")
	if n := len(prs.([]any)); n != 1 {
		t.Fatalf("open_prs = %v", prs)
	}
	pr := prs.([]any)[0].(*ordjson.Object)
	if at, _ := pr.Get("observed_at"); at != "2026-01-02T00:00:00+00:00" {
		t.Fatalf("observed_at = %v, want the recorded instant", at)
	}
	if next, _ := pr.Get("next"); !strings.Contains(fmt.Sprint(next), "pr reconcile "+sweepTaskID) {
		t.Fatalf("pr next = %v", next)
	}
	if n := len(cleanups.([]any)); n != 1 {
		t.Fatalf("cleanup = %v", cleanups)
	}
	if next, _ := cleanups.([]any)[0].(*ordjson.Object).Get("next"); !strings.Contains(fmt.Sprint(next), "cleanup "+merged) {
		t.Fatalf("cleanup next = %v", next)
	}
	if next, _ := view.Get("next"); !strings.Contains(fmt.Sprint(next), "sweep") {
		t.Fatalf("next = %v", next)
	}
	listed, _ := view.Get("panes")
	if n := len(listed.([]any)); n != 2 {
		t.Fatalf("panes = %v, want both reported tasks", listed)
	}
	if l.ghCalls() != 0 {
		t.Fatal("Pending called gh")
	}
}

// cloneTask copies the lab task under id with pr and a PR observation time, pointing at a checkout of its own.
func (l *sweepLab) cloneTask(id, pr, observedAt string) string {
	l.t.Helper()
	checkout := filepath.Join(l.t.TempDir(), "checkout-"+id)
	add := exec.Command("git", "-C", l.repo, "worktree", "add", checkout, "-b", "sum/"+id)
	if out, err := add.CombinedOutput(); err != nil {
		l.t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	raw := strings.NewReplacer(`"2026-01-02T00:00:00+00:00"`, fmt.Sprintf("%q", observedAt)).Replace(pr)
	src, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		l.t.Fatal(err)
	}
	value, err := ordjson.Decode([]byte(raw))
	if err != nil {
		l.t.Fatal(err)
	}
	src.Set("id", id)
	src.Set("pr", value)
	src.Set("worktree", checkout)
	src.Set("branch", "sum/"+id)
	src.Set("pane", "w-"+id+":p1")
	src.Set("workspace", "w-"+id)
	if err := os.MkdirAll(filepath.Join(l.store.Tasks, id), 0o700); err != nil {
		l.t.Fatal(err)
	}
	if err := l.store.SaveTask(src); err != nil {
		l.t.Fatal(err)
	}
	return id
}

func (l *sweepLab) bindPane(pane, role string) {
	l.t.Helper()
	ep := store.Endpoint{Machine: l.host, Session: "sum-test", Pane: pane, Cwd: l.checkout}
	inc := incarnation.Evidence{
		Terminal: "term-" + pane,
		Shell:    &incarnation.Shell{PID: 4242, Started: "2025-01-01T00:00:00Z"},
	}.Record(store.Now())
	if _, err := l.store.Register(ep, role, sweepTaskID, inc); err != nil {
		l.t.Fatal(err)
	}
}

func (l *sweepLab) setWorkerState(state string) {
	l.t.Helper()
	task, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		l.t.Fatal(err)
	}
	exec := asObject(field(task, "execution"))
	worker := asObject(field(exec, "worker"))
	worker.Set("state", state)
	if err := l.store.SaveTask(task); err != nil {
		l.t.Fatal(err)
	}
}

func (l *sweepLab) clearReport() {
	l.t.Helper()
	task, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		l.t.Fatal(err)
	}
	task.Set("report", nil)
	var kept []any
	for _, raw := range asList(field(task, "evidence")) {
		row := asObject(raw)
		kind := asString(row, "kind")
		if kind == "report" || kind == "handoff" {
			continue
		}
		kept = append(kept, raw)
	}
	if kept == nil {
		kept = []any{}
	}
	task.Set("evidence", kept)
	if err := l.store.SaveTask(task); err != nil {
		l.t.Fatal(err)
	}
}

func TestSweep_closesReportedWorkerPane(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	l.writeGHScenario("OPEN", "")
	l.writeLivePanes(nil)
	l.bindPane("w-worker:p1", "worker")
	l.setWorkerState("running")
	rows, _ := l.sweep(SweepOpts{})
	row := sweepRow(rows, "pane-close")
	if row == nil {
		t.Fatalf("no pane-close row in %v", rows)
	}
	if state, _ := row.Get("state"); state != "closed" {
		t.Fatalf("pane-close state = %v err=%v", state, func() any { v, _ := row.Get("error"); return v }())
	}
	if l.paneExists("w-worker:p1") {
		t.Fatal("reported worker pane is still open")
	}
	if l.paneExists("w-parent:p1") == false {
		t.Fatal("coordinator pane was closed")
	}
	task, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !panes.WorkerIsClosed(task) {
		t.Fatalf("closed_panes = %v", field(task, "closed_panes"))
	}
	if park, _ := row.Get("park_error"); park != nil {
		t.Fatalf("park after close: %v", park)
	}
	exec := asObject(field(task, "execution"))
	worker := asObject(field(exec, "worker"))
	if asString(worker, "state") != "released" {
		t.Fatalf("worker state after close = %v", asString(worker, "state"))
	}
	if _, err := os.Stat(l.checkout); err != nil {
		t.Fatal("pane close removed the checkout")
	}
}

func TestSweep_leavesInFlightWorkerPane(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	l.writeGHScenario("OPEN", "")
	l.writeLivePanes(nil)
	l.clearReport()
	l.setWorkerState("running")
	rows, _ := l.sweep(SweepOpts{})
	if row := sweepRow(rows, "pane-close"); row != nil {
		t.Fatalf("in-flight pane was closed: %v", row)
	}
	if !l.paneExists("w-worker:p1") {
		t.Fatal("in-flight worker pane is gone")
	}
}

func TestSweep_neverClosesCoordinatorPane(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	l.writeGHScenario("OPEN", "")
	l.writeLivePanes(nil)
	task, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		t.Fatal(err)
	}
	task.Set("pane", "w-parent:p1")
	task.Set("workspace", "w-parent")
	for _, raw := range asList(field(task, "evidence")) {
		row := asObject(raw)
		if ep := asObject(field(row, "endpoint")); ep != nil {
			ep.Set("pane", "w-parent:p1")
		}
	}
	if err := l.store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	rows, _ := l.sweep(SweepOpts{})
	row := sweepRow(rows, "pane-close")
	if row == nil {
		t.Fatalf("expected a skipped pane-close, got %v", rows)
	}
	if state, _ := row.Get("state"); state != "skipped" {
		t.Fatalf("state = %v, want skipped", state)
	}
	if !l.paneExists("w-parent:p1") {
		t.Fatal("coordinator pane was closed")
	}
}

func TestSweep_closesReviewerPaneAfterVerdict(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	l.writeGHScenario("OPEN", "")
	l.clearReport()
	l.setWorkerState("running")
	task, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		t.Fatal(err)
	}
	reviewer := ordjson.NewObject()
	reviewer.Set("machine", l.host)
	reviewer.Set("session", "sum-test")
	reviewer.Set("pane", "w-rev:p1")
	reviewer.Set("cwd", l.home)
	task.Set("reviewer", reviewer)
	review := ordjson.NewObject()
	review.Set("kind", "review")
	review.Set("source", "reviewer")
	review.Set("candidate", l.head)
	review.Set("verdict", "approve")
	review.Set("at", "2026-01-01T00:00:00+00:00")
	endpoint := ordjson.NewObject()
	endpoint.Set("machine", l.host)
	endpoint.Set("session", "sum-test")
	endpoint.Set("pane", "w-rev:p1")
	review.Set("endpoint", endpoint)
	task.Set("evidence", []any{review})
	if err := l.store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	l.writeLivePanes(map[string]any{
		"w-rev:p1": map[string]any{
			"pane_id": "w-rev:p1", "cwd": l.home, "workspace_id": "w-rev",
			"agent_status": "idle", "agent": "codex", "name": "reviewer",
			"shell_pid": 5151, "created": true, "terminal_id": "term-w-rev:p1", "processes": []any{},
		},
	})
	l.bindPane("w-rev:p1", "reviewer")
	rows, _ := l.sweep(SweepOpts{})
	row := sweepRow(rows, "pane-close")
	if row == nil {
		t.Fatalf("no pane-close row in %v", rows)
	}
	if pane, _ := row.Get("pane"); pane != "w-rev:p1" {
		t.Fatalf("closed pane = %v, want reviewer", pane)
	}
	if state, _ := row.Get("state"); state != "closed" {
		t.Fatalf("state = %v err=%v", state, func() any { v, _ := row.Get("error"); return v }())
	}
	if l.paneExists("w-rev:p1") {
		t.Fatal("reviewer pane is still open")
	}
	if !l.paneExists("w-worker:p1") {
		t.Fatal("in-flight worker pane was closed with the reviewer")
	}
}

func TestSweep_closesReportedWorkerWithOpenQuestion(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	l.writeGHScenario("OPEN", "")
	l.writeLivePanes(nil)
	l.bindPane("w-worker:p1", "worker")
	l.setWorkerState("running")
	task, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		t.Fatal(err)
	}
	q := ordjson.NewObject()
	q.Set("id", "q-aaaaaaaaaa")
	q.Set("key", "open")
	q.Set("status", "open")
	q.Set("text", "which one?")
	task.Set("questions", []any{q})
	if err := l.store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	rows, _ := l.sweep(SweepOpts{})
	row := sweepRow(rows, "pane-close")
	if row == nil {
		t.Fatal("open question kept a reported pane open")
	}
	if state, _ := row.Get("state"); state != "closed" {
		t.Fatalf("state = %v", state)
	}
}

func TestSweep_closedPaneRepairRecordsPendingResume(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	l.writeGHScenario("OPEN", "")
	l.writeLivePanes(nil)
	l.bindPane("w-worker:p1", "worker")
	l.setWorkerState("running")
	rows, _ := l.sweep(SweepOpts{})
	if row := sweepRow(rows, "pane-close"); row == nil {
		t.Fatal("worker pane was not closed")
	}
	task, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		t.Fatal(err)
	}
	worker := asObject(field(asObject(field(task, "execution")), "worker"))
	attempt := asString(worker, "id")
	got, err := repair.Send(l.store, l.ctx, repair.SendArgs{
		TaskID:      sweepTaskID,
		Attempt:     attempt,
		Key:         "after-close",
		Text:        "re-run the failing gate",
		Class:       repair.ClassInScope,
		RuntimeRoot: l.runtime,
	})
	if err != nil {
		t.Fatalf("repair send: %v", err)
	}
	if state, _ := got.Get("state"); state != "pane-closed" {
		t.Fatalf("repair state = %v", got)
	}
	if next, _ := got.Get("next"); !strings.Contains(fmt.Sprint(next), "execution resume") {
		t.Fatalf("repair next = %v", next)
	}
	task, err = l.store.ReadTask(sweepTaskID)
	if err != nil {
		t.Fatal(err)
	}
	texts := repair.PendingResumeTexts(task)
	if len(texts) != 1 || texts[0] != "re-run the failing gate" {
		t.Fatalf("pending resume texts = %v", texts)
	}
}

func TestSweep_successorPaneSurvivesUntilNewReport(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	l.writeGHScenario("OPEN", "")
	task, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		t.Fatal(err)
	}
	task.Set("pane", "w-worker:p2")
	exec := asObject(field(task, "execution"))
	worker := asObject(field(exec, "worker"))
	worker.Set("id", "x-dddddddddddd")
	worker.Set("state", "running")
	worker.Set("resumes", sweepWorkerID)
	worker.Set("created_at", "2026-01-02T00:00:00+00:00")
	owner := asObject(field(worker, "owner"))
	owner.Set("pane", "w-worker:p2")
	if err := l.store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	l.writeLivePanes(map[string]any{
		"w-worker:p2": map[string]any{
			"pane_id": "w-worker:p2", "cwd": l.checkout, "workspace_id": "w-worker",
			"agent_status": "idle", "agent": "codex", "name": "worker",
			"shell_pid": 4343, "created": true, "terminal_id": "term-w-worker:p2",
			"processes": []any{},
		},
	})
	l.bindPane("w-worker:p2", "worker")
	rows, _ := l.sweep(SweepOpts{})
	if row := sweepRow(rows, "pane-close"); row != nil {
		t.Fatalf("successor pane was closed: %v", row)
	}
	if !l.paneExists("w-worker:p2") {
		t.Fatal("successor pane is gone")
	}

	task, err = l.store.ReadTask(sweepTaskID)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := ordjson.NewObject()
	endpoint.Set("machine", l.host)
	endpoint.Set("session", "sum-test")
	endpoint.Set("pane", "w-worker:p2")
	fresh := ordjson.NewObject()
	fresh.Set("kind", "report")
	fresh.Set("source", "worker")
	fresh.Set("candidate", l.head)
	fresh.Set("at", "2026-01-03T00:00:00+00:00")
	fresh.Set("endpoint", endpoint)
	task.Set("evidence", append(asList(field(task, "evidence")), fresh))
	asObject(field(task, "report")).Set("submitted_at", "2026-01-03T00:00:00+00:00")
	if err := l.store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	rows, _ = l.sweep(SweepOpts{})
	row := sweepRow(rows, "pane-close")
	if row == nil {
		t.Fatal("successor pane stayed open after a new report")
	}
	if pane, _ := row.Get("pane"); pane != "w-worker:p2" {
		t.Fatalf("closed pane = %v, want successor", pane)
	}
	if l.paneExists("w-worker:p2") {
		t.Fatal("successor pane is still open after a new report")
	}
}

func TestSweep_successorReviewerPaneSurvivesUntilNewVerdict(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	l.writeGHScenario("OPEN", "")
	l.clearReport()
	task, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		t.Fatal(err)
	}
	reviewer := ordjson.NewObject()
	reviewer.Set("machine", l.host)
	reviewer.Set("session", "sum-test")
	reviewer.Set("pane", "w-rev:p2")
	reviewer.Set("cwd", l.home)
	reviewer.Set("bound_at", "2026-01-02T00:00:00+00:00")
	task.Set("reviewer", reviewer)
	endpoint := ordjson.NewObject()
	endpoint.Set("machine", l.host)
	endpoint.Set("session", "sum-test")
	endpoint.Set("pane", "w-rev:p1")
	review := ordjson.NewObject()
	review.Set("kind", "review")
	review.Set("source", "reviewer")
	review.Set("candidate", l.head)
	review.Set("verdict", "changes-requested")
	review.Set("at", "2026-01-01T00:00:00+00:00")
	review.Set("endpoint", endpoint)
	task.Set("evidence", []any{review})
	if err := l.store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	l.writeLivePanes(map[string]any{
		"w-rev:p2": map[string]any{
			"pane_id": "w-rev:p2", "cwd": l.home, "workspace_id": "w-rev",
			"agent_status": "idle", "agent": "codex", "name": "reviewer",
			"shell_pid": 5252, "created": true, "terminal_id": "term-w-rev:p2", "processes": []any{},
		},
	})
	l.bindPane("w-rev:p2", "reviewer")
	rows, _ := l.sweep(SweepOpts{})
	if row := sweepRow(rows, "pane-close"); row != nil {
		t.Fatalf("successor reviewer pane was closed: %v", row)
	}
	if !l.paneExists("w-rev:p2") {
		t.Fatal("successor reviewer pane is gone")
	}
}

func TestSweep_reviewWithoutVerdictDoesNotClose(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	l.writeGHScenario("OPEN", "")
	l.clearReport()
	task, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		t.Fatal(err)
	}
	reviewer := ordjson.NewObject()
	reviewer.Set("machine", l.host)
	reviewer.Set("session", "sum-test")
	reviewer.Set("pane", "w-rev:p1")
	reviewer.Set("cwd", l.home)
	task.Set("reviewer", reviewer)
	endpoint := ordjson.NewObject()
	endpoint.Set("machine", l.host)
	endpoint.Set("session", "sum-test")
	endpoint.Set("pane", "w-rev:p1")
	review := ordjson.NewObject()
	review.Set("kind", "review")
	review.Set("source", "reviewer")
	review.Set("candidate", l.head)
	review.Set("at", "2026-01-01T00:00:00+00:00")
	review.Set("endpoint", endpoint)
	task.Set("evidence", []any{review})
	if err := l.store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	l.writeLivePanes(map[string]any{
		"w-rev:p1": map[string]any{
			"pane_id": "w-rev:p1", "cwd": l.home, "workspace_id": "w-rev",
			"agent_status": "idle", "agent": "codex", "name": "reviewer",
			"shell_pid": 5151, "created": true, "terminal_id": "term-w-rev:p1", "processes": []any{},
		},
	})
	l.bindPane("w-rev:p1", "reviewer")
	rows, _ := l.sweep(SweepOpts{})
	if row := sweepRow(rows, "pane-close"); row != nil {
		t.Fatalf("review without verdict closed a pane: %v", row)
	}
	if !l.paneExists("w-rev:p1") {
		t.Fatal("reviewer pane is gone")
	}
}

func TestSweep_emptyCandidateDoesNotClose(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	l.writeGHScenario("OPEN", "")
	task, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		t.Fatal(err)
	}
	asObject(field(task, "report")).Set("candidate", "")
	for _, raw := range asList(field(task, "evidence")) {
		asObject(raw).Set("candidate", "")
	}
	if err := l.store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	l.writeLivePanes(nil)
	l.bindPane("w-worker:p1", "worker")
	l.setWorkerState("running")
	rows, _ := l.sweep(SweepOpts{})
	if row := sweepRow(rows, "pane-close"); row != nil {
		t.Fatalf("empty candidate closed a live pane: %v", row)
	}
	if !l.paneExists("w-worker:p1") {
		t.Fatal("live pane is gone")
	}
}

func TestSweep_skipsReusedPane(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	l.writeGHScenario("OPEN", "")
	l.writeLivePanes(nil)
	l.bindPane("w-worker:p1", "worker")
	raw, err := os.ReadFile(filepath.Join(l.herdr, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	state["panes"].(map[string]any)["w-worker:p1"].(map[string]any)["terminal_id"] = "term-reused"
	state["panes"].(map[string]any)["w-worker:p1"].(map[string]any)["shell_pid"] = 9999
	out, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.herdr, "state.json"), out, 0o600); err != nil {
		t.Fatal(err)
	}
	l.setWorkerState("running")
	rows, _ := l.sweep(SweepOpts{})
	row := sweepRow(rows, "pane-close")
	if row == nil {
		t.Fatal("expected a skipped close for a reused pane")
	}
	if state, _ := row.Get("state"); state != "skipped" {
		t.Fatalf("state = %v, want skipped", state)
	}
	if !l.paneExists("w-worker:p1") {
		t.Fatal("reused pane was closed")
	}
}

func TestSweep_parkFailureDoesNotRecordClosed(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	l.writeGHScenario("OPEN", "")
	l.writeLivePanes(nil)
	l.bindPane("w-worker:p1", "worker")
	l.setWorkerState("starting")
	rows, _ := l.sweep(SweepOpts{})
	row := sweepRow(rows, "pane-close")
	if row == nil {
		t.Fatal("expected a pane-close attempt")
	}
	if state, _ := row.Get("state"); state != "error" {
		t.Fatalf("state = %v, want error after park failure", state)
	}
	task, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if panes.WorkerIsClosed(task) {
		t.Fatal("park failure recorded the pane closed")
	}
}
