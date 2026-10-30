package cleanup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/execution"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	taskID     = "t-aaaaaaaaaaaa"
	workerID   = "x-aaaaaaaaaaaa"
	verifierID = "x-bbbbbbbbbbbb"
)

type lab struct {
	t        *testing.T
	store    *store.Store
	ctx      *ordjson.Object
	home     string
	host     string
	checkout string
	runtime  string
	hc       *hctest.Fixture
	lsofRoot string
	repo     string
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func newLab(t *testing.T) *lab {
	t.Helper()
	t.Setenv("SUM_HOME", "")
	t.Setenv("SUM_SESSION", "")
	home := t.TempDir()
	root := repoRoot(t)
	lsofRoot := filepath.Join(home, "fake-lsof")
	if err := os.MkdirAll(lsofRoot, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_PS_BIN", filepath.Join(root, "tests", "fixtures", "ps.py"))
	t.Setenv("SUM_LSOF_BIN", filepath.Join(root, "tests", "fixtures", "lsof.py"))
	t.Setenv("FAKE_LSOF_ROOT", lsofRoot)
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Init(); err != nil {
		t.Fatal(err)
	}
	host, err := st.Machine()
	if err != nil {
		t.Fatal(err)
	}
	fixture := hctest.New(t)
	checkout := t.TempDir()
	repo := t.TempDir()
	initGitRepo(t, repo)
	initGitRepo(t, checkout)
	coordinator := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: fixture.CoreID, Socket: fixture.Socket, Session: "sum-test", Address: "t1", Role: "coordinator", Attempt: "bootstrap", BindingGeneration: 1, CreatedMS: 1, Cwd: home}
	cp := uint32(12000)
	cf := &hcclient.Foreground{PID: 12001, Name: "codex", Argv: []string{"codex"}, Cwd: home}
	fixture.SetTerminal(hcclient.Terminal{Address: "t1", Cwd: home, State: "live", CreatedMS: 1, PID: &cp, Foreground: cf})
	bound := incarnation.NativeEvidence{Endpoint: coordinator.Native(), PID: cp, Foreground: cf}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: coordinator.Native(), Kind: "coordinator-init", Attempt: coordinator.Attempt})
	owner := coordinator.Object()
	owner.Set("incarnation", bound.Record(store.Now()))
	owner.Set("native_receipt", receipt)
	if _, err = st.Register(coordinator, "coordinator", nil, bound.Record(store.Now())); err != nil {
		t.Fatal(err)
	}
	if err = ordjson.WriteFile(filepath.Join(st.Home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	l := &lab{t: t, store: st, ctx: coordinator.Object(), home: home, host: host.ID, checkout: checkout, runtime: root, lsofRoot: lsofRoot, repo: repo, hc: fixture}
	l.plantLsof(nil)
	return l
}

func (l *lab) plantLsof(processes []map[string]any) {
	l.t.Helper()
	if processes == nil {
		processes = []map[string]any{}
	}
	payload := map[string]any{"processes": processes}
	raw, err := json.Marshal(payload)
	if err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.lsofRoot, "cwds.json"), raw, 0o600); err != nil {
		l.t.Fatal(err)
	}
}

func (l *lab) writeSettings(global, perRepo int) {
	l.t.Helper()
	body := fmt.Sprintf(`{"schema": 1, "capacity": {"global": %d, "per_repository": %d}}`+"\n", global, perRepo)
	if err := os.WriteFile(filepath.Join(l.store.Home, "settings.json"), []byte(body), 0o600); err != nil {
		l.t.Fatal(err)
	}
}

func (l *lab) endpoint(address, role, attempt string, created uint64) store.Endpoint {
	return store.Endpoint{Schema: 2, Backend: "hc", Machine: l.host, CoreID: l.hc.CoreID, Socket: l.hc.Socket, Session: "sum-test", Address: address, Role: role, Task: taskID, Attempt: attempt, BindingGeneration: 1, CreatedMS: created, Cwd: l.checkout}
}
func (l *lab) workerTerminal(live bool) {
	pid := uint32(40000001)
	fg := &hcclient.Foreground{PID: int32(pid), Name: "worker", Argv: []string{"worker"}, Cwd: l.checkout}
	finished := uint64(3)
	code := int32(0)
	terminal := hcclient.Terminal{Address: "t2", CreatedMS: 2, Cwd: l.checkout, State: "finished", PID: &pid, Foreground: fg, FinishedMS: &finished, ExitCode: &code}
	if live {
		terminal.State = "live"
		terminal.FinishedMS = nil
		terminal.ExitCode = nil
		terminal.Status = &hcclient.Status{State: "done"}
	}
	l.hc.SetTerminal(terminal)
}
func (l *lab) writeWorkerPane() { l.workerTerminal(false) }

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := exec.Command("git", "-C", dir, "init", "-b", "main")
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	run = exec.Command("git", "-C", dir, "config", "user.email", "test@example.invalid")
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("git config email: %v\n%s", err, out)
	}
	run = exec.Command("git", "-C", dir, "config", "user.name", "sum test")
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("git config name: %v\n%s", err, out)
	}
}

