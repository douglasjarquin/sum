package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/app"
	"os"
	"path/filepath"
	"testing"

	"context"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func developerFallbackLab(t *testing.T) (string, *store.Store, *hctest.Fixture, map[string]string) {
	t.Helper()
	runtimeRoot := t.TempDir()
	procedure := filepath.Join(runtimeRoot, "skills", "sum-develop", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(procedure), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(procedure, []byte("# Developer procedure\nUse an isolated development checkout.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(runtimeRoot, ".sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Init(); err != nil {
		t.Fatal(err)
	}
	f := hctest.New(t)
	pid := uint32(101)
	foreground := &hcclient.Foreground{PID: 102, Name: "codex", Cwd: runtimeRoot}
	terminal := hcclient.Terminal{Address: "t1", CreatedMS: 1, Cwd: runtimeRoot, State: "live", PID: &pid, Foreground: foreground}
	f.SetTerminal(terminal)
	host, err := st.Machine()
	if err != nil {
		t.Fatal(err)
	}
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Session: "fallback-lab", Socket: f.Socket, Address: "t1", CreatedMS: 1, Role: "coordinator", Attempt: "existing-attempt", BindingGeneration: 1, Cwd: runtimeRoot}
	owner := endpoint.Object()
	owner.Set("incarnation", incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: foreground}.Record(store.Now()))
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: "coordinator-init", Attempt: endpoint.Attempt})
	owner.Set("native_receipt", receipt)
	if err = ordjson.WriteFile(filepath.Join(st.Home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"SUM_SESSION": endpoint.Session, "SUM_CORE_ID": endpoint.CoreID, "HC_SOCKET": endpoint.Socket, "HC_TERMINAL_ID": endpoint.Address, "SUM_ROLE": endpoint.Role, "SUM_ATTEMPT": endpoint.Attempt, "SUM_BINDING_GENERATION": "1"}
	return runtimeRoot, st, f, env
}
func clearFallbackIdentity(t *testing.T) {
	t.Helper()
	for _, key := range []string{"SUM_SESSION", "HC_SESSION", "HC_SOCKET", "HC_TERMINAL_ID", "SUM_CORE_ID", "SUM_ROLE", "SUM_TASK", "SUM_ATTEMPT", "SUM_BINDING_GENERATION", "SUM_HOME", "SUM_INSTALL_ROOT"} {
		t.Setenv(key, "")
	}
}
func TestInitUnboundDesignatedSessionIsReadOnlyDeveloperWithoutHC(t *testing.T) {
	cases := []struct {
		name     string
		explicit bool
		change   func(map[string]string)
	}{
		{name: "bare without context", change: func(env map[string]string) {
			for key := range env {
				env[key] = ""
			}
		}},
		{name: "explicit developer with otherwise qualified coordinator context", explicit: true},
		{name: "unbound existing socket", change: func(env map[string]string) {
			env["SUM_ROLE"] = ""
			env["SUM_ATTEMPT"] = ""
			env["SUM_BINDING_GENERATION"] = ""
		}},
		{name: "inherited role alone", change: func(env map[string]string) {
			for key := range env {
				env[key] = ""
			}
			env["SUM_ROLE"] = "coordinator"
		}},
		{name: "inherited worker role alone", change: func(env map[string]string) {
			for key := range env {
				env[key] = ""
			}
			env["SUM_ROLE"] = "worker"
		}},
		{name: "stale socket", change: func(env map[string]string) { env["HC_SOCKET"] = filepath.Join(t.TempDir(), "absent.sock") }},
		{name: "stale core on existing socket", change: func(env map[string]string) { env["SUM_CORE_ID"] = "unrelated-core" }},
		{name: "explicit developer with stale core", explicit: true, change: func(env map[string]string) { env["SUM_CORE_ID"] = "unrelated-core" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			clearFallbackIdentity(t)
			runtimeRoot, st, f, env := developerFallbackLab(t)
			if test.change != nil {
				test.change(env)
			}
			for key, value := range env {
				t.Setenv(key, value)
			}
			before := snapshotHome(t, st.Root)
			var out, errOut bytes.Buffer
			command := NewRoot(filepath.Join(runtimeRoot, "bin", "sumctl"), &out, &errOut)
			args := []string{"--home", st.Root, "--format", "json", "init"}
			if test.explicit {
				args = append(args, "--role", "developer")
			}
			command.SetArgs(args)
			if err := command.Execute(); err != nil {
				t.Fatalf("developer bootstrap failed: %v %s", err, errOut.String())
			}
			view := decodeObject(t, out.String())
			if view["role"] != "developer" || view["registered"] != false || view["task"] != nil || view["endpoint"] != nil {
				t.Fatalf("read-only developer output retained native identity: %v", view)
			}
			row := procedureRow(t, view)
			procedure := filepath.Join(runtimeRoot, "skills", "sum-develop", "SKILL.md")
			data, err := os.ReadFile(procedure)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(data)
			if row["ok"] != true || row["path"] != procedure || row["sha256"] != hex.EncodeToString(digest[:]) {
				t.Fatalf("developer procedure not validated: %v", row)
			}
			if calls := f.Calls(); len(calls) != 0 {
				t.Fatalf("read-only developer bootstrap contacted HC: %v", calls)
			}
			if after := snapshotHome(t, st.Root); after != before {
				t.Fatalf("read-only developer bootstrap changed state:\nbefore %s\nafter %s", before, after)
			}
		})
	}
}
func TestInitExplicitAuthorityNeverFallsBackToDeveloper(t *testing.T) {
	for _, args := range [][]string{{"--role", "coordinator"}, {"--role", "worker", "--task", "t-aaaaaaaaaaaa"}, {"--role", "reviewer", "--task", "t-aaaaaaaaaaaa"}, {"--task", "t-aaaaaaaaaaaa"}, {"--role", "developer", "--task", "t-aaaaaaaaaaaa"}, {"--reclaim"}} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			clearFallbackIdentity(t)
			runtimeRoot, st, f, _ := developerFallbackLab(t)
			before := snapshotHome(t, st.Root)
			var out, errOut bytes.Buffer
			command := NewRoot(filepath.Join(runtimeRoot, "bin", "sumctl"), &out, &errOut)
			command.SetArgs(append([]string{"--home", st.Root, "--format", "json", "init"}, args...))
			if err := command.Execute(); err == nil {
				t.Fatalf("authority request fell back without identity: %s", out.String())
			}
			if snapshotHome(t, st.Root) != before {
				t.Fatal("refused authority initialization changed state")
			}
			if len(f.Calls()) != 0 {
				t.Fatal("missing native identity contacted HC")
			}
		})
	}
}

