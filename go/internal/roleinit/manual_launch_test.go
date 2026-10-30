package roleinit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestManualCoordinatorAdoptionResolvesDispatchWithoutLaunch(t *testing.T) {
	for _, test := range []struct {
		name             string
		argv             []string
		model, reasoning any
	}{
		{name: "explicit selection", argv: []string{"--model", "owner-model", "-c", `model_reasoning_effort="high"`, "--sandbox", "read-only"}, model: "owner-model", reasoning: "high"},
		{name: "unknown native selection", argv: []string{"--sandbox", "read-only"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("CODEX_HOME", t.TempDir())
			config := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".local/bin"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".local/bin/codex"), []byte("#!/bin/sh\nprintf '%s\\n' 'codex-cli 0.166.0'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "COORDINATOR.md"), []byte("Coordinator procedure.\n"), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := store.Open(filepath.Join(root, ".sum"))
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Init(); err != nil {
				t.Fatal(err)
			}
			stateValue, err := ordjson.ReadFile(filepath.Join(s.Home, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			state := stateValue.(*ordjson.Object)
			state.Set("instance", "manual-adoption-installation")
			if err = ordjson.WriteFile(filepath.Join(s.Home, "state.json"), state); err != nil {
				t.Fatal(err)
			}
			spec, err := launch.PrepareHCTemplate(root, config, "codex", test.argv, launch.HCTemplateOptions{Cwd: root})
			if err != nil {
				t.Fatal(err)
			}
			f := hctest.New(t)
			f.SetTemplates([]hcclient.TemplateInfo{{Name: spec.Name, Command: templateCommand(spec)}})
			pid := uint32(101)
			template, submit := spec.Name, "tab"
			terminal := hcclient.Terminal{Address: "t1", CreatedMS: 1, State: "live", Labels: map[string]string{"human-label": "preserved"}, Argv: []string{"sh", "-c", templateCommand(spec)}, Cwd: root, Template: &template, Submit: &submit, PID: &pid, Foreground: &hcclient.Foreground{PID: 102, Name: "codex", Argv: spec.Argv, Cwd: root}}
			f.SetTerminal(terminal)
			host, err := s.Machine()
			if err != nil {
				t.Fatal(err)
			}
			endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Socket: f.Socket, Session: "manual-adoption", Address: "t1", CreatedMS: 1, Cwd: root}
			view, err := InitDesignated(DesignatedOpts{RuntimeRoot: root, Store: s, Ctx: endpoint.Object(), Role: "coordinator", CoreConfigHome: config, Argv: test.argv})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = ordjson.MarshalCompact(view); err != nil {
				t.Fatalf("init CLI output cannot serialize: %v", err)
			}
			endpointValue, _ := view.Get("endpoint")
			bound := endpointValue.(*ordjson.Object)
			rootLaunch := launch.RootLaunch(s, bound)
			if value, _ := rootLaunch.Get("error"); value != nil {
				t.Fatalf("manual adoption cannot resolve coordinator launch: %v", value)
			}
			if value, _ := rootLaunch.Get("harness"); value != "codex" {
				t.Fatalf("harness %v", value)
			}
			for key, want := range map[string]any{"model": test.model, "reasoning": test.reasoning} {
				if value, _ := rootLaunch.Get(key); value != want {
					t.Fatalf("%s=%v want %v", key, value, want)
				}
			}
			resolved, err := launch.Resolve(s, bound, launch.ResolveArgs{})
			if err != nil {
				t.Fatalf("ordinary dispatch resolution: %v", err)
			}
			if value, _ := resolved.Get("harness"); value != "codex" {
				t.Fatalf("resolved harness %v", value)
			}
			same, err := launch.Resolve(s, bound, launch.ResolveArgs{SameAsRoot: true})
			if test.model == nil {
				if err == nil || !strings.Contains(err.Error(), "cannot prove a matching model") {
					t.Fatalf("unknown same-as-you model: %v %v", same, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if value, _ := same.Get("model"); value != test.model {
					t.Fatalf("same-as-you model %v", value)
				}
			}
			owner, err := s.Owner()
			if err != nil {
				t.Fatal(err)
			}
			if value, _ := owner.Get("wake_protocol"); fmt.Sprint(value) != fmt.Sprint(contract.WakeProtocol) {
				t.Fatalf("verified coordinator did not adopt native wake protocol: %v", value)
			}
			owner.Delete("wake_protocol")
			if err = ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
				t.Fatal(err)
			}
			if _, err = InitDesignated(DesignatedOpts{RuntimeRoot: root, Store: s, Ctx: bound}); err != nil {
				t.Fatal(err)
			}
			owner, err = s.Owner()
			if err != nil {
				t.Fatal(err)
			}
			if value, _ := owner.Get("wake_protocol"); fmt.Sprint(value) != fmt.Sprint(contract.WakeProtocol) {
				t.Fatalf("existing verified coordinator did not adopt native wake protocol: %v", value)
			}
			launchValue, _ := owner.Get("launch")
			recorded := launchValue.(*ordjson.Object)
			rawArgv, _ := recorded.Get("argv")
			var savedArgv []string
			if err = decode(rawArgv, &savedArgv); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(savedArgv, spec.Argv[1:]) {
				t.Fatalf("existing command changed: %v", savedArgv)
			}
			permissionsValue, _ := recorded.Get("permissions")
			permissions := permissionsValue.(*ordjson.Object)
			if value, _ := permissions.Get("override"); value != false {
				t.Fatalf("adoption authorized permission override: %v", value)
			}
			if value, _ := permissions.Get("status"); value != "not-exposed" {
				t.Fatalf("adoption invented runtime permissions: %v", value)
			}
			client := f.Client()
			current, err := client.Get(context.Background(), "t1")
			if err != nil {
				t.Fatal(err)
			}
			boundEndpoint := store.EndpointFromContext(bound)
			instance, _ := s.Instance()
			for key, want := range map[string]string{"human-label": "preserved", "sum.installation": instance, "sum.role": "coordinator", "sum.attempt": boundEndpoint.Attempt} {
				if current.Labels[key] != want {
					t.Fatalf("coordinator observer label %s=%q want %q", key, current.Labels[key], want)
				}
			}
			if !reflect.DeepEqual(current.Argv, terminal.Argv) {
				t.Fatal("manual adoption changed the running command")
			}
			for _, call := range f.Calls() {
				if call.Method == "terminal.run" || call.Method == "terminal.send" {
					t.Fatalf("manual adoption invoked %s", call.Method)
				}
			}
		})
	}
}

func TestExplicitCodexSelectionDoesNotGuessConflictingOrDefaultValues(t *testing.T) {
	for _, test := range []struct {
		argv             []string
		model, reasoning any
	}{
		{argv: []string{"--profile", "saved"}},
		{argv: []string{"-m", "first", "--model=second"}},
		{argv: []string{"--model=explicit", "-c", `model="different"`}},
		{argv: []string{"-c", `model="explicit"`, "-c", `model_reasoning_effort="medium"`}, model: "explicit", reasoning: "medium"},
	} {
		model, reasoning := explicitCodexSelection(test.argv)
		if model != test.model || reasoning != test.reasoning {
			t.Fatalf("argv %v yielded model=%v reasoning=%v", test.argv, model, reasoning)
		}
	}
}

func TestWorkerInitReceiptReconcilesOnlyStartupAttention(t *testing.T) {
	startup := "Native terminal created without a submitted first message; inspect and explicitly redeliver this attempt. Do not launch again."
	for _, test := range []struct {
		name, status, errorText, wantStatus string
		clearError                          bool
	}{
		{name: "starting", status: "starting", wantStatus: "running"},
		{name: "launch attention", status: "needs-attention", errorText: startup, wantStatus: "running", clearError: true},
		{name: "lost launch response", status: "needs-attention", errorText: "Launch/prompt uncertain: native response lost", wantStatus: "running", clearError: true},
		{name: "unrelated attention", status: "needs-attention", errorText: "Verification prerequisites are missing", wantStatus: "needs-attention"},
		{name: "reported remains reported", status: "reported", wantStatus: "reported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			task := ordjson.NewObject()
			task.Set("status", test.status)
			task.Set("error", test.errorText)
			task.Set("attention", []any{"unrelated saved decision"})
			attempt := ordjson.NewObject()
			native := ordjson.NewObject()
			native.Set("effect", "uncertain")
			first := ordjson.NewObject()
			first.Set("sent", false)
			native.Set("first_message", first)
			attempt.Set("native_launch", native)
			receipt := ordjson.NewObject()
			receipt.Set("kind", "worker-init")
			reconcileWorkerInit(task, attempt, receipt)
			if value, _ := task.Get("status"); value != test.wantStatus {
				t.Fatalf("status %v", value)
			}
			errorValue, _ := task.Get("error")
			if test.clearError {
				if errorValue != nil {
					t.Fatalf("stale startup error %v", errorValue)
				}
			} else if errorValue != test.errorText {
				t.Fatalf("unrelated error changed: %v", errorValue)
			}
			if value, _ := task.Get("attention"); !reflect.DeepEqual(value, []any{"unrelated saved decision"}) {
				t.Fatalf("unrelated attention changed: %v", value)
			}
			if value, _ := native.Get("effect"); value != "uncertain" {
				t.Fatalf("native uncertainty changed: %v", value)
			}
			if value, _ := first.Get("sent"); value != false {
				t.Fatal("init fabricated a native submission")
			}
			if value, _ := native.Get("application_reconciliation"); asString(value.(*ordjson.Object), "state") != "receipt-confirmed" {
				t.Fatal("receipt not reconciled")
			}
		})
	}
}
