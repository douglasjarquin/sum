package prepare

import (
	"encoding/json"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeLaunchPersistsCreationWithoutAdoptingStarted(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		sent, lost bool
	}{{"sent", true, false}, {"unsent", false, false}, {"lost", true, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			sent := scenario.sent
			fixture := hctest.New(t)
			root := t.TempDir()
			codex := filepath.Join(root, ".local/bin/codex")
			if err := os.MkdirAll(filepath.Dir(codex), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(codex, []byte("#!/bin/sh\nprintf '%s\\n' 'codex-cli 0.166.0'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			config, home, checkout := t.TempDir(), t.TempDir(), t.TempDir()
			options := launch.HCTemplateOptions{Cwd: root, CodexHome: home}
			spec, err := launch.PrepareHCTemplate(root, config, "codex", nil, options)
			if err != nil {
				t.Fatal(err)
			}
			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = st.Init(); err != nil {
				t.Fatal(err)
			}
			machine, err := st.Machine()
			if err != nil {
				t.Fatal(err)
			}
			endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: machine.ID, CoreID: fixture.CoreID, Session: "sum-test", Socket: fixture.Socket, Address: "t1", Role: "coordinator", Attempt: "bootstrap", BindingGeneration: 1, CreatedMS: 1, Cwd: root}
			provenance, _ := ordjson.FromValue(NativeTemplate{spec, config, options})
			owner := endpoint.Object()
			owner.Set("native_template", provenance)
			if err = ordjson.WriteFile(filepath.Join(st.Home, "context.json"), owner); err != nil {
				t.Fatal(err)
			}
			holder := ordjson.NewObject()
			holder.Set("machine", machine.ID)
			holder.Set("session", "sum-test")
			holder.Set("pane", nil)
			attempt, err := reservations.NewAttempt("worker", holder, checkout, store.Now(), "held", nil)
			if err != nil {
				t.Fatal(err)
			}
			task := ordjson.NewObject()
			task.Set("schema", json.Number("2"))
			task.Set("id", "t-0123456789ab")
			task.Set("machine", machine.ID)
			task.Set("status", "prepared")
			task.Set("worktree", checkout)
			task.Set("execution", reservations.NewExecution(attempt))
			task.Set("launch", ordjson.NewObject())
			task.Set("evidence", []any{})
			if err = st.SaveTask(task); err != nil {
				t.Fatal(err)
			}
			started := true
			fixture.QueueFault("terminal.run", hctest.Fault{LoseResponse: scenario.lost, FirstMessage: &hcclient.FirstMessage{Sent: sent, Started: &started}})
			unlock, err := st.Lock()
			if err != nil {
				t.Fatal(err)
			}
			result, err := startNative(st, endpoint.Object(), root, "t-0123456789ab", filepath.Join(checkout, "brief.md"), task, attempt, ordjson.NewObject(), nil, unlock)
			if err == nil {
				t.Fatal("empty harness unexpectedly qualified")
			}
			if len(fixture.Terminals()) != 0 {
				t.Fatal("launched before qualification")
			}
			contents, err := os.ReadFile(filepath.Join(config, "herdr-core/templates", spec.Name+".md"))
			if err != nil {
				t.Fatal(err)
			}
			command := ""
			for _, line := range strings.Split(string(contents), "\n") {
				if strings.HasPrefix(line, "command: ") {
					command = strings.TrimPrefix(line, "command: ")
				}
			}
			fixture.SetTemplates([]hcclient.TemplateInfo{{Name: spec.Name, Command: command}})
			fixture.SetTemplateSubmit(spec.Name, "tab")
			specLaunch := ordjson.NewObject()
			specLaunch.Set("harness", "codex")
			unlock, err = st.Lock()
			if err != nil {
				t.Fatal(err)
			}
			result, err = startNative(st, endpoint.Object(), root, "t-0123456789ab", filepath.Join(checkout, "brief.md"), task, attempt, specLaunch, nil, unlock)
			if scenario.lost {
				if err == nil {
					t.Fatal("lost response was accepted")
				}
				saved, e := st.ReadTask("t-0123456789ab")
				if e != nil {
					t.Fatal(e)
				}
				w, e := reservations.Worker(saved)
				if e != nil {
					t.Fatal(e)
				}
				if policyField(w, "state") != "uncertain" || policyField(saved, "worker_endpoint") != nil {
					t.Fatal("lost response fabricated creation authority")
				}
				if native := asObject(policyField(w, "native_launch")); native == nil || policyField(native, "intent") == nil {
					t.Fatal("launch intent missing after uncertainty")
				}
				if len(fixture.Terminals()) != 1 {
					t.Fatal("uncertain native launch replayed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := asString(policyField(result, "status")); (sent && got != "starting") || (!sent && got != "needs-attention") {
				t.Fatalf("status %s", got)
			}
			worker, err := reservations.Worker(result)
			if err != nil {
				t.Fatal(err)
			}
			if asString(policyField(worker, "state")) != "starting" {
				t.Fatal("native started adopted as running")
			}
			native := asObject(policyField(worker, "native_launch"))
			if native == nil || policyField(native, "first_message") == nil {
				t.Fatal("first-message record missing")
			}
			terminals := fixture.Terminals()
			if len(terminals) != 1 {
				t.Fatalf("terminals=%d", len(terminals))
			}
			terminal := terminals[0]
			if terminal.Keep || terminal.RM || terminal.Notify || terminal.DieWithParent {
				t.Fatalf("unsafe lifetime %#v", terminal)
			}
			if len(*terminal.Name) > 32 {
				t.Fatal("name exceeds bound")
			}
			calls := fixture.Calls()
			for _, call := range calls {
				if call.Method == "terminal.send" {
					t.Fatal("blind separate first-message send")
				}
			}
		})
	}
}
