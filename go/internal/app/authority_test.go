package app

import (
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type actorLab struct {
	store        *store.Store
	task         *ordjson.Object
	endpoint     store.Endpoint
	fixture      *hctest.Fixture
	terminal     hcclient.Terminal
	registration *ordjson.Object
}

func taskActorLab(t *testing.T, role string) actorLab {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	fixture := hctest.New(t)
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: fixture.CoreID, Session: "sum-test-authority", Socket: fixture.Socket, Address: "t1", CreatedMS: 1, Task: "t-aaaaaaaaaaaa", Role: role, Attempt: "x-aaaaaaaaaaaa", BindingGeneration: 1}
	if role == "coordinator" {
		endpoint.Task = ""
	}
	pid := uint32(100)
	terminal := hcclient.Terminal{Address: "t1", CreatedMS: 1, State: "live", PID: &pid, Foreground: &hcclient.Foreground{PID: 101, Argv: []string{"codex"}}}
	fixture.SetTerminal(terminal)
	observed := (incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: terminal.Foreground}).Record(store.Now())
	var taskValue any = endpoint.Task
	if role == "coordinator" {
		taskValue = nil
	}
	registration, err := s.Register(endpoint, role, taskValue, observed)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: role + "-init", Attempt: endpoint.Attempt})
	registration.Set("native_receipt", receipt)
	if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(endpoint)+".json"), registration); err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("schema", store.Schema)
	task.Set("status", "running")
	record := endpoint.Object()
	record.Set("incarnation", observed)
	record.Set("native_receipt", receipt)
	switch role {
	case "worker":
		attempt, err := reservations.NewAttempt("worker", endpoint.Object(), "/tmp/checkout", store.Now(), "running", nil)
		if err != nil {
			t.Fatal(err)
		}
		attempt.Set("id", endpoint.Attempt)
		attempt.Set("incarnation", observed)
		attempt.Set("native_receipt", receipt)
		task.Set("execution", reservations.NewExecution(attempt))
		task.Set("worker_endpoint", endpoint.Object())
	case "coordinator":
		task.Set("parent", endpoint.Object())
		if err = ordjson.WriteFile(filepath.Join(s.Home, "context.json"), record); err != nil {
			t.Fatal(err)
		}
	case "reviewer":
		checkout := t.TempDir()
		git := func(args ...string) string {
			t.Helper()
			r, err := proc.Run(append([]string{"git", "-C", checkout}, args...), "", 0, true, proc.ScrubbedEnv())
			if err != nil {
				t.Fatal(err)
			}
			return r.Stdout
		}
		git("init", "-q")
		git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "base", "--allow-empty")
		head, err := nativeCandidate(checkout)
		if err != nil {
			t.Fatal(err)
		}
		prompt := ordjson.NewObject()
		prompt.Set("candidate", head)
		record.Set("prompt", prompt)
		task.Set("reviewer", record)
		task.Set("worktree", checkout)
	}
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	return actorLab{s, task, endpoint, fixture, terminal, registration}
}
func saveRegistration(t *testing.T, l actorLab) {
	t.Helper()
	if err := ordjson.WriteFile(filepath.Join(l.store.Sessions, store.RegistrationKey(l.endpoint)+".json"), l.registration); err != nil {
		t.Fatal(err)
	}
}
func checkActorLocked(l actorLab, actor *TaskActor, roles ...string) error {
	unlock, err := l.store.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	task, err := l.store.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		return err
	}
	return CheckTaskRole(l.store, task, actor, roles...)
}