func startCheckoutWriter(t *testing.T, checkout string) int {
	t.Helper()
	cmd := exec.Command("sleep", "120")
	cmd.Dir = checkout
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

func startThenStop(t *testing.T, checkout string) (pid int, argv []any) {
	t.Helper()
	cmd := exec.Command("sleep", "120")
	cmd.Dir = checkout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid = cmd.Process.Pid
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = cmd.Process.Wait()
	return pid, []any{"sleep", "120"}
}

func (l *lab) saveTask(raw string) {
	l.t.Helper()
	value, err := ordjson.Decode([]byte(raw))
	if err != nil {
		l.t.Fatal(err)
	}
	obj, ok := value.(*ordjson.Object)
	if !ok {
		l.t.Fatal("task is not an object")
	}
	obj.Set("schema", json.Number("2"))
	endpoint := l.endpoint("t2", "worker", workerID, 2)
	obj.Set("worker_endpoint", endpoint.Object())
	pid := uint32(40000001)
	fg := &hcclient.Foreground{PID: int32(pid), Name: "worker", Argv: []string{"worker"}, Cwd: l.checkout}
	bound := incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: fg}
	if _, err := l.store.Register(endpoint, "worker", taskID, bound.Record(store.Now())); err != nil {
		l.t.Fatal(err)
	}

	obj.Set("parent", l.ctx)
	if ex := asObject(func() any { v, _ := obj.Get("execution"); return v }()); ex != nil {
		worker := asObject(func() any { v, _ := ex.Get("worker"); return v }())
		if worker != nil {
			native := ordjson.NewObject()
			pid := uint32(40000001)
			fg := &hcclient.Foreground{PID: int32(pid), Name: "worker", Argv: []string{"worker"}, Cwd: l.checkout}
			native.Set("incarnation", incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: fg}.Record(store.Now()))
			worker.Set("native_launch", native)
		}
	}
	if err := l.store.SaveTask(obj); err != nil {
		l.t.Fatal(err)
	}
}

func (l *lab) taskJSON(verifierState, verifierExtra string) string {
	return fmt.Sprintf(`{
"schema": 1, "id": %q, "status": "running", "repository": %q,
"machine": %q, "session": "sum-test", "pane": "w-worker:p1", "workspace": "w-worker",
"worktree": %q, "branch": "sum/t-aaaaaaaaaaaa",
"questions": [{"id": "q-aaaaaaaaaa", "status": "open", "text": "keep the report?"}],
"evidence": [], "report": {"text": "done"}, "notice": null, "attention": [],
"brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship",
"execution": {"schema": 1,
  "worker": {"id": %q, "kind": "worker", "state": "released", "generation": 1,
             "owner": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1"}, "checkout": %q,
             "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": []},
  "verifiers": [{"id": %q, "kind": "verifier", "state": %q, "generation": 1,
             "owner": {"machine": %q, "session": "sum-test", "pane": "w-rev:p1"}, "checkout": %q,
             "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": []%s}]
}}`, taskID, l.repo, l.host, l.checkout, workerID, l.host, l.checkout, verifierID, verifierState, l.host, l.checkout, verifierExtra)
}

func (l *lab) attemptState(id string) string {
	l.t.Helper()
	task, err := l.store.ReadTask(taskID)
	if err != nil {
		l.t.Fatal(err)
	}
	value, err := reservations.GetExecution(task)
	if err != nil {
		l.t.Fatal(err)
	}
	rows := append([]*ordjson.Object{value.Worker}, value.Verifiers...)
	for _, row := range rows {
		got, _ := row.Get("id")
		if got == id {
			st, _ := row.Get("state")
			s, _ := st.(string)
			return s
		}
	}
	l.t.Fatalf("attempt %s missing", id)
	return ""
}

