package returns

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// The tests in this file run a second delivery operation in a separate OS process (this test binary re-executed as
// TestDeliveryHelperProcess) against the same state home, so contention is between processes exactly as it is
// between two sumctl invocations.

const helperEnv = "SUM_RETURNS_DELIVERY_HELPER"

// TestDeliveryHelperProcess is not a test: it is the second process. It runs one Pump from its environment and writes
// the result to HELPER_OUT.
func TestDeliveryHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) == "" {
		t.Skip("helper process for the multi-process delivery tests")
	}
	if path := os.Getenv("HELPER_STARTED"); path != "" {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ms := func(key string) time.Duration {
		var n int
		fmt.Sscan(os.Getenv(key), &n)
		return time.Duration(n) * time.Millisecond
	}
	ObserveTimeout, PromptTimeout = ms("HELPER_OBSERVE_MS"), ms("HELPER_PROMPT_MS")
	debug := ordjson.NewObject()
	debug.Set("sum_herdr_bin", os.Getenv("SUM_HERDR_BIN"))
	debug.Set("pass_fake", os.Getenv("PASS_FAKE"))
	debug.Set("sum_now", os.Getenv("SUM_NOW"))
	debug.Set("herdr_env", os.Getenv("HERDR_ENV"))
	debug.Set("helper_home", os.Getenv("HELPER_HOME"))
	debug.Set("helper_root", os.Getenv("HELPER_ROOT"))
	debug.Set("helper_tasks", os.Getenv("HELPER_TASKS"))
	debug.Set("helper_recipient", os.Getenv("HELPER_RECIPIENT"))
	s, err := store.Open(os.Getenv("HELPER_HOME"))
	if err != nil {
		debug.Set("open_error", err.Error())
		result := ordjson.NewObject()
		result.Set("error", err.Error())
		result.Set("helper_debug", debug)
		_ = ordjson.WriteFile(os.Getenv("HELPER_OUT"), result)
		t.Fatal(err)
	}
	if host, mErr := s.Machine(); mErr != nil {
		debug.Set("machine_error", mErr.Error())
	} else {
		debug.Set("machine_id", host.ID)
		tasks, _ := s.AllTasks()
		debug.Set("task_count", len(tasks))
		var ids []any
		for _, task := range tasks {
			recorded, _ := task.Get("machine")
			id, _ := task.Get("id")
			row := ordjson.NewObject()
			row.Set("id", id)
			row.Set("machine", recorded)
			row.Set("host_is", host.Is(recorded))
			ids = append(ids, row)
		}
		debug.Set("tasks", ids)
	}
	result, err := Pump(s, PumpOpts{RuntimeRoot: os.Getenv("HELPER_ROOT"), SumctlPath: "sumctl",
		Tasks: strings.Split(os.Getenv("HELPER_TASKS"), ","), Recipient: os.Getenv("HELPER_RECIPIENT"), Budget: ms("HELPER_BUDGET_MS")})
	if result == nil {
		result = ordjson.NewObject()
	}
	if err != nil {
		result.Set("error", err.Error())
	}
	result.Set("helper_debug", debug)
	if err := ordjson.WriteFile(os.Getenv("HELPER_OUT"), result); err != nil {
		t.Fatal(err)
	}
}

type helperProc struct {
	cmd     *exec.Cmd
	out     string
	started string
	done    chan error
}

const helperPromptTimeout = 20 * time.Second

func parallelTestProcs() int {
	n := runtime.GOMAXPROCS(0)
	if n < 1 {
		return 1
	}
	return n
}

// helperStartBudget is how long a helper process may take to reach a held Herdr call.
// It is not a delivery-contract bound. The helper re-executes this test binary, then
// Pump's first `agent list` execs the fake Herdr (Python). `go test ./...` runs up to
// GOMAXPROCS package binaries at once; that contention delays process start without
// changing whether B can deliver while A's prompt is held.
func helperStartBudget() time.Duration {
	const idle = 20 * time.Second
	const perPeer = 8 * time.Second
	return idle + perPeer*time.Duration(parallelTestProcs()-1)
}

