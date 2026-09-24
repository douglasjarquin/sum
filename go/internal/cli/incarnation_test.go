package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// The fake Herdr scripts the calling pane from FAKE_PARENT_* and the fake ps answers start times from FAKE_PS_*.
// restartHerdr is what a real Herdr 0.9.0 restart does to a restored pane: the same pane ID, a new terminal_id, and a
// new shell process that started after every record written so far.
func restartHerdr(t *testing.T, terminal string) {
	t.Helper()
	t.Setenv("FAKE_PARENT_TERMINAL", terminal)
	t.Setenv("FAKE_PARENT_SHELL_PID", "5151")
	t.Setenv("FAKE_PS_STARTS", `{"5151": "2030-01-01T00:00:00Z"}`)
}

// initView runs init and returns its decoded output.
func initView(t *testing.T, home string, args ...string) map[string]any {
	t.Helper()
	out, err := runCLI(t, home, append([]string{"init"}, args...)...)
	if err != nil {
		t.Fatalf("init %v: %v\n%s", args, err, out)
	}
	return decodeObject(t, out)
}

func outcomeOf(view map[string]any) string {
	inc, _ := view["incarnation"].(map[string]any)
	outcome, _ := inc["outcome"].(string)
	return outcome
}

func ownerIncarnation(t *testing.T, home string) map[string]any {
	t.Helper()
	inc, _ := readJSON(t, filepath.Join(home, "context.json"))["incarnation"].(map[string]any)
	return inc
}

func TestIncarnation_aRestoredPaneUnderANewServerDoesNotInheritTheCoordinator(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	claimed := initView(t, home)
	if claimed["role"] != "coordinator" || ownerIncarnation(t, home)["terminal"] != "term-w-parent:p1" {
		t.Fatalf("first init = %v, owner %v", claimed, ownerIncarnation(t, home))
	}
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", questionTask("t-aaaaaaaaaaaa", mustMachine(t), mustMachine(t), home))
	before, err := os.ReadFile(filepath.Join(home, "context.json"))
	if err != nil {
		t.Fatal(err)
	}

	// Herdr restarts; w-parent:p1 comes back with a new terminal and a new shell, occupied by someone else.
	restartHerdr(t, "term-after-restart")
	// Before any init, the old registration still says coordinator: the bridge lets the new occupant observe, not act.
	if out, err := runCLI(t, home, "herdr", "--", "agent", "list"); err != nil {
		t.Fatalf("bridge observation from the restored pane: %v\n%s", err, out)
	}
	if out, err := runCLI(t, home, "herdr", "--", "agent", "prompt", "w-worker:p1", "hello"); err == nil || !strings.Contains(out+err.Error(), incarnation.Replaced) {
		t.Fatalf("bridge prompt from the restored pane = %v %s, want the replaced refusal", err, out)
	}
	view := initView(t, home)
	if view["role"] != "developer" || outcomeOf(view) != incarnation.Replaced {
		t.Fatalf("init in the restored pane = role %v, incarnation %v; want developer, replaced", view["role"], view["incarnation"])
	}
	if !strings.Contains(asString(asMap(view["incarnation"])["recovery"]), "init --reclaim") {
		t.Fatalf("recovery = %v, want the deliberate reclaim", view["incarnation"])
	}
	after, _ := os.ReadFile(filepath.Join(home, "context.json"))
	if string(after) != string(before) {
		t.Fatalf("a refused init changed the coordinator record:\nbefore %s\nafter  %s", before, after)
	}
	// A worker's new question is saved; its notice is not prompted into the restored coordinator pane.
	t.Setenv("HERDR_PANE_ID", "w-worker:p1")
	if out, err := runCLI(t, home, "ask", "t-aaaaaaaaaaaa", "--key", "after-restart", "--text", "Still blocked?"); err != nil {
		t.Fatalf("ask: %v\n%s", err, out)
	}
	t.Setenv("HERDR_PANE_ID", "w-parent:p1")
	if calls := fakePrompts(t, home); len(calls) != 0 {
		t.Fatalf("the restored pane was prompted with the old coordinator's returns: %v", calls)
	}
	// Coordinator commands from the restored pane are refused, and so is a claim without --reclaim.
	if out, err := runCLI(t, home, "bind", "t-aaaaaaaaaaaa", "--parent-only"); err == nil {
		t.Fatalf("bind from a replaced coordinator pane succeeded: %s", out)
	}
	if _, err := initRole(t, home, "--role", "coordinator"); err == nil {
		t.Fatal("a replaced pane claimed the coordinator role without --reclaim")
	}

	// The deliberate, user-approved rebind: the recorded occupant is verifiably gone.
	reclaimed := initView(t, home, "--role", "coordinator", "--reclaim")
	owner := readJSON(t, filepath.Join(home, "context.json"))
	if reclaimed["role"] != "coordinator" || owner["previous_observed"] != incarnation.Replaced || ownerIncarnation(t, home)["terminal"] != "term-after-restart" {
		t.Fatalf("reclaim = %v, owner %v", reclaimed, owner)
	}
	if again := initView(t, home); again["role"] != "coordinator" || outcomeOf(again) != incarnation.Same {
		t.Fatalf("init after reclaim = %v", again)
	}
}

