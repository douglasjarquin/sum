package cleanup

import (
	"encoding/json"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/ask"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/panes"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestCleanupNativeEndpointIdentityAndUnknownLaunchBlock(t *testing.T) {
	for _, change := range []string{"core", "creation", "kept", "unrecorded"} {
		t.Run(change, func(t *testing.T) {
			l := newLab(t)
			head := l.makeWorktreeCheckout()
			l.saveTask(l.mergedTaskJSON(head, `[]`, "released"))
			l.workerTerminal(false)
			switch change {
			case "core":
				task, _ := l.store.ReadTask(taskID)
				ep := taskWorker(task)
				ep.CoreID = "other-core"
				task.Set("worker_endpoint", ep.Object())
				if err := l.store.SaveTask(task); err != nil {
					t.Fatal(err)
				}
			case "creation", "kept":
				term := l.hc.Terminals()
				for _, terminal := range term {
					if terminal.Address != "t2" {
						continue
					}
					if change == "creation" {
						terminal.CreatedMS++
					} else {
						terminal.Keep = true
					}
					l.hc.SetTerminal(terminal)
				}
			case "unrecorded":
				l.hc.SetTerminal(hcclient.Terminal{Address: "t9", State: "live", CreatedMS: 9, Cwd: l.checkout, Labels: map[string]string{"sum.task": taskID}})
			}
			if _, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true}); err == nil {
				t.Fatal("unsafe cleanup accepted")
			}
			if _, err := os.Stat(l.checkout); err != nil {
				t.Fatal("checkout removed")
			}
			for _, call := range l.hc.Calls() {
				if call.Method == "terminal.rm" || call.Method == "terminal.kill" {
					t.Fatalf("unsafe effect=%s", call.Method)
				}
			}
		})
	}
}
func TestCleanupPendingNativeEffectBlocksArchive(t *testing.T) {
	for _, state := range []string{"in-flight", "uncertain", "unknown"} {
		t.Run(state, func(t *testing.T) {
			l := newLab(t)
			head := l.makeWorktreeCheckout()
			l.saveTask(l.mergedTaskJSON(head, `[]`, "released"))
			record := ordjson.NewObject()
			record.Set("schema", json.Number(fmt.Sprint(returns.Schema)))
			record.Set("task", taskID)
			entry := ordjson.NewObject()
			entry.Set("id", "d-pending")
			entry.Set("state", state)
			record.Set("deliveries", []any{entry})
			if err := returns.Write(l.store, record); err != nil {
				t.Fatal(err)
			}
			if !hasCode(blockerCodes(l.inspect()), "pending-effect") {
				t.Fatal("uncertain effect lost")
			}
			if _, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true}); err == nil {
				t.Fatal("uncertain native effect archived")
			}
			if _, err := os.Stat(l.checkout); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func (l *lab) reviewer(t *testing.T, pid int) store.Endpoint {
	t.Helper()
	endpoint := l.endpoint("t3", "reviewer", "review-native", 3)
	p := uint32(pid)
	fg := &hcclient.Foreground{PID: int32(pid), Name: "reviewer", Argv: []string{"reviewer"}, Cwd: l.checkout}
	l.hc.SetTerminal(hcclient.Terminal{Address: "t3", CreatedMS: 3, Cwd: l.checkout, State: "live", PID: &p, Foreground: fg})
	bound := incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: p, Foreground: fg}
	if _, err := l.store.Register(endpoint, "reviewer", taskID, bound.Record(store.Now())); err != nil {
		t.Fatal(err)
	}
	task, err := l.store.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	task.Set("reviewer", endpoint.Object())
	e := ordjson.NewObject()
	e.Set("kind", "review")
	e.Set("candidate", stringField(asObject(func() any { v, _ := task.Get("report"); return v }()), "candidate"))
	e.Set("verdict", "approve")
	e.Set("endpoint", endpoint.Object())
	task.Set("evidence", append(asList(func() any { v, _ := task.Get("evidence"); return v }()), e))
	if err = l.store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	return endpoint
}
func TestCleanupLiveReviewerAndChildRemainProtectedWhenWorkerGone(t *testing.T) {
	l := newLab(t)
	head := l.makeWorktreeCheckout()
	l.saveTask(l.mergedTaskJSON(head, `[]`, "running"))
	shell := exec.Command("sh", "-c", "sleep 120 & wait")
	shell.Dir = l.checkout
	shell.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { syscall.Kill(-shell.Process.Pid, syscall.SIGKILL); shell.Wait() }()
	l.reviewer(t, shell.Process.Pid)
	l.plantLsof([]map[string]any{{"pid": shell.Process.Pid, "cwd": l.checkout}})
	plan := l.inspect()
	if !hasCode(blockerCodes(plan), "reviewer") || !hasCode(blockerCodes(plan), "occupant") {
		t.Fatal(blockerDetails(plan))
	}
	for _, raw := range asList(func() any { v, _ := plan.Get("orphans"); return v }()) {
		if fmt.Sprint(func() any { v, _ := asObject(raw).Get("pid"); return v }()) == fmt.Sprint(shell.Process.Pid) {
			t.Fatal("live reviewer classified as orphan")
		}
	}
	if _, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true}); err == nil {
		t.Fatal("cleanup removed live reviewer")
	}
}
func TestCleanupReviewerOnlyRemovesExactFinishedBindingAndKeepsCheckout(t *testing.T) {
	l := newLab(t)
	head := l.makeWorktreeCheckout()
	l.saveTask(l.mergedTaskJSON(head, `[]`, "released"))
	endpoint := l.reviewer(t, 40000002)
	finished := uint64(9)
	code := int32(0)
	pid := uint32(40000002)
	l.hc.SetTerminal(hcclient.Terminal{Address: endpoint.Address, CreatedMS: endpoint.CreatedMS, Cwd: l.checkout, State: "finished", PID: &pid, Foreground: &hcclient.Foreground{PID: int32(pid), Name: "reviewer", Argv: []string{"reviewer"}, Cwd: l.checkout}, FinishedMS: &finished, ExitCode: &code})
	out, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true, ReviewerOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if stringField(out, "state") != "reviewer-closed" {
		t.Fatal(out)
	}
	task, _ := l.store.ReadTask(taskID)
	if !panes.ReviewerIsClosed(task) {
		t.Fatal("closure not binding-exact")
	}
	if stringField(task, "status") == "archived" {
		t.Fatal("reviewer-only archived task")
	}
	if _, err = os.Stat(l.checkout); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(l.store.Tasks, taskID, "task.json")); err != nil {
		t.Fatal("durable task lost")
	}
}

