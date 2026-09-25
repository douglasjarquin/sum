package metadata

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/inboxview"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// scriptedHerdr is a logging fake: every argv line is appended to PROJECTION_TEST_LOG, `pane get` answers with the
// cwd in FAKE_CWD and a fixed terminal, and every other call succeeds silently like 0.9.0 report-metadata.
func scriptedHerdr(t *testing.T, cwd string) (runtime, log string) {
	t.Helper()
	runtime = t.TempDir()
	log = filepath.Join(runtime, "calls")
	t.Setenv("PROJECTION_TEST_LOG", log)
	t.Setenv("FAKE_CWD", cwd)
	bin := filepath.Join(runtime, ".local", "bin", "herdr")
	t.Setenv("SUM_HERDR_BIN", bin)
	if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_SLEEP", "")
	t.Setenv("FAKE_REFUSE_CLEAR", "")
	// FAKE_SLEEP delays every call (budget tests); FAKE_REFUSE_CLEAR refuses every --clear-token call with a code.
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$PROJECTION_TEST_LOG\"\n" +
		"[ -n \"$FAKE_SLEEP\" ] && sleep \"$FAKE_SLEEP\"\n" +
		"case \"$*\" in *'--clear-token'*) if [ -n \"$FAKE_REFUSE_CLEAR\" ]; then printf '{\"error\":{\"code\":\"internal\",\"message\":\"nope\"}}' >&2; exit 1; fi;; esac\n" +
		"case \"$*\" in *'pane get'*) printf '{\"result\":{\"pane\":{\"pane_id\":\"x\",\"cwd\":\"%s\",\"terminal_id\":\"term-root\"}}}\\n' \"$FAKE_CWD\";; esac\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return runtime, log
}

