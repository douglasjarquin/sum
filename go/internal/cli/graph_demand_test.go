package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// graphDemandLab is a dispatched standardized project whose checkouts get an index only through `graph init`.
type graphDemandLab struct {
	*verifyLab
	calls string
}

func newGraphDemandLab(t *testing.T) *graphDemandLab {
	t.Helper()
	v := newVerifyLab(t)
	return &graphDemandLab{verifyLab: v, calls: filepath.Join(v.base, "fake-codegraph", "calls.jsonl")}
}

// with runs fn with extra environment entries appended to every sumctl call it makes.
func (l *graphDemandLab) with(extra []string, fn func()) {
	saved := l.env
	l.env = append(append([]string{}, saved...), extra...)
	defer func() { l.env = saved }()
	fn()
}

func (l *graphDemandLab) graphInit(check bool, taskID string) map[string]any {
	return l.ctl(check, "graph", "init", taskID)
}

func (l *graphDemandLab) taskRecord(taskID string) map[string]any {
	l.t.Helper()
	raw, err := os.ReadFile(filepath.Join(l.home, "tasks", taskID, "task.json"))
	if err != nil {
		l.t.Fatal(err)
	}
	var task map[string]any
	if err := json.Unmarshal(raw, &task); err != nil {
		l.t.Fatal(err)
	}
	return task
}

func graphOf(out map[string]any) map[string]any { return asMap(out["graph"]) }

func TestGraphInit_twoCheckoutsGetTheirOwnIndexAndRepeatStaysReady(t *testing.T) {
	l := newGraphDemandLab(t)
	second := l.ctl(true, "dispatch", "--repo", l.repo, "--brief", filepath.Join(l.base, "brief.md"), "--approved")
	checkouts := map[string]string{l.taskID: l.worktree, asString(second["id"]): asString(second["worktree"])}

	for id, worktree := range checkouts {
		graph := graphOf(l.graphInit(true, id))
		if asString(graph["state"]) != "ready" {
			t.Fatalf("%s graph %v", id, graph)
		}
		if got := asString(graph["index_path"]); got != filepath.Join(worktree, ".codegraph") {
			t.Fatalf("%s index_path %q, want inside %s", id, got, worktree)
		}
		raw, err := os.ReadFile(filepath.Join(worktree, ".codegraph", "meta.json"))
		if err != nil {
			t.Fatal(err)
		}
		var meta map[string]any
		_ = json.Unmarshal(raw, &meta)
		if resolved, _ := filepath.EvalSymlinks(worktree); asString(meta["projectPath"]) != worktree && asString(meta["projectPath"]) != resolved {
			t.Fatalf("%s index built for %v", id, meta["projectPath"])
		}
		if out := l.git(worktree, "status", "--porcelain", "--untracked-files=all"); out != "" {
			t.Fatalf("%s checkout dirty after graph init:\n%s", id, out)
		}
	}
	if _, err := os.Stat(filepath.Join(l.repo, ".codegraph")); !os.IsNotExist(err) {
		t.Fatalf("primary clone gained an index: %v", err)
	}

	again := graphOf(l.graphInit(true, l.taskID))
	if asString(again["state"]) != "ready" || again["error"] != nil {
		t.Fatalf("repeat init %v", again)
	}
	if n := len(asSlice(again["attempts"])); n <= 2 {
		t.Fatalf("repeat init did not record its attempts: %d", n)
	}

	if err := os.WriteFile(filepath.Join(l.worktree, "extra.py"), []byte("def extra():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := l.ctl(true, "graph", "status", l.taskID)
	if state := asString(asMap(asMap(status["live"])["freshness"])["state"]); state != "stale" {
		t.Fatalf("status after an uncommitted edit: %v", status["live"])
	}
}

func TestGraphInit_incompleteIndexIsNeverReadyAndIsRebuilt(t *testing.T) {
	l := newGraphDemandLab(t)
	l.with([]string{"FAKE_CODEGRAPH_PARTIAL=1"}, func() {
		if state := asString(graphOf(l.graphInit(true, l.taskID))["state"]); state != "failed" {
			t.Fatalf("interrupted build recorded %q", state)
		}
	})
	graph := graphOf(l.graphInit(true, l.taskID))
	if asString(graph["state"]) != "ready" {
		t.Fatalf("retry over an incomplete index: %v", graph)
	}
	var actions []string
	for _, a := range asSlice(graph["attempts"]) {
		actions = append(actions, asString(asMap(a)["action"]))
	}
	if !strings.Contains(strings.Join(actions, ","), "init,index,verified") {
		t.Fatalf("retry did not rebuild through index: %v", actions)
	}
}

func TestGraphInit_unavailableAndExhaustedLeaveTheTaskAlone(t *testing.T) {
	l := newGraphDemandLab(t)
	before := l.taskRecord(l.taskID)

	l.with([]string{"SUM_CODEGRAPH_BIN=" + filepath.Join(l.base, "no-such-codegraph")}, func() {
		if state := asString(graphOf(l.graphInit(true, l.taskID))["state"]); state != "unavailable" {
			t.Fatalf("missing tool recorded %q", state)
		}
	})
	l.with([]string{"FAKE_CODEGRAPH_FAIL=1"}, func() {
		var states []string
		for i := 0; i < 3; i++ {
			states = append(states, asString(graphOf(l.graphInit(true, l.taskID))["state"]))
		}
		if got := strings.Join(states, ","); got != "failed,failed,exhausted" {
			t.Fatalf("states %s", got)
		}
	})
	refused := l.graphInit(false, l.taskID)
	if !strings.Contains(asString(refused["error"]), "exhausted") {
		t.Fatalf("fourth init %v", refused)
	}
	after := l.taskRecord(l.taskID)
	for _, key := range []string{"status", "pane", "workspace", "worktree", "reservations"} {
		b, _ := json.Marshal(before[key])
		a, _ := json.Marshal(after[key])
		if string(a) != string(b) {
			t.Fatalf("graph init changed task %s: %s -> %s", key, b, a)
		}
	}
}

func TestGraphInit_refusesWhileAWriterIsRunning(t *testing.T) {
	l := newGraphDemandLab(t)
	writer := filepath.Join(l.base, "stray", "codegraph")
	if err := os.MkdirAll(filepath.Dir(writer), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(writer, []byte("#!/bin/sh\nsleep 30; exit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(writer, "init", l.worktree)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })

	callsBefore := countLines(t, l.calls)
	refused := l.graphInit(false, l.taskID)
	if !strings.Contains(asString(refused["error"]), "still running") {
		t.Fatalf("init beside a live writer: %v", refused)
	}
	if added := countLines(t, l.calls) - callsBefore; added != 0 {
		t.Fatalf("a refused init still ran codegraph %d times", added)
	}
	if _, err := os.Stat(filepath.Join(l.home, "tasks", l.taskID, "graph.json")); !os.IsNotExist(err) {
		t.Fatalf("a refused init recorded graph.json: %v", err)
	}

	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Wait()
	if state := asString(graphOf(l.graphInit(true, l.taskID))["state"]); state != "ready" {
		t.Fatalf("init after the writer exited: %q", state)
	}
}
