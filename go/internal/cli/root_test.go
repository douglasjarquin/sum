package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestCompatibilityCommandPreservesLeadingDashAndRepeatedArguments(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	reference := filepath.Join(dir, "reference.sh")
	if err := os.WriteFile(reference, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$SUM_GO_ARGS_FILE\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_GO_ARGS_FILE", argsFile)
	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs([]string{"status", "--arg=-m", "--arg=-m", "--"})

	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("compatibility command failed: %v", err)
	}
	want := "status\n--arg=-m\n--arg=-m\n--\n"
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("reference argv = %q, want %q", got, want)
	}
}

func TestCompatibilityPreservesGlobalHomePosition(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	reference := filepath.Join(dir, "reference.sh")
	if err := os.WriteFile(reference, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$SUM_GO_ARGS_FILE\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_GO_ARGS_FILE", argsFile)
	for _, input := range [][]string{
		{"--home", filepath.Join(dir, "before"), "status", "--arg=-m"},
		{"status", "--home=" + filepath.Join(dir, "after"), "--arg=-m"},
	} {
		root := NewRoot(reference, &bytes.Buffer{}, &bytes.Buffer{})
		root.SetArgs(input)
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("compatibility command failed for %q: %v", input, err)
		}
		got, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatal(err)
		}
		want := "--home\n" + input[1] + "\nstatus\n--arg=-m\n"
		if input[0] == "status" {
			want = "status\n" + input[1] + "\n--arg=-m\n"
		}
		if string(got) != want {
			t.Fatalf("reference argv = %q, want %q", got, want)
		}
	}
}

func TestCompatibilityHonorsCancellation(t *testing.T) {
	dir := t.TempDir()
	reference := filepath.Join(dir, "reference.sh")
	if err := os.WriteFile(reference, []byte("#!/bin/sh\nsleep 10\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reference, &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"cleanup"})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := root.ExecuteContext(ctx); err == nil || err != context.DeadlineExceeded {
		t.Fatalf("cancellation error = %v, want context deadline", err)
	}
}

func TestCompatibilityPreservesExitCode(t *testing.T) {
	dir := t.TempDir()
	reference := filepath.Join(dir, "reference.sh")
	if err := os.WriteFile(reference, []byte("#!/bin/sh\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reference, &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"cleanup"})
	err := root.ExecuteContext(context.Background())
	exit, ok := err.(*ExitError)
	if !ok || exit.Code != 7 {
		t.Fatalf("error = %#v, want ExitError{Code: 7}", err)
	}
}
