package environment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLsof puts an lsof that runs script first on PATH.
func fakeLsof(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "lsof")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestListenersRejectAnIncompleteProcessTable(t *testing.T) {
	// A table that overflows the capture bound is cut off mid-stream: it must be reported as unobserved,
	// never parsed into a shorter listener map that would read as "not listening".
	fakeLsof(t, `printf 'p4242\nn127.0.0.1:8080\n'; exec yes 'p1'`)
	rows, reason := listeners()
	if rows != nil || reason == "" || !strings.Contains(reason, "stdout exceeded") {
		t.Fatalf("rows = %v reason = %q", rows, reason)
	}
	cwds, reason := processCwds(nil)
	if cwds != nil || reason == "" {
		t.Fatalf("cwds = %v reason = %q", cwds, reason)
	}
}

func TestListenersParseACompleteTable(t *testing.T) {
	fakeLsof(t, `printf 'p4242\nn127.0.0.1:8080\n'`)
	rows, reason := listeners()
	if reason != "" || len(rows[8080]) != 1 {
		t.Fatalf("rows = %v reason = %q", rows, reason)
	}
}
