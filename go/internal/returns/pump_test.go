package returns

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/herdrclient"
)

// fakeHerdr writes an executable herdr that runs script.
func fakeHerdr(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPromptFailureKeepsPossibleDeliveryUncertain(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Cleanup(func() {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw))); convErr == nil && pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
		}
	})
	cases := []struct {
		name   string
		script string
		want   string
	}{
		{name: "timed out", script: "sleep 5", want: "uncertain"},
		{name: "exited 0 with a descendant holding the output", script: "sleep 30 & echo $! > '" + pidFile + "'\necho '{\"result\": {}}'", want: "uncertain"},
		{name: "stopped for runaway output", script: "exec yes", want: "uncertain"},
		{name: "refused before delivery", script: "echo 'no such pane' >&2; exit 2", want: "not-delivered"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := herdrclient.Call(fakeHerdr(t, tc.script), "lab", time.Second, "agent", "prompt", "p1", "hello")
			if err == nil {
				t.Fatal("expected a send error")
			}
			state, detail, _ := promptFailure(err)
			if state != tc.want {
				t.Fatalf("state = %s (%s), want %s", state, detail, tc.want)
			}
			if state == "uncertain" && !strings.Contains(detail, "not retried by itself") {
				t.Fatalf("detail = %q", detail)
			}
		})
	}
	if state, _, _ := promptFailure(&unreachableError{state: "pending-unreachable", msg: "Recipient is on another machine."}); state != "not-delivered" {
		t.Fatalf("unreachable state = %s", state)
	}
	if state, _, _ := promptFailure(errors.New("herdr: timed out after 5s; its effect is unknown")); state != "uncertain" {
		t.Fatalf("legacy timeout text state = %s", state)
	}
}
