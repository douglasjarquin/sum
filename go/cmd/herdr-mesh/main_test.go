package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func packageDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(file)
}

func meshBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "herdr-mesh-go")
	command := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", binary, ".")
	command.Dir = packageDir(t)
	command.Env = cleanGoEnv(os.Environ())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, output)
	}
	return binary
}

func cleanGoEnv(values []string) []string {
	clean := make([]string, 0, len(values)+1)
	for _, value := range values {
		if strings.HasPrefix(value, "GOROOT=") || strings.HasPrefix(value, "GOBIN=") || strings.HasPrefix(value, "GOTOOLDIR=") || strings.HasPrefix(value, "GOTOOLCHAIN=") {
			continue
		}
		clean = append(clean, value)
	}
	return append(clean, "GO111MODULE=on")
}

func labEnv(t *testing.T, base []string) []string {
	t.Helper()
	home := t.TempDir()
	filtered := make([]string, 0, len(base)+8)
	for _, value := range base {
		switch {
		case strings.HasPrefix(value, "SUM_SESSION="),
			strings.HasPrefix(value, "HERDR_SESSION="),
			strings.HasPrefix(value, "HERDR_ENV="),
			strings.HasPrefix(value, "HERDR_PANE_ID="),
			strings.HasPrefix(value, "SUM_HERDR_BIN="),
			strings.HasPrefix(value, "SUM_HOME="),
			strings.HasPrefix(value, "SUM_INSTALL_ROOT="):
			continue
		default:
			filtered = append(filtered, value)
		}
	}
	return append(filtered,
		"SUM_SESSION=stdio-lab",
		"HERDR_ENV=1",
		"HERDR_PANE_ID=w-lab:p1",
		"SUM_HERDR_BIN=/bin/true",
		"SUM_HOME="+home,
	)
}

func initialize(t *testing.T, command *exec.Cmd, input io.WriteCloser, output *bufio.Scanner) {
	t.Helper()
	request := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"stdio-test","version":"0"}}}` + "\n"
	if _, err := io.WriteString(input, request); err != nil {
		t.Fatal(err)
	}
	if !output.Scan() {
		t.Fatalf("initialize response missing: %v", output.Err())
	}
	var frame map[string]any
	if err := json.Unmarshal(output.Bytes(), &frame); err != nil {
		t.Fatalf("initialize wrote non-JSON: %q", output.Bytes())
	}
	if frame["id"].(float64) != 1 {
		t.Fatalf("initialize frame = %#v", frame)
	}
}

func protocolProcess(t *testing.T) (*exec.Cmd, io.WriteCloser, *bufio.Scanner, *bytes.Buffer) {
	t.Helper()
	command := exec.Command(meshBinary(t))
	command.Env = labEnv(t, cleanGoEnv(os.Environ()))
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(output)
	initialize(t, command, input, scanner)
	return command, input, scanner, &stderr
}

func TestStdioToolErrorKeepsStdoutProtocolOnly(t *testing.T) {
	command, input, output, stderr := protocolProcess(t)
	if _, err := io.WriteString(input, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"+`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"herdr_agent_get","arguments":{"target":"-bad"}}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	if !output.Scan() {
		t.Fatalf("tool error response missing; stderr=%q", stderr.String())
	}
	var frame map[string]any
	if err := json.Unmarshal(output.Bytes(), &frame); err != nil {
		t.Fatalf("tool error wrote non-JSON: %q", output.Bytes())
	}
	result, ok := frame["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("tool error frame = %#v", frame)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("wait: %v stderr=%q", err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestStdioMalformedInputAndEOFShutdown(t *testing.T) {
	command, input, output, stderr := protocolProcess(t)
	if _, err := io.WriteString(input, "not-json\n"); err != nil {
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	for output.Scan() {
		var frame map[string]any
		if err := json.Unmarshal(output.Bytes(), &frame); err != nil {
			t.Fatalf("malformed-input path wrote non-JSON: %q", output.Bytes())
		}
	}
	if err := command.Wait(); err == nil {
		t.Fatal("malformed input unexpectedly succeeded")
	}
	if output.Err() != nil {
		t.Fatalf("stdout read error: %v", output.Err())
	}
	if stderr.Len() == 0 {
		t.Fatal("malformed input produced no stderr diagnostic")
	}
}

func TestStdioEOFShutdownAfterInitialize(t *testing.T) {
	command, input, output, stderr := protocolProcess(t)
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	for output.Scan() {
		var frame map[string]any
		if err := json.Unmarshal(output.Bytes(), &frame); err != nil {
			t.Fatalf("EOF path wrote non-JSON: %q", output.Bytes())
		}
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("wait: %v stderr=%q", err, stderr.String())
	}
	if output.Err() != nil {
		t.Fatalf("stdout read error: %v", output.Err())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
