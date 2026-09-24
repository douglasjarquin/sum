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
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/review"
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
	herdr    string
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
	t.Setenv("SUM_SESSION", "sum-test")
	t.Setenv("HERDR_SESSION", "sum-test")
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w-parent:p1")
	home := t.TempDir()
	root := repoRoot(t)
	fakeHerdr := filepath.Join(root, "tests", "fixtures", "herdr.py")
	fakeLsof := filepath.Join(root, "tests", "fixtures", "lsof.py")
	herdrRoot := filepath.Join(home, "fake-herdr")
	lsofRoot := filepath.Join(home, "fake-lsof")
	if err := os.MkdirAll(herdrRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(lsofRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_HERDR_BIN", fakeHerdr)
	t.Setenv("SUM_PS_BIN", filepath.Join(root, "tests", "fixtures", "ps.py"))
	t.Setenv("SUM_LSOF_BIN", fakeLsof)
	t.Setenv("FAKE_HERDR_ROOT", herdrRoot)
	t.Setenv("FAKE_SESSION", "sum-test")
	t.Setenv("FAKE_PARENT_CWD", home)
	t.Setenv("FAKE_PARENT_STATUS", "idle")
	t.Setenv("FAKE_PARENT_KIND", "claude")
	t.Setenv("FAKE_LSOF_ROOT", lsofRoot)
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
	checkout := t.TempDir()
	repo := t.TempDir()
	initGitRepo(t, repo)
	initGitRepo(t, checkout)
	if err := os.WriteFile(filepath.Join(lsofRoot, "cwds.json"), []byte(`{"processes":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return &lab{t: t, store: st, ctx: ctx, home: home, host: host, checkout: checkout, runtime: root, herdr: herdrRoot, lsofRoot: lsofRoot, repo: repo}
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
	if err := os.WriteFile(filepath.Join(l.home, "settings.json"), []byte(body), 0o600); err != nil {
		l.t.Fatal(err)
	}
}

func (l *lab) writeWorkerPane() {
	l.t.Helper()
	state := map[string]any{
		"panes": map[string]any{
			"w-parent:p1": map[string]any{
				"pane_id": "w-parent:p1", "cwd": l.home, "workspace_id": "w-parent",
				"agent_status": "idle", "agent": "claude",
			},
			"w-worker:p1": map[string]any{
				"pane_id": "w-worker:p1", "cwd": l.checkout, "workspace_id": "w-worker",
				"agent_status": "unknown", "agent": nil, "created": true, "shell_pid": 4242,
				"processes": []map[string]any{
					{"pid": 4242, "name": "bash", "argv0": "bash", "argv": []any{"-bash"}, "cwd": l.checkout},
				},
			},
		},
		"workspaces": map[string]any{
			"w-parent": map[string]any{"workspace_id": "w-parent", "label": "coordinator", "worktree": nil},
			"w-worker": map[string]any{
				"workspace_id": "w-worker", "label": "task",
				"worktree": map[string]any{"checkout_path": l.checkout},
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
	raw, err := os.ReadFile(filepath.Join(l.herdr, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	panes := state["panes"].(map[string]any)
	worker := panes["w-worker:p1"].(map[string]any)
	worker["agent"] = "claude"
	worker["agent_status"] = "running"
	out, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.herdr, "state.json"), out, 0o600); err != nil {
		t.Fatal(err)
	}
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

// addReviewerPane records an idle reviewer pane w-worker:p2 whose shell runs
// in the task checkout, beside the worker pane writeWorkerPane created.
func (l *lab) addReviewerPane(shellPID int) {
	l.t.Helper()
	path := filepath.Join(l.herdr, "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		l.t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		l.t.Fatal(err)
	}
	state["panes"].(map[string]any)["w-worker:p2"] = map[string]any{
		"pane_id": "w-worker:p2", "cwd": l.checkout, "workspace_id": "w-worker",
		"agent_status": "unknown", "agent": nil, "shell_pid": shellPID,
		"processes": []map[string]any{
			{"pid": shellPID, "name": "bash", "argv0": "bash", "argv": []any{"/bin/bash"}, "cwd": l.checkout},
		},
	}
	out, err := json.Marshal(state)
	if err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		l.t.Fatal(err)
	}
}

// startReviewerShell starts a real reviewer shell in the checkout running a
// real background child, and returns both pids.
func startReviewerShell(t *testing.T, checkout string) (shell, child int) {
	t.Helper()
	cmd := exec.Command("bash", "-c", "sleep 120 & wait")
	cmd.Dir = checkout
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
	for i := 0; i < 100 && child == 0; i++ {
		kids, _ := exec.Command("pgrep", "-P", fmt.Sprint(cmd.Process.Pid)).Output()
		fmt.Sscan(string(kids), &child)
	}
	if child == 0 {
		t.Fatal("reviewer shell child did not start")
	}
	return cmd.Process.Pid, child
}

// removeWorkerPane closes the worker pane w-worker:p1 in fake Herdr, so the
// worker is verified absent and surviving checkout processes are classified
// as owned or orphaned.
func (l *lab) removeWorkerPane() {
	l.t.Helper()
	path := filepath.Join(l.herdr, "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		l.t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		l.t.Fatal(err)
	}
	delete(state["panes"].(map[string]any), "w-worker:p1")
	out, err := json.Marshal(state)
	if err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		l.t.Fatal(err)
	}
}

// saveTaskWithReviewer saves workerRunningJSON, then records w-worker:p2 as its
// reviewer endpoint through review.Run from the lab context, so the reviewer
// carries the cwd production records (the installation root) and has saved
// its findings.
func (l *lab) saveTaskWithReviewer() {
	l.t.Helper()
	l.saveTask(l.workerRunningJSON())
	endpoint := ordjson.NewObject()
	for _, key := range []string{"session", "machine", "cwd"} {
		v, _ := l.ctx.Get(key)
		endpoint.Set(key, v)
	}
	endpoint.Set("pane", "w-worker:p2")
	if _, err := review.Run(l.store, taskID, "approve", "", "", "no findings", "", false, endpoint, returns.PumpOpts{}); err != nil {
		l.t.Fatal(err)
	}
}

func TestCleanup_reviewerShellInCheckoutIsNotWorkerOccupant(t *testing.T) {
	l := newLab(t)
	l.writeSettings(1, 1)
	l.writeWorkerPane()
	l.addReviewerPane(4343)
	l.plantLsof([]map[string]any{{"pid": 4242, "cwd": l.checkout}, {"pid": 4343, "cwd": l.checkout}})
	l.saveTaskWithReviewer()
	plan := l.inspect()
	for _, detail := range blockerDetails(plan) {
		if strings.Contains(detail, "4343") {
			t.Fatalf("reviewer shell counted against the worker: %v", blockerDetails(plan))
		}
	}
	if hasCode(blockerCodes(plan), "occupant") || hasCode(blockerCodes(plan), "execution") || hasCode(blockerCodes(plan), "reviewer") {
		t.Fatalf("cleanup blockers = %v details=%v", blockerCodes(plan), blockerDetails(plan))
	}
	occupancy := asObject(func() any { v, _ := plan.Get("occupancy"); return v }())
	if detached := asList(func() any { v, _ := occupancy.Get("detached"); return v }()); len(detached) > 0 {
		t.Fatalf("worker occupancy detached = %v, want none", detached)
	}
	reviewer := asObject(func() any { v, _ := plan.Get("reviewer"); return v }())
	if closable, _ := reviewer.Get("closable"); closable != true {
		t.Fatalf("reviewer pane is not closable through its own path: %v", reviewer)
	}
	if _, err := execution.Park(l.store, l.ctx, l.runtime, taskID, workerID); err != nil {
		t.Fatalf("park held the worker on the reviewer shell: %v", err)
	}
	if got := l.attemptState(workerID); got != "released" {
		t.Fatalf("worker attempt state = %s, want released", got)
	}
}

func TestCleanup_strayProcessBesideReviewerShellStillBlocks(t *testing.T) {
	l := newLab(t)
	l.writeSettings(1, 1)
	l.writeWorkerPane()
	// Only the shell pid Herdr reports is the reviewer's; its child and an
	// unrelated process still bind the checkout.
	shell, child := startReviewerShell(t, l.checkout)
	stray := startCheckoutWriter(t, l.checkout)
	l.addReviewerPane(shell)
	l.plantLsof([]map[string]any{
		{"pid": 4242, "cwd": l.checkout},
		{"pid": shell, "cwd": l.checkout},
		{"pid": child, "cwd": l.checkout},
		{"pid": stray, "cwd": l.checkout},
	})
	l.saveTaskWithReviewer()
	if _, err := execution.Park(l.store, l.ctx, l.runtime, taskID, workerID); err == nil {
		t.Fatal("park released the worker with processes still in the checkout")
	}
	if got := l.attemptState(workerID); got == "released" {
		t.Fatal("worker reservation was released")
	}
	plan := l.inspect()
	if !hasCode(blockerCodes(plan), "occupant") {
		t.Fatalf("cleanup blockers = %v details=%v, want occupant", blockerCodes(plan), blockerDetails(plan))
	}
	occupancy := asObject(func() any { v, _ := plan.Get("occupancy"); return v }())
	detached := map[string]bool{}
	for _, raw := range asList(func() any { v, _ := occupancy.Get("detached"); return v }()) {
		pid, _ := asObject(raw).Get("pid")
		detached[fmt.Sprint(pid)] = true
	}
	if !detached[fmt.Sprint(child)] || !detached[fmt.Sprint(stray)] {
		t.Fatalf("detached = %v, want the reviewer shell's child %d and the stray %d", detached, child, stray)
	}
	if detached[fmt.Sprint(shell)] {
		t.Fatalf("detached = %v lists the reviewer shell %d", detached, shell)
	}
}

func TestCleanup_reviewerShellIsNotOrphanWhenWorkerPaneGone(t *testing.T) {
	l := newLab(t)
	l.writeSettings(1, 1)
	l.writeWorkerPane()
	l.addReviewerPane(4343)
	l.removeWorkerPane()
	l.plantLsof([]map[string]any{{"pid": 4343, "cwd": l.checkout}})
	l.saveTaskWithReviewer()
	plan := l.inspect()
	if orphans := asList(func() any { v, _ := plan.Get("orphans"); return v }()); len(orphans) > 0 {
		t.Fatalf("orphans = %v; the reviewer shell would be terminated by apply", orphans)
	}
	if hasCode(blockerCodes(plan), "occupant") || hasCode(blockerCodes(plan), "execution") {
		t.Fatalf("cleanup blockers = %v details=%v", blockerCodes(plan), blockerDetails(plan))
	}
	if _, err := execution.Park(l.store, l.ctx, l.runtime, taskID, workerID); err != nil {
		t.Fatalf("park held the closed worker on the reviewer shell: %v", err)
	}
}

func TestCleanup_reviewerShellChildIsNotOrphanWhenWorkerPaneGone(t *testing.T) {
	l := newLab(t)
	l.writeSettings(1, 1)
	l.writeWorkerPane()
	shell, child := startReviewerShell(t, l.checkout)
	l.addReviewerPane(shell)
	l.removeWorkerPane()
	l.plantLsof([]map[string]any{
		{"pid": shell, "cwd": l.checkout},
		{"pid": child, "cwd": l.checkout},
	})
	l.saveTaskWithReviewer()
	plan := l.inspect()
	if orphans := asList(func() any { v, _ := plan.Get("orphans"); return v }()); len(orphans) > 0 {
		t.Fatalf("orphans = %v; apply would terminate the reviewer shell's child %d", orphans, child)
	}
	if !hasCode(blockerCodes(plan), "occupant") || !hasCode(blockerCodes(plan), "execution") {
		t.Fatalf("cleanup blockers = %v details=%v, want occupant and execution", blockerCodes(plan), blockerDetails(plan))
	}
	named := false
	for _, detail := range blockerDetails(plan) {
		if strings.Contains(detail, fmt.Sprint(child)) {
			named = true
		}
		if strings.Contains(detail, fmt.Sprintf("pid %d ", shell)) {
			t.Fatalf("reviewer shell %d counted against the worker: %v", shell, blockerDetails(plan))
		}
	}
	if !named {
		t.Fatalf("reviewer shell child %d is not a named blocker: %v", child, blockerDetails(plan))
	}
}

// failWorkerProcessInfo makes fake Herdr list the worker pane w-worker:p1 but
// fail to read its processes.
func (l *lab) failWorkerProcessInfo() {
	l.t.Helper()
	path := filepath.Join(l.herdr, "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		l.t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		l.t.Fatal(err)
	}
	state["panes"].(map[string]any)["w-worker:p1"].(map[string]any)["process_info_error"] = "server_unavailable"
	out, err := json.Marshal(state)
	if err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		l.t.Fatal(err)
	}
}

func TestCleanup_reviewerShellIsNotDetachedWhenWorkerProcessInfoUnreadable(t *testing.T) {
	l := newLab(t)
	l.writeSettings(1, 1)
	l.writeWorkerPane()
	l.addReviewerPane(4343)
	l.failWorkerProcessInfo()
	l.plantLsof([]map[string]any{{"pid": 4242, "cwd": l.checkout}, {"pid": 4343, "cwd": l.checkout}})
	l.saveTaskWithReviewer()
	plan := l.inspect()
	// Without the worker's process-info its own shell cannot be excluded, so it
	// stays detached; the reviewer shell is still the reviewer's.
	occupancy := asObject(func() any { v, _ := plan.Get("occupancy"); return v }())
	detached := map[string]bool{}
	for _, raw := range asList(func() any { v, _ := occupancy.Get("detached"); return v }()) {
		pid, _ := asObject(raw).Get("pid")
		detached[fmt.Sprint(pid)] = true
	}
	if !detached["4242"] || detached["4343"] {
		t.Fatalf("detached = %v, want the worker shell 4242 and not the reviewer shell 4343", detached)
	}
	if !hasCode(blockerCodes(plan), "occupant") {
		t.Fatalf("cleanup blockers = %v details=%v, want occupant", blockerCodes(plan), blockerDetails(plan))
	}
}
