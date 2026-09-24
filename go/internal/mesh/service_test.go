package mesh

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type fakeRunner struct {
	mu        sync.Mutex
	calls     [][]string
	durations []time.Duration
	results   []commandResult
	errors    []error
	// terminal is what `pane get` reports for this server's own pane (the occupant check before a mutating call);
	// those calls are answered here and not scripted or recorded.
	terminal string
}

func (r *fakeRunner) Run(_ context.Context, args []string, duration time.Duration, _ bool) (commandResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(args) > 2 && args[0] == "pane" && args[1] == "get" {
		terminal := r.terminal
		if terminal == "" {
			terminal = "term-" + args[2]
		}
		return commandResult{stdout: `{"result":{"pane":{"pane_id":"` + args[2] + `","terminal_id":"` + terminal + `"}}}`}, nil
	}
	r.calls = append(r.calls, append([]string(nil), args...))
	r.durations = append(r.durations, duration)
	if len(r.errors) > 0 {
		err := r.errors[0]
		r.errors = r.errors[1:]
		if err != nil {
			return commandResult{}, err
		}
	}
	if len(r.results) == 0 {
		return commandResult{}, errors.New("fake runner exhausted")
	}
	result := r.results[0]
	r.results = r.results[1:]
	return result, nil
}

func newTestService(t *testing.T, role string, runner *fakeRunner) Service {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	instance := "instance-1"
	writeJSON(t, filepath.Join(home, "state.json"), map[string]string{"instance": instance})
	machine, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	session, pane := "mesh-test", "w-test:p1"
	bound := map[string]any{"terminal": "term-" + pane, "agent_session": nil, "shell": nil, "observed_at": "2026-09-24T00:00:00+00:00"}
	writeJSON(t, filepath.Join(home, "sessions", store.RegistrationKey(store.Endpoint{Machine: machine, Session: session, Pane: pane})+".json"), map[string]any{
		"instance": instance, "machine": machine, "session": session, "pane": pane, "role": role, "incarnation": bound,
	})
	if role == "coordinator" {
		writeJSON(t, filepath.Join(home, "context.json"), map[string]any{"machine": machine, "session": session, "pane": pane, "role": role, "incarnation": bound})
	}
	service := NewService(Config{StateHome: home, Session: session, Pane: pane})
	service.runner = runner
	return service
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestService_relay_usesAgentPromptAfterIdlePreflight(t *testing.T) {
	runner := &fakeRunner{results: []commandResult{{stdout: `{"result":{"agent":{"agent_status":"idle"}}}`}, {stdout: `{"result":{"ok":true}}`}}}
	service := newTestService(t, "coordinator", runner)

	result, err := service.Call(context.Background(), "herdr_relay", json.RawMessage(`{"target":"w-test:p2","message":"--session malicious"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "submitted-not-acknowledged") {
		t.Fatalf("result=%q", result)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls=%d, want preflight and one prompt", len(runner.calls))
	}
	want := []string{"agent", "prompt", "w-test:p2", "sum message:\n--session malicious"}
	if !slices.Equal(runner.calls[1], want) {
		t.Fatalf("prompt argv = %v, want %v", runner.calls[1], want)
	}
}

func TestService_relay_refusesBusyStatusesBeforePrompt(t *testing.T) {
	for _, status := range []string{"working", "blocked", "unknown"} {
		t.Run(status, func(t *testing.T) {
			runner := &fakeRunner{results: []commandResult{{stdout: `{"result":{"agent":{"agent_status":"` + status + `"}}}`}}}
			service := newTestService(t, "coordinator", runner)

			_, err := service.Call(context.Background(), "herdr_relay", json.RawMessage(`{"target":"w-test:p2","message":"hello"}`))
			if err == nil || !strings.Contains(err.Error(), "do not inject") {
				t.Fatalf("error=%v", err)
			}
			if len(runner.calls) != 1 {
				t.Fatalf("calls=%d, want preflight only", len(runner.calls))
			}
		})
	}
}

func TestService_handoff_usesOnePromptWait(t *testing.T) {
	runner := &fakeRunner{results: []commandResult{
		{stdout: `{"result":{"agent":{"agent_status":"idle"}}}`},
		{stdout: "prompted\n"},
		{stdout: "screen\n"},
	}}
	service := newTestService(t, "coordinator", runner)

	result, err := service.Call(context.Background(), "herdr_handoff", json.RawMessage(`{"target":"w-test:p2","message":"review","timeout_ms":5000}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "NOT proof of task completion") {
		t.Fatalf("result=%q", result)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("calls=%d, want preflight, one prompt/wait, and one read", len(runner.calls))
	}
	if !slices.Contains(runner.calls[1], "--wait") || !slices.Contains(runner.calls[1], "--until") {
		t.Fatalf("prompt argv = %v", runner.calls[1])
	}
}

func TestService_handoff_failedWaitIsErrorWithoutStaleRead(t *testing.T) {
	runner := &fakeRunner{results: []commandResult{{stdout: `{"result":{"agent":{"agent_status":"idle"}}}`}, {}}, errors: []error{nil, errors.New("timeout")}}
	service := newTestService(t, "coordinator", runner)

	result, err := service.Call(context.Background(), "herdr_handoff", json.RawMessage(`{"target":"w-test:p2","message":"review"}`))
	if err == nil || result != "" {
		t.Fatalf("result=%q error=%v", result, err)
	}
	if !strings.Contains(err.Error(), "may already have been submitted") {
		t.Fatalf("error=%v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls=%d, want preflight and one prompt", len(runner.calls))
	}
}

func TestService_developerCannotMutateThroughMesh(t *testing.T) {
	runner := &fakeRunner{results: []commandResult{{stdout: `{"result":{"agent":{"agent_status":"idle"}}}`}, {stdout: `{"result":{"agents":[]}}`}}}
	service := newTestService(t, "developer", runner)

	if _, err := service.Call(context.Background(), "herdr_relay", json.RawMessage(`{"target":"w-test:p2","message":"hello"}`)); err == nil {
		t.Fatal("developer mutation unexpectedly succeeded")
	}
	if _, err := service.Call(context.Background(), "herdr_agent_list", nil); err != nil {
		t.Fatal(err)
	}
}

func TestService_rejectsMalformedTargetBeforeHerdr(t *testing.T) {
	runner := &fakeRunner{}
	service := newTestService(t, "coordinator", runner)

	if _, err := service.Call(context.Background(), "herdr_agent_get", json.RawMessage(`{"target":"-bad"}`)); err == nil {
		t.Fatal("leading-dash target unexpectedly succeeded")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls=%d, want none", len(runner.calls))
	}
}

func TestService_rejectsUnknownArgumentsBeforeHerdr(t *testing.T) {
	runner := &fakeRunner{}
	service := newTestService(t, "coordinator", runner)

	if _, err := service.Call(context.Background(), "herdr_agent_list", json.RawMessage(`{"unexpected":true}`)); err == nil {
		t.Fatal("unknown argument unexpectedly succeeded")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls=%d, want none", len(runner.calls))
	}
}

func TestService_rejectsTrailingArgumentsBeforeHerdr(t *testing.T) {
	runner := &fakeRunner{}
	service := newTestService(t, "coordinator", runner)

	if _, err := service.Call(context.Background(), "herdr_agent_list", json.RawMessage(`{} {}`)); err == nil {
		t.Fatal("trailing JSON unexpectedly succeeded")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls=%d, want none", len(runner.calls))
	}
}

func TestService_wait_usesUntilAndClampsTimeout(t *testing.T) {
	runner := &fakeRunner{results: []commandResult{{stdout: `{"result":{"agent":{"agent_status":"idle"}}}`}}}
	service := newTestService(t, "coordinator", runner)

	if _, err := service.Call(context.Background(), "herdr_agent_wait", json.RawMessage(`{"target":"reviewer","status":"done","timeout_ms":999999}`)); err != nil {
		t.Fatal(err)
	}
	want := []string{"agent", "wait", "reviewer", "--until", "done", "--timeout", "60000"}
	if !slices.Equal(runner.calls[0], want) || runner.durations[0] != 65*time.Second {
		t.Fatalf("timeout=%s args=%v", runner.durations[0], runner.calls[0])
	}
}

func TestService_start_usesExistingPaneAndKind(t *testing.T) {
	runner := &fakeRunner{results: []commandResult{{stdout: `{"result":{"ok":true}}`}}}
	service := newTestService(t, "coordinator", runner)

	if _, err := service.Call(context.Background(), "herdr_agent_start", json.RawMessage(`{"name":"reviewer","kind":"codex","pane_id":"w1:p2","args":["-m","chosen-model"]}`)); err != nil {
		t.Fatal(err)
	}
	want := []string{"agent", "start", "reviewer", "--kind", "codex", "--pane", "w1:p2", "--timeout", "30000", "--", "-m", "chosen-model"}
	if !slices.Equal(runner.calls[0], want) {
		t.Fatalf("start argv = %v", runner.calls[0])
	}
}

func TestService_read_isBoundedAndPassive(t *testing.T) {
	runner := &fakeRunner{results: []commandResult{{stdout: "visible output\n"}}}
	service := newTestService(t, "coordinator", runner)

	if _, err := service.Call(context.Background(), "herdr_agent_read", json.RawMessage(`{"target":"reviewer"}`)); err != nil {
		t.Fatal(err)
	}
	want := []string{"agent", "read", "reviewer", "--source", "visible", "--lines", "80", "--format", "text"}
	if !slices.Equal(runner.calls[0], want) {
		t.Fatalf("read argv = %v", runner.calls[0])
	}
}

func TestAuthorize_findsARegistrationKeyedByThisHostsLegacyHostname(t *testing.T) {
	t.Cleanup(machine.Pin("0123456789abcdef0123456789abcdef", "dev"))
	session, pane := "mesh-test", "w-test:p1"
	for _, tc := range []struct {
		recorded string
		allowed  bool
	}{{"dev", true}, {"elsewhere", false}} {
		home := t.TempDir()
		if err := os.Mkdir(filepath.Join(home, "sessions"), 0o700); err != nil {
			t.Fatal(err)
		}
		writeJSON(t, filepath.Join(home, "state.json"), map[string]string{"instance": "instance-1"})
		key := store.RegistrationKey(store.Endpoint{Machine: tc.recorded, Session: session, Pane: pane})
		writeJSON(t, filepath.Join(home, "sessions", key+".json"), map[string]string{
			"instance": "instance-1", "machine": tc.recorded, "session": session, "pane": pane, "role": "coordinator",
		})
		err := NewService(Config{StateHome: home, Session: session, Pane: pane}).authorize(context.Background(), []string{"pane", "list"})
		if (err == nil) != tc.allowed {
			t.Fatalf("registration recorded on %q: authorize = %v, want allowed=%v", tc.recorded, err, tc.allowed)
		}
	}
}

// A pane whose occupant is no longer the one its role was granted to (Herdr restarted and restored the pane ID under a
// new terminal) may still observe but not prompt; nothing reaches the target.
func TestService_relayFromAReplacedCallerIsRefusedBeforeAnyPrompt(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_PS_BIN", filepath.Join(root, "tests", "fixtures", "ps.py"))
	runner := &fakeRunner{terminal: "term-after-restart", results: []commandResult{
		{stdout: `{"result":{"agent":{"agent_status":"idle"}}}`},
		{stdout: `{"result":{"process_info":{"pane_id":"w-test:p1","shell_pid":5151}}}`},
		{stdout: `{"result":{"ok":true}}`},
	}}
	t.Setenv("FAKE_PS_STARTS", `{"5151": "2030-01-01T00:00:00Z"}`)
	service := newTestService(t, "worker", runner)
	_, err = service.Call(context.Background(), "herdr_relay", json.RawMessage(`{"target":"w-test:p2","message":"hello"}`))
	if err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("relay from a replaced pane = %v, want the replaced refusal", err)
	}
	for _, call := range runner.calls {
		if len(call) > 1 && call[0] == "agent" && call[1] == "prompt" {
			t.Fatalf("a prompt was sent: %v", runner.calls)
		}
	}
	if _, err := service.Call(context.Background(), "herdr_agent_list", json.RawMessage(`{}`)); err != nil && strings.Contains(err.Error(), "occupant") {
		t.Fatalf("observation was refused: %v", err)
	}
}

// An unreadable coordinator record is reported as the read failure it is, not as a different occupant.
func TestService_unreadableCoordinatorRecordIsAnError(t *testing.T) {
	runner := &fakeRunner{results: []commandResult{{stdout: `{"result":{"agent":{"agent_status":"idle"}}}`}}}
	service := newTestService(t, "coordinator", runner)
	if err := os.WriteFile(filepath.Join(service.config.StateHome, "context.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := service.Call(context.Background(), "herdr_relay", json.RawMessage(`{"target":"w-test:p2","message":"hello"}`))
	if err == nil || !strings.Contains(err.Error(), "read coordinator record") {
		t.Fatalf("relay with a corrupt coordinator record = %v, want the read error", err)
	}
}
