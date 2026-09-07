package mesh

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeRunner struct {
	mu        sync.Mutex
	calls     [][]string
	durations []time.Duration
	results   []commandResult
	errors    []error
}

func (r *fakeRunner) Run(_ context.Context, args []string, duration time.Duration) (commandResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
	machine, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	session, pane := "mesh-test", "w-test:p1"
	writeJSON(t, filepath.Join(home, "sessions", registrationKey(machine, session, pane)+".json"), map[string]string{
		"instance": instance, "machine": machine, "session": session, "pane": pane, "role": role,
	})
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

func TestService_relay_preservesLeadingDashMessageAfterIdlePreflight(t *testing.T) {
	runner := &fakeRunner{results: []commandResult{{stdout: `{"result":{"agent":{"agent_status":"idle"}}}`}, {stdout: `{"result":{"ok":true}}`}}}
	service := newTestService(t, "coordinator", runner)

	result, err := service.Call(context.Background(), "herdr_relay", json.RawMessage(`{"target":"w-test:p2","message":"--session malicious"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result == "" || len(runner.calls) != 2 {
		t.Fatalf("result=%q calls=%d", result, len(runner.calls))
	}
	if got := runner.calls[1][3]; got != "sum message:\n--session malicious" {
		t.Fatalf("prompt message = %q", got)
	}
}

func TestService_relay_refusesBusyTargetBeforePrompt(t *testing.T) {
	runner := &fakeRunner{results: []commandResult{{stdout: `{"result":{"agent":{"agent_status":"working"}}}`}}}
	service := newTestService(t, "coordinator", runner)

	_, err := service.Call(context.Background(), "herdr_relay", json.RawMessage(`{"target":"w-test:p2","message":"hello"}`))
	if err == nil || len(runner.calls) != 1 {
		t.Fatalf("error=%v calls=%d", err, len(runner.calls))
	}
}

func TestService_handoff_reportsUncertainSubmissionWithoutReadingStaleOutput(t *testing.T) {
	runner := &fakeRunner{results: []commandResult{{stdout: `{"result":{"agent":{"agent_status":"idle"}}}`}, {}}, errors: []error{nil, errors.New("timeout")}}
	service := newTestService(t, "coordinator", runner)

	result, err := service.Call(context.Background(), "herdr_handoff", json.RawMessage(`{"target":"w-test:p2","message":"review"}`))
	if err == nil || result != "" {
		t.Fatalf("result=%q error=%v", result, err)
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

func TestService_wait_clampsTimeoutLikeTheNodeHandler(t *testing.T) {
	runner := &fakeRunner{results: []commandResult{{stdout: `{"result":{"agent":{"agent_status":"idle"}}}`}}}
	service := newTestService(t, "coordinator", runner)

	if _, err := service.Call(context.Background(), "herdr_agent_wait", json.RawMessage(`{"target":"w-test:p2","timeout_ms":999999}`)); err != nil {
		t.Fatal(err)
	}
	if runner.durations[0] != 65*time.Second || runner.calls[0][6] != "60000" {
		t.Fatalf("timeout=%s args=%v", runner.durations[0], runner.calls[0])
	}
}
