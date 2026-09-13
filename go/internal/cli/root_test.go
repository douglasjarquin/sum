package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkNewRoot(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		NewRoot("", io.Discard, io.Discard)
	}
}

func TestHelpBuildsWithoutTouchingReferenceOrHome(t *testing.T) {
	var stdout, stderr bytes.Buffer
	root := NewRoot("/path/that/must/not/be opened", &stdout, &stderr)
	root.SetArgs([]string{"--help"})

	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if stdout.Len() == 0 {
		t.Fatal("help produced no output")
	}
}

func TestUnknownCommandDoesNotInvokeReference(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "invoked")
	reference := filepath.Join(dir, "reference.sh")
	if err := os.WriteFile(reference, []byte("#!/bin/sh\ntouch \"$SUM_GO_MARKER\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_GO_MARKER", marker)
	root := NewRoot(reference, &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"not-a-command"})
	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("unknown command unexpectedly succeeded")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("reference marker exists after invalid invocation: %v", err)
	}
}

func TestCommandTreesDoNotLeakFlags(t *testing.T) {
	first := NewRoot("", &bytes.Buffer{}, &bytes.Buffer{})
	second := NewRoot("", &bytes.Buffer{}, &bytes.Buffer{})

	if first == second {
		t.Fatal("NewRoot returned a shared command tree")
	}
	if first.PersistentFlags() == second.PersistentFlags() {
		t.Fatal("NewRoot returned shared flag state")
	}
}

func TestUnknownFlagsAreGoErrors(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRoot("", &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"--home", home, "status", "--arg=-m"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected unrecognized arguments")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("err = %v, want unknown flag", err)
	}
}

func TestCleanupUnknownArgsAreGoErrors(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRoot("", &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"--home", home, "cleanup"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected invalid cleanup arguments")
	}
}