func (l *lab) inspect() *ordjson.Object {
	l.t.Helper()
	task, err := l.store.ReadTask(taskID)
	if err != nil {
		l.t.Fatal(err)
	}
	plan, err := inspectTask(l.store, task, l.ctx, l.runtime, 0, "task")
	if err != nil {
		l.t.Fatal(err)
	}
	return plan
}

func blockerCodes(plan *ordjson.Object) []string {
	raw, _ := plan.Get("blockers")
	list, _ := raw.([]any)
	var codes []string
	for _, item := range list {
		row, _ := item.(*ordjson.Object)
		if row == nil {
			continue
		}
		code, _ := row.Get("code")
		s, _ := code.(string)
		codes = append(codes, s)
	}
	return codes
}

func hasCode(codes []string, want string) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

func TestCleanup_deadVerifierChildBlocksDestructiveCleanup(t *testing.T) {
	l := newLab(t)
	l.writeSettings(1, 1)
	l.writeWorkerPane()
	child := startCheckoutWriter(t, l.checkout)
	parent, argv := startThenStop(t, l.checkout)
	l.plantLsof([]map[string]any{{"pid": child, "cwd": l.checkout}})
	argvJSON, _ := json.Marshal(argv)
	extra := fmt.Sprintf(`, "operation_pid": %d, "occupant": {"machine": %q, "pid": %d, "argv": %s, "checkout": %q}`,
		parent, l.host, parent, argvJSON, l.checkout)
	l.saveTask(l.taskJSON("running", extra))
	if _, err := execution.Park(l.store, l.ctx, l.runtime, taskID, verifierID); err == nil {
		t.Fatal("park released a dead parent with a surviving child")
	}
	if l.attemptState(verifierID) == "released" {
		t.Fatal("verifier reservation was released")
	}
	plan := l.inspect()
	state, _ := plan.Get("state")
	if state != "blocked" {
		t.Fatalf("cleanup state = %v codes=%v", state, blockerCodes(plan))
	}
	if !hasCode(blockerCodes(plan), "execution") && !hasCode(blockerCodes(plan), "occupant") {
		t.Fatalf("cleanup blockers = %v, want execution or occupant", blockerCodes(plan))
	}
	tasks, err := l.store.AllTasks()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := launch.Admit(l.store, tasks, l.repo); err == nil {
		t.Fatal("replacement writer admitted while stop evidence is uncertain")
	}
}

func TestCleanup_releasedAttemptsAreNotReobserved(t *testing.T) {
	l := newLab(t)
	l.writeWorkerPane()
	// A live agent in the released worker's pane and a released verifier without
	// operation_pid: re-observing either attempt reports an execution blocker.
	l.workerTerminal(true)
	l.saveTask(l.taskJSON("released", ""))
	plan := l.inspect()
	if hasCode(blockerCodes(plan), "execution") {
		t.Fatalf("released attempts were re-observed: %v", blockerDetails(plan))
	}
	view := asObject(func() any { v, _ := plan.Get("execution"); return v }())
	for _, item := range asList(func() any { v, _ := view.Get("attempts"); return v }()) {
		row := asObject(item)
		if stringField(row, "outcome") != "stopped" {
			id, _ := row.Get("id")
			outcome, _ := row.Get("outcome")
			t.Fatalf("released attempt %v outcome = %v, want stopped", id, outcome)
		}
	}
}

func TestCleanup_agreesWithParkOnUncertainStopEvidence(t *testing.T) {
	l := newLab(t)
	l.writeWorkerPane()
	l.saveTask(l.taskJSON("running", ""))
	parkErr := error(nil)
	_, parkErr = execution.Park(l.store, l.ctx, l.runtime, taskID, verifierID)
	if parkErr == nil {
		t.Fatal("park treated missing identity as stopped")
	}
	if l.attemptState(verifierID) == "released" {
		t.Fatal("park released uncertain execution")
	}
	plan := l.inspect()
	state, _ := plan.Get("state")
	if state != "blocked" {
		t.Fatalf("cleanup state = %v codes=%v", state, blockerCodes(plan))
	}
	if !hasCode(blockerCodes(plan), "execution") {
		t.Fatalf("cleanup blockers = %v, want shared execution stop evidence", blockerCodes(plan))
	}
}

