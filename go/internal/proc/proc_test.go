package proc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func sh(script string) []string { return []string{"/bin/sh", "-c", script} }

func requireKind(t *testing.T, err error, kinds ...error) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil, want %v", kinds)
	}
	for _, kind := range kinds {
		if !errors.Is(err, kind) {
			t.Fatalf("err = %v, want errors.Is %v", err, kind)
		}
	}
}

func within(t *testing.T, start time.Time, limit time.Duration) {
	t.Helper()
	if elapsed := time.Since(start); elapsed > limit {
		t.Fatalf("took %s, want under %s", elapsed, limit)
	}
}

func TestRunContext_callerDeadlineShorterThanCommandTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := RunContext(ctx, Cmd{Argv: []string{"sleep", "5"}, Timeout: 20 * time.Second})
	within(t, start, 2*time.Second)
	requireKind(t, err, ErrUncertain)
	if errors.Is(err, ErrNotStarted) {
		t.Fatalf("a started helper is not a refusal: %v", err)
	}
	if res.Code != -1 {
		t.Fatalf("code = %d, want -1", res.Code)
	}
	if !strings.Contains(err.Error(), "caller's deadline") || !strings.Contains(err.Error(), "its effect is unknown") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunContext_commandTimeoutKeepsWording(t *testing.T) {
	start := time.Now()
	res, err := RunContext(context.Background(), Cmd{Argv: []string{"sleep", "5"}, Timeout: 300 * time.Millisecond})
	within(t, start, 2*time.Second)
	requireKind(t, err, ErrUncertain)
	if err.Error() != "sleep: timed out after 300ms; its effect is unknown" {
		t.Fatalf("err = %q", err)
	}
	if res.Code != -1 {
		t.Fatalf("code = %d, want -1", res.Code)
	}
	if _, err := Run([]string{"sleep", "5"}, "", time.Second, false, nil); err == nil || err.Error() != "sleep: timed out after 1s; its effect is unknown" {
		t.Fatalf("Run err = %v", err)
	}
}

func TestRunContext_explicitCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	start := time.Now()
	res, err := RunContext(ctx, Cmd{Argv: []string{"sleep", "5"}})
	within(t, start, 2*time.Second)
	requireKind(t, err, ErrUncertain)
	if !strings.Contains(err.Error(), "canceled by the caller") || res.Code != -1 {
		t.Fatalf("err = %v code = %d", err, res.Code)
	}
}

func TestRunContext_contextDoneBeforeStartRunsNothing(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := RunContext(ctx, Cmd{Argv: sh("touch " + marker)})
	requireKind(t, err, ErrNotStarted)
	if errors.Is(err, ErrUncertain) || res.Code != -1 {
		t.Fatalf("err = %v code = %d", err, res.Code)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("helper ran after its context was done")
	}
}

func TestRunContext_missingExecutable(t *testing.T) {
	res, err := Run([]string{"/nonexistent/sum-no-such-helper"}, "", 0, false, nil)
	requireKind(t, err, ErrNotStarted)
	if !strings.HasPrefix(err.Error(), "sum-no-such-helper: ") || res.Code != -1 {
		t.Fatalf("err = %v code = %d", err, res.Code)
	}
}

func TestRunContext_nonzeroExitWithAndWithoutCheck(t *testing.T) {
	_, err := Run(sh("echo oops >&2; exit 3"), "", 0, true, nil)
	if err == nil || err.Error() != "sh exited 3: oops" {
		t.Fatalf("check err = %v", err)
	}
	if errors.Is(err, ErrUncertain) || errors.Is(err, ErrNotStarted) {
		t.Fatalf("a completed nonzero exit is neither uncertain nor a refusal: %v", err)
	}
	res, err := Run(sh("echo out; echo oops >&2; exit 3"), "", 0, false, nil)
	if err != nil || res.Code != 3 || res.Stdout != "out\n" || res.Stderr != "oops\n" {
		t.Fatalf("check=false res = %+v err = %v", res, err)
	}
}

func TestRunContext_multiMegabyteStdoutIsBoundedAndRejected(t *testing.T) {
	res, err := RunContext(context.Background(), Cmd{Argv: sh("head -c 33554432 /dev/zero"), StdoutLimit: 8 << 20})
	requireKind(t, err, ErrOutputLimit)
	if !res.StdoutTruncated || len(res.Stdout) != 8<<20 {
		t.Fatalf("truncated=%v len=%d", res.StdoutTruncated, len(res.Stdout))
	}
}

func TestRunContext_neverEndingStdoutStopsEarly(t *testing.T) {
	start := time.Now()
	res, err := RunContext(context.Background(), Cmd{Argv: []string{"yes"}, StdoutLimit: 1 << 20, Timeout: 20 * time.Second})
	within(t, start, 3*time.Second)
	requireKind(t, err, ErrOutputLimit, ErrUncertain)
	if !res.StdoutTruncated || len(res.Stdout) != 1<<20 || res.Code != -1 {
		t.Fatalf("truncated=%v len=%d code=%d", res.StdoutTruncated, len(res.Stdout), res.Code)
	}
}

