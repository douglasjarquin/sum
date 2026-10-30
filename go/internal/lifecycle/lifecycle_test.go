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

	"github.com/douglasjarquin/sum/go/internal/ask"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/panes"
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
	hc       *hctest.Fixture
}

func newSweepLab(t *testing.T) *sweepLab {
	t.Helper()
	t.Setenv("SUM_HOME", "")
	t.Setenv("SUM_SESSION", "")
	home := t.TempDir()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	lsofRoot := filepath.Join(home, "fake-lsof")
	ghRoot := filepath.Join(home, "fake-gh")
	for _, dir := range []string{lsofRoot, ghRoot} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SUM_PS_BIN", filepath.Join(root, "tests", "fixtures", "ps.py"))
	t.Setenv("SUM_LSOF_BIN", filepath.Join(root, "tests", "fixtures", "lsof.py"))
	t.Setenv("SUM_GH_BIN", filepath.Join(root, "tests", "fixtures", "gh.py"))
	t.Setenv("FAKE_LSOF_ROOT", lsofRoot)
	t.Setenv("FAKE_GH_ROOT", ghRoot)
	if err = os.WriteFile(filepath.Join(lsofRoot, "cwds.json"), []byte(`{"processes":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Init(); err != nil {
		t.Fatal(err)
	}
	hostInfo, err := st.Machine()
	if err != nil {
		t.Fatal(err)
	}
	host := hostInfo.ID
	hc := hctest.New(t)
	ep := store.Endpoint{Schema: 2, Backend: "hc", Machine: host, CoreID: hc.CoreID, Session: "sum-test", Socket: hc.Socket, Address: "t1", Role: "coordinator", Attempt: "bootstrap", BindingGeneration: 1, CreatedMS: 1, Cwd: home}
	cp := uint32(12000)
	cf := &hcclient.Foreground{PID: 12001, Name: "codex", Argv: []string{"codex"}, Cwd: home}
	hc.SetTerminal(hcclient.Terminal{Address: "t1", State: "live", CreatedMS: 1, Cwd: home, PID: &cp, Foreground: cf})
	bound := incarnation.NativeEvidence{Endpoint: ep.Native(), PID: cp, Foreground: cf}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: ep.Native(), Kind: "coordinator-init", Attempt: ep.Attempt})
	owner := ep.Object()
	owner.Set("incarnation", bound.Record(store.Now()))
	owner.Set("native_receipt", receipt)
	if _, err = st.Register(ep, "coordinator", nil, bound.Record(store.Now())); err != nil {
		t.Fatal(err)
	}
	if err = ordjson.WriteFile(filepath.Join(st.Home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	ctx := ep.Object()
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
	return &sweepLab{t: t, store: st, ctx: ctx, host: host, home: home, repo: repo, checkout: checkout, head: strings.TrimSpace(string(head)), runtime: root, ghRoot: ghRoot, hc: hc}
}

// saveTask writes a task whose pane and workspace are already gone: the worker
// attempt is released, the handoff is recorded, and the PR record carries the
// given state. With state "merged" it is cleanup-pending; with "open" the sweep
// must first observe the merge through gh.
func (l *sweepLab) saveTask(pr string) {
	l.t.Helper()
	l.saveTaskAs(pr, l.head, "")
}

func (l *sweepLab) saveTaskAs(pr, candidate, extraEvidence string) {
	l.t.Helper()
	if extraEvidence != "" {
		extraEvidence = ",\n" + extraEvidence
	}
	raw := fmt.Sprintf(`{
"schema": 1, "id": %q, "status": "running", "repository": %q,
"machine": %q, "session": "sum-test", "pane": "w-worker:p1", "workspace": "w-worker",
"worktree": %q, "branch": "sum/t-cccccccccccc",
"questions": [],
"evidence": [
  {"kind": "handoff", "source": "worker", "candidate": %q, "at": "2026-01-01T00:00:00+00:00",
   "endpoint": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1"}},
  {"kind": "report", "source": "worker", "candidate": %q, "at": "2026-01-01T00:00:00+00:00",
   "endpoint": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1"}}%s
],
"report": {"text": "done", "candidate": %q, "submitted_at": "2026-01-01T00:00:00+00:00"}, "notice": null, "attention": [],
"brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship",
"pr": %s,
"execution": {"schema": 1,
  "worker": {"id": %q, "kind": "worker", "state": "released", "generation": 1,
             "owner": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1"}, "checkout": %q,
             "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": []},
  "verifiers": []}
}`, sweepTaskID, l.repo, l.host, l.checkout, candidate, l.host, candidate, l.host, extraEvidence, candidate, pr, sweepWorkerID, l.host, l.checkout)
	value, err := ordjson.Decode([]byte(raw))
	if err != nil {
		l.t.Fatal(err)
	}
	obj, _ := value.(*ordjson.Object)
	ep := l.endpoint("t2", "worker", sweepWorkerID, 2, l.checkout)
	obj.Set("schema", json.Number("2"))
	obj.Set("worker_endpoint", ep.Object())
	obj.Set("parent", l.ctx)
	for _, raw := range asList(field(obj, "evidence")) {
		e := asObject(raw)
		e.Set("endpoint", ep.Object())
	}
	l.register(ep)
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

func TestSweep_closedPRNeverCleans(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	l.writeGHScenario("CLOSED", "")
	rows, _ := l.sweep(SweepOpts{})
	obs := sweepRow(rows, "pr-observe")
	if obs == nil {
		t.Fatalf("no pr-observe row in %v", rows)
	}
	if state, _ := obs.Get("state"); state != "closed" {
		t.Fatalf("pr-observe state = %v err=%v", state, func() any { v, _ := obs.Get("error"); return v }())
	}
	if clean := sweepRow(rows, "cleanup"); clean != nil {
		t.Fatalf("cleanup ran for a closed PR: %v", clean)
	}
	if _, err := os.Stat(l.checkout); err != nil {
		t.Fatal("checkout was removed for a closed PR")
	}
}

func TestSweep_squashMergeWithVerifiedHeadCleans(t *testing.T) {
	l := newSweepLab(t)
	stale := strings.Repeat("9", 40)
	verify := fmt.Sprintf(`{"kind": "verification", "source": "coordinator", "candidate": %q, "result": "pass",
		"id": "e-verify0000", "at": "2026-01-02T00:00:00+00:00"}`, l.head)
	l.saveTaskAs(l.openPR(), stale, verify)
	l.writeGHScenario("MERGED", strings.Repeat("b", 40))
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
		t.Fatalf("no cleanup row after a verified merge observation in %v", rows)
	}
	if state, _ := clean.Get("state"); state != "complete" {
		t.Fatalf("cleanup state = %v err=%v", state, func() any { v, _ := clean.Get("error"); return v }())
	}
	if _, err := os.Stat(l.checkout); !os.IsNotExist(err) {
		t.Fatalf("checkout still exists: %v", err)
	}
	task, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		t.Fatal(err)
	}
	pr := asObject(field(task, "pr"))
	if merged, _ := pr.Get("merged_for_task"); merged != true {
		t.Fatalf("merged_for_task = %v", field(pr, "merged_for_task"))
	}
	if findings := asList(field(pr, "findings")); len(findings) != 0 {
		t.Fatalf("findings = %v, want none", findings)
	}
	if notes := asList(field(pr, "notes")); len(notes) != 1 {
		t.Fatalf("notes = %v, want the explained mismatch recorded", notes)
	}
}

func TestSweep_squashMergeUnverifiedHeadStaysPending(t *testing.T) {
	l := newSweepLab(t)
	l.saveTaskAs(l.openPR(), strings.Repeat("9", 40), "")
	l.writeGHScenario("MERGED", strings.Repeat("b", 40))
	rows, _ := l.sweep(SweepOpts{})
	obs := sweepRow(rows, "pr-observe")
	if obs == nil {
		t.Fatalf("no pr-observe row in %v", rows)
	}
	if state, _ := obs.Get("state"); state != "merged" {
		t.Fatalf("pr-observe state = %v err=%v", state, func() any { v, _ := obs.Get("error"); return v }())
	}
	if clean := sweepRow(rows, "cleanup"); clean != nil {
		t.Fatalf("cleanup ran for an unattributed merge: %v", clean)
	}
	if _, err := os.Stat(l.checkout); err != nil {
		t.Fatal("checkout was removed for an unattributed merge")
	}
	task, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		t.Fatal(err)
	}
	pr := asObject(field(task, "pr"))
	if merged, _ := pr.Get("merged_for_task"); merged == true {
		t.Fatal("unexplained head mismatch counted as merged for the task")
	}
	findings := asList(field(pr, "findings"))
	if len(findings) != 1 || !strings.Contains(fmt.Sprint(findings[0]), "is not the recorded candidate") {
		t.Fatalf("findings = %v, want the head mismatch named", findings)
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
	listed, _ := view.Get("terminals")
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
	ep := l.endpoint("t"+strings.TrimPrefix(id, "t-"), "worker", sweepWorkerID, 4, checkout)
	ep.Task = id
	src.Set("worker_endpoint", ep.Object())
	for _, raw := range asList(field(src, "evidence")) {
		asObject(raw).Set("endpoint", ep.Object())
	}
	l.register(ep)
	if err := os.MkdirAll(filepath.Join(l.store.Tasks, id), 0o700); err != nil {
		l.t.Fatal(err)
	}
	if err := l.store.SaveTask(src); err != nil {
		l.t.Fatal(err)
	}
	return id
}

func (l *sweepLab) endpoint(address, role, attempt string, created uint64, cwd string) store.Endpoint {
	return store.Endpoint{Schema: 2, Backend: "hc", Machine: l.host, CoreID: l.hc.CoreID, Session: "sum-test", Socket: l.hc.Socket, Address: address, Role: role, Task: sweepTaskID, Attempt: attempt, BindingGeneration: 1, CreatedMS: created, Cwd: cwd}
}
func (l *sweepLab) register(ep store.Endpoint) {
	l.t.Helper()
	pid := uint32(40000001)
	fg := &hcclient.Foreground{PID: int32(pid), Name: "agent", Argv: []string{"agent"}, Cwd: ep.Cwd}
	bound := incarnation.NativeEvidence{Endpoint: ep.Native(), PID: pid, Foreground: fg}
	if _, err := l.store.Register(ep, ep.Role, ep.Task, bound.Record(store.Now())); err != nil {
		l.t.Fatal(err)
	}
}
func (l *sweepLab) terminal(ep store.Endpoint, state string) hcclient.Terminal {
	pid := uint32(40000001)
	fg := &hcclient.Foreground{PID: int32(pid), Name: "agent", Argv: []string{"agent"}, Cwd: ep.Cwd}
	finished := uint64(10)
	code := int32(0)
	terminal := hcclient.Terminal{Address: ep.Address, CreatedMS: ep.CreatedMS, Cwd: ep.Cwd, State: state, PID: &pid, Foreground: fg, Status: &hcclient.Status{State: "done"}}
	if state == "finished" {
		terminal.FinishedMS = &finished
		terminal.ExitCode = &code
	}
	l.hc.SetTerminal(terminal)
	return terminal
}
func (l *sweepLab) task() *ordjson.Object {
	l.t.Helper()
	task, err := l.store.ReadTask(sweepTaskID)
	if err != nil {
		l.t.Fatal(err)
	}
	return task
}
func (l *sweepLab) save(task *ordjson.Object) {
	l.t.Helper()
	if err := l.store.SaveTask(task); err != nil {
		l.t.Fatal(err)
	}
}
func (l *sweepLab) assertTerminal(address string, present bool) {
	l.t.Helper()
	exists := false
	for _, t := range l.hc.Terminals() {
		if t.Address == address {
			exists = true
		}
	}
	if exists != present {
		l.t.Fatalf("terminal %s present=%v want=%v", address, exists, present)
	}
}
func (l *sweepLab) closeWorker() *ordjson.Object {
	task := l.task()
	endpoint := roleEndpoint(task, "worker")
	return removeOneTerminal(l.store, l.ctx, l.runtime, sweepTaskID, endpoint.Address, "worker", "report-submitted", endpoint.Cwd)
}
func TestSweep_closesReportedWorkerPane(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	ep := roleEndpoint(l.task(), "worker")
	l.terminal(ep, "finished")
	row := l.closeWorker()
	if asString(row, "state") != "closed" {
		t.Fatalf("%v", row)
	}
	l.assertTerminal(ep.Address, false)
	l.assertTerminal("t1", true)
	if !panes.WorkerIsClosed(l.task()) {
		t.Fatal("missing exact closure")
	}
	if _, err := os.Stat(l.checkout); err != nil {
		t.Fatal("maintenance removed checkout")
	}
}
func TestSweep_leavesInFlightWorkerPane(t *testing.T) {
	for _, status := range []string{"idle", "done", "working", "blocked", ""} {
		t.Run(status, func(t *testing.T) {
			l := newSweepLab(t)
			l.saveTask(l.openPR())
			ep := roleEndpoint(l.task(), "worker")
			terminal := l.terminal(ep, "live")
			if status == "" {
				terminal.Status = nil
			} else {
				terminal.Status = &hcclient.Status{State: status}
			}
			l.hc.SetTerminal(terminal)
			row := l.closeWorker()
			if asString(row, "state") != "skipped" {
				t.Fatalf("%v", row)
			}
			l.assertTerminal(ep.Address, true)
		})
	}
}
func TestSweep_neverClosesCoordinatorPane(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	task := l.task()
	task.Set("worker_endpoint", l.ctx)
	l.save(task)
	row := l.closeWorker()
	if asString(row, "state") != "skipped" {
		t.Fatal(row)
	}
	l.assertTerminal("t1", true)
}
func TestSweep_closesReviewerPaneAfterVerdict(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	task := l.task()
	ep := l.endpoint("t3", "reviewer", "review-1", 3, l.checkout)
	l.register(ep)
	l.terminal(ep, "finished")
	task.Set("reviewer", ep.Object())
	e := ordjson.NewObject()
	e.Set("kind", "review")
	e.Set("verdict", "approve")
	e.Set("candidate", l.head)
	e.Set("endpoint", ep.Object())
	task.Set("evidence", []any{e})
	l.save(task)
	rows := removeSettledTerminals(l.store, l.ctx, l.runtime, sweepTaskID)
	if len(rows) != 1 || asString(asObject(rows[0]), "state") != "closed" {
		t.Fatal(rows)
	}
	l.assertTerminal(ep.Address, false)
	if !panes.ReviewerIsClosed(l.task()) {
		t.Fatal("reviewer closure missing")
	}
}
func TestSweep_pendingObligationsRetainFinishedTerminal(t *testing.T) {
	for _, status := range []string{"open", "answered"} {
		t.Run(status, func(t *testing.T) {
			l := newSweepLab(t)
			l.saveTask(l.openPR())
			task := l.task()
			ep := roleEndpoint(task, "worker")
			l.terminal(ep, "finished")
			q := ordjson.NewObject()
			q.Set("id", "q-aaaaaaaaaa")
			q.Set("status", status)
			task.Set("questions", []any{q})
			l.save(task)
			row := l.closeWorker()
			if asString(row, "state") != "skipped" {
				t.Fatal(row)
			}
			l.assertTerminal(ep.Address, true)
			if asString(asObject(asList(field(l.task(), "questions"))[0]), "status") != status {
				t.Fatal("obligation changed")
			}
		})
	}
}
func TestSweep_successorPaneSurvivesUntilNewReport(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	task := l.task()
	old := roleEndpoint(task, "worker")
	ep := old
	ep.Attempt = "new-attempt"
	ep.BindingGeneration++
	task.Set("worker_endpoint", ep.Object())
	l.register(ep)
	l.terminal(ep, "finished")
	l.save(task)
	if reason, _ := workerCloseReason(task); reason != "" {
		t.Fatal("old evidence authorized new binding")
	}
	e := ordjson.NewObject()
	e.Set("kind", "report")
	e.Set("candidate", l.head)
	e.Set("endpoint", ep.Object())
	task.Set("evidence", append(asList(field(task, "evidence")), e))
	l.save(task)
	if reason, _ := workerCloseReason(task); reason == "" {
		t.Fatal("new exact report not eligible")
	}
}
func TestSweep_reboundReviewerClosesOnlyAfterItsOwnReview(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	task := l.task()
	ep := l.endpoint("t3", "reviewer", "review-new", 3, l.checkout)
	old := ep
	old.Attempt = "review-old"
	old.BindingGeneration = 2
	task.Set("reviewer", ep.Object())
	e := ordjson.NewObject()
	e.Set("kind", "review")
	e.Set("verdict", "approve")
	e.Set("candidate", l.head)
	e.Set("endpoint", old.Object())
	task.Set("evidence", []any{e})
	if reason, _ := reviewerCloseReason(task); reason != "" {
		t.Fatal("prior review adopted new reviewer")
	}
	fresh := ordjson.NewObject()
	fresh.Set("kind", "review")
	fresh.Set("verdict", "approve")
	fresh.Set("candidate", l.head)
	fresh.Set("endpoint", ep.Object())
	task.Set("evidence", []any{e, fresh})
	if reason, _ := reviewerCloseReason(task); reason == "" {
		t.Fatal("matching review not eligible")
	}
}
func TestSweep_reviewWithoutVerdictDoesNotClose(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	task := l.task()
	ep := l.endpoint("t3", "reviewer", "review", 3, l.checkout)
	task.Set("reviewer", ep.Object())
	e := ordjson.NewObject()
	e.Set("kind", "review")
	e.Set("candidate", l.head)
	e.Set("endpoint", ep.Object())
	task.Set("evidence", []any{e})
	if reason, _ := reviewerCloseReason(task); reason != "" {
		t.Fatal(reason)
	}
}
func TestSweep_emptyCandidateDoesNotClose(t *testing.T) {
	l := newSweepLab(t)
	l.saveTaskAs(l.openPR(), "", "")
	if hasRemovableTerminal(l.task()) {
		t.Fatal("empty candidate eligible")
	}
}
func TestSweep_skipsReusedPane(t *testing.T) {
	for _, change := range []string{"creation", "kept", "missing-exit"} {
		t.Run(change, func(t *testing.T) {
			l := newSweepLab(t)
			l.saveTask(l.openPR())
			ep := roleEndpoint(l.task(), "worker")
			terminal := l.terminal(ep, "finished")
			switch change {
			case "creation":
				terminal.CreatedMS++
			case "kept":
				terminal.Keep = true
			case "missing-exit":
				terminal.ExitCode = nil
			}
			l.hc.SetTerminal(terminal)
			row := l.closeWorker()
			if asString(row, "state") != "skipped" {
				t.Fatal(row)
			}
			l.assertTerminal(ep.Address, true)
			if panes.WorkerIsClosed(l.task()) {
				t.Fatal("failed close recorded")
			}
		})
	}
}
func TestSweep_parkFailureDoesNotRecordClosed(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	task := l.task()
	ep := roleEndpoint(task, "worker")
	l.terminal(ep, "finished")
	worker := asObject(field(asObject(field(task, "execution")), "worker"))
	worker.Set("state", "running")
	l.save(task)
	if err := os.WriteFile(filepath.Join(l.home, "fake-lsof", "cwds.json"), []byte(fmt.Sprintf(`{"processes":[{"pid":%d,"cwd":%q}]}`, os.Getpid(), l.checkout)), 0600); err != nil {
		t.Fatal(err)
	} // own process is intentionally excluded by proc; use a real checkout writer
	writer := exec.Command("sleep", "60")
	writer.Dir = l.checkout
	if err := writer.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { writer.Process.Kill(); writer.Wait() }()
	if err := os.WriteFile(filepath.Join(l.home, "fake-lsof", "cwds.json"), []byte(fmt.Sprintf(`{"processes":[{"pid":%d,"cwd":%q}]}`, writer.Process.Pid, l.checkout)), 0600); err != nil {
		t.Fatal(err)
	}
	row := l.closeWorker()
	if asString(row, "state") != "skipped" {
		t.Fatal(row)
	}
	l.assertTerminal(ep.Address, true)
	if panes.WorkerIsClosed(l.task()) {
		t.Fatal("writer not stopped but closure recorded")
	}
}

func TestSweepLostNativeRemovalReconcilesExactClaimWithoutReplay(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	ep := roleEndpoint(l.task(), "worker")
	l.terminal(ep, "finished")
	l.hc.QueueFault("terminal.rm", hctest.Fault{LoseResponse: true})
	first := l.closeWorker()
	if asString(first, "state") != "skipped" {
		t.Fatal(first)
	}
	task := l.task()
	if asString(asObject(field(task, "terminal_maintenance")), "state") != "uncertain" || panes.WorkerIsClosed(task) {
		t.Fatal("uncertain removal claimed closure")
	}
	again := l.closeWorker()
	if asString(again, "state") != "already-absent" {
		t.Fatal(again)
	}
	task = l.task()
	if field(task, "terminal_maintenance") != nil || !panes.WorkerIsClosed(task) {
		t.Fatal("exact removal not reconciled")
	}
	count := 0
	for _, call := range l.hc.Calls() {
		if call.Method == "terminal.rm" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("native rm replayed %d times", count)
	}
}
func TestSweepNativeRemovalLeavesLedgerAvailableAndRefusesNewQuestion(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	ep := roleEndpoint(l.task(), "worker")
	l.terminal(ep, "finished")
	entered, release := l.hc.HoldNext("terminal.rm", ep.Address)
	defer release()
	done := make(chan *ordjson.Object, 1)
	go func() { done <- l.closeWorker() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("native rm did not start")
	}
	other, err := store.Open(l.store.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ask.Ask(other, sweepTaskID, "racing-maintenance", "Keep this new question", func() (*ordjson.Object, error) { return nil, nil }); err == nil || !strings.Contains(err.Error(), "maintenance") {
		t.Fatalf("question not refused by exact native claim: %v", err)
	}
	release()
	select {
	case row := <-done:
		if asString(row, "state") != "closed" {
			t.Fatal(row)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("maintenance failed to finish")
	}
}