func TestTaskActorRequiresLiveProcessReceiptAndCurrentBinding(t *testing.T) {
	for _, role := range []string{"worker", "coordinator", "reviewer"} {
		t.Run(role, func(t *testing.T) {
			l := taskActorLab(t, role)
			actor, err := ObserveTaskActor(l.store, l.endpoint.Object())
			if err != nil {
				t.Fatal(err)
			}
			callsBefore := len(l.fixture.Calls())
			if err = checkActorLocked(l, actor, role); err != nil {
				t.Fatal(err)
			}
			if len(l.fixture.Calls()) != callsBefore {
				t.Fatal("commit guard performed HC IO while state lock held")
			}
			if err = checkActorLocked(l, actor, "service"); err == nil {
				t.Fatal("wrong role granted authority")
			}
			l.registration.Delete("native_receipt")
			saveRegistration(t, l)
			if _, err = ObserveTaskActor(l.store, l.endpoint.Object()); err == nil {
				t.Fatal("missing receipt granted authority")
			}
			if err = checkActorLocked(l, actor, role); err == nil {
				t.Fatal("changed registration passed commit check")
			}
		})
	}
	for _, change := range []string{"core", "attempt", "generation", "creation", "foreground", "program", "kept", "finished", "historical"} {
		t.Run(change, func(t *testing.T) {
			l := taskActorLab(t, "worker")
			endpoint := l.endpoint
			switch change {
			case "core":
				endpoint.CoreID = "other"
			case "attempt":
				endpoint.Attempt = "x-other"
			case "generation":
				endpoint.BindingGeneration++
			case "creation":
				endpoint.CreatedMS++
			case "foreground":
				l.terminal.Foreground = &hcclient.Foreground{PID: 102, Argv: []string{"codex"}}
				l.fixture.SetTerminal(l.terminal)
			case "program":
				pid := uint32(102)
				l.terminal.PID = &pid
				l.fixture.SetTerminal(l.terminal)
			case "kept":
				l.terminal.Keep = true
				l.fixture.SetTerminal(l.terminal)
			case "finished":
				l.fixture.Exit(endpoint.Address, 0)
			case "historical":
				endpoint = store.Endpoint{Machine: endpoint.Machine, Session: "old", Pane: "p1"}
			}
			if _, err := ObserveTaskActor(l.store, endpoint.Object()); err == nil {
				t.Fatal("stale or historical actor authorized")
			}
		})
	}
}
func TestTaskActorRejectsReleasedWorkerAndChangedTaskRecords(t *testing.T) {
	for _, change := range []string{"released", "attempt", "receipt", "binding", "task"} {
		t.Run(change, func(t *testing.T) {
			l := taskActorLab(t, "worker")
			actor, err := ObserveTaskActor(l.store, l.endpoint.Object())
			if err != nil {
				t.Fatal(err)
			}
			attempt, err := reservations.Worker(l.task)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "released":
				_, err = reservations.Transition(l.task, l.endpoint.Attempt, "released", store.Now(), nil, nil)
				if err != nil {
					t.Fatal(err)
				}
			case "attempt":
				attempt.Set("id", "x-other")
			case "receipt":
				attempt.Delete("native_receipt")
			case "binding":
				ep := l.endpoint
				ep.CreatedMS++
				l.task.Set("worker_endpoint", ep.Object())
			case "task":
				l.task.Set("id", "t-bbbbbbbbbbbb")
			}
			if change == "task" {
				if err = CheckTaskRole(l.store, l.task, actor, "worker"); err == nil {
					t.Fatal("wrong task authorized")
				}
				return
			}
			if err = l.store.SaveTask(l.task); err != nil {
				t.Fatal(err)
			}
			if err = checkActorLocked(l, actor, "worker"); err == nil {
				t.Fatal("changed/released task granted authority")
			}
		})
	}
}
func TestTaskActorReviewerCandidateFollowsActualGitHEAD(t *testing.T) {
	l := taskActorLab(t, "reviewer")
	actor, err := ObserveTaskActor(l.store, l.endpoint.Object())
	if err != nil {
		t.Fatal(err)
	}
	if err = checkActorLocked(l, actor, "reviewer"); err != nil {
		t.Fatal(err)
	}
	checkout, _ := l.task.Get("worktree")
	_, err = proc.Run([]string{"git", "-C", checkout.(string), "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "new candidate", "--allow-empty"}, "", 0, true, proc.ScrubbedEnv())
	if err != nil {
		t.Fatal(err)
	}
	if err = checkActorLocked(l, actor, "reviewer"); err == nil {
		t.Fatal("changed Git candidate granted authority")
	}
	if _, err = ObserveTaskActor(l.store, l.endpoint.Object()); err == nil {
		t.Fatal("stale candidate assignment granted authority")
	}
}
