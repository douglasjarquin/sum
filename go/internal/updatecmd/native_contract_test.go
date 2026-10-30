package updatecmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/hookstatus"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestNativeTargetContractUsesExecutableEvidence(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go/internal/returns/wake.go"), "package returns\n")
	writeFile(t, filepath.Join(root, "go/internal/machine/machine.go"), "package machine\n")
	plantNativeHelper(t, root, "#!/bin/sh\nprintf '%s\\n' '{\"supports\":{\"state_schema\":[1]}}'\n")
	if _, err := nativeTargetContract(root); err == nil {
		t.Fatal("schema-1 executable accepted from source markers")
	}
	for _, missing := range []string{"state", "endpoint", "notification", "observer", "wake"} {
		t.Run(missing, func(t *testing.T) {
			c := contract.BuildRelease()
			switch missing {
			case "state":
				c.Supports.StateSchema = nil
			case "endpoint":
				c.Supports.EndpointSchema = nil
			case "notification":
				c.Supports.NotificationProtocol = nil
			case "observer":
				c.Supports.ObserverProtocol = nil
			case "wake":
				c.Supports.WakeProtocol = nil
			}
			raw, _ := json.Marshal(c)
			plantNativeHelper(t, root, "#!/bin/sh\nprintf '%s\\n' '"+string(raw)+"'\n")
			if _, err := nativeTargetContract(root); err == nil {
				t.Fatalf("missing %s accepted", missing)
			}
		})
	}
	raw, _ := json.Marshal(contract.BuildRelease())
	log := filepath.Join(root, "invocation")
	plantNativeHelper(t, root, "#!/bin/sh\nprintf '%s' \"$*\" > '"+log+"'\nprintf '%s\\n' '"+string(raw)+"'\n")
	if _, err := nativeTargetContract(root); err != nil {
		t.Fatal(err)
	}
	argv, _ := os.ReadFile(log)
	if string(argv) != "--format json release-contract" {
		t.Fatalf("target invoked with %q", argv)
	}
}

