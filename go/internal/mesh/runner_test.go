package mesh

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeHerdr(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "herdr")
	contents := `#!/bin/sh
printf '%s\n' "$@" >> "$CALLS"
if [ "${FAKE_HANG:-}" = "1" ]; then
  sleep 2
fi
if [ "$3" = "agent" ] && [ "$4" = "get" ]; then
  printf '{"agent":{"agent_status":"%s"}}\n' "${FAKE_STATUS:-idle}"
  exit 0
fi
if [ "$3" = "agent" ] && [ "$4" = "prompt" ] && [ "${FAKE_PROMPT_FAIL:-}" = "1" ]; then
  printf '%s\n' 'prompt uncertain' >&2
  exit 7
fi
if [ "$3" = "agent" ] && [ "$4" = "read" ]; then
  printf '%s\n' 'visible screen'
  exit 0
fi
printf '{"ok":true}\n'
`
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestRelayPreservesMessageAsOneNonOptionArgument(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "calls")
	t.Setenv("SUM_SESSION", "sum-test")
	t.Setenv("CALLS", calls)
	operations := NewOperations(NewRunner(fakeHerdr(t)))
	result, err := operations.Relay(context.Background(), "w1:p1", "--session malicious")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "submitted-not-acknowledged") {
		t.Fatalf("result = %q", result)
	}
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "sum message:\n--session malicious") {
		t.Fatalf("calls = %q", data)
	}
}

func TestRelayRefusesBusyAgentBeforePrompt(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "calls")
	t.Setenv("SUM_SESSION", "sum-test")
	t.Setenv("CALLS", calls)
	t.Setenv("FAKE_STATUS", "working")
	operations := NewOperations(NewRunner(fakeHerdr(t)))
	_, err := operations.Relay(context.Background(), "w1:p1", "hello")
	if err == nil || !strings.Contains(err.Error(), "do not inject") {
		t.Fatalf("error = %v", err)
	}
	data, readErr := os.ReadFile(calls)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(data), "prompt") {
		t.Fatalf("busy relay injected prompt: %q", data)
	}
}

func TestHandoffFailureIsUncertainErrorResult(t *testing.T) {
	t.Setenv("SUM_SESSION", "sum-test")
	t.Setenv("CALLS", filepath.Join(t.TempDir(), "calls"))
	t.Setenv("FAKE_PROMPT_FAIL", "1")
	operations := NewOperations(NewRunner(fakeHerdr(t)))
	result, err := operations.Handoff(context.Background(), "w1:p1", "review", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Text, "may already have been submitted") {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunnerRequiresExplicitSession(t *testing.T) {
	t.Setenv("SUM_SESSION", "")
	t.Setenv("HERDR_SESSION", "")
	t.Setenv("HERDR_SOCKET_PATH", "")
	_, err := NewRunner(fakeHerdr(t)).Run(context.Background(), []string{"agent", "list"}, 0)
	if err == nil || !strings.Contains(err.Error(), "explicit name") {
		t.Fatalf("error = %v", err)
	}
}