func TestInitRecognizesPendingExplicitCoordinatorLaunch(t *testing.T) {
	clearFallbackIdentity(t)
	runtimeRoot, st, f, _ := developerFallbackLab(t)
	if err := os.Rename(filepath.Join(st.Home, "context.json"), filepath.Join(t.TempDir(), "old-owner.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, "COORDINATOR.md"), []byte("Coordinator fixture procedure.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(runtimeRoot, "codex")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' 'codex-cli 0.166.0'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_CODEX_BIN", binary)
	t.Setenv("CODEX_HOME", t.TempDir())
	config := t.TempDir()
	spec, err := launch.PrepareHCTemplate(runtimeRoot, config, "codex", nil, launch.HCTemplateOptions{Cwd: runtimeRoot})
	if err != nil {
		t.Fatal(err)
	}
	f.SetTemplates([]hcclient.TemplateInfo{{Name: spec.Name, Command: launch.TemplateCommand(spec.Argv)}})
	f.SetTemplateSubmit(spec.Name, "tab")
	configureNativeEventsForTest(t, st.Home, false)
	var startOut, startErr bytes.Buffer
	start := NewRoot(filepath.Join(runtimeRoot, "bin", "sumctl"), &startOut, &startErr)
	start.SetArgs([]string{"--home", st.Root, "--format", "json", "coordinator", "start", "--socket", f.Socket, "--session", "fallback-lab", "--hc-config-home", config})
	if err = start.Execute(); err != nil {
		t.Fatalf("explicit coordinator start: %v %s", err, startErr.String())
	}
	launchRecord := decodeObject(t, startOut.String())
	endpointValue := asMap(launchRecord["endpoint"])
	address := asString(endpointValue["address"])
	client := f.Client()
	terminal, err := client.Get(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	pid := uint32(201)
	terminal.PID = &pid
	terminal.Foreground = &hcclient.Foreground{PID: 202, Name: "codex", Argv: spec.Argv, Cwd: runtimeRoot}
	f.SetTerminal(terminal)
	for key, value := range endpointEnv(endpointValue) {
		t.Setenv(key, value)
	}
	t.Setenv("SUM_SESSION", "fallback-lab")
	pendingPath := filepath.Join(st.Home, "coordinator-launch.json")
	pendingValue, err := ordjson.ReadFile(pendingPath)
	if err != nil {
		t.Fatal(err)
	}
	pending := pendingValue.(*ordjson.Object)
	expectedValue, _ := pending.Get("endpoint")
	expected := expectedValue.(*ordjson.Object)
	expected.Set("address", "")
	expected.Set("created_ms", json.Number("0"))
	pending.Set("effect", "claimed")
	if err = ordjson.WriteFile(pendingPath, pending); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	command := NewRoot(filepath.Join(runtimeRoot, "bin", "sumctl"), &out, &errOut)
	command.SetArgs([]string{"--home", st.Root, "--format", "json", "init"})
	if err = command.Execute(); err != nil {
		t.Fatalf("pending launch first init: %v %s", err, errOut.String())
	}
	view := decodeObject(t, out.String())
	if view["role"] != "coordinator" || view["registered"] != true {
		t.Fatalf("approved native launch defaulted to developer: %v", view)
	}
	owner, err := st.Owner()
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := app.NativeReceipt(owner)
	if err != nil || receipt.Attempt != asString(endpointValue["attempt"]) {
		t.Fatalf("current coordinator init receipt missing: %v %v", receipt, err)
	}
	runs := 0
	for _, call := range f.Calls() {
		if call.Method == "terminal.run" {
			runs++
		}
	}
	if runs != 1 {
		t.Fatalf("coordinator bootstrap duplicated native launch: %d", runs)
	}
}

func TestInitRequiredTaskIdentityCannotUseDeveloperFallback(t *testing.T) {
	for _, role := range []string{"worker", "reviewer"} {
		for _, explicitDeveloper := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/developer=%t", role, explicitDeveloper), func(t *testing.T) {
				clearFallbackIdentity(t)
				runtimeRoot, st, f, _ := developerFallbackLab(t)
				t.Setenv("SUM_ROLE", role)
				t.Setenv("SUM_TASK", "t-aaaaaaaaaaaa")
				t.Setenv("SUM_ATTEMPT", "required-attempt")
				before := snapshotHome(t, st.Root)
				var out, errOut bytes.Buffer
				command := NewRoot(filepath.Join(runtimeRoot, "bin", "sumctl"), &out, &errOut)
				args := []string{"--home", st.Root, "--format", "json", "init"}
				if explicitDeveloper {
					args = append(args, "--role", "developer")
				}
				command.SetArgs(args)
				if err := command.Execute(); err == nil {
					t.Fatalf("required task identity was downgraded: %s", out.String())
				}
				if snapshotHome(t, st.Root) != before || len(f.Calls()) != 0 {
					t.Fatal("refused task identity changed state or contacted HC")
				}
			})
		}
	}
}
func TestInitUnavailableRecordedCoreNeverGrantsAuthority(t *testing.T) {
	for _, explicitCoordinator := range []bool{false, true} {
		t.Run(fmt.Sprint(explicitCoordinator), func(t *testing.T) {
			clearFallbackIdentity(t)
			runtimeRoot, st, f, env := developerFallbackLab(t)
			for key, value := range env {
				t.Setenv(key, value)
			}
			f.QueueFault("hello", hctest.Fault{RefuseKind: "config"})
			before := snapshotHome(t, st.Root)
			var out, errOut bytes.Buffer
			command := NewRoot(filepath.Join(runtimeRoot, "bin", "sumctl"), &out, &errOut)
			args := []string{"--home", st.Root, "--format", "json", "init"}
			if explicitCoordinator {
				args = append(args, "--role", "coordinator")
			}
			command.SetArgs(args)
			err := command.Execute()
			if explicitCoordinator {
				if err == nil {
					t.Fatal("explicit coordinator request fell back after failed observation")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				view := decodeObject(t, out.String())
				if view["role"] != "developer" || view["registered"] != false || view["endpoint"] != nil {
					t.Fatalf("unavailable core granted authority: %v", view)
				}
			}
			if snapshotHome(t, st.Root) != before {
				t.Fatal("failed observation changed state")
			}
			for _, call := range f.Calls() {
				if call.Method != "hello" {
					t.Fatalf("failed observation invoked %s", call.Method)
				}
			}
		})
	}
}