// coordinatorHome opens home with a recorded coordinator owner whose pane the scripted fake verifies.
func coordinatorHome(t *testing.T, home, cwd string) *store.Store {
	t.Helper()
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	host, err := st.Machine()
	if err != nil {
		t.Fatal(err)
	}
	owner := map[string]any{"role": "coordinator", "machine": host.ID, "session": "sum-test-projection", "pane": "root", "cwd": cwd,
		"claimed_at": "2026-01-01T00:00:00+00:00", "incarnation": map[string]any{"terminal": "term-root", "observed_at": "2026-01-01T00:00:00+00:00"}}
	raw, _ := json.Marshal(owner)
	if err := os.WriteFile(filepath.Join(home, "context.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return st
}

func enabledMeta() *ordjson.Object {
	meta := emptyMetadata()
	meta.Set("enabled", true)
	meta.Set("source", "sum:testinstance")
	caps := ordjson.NewObject()
	caps.Set("pane_tokens", true)
	caps.Set("workspace_tokens", true)
	meta.Set("capabilities", caps)
	return meta
}

func runPass(t *testing.T, st *store.Store, meta *ordjson.Object, runtime string) *ordjson.Object {
	t.Helper()
	p := newPass(st, meta, filepath.Join(runtime, ".local", "bin", "herdr"), false)
	return p.run(nil, "test")
}

func countLines(t *testing.T, log, exact string) int {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if line == exact {
			n++
		}
	}
	return n
}

func TestProjectionCountsOnlyHumanDecisionsAndShowsUnknownCoverage(t *testing.T) {
	for _, damaged := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy", true: "unreadable neighbor"}[damaged], func(t *testing.T) {
			home := t.TempDir()
			worktree := t.TempDir()
			taskdir := filepath.Join(home, "tasks", "t-aaaaaaaaaaaa")
			if err := os.MkdirAll(taskdir, 0700); err != nil {
				t.Fatal(err)
			}
			task := map[string]any{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo", "pane": "worker", "session": "sum-test-projection", "worktree": worktree, "questions": []any{map[string]any{"id": "q-one", "status": "open"}, map[string]any{"id": "q-two", "status": "answered", "answer": "yes"}}, "evidence": []any{}, "attention": []any{}}
			raw, _ := json.Marshal(task)
			if err := os.WriteFile(filepath.Join(taskdir, "task.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			if damaged {
				bad := filepath.Join(home, "tasks", "t-bbbbbbbbbbbb")
				if err := os.MkdirAll(bad, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(bad, "task.json"), []byte("bad"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			runtime, log := scriptedHerdr(t, worktree)
			st := coordinatorHome(t, home, worktree)
			snapshot := inboxview.Read(st)
			if snapshot.Counts.Decisions != 1 || snapshot.Counts.Worker != 1 {
				t.Fatalf("fixture counts: %+v", snapshot.Counts)
			}
			meta := enabledMeta()
			result := runPass(t, st, meta, runtime)
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			want := "sum_inbox=1 decision; 1 worker"
			if damaged {
				want = "sum_inbox=unknown; 1 decision; 1 worker; 1 inspection"
			}
			if !strings.Contains(string(calls), want+"\n") || strings.Contains(string(calls), "sum_inbox=clear") {
				t.Fatalf("projection must agree with snapshot counts/coverage: %s", calls)
			}
			rootRow, _ := result.Get("root")
			outcome, _ := rootRow.(*ordjson.Object).Get("outcome")
			if outcome != "written" {
				t.Fatalf("root row: %v", rootRow)
			}
			if countLines(t, log, "report-metadata") != 2 {
				t.Fatalf("one write per endpoint (worker pane, coordinator pane): %s", calls)
			}
			// The same facts again: nothing differs, nothing is written, the panes are not even observed.
			before := len(calls)
			runPass(t, st, meta, runtime)
			after, _ := os.ReadFile(log)
			if len(after) != before {
				t.Fatalf("unchanged pass called Herdr: %s", after[before:])
			}
		})
	}
}

func TestProjectionRoutineInspectionAndPassiveStates(t *testing.T) {
	cases := []struct{ name, extra, sidecar, state, inbox string }{
		{"report", `"evidence":[{"id":"e1","kind":"report","at":"1"}]`, "", "review-ready", "1 coordinator"},
		{"review", `"evidence":[{"id":"e1","kind":"review","candidate":"sha","verdict":"reject","at":"1"}]`, "", "review-ready", "1 coordinator"},
		{"native attention", `"attention":[{"id":"a1","kind":"blocked","status":"open","at":"1"}]`, "", "needs-attention", "1 inspection"},
		{"answered", `"questions":[{"id":"q1","status":"answered","answer":"yes"}]`, "", "answer-pending", "1 worker"},
		{"passive pipeline", `"questions":[]`, `{"schema":1,"task":"t-aaaaaaaaaaaa","candidate":"sha","rows":[{"stage":"test","status":"pass"}]}`, "running", "clear"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			worktree := t.TempDir()
			taskdir := filepath.Join(home, "tasks", "t-aaaaaaaaaaaa")
			if err := os.MkdirAll(taskdir, 0700); err != nil {
				t.Fatal(err)
			}
			raw := `{"schema":1,"id":"t-aaaaaaaaaaaa","status":"running","pane":"worker","session":"sum-test-projection","worktree":` + strconv(worktree) + `,"parent":{"pane":"root"},` + tc.extra + `}`
			if err := os.WriteFile(filepath.Join(taskdir, "task.json"), []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.sidecar != "" {
				if err := os.WriteFile(filepath.Join(taskdir, "pipeline.json"), []byte(tc.sidecar), 0600); err != nil {
					t.Fatal(err)
				}
			}
			runtime, log := scriptedHerdr(t, worktree)
			st := coordinatorHome(t, home, worktree)
			runPass(t, st, enabledMeta(), runtime)
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"sum_state=" + tc.state + "\n", "sum_inbox=" + tc.inbox + "\n"} {
				if !strings.Contains(string(calls), want) {
					t.Fatalf("missing %q: %s", want, calls)
				}
			}
		})
	}
}

func strconv(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

func TestProjectionSkipsWithoutCapabilityAndWritesOnlyVerifiedPanes(t *testing.T) {
	home := t.TempDir()
	worktree := t.TempDir()
	taskdir := filepath.Join(home, "tasks", "t-aaaaaaaaaaaa")
	if err := os.MkdirAll(taskdir, 0700); err != nil {
		t.Fatal(err)
	}
	raw := `{"schema":1,"id":"t-aaaaaaaaaaaa","status":"running","pane":"worker","workspace":"w-1","session":"sum-test-projection","worktree":"/elsewhere/checkout","questions":[]}`
	if err := os.WriteFile(filepath.Join(taskdir, "task.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	runtime, log := scriptedHerdr(t, worktree)
	st := coordinatorHome(t, home, worktree)
	meta := enabledMeta()
	result := runPass(t, st, meta, runtime)
	rows, _ := result.Get("tasks")
	endpoints, _ := rows.([]any)[0].(*ordjson.Object).Get("endpoints")
	outcomes := map[string]string{}
	for _, e := range endpoints.([]any) {
		kind, _ := e.(*ordjson.Object).Get("kind")
		outcome, _ := e.(*ordjson.Object).Get("outcome")
		outcomes[kind.(string)] = outcome.(string)
	}
	if outcomes["pane"] != "stale" || outcomes["workspace"] != "written" {
		t.Fatalf("a pane in another checkout is stale, the recorded workspace is written: %v", outcomes)
	}
	calls, _ := os.ReadFile(log)
	if strings.Contains(string(calls), "worker\n--source") {
		t.Fatalf("stale pane must not be written: %s", calls)
	}
	resources, _ := meta.Get("resources")
	rec, _ := resources.(*ordjson.Object).Get("t-aaaaaaaaaaaa")
	if _, has := rec.(*ordjson.Object).Get("pane"); has {
		t.Fatalf("stale pane must not be recorded: %v", rec)
	}

	caps := ordjson.NewObject()
	caps.Set("pane_tokens", false)
	caps.Set("workspace_tokens", false)
	meta.Set("capabilities", caps)
	meta.Set("resources", ordjson.NewObject())
	meta.Set("root", nil)
	before, _ := os.ReadFile(log)
	result = runPass(t, st, meta, runtime)
	rootRow, _ := result.Get("root")
	if outcome, _ := rootRow.(*ordjson.Object).Get("outcome"); outcome != "unsupported" {
		t.Fatalf("no capability: %v", rootRow)
	}
	if after, _ := os.ReadFile(log); len(after) != len(before) {
		t.Fatalf("unsupported endpoints must not reach Herdr: %s", after[len(before):])
	}
}

func TestInboxTokenKeepsUnknownAndDecisionsWithinNativeLimit(t *testing.T) {
	for _, n := range []int{100, 999999999} {
		snapshot := inboxview.Snapshot{Counts: inboxview.Counts{Decisions: n, Worker: n, Coordinator: 2 * n, Inspection: n}}
		token := inboxToken(snapshot)
		if len(token) > 80 || !strings.HasPrefix(token, "unknown;") || !strings.Contains(token, fmt.Sprintf("%d decisions", n)) {
			t.Fatalf("invalid native token: %q", token)
		}
	}
}

func TestSourceDerivesFromInstanceNotHomeBasename(t *testing.T) {
	for _, name := range []string{"a.sum", "b.sum"} {
		home := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(home, 0o700); err != nil {
			t.Fatal(err)
		}
		st, err := store.Open(home)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema":1}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Source(st); err == nil || !strings.Contains(err.Error(), "no identity") {
			t.Fatalf("no instance must refuse: %v", err)
		}
		if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema":1,"instance":"`+name+`0123456789abcdef"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		source, err := Source(st)
		if err != nil || source != "sum:"+(name + "0123456789abcdef")[:12] {
			t.Fatalf("source %q %v", source, err)
		}
	}
}

// writeRunningTask saves one running task bound to pane in the test session, returning its task.json path.
func writeRunningTask(t *testing.T, home, id, pane, worktree string) string {
	t.Helper()
	taskdir := filepath.Join(home, "tasks", id)
	if err := os.MkdirAll(taskdir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(taskdir, "task.json")
	raw := `{"schema":1,"id":"` + id + `","status":"running","repository":"owner/repo","pane":"` + pane + `","session":"sum-test-projection","worktree":` + strconv(worktree) + `,"questions":[],"evidence":[],"attention":[]}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func taskRows(t *testing.T, result *ordjson.Object) []*ordjson.Object {
	t.Helper()
	var out []*ordjson.Object
	for _, row := range asList(get(result, "tasks")) {
		out = append(out, row.(*ordjson.Object))
	}
	return out
}

func TestProjectionLockBusySkipsWithoutStateWriteOrHerdrCall(t *testing.T) {
	home := t.TempDir()
	worktree := t.TempDir()
	writeRunningTask(t, home, "t-aaaaaaaaaaaa", "worker", worktree)
	runtime, log := scriptedHerdr(t, worktree)
	st := coordinatorHome(t, home, worktree)
	if err := writeMetadata(st, enabledMeta()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path(st))
	if err != nil {
		t.Fatal(err)
	}
	holder, err := os.OpenFile(filepath.Join(home, Dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	previous := lockWait
	lockWait = 120 * time.Millisecond
	defer func() { lockWait = previous }()

	started := time.Now()
	result := After(st, runtime, nil, "test")
	if time.Since(started) > 2*time.Second {
		t.Fatalf("a busy lock must not block: waited %s", time.Since(started))
	}
	if get(result, "degraded") != true || !strings.Contains(asString(get(result, "reason")), "lock busy") {
		t.Fatalf("busy lock must degrade with the busy reason: %v", result)
	}
	after, err := os.ReadFile(Path(st))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("busy lock must not write state:\n%s\n%s", before, after)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		calls, _ := os.ReadFile(log)
		t.Fatalf("busy lock must not call Herdr: %s", calls)
	}
}

func TestProjectionBudgetExhaustedSkipsRemainingTasks(t *testing.T) {
	home := t.TempDir()
	worktree := t.TempDir()
	writeRunningTask(t, home, "t-aaaaaaaaaaaa", "worker-a", worktree)
	writeRunningTask(t, home, "t-bbbbbbbbbbbb", "worker-b", worktree)
	runtime, log := scriptedHerdr(t, worktree)
	t.Setenv("FAKE_SLEEP", "0.4")
	st := coordinatorHome(t, home, worktree)
	previous := passBudget
	passBudget = 150 * time.Millisecond
	defer func() { passBudget = previous }()

	meta := enabledMeta()
	started := time.Now()
	result := runPass(t, st, meta, runtime)
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("the pass must stop at its budget, not run every task: %s", elapsed)
	}
	rows := taskRows(t, result)
	if len(rows) != 2 {
		t.Fatalf("rows: %v", rows)
	}
	skipped := 0
	for _, row := range rows {
		if get(row, "outcome") == "skipped" {
			skipped++
			if get(row, "reason") != "pass budget exhausted" {
				t.Fatalf("skipped row reason: %v", row)
			}
		}
	}
	if skipped != 1 {
		t.Fatalf("the task after the budget expired is skipped, the first was attempted: %v", rows)
	}
	last := asObject(get(meta, "last_pass"))
	if get(last, "skipped") != jsonInt(1) {
		t.Fatalf("last_pass must count skipped tasks: %v", last)
	}
	if root := asObject(get(result, "root")); get(root, "outcome") != "skipped" {
		t.Fatalf("the coordinator summary is skipped once the budget is spent: %v", root)
	}
	if countLines(t, log, "report-metadata") != 0 {
		calls, _ := os.ReadFile(log)
		t.Fatalf("nothing may be written once the budget is spent: %s", calls)
	}
	if countLines(t, log, "get") != 1 {
		calls, _ := os.ReadFile(log)
		t.Fatalf("only the first task's pane is observed: %s", calls)
	}
	resources := asObject(get(meta, "resources"))
	if _, has := resources.Get("t-bbbbbbbbbbbb"); has {
		t.Fatalf("a skipped task must not be marked applied: %v", resources)
	}
}

func TestProjectionRefusedReleaseStaysPendingUntilCleared(t *testing.T) {
	home := t.TempDir()
	worktree := t.TempDir()
	path := writeRunningTask(t, home, "t-aaaaaaaaaaaa", "worker", worktree)
	runtime, log := scriptedHerdr(t, worktree)
	st := coordinatorHome(t, home, worktree)
	meta := enabledMeta()
	runPass(t, st, meta, runtime)
	if countLines(t, log, "--token") == 0 {
		t.Fatal("first pass wrote nothing")
	}

	// The task is archived and no longer names its pane: sum owes that pane a clear, which Herdr refuses.
	raw := `{"schema":1,"id":"t-aaaaaaaaaaaa","status":"archived","repository":"owner/repo","session":"sum-test-projection","worktree":` + strconv(worktree) + `,"questions":[],"evidence":[],"attention":[]}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_REFUSE_CLEAR", "1")
	result := runPass(t, st, meta, runtime)
	row := taskRows(t, result)[0]
	if get(row, "released") == true {
		t.Fatalf("a refused clear must not report released: %v", row)
	}
	resources := asObject(get(meta, "resources"))
	rec := asObject(get(resources, "t-aaaaaaaaaaaa"))
	pending := asList(get(rec, "pending_clears"))
	if rec == nil || len(pending) != 1 {
		t.Fatalf("the refused clear must stay recorded for a retry: %v", resources)
	}
	owed := asObject(pending[0])
	if get(owed, "id") != "worker" || get(owed, "kind") != "pane" || get(owed, "pending_clear") != true || len(recordTokens(owed)) == 0 {
		t.Fatalf("pending clear must keep the endpoint and its keys: %v", owed)
	}
	if _, has := rec.Get("pane"); has {
		t.Fatalf("the released pane is owed, not applied: %v", rec)
	}
	if get(meta, "last_error") == nil {
		t.Fatal("refusal must be recorded")
	}
	firstClears := countLines(t, log, "--clear-token")

	// Herdr accepts again: the owed clear is retried and the record goes away.
	t.Setenv("FAKE_REFUSE_CLEAR", "")
	result = runPass(t, st, meta, runtime)
	row = taskRows(t, result)[0]
	if get(row, "released") != true {
		t.Fatalf("a successful retry releases the task: %v", row)
	}
	retried := false
	for _, e := range asList(get(row, "endpoints")) {
		if get(asObject(e), "retried") == true && get(asObject(e), "id") == "worker" {
			retried = true
		}
	}
	if !retried {
		t.Fatalf("the retry must be reported: %v", row)
	}
	if _, has := asObject(get(meta, "resources")).Get("t-aaaaaaaaaaaa"); has {
		t.Fatalf("a cleared task leaves no record: %v", get(meta, "resources"))
	}
	if countLines(t, log, "--clear-token") <= firstClears {
		t.Fatal("the retry must reach Herdr")
	}
	if get(meta, "degraded") != nil {
		t.Fatalf("a clean pass clears degraded: %v", get(meta, "degraded"))
	}
}

func TestProjectionRebindClearsPreviousPaneAndWritesNew(t *testing.T) {
	home := t.TempDir()
	worktree := t.TempDir()
	writeRunningTask(t, home, "t-aaaaaaaaaaaa", "pane-a", worktree)
	runtime, log := scriptedHerdr(t, worktree)
	st := coordinatorHome(t, home, worktree)
	meta := enabledMeta()
	runPass(t, st, meta, runtime)
	rec := asObject(get(asObject(get(meta, "resources")), "t-aaaaaaaaaaaa"))
	if get(asObject(get(rec, "pane")), "id") != "pane-a" {
		t.Fatalf("first pass record: %v", rec)
	}
	before, _ := os.ReadFile(log)

	writeRunningTask(t, home, "t-aaaaaaaaaaaa", "pane-b", worktree)
	result := runPass(t, st, meta, runtime)
	row := taskRows(t, result)[0]
	var paneRow *ordjson.Object
	for _, e := range asList(get(row, "endpoints")) {
		if get(asObject(e), "kind") == "pane" {
			paneRow = asObject(e)
		}
	}
	if paneRow == nil || get(paneRow, "id") != "pane-b" || get(paneRow, "outcome") != "written" {
		t.Fatalf("pane row must be the write to B: %v", row)
	}
	cleared := asObject(get(paneRow, "cleared_previous"))
	if cleared == nil || get(cleared, "id") != "pane-a" || len(asList(get(cleared, "cleared"))) == 0 {
		t.Fatalf("cleared_previous must name A and its cleared keys: %v", paneRow)
	}
	after, _ := os.ReadFile(log)
	calls := string(after[len(before):])
	if !strings.Contains(calls, "report-metadata\npane-a\n--source\nsum:testinstance\n--clear-token\n") || strings.Contains(calls, "report-metadata\npane-a\n--source\nsum:testinstance\n--token\n") {
		t.Fatalf("A must only have its keys cleared: %s", calls)
	}
	if !strings.Contains(calls, "report-metadata\npane-b\n--source\nsum:testinstance\n--token\n") {
		t.Fatalf("B must receive the write: %s", calls)
	}
	rec = asObject(get(asObject(get(meta, "resources")), "t-aaaaaaaaaaaa"))
	if get(asObject(get(rec, "pane")), "id") != "pane-b" {
		t.Fatalf("record must follow the task to B: %v", rec)
	}
	if _, has := rec.Get("pending_clears"); has {
		t.Fatalf("a successful rebind owes nothing: %v", rec)
	}
}