func TestIncarnation_theSameOccupantStaysTheCoordinator(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	initView(t, home)
	if view := initView(t, home); view["role"] != "coordinator" || outcomeOf(view) != incarnation.Same {
		t.Fatalf("repeated init = %v", view)
	}

	// A live handoff replaces the terminal and keeps the shell process.
	t.Setenv("FAKE_PARENT_TERMINAL", "term-after-handoff")
	if view := initView(t, home); view["role"] != "coordinator" || outcomeOf(view) != incarnation.Handoff {
		t.Fatalf("handoff = %v", view)
	}
	if ownerIncarnation(t, home)["terminal"] != "term-after-handoff" {
		t.Fatalf("owner terminal = %v after handoff", ownerIncarnation(t, home))
	}

	// The integration reports the native conversation; a new conversation in the same terminal keeps the role.
	t.Setenv("FAKE_PARENT_SESSION", "conv-A")
	if view := initView(t, home); view["role"] != "coordinator" || outcomeOf(view) != incarnation.Same {
		t.Fatalf("session reported = %v", view)
	}
	t.Setenv("FAKE_PARENT_SESSION", "conv-B")
	if view := initView(t, home); view["role"] != "coordinator" || outcomeOf(view) != incarnation.NewConversation {
		t.Fatalf("new conversation = %v", view)
	}
	if session := asMap(ownerIncarnation(t, home)["agent_session"]); session["value"] != "conv-B" {
		t.Fatalf("owner session = %v, want conv-B recorded", session)
	}

	// A restart that Herdr restores into the recorded native conversation keeps the role.
	restartHerdr(t, "term-after-restore")
	if view := initView(t, home); view["role"] != "coordinator" || outcomeOf(view) != incarnation.Restored {
		t.Fatalf("restore = %v", view)
	}
}

func TestIncarnation_anOccupantThatCannotBeObservedGainsNothingAndKeepsItsRecord(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	initView(t, home)
	registration := readJSON(t, sessionPath(home, mustMachine(t), "w-parent:p1"))

	// The terminal changed and the shell's start time cannot be read: a handoff cannot be told from a replacement.
	t.Setenv("FAKE_PARENT_TERMINAL", "term-unknown")
	t.Setenv("FAKE_PS_GONE", "4242")
	view := initView(t, home)
	if view["role"] != "developer" || outcomeOf(view) != incarnation.Unobservable || view["registered"] != false {
		t.Fatalf("init = %v", view)
	}
	if kept := readJSON(t, sessionPath(home, mustMachine(t), "w-parent:p1")); kept["role"] != "coordinator" || kept["updated_at"] != registration["updated_at"] {
		t.Fatalf("an unobservable init rewrote the registration: %v", kept)
	}
	if out, err := runCLI(t, home, "bind", "t-aaaaaaaaaaaa", "--parent-only"); err == nil || !strings.Contains(out+err.Error(), incarnation.Unobservable) {
		t.Fatalf("coordinator command from an unobservable pane = %v %s", err, out)
	}
	if _, err := initRole(t, home, "--role", "coordinator", "--reclaim"); err == nil {
		t.Fatal("an unobservable coordinator pane was reclaimed")
	}

	// Once Herdr can report the shell again, the same pane is the coordinator again.
	t.Setenv("FAKE_PS_GONE", "")
	if view := initView(t, home); view["role"] != "coordinator" || outcomeOf(view) != incarnation.Handoff {
		t.Fatalf("init after recovery = %v", view)
	}
}

func TestIncarnation_aLegacyCoordinatorRecordNeedsProof(t *testing.T) {
	home := legacyHome(t, "dev")
	herdrEnv(t, home)
	onHost(t, thisHostRaw, "dev")
	// The legacy owner was claimed on 2026-01-01; this pane's shell started after it: a different occupant.
	t.Setenv("FAKE_PS_DEFAULT_START", "2026-06-01T00:00:00Z")
	view := initView(t, home)
	if view["role"] != "developer" || outcomeOf(view) != incarnation.Replaced {
		t.Fatalf("legacy owner with a newer shell = %v", view)
	}
	if _, has := readJSON(t, filepath.Join(home, "context.json"))["incarnation"]; has {
		t.Fatal("a refused init adopted the legacy record")
	}
	// The shell predates the record: legacy-verified, and adopted by this write only.
	t.Setenv("FAKE_PS_DEFAULT_START", "2025-06-01T00:00:00Z")
	if view := initView(t, home); view["role"] != "coordinator" || outcomeOf(view) != incarnation.LegacyVerified {
		t.Fatalf("legacy owner with an older shell = %v", view)
	}
	if ownerIncarnation(t, home)["terminal"] != "term-w-parent:p1" {
		t.Fatalf("the verified legacy owner was not adopted: %v", ownerIncarnation(t, home))
	}
}