func TestCompatibilityRefusesLegacyServingBinaryWithoutMutation(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-b", "main")
	git(t, root, "-c", "user.name=test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	s, err := store.Open(filepath.Join(root, ".sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	current := ordjson.NewObject()
	current.Set("kind", "checkout")
	current.Set("sha", git(t, root, "rev-parse", "HEAD"))
	current.Set("path", root)
	raw, _ := json.Marshal(contract.BuildRelease())
	plantNativeHelper(t, root, "#!/bin/sh\nprintf '%s\\n' '"+string(raw)+"'\n")
	view, err := Compatibility(s, root, root, current)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := view.Get("ok"); ok != true {
		t.Fatalf("compatible native artifact refused: %s", dump(view))
	}
	before, err := os.ReadFile(filepath.Join(s.Home, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	plantNativeHelper(t, root, "#!/bin/sh\nprintf '%s\\n' '"+preIdentityContractJSON+"'\n")
	view, err = Compatibility(s, root, root, current)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := view.Get("ok"); ok != false {
		t.Fatal("serving legacy executable or old override bypassed native gate")
	}
	after, _ := os.ReadFile(filepath.Join(s.Home, "state.json"))
	if string(before) != string(after) {
		t.Fatal("refusal changed state")
	}
}

func TestUnresolvedNativeEffectsArePreserved(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	id := "t-aaaaaaaaaaaa"
	task := ordjson.NewObject()
	task.Set("id", id)
	for _, state := range []string{"in-flight", "uncertain"} {
		path := filepath.Join(s.Tasks, id, "returns.json")
		writeFile(t, path, `{"schema":1,"task":"`+id+`","deliveries":[{"state":"`+state+`"}]}`)
		before, _ := os.ReadFile(path)
		if got := unresolvedEffects(s, []*ordjson.Object{task}); len(got) == 0 {
			t.Fatalf("%s notification did not block activation", state)
		}
		after, _ := os.ReadFile(path)
		if string(before) != string(after) {
			t.Fatal("gate rewrote attempt")
		}
	}
	writeFile(t, filepath.Join(s.Tasks, id, "returns.json"), `{"schema":1,"task":"`+id+`","deliveries":[]}`)
	for _, field := range []string{"native_launch", "prompt"} {
		for _, state := range []string{"in-flight", "uncertain"} {
			record := ordjson.NewObject()
			record.Set("state", state)
			reviewer := ordjson.NewObject()
			reviewer.Set(field, record)
			task.Set("reviewer", reviewer)
			if got := unresolvedEffects(s, []*ordjson.Object{task}); len(got) == 0 {
				t.Fatalf("reviewer %s %s did not block", field, state)
			}
		}
	}
}

func TestUnresolvedLaunchIncludesRedelivery(t *testing.T) {
	for _, effect := range []string{"claimed", "uncertain"} {
		launch := ordjson.NewObject()
		launch.Set("effect", effect)
		if !unresolvedLaunch(launch) {
			t.Fatalf("%s launch accepted", effect)
		}
		redelivery := ordjson.NewObject()
		redelivery.Set("effect", effect)
		launch.Set("effect", "created")
		launch.Set("redelivery", redelivery)
		if !unresolvedLaunch(launch) {
			t.Fatalf("%s redelivery accepted", effect)
		}
	}
}

func nativeApplyLab(t *testing.T) *applyLab {
	t.Helper()
	for _, key := range []string{"SUM_HOME", "SUM_SESSION", "SUM_NOW", "SUM_CORE_ID", "SUM_TASK", "SUM_ROLE", "SUM_ATTEMPT", "SUM_BINDING_GENERATION", "HC_SOCKET", "HC_SESSION", "HC_TERMINAL_ID"} {
		t.Setenv(key, "")
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "HERDR_") {
			t.Setenv(key, "")
		}
	}

	base := t.TempDir()
	root := filepath.Join(base, "install")
	origin := filepath.Join(base, "origin.git")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-b", "main")
	git(t, root, "config", "user.name", "test")
	git(t, root, "config", "user.email", "test@example.invalid")
	git(t, root, "config", "commit.gpgsign", "false")
	writeFile(t, filepath.Join(root, ".gitignore"), ".sum/\n.local/\n")
	writeFile(t, filepath.Join(root, "AGENTS.md"), "old instructions\n")
	writeFile(t, filepath.Join(root, "bin/sumctl"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(root, "bin/sumctl"), 0755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "old")
	old := git(t, root, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(root, "AGENTS.md"), "new instructions\n")
	git(t, root, "add", "AGENTS.md")
	git(t, root, "commit", "-m", "new")
	next := git(t, root, "rev-parse", "HEAD")
	git(t, base, "clone", "--bare", "--quiet", root, origin)
	git(t, root, "remote", "add", "origin", origin)
	git(t, root, "fetch", "--quiet", "origin")
	git(t, root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	git(t, root, "reset", "--hard", old)
	s, err := store.Open(filepath.Join(root, ".sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	fixture := hctest.New(t)
	pid := uint32(101)
	terminal := hcclient.Terminal{Address: "t1", CreatedMS: 1, State: "live", PID: &pid, Foreground: &hcclient.Foreground{PID: 102, Name: "codex", Argv: []string{"codex"}, Cwd: root}}
	fixture.SetTerminal(terminal)
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: fixture.CoreID, Socket: fixture.Socket, Session: "sum-test", Address: "t1", CreatedMS: 1, Role: "coordinator", Attempt: "coordinator-one", BindingGeneration: 1, Cwd: root}
	evidence := incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: terminal.Foreground}.Record(store.Now())
	owner, err := s.Register(endpoint, "coordinator", nil, evidence)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: "coordinator-init", Attempt: endpoint.Attempt})
	owner.Set("native_receipt", receipt)
	if err = ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	if err = settings.SetNativeEventsEnabled(s, false); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(contract.BuildRelease())
	helper := "#!/bin/sh\nif [ \"$1\" = --format ]; then printf '%s\\n' '" + string(raw) + "'; fi\nexit 0\n"
	plantNativeHelper(t, root, helper)
	for _, sha := range []string{old, next} {
		buildCompatibleRelease(t, filepath.Join(root, ".local/releases"), sha)

	}
	return &applyLab{root: root, home: s.Home, oldSHA: old, newSHA: next, store: s, ctx: endpoint.Object(), fixture: fixture}
}

func TestNativeApplyRollbackPreservesGenerationAndPreferences(t *testing.T) {
	lab := nativeApplyLab(t)
	generation := lab.store.Generation
	view, err := Apply(lab.store, lab.ctx, lab.newSHA, true)
	if err != nil {
		t.Fatal(err)
	}
	if currentSHA(t, lab.root) != lab.newSHA {
		t.Fatalf("not selected: %s", dump(view))
	}
	if _, err = Rollback(lab.store, lab.ctx, lab.oldSHA); err != nil {
		t.Fatal(err)
	}
	if currentSHA(t, lab.root) != lab.oldSHA {
		t.Fatal("rollback selection differs")
	}
	if err = lab.store.CheckWritable(); err != nil {
		t.Fatal(err)
	}
	if lab.store.Generation != generation {
		t.Fatal("runtime rollback changed state generation")
	}
	enabled, err := settings.NativeEventsEnabled(lab.store)
	if err != nil || enabled {
		t.Fatal("observer preference changed", err)
	}
}

func TestNativeRollbackRefusesSchemaOneArtifactEvenWhenPreviouslyApproved(t *testing.T) {
	lab := nativeApplyLab(t)
	if _, err := Apply(lab.store, lab.ctx, lab.oldSHA, true); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(lab.store, lab.ctx, lab.newSHA, true); err != nil {
		t.Fatal(err)
	}
	plantNativeHelper(t, filepath.Join(lab.root, ".local/releases", lab.oldSHA), "#!/bin/sh\nprintf '%s\\n' '"+preIdentityContractJSON+"'\n")
	before, _ := os.ReadFile(filepath.Join(lab.store.Home, "state.json"))
	if _, err := Rollback(lab.store, lab.ctx, lab.oldSHA); err == nil {
		t.Fatal("previous approval bypassed schema-2 executable gate")
	}
	if currentSHA(t, lab.root) != lab.newSHA {
		t.Fatal("incompatible rollback changed selection")
	}
	after, _ := os.ReadFile(filepath.Join(lab.store.Home, "state.json"))
	if string(before) != string(after) {
		t.Fatal("incompatible rollback changed state")
	}
}

func TestNativeInterruptedActivationRecoveryUsesCompatibleArtifact(t *testing.T) {
	lab := nativeApplyLab(t)
	afterSelect = func() error { return fmt.Errorf("simulated interruption") }
	t.Cleanup(func() { afterSelect = nil })
	if _, err := Apply(lab.store, lab.ctx, lab.newSHA, true); err == nil {
		t.Fatal("expected interrupted activation")
	}
	afterSelect = nil
	state := activationState(t, lab.root)
	pending, _ := state.Get("pending")
	generation := strField(asObject(pending), "generation")
	reopened, err := store.Open(lab.store.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Recover(reopened, lab.ctx, generation); err != nil {
		t.Fatal(err)
	}
	if err = reopened.CheckWritable(); err != nil {
		t.Fatal(err)
	}
	if reopened.Generation != lab.store.Generation {
		t.Fatal("recovery changed state authority")
	}
}

func TestNativeCompatibilityRefusesUnresolvedObserverWithoutStartingCore(t *testing.T) {
	lab := nativeApplyLab(t)
	if err := settings.SetNativeEventsEnabled(lab.store, true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lab.store.Home, "hook", "health.json")
	writeFile(t, path, `{"schema":2,"enabled":true,"launch_effect":"uncertain"}`)
	before, _ := os.ReadFile(path)
	view, err := Compatibility(lab.store, lab.root, lab.root, DefaultRuntime(lab.root))
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := view.Get("ok"); ok != false {
		t.Fatal("unresolved observer permitted activation")
	}
	for _, call := range lab.fixture.Calls() {
		if call.Method == "terminal.run" {
			t.Fatal("compatibility launched a runtime helper")
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("observer gate changed unresolved state")
	}
}

func TestMatchingApplicationReceiptResolvesUncertainLaunchWithoutRewritingIt(t *testing.T) {
	lab := nativeApplyLab(t)
	endpoint := store.EndpointFromContext(lab.ctx)
	endpoint.Role = "worker"
	endpoint.Task = "t-aaaaaaaaaaaa"
	endpoint.Attempt = "worker-one"
	endpoint.Address = "t2"
	evidence := incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: 103, Foreground: &hcclient.Foreground{PID: 104, Name: "codex", Argv: []string{"codex"}}}.Record(store.Now())
	registration, err := lab.store.Register(endpoint, "worker", endpoint.Task, evidence)
	if err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("id", endpoint.Task)
	task.Set("worker_endpoint", endpoint.Object())
	attempt := ordjson.NewObject()
	attempt.Set("id", endpoint.Attempt)
	attempt.Set("state", "running")
	if nativeReceiptResolvesLaunch(lab.store, task, attempt) {
		t.Fatal("native status or running state supplied a receipt")
	}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: "worker-init", Attempt: endpoint.Attempt})
	registration.Set("native_receipt", receipt)
	path := filepath.Join(lab.store.Sessions, store.RegistrationKey(endpoint)+".json")
	if err = ordjson.WriteFile(path, registration); err != nil {
		t.Fatal(err)
	}
	if !nativeReceiptResolvesLaunch(lab.store, task, attempt) {
		t.Fatal("matching application receipt did not resolve launch")
	}
	attempt.Set("id", "different-attempt")
	if nativeReceiptResolvesLaunch(lab.store, task, attempt) {
		t.Fatal("different attempt inherited receipt")
	}
}

// startNativeObserverProgram runs the actual observer entrypoint when the native
// fixture emits its created event. The fixture supplies public process facts;
// Observe must produce the matching application receipt itself.
func startNativeObserverProgram(t *testing.T, lab *applyLab) {
	t.Helper()
	observerStore, err := store.Open(lab.store.Root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	client := lab.fixture.Client()
	subscription, err := client.Subscribe(ctx, hcclient.SubscribeParams{Labels: map[string]string{"sum.kind": "observer"}})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		defer subscription.Close()
		for {
			event, err := subscription.Next()
			if err != nil {
				done <- err
				return
			}
			var payload struct {
				Type string            `json:"type"`
				Data hcclient.Terminal `json:"data"`
			}
			if err = json.Unmarshal(event.Params, &payload); err != nil {
				done <- err
				return
			}
			if payload.Type != "created" {
				continue
			}
			terminal := payload.Data
			pid := uint32(201)
			terminal.PID = &pid
			terminal.Foreground = &hcclient.Foreground{PID: 202, Name: "sumctl", Argv: terminal.Argv, Cwd: terminal.Cwd}
			lab.fixture.SetTerminal(terminal)
			endpoint := store.EndpointFromContext(lab.ctx)
			endpoint.Address = terminal.Address
			endpoint.CreatedMS = terminal.CreatedMS
			observedErr := hookstatus.Observe(ctx, observerStore, endpoint, terminal.Cwd, terminal.Argv[0])
			if observedErr != nil && ctx.Err() == nil {
				t.Errorf("observer program failed: %v", observedErr)
			}
			done <- observedErr
			return
		}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && ctx.Err() == nil {
				t.Errorf("native observer: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("native observer did not stop")
		}
	})
}
