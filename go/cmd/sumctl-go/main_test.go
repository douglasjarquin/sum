package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCompiledEntrypointCancellationExitsOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal semantics differ on Windows")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "sumctl-go")
	build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", binary, ".")
	build.Dir = "."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, output)
	}
	reference := filepath.Join(dir, "reference.sh")
	marker := filepath.Join(dir, "started")
	if err := os.WriteFile(reference, []byte("#!/bin/sh\nprintf '%s' $$ > \"$SUM_GO_MARKER\"\nsleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "review")
	command.Env = append(os.Environ(), "SUM_PYTHON_HELPER="+reference, "SUM_GO_MARKER="+marker)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			t.Fatal("reference helper did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err := command.Wait()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("compiled exit = %v, want status 1", err)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), `"error": "context canceled"`) {
		t.Fatalf("compiled cancellation output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
