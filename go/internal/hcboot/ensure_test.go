package hcboot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureSkipsARuntimeWithoutHC(t *testing.T) {
	if Ensure(t.TempDir()) != nil {
		t.Fatal("missing hc produced a report")
	}
}

func TestEnsureReportsAReadyHolder(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "hc.log")
	writeHC(t, root, log, "ready")
	t.Setenv("HC_SESSION", "not-the-default")
	report := Ensure(root)
	if report == nil || reportValue(t, report, "ok") != true || reportValue(t, report, "action") != "ready" {
		t.Fatalf("ready report = %v", report)
	}
	calls := readLog(t, log)
	if calls != "--json server status\n" {
		t.Fatalf("calls = %q", calls)
	}
	if strings.Contains(calls, "not-the-default") {
		t.Fatal("session leaked into the hc command")
	}
}

func TestEnsureStartsADownCore(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "hc.log")
	writeHC(t, root, log, "down")
	report := Ensure(root)
	if reportValue(t, report, "ok") != true || reportValue(t, report, "action") != "started" {
		t.Fatalf("start report = %v", report)
	}
	if !strings.Contains(readLog(t, log), "--json server start") {
		t.Fatalf("start was not requested: %s", readLog(t, log))
	}
}

func TestEnsureRestartsThenSpawnsAHolder(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "hc.log")
	writeHC(t, root, log, "wedged")
	report := Ensure(root)
	if reportValue(t, report, "ok") != true || reportValue(t, report, "action") != "spawned" || reportValue(t, report, "holder_pid") != 9 {
		t.Fatalf("spawn report = %v", report)
	}
	calls := readLog(t, log)
	for _, want := range []string{"--json server restart", "--json run true", "rm t1"} {
		if !strings.Contains(calls, want) {
			t.Fatalf("missing %q in %s", want, calls)
		}
	}
}

func TestEnsureDoesNotRestartWhileTerminalsAreLive(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "hc.log")
	writeHC(t, root, log, "busy")
	report := Ensure(root)
	if reportValue(t, report, "ok") != false || reportValue(t, report, "action") != "blocked" {
		t.Fatalf("busy report = %v", report)
	}
	if strings.Contains(readLog(t, log), "restart") {
		t.Fatal("live terminals were restarted")
	}
}

func writeHC(t *testing.T, root, log, mode string) {
	t.Helper()
	path := filepath.Join(root, ".local", "bin", "hc")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$HCBOOT_LOG"
status_n=0
if [ -f "$HCBOOT_LOG.count" ]; then status_n=$(cat "$HCBOOT_LOG.count"); fi
case "$*" in
"--json server status")
  status_n=$((status_n + 1))
  printf '%s' "$status_n" > "$HCBOOT_LOG.count"
  case "$HCBOOT_MODE" in
  ready) printf '%s\n' '{"live":0,"holder":{"current":true,"pid":4}}' ;;
  down)
    if [ "$status_n" -eq 1 ]; then exit 1; fi
    printf '%s\n' '{"live":0,"holder":{"current":true,"pid":5}}'
    ;;
  wedged)
    if [ "$status_n" -lt 3 ]; then printf '%s\n' '{"live":0,"holder":null}'; else printf '%s\n' '{"live":0,"holder":{"current":true,"pid":9}}'; fi
    ;;
  busy) printf '%s\n' '{"live":2,"holder":null}' ;;
  esac
  ;;
"--json run true") printf '%s\n' '{"address":"t1","state":"finished"}' ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HCBOOT_LOG", log)
	t.Setenv("HCBOOT_MODE", mode)
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func reportValue(t *testing.T, report interface{ Get(string) (any, bool) }, key string) any {
	t.Helper()
	if report == nil {
		t.Fatal("missing report")
	}
	value, ok := report.Get(key)
	if !ok {
		t.Fatalf("missing %s", key)
	}
	return value
}
