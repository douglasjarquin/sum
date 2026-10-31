package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/roleinit"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestCoordinatorRecoverNoHolderCLI(t *testing.T) {
	clearFallbackIdentity(t)
	root, st, f, _ := developerFallbackLab(t)
	if err := os.Rename(filepath.Join(st.Home, "context.json"), filepath.Join(t.TempDir(), "owner.json")); err != nil {
		t.Fatal(err)
	}
	host, err := st.Machine()
	if err != nil {
		t.Fatal(err)
	}
	record, err := ordjson.FromValue(map[string]any{
		"endpoint": map[string]any{"schema": 2, "backend": "hc", "machine": host.ID, "core_id": f.CoreID, "socket": f.Socket, "session": "sum-test-recovery", "role": "coordinator", "attempt": "failed-attempt", "binding_generation": 1, "address": "", "created_ms": 0, "cwd": root},
		"effect":   "uncertain",
		"error":    `HC terminal.run (uncertain): RPC -32000: no holder: No such file or directory (os error 2) ({"hint":"run hc server restart on this machine, then retry; see holder.log and core.log in the state directory","kind":"unavailable"})`,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Home, "coordinator-launch.json")
	if err = ordjson.WriteFile(path, record); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	cmd := NewRoot(filepath.Join(root, "bin/sumctl"), &out, &stderr)
	cmd.SetArgs([]string{"--home", st.Root, "--format", "json", "coordinator", "recover", "--attempt", "failed-attempt"})
	if err = cmd.Execute(); err != nil {
		t.Fatalf("recover refused known pre-creation failure: %v %s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "not-created") {
		t.Fatal(out.String())
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("pending launch still blocks start: %v", err)
	}
	archived, err := os.ReadFile(filepath.Join(st.Home, "coordinator-launches", "failed-attempt.json"))
	if err != nil || !strings.Contains(string(archived), "no holder") {
		t.Fatalf("failed attempt lost: %s %v", archived, err)
	}
	for _, call := range f.Calls() {
		if call.Method != "hello" && call.Method != "terminal.list" {
			t.Fatalf("recovery mutated HC: %s", call.Method)
		}
	}
}

func TestCoordinatorStartRecoverAndRestartCLI(t *testing.T) {
	for _, harness := range []string{"codex", "grok", "claude", "devin"} {
		t.Run(harness, func(t *testing.T) {
			clearFallbackIdentity(t)
			root, st, f, _ := developerFallbackLab(t)
			if err := os.Rename(filepath.Join(st.Home, "context.json"), filepath.Join(t.TempDir(), "owner.json")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "COORDINATOR.md"), []byte("Coordinator procedure.\n"), 0600); err != nil {
				t.Fatal(err)
			}
			userHome := t.TempDir()
			for _, key := range []string{"HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "GROK_HOME", "XDG_CONFIG_HOME"} {
				t.Setenv(key, userHome)
			}
			versions := map[string]string{"codex": "codex-cli 0.166.0", "grok": "grok 1.0.67 (2fe064390d07) [stable]", "claude": "2.1.316 (Claude Code)", "devin": "devin 3000.11.234 (92c83011d113)"}
			binary := filepath.Join(root, harness)
			program := "#!/bin/sh\nif [ \"$1\" = --version ]; then printf '%s\\n' '" + versions[harness] + "'; else printf '%s\\n' \"$HOME\" \"$CODEX_HOME\" \"$CLAUDE_CONFIG_DIR\" \"$GROK_HOME\" \"$XDG_CONFIG_HOME\"; fi\n"
			if err := os.WriteFile(binary, []byte(program), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SUM_"+strings.ToUpper(harness)+"_BIN", binary)
			config := t.TempDir()
			spec, err := launch.PrepareHCTemplate(root, config, harness, nil, launch.HCTemplateOptions{Cwd: root})
			if err != nil {
				t.Fatal(err)
			}
			f.SetTemplates([]hcclient.TemplateInfo{{Name: spec.Name, Command: spec.Command()}})
			f.SetTemplateSubmit(spec.Name, spec.Submit)
			run := func(args ...string) (map[string]any, error) {
				var out, stderr bytes.Buffer
				cmd := NewRoot(filepath.Join(root, "bin/sumctl"), &out, &stderr)
				cmd.SetArgs(append([]string{"--home", st.Root, "--format", "json", "coordinator"}, args...))
				err := cmd.Execute()
				if err != nil {
					return nil, err
				}
				return decodeObject(t, out.String()), nil
			}
			start := []string{"start", "--socket", f.Socket, "--session", "sum-test-recovery", "--hc-config-home", config, "--harness", harness}
			if err = os.WriteFile(binary, []byte("#!/bin/sh\necho unsupported-version\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if _, err = run(start...); err == nil {
				t.Fatal("unqualified binary version launched")
			}
			if err = os.WriteFile(binary, []byte(program), 0700); err != nil {
				t.Fatal(err)
			}
			inputConfig := filepath.Join(userHome, "settings.json")
			contents := `{"keymap":{"composer":{"submit":"ctrl+q"}}}`
			switch harness {
			case "codex":
				inputConfig = filepath.Join(userHome, "config.toml")
				contents = "[tui.keymap.composer]\nqueue_message='ctrl+q'\n"
			case "claude":
				inputConfig = filepath.Join(userHome, "keybindings.json")
				contents = `{"bindings":[{"context":"Chat","bindings":{"enter":"chat:newline"}}]}`
			case "devin":
				inputConfig = filepath.Join(userHome, "devin/config.json")
			}
			if err = os.MkdirAll(filepath.Dir(inputConfig), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(inputConfig, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = run(start...); err == nil {
				t.Fatal("custom input bindings launched")
			}
			if err = os.Rename(inputConfig, filepath.Join(t.TempDir(), "preserved-config")); err != nil {
				t.Fatal(err)
			}
			if harness != "codex" {
				if _, err = run(append(append([]string{}, start...), "--arg=--print")...); err == nil {
					t.Fatal("non-interactive mode launched")
				}
			}
			for _, arg := range []string{"--settings={\"permissions\":{\"defaultMode\":\"bypassPermissions\"}}", "--config=alternate.json"} {
				if harness == "codex" {
					continue
				}
				if _, err = run(append(append([]string{}, start...), "--arg="+arg)...); err == nil {
					t.Fatal("alternate CLI configuration launched without permission disclosure")
				}
			}
			if harness == "grok" {
				for _, name := range []string{"managed_config.toml", "requirements.toml", "pager.toml"} {
					path := filepath.Join(userHome, name)
					if err = os.WriteFile(path, []byte("[ui]\nsimple_mode=false\n"), 0600); err != nil {
						t.Fatal(err)
					}
					if _, err = run(start...); err == nil {
						t.Fatalf("modal input in %s launched", name)
					}
					if err = os.Rename(path, filepath.Join(t.TempDir(), "preserved-modal-config")); err != nil {
						t.Fatal(err)
					}
				}
			}
			probe := exec.Command("/bin/sh", "-c", spec.Command())
			probe.Env = []string{"PATH=/usr/bin:/bin", "HOME=/wrong", "CODEX_HOME=/wrong", "CLAUDE_CONFIG_DIR=/wrong", "GROK_HOME=/wrong", "XDG_CONFIG_HOME=/wrong"}
			actual, err := probe.Output()
			if err != nil || string(actual) != strings.Repeat(userHome+"\n", 5) {
				t.Fatalf("template inherited core configuration: %q %v", actual, err)
			}
			f.QueueFault("terminal.run", hctestFaultNoHolder())
			failed, err := run(start...)
			if err != nil || failed["effect"] != "refused" {
				t.Fatalf("pre-creation failure: %v %v", failed, err)
			}
			attempt := asString(asMap(failed["endpoint"])["attempt"])
			if _, err = run(start...); err == nil {
				t.Fatal("start automatically replayed failed attempt")
			}
			if _, err = run("recover", "--attempt", "wrong-attempt"); err == nil {
				t.Fatal("stale recovery accepted")
			}
			for range 2 {
				recovered, err := run("recover", "--attempt", attempt)
				if err != nil || recovered["effect"] != "not-created" {
					t.Fatalf("recovery: %v %v", recovered, err)
				}
			}
			created, err := run(start...)
			if err != nil || created["effect"] != "created" {
				t.Fatalf("new start: %v %v", created, err)
			}
			if len(f.Terminals()) != 2 {
				t.Fatalf("unexpected terminals: %v", f.Terminals())
			}
			if owner, err := st.Owner(); err != nil || owner != nil {
				t.Fatalf("launch claimed coordinator without init receipt: %v %v", owner, err)
			}
			endpointValue := asMap(created["endpoint"])
			address := asString(endpointValue["address"])
			client := f.Client()
			terminal, err := client.Get(context.Background(), address)
			if err != nil {
				t.Fatal(err)
			}
			pid := uint32(201)
			terminal.PID = &pid
			terminal.Foreground = &hcclient.Foreground{PID: 202, Name: harness, Argv: spec.Argv, Cwd: root}
			f.SetTerminal(terminal)
			for key, value := range endpointEnv(endpointValue) {
				t.Setenv(key, value)
			}
			t.Setenv("SUM_SESSION", "sum-test-recovery")
			configureNativeEventsForTest(t, st.Home, false)
			var initialized, initErr bytes.Buffer
			init := NewRoot(filepath.Join(root, "bin/sumctl"), &initialized, &initErr)
			init.SetArgs([]string{"--home", st.Root, "--format", "json", "init"})
			if err = init.Execute(); err != nil {
				t.Fatalf("matching %s init: %v %s", harness, err, initErr.String())
			}
			if view := decodeObject(t, initialized.String()); view["role"] != "coordinator" {
				t.Fatal(view)
			}
			owner, err := st.Owner()
			if err != nil || owner == nil {
				t.Fatalf("missing receipt-bound coordinator: %v", err)
			}
			endpoint := store.EndpointFromContext(owner)
			f.SetStatus(address, "done")
			recipient, err := returns.ObserveNative(context.Background(), endpoint, root)
			if err != nil {
				t.Fatal(err)
			}
			if err = recipient.Qualify(owner); err != nil {
				t.Fatalf("%s return qualification: %v", harness, err)
			}
			if _, err = recipient.Send(context.Background(), "follow-up after completion"); err != nil {
				t.Fatal(err)
			}
			if got := f.Inputs(address); len(got) != 1 || got[0] != "follow-up after completion" {
				t.Fatal(got)
			}
			f.SetStatus(address, "blocked")
			if _, err = returns.ObserveNative(context.Background(), endpoint, root); err == nil {
				t.Fatal("blocked dialog accepted for automatic input")
			}
			if harness == "claude" {
				inputConfig = filepath.Join(userHome, "settings.json")
			}
			drift := `{"model":"changed"}`
			if harness == "codex" {
				drift = "model='changed'\n"
			}
			if err = os.WriteFile(inputConfig, []byte(drift), 0600); err != nil {
				t.Fatal(err)
			}
			if err = recipient.Qualify(owner); err == nil {
				t.Fatal("configuration drift retained input qualification")
			}
			if err = os.Rename(inputConfig, filepath.Join(t.TempDir(), "preserved-drift-config")); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(binary, []byte("changed binary"), 0700); err != nil {
				t.Fatal(err)
			}
			if err = recipient.Qualify(owner); err == nil {
				t.Fatal("binary drift retained input qualification")
			}
		})
	}
}

func hctestFaultNoHolder() hctest.Fault {
	return hctest.Fault{RPCError: &hcclient.RPCError{Code: -32000, Message: "no holder: No such file or directory (os error 2)", Data: json.RawMessage(`{"kind":"unavailable","hint":"run hc server restart on this machine, then retry"}`)}}
}

func TestCoordinatorRecoverRefusesAmbiguousOrChangedIdentityCLI(t *testing.T) {
	for _, scenario := range []string{"lost-response", "in-flight", "created", "other-error", "other-core", "other-machine", "registered", "live-match", "finished-match", "receipt", "changed-during-observation"} {
		t.Run(scenario, func(t *testing.T) {
			clearFallbackIdentity(t)
			root, st, f, _ := developerFallbackLab(t)
			if scenario != "registered" {
				if err := os.Rename(filepath.Join(st.Home, "context.json"), filepath.Join(t.TempDir(), "owner.json")); err != nil {
					t.Fatal(err)
				}
			}
			host, err := st.Machine()
			if err != nil {
				t.Fatal(err)
			}
			endpoint := map[string]any{"schema": 2, "backend": "hc", "machine": host.ID, "core_id": f.CoreID, "socket": f.Socket, "session": "sum-test-recovery", "role": "coordinator", "attempt": "failed-attempt", "binding_generation": 1, "address": "", "created_ms": 0, "cwd": root}
			record := map[string]any{"endpoint": endpoint, "effect": "uncertain", "error": `HC terminal.run (uncertain): RPC -32000: no holder: No such file or directory (os error 2) ({"hint":"run hc server restart on this machine, then retry","kind":"unavailable"})`}
			switch scenario {
			case "lost-response":
				record["error"] = "HC terminal.run (uncertain): EOF"
			case "in-flight":
				record["effect"] = "claimed"
			case "created":
				record["effect"] = "created"
			case "other-error":
				record["error"] = `HC terminal.run (uncertain): RPC -32000: holder disconnected after allocation ({"kind":"unavailable"})`
			case "other-core":
				endpoint["core_id"] = "unrelated-core"
			case "other-machine":
				endpoint["machine"] = "unrelated-machine"
			case "receipt":
				record["native_receipt"] = map[string]any{"kind": "coordinator-init", "attempt": "failed-attempt"}
			case "live-match", "finished-match":
				state := "live"
				if scenario == "finished-match" {
					state = "finished"
				}
				f.SetTerminal(hcclient.Terminal{Address: "t2", State: state, Labels: map[string]string{"sum.attempt": "failed-attempt"}})
			}
			path := filepath.Join(st.Home, "coordinator-launch.json")
			obj, err := ordjson.FromValue(record)
			if err != nil {
				t.Fatal(err)
			}
			if err = ordjson.WriteFile(path, obj); err != nil {
				t.Fatal(err)
			}
			before := snapshotHome(t, st.Root)
			if scenario == "changed-during-observation" {
				entered, release := f.HoldReplyNext("terminal.list", "")
				defer release()
				result := make(chan error, 1)
				go func() { _, err := roleinit.RecoverCoordinator(st, "failed-attempt"); result <- err }()
				<-entered
				obj.Set("changed", true)
				if err = ordjson.WriteFile(path, obj); err != nil {
					t.Fatal(err)
				}
				release()
				if err = <-result; err == nil {
					t.Fatal("recovery ignored concurrent record change")
				}
				return
			}
			var out, stderr bytes.Buffer
			cmd := NewRoot(filepath.Join(root, "bin/sumctl"), &out, &stderr)
			cmd.SetArgs([]string{"--home", st.Root, "--format", "json", "coordinator", "recover", "--attempt", "failed-attempt"})
			if err = cmd.Execute(); err == nil {
				t.Fatalf("unsafe recovery accepted: %s", out.String())
			}
			if after := snapshotHome(t, st.Root); after != before {
				t.Fatalf("refusal changed saved state: %s", fmt.Sprint(err))
			}
			for _, call := range f.Calls() {
				if call.Method != "hello" && call.Method != "terminal.list" {
					t.Fatalf("recovery mutated HC: %s", call.Method)
				}
			}
		})
	}
}