func TestCleanup_applyRefusesUncertainStop(t *testing.T) {
	l := newLab(t)
	l.writeWorkerPane()
	child := startCheckoutWriter(t, l.checkout)
	l.plantLsof([]map[string]any{{"pid": child, "cwd": l.checkout}})
	l.saveTask(l.taskJSON("running", ""))
	result, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true})
	if err == nil {
		t.Fatal("cleanup --apply succeeded without stop evidence")
	}
	if result != nil {
		state, _ := result.Get("state")
		if state == "complete" {
			t.Fatal("cleanup completed against uncertain execution")
		}
	}
	task, err := l.store.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	status, _ := task.Get("status")
	if status == "archived" {
		t.Fatal("task was archived")
	}
	held, err := reservations.Held(task)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) == 0 {
		t.Fatal("apply released the reservation")
	}
}

func blockerDetails(plan *ordjson.Object) []string {
	raw, _ := plan.Get("blockers")
	list, _ := raw.([]any)
	var details []string
	for _, item := range list {
		row, _ := item.(*ordjson.Object)
		if row == nil {
			continue
		}
		detail, _ := row.Get("detail")
		details = append(details, fmt.Sprint(detail))
	}
	return details
}

func (l *lab) workerRunningJSON() string {
	return fmt.Sprintf(`{
"schema": 1, "id": %q, "status": "running", "repository": %q,
"machine": %q, "session": "sum-test", "pane": "w-worker:p1", "workspace": "w-worker",
"worktree": %q, "branch": "sum/t-aaaaaaaaaaaa",
"questions": [{"id": "q-aaaaaaaaaa", "status": "open", "text": "keep the report?"}],
"evidence": [], "report": {"text": "done"}, "notice": null, "attention": [],
"brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship",
"execution": {"schema": 1,
  "worker": {"id": %q, "kind": "worker", "state": "running", "generation": 1,
             "owner": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1"}, "checkout": %q,
             "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": [],
             "occupant": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1", "checkout": %q, "harness": "codex", "name": "done", "shell_pid": 4242, "pid": null, "argv": null}},
  "verifiers": []}
}`, taskID, l.repo, l.host, l.checkout, workerID, l.host, l.checkout, l.host, l.checkout)
}

func TestCleanup_absentWorkerPaneIsNotOccupant(t *testing.T) {
	l := newLab(t)
	l.writeSettings(1, 1)
	l.plantLsof(nil)
	l.saveTask(l.workerRunningJSON())
	plan := l.inspect()
	for _, detail := range blockerDetails(plan) {
		if strings.Contains(detail, "still hosts agent") {
			t.Fatalf("missing agent listed as occupant: %v", blockerDetails(plan))
		}
		if strings.Contains(detail, "cannot prove exit") || strings.Contains(detail, "missing or unobservable pane") {
			t.Fatalf("closed pane treated as unknown stop: %v", blockerDetails(plan))
		}
	}
	if hasCode(blockerCodes(plan), "occupant") {
		t.Fatalf("closed pane occupancy blockers = %v details=%v", blockerCodes(plan), blockerDetails(plan))
	}
}

func TestCleanup_checkoutProcessStillBlocksWhenPaneGone(t *testing.T) {
	l := newLab(t)
	l.writeSettings(1, 1)
	child := startCheckoutWriter(t, l.checkout)
	l.plantLsof([]map[string]any{{"pid": child, "cwd": l.checkout}})
	l.saveTask(l.workerRunningJSON())
	if _, err := execution.Park(l.store, l.ctx, l.runtime, taskID, workerID); err == nil {
		t.Fatal("park released a closed pane with a checkout writer")
	}
	plan := l.inspect()
	if !hasCode(blockerCodes(plan), "occupant") && !hasCode(blockerCodes(plan), "execution") {
		t.Fatalf("cleanup blockers = %v details=%v, want occupant or execution for the live process", blockerCodes(plan), blockerDetails(plan))
	}
	foundProcess := false
	for _, detail := range blockerDetails(plan) {
		if strings.Contains(detail, fmt.Sprintf("pid %d", child)) || strings.Contains(detail, "still run inside the checkout") || strings.Contains(detail, "owned process") {
			foundProcess = true
		}
		if strings.Contains(detail, "still hosts agent") {
			t.Fatalf("missing agent listed as occupant: %s", detail)
		}
	}
	if !foundProcess {
		t.Fatalf("live checkout process was not named: %v", blockerDetails(plan))
	}
}
