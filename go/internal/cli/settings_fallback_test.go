package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSettings_fallsBackToReferenceForUnknownSubcommand(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	reference := filepath.Join(dir, "reference.sh")
	if err := os.WriteFile(reference, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$SUM_GO_ARGS_FILE\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_GO_ARGS_FILE", argsFile)

	home := filepath.Join(dir, "state")
	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs([]string{"--home", home, "settings", "explode"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "--home\n" + home + "\nsettings\nexplode\n"
	if string(got) != want {
		t.Fatalf("reference argv = %q, want %q", got, want)
	}
}
