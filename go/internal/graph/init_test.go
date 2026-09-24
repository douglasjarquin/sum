package graph

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

// isolate keeps these tests off any real sum state and the runtime's pinned codegraph.
func isolate(t *testing.T) {
	t.Helper()
	for _, key := range []string{"SUM_HOME", "SUM_SESSION", "SUM_NOW", "SUM_GRAPH_TIMEOUT", "HERDR_ENV", "HERDR_PANE_ID", "HERDR_SOCKET_PATH"} {
		t.Setenv(key, "")
	}
}

// fakeCodegraph writes a shell script that answers `--version` with the pin and runs body for any other command.
func fakeCodegraph(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codegraph")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo " + CodegraphVersion + "; exit 0; fi\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_CODEGRAPH_BIN", path)
	return path
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "--allow-empty", "-m", "one"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func gitHead(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func lastAttempt(t *testing.T, record *ordjson.Object) *ordjson.Object {
	t.Helper()
	list, _ := record.Get("attempts")
	items, _ := list.([]any)
	if len(items) == 0 {
		t.Fatalf("no attempts recorded")
	}
	return asObject(items[len(items)-1])
}

func get(o *ordjson.Object, key string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	return v
}

func TestInitCheckoutTimeoutRecordsFailedAttempt(t *testing.T) {
	isolate(t)
	fakeCodegraph(t, "exec sleep 30")
	t.Setenv("SUM_GRAPH_TIMEOUT", "1")
	repo := gitRepo(t)

	start := time.Now()
	record := InitCheckout(nil, t.TempDir(), repo, "task", nil)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("InitCheckout took %s; the init bound was not applied", elapsed)
	}
	if state := get(record, "state"); state != "failed" {
		t.Fatalf("state = %v, want failed", state)
	}
	attempt := lastAttempt(t, record)
	if get(attempt, "action") != "init" || get(attempt, "ok") != false {
		t.Fatalf("last attempt = %v %v, want init not ok", get(attempt, "action"), get(attempt, "ok"))
	}
	errText := asString(get(attempt, "error"))
	if !strings.Contains(errText, "timed out after 1s") {
		t.Fatalf("attempt error = %q, want a timed out reason", errText)
	}
	if strings.Contains(errText, "descendant") {
		t.Fatalf("attempt error = %q names a descendant, but the helper was the only process", errText)
	}
}

func TestInitCheckoutTimeoutNamesHeldDescendant(t *testing.T) {
	isolate(t)
	pidFile := filepath.Join(t.TempDir(), "indexer.pid")
	// Like the real launcher chain: the shim's indexer child inherits stdout and outlives the stopped launcher.
	fakeCodegraph(t, "sleep 30 &\necho $! > '"+pidFile+"'\nwait")
	t.Cleanup(func() {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw))); convErr == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	// Three seconds leaves the fake time to start its indexer even on a loaded machine.
	t.Setenv("SUM_GRAPH_TIMEOUT", "3")
	repo := gitRepo(t)

	start := time.Now()
	record := InitCheckout(nil, t.TempDir(), repo, "task", nil)
	if elapsed := time.Since(start); elapsed > 12*time.Second {
		t.Fatalf("InitCheckout took %s", elapsed)
	}
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatalf("the fake never started its indexer before the bound: %v", err)
	}
	if state := get(record, "state"); state != "failed" {
		t.Fatalf("state = %v, want failed", state)
	}
	errText := asString(get(lastAttempt(t, record), "error"))
	for _, want := range []string{"timed out after 3s", "descendant still holds its output and was not stopped"} {
		if !strings.Contains(errText, want) {
			t.Fatalf("attempt error = %q, want %q", errText, want)
		}
	}
}

func TestInitCheckoutSuccessRecordsReady(t *testing.T) {
	isolate(t)
	fakeCodegraph(t, "echo indexing; exit 0")
	repo := gitRepo(t)

	record := InitCheckout(nil, t.TempDir(), repo, "task", nil)
	if state := get(record, "state"); state != "ready" {
		t.Fatalf("state = %v, error = %v, want ready", state, get(record, "error"))
	}
	if head := get(record, "indexed_head"); head != gitHead(t, repo) {
		t.Fatalf("indexed_head = %v, want %s", head, gitHead(t, repo))
	}
}

func TestInitCheckoutExitFailureKeepsToolOutput(t *testing.T) {
	isolate(t)
	fakeCodegraph(t, "echo 'index broke' >&2; exit 3")
	repo := gitRepo(t)

	record := InitCheckout(nil, t.TempDir(), repo, "task", nil)
	if state := get(record, "state"); state != "failed" {
		t.Fatalf("state = %v, want failed", state)
	}
	if errText := asString(get(lastAttempt(t, record), "error")); !strings.Contains(errText, "index broke") {
		t.Fatalf("attempt error = %q, want the tool's output", errText)
	}
}

