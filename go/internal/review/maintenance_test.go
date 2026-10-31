package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/evidenceview"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/taskcheckout"
)

type reviewerOperationResult struct {
	view *ordjson.Object
	err  error
}

func awaitReviewerBoundary(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("native operation never reached its synchronization boundary")
	}
}
func awaitReviewerOperation(t *testing.T, result <-chan reviewerOperationResult) reviewerOperationResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("native reviewer operation did not finish")
		return reviewerOperationResult{}
	}
}

func claimReviewerMaintenance(t *testing.T, s *store.Store, taskID, kind string) {
	t.Helper()
	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	marker := ordjson.NewObject()
	if kind == "cleanup" {
		marker.Set("intent", "test-cleanup-intent")
		task.Set("cleanup", marker)
	} else {
		marker.Set("state", kind)
		marker.Set("operation", "terminal.rm")
		task.Set("terminal_maintenance", marker)
	}
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
}
func receiptedMaintenanceReviewer(t *testing.T) (*store.Store, *ordjson.Object, store.Endpoint, *hctest.Fixture) {
	t.Helper()
	s, ctx, endpoint, f := recoveryLab(t)
	task, err := s.ReadTask(endpoint.Task)
	if err != nil {
		t.Fatal(err)
	}
	terminal := f.Terminals()[0]
	binding := (incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: *terminal.PID, Foreground: terminal.Foreground}).Record(store.Now())
	objectField(task, "reviewer").Set("incarnation", binding)
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	registration, err := s.Register(endpoint, "reviewer", endpoint.Task, binding)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: "reviewer-init", Attempt: endpoint.Attempt})
	registration.Set("native_receipt", receipt)
	if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(endpoint)+".json"), registration); err != nil {
		t.Fatal(err)
	}
	return s, ctx, endpoint, f
}
func maintenanceNewReviewerLab(t *testing.T) (*store.Store, *ordjson.Object, *ordjson.Object, string, *hctest.Fixture) {
	t.Helper()
	s, ctx, _, f := recoveryLab(t)
	task, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	var template returns.NativeTemplate
	if err = decodeObject(objectField(objectField(task, "reviewer"), "native_template"), &template); err != nil {
		t.Fatal(err)
	}
	runtime := filepath.Dir(filepath.Dir(filepath.Dir(template.Spec.Binary)))
	skill := filepath.Join(runtime, "skills/sum-deliver/SKILL.md")
	if err = os.MkdirAll(filepath.Dir(skill), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(skill, []byte("# Independent reviewer\nRead the candidate.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	intent, err := taskcheckout.Plan(s.Root, asString(task, "worktree"), asString(task, "id"), evidenceview.CurrentCandidate(task))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = taskcheckout.Create(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	value, _ := ordjson.FromValue(intent)
	task.Set("checkout_intent", value)
	task.Set("worktree", intent.Path)
	task.Delete("reviewer")
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	task.Set("machine", host.ID)
	dir, err := s.TaskPath(asString(task, "id"))
	if err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(dir, "brief.md")
	if err = os.WriteFile(brief, []byte("Approved scope\n"), 0600); err != nil {
		t.Fatal(err)
	}
	task.Set("brief_path", brief)
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	nativeTemplate, _ := ordjson.FromValue(template)
	ctx.Set("native_template", nativeTemplate)
	if err = ordjson.WriteFile(filepath.Join(s.Home, "context.json"), ctx); err != nil {
		t.Fatal(err)
	}
	f.SetTemplates([]hcclient.TemplateInfo{{Name: template.Spec.Name, Command: templateCommand(template.Spec.Argv)}})
	f.SetTemplateSubmit(template.Spec.Name, "tab")
	return s, ctx, task, runtime, f
}

func TestReviewerMaintenanceClaimWinsPausedNativeObservation(t *testing.T) {
	for _, operation := range []string{"launch", "prompt", "redeliver"} {
		for _, barrier := range []string{"cleanup", "claimed", "uncertain"} {
			t.Run(operation+"/"+barrier, func(t *testing.T) {
				var s *store.Store
				var f *hctest.Fixture
				var taskID, address, method string
				var run func() (*ordjson.Object, error)
				if operation == "launch" {
					var ctx, task *ordjson.Object
					var runtime string
					s, ctx, task, runtime, f = maintenanceNewReviewerLab(t)
					taskID = asString(task, "id")
					method = "templates.list"
					run = func() (*ordjson.Object, error) {
						return Launch(s, ctx, runtime, "/tmp/sumctl", LaunchArgs{Task: taskID})
					}
				} else {
					var ctx *ordjson.Object
					var endpoint store.Endpoint
					if operation == "prompt" {
						s, ctx, endpoint, f = receiptedMaintenanceReviewer(t)
					} else {
						s, ctx, endpoint, f = recoveryLab(t)
					}
					taskID, address, method = endpoint.Task, endpoint.Address, "terminal.get"
					if operation == "prompt" {
						task, err := s.ReadTask(taskID)
						if err != nil {
							t.Fatal(err)
						}
						run = func() (*ordjson.Object, error) {
							return promptNativeReviewer(context.Background(), s, task, objectField(task, "reviewer"), "/tmp/sumctl")
						}
					} else {
						run = func() (*ordjson.Object, error) { return Redeliver(s, ctx, taskID, endpoint.Attempt) }
					}
				}
				entered, release := f.HoldNext(method, address)
				defer release()
				result := make(chan reviewerOperationResult, 1)
				go func() { view, err := run(); result <- reviewerOperationResult{view, err} }()
				awaitReviewerBoundary(t, entered)
				claimReviewerMaintenance(t, s, taskID, barrier)
				release()
				got := awaitReviewerOperation(t, result)
				if got.err == nil || !strings.Contains(got.err.Error(), "refused because") {
					t.Fatalf("cleanup barrier lost paused %s: view=%v err=%v", operation, got.view, got.err)
				}
				for _, call := range f.Calls() {
					if call.Method == "terminal.run" || call.Method == "terminal.send" {
						t.Fatalf("%s crossed cleanup barrier via %s", operation, call.Method)
					}
				}
				saved, err := s.ReadTask(taskID)
				if err != nil {
					t.Fatal(err)
				}
				if operation == "launch" {
					if objectField(saved, "reviewer") != nil {
						t.Fatal("refused launch created a durable attempt")
					}
				} else if asString(objectField(objectField(saved, "reviewer"), "prompt"), "state") != "not-delivered" {
					t.Fatal("refused input created a durable in-flight claim")
				}
			})
		}
	}
}

func TestReviewerMaintenancePreservesAlreadyClaimedNativeOutcomes(t *testing.T) {
	for _, operation := range []string{"launch", "prompt", "redeliver"} {
		for _, lost := range []bool{false, true} {
			t.Run(operation+map[bool]string{false: "/submitted", true: "/uncertain"}[lost], func(t *testing.T) {
				var s *store.Store
				var f *hctest.Fixture
				var taskID, address, method string
				var run func() (*ordjson.Object, error)
				if operation == "launch" {
					var ctx, task *ordjson.Object
					var runtime string
					s, ctx, task, runtime, f = maintenanceNewReviewerLab(t)
					taskID = asString(task, "id")
					method = "terminal.run"
					f.QueueFault(method, hctest.Fault{LoseResponse: lost, FirstMessage: &hcclient.FirstMessage{Sent: true}})
					run = func() (*ordjson.Object, error) {
						return Launch(s, ctx, runtime, "/tmp/sumctl", LaunchArgs{Task: taskID})
					}
				} else {
					var ctx *ordjson.Object
					var endpoint store.Endpoint
					if operation == "prompt" {
						s, ctx, endpoint, f = receiptedMaintenanceReviewer(t)
					} else {
						s, ctx, endpoint, f = recoveryLab(t)
					}
					taskID, address, method = endpoint.Task, endpoint.Address, "terminal.send"
					f.QueueFault(method, hctest.Fault{LoseResponse: lost})
					if operation == "prompt" {
						task, err := s.ReadTask(taskID)
						if err != nil {
							t.Fatal(err)
						}
						run = func() (*ordjson.Object, error) {
							return promptNativeReviewer(context.Background(), s, task, objectField(task, "reviewer"), "/tmp/sumctl")
						}
					} else {
						run = func() (*ordjson.Object, error) { return Redeliver(s, ctx, taskID, endpoint.Attempt) }
					}
				}
				entered, release := f.HoldNext(method, address)
				defer release()
				result := make(chan reviewerOperationResult, 1)
				go func() { view, err := run(); result <- reviewerOperationResult{view, err} }()
				awaitReviewerBoundary(t, entered)
				claimReviewerMaintenance(t, s, taskID, "claimed")
				release()
				got := awaitReviewerOperation(t, result)
				if got.err != nil && !(lost && operation == "launch") {
					t.Fatal(got.err)
				}
				saved, err := s.ReadTask(taskID)
				if err != nil {
					t.Fatal(err)
				}
				want := "submitted"
				if lost {
					want = "uncertain"
				}
				if asString(objectField(objectField(saved, "reviewer"), "prompt"), "state") != want {
					t.Fatalf("maintenance erased attempted %s outcome: %v", operation, saved)
				}
				if asString(objectField(saved, "terminal_maintenance"), "state") != "claimed" {
					t.Fatal("outcome recording removed cleanup ownership")
				}
			})
		}
	}
}

func TestReviewerApplicationReceiptRemainsRecordableDuringMaintenance(t *testing.T) {
	s, _, endpoint, _ := receiptedMaintenanceReviewer(t)
	task, err := s.ReadTask(endpoint.Task)
	if err != nil {
		t.Fatal(err)
	}
	candidate := asString(objectField(task, "reviewer"), "candidate")
	objectField(objectField(task, "reviewer"), "prompt").Set("state", "uncertain")
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	claimReviewerMaintenance(t, s, endpoint.Task, "uncertain")
	if err = validateNativeReview(s, endpoint.Task, candidate, "", endpoint.Object()); err != nil {
		t.Fatal(err)
	}
	if _, _, err = record(s, endpoint.Task, "comment", candidate, "", "independent findings", false, nil, nil, endpoint.Object()); err != nil {
		t.Fatal(err)
	}
	saved, err := s.ReadTask(endpoint.Task)
	if err != nil {
		t.Fatal(err)
	}
	if asString(objectField(objectField(saved, "reviewer"), "prompt"), "state") != "applied" || asString(objectField(saved, "terminal_maintenance"), "state") != "uncertain" {
		t.Fatal("matching application receipt was blocked or erased maintenance")
	}
}
