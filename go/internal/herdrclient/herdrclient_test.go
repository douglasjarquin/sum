package herdrclient

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/proc"
)

// fakeHerdr writes an executable shell script standing in for herdr; it never reaches a real Herdr session.
func fakeHerdr(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestObserveRejectsIncompleteJSON(t *testing.T) {
	herdr := fakeHerdr(t, `printf '{"result": {'`)
	value, code, err := Observe(herdr, "lab", 5*time.Second, "pane", "get", "p1")
	if err == nil || value != nil || code != "" {
		t.Fatalf("value=%v code=%q err=%v, want an error and no observation", value, code, err)
	}
	if !strings.Contains(err.Error(), "Herdr did not return JSON") {
		t.Fatalf("err=%v", err)
	}
}

func TestCallRejectsOversizedStdout(t *testing.T) {
	// A complete, valid envelope past the stdout limit: a prefix of it is not an observation.
	herdr := fakeHerdr(t, `printf '{"result":"'; head -c 9437184 /dev/zero | tr '\0' 'a'; printf '"}'`)
	value, err := Call(herdr, "lab", 20*time.Second, "pane", "read", "p1")
	if err == nil || value != nil {
		t.Fatalf("value of %T returned, err=%v; want an output-limit error and no value", value, err)
	}
	if !errors.Is(err, proc.ErrOutputLimit) {
		t.Fatalf("err=%v, want errors.Is proc.ErrOutputLimit", err)
	}
}

func TestObserveReportsHerdrErrorCode(t *testing.T) {
	herdr := fakeHerdr(t, `printf '{"error":{"code":"pane_not_found"}}' >&2; exit 1`)
	value, code, err := Observe(herdr, "lab", 5*time.Second, "pane", "get", "p1")
	if value != nil || code != "pane_not_found" || err != nil {
		t.Fatalf("value=%v code=%q err=%v, want (nil, pane_not_found, nil)", value, code, err)
	}
}

func TestObserveTimeoutIsUncertain(t *testing.T) {
	herdr := fakeHerdr(t, `exec sleep 5`)
	start := time.Now()
	_, _, err := Observe(herdr, "lab", 300*time.Millisecond, "pane", "get", "p1")
	if err == nil || !strings.Contains(err.Error(), "herdr: timed out after 300ms; its effect is unknown") {
		t.Fatalf("err=%v", err)
	}
	if !errors.Is(err, proc.ErrUncertain) {
		t.Fatalf("err=%v, want errors.Is proc.ErrUncertain", err)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("took %s, want the timeout to stop the fake", elapsed)
	}
}

func TestCallNonzeroExitKeepsDetail(t *testing.T) {
	herdr := fakeHerdr(t, `echo 'boom' >&2; exit 3`)
	_, err := Call(herdr, "lab", 5*time.Second, "pane", "get", "p1")
	if err == nil || err.Error() != "herdr exited 3: boom" {
		t.Fatalf("err=%v", err)
	}
}

func TestVersionParses(t *testing.T) {
	herdr := fakeHerdr(t, `echo 'herdr 0.9.0'`)
	version, found, err := Version(herdr)
	if err != nil || version != "0.9.0" || found != "herdr 0.9.0" {
		t.Fatalf("version=%q found=%q err=%v", version, found, err)
	}
}

func TestAgentStartCallTimeoutExceedsReadinessBound(t *testing.T) {
	if AgentStartTimeout != 90*time.Second {
		t.Fatalf("AgentStartTimeout = %s, want 90s", AgentStartTimeout)
	}
	if AgentStartCallTimeout <= AgentStartTimeout {
		t.Fatalf("AgentStartCallTimeout = %s, want above %s", AgentStartCallTimeout, AgentStartTimeout)
	}
	args := AgentStartArgs("t-test", "grok", "w1:p1")
	want := []string{"agent", "start", "t-test", "--kind", "grok", "--pane", "w1:p1", "--timeout", "90000"}
	if !slices.Equal(args, want) {
		t.Fatalf("AgentStartArgs = %v, want %v", args, want)
	}
}
