package execution

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

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
	herdr    string
	lsofRoot string
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
	t.Setenv("SUM_LSOF_BIN", fakeLsof)
	t.Setenv("FAKE_HERDR_ROOT", herdrRoot)
	t.Setenv("FAKE_SESSION", "sum-test")
	t.Setenv("FAKE_PARENT_CWD", home)
	t.Setenv("FAKE_PARENT_STATUS", "idle")
	t.Setenv("FAKE_PARENT_KIND", "claude")
	t.Setenv("FAKE_LSOF_ROOT", lsofRoot)
	host, err := os.Hostname()
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
	checkout := t.TempDir()
	if err := os.WriteFile(filepath.Join(lsofRoot, "cwds.json"), []byte(`{"processes":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return &lab{t: t, store: st, ctx: ctx, home: home, host: host, checkout: checkout, runtime: root, herdr: herdrRoot, lsofRoot: lsofRoot}
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

func (l *lab) failingLsof() {
	l.t.Helper()
	path := filepath.Join(l.home, "fail-lsof")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho cannot inspect cwd table >&2\nexit 1\n"), 0o700); err != nil {
		l.t.Fatal(err)
	}
	l.t.Setenv("SUM_LSOF_BIN", path)
}

func (l *lab) writeSettings(global, perRepo int) {
	l.t.Helper()
	body := fmt.Sprintf(`{"schema": 1, "capacity": {"global": %d, "per_repository": %d}}`+"\n", global, perRepo)
	if err := os.WriteFile(filepath.Join(l.home, "settings.json"), []byte(body), 0o600); err != nil {
		l.t.Fatal(err)
	}
}

func (l *lab) writeWorkerPane(agent any, processes []map[string]any) {
	l.t.Helper()
	state := map[string]any{
		"panes": map[string]any{
			"w-parent:p1": map[string]any{
				"pane_id": "w-parent:p1", "cwd": l.home, "workspace_id": "w-parent",
				"agent_status": "idle", "agent": "claude",
			},
			"w-worker:p1": map[string]any{
				"pane_id": "w-worker:p1", "cwd": l.checkout, "workspace_id": "w-worker",
				"agent_status": "unknown", "agent": agent, "created": true, "shell_pid": 4242,
				"processes": processes,
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

func decodeObject(raw string) (*ordjson.Object, error) {
	value, err := ordjson.Decode([]byte(raw))
	if err != nil {
		return nil, err
	}
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("not an object")
	}
	return obj, nil
}

func (l *lab) saveTask(raw string) {
	l.t.Helper()
	obj, err := decodeObject(raw)
	if err != nil {
		l.t.Fatal(err)
	}
	if err := l.store.SaveTask(obj); err != nil {
		l.t.Fatal(err)
	}
}

func (l *lab) taskJSON(workerState, verifierState string, extraVerifier string) string {
	l.t.Helper()
	return fmt.Sprintf(`{
"schema": 1, "id": %q, "status": "running", "repository": "owner/repo",
"machine": %q, "session": "sum-test", "pane": "w-worker:p1", "workspace": "w-worker",
"worktree": %q, "branch": "sum/t-aaaaaaaaaaaa",
"questions": [{"id": "q-aaaaaaaaaa", "status": "open", "text": "keep the report?"}],
"evidence": [], "report": {"text": "done"}, "notice": null, "attention": [],
"brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship",
"execution": {"schema": 1,
  "worker": {"id": %q, "kind": "worker", "state": %q, "generation": 1,
             "owner": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1"}, "checkout": %q,
             "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": []},
  "verifiers": [{"id": %q, "kind": "verifier", "state": %q, "generation": 1,
             "owner": {"machine": %q, "session": "sum-test", "pane": "w-rev:p1"}, "checkout": %q,
             "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": []%s}]
}}`, taskID, l.host, l.checkout, workerID, workerState, l.host, l.checkout, verifierID, verifierState, l.host, l.checkout, extraVerifier)
}

func (l *lab) park(attempt string) (*ordjson.Object, error) {
	l.t.Helper()
	return Park(l.store, l.ctx, l.runtime, taskID, attempt)
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

func (l *lab) heldCount() int {
	l.t.Helper()
	task, err := l.store.ReadTask(taskID)
	if err != nil {
		l.t.Fatal(err)
	}
	held, err := reservations.Held(task)
	if err != nil {
		l.t.Fatal(err)
	}
	return len(held)
}

func (l *lab) admit() error {
	l.t.Helper()
	tasks, err := l.store.AllTasks()
	if err != nil {
		l.t.Fatal(err)
	}
	_, err = launch.Admit(l.store, tasks, "owner/repo")
	return err
}

func (l *lab) questionsRemain() {
	l.t.Helper()
	task, err := l.store.ReadTask(taskID)
	if err != nil {
		l.t.Fatal(err)
	}
	raw, _ := task.Get("questions")
	list, _ := raw.([]any)
	if len(list) != 1 {
		l.t.Fatalf("questions = %v", raw)
	}
	report, _ := task.Get("report")
	if report == nil {
		l.t.Fatal("report was dropped")
	}
}

func TestPark_refusesMissingIdentityRunningVerifier(t *testing.T) {
	l := newLab(t)
	l.writeSettings(1, 1)
	l.saveTask(l.taskJSON("released", "running", ""))
	_, err := l.park(verifierID)
	if err == nil {
		t.Fatal("park released a running verifier with no process identity")
	}
	if !strings.Contains(err.Error(), "reservation remains held") && !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("err = %v", err)
	}
	if st := l.attemptState(verifierID); st == "released" {
		t.Fatalf("verifier state = %s, want still reserved", st)
	}
	if l.heldCount() == 0 {
		t.Fatal("reservation was released")
	}
	if err := l.admit(); err == nil {
		t.Fatal("replacement admission succeeded while the verifier remains unknown")
	}
}

func TestPark_refusesMissingOrStaleProcessMetadata(t *testing.T) {
	t.Run("absence of pids is not stopped", func(t *testing.T) {
		l := newLab(t)
		l.saveTask(l.taskJSON("released", "running", ""))
		if _, err := l.park(verifierID); err == nil {
			t.Fatal("missing pids became stopped")
		}
		if l.attemptState(verifierID) == "released" {
			t.Fatal("released from absence")
		}
	})
	t.Run("inspection error is not stopped", func(t *testing.T) {
		l := newLab(t)
		l.failingLsof()
		pid, argv := startThenStop(t, l.checkout)
		extra := fmt.Sprintf(`, "operation_pid": %d, "occupant": {"machine": %q, "pid": %d, "argv": %s, "checkout": %q}`,
			pid, l.host, pid, mustJSON(t, argv), l.checkout)
		l.saveTask(l.taskJSON("released", "running", extra))
		if _, err := l.park(verifierID); err == nil {
			t.Fatal("lsof failure became stopped")
		}
		if l.attemptState(verifierID) == "released" {
			t.Fatal("released after inspection error")
		}
	})
}

func TestPark_deadVerifierParentWithOwnedChildRetainsCapacity(t *testing.T) {
	l := newLab(t)
	l.writeSettings(1, 1)
	child := startCheckoutWriter(t, l.checkout)
	parent, argv := startThenStop(t, l.checkout)
	l.plantLsof([]map[string]any{{"pid": child, "cwd": l.checkout}})
	extra := fmt.Sprintf(`, "operation_pid": %d, "occupant": {"machine": %q, "pid": %d, "argv": %s, "checkout": %q}`,
		parent, l.host, parent, mustJSON(t, argv), l.checkout)
	l.saveTask(l.taskJSON("released", "running", extra))
	if _, err := l.park(verifierID); err == nil {
		t.Fatal("dead parent with a surviving child released capacity")
	}
	if l.heldCount() == 0 {
		t.Fatal("capacity was released")
	}
	if err := l.admit(); err == nil {
		t.Fatal("replacement writer was admitted")
	}
}

func TestPark_absentWorkerAgentWithBackgroundWriterRetainsCapacity(t *testing.T) {
	l := newLab(t)
	l.writeSettings(1, 1)
	child := startCheckoutWriter(t, l.checkout)
	l.plantLsof([]map[string]any{{"pid": child, "cwd": l.checkout}})
	l.writeWorkerPane(nil, []map[string]any{
		{"pid": 4242, "name": "bash", "argv0": "bash", "argv": []any{"-bash"}, "cwd": l.checkout},
	})
	occupant := fmt.Sprintf(`"occupant": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1", "checkout": %q, "harness": "codex", "name": "done", "shell_pid": 4242, "pid": null, "argv": null}`, l.host, l.checkout)
	raw := fmt.Sprintf(`{
"schema": 1, "id": %q, "status": "running", "repository": "owner/repo",
"machine": %q, "session": "sum-test", "pane": "w-worker:p1", "workspace": "w-worker",
"worktree": %q, "branch": "sum/t-aaaaaaaaaaaa",
"questions": [{"id": "q-aaaaaaaaaa", "status": "open", "text": "keep the report?"}],
"evidence": [], "report": {"text": "done"}, "notice": null, "attention": [],
"brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship",
"execution": {"schema": 1,
  "worker": {"id": %q, "kind": "worker", "state": "running", "generation": 1,
             "owner": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1"}, "checkout": %q,
             "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": [],
             %s},
  "verifiers": []}
}`, taskID, l.host, l.checkout, workerID, l.host, l.checkout, occupant)
	l.saveTask(raw)
	if _, err := l.park(workerID); err == nil {
		t.Fatal("absent agent with a checkout writer released capacity")
	}
	if l.heldCount() == 0 {
		t.Fatal("worker reservation was released")
	}
}

func TestPark_stoppedExecutionReleasesOnceAndKeepsObligations(t *testing.T) {
	l := newLab(t)
	l.writeSettings(1, 1)
	pid, argv := startThenStop(t, l.checkout)
	l.plantLsof(nil)
	extra := fmt.Sprintf(`, "operation_pid": %d, "occupant": {"machine": %q, "pid": %d, "argv": %s, "checkout": %q}`,
		pid, l.host, pid, mustJSON(t, argv), l.checkout)
	l.saveTask(l.taskJSON("released", "running", extra))
	result, err := l.park(verifierID)
	if err != nil {
		t.Fatalf("park stopped verifier: %v", err)
	}
	released, _ := result.Get("released")
	if released != true {
		t.Fatalf("released = %v", released)
	}
	if l.attemptState(verifierID) != "released" {
		t.Fatalf("state = %s", l.attemptState(verifierID))
	}
	l.questionsRemain()
	again, err := l.park(verifierID)
	if err != nil {
		t.Fatalf("retry park: %v", err)
	}
	released, _ = again.Get("released")
	if released != true {
		t.Fatalf("retry released = %v", released)
	}
	if l.attemptState(verifierID) != "released" {
		t.Fatal("retry mutated a released attempt")
	}
	l.questionsRemain()
	if err := l.admit(); err != nil {
		t.Fatalf("admission after verified stop: %v", err)
	}
}

func TestPark_refusesWrongIdentityAndStaleAttempt(t *testing.T) {
	t.Run("wrong checkout", func(t *testing.T) {
		l := newLab(t)
		pid, argv := startThenStop(t, l.checkout)
		other := t.TempDir()
		extra := fmt.Sprintf(`, "operation_pid": %d, "occupant": {"machine": %q, "pid": %d, "argv": %s, "checkout": %q}`,
			pid, l.host, pid, mustJSON(t, argv), other)
		l.saveTask(l.taskJSON("released", "running", extra))
		if _, err := l.park(verifierID); err == nil {
			t.Fatal("wrong checkout released")
		}
		if l.attemptState(verifierID) == "released" {
			t.Fatal("current reservation released")
		}
	})
	t.Run("wrong instance", func(t *testing.T) {
		l := newLab(t)
		pid, argv := startThenStop(t, l.checkout)
		extra := fmt.Sprintf(`, "operation_pid": %d, "occupant": {"machine": "other-host", "pid": %d, "argv": %s, "checkout": %q}`,
			pid, pid, mustJSON(t, argv), l.checkout)
		l.saveTask(l.taskJSON("released", "running", extra))
		if _, err := l.park(verifierID); err == nil {
			t.Fatal("wrong instance released")
		}
		if l.attemptState(verifierID) == "released" {
			t.Fatal("current reservation released")
		}
	})
	t.Run("stale attempt id", func(t *testing.T) {
		l := newLab(t)
		pid, argv := startThenStop(t, l.checkout)
		extra := fmt.Sprintf(`, "operation_pid": %d, "occupant": {"machine": %q, "pid": %d, "argv": %s, "checkout": %q}`,
			pid, l.host, pid, mustJSON(t, argv), l.checkout)
		l.saveTask(l.taskJSON("released", "running", extra))
		if _, err := l.park("x-ffffffffffff"); err == nil {
			t.Fatal("unknown attempt parked")
		}
		if l.attemptState(verifierID) != "running" {
			t.Fatalf("current attempt mutated to %s", l.attemptState(verifierID))
		}
	})
	t.Run("concurrent generation change", func(t *testing.T) {
		l := newLab(t)
		pid, argv := startThenStop(t, l.checkout)
		extra := fmt.Sprintf(`, "operation_pid": %d, "occupant": {"machine": %q, "pid": %d, "argv": %s, "checkout": %q}`,
			pid, l.host, pid, mustJSON(t, argv), l.checkout)
		l.saveTask(l.taskJSON("released", "observing", extra))
		task, err := l.store.ReadTask(taskID)
		if err != nil {
			t.Fatal(err)
		}
		value, err := reservations.GetExecution(task)
		if err != nil {
			t.Fatal(err)
		}
		value.Verifiers[0].Set("generation", json.Number("9"))
		value.Verifiers[0].Set("observer_pid", json.Number("1"))
		if err := l.store.SaveTask(task); err != nil {
			t.Fatal(err)
		}
		if _, err := l.park(verifierID); err == nil {
			t.Fatal("stale generation released")
		}
		if l.attemptState(verifierID) == "released" {
			t.Fatal("current reservation released after generation change")
		}
	})
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