func TestRunContext_freeTextStdoutKeepsHead(t *testing.T) {
	res, err := RunContext(context.Background(), Cmd{Argv: sh("printf 'first-line\\n'; head -c 200000 /dev/zero"), StdoutLimit: 1024, FreeText: true})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !res.StdoutTruncated || len(res.Stdout) != 1024 || !strings.HasPrefix(res.Stdout, "first-line\n") || res.Code != 0 {
		t.Fatalf("res truncated=%v len=%d code=%d", res.StdoutTruncated, len(res.Stdout), res.Code)
	}
}

func TestRunContext_stderrKeepsTailAndIsOnlyFlagged(t *testing.T) {
	res, err := RunContext(context.Background(), Cmd{Argv: sh("head -c 4194304 /dev/zero | tr '\\0' x >&2; echo LAST-MARKER >&2"), StderrLimit: 64 << 10})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !res.StderrTruncated || len(res.Stderr) != 64<<10 || !strings.HasSuffix(res.Stderr, "LAST-MARKER\n") {
		t.Fatalf("truncated=%v len=%d tail=%q", res.StderrTruncated, len(res.Stderr), res.Stderr[len(res.Stderr)-20:])
	}
}

func TestRunContext_descendantHoldingPipesDoesNotHang(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	start := time.Now()
	res, err := RunContext(context.Background(), Cmd{Argv: sh("sleep 30 & echo $! > " + pidFile + "; echo hi")})
	within(t, start, PipeGrace+2*time.Second)
	t.Cleanup(func() { stopFixture(t, pidFile) })
	requireKind(t, err, ErrUncertain)
	if res.Code != -1 || res.Stdout != "hi\n" {
		t.Fatalf("res = %+v", res)
	}
	if !strings.Contains(err.Error(), "sh exited 0 but a descendant still holds its output and was not stopped") {
		t.Fatalf("err = %v", err)
	}
	if running, _ := PIDRunning(fixturePID(t, pidFile)); !running {
		t.Fatal("the runner stopped a descendant it does not own")
	}
}

func TestRunContext_timeoutWithHeldPipesNamesBoth(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	start := time.Now()
	// One second leaves the shell time to start its sleeper even on a loaded machine.
	_, err := RunContext(context.Background(), Cmd{Argv: sh("sleep 30 & echo $! > " + pidFile + "; wait"), Timeout: time.Second})
	within(t, start, time.Second+PipeGrace+2*time.Second)
	t.Cleanup(func() { stopFixture(t, pidFile) })
	fixturePID(t, pidFile)
	requireKind(t, err, ErrUncertain)
	if !strings.Contains(err.Error(), "timed out after 1s") || !strings.Contains(err.Error(), "descendant still holds its output") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunContext_timeoutAfterSideEffectIsNotReplayed(t *testing.T) {
	log := filepath.Join(t.TempDir(), "effects")
	_, err := RunContext(context.Background(), Cmd{Argv: sh("echo effect >> " + log + "; sleep 5"), Timeout: 300 * time.Millisecond})
	requireKind(t, err, ErrUncertain)
	data, readErr := os.ReadFile(log)
	if readErr != nil || string(data) != "effect\n" {
		t.Fatalf("effects = %q err = %v", data, readErr)
	}
}

func TestRunContext_envAndCwdSemantics(t *testing.T) {
	t.Setenv("SUM_PROC_TEST_MARKER", "inherited")
	res, err := Run(sh("echo ${SUM_PROC_TEST_MARKER:-absent}"), "", 0, true, nil)
	if err != nil || res.Stdout != "inherited\n" {
		t.Fatalf("nil env res = %+v err = %v", res, err)
	}
	res, err = Run(sh("echo ${SUM_PROC_TEST_MARKER:-absent}"), "", 0, true, []string{"PATH=" + os.Getenv("PATH")})
	if err != nil || res.Stdout != "absent\n" {
		t.Fatalf("explicit env res = %+v err = %v", res, err)
	}
	dir := t.TempDir()
	res, err = Run([]string{"pwd", "-P"}, dir, 0, true, nil)
	real, _ := filepath.EvalSymlinks(dir)
	if err != nil || strings.TrimSpace(res.Stdout) != real {
		t.Fatalf("cwd res = %+v err = %v want %s", res, err, real)
	}
	res, err = Run([]string{"printf", "%s|", "a b", "$HOME", "'q'"}, "", 0, true, nil)
	if err != nil || res.Stdout != "a b|$HOME|'q'|" {
		t.Fatalf("argv res = %+v err = %v", res, err)
	}
}

func fixturePID(t *testing.T, pidFile string) int {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

// stopFixture stops only the sleeper this test itself started.
func stopFixture(t *testing.T, pidFile string) {
	if data, err := os.ReadFile(pidFile); err == nil {
		if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
	}
}