func TestCleanupNativeRemovalReleasesStateLockAndRefusesConcurrentQuestion(t *testing.T) {
	l := newLab(t)
	head := l.makeWorktreeCheckout()
	l.saveTask(l.mergedTaskJSON(head, `[]`, "released"))
	l.workerTerminal(false)
	entered, release := l.hc.HoldNext("terminal.rm", "t2")
	defer release()
	done := make(chan error, 1)
	go func() { _, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true}); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("cleanup ended before rm: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("cleanup never reached native rm")
	}
	other, err := store.Open(l.store.Root)
	if err != nil {
		t.Fatal(err)
	}
	locked := make(chan error, 1)
	go func() {
		unlock, err := other.Lock()
		if err == nil {
			err = unlock()
		}
		locked <- err
	}()
	select {
	case err = <-locked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("ledger state lock held across native I/O")
	}
	if _, err = ask.Ask(other, taskID, "concurrent-question", "Do not lose this question", func() (*ordjson.Object, error) { return nil, nil }); err == nil || !strings.Contains(err.Error(), "cleanup") {
		t.Fatalf("new question did not receive explicit cleanup refusal: %v", err)
	}
	task, err := other.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(asList(func() any { v, _ := task.Get("questions"); return v }())) != 0 {
		t.Fatal("question partially committed behind removal claim")
	}
	release()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cleanup did not complete after native release")
	}
}
func TestCleanupQuestionCommittedBeforeIntentRemainsAndAbortsClaim(t *testing.T) {
	l := newLab(t)
	head := l.makeWorktreeCheckout()
	l.saveTask(l.mergedTaskJSON(head, `[]`, "released"))
	observed, err := l.store.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	plan := l.inspect()
	execution := asObject(func() any { v, _ := plan.Get("execution"); return v }())
	attempts, _ := execution.Get("attempts")
	intent := ordjson.NewObject()
	intent.Set("worker_endpoint", taskWorker(observed).Object())
	if _, err = ask.Ask(l.store, taskID, "before-cleanup", "Keep this question", func() (*ordjson.Object, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if err = saveCleanupIntent(l.store, taskID, intent, attempts, ordjson.NewObject(), observed); err == nil {
		t.Fatal("stale cleanup snapshot accepted new question")
	}
	current, err := l.store.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	questions := asList(func() any { v, _ := current.Get("questions"); return v }())
	if len(questions) != 1 || stringField(asObject(questions[0]), "text") != "Keep this question" {
		t.Fatal("committed question lost")
	}
	if _, err = Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true}); err == nil {
		t.Fatal("cleanup passed open question")
	}
	if _, err = os.Stat(l.checkout); err != nil {
		t.Fatal(err)
	}
}