func TestObserveStatusRejectsIncompleteJSON(t *testing.T) {
	isolate(t)
	bin := fakeCodegraph(t, "printf '{\"initialized\": true, '")
	live := observeStatus(bin, gitRepo(t))
	if _, ok := live.Get("status"); ok {
		t.Fatalf("incomplete JSON produced a live status: %v", get(live, "status"))
	}
	if errText := asString(get(live, "error")); !strings.Contains(errText, "JSON") {
		t.Fatalf("error = %q, want a JSON reason", errText)
	}
}

func TestObserveStatusStaleFromPendingChanges(t *testing.T) {
	isolate(t)
	bin := fakeCodegraph(t, "echo '{\"initialized\": true, \"pendingChanges\": {\"added\": 1, \"modified\": 0}}'")
	live := observeStatus(bin, gitRepo(t))
	if get(live, "status") == nil {
		t.Fatalf("no status; error = %v", get(live, "error"))
	}
	if state := get(asObject(get(live, "freshness")), "state"); state != "stale" {
		t.Fatalf("freshness = %v, want stale", state)
	}
	if _, ok := live.Get("error"); ok {
		t.Fatalf("unexpected error on success: %v", get(live, "error"))
	}
}

func TestObserveStatusRejectsOverLimitOutput(t *testing.T) {
	isolate(t)
	bin := fakeCodegraph(t, "exec head -c 9000000 /dev/zero")
	live := observeStatus(bin, gitRepo(t))
	if _, ok := live.Get("status"); ok {
		t.Fatalf("over-limit output produced a live status")
	}
	if errText := asString(get(live, "error")); !strings.Contains(errText, "exceeded") {
		t.Fatalf("error = %q, want an output-limit reason", errText)
	}
}

func TestObserveStatusReportsFailingCommand(t *testing.T) {
	isolate(t)
	bin := fakeCodegraph(t, "echo '{\"initialized\": true}'; echo 'no index' >&2; exit 2")
	live := observeStatus(bin, gitRepo(t))
	if _, ok := live.Get("status"); ok {
		t.Fatalf("a failing status command produced a live status")
	}
	if errText := asString(get(live, "error")); !strings.Contains(errText, "no index") {
		t.Fatalf("error = %q, want the command's failure", errText)
	}
}

func TestToolAvailableWithPinnedVersion(t *testing.T) {
	isolate(t)
	fakeCodegraph(t, "exit 0")
	tool := Tool(t.TempDir())
	if get(tool, "available") != true {
		t.Fatalf("available = %v, reason = %v", get(tool, "available"), get(tool, "reason"))
	}
	if get(tool, "version") != CodegraphVersion {
		t.Fatalf("version = %v", get(tool, "version"))
	}
}

func TestToolMissingBinaryNotInstalled(t *testing.T) {
	isolate(t)
	t.Setenv("SUM_CODEGRAPH_BIN", filepath.Join(t.TempDir(), "no-such-codegraph"))
	tool := Tool(t.TempDir())
	if get(tool, "available") != false {
		t.Fatalf("available = %v", get(tool, "available"))
	}
	if reason := asString(get(tool, "reason")); !strings.Contains(reason, "not installed") {
		t.Fatalf("reason = %q", reason)
	}
}

func TestToolWrongVersionAndNoVersion(t *testing.T) {
	isolate(t)
	path := filepath.Join(t.TempDir(), "codegraph")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 9.9.9\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_CODEGRAPH_BIN", path)
	if reason := asString(get(Tool(t.TempDir()), "reason")); !strings.Contains(reason, "is not the tested pin") {
		t.Fatalf("reason = %q", reason)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho broken >&2\nexit 4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if reason := asString(get(Tool(t.TempDir()), "reason")); !strings.Contains(reason, "exited 4 without a version: broken") {
		t.Fatalf("reason = %q", reason)
	}
}

func TestGraphTimeoutKnob(t *testing.T) {
	isolate(t)
	for value, want := range map[string]time.Duration{"": 300 * time.Second, "7": 7 * time.Second, "0": 300 * time.Second, "-3": 300 * time.Second, "abc": 300 * time.Second} {
		t.Setenv("SUM_GRAPH_TIMEOUT", value)
		if got := graphTimeout(); got != want {
			t.Fatalf("SUM_GRAPH_TIMEOUT=%q: graphTimeout() = %s, want %s", value, got, want)
		}
	}
}