// helperObserveTimeout is Pump's observation bound in the helper (and in the parent
// after startHelper). Production ObserveTimeout is 5s; the lab used to pin 1s, which
// kills the fake Herdr during Python startup under `go test ./...` so agent list
// times out, calls stays empty, and the helper exits cleanly as not-delivered.
func helperObserveTimeout() time.Duration {
	const idle = 5 * time.Second
	const perPeer = 2 * time.Second
	return idle + perPeer*time.Duration(parallelTestProcs()-1)
}

func helperChildEnv(extra ...string) []string {
	out := make([]string, 0, len(os.Environ())+len(extra)+1)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "GOMAXPROCS=") {
			continue
		}
		out = append(out, e)
	}
	// The helper runs one sequential Pump. Default GOMAXPROCS would start one OS thread
	// per CPU in a process that already competes with every other package test.
	out = append(out, "GOMAXPROCS=1")
	return append(out, extra...)
}

// startHelper runs one Pump for tasks/recipient in a separate process with a long prompt timeout, so a held prompt
// stands in for a recipient whose Herdr call takes its whole external timeout.
func (l *passLab) startHelper(name string, tasks []string, recipient string) *helperProc {
	l.t.Helper()
	out := filepath.Join(l.root, "helper-"+name+".json")
	started := filepath.Join(l.root, "helper-"+name+".started")
	observe := helperObserveTimeout()
	// fits() requires remaining budget >= timeout+PipeGrace. A 1s observe bound
	// expires during fake-Herdr Python startup under `go test ./...`; scaling
	// observe without scaling the pass budget defers with zero Herdr calls.
	budget := observe + helperPromptTimeout + 2*proc.PipeGrace + 5*time.Second
	cmd := exec.Command(os.Args[0], "-test.run=^TestDeliveryHelperProcess$", "-test.count=1")
	cmd.Env = helperChildEnv(helperEnv+"=1", "HELPER_HOME="+l.s.Home, "HELPER_ROOT="+l.root,
		"HELPER_TASKS="+strings.Join(tasks, ","), "HELPER_RECIPIENT="+recipient, "HELPER_OUT="+out,
		"HELPER_STARTED="+started,
		fmt.Sprintf("HELPER_OBSERVE_MS=%d", observe/time.Millisecond),
		fmt.Sprintf("HELPER_PROMPT_MS=%d", helperPromptTimeout/time.Millisecond),
		fmt.Sprintf("HELPER_BUDGET_MS=%d", budget/time.Millisecond))
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		l.t.Fatal(err)
	}
	h := &helperProc{cmd: cmd, out: out, started: started, done: make(chan error, 1)}
	go func() { h.done <- cmd.Wait() }()
	l.t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-h.done
	})
	return h
}

// result waits for the helper to exit and reads its pass output.
func (h *helperProc) result(t *testing.T) *ordjson.Object {
	t.Helper()
	select {
	case err := <-h.done:
		h.done <- err
		if err != nil {
			t.Fatalf("helper process: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("helper process did not finish")
	}
	value, err := ordjson.ReadFile(h.out)
	if err != nil {
		t.Fatal(err)
	}
	obj := value.(*ordjson.Object)
	if msg, ok := obj.Get("error"); ok {
		t.Fatalf("helper pump: %v", msg)
	}
	return obj
}

// kill ends the helper abruptly, like a crashed or interrupted sumctl.
func (h *helperProc) kill(t *testing.T) {
	t.Helper()
	_ = h.cmd.Process.Signal(syscall.SIGKILL)
	err := <-h.done
	h.done <- err
}

// awaitHeld waits until the fake Herdr holds verb for pane. The wait follows the helper
// process: it fails as soon as the helper exits without holding, and its cap scales with
// GOMAXPROCS instead of assuming an idle `go test` of this package alone.
func (l *passLab) awaitHeld(h *helperProc, verb, pane string) {
	l.t.Helper()
	path := filepath.Join(l.root, "held-"+verb+"-"+pane)
	budget := helperStartBudget()
	deadline := time.Now().Add(budget)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case err := <-h.done:
			h.done <- err
			if _, statErr := os.Stat(path); statErr == nil {
				return
			}
			l.t.Fatalf("helper exited before holding %s for %s (%s): wait=%v; calls = %v; helper_out = %s",
				verb, pane, helperExitReason(h), err, l.calls(), helperOutDump(h))
		case <-ticker.C:
			if time.Now().After(deadline) {
				l.t.Fatalf("the fake Herdr never held %s for %s after %s (GOMAXPROCS=%d, %s); calls = %v; helper_out = %s",
					verb, pane, budget, runtime.GOMAXPROCS(0), helperExitReason(h), l.calls(), helperOutDump(h))
			}
		}
	}
}

