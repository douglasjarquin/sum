package lifecycle

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pipeline"
	"github.com/douglasjarquin/sum/go/internal/prcmd"
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
	if err := ordjson.WriteFile(filepath.Join(home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Register(store.EndpointFromContext(ctx), "coordinator", nil); err != nil {
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
		checkout: checkout, head: strings.TrimSpace(string(head)), runtime: root, ghRoot: ghRoot}
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
"evidence": [{"kind": "handoff", "source": "worker", "candidate": %q, "at": "2026-01-01T00:00:00+00:00"}],
"report": {"text": "done", "candidate": %q}, "notice": null, "attention": [],
"brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship",
"pr": %s,
"execution": {"schema": 1,
  "worker": {"id": %q, "kind": "worker", "state": "released", "generation": 1,
             "owner": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1"}, "checkout": %q,
             "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": []},
  "verifiers": []}
}`, sweepTaskID, l.repo, l.host, l.checkout, l.head, l.head, pr, sweepWorkerID, l.host, l.checkout)
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
