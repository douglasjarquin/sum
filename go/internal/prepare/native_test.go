package prepare

import (
	"encoding/json"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"path/filepath"
	"strconv"
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
			config := fixture.DefaultRoute(t)
			home, checkout := t.TempDir(), t.TempDir()
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
			session, err := st.ClaimCoordinator("", "ordinary coordinator", false)
			if err != nil {
				t.Fatal(err)
			}
			ctx := session.Owner()
			ctx.Set("claim", session.Token())
			if err := os.WriteFile(filepath.Join(st.Home, "settings.json"), []byte(`{"schema":1,"template":{"codex_home":`+strconv.Quote(home)+`}}`), 0600); err != nil {
				t.Fatal(err)
			}
			holder := session.Owner()
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
			task.Set("owner", session.Owner())
			task.Set("parent", nil)
			if err = st.SaveTask(task); err != nil {
				t.Fatal(err)
			}
			started := true
			fixture.QueueFault("terminal.run", hctest.Fault{LoseResponse: scenario.lost})
			if sent {
				fixture.QueueFault("terminal.send", hctest.Fault{SendResult: &hcclient.SendResult{Turn: 1, Started: &started}})
			} else {
				fixture.QueueFault("terminal.send", hctest.Fault{RefuseKind: "blocked"})
			}
			unlock, err := st.Lock()
			if err != nil {
				t.Fatal(err)
			}
			result, err := startNative(st, ctx, root, "t-0123456789ab", filepath.Join(checkout, "brief.md"), task, attempt, ordjson.NewObject(), nil, unlock)
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
			result, err = startNative(st, ctx, root, "t-0123456789ab", filepath.Join(checkout, "brief.md"), task, attempt, specLaunch, nil, unlock)
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
			if policyField(asObject(policyField(result, "launch")), "hc_template") == nil {
				t.Fatal("the persisted launch lost its qualified worker template")
			}
			terminals := fixture.Terminals()
			if len(terminals) != 1 {
				t.Fatalf("terminals=%d", len(terminals))
			}
			terminal := terminals[0]
			if terminal.RM || terminal.Notify || terminal.DieWithParent {
				t.Fatalf("unsafe lifetime %#v", terminal)
			}
			for _, call := range fixture.Calls() {
				if call.Method == "terminal.run" && call.Params["keep"] != false {
					t.Fatalf("creation requested keep: %v", call.Params)
				}
			}
			if terminal.Keep != (terminal.Labels["herdr.space"] != "") {
				t.Fatalf("keep does not follow adoption: %#v", terminal)
			}
			if len(*terminal.Name) > 32 {
				t.Fatal("name exceeds bound")
			}
			sends := 0
			for _, call := range fixture.Calls() {
				if call.Method == "terminal.run" && (call.Params["parent"] != nil || call.Params["caller"] != nil) {
					t.Fatal("worker creation retained a native coordinator parent/caller")
				}
				if call.Method == "terminal.run" && len(call.Params["argv"].([]any)) != 1 {
					t.Fatal("creation included an implicit opening")
				}
				if call.Method == "terminal.send" {
					sends++
				}
			}
			if sends != 1 {
				t.Fatalf("opening sends=%d", sends)
			}
			if scenario.name == "sent" {
				secondCheckout := t.TempDir()
				secondAttempt, err := reservations.NewAttempt("worker", session.Owner(), secondCheckout, store.Now(), "held", nil)
				if err != nil {
					t.Fatal(err)
				}
				second := ordjson.NewObject()
				for _, key := range task.Keys() {
					value, _ := task.Get(key)
					second.Set(key, value)
				}
				second.Set("id", "t-abcdef012345")
				second.Set("status", "prepared")
				second.Set("worktree", secondCheckout)
				second.Set("execution", reservations.NewExecution(secondAttempt))
				if err := st.SaveTask(second); err != nil {
					t.Fatal(err)
				}
				unlock, err := st.Lock()
				if err != nil {
					t.Fatal(err)
				}
				secondResult, err := startNative(st, ctx, root, "t-abcdef012345", filepath.Join(secondCheckout, "brief.md"), second, secondAttempt, specLaunch, nil, unlock)
				if err != nil {
					t.Fatal(err)
				}
				firstEndpoint := store.EndpointFromContext(asObject(policyField(result, "worker_endpoint")))
				secondEndpoint := store.EndpointFromContext(asObject(policyField(secondResult, "worker_endpoint")))
				if firstEndpoint.Address == secondEndpoint.Address || firstEndpoint.Attempt == secondEndpoint.Attempt || firstEndpoint.Cwd == secondEndpoint.Cwd {
					t.Fatal("workers did not retain distinct execution identities")
				}
				tasks, err := st.AllTasks()
				if err != nil {
					t.Fatal(err)
				}
				occupied, err := launch.Occupancy(tasks)
				if err != nil {
					t.Fatal(err)
				}
				count, _ := occupied.Get("global")
				if fmt.Sprint(count) != "2" {
					t.Fatalf("two worker attempts not counted: %v", occupied)
				}
				for _, call := range fixture.Calls() {
					if call.Method == "terminal.run" && (call.Params["parent"] != nil || call.Params["caller"] != nil) {
						t.Fatal("second worker inherited a native coordinator")
					}
				}
			}
		})
	}
}