func helperExitReason(h *helperProc) string {
	if _, err := os.Stat(h.started); err != nil {
		return "never entered TestDeliveryHelperProcess"
	}
	return "entered TestDeliveryHelperProcess but Pump did not reach Herdr"
}

func helperOutDump(h *helperProc) string {
	data, err := os.ReadFile(h.out)
	if err != nil {
		return fmt.Sprintf("<unreadable %s: %v>", h.out, err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "<empty>"
	}
	return text
}

func (l *passLab) release(pane string) {
	l.t.Helper()
	if err := os.WriteFile(filepath.Join(l.root, "release-"+pane), nil, 0o600); err != nil {
		l.t.Fatal(err)
	}
}

// pumpFor is one pass in this process for tasks' returns to recipient.
func (l *passLab) pumpFor(tasks []string, recipient string, budget time.Duration) (*ordjson.Object, time.Duration) {
	l.t.Helper()
	started := time.Now()
	result, err := Pump(l.s, PumpOpts{RuntimeRoot: l.root, SumctlPath: "sumctl", Tasks: tasks, Recipient: recipient, Budget: budget})
	if err != nil {
		l.t.Fatal(err)
	}
	return result, time.Since(started)
}

// asking records a task whose open question is owed to the lab coordinator, with the coordinator's machine recorded
// as parentMachine (the stable ID or a legacy hostname alias of this host).
func (l *passLab) asking(parentMachine string) string {
	l.t.Helper()
	l.tasks++
	id := fmt.Sprintf("t-%012x", l.tasks)
	if err := os.MkdirAll(filepath.Join(l.s.Tasks, id), 0o700); err != nil {
		l.t.Fatal(err)
	}
	parent := ordjson.NewObject()
	for _, k := range l.ctx.Keys() {
		v, _ := l.ctx.Get(k)
		parent.Set(k, v)
	}
	parent.Set("machine", parentMachine)
	task := ordjson.NewObject()
	task.Set("schema", json.Number("1"))
	task.Set("id", id)
	task.Set("status", "running")
	task.Set("machine", l.host)
	task.Set("session", "lab")
	task.Set("pane", fmt.Sprintf("w-asker%d:p1", l.tasks))
	task.Set("worktree", filepath.Join(l.root, "wt", id))
	task.Set("parent", parent)
	question := ordjson.NewObject()
	question.Set("id", "q1")
	question.Set("status", "open")
	question.Set("created_at", "2026-09-24T00:00:00+00:00")
	task.Set("questions", []any{question})
	if err := l.s.SaveTask(task); err != nil {
		l.t.Fatal(err)
	}
	return id
}

// closeQuestion applies q1 on task id through a separate Store, as another sumctl process would.
func (l *passLab) mutate(id string, change func(task *ordjson.Object)) {
	l.t.Helper()
	other, err := store.Open(l.s.Home)
	if err != nil {
		l.t.Fatal(err)
	}
	unlock, err := other.Lock()
	if err != nil {
		l.t.Fatal(err)
	}
	defer unlock()
	task, err := other.ReadTask(id)
	if err != nil {
		l.t.Fatal(err)
	}
	change(task)
	if err := other.SaveTask(task); err != nil {
		l.t.Fatal(err)
	}
}

func prompts(calls [][]string, pane string) []string {
	var out []string
	for _, c := range calls {
		if len(c) > 4 && c[1] == "agent" && c[2] == "prompt" && c[3] == pane {
			out = append(out, c[4])
		}
	}
	return out
}

func TestHelperStartBudgetScalesWithGOMAXPROCS(t *testing.T) {
	orig := runtime.GOMAXPROCS(0)
	t.Cleanup(func() { runtime.GOMAXPROCS(orig) })
	runtime.GOMAXPROCS(1)
	one := helperStartBudget()
	if one != 20*time.Second {
		t.Fatalf("GOMAXPROCS=1 budget = %s, want 20s idle floor", one)
	}
	runtime.GOMAXPROCS(4)
	four := helperStartBudget()
	if four != 20*time.Second+3*8*time.Second {
		t.Fatalf("GOMAXPROCS=4 budget = %s, want idle plus 3 peers", four)
	}
	if four <= one {
		t.Fatalf("budget did not grow with GOMAXPROCS: 1=%s 4=%s", one, four)
	}
	runtime.GOMAXPROCS(1)
	obsOne := helperObserveTimeout()
	runtime.GOMAXPROCS(4)
	obsFour := helperObserveTimeout()
	if obsOne != 5*time.Second || obsFour != 5*time.Second+3*2*time.Second || obsFour <= obsOne {
		t.Fatalf("observe timeout did not scale: 1=%s 4=%s", obsOne, obsFour)
	}
}

func fanoutField(result *ordjson.Object, key string) string {
	fanout, _ := result.Get("fanout")
	obj, _ := fanout.(*ordjson.Object)
	if obj == nil {
		return ""
	}
	v, _ := obj.Get(key)
	return fmt.Sprint(v)
}

// R1: B completes while A's prompt is held in another process for most of A's external timeout.
func TestConcurrentPassDoesNotWaitForAnUnrelatedRecipient(t *testing.T) {
	l := newPassLab(t)
	a := l.worker("lab", "w1:p1")
	b := l.worker("lab", "w2:p1")
	l.session("lab", map[string]any{"hold": map[string]any{"w1:p1": "prompt"}, "panes": map[string]any{
		"w1:p1": l.pane("idle", l.worktree(a)), "w2:p1": l.pane("idle", l.worktree(b))}})
	h := l.startHelper("a", []string{a}, "worker")
	l.awaitHeld(h, "prompt", "w1:p1")

	result, elapsed := l.pumpFor([]string{b}, "worker", 8*time.Second)
	t.Logf("independent recipient: elapsed %s, lock_wait_ms %s, while A's prompt was held", elapsed, fanoutField(result, "lock_wait_ms"))
	if got := states(result)["w2:p1"]; got != "submitted" {
		t.Fatalf("B = %s after %s while A was held (%v), want submitted", got, elapsed, result)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("B took %s while A was held; B must not wait for A's prompt", elapsed)
	}
	// An older runtime takes the compatibility lock exclusively; it must still wait for a new-runtime delivery.
	handle, err := os.OpenFile(filepath.Join(l.s.Home, ".deliver.lock"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	if err := syscall.Flock(int(handle.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != syscall.EWOULDBLOCK {
		t.Fatalf("legacy exclusive acquire while A is mid-prompt = %v, want EWOULDBLOCK", err)
	}

	l.release("w1:p1")
	if got := states(h.result(t))["w1:p1"]; got != "submitted" {
		t.Fatalf("A = %s, want submitted", got)
	}
	calls := l.calls()
	if len(prompts(calls, "w1:p1")) != 1 || len(prompts(calls, "w2:p1")) != 1 {
		t.Fatalf("calls = %v, want exactly one prompt per recipient", calls)
	}
}

// R2: two processes and two machine spellings for one coordinator make one prompt.
func TestConcurrentPassesToOneRecipientPromptOnceAcrossMachineAliases(t *testing.T) {
	l := newPassLab(t)
	identity, err := machine.Local(l.s.Home)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Hostname == "" || identity.Hostname == l.host || !identity.Is(identity.Hostname) {
		t.Skip("this host has no legacy hostname alias")
	}
	stable := l.asking(l.host)
	legacy := l.asking(identity.Hostname)
	l.session("lab", map[string]any{"hold": map[string]any{"w-root:p1": "prompt"}, "panes": map[string]any{
		"w-root:p1": l.pane("idle", l.root)}})
	h := l.startHelper("stable", []string{stable}, "parent")
	l.awaitHeld(h, "prompt", "w-root:p1")

	result, _ := l.pumpFor([]string{legacy}, "parent", 3*time.Second)
	if got := states(result)["w-root:p1"]; got != "deferred" {
		t.Fatalf("legacy-spelled pass = %s (%v), want deferred behind the same recipient", got, result)
	}
	l.release("w-root:p1")
	if got := states(h.result(t))["w-root:p1"]; got != "submitted" {
		t.Fatalf("stable-spelled pass = %s, want submitted", got)
	}
	again, _ := l.pumpFor([]string{legacy}, "parent", 8*time.Second)
	if got := states(again)["w-root:p1"]; got != "quiet" {
		t.Fatalf("after the first notice, pass = %s (%v), want quiet", got, again)
	}
	sent := prompts(l.calls(), "w-root:p1")
	if len(sent) != 1 || !strings.Contains(sent[0], stable) || !strings.Contains(sent[0], legacy) {
		t.Fatalf("prompts = %v, want one coalesced notice naming both tasks", sent)
	}
}

// R4: a crash after the in-flight stamp leaves the attempt uncertain; it is never replayed and the lock is free.
func TestCrashAfterPossibleSubmissionStaysUncertain(t *testing.T) {
	l := newPassLab(t)
	a := l.worker("lab", "w1:p1")
	l.session("lab", map[string]any{"hold": map[string]any{"w1:p1": "prompt"}, "panes": map[string]any{
		"w1:p1": l.pane("idle", l.worktree(a))}})
	h := l.startHelper("a", []string{a}, "worker")
	l.awaitHeld(h, "prompt", "w1:p1")
	h.kill(t)
	l.release("w1:p1")

	result, elapsed := l.pumpFor([]string{a}, "worker", 8*time.Second)
	if got := states(result)["w1:p1"]; got != "uncertain" {
		t.Fatalf("after a crash mid-prompt, state = %s (%v), want uncertain", got, result)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("pass after the crash took %s; the dead process's locks must be free", elapsed)
	}
	if n := len(prompts(l.calls(), "w1:p1")); n != 1 {
		t.Fatalf("prompts = %d, want the one interrupted attempt and no replay", n)
	}
	d := deliveries(t, l.s, a)
	if len(d) != 1 || fmt.Sprint(func() any { v, _ := d[0].Get("state"); return v }()) != "in-flight" {
		t.Fatalf("deliveries = %v, want the interrupted attempt kept in-flight", d)
	}
}

// R4: a crash during observation, before the in-flight stamp, records nothing; the next pass sends once.
func TestCrashBeforeSubmissionLeavesThePendingReturn(t *testing.T) {
	l := newPassLab(t)
	a := l.worker("lab", "w1:p1")
	panes := map[string]any{"w1:p1": l.pane("idle", l.worktree(a))}
	l.session("lab", map[string]any{"hold": map[string]any{"w1:p1": "get"}, "panes": panes})
	h := l.startHelper("a", []string{a}, "worker")
	l.awaitHeld(h, "get", "w1:p1")
	h.kill(t)
	l.release("w1:p1")
	if d := deliveries(t, l.s, a); len(d) != 0 {
		t.Fatalf("deliveries = %v, want nothing recorded before the prompt", d)
	}
	l.session("lab", map[string]any{"panes": panes})

	result, _ := l.pumpFor([]string{a}, "worker", 8*time.Second)
	if got := states(result)["w1:p1"]; got != "submitted" {
		t.Fatalf("state = %s (%v), want submitted", got, result)
	}
	if n := len(prompts(l.calls(), "w1:p1")); n != 1 {
		t.Fatalf("prompts = %d, want 1", n)
	}
}

// R5: an obligation closed, or a task rebound, while the recipient is being observed gets no prompt and no stamp.
func TestChangeDuringObservationSendsNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(task *ordjson.Object)
	}{
		{"closed", func(task *ordjson.Object) {
			questions, _ := task.Get("questions")
			questions.([]any)[0].(*ordjson.Object).Set("status", "applied")
		}},
		{"rebound", func(task *ordjson.Object) { task.Set("pane", "w1:p9") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newPassLab(t)
			a := l.worker("lab", "w1:p1")
			l.session("lab", map[string]any{"hold": map[string]any{"w1:p1": "get"}, "panes": map[string]any{
				"w1:p1": l.pane("idle", l.worktree(a)), "w1:p9": l.pane("idle", l.worktree(a))}})
			h := l.startHelper("a", []string{a}, "worker")
			l.awaitHeld(h, "get", "w1:p1")
			l.mutate(a, tc.change)
			l.release("w1:p1")
			result := h.result(t)
			if n := countCalls(l.calls(), "prompt"); n != 0 {
				t.Fatalf("prompts = %d (%v), want none after the record changed", n, result)
			}
			if d := deliveries(t, l.s, a); len(d) != 0 {
				t.Fatalf("deliveries = %v, want no in-flight stamp", d)
			}
		})
	}
}

// R5/R9: when one of two coalesced returns closes during observation, the survivor is still sent, alone.
func TestSurvivingObligationIsSentAloneAfterAConcurrentClose(t *testing.T) {
	l := newPassLab(t)
	closing := l.asking(l.host)
	surviving := l.asking(l.host)
	l.session("lab", map[string]any{"hold": map[string]any{"w-root:p1": "get"}, "panes": map[string]any{
		"w-root:p1": l.pane("idle", l.root)}})
	h := l.startHelper("both", []string{closing}, "parent")
	l.awaitHeld(h, "get", "w-root:p1")
	l.mutate(closing, func(task *ordjson.Object) {
		questions, _ := task.Get("questions")
		questions.([]any)[0].(*ordjson.Object).Set("status", "answered")
		questions.([]any)[0].(*ordjson.Object).Set("answered_at", "2026-09-24T00:05:00+00:00")
	})
	l.release("w-root:p1")
	if got := states(h.result(t))["w-root:p1"]; got != "submitted" {
		t.Fatalf("state = %s, want the survivor submitted", got)
	}
	sent := prompts(l.calls(), "w-root:p1")
	if len(sent) != 1 || !strings.Contains(sent[0], surviving) || strings.Contains(sent[0], closing) {
		t.Fatalf("prompts = %v, want one notice naming only %s", sent, surviving)
	}
	for _, d := range deliveries(t, l.s, closing) {
		t.Fatalf("closed task has a delivery record: %v", d)
	}
}

// KTD3: a recipient another process is prompting is revisited after the others and waited for within the budget;
// once the other process has sent, this pass finds nothing left to send.
func TestContendedRecipientIsRevisitedLastWithinTheBudget(t *testing.T) {
	l := newPassLab(t)
	a := l.worker("lab", "w1:p1")
	b := l.worker("lab", "w2:p1")
	l.session("lab", map[string]any{"hold": map[string]any{"w1:p1": "prompt"}, "panes": map[string]any{
		"w1:p1": l.pane("idle", l.worktree(a)), "w2:p1": l.pane("idle", l.worktree(b))}})
	h := l.startHelper("a", []string{a}, "worker")
	l.awaitHeld(h, "prompt", "w1:p1")
	// Release the held recipient only once this pass has prompted the free one and has been waiting on the held one,
	// so the measured wait does not depend on how fast the free recipient's prompt is.
	released := make(chan struct{})
	go func() {
		defer close(released)
		deadline := time.Now().Add(20 * time.Second)
		for len(prompts(l.calls(), "w2:p1")) == 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(400 * time.Millisecond)
		l.release("w1:p1")
	}()

	result, elapsed := l.pumpFor([]string{a, b}, "worker", 10*time.Second)
	<-released
	got := states(result)
	t.Logf("contended pass: elapsed %s, lock_wait_ms %s", elapsed, fanoutField(result, "lock_wait_ms"))
	if got["w2:p1"] != "submitted" || got["w1:p1"] != "quiet" {
		t.Fatalf("states = %v, want the free recipient submitted and the contended one quiet after the other process sent", got)
	}
	var wait int
	fmt.Sscan(fanoutField(result, "lock_wait_ms"), &wait)
	if wait < 300 || elapsed > 5*time.Second {
		t.Fatalf("lock_wait_ms = %d, elapsed %s; want a bounded wait for the held recipient", wait, elapsed)
	}
	if got := states(h.result(t))["w1:p1"]; got != "submitted" {
		t.Fatalf("other process = %s, want submitted", got)
	}
	calls := l.calls()
	if len(prompts(calls, "w1:p1")) != 1 || len(prompts(calls, "w2:p1")) != 1 {
		t.Fatalf("calls = %v, want one prompt per recipient", calls)
	}
}

// holdRecipient holds route's recipient lock the way another sumctl process would, through its own Store.
func (l *passLab) holdRecipient(session, pane, cwd string) func() {
	l.t.Helper()
	other, err := store.Open(l.s.Home)
	if err != nil {
		l.t.Fatal(err)
	}
	route := ordjson.NewObject()
	for k, v := range map[string]any{"machine": l.host, "session": session, "pane": pane, "cwd": cwd} {
		route.Set(k, v)
	}
	unlock, err := LockRecipient(other, context.Background(), route)
	if err != nil {
		l.t.Fatal(err)
	}
	return unlock
}

// KTD3: a contended revisit waits only while an observation and a prompt could still follow. With too little budget
// it defers at once; otherwise it gives up when that bound passes. Either way nothing is sent or recorded.
func TestContendedRecipientDefersWhenItsWaitCannotLeadToAPrompt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		budget time.Duration
		reason string
		within time.Duration
	}{
		// The cheapest observe-and-prompt cost is 2 x 200 ms plus 2 x PipeGrace (4 s).
		{"too little budget to wait", 3 * time.Second, "too little of the pass budget remained", time.Second},
		{"held past the bound", 5 * time.Second, "for the rest of this pass's budget", 3 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newPassLab(t)
			ObserveTimeout, PromptTimeout = 200*time.Millisecond, 200*time.Millisecond
			a := l.worker("lab", "w1:p1")
			l.session("lab", map[string]any{"panes": map[string]any{"w1:p1": l.pane("idle", l.worktree(a))}})
			release := l.holdRecipient("lab", "w1:p1", l.worktree(a))
			defer release()
			result, elapsed := l.pumpFor([]string{a}, "worker", tc.budget)
			rows, _ := result.Get("recipients")
			row := rows.([]any)[0].(*ordjson.Object)
			state, _ := row.Get("state")
			reason, _ := row.Get("reason")
			if state != "deferred" || !strings.Contains(fmt.Sprint(reason), tc.reason) {
				t.Fatalf("row = %v / %v, want deferred with %q", state, reason, tc.reason)
			}
			if elapsed > tc.within {
				t.Fatalf("pass took %s, want under %s", elapsed, tc.within)
			}
			if len(l.calls()) != 0 || len(deliveries(t, l.s, a)) != 0 {
				t.Fatalf("calls = %v, deliveries = %v; want nothing sent or recorded", l.calls(), deliveries(t, l.s, a))
			}
		})
	}
}

// R5: a recipient deregistered while it was being observed is refused at the claim, before any prompt.
func TestDeregistrationDuringObservationIsRefusedAtTheClaim(t *testing.T) {
	l := newPassLab(t)
	a := l.worker("lab", "w1:p1")
	l.session("lab", map[string]any{"hold": map[string]any{"w1:p1": "get"}, "panes": map[string]any{
		"w1:p1": l.pane("idle", l.worktree(a))}})
	h := l.startHelper("a", []string{a}, "worker")
	l.awaitHeld(h, "get", "w1:p1")
	key := store.RegistrationKey(store.Endpoint{Machine: l.host, Session: "lab", Pane: "w1:p1"})
	if err := os.Remove(filepath.Join(l.s.Sessions, key+".json")); err != nil {
		t.Fatal(err)
	}
	l.release("w1:p1")
	result := h.result(t)
	if got := states(result)["w1:p1"]; got != "not-delivered" {
		t.Fatalf("state = %s (%v), want not-delivered", got, result)
	}
	if n := countCalls(l.calls(), "prompt"); n != 0 {
		t.Fatalf("prompts = %d, want none to a deregistered pane", n)
	}
}
