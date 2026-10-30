package execution

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/prepare"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/roleinit"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type redeliveryLab struct {
	s             *store.Store
	ctx           *ordjson.Object
	endpoint      store.Endpoint
	fixture       *hctest.Fixture
	runtime, path string
}

func newRedeliveryLab(t *testing.T) redeliveryLab {
	t.Helper()
	runtime, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(root, "repo")
	if err = os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(cwd string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		data, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %v %s", args, e, data)
		}
		return strings.TrimSpace(string(data))
	}
	git(repo, "init", "-b", "main")
	if err = os.WriteFile(filepath.Join(repo, "README"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(repo, "add", "README")
	git(repo, "commit", "-m", "base")
	remote := filepath.Join(root, "origin.git")
	git(root, "clone", "--bare", repo, remote)
	git(repo, "remote", "add", "origin", remote)
	codex := filepath.Join(root, "codex")
	if err = os.WriteFile(codex, []byte("#!/bin/sh\nprintf '%s\\n' 'codex-cli 0.166.0'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_CODEX_BIN", codex)
	config, home := filepath.Join(root, "config"), filepath.Join(root, "codex-home")
	if err = os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	options := launch.HCTemplateOptions{Cwd: runtime, CodexHome: home}
	template, err := launch.PrepareHCTemplate(runtime, config, "codex", nil, options)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(root, "state"))
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
	coordinator := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: fixture.CoreID, Socket: fixture.Socket, Session: "sum-test", Address: "t1", CreatedMS: 1, Role: "coordinator", Attempt: "bootstrap", BindingGeneration: 1, Cwd: runtime}
	pid := uint32(100000)
	fg := &hcclient.Foreground{PID: 100001, Name: "codex", Argv: template.Argv, Cwd: runtime}
	fixture.SetTerminal(hcclient.Terminal{Address: "t1", CreatedMS: 1, Cwd: runtime, State: "live", PID: &pid, Foreground: fg})
	observed := incarnation.NativeEvidence{Endpoint: coordinator.Native(), PID: pid, Foreground: fg}
	owner := coordinator.Object()
	owner.Set("incarnation", observed.Record(store.Now()))
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: coordinator.Native(), Kind: "coordinator-init", Attempt: coordinator.Attempt})
	owner.Set("native_receipt", receipt)
	provenance, _ := ordjson.FromValue(prepare.NativeTemplate{Spec: template, ConfigHome: config, Options: options})
	owner.Set("native_template", provenance)
	if _, err = s.Register(coordinator, "coordinator", nil, observed.Record(store.Now())); err != nil {
		t.Fatal(err)
	}
	if err = ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	if err = fixture.LoadTemplatesFrom(config); err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(root, "approved.md")
	if err = os.WriteFile(brief, []byte("Only the approved task."), 0600); err != nil {
		t.Fatal(err)
	}
	fixture.QueueFault("terminal.run", hctest.Fault{FirstMessage: &hcclient.FirstMessage{Sent: false}})
	task, err := prepare.Dispatch(s, coordinator.Object(), prepare.Args{Repo: repo, Base: "main", Brief: brief, Harness: "codex", Approved: true, RuntimeRoot: runtime, SumctlPath: filepath.Join(runtime, "bin/sumctl")})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := store.EndpointFromContext(asObject(field(task, "worker_endpoint")))
	if err = endpoint.Validate(); err != nil {
		t.Fatal(err)
	}
	// Model native creation/process evidence without creating an application
	// receipt. The receipt tests below invoke actual worker init separately.
	for _, terminal := range fixture.Terminals() {
		if terminal.Address == endpoint.Address {
			program := uint32(200000)
			terminal.PID = &program
			terminal.Foreground = &hcclient.Foreground{PID: 200001, Name: "codex", Argv: []string{codex, "--no-daemon"}, Cwd: terminal.Cwd}
			fixture.SetTerminal(terminal)
			fixture.SetStatus(endpoint.Address, "idle")
			bound := incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: program, Foreground: terminal.Foreground}
			if err = s.SetIncarnation(endpoint, bound.Record(store.Now())); err != nil {
				t.Fatal(err)
			}
		}
	}
	path, err := s.TaskPath(endpoint.Task)
	if err != nil {
		t.Fatal(err)
	}
	return redeliveryLab{s, coordinator.Object(), endpoint, fixture, runtime, filepath.Join(path, "task.json")}
}
func redeliveryTaskBytes(t *testing.T, l redeliveryLab) []byte {
	t.Helper()
	data, err := os.ReadFile(l.path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func redeliveryCalls(l redeliveryLab, method string) int {
	n := 0
	for _, call := range l.fixture.Calls() {
		if call.Method == method {
			n++
		}
	}
	return n
}
func mutateRedeliveryTask(t *testing.T, l redeliveryLab, mutate func(*ordjson.Object, *ordjson.Object)) {
	t.Helper()
	other, err := store.Open(l.s.Root)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := other.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	task, err := other.ReadTask(l.endpoint.Task)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := reservations.Worker(task)
	if err != nil {
		t.Fatal(err)
	}
	mutate(task, worker)
	if err = other.SaveTask(task); err != nil {
		t.Fatal(err)
	}
}
func assertRedeliveryRefusalUnchanged(t *testing.T, l redeliveryLab, before []byte, err error) {
	t.Helper()
	if err == nil {
		t.Error("unproven redelivery was accepted")
	}
	if !bytes.Equal(before, redeliveryTaskBytes(t, l)) {
		t.Error("refused redelivery rewrote the task or claimed an input")
	}
	if n := redeliveryCalls(l, "terminal.send"); n != 0 {
		t.Errorf("refused redelivery sent %d native inputs", n)
	}
	if redeliveryCalls(l, "terminal.run") != 1 {
		t.Error("redelivery created another native process")
	}
}

func TestRedeliverRequiresCreatedAndExplicitlyUnsentFirstMessage(t *testing.T) {
	for _, tc := range []struct {
		name, effect string
		first        any
		missing      bool
	}{
		{name: "sent true", effect: "created", first: map[string]any{"sent": true}},
		{name: "first message null", effect: "created", first: nil},
		{name: "first message missing", effect: "created", missing: true},
		{name: "sent null", effect: "created", first: map[string]any{"sent": nil}},
		{name: "sent missing", effect: "created", first: map[string]any{"started": false}},
		{name: "unknown creation", effect: "uncertain", first: map[string]any{"sent": false}},
		{name: "claimed creation", effect: "claimed", first: map[string]any{"sent": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newRedeliveryLab(t)
			mutateRedeliveryTask(t, l, func(task, worker *ordjson.Object) {
				native := asObject(field(worker, "native_launch"))
				native.Set("effect", tc.effect)
				if tc.missing {
					native.Delete("first_message")
				} else {
					var first any
					if tc.first != nil {
						first, _ = ordjson.FromValue(tc.first)
					}
					native.Set("first_message", first)
				}
			})
			before := redeliveryTaskBytes(t, l)
			_, err := Redeliver(l.s, l.ctx, l.runtime, l.endpoint.Task, l.endpoint.Attempt)
			assertRedeliveryRefusalUnchanged(t, l, before, err)
		})
	}
}

func TestRedeliverRevalidatesReceiptLaunchFactsAndMaintenanceAtClaim(t *testing.T) {
	for _, kind := range []string{"init receipt checkpoint", "sent fact changed", "cleanup intent", "maintenance claimed", "maintenance uncertain"} {
		t.Run(kind, func(t *testing.T) {
			l := newRedeliveryLab(t)
			entered, release := l.fixture.HoldNext("templates.list", "")
			defer release()
			done := make(chan error, 1)
			go func() { _, err := Redeliver(l.s, l.ctx, l.runtime, l.endpoint.Task, l.endpoint.Attempt); done <- err }()
			select {
			case <-entered:
			case err := <-done:
				t.Fatalf("redelivery ended before observation boundary: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("redelivery never observed template")
			}
			if kind == "init receipt checkpoint" {
				// Init writes this exact registration receipt before its task transition.
				// Model a crash at that durable checkpoint: task generation is unchanged.
				other, err := store.Open(l.s.Root)
				if err != nil {
					t.Fatal(err)
				}
				unlock, err := other.Lock()
				if err != nil {
					t.Fatal(err)
				}
				reg, err := other.Registration(l.endpoint)
				if err != nil {
					t.Fatal(err)
				}
				receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: l.endpoint.Native(), Kind: "worker-init", Attempt: l.endpoint.Attempt})
				reg.Set("native_receipt", receipt)
				if err = ordjson.WriteFile(filepath.Join(other.Sessions, store.RegistrationKey(l.endpoint)+".json"), reg); err != nil {
					t.Fatal(err)
				}
				unlock()
			} else {
				mutateRedeliveryTask(t, l, func(task, worker *ordjson.Object) {
					switch kind {
					case "sent fact changed":
						asObject(field(worker, "native_launch")).Set("first_message", func() *ordjson.Object { v := ordjson.NewObject(); v.Set("sent", true); return v }())
					case "cleanup intent":
						v := ordjson.NewObject()
						v.Set("intent", true)
						task.Set("cleanup", v)
					default:
						v := ordjson.NewObject()
						state := "claimed"
						if kind == "maintenance uncertain" {
							state = "uncertain"
						}
						v.Set("state", state)
						task.Set("terminal_maintenance", v)
					}
				})
			}
			before := redeliveryTaskBytes(t, l)
			release()
			var err error
			select {
			case err = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("redelivery did not finish after observation")
			}
			assertRedeliveryRefusalUnchanged(t, l, before, err)
		})
	}
}

func TestRedeliverKnownUnsentDoesNotAdoptSendAndReceiptStillConfirms(t *testing.T) {
	for _, lost := range []bool{false, true} {
		name := "submitted"
		if lost {
			name = "uncertain response"
		}
		t.Run(name, func(t *testing.T) {
			l := newRedeliveryLab(t)
			if lost {
				l.fixture.QueueFault("terminal.send", hctest.Fault{LoseResponse: true})
			}
			_, err := Redeliver(l.s, l.ctx, l.runtime, l.endpoint.Task, l.endpoint.Attempt)
			if (err != nil) != lost {
				t.Fatalf("redelivery response %v", err)
			}
			task, err := l.s.ReadTask(l.endpoint.Task)
			if err != nil {
				t.Fatal(err)
			}
			worker, err := reservations.Worker(task)
			if err != nil {
				t.Fatal(err)
			}
			native := asObject(field(worker, "native_launch"))
			diagnostic := field(asObject(field(native, "redelivery")), "effect")
			want := "submitted"
			if lost {
				want = "uncertain"
			}
			if diagnostic != want || field(worker, "state") == "running" {
				t.Fatalf("native input became application receipt or lost uncertainty: %v %v", diagnostic, field(worker, "state"))
			}
			before := redeliveryTaskBytes(t, l)
			_, err = Redeliver(l.s, l.ctx, l.runtime, l.endpoint.Task, l.endpoint.Attempt)
			if err == nil || !bytes.Equal(before, redeliveryTaskBytes(t, l)) {
				t.Fatal("claimed redelivery replayed or rewrote its diagnostic")
			}
			if redeliveryCalls(l, "terminal.send") != 1 || redeliveryCalls(l, "terminal.run") != 1 {
				t.Fatal("redelivery duplicated input or process")
			}
			initialized, err := roleinit.InitDesignated(roleinit.DesignatedOpts{RuntimeRoot: l.runtime, SumctlPath: filepath.Join(l.runtime, "bin/sumctl"), Store: l.s, Ctx: l.endpoint.Object(), Role: "worker", Task: l.endpoint.Task})
			if err != nil {
				t.Fatal(err)
			}
			if field(initialized, "role") != "worker" {
				t.Fatal("actual init did not grant worker role")
			}
			confirmed, err := Confirm(l.s, l.ctx, l.runtime, l.endpoint.Task, l.endpoint.Attempt)
			if err != nil {
				t.Fatal(err)
			}
			if field(confirmed, "confirmed") != true {
				t.Fatal("matching init receipt did not confirm")
			}
			current, err := l.s.ReadTask(l.endpoint.Task)
			if err != nil {
				t.Fatal(err)
			}
			worker, err = reservations.Worker(current)
			if err != nil {
				t.Fatal(err)
			}
			native = asObject(field(worker, "native_launch"))
			if field(worker, "state") != "running" || field(asObject(field(native, "redelivery")), "effect") != want {
				t.Fatal("confirmation discarded original native diagnostic")
			}
			l.fixture.AssertNoForce(t)
		})
	}
}