func TestIncarnation_aRestoredWorkerPaneIsNotTheWorkerUntilTheCoordinatorRebinds(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	initView(t, home)
	host := mustMachine(t)
	task := strings.Replace(questionTask("t-aaaaaaaaaaaa", host, host, home), `"brief": "worker task"`, `"brief": "worker task", "worktree": `+jsonString(home), 1)
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", task)
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Register(store.Endpoint{Machine: host, Session: "sum-test", Pane: "w-worker:p1", Cwd: home}, "worker", "t-aaaaaaaaaaaa",
		incarnation.Evidence{Terminal: "term-w-worker:p1", Shell: &incarnation.Shell{PID: 4242, Started: "2025-01-01T00:00:00Z"}}.Record(store.Now())); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PANE_ID", "w-worker:p1")
	if view := initView(t, home); view["role"] != "worker" || outcomeOf(view) != incarnation.Same {
		t.Fatalf("worker init = %v", view)
	}

	restartHerdr(t, "term-worker-restarted")
	view := initView(t, home)
	if view["role"] != "developer" || outcomeOf(view) != incarnation.Replaced || !strings.Contains(asString(asMap(view["incarnation"])["recovery"]), "bind TASK --worker-pane") {
		t.Fatalf("restored worker pane init = %v", view)
	}
	if status := readJSON(t, filepath.Join(home, "tasks", "t-aaaaaaaaaaaa", "task.json"))["status"]; status != "running" {
		t.Fatalf("the task changed: status %v", status)
	}

	// The coordinator inspects the pane and rebinds it deliberately; the pane is the worker again.
	t.Setenv("HERDR_PANE_ID", "w-parent:p1")
	t.Setenv("FAKE_PARENT_TERMINAL", "")
	t.Setenv("FAKE_PARENT_SHELL_PID", "")
	if out, err := runCLI(t, home, "bind", "t-aaaaaaaaaaaa", "--worker-pane", "w-worker:p1"); err != nil {
		t.Fatalf("bind --worker-pane: %v\n%s", err, out)
	}
	t.Setenv("HERDR_PANE_ID", "w-worker:p1")
	t.Setenv("FAKE_PARENT_TERMINAL", "term-worker-restarted")
	t.Setenv("FAKE_PARENT_SHELL_PID", "5151")
	if view := initView(t, home); view["role"] != "worker" || outcomeOf(view) != incarnation.Same {
		t.Fatalf("worker init after the rebind = %v", view)
	}
}

func mustMachine(t *testing.T) string {
	t.Helper()
	id, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func jsonString(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

// fakePrompts lists every agent prompt the fake Herdr received.
func fakePrompts(t *testing.T, home string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "fake-herdr", "calls.jsonl"))
	if err != nil {
		return nil
	}
	var prompts []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var call struct{ Args []string }
		if json.Unmarshal([]byte(line), &call) == nil && len(call.Args) > 2 && call.Args[0] == "agent" && call.Args[1] == "prompt" {
			prompts = append(prompts, call.Args[2])
		}
	}
	return prompts
}

// An unreachable Herdr (a stale socket, a stopped server) is never identity proof: coordinator commands are refused as
// unobservable and init claims nothing.
func TestIncarnation_anUnreachableBackendIsNotIdentityProof(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	initView(t, home)
	stale := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(stale, []byte("#!/bin/sh\n[ \"$1\" = --version ] && { echo 'herdr 0.9.0'; exit 0; }\necho '{\"error\":{\"code\":\"server_not_running\",\"message\":\"connect: no such file or directory\"}}' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_HERDR_BIN", stale)
	if out, err := runCLI(t, home, "bind", "t-aaaaaaaaaaaa", "--parent-only"); err == nil || !strings.Contains(out+err.Error(), incarnation.Unobservable) {
		t.Fatalf("coordinator command with Herdr unreachable = %v %s, want unobservable", err, out)
	}
	before, _ := os.ReadFile(filepath.Join(home, "context.json"))
	if _, err := initRole(t, home); err == nil {
		t.Fatal("init succeeded with Herdr unreachable")
	}
	if after, _ := os.ReadFile(filepath.Join(home, "context.json")); string(after) != string(before) {
		t.Fatal("init changed the coordinator record while Herdr was unreachable")
	}
}
