package graph

import (
	"fmt"
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
	statusFake(t, "complete")
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

func TestIsWriterMatchesRealAndFakeArgvShapes(t *testing.T) {
	checkout := "/work/.sum/worktrees with spaces/sum-t-1"
	roots := []string{checkout}
	cases := map[string]bool{
		// The real 1.5.0 indexer behind the npm shim.
		"/opt/x/@colbymchenry/codegraph-linux-x64/node --liftoff-only --disable-warning=ExperimentalWarning /opt/x/@colbymchenry/codegraph-linux-x64/lib/dist/bin/codegraph.js init " + checkout: true,
		"/bin/sh /tmp/fake/codegraph init " + checkout:                true,
		"python3 /repo/tests/fixtures/codegraph.py index " + checkout: true,
		"/rt/.local/bin/codegraph sync " + checkout + "/":             true,
		"/rt/.local/bin/codegraph serve --mcp --path " + checkout:     false,
		"/rt/.local/bin/codegraph status --json " + checkout:          false,
		"/rt/.local/bin/codegraph init " + checkout + "-other":        false,
		"/rt/.local/bin/codegraph init /elsewhere" + checkout:         false,
		"node /opt/x/codegraph/npm-shim.js init " + checkout:          false,
		"sleep 30 " + checkout:                                        false,
	}
	for args, want := range cases {
		if got := isWriter(args, roots); got != want {
			t.Errorf("isWriter(%q) = %v, want %v", args, got, want)
		}
	}
}

func TestLiveWritersFindsARunningIndexer(t *testing.T) {
	isolate(t)
	// Not exec: the shell must keep the fake's own argv (`<fake>/codegraph init <repo>`) in the process table.
	bin := fakeCodegraph(t, "sleep 30; exit 0")
	repo := gitRepo(t)
	cmd := exec.Command(bin, "init", repo)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		writers, err := liveWriters(repo)
		if err != nil {
			t.Fatal(err)
		}
		if len(writers) > 0 {
			if !strings.Contains(writers[0], strconv.Itoa(cmd.Process.Pid)) {
				t.Fatalf("writers = %v, want pid %d", writers, cmd.Process.Pid)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the running fake indexer was not found")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if others, err := liveWriters(gitRepo(t)); err != nil || len(others) != 0 {
		t.Fatalf("another checkout sees writers %v (%v)", others, err)
	}
}

// statusFake answers init and index with exit 0 and status with the given index state (complete once an index
// run with REBUILD_OK set has marked the rebuild), logging each subcommand.
func statusFake(t *testing.T, state string) (string, string) {
	t.Helper()
	log := filepath.Join(t.TempDir(), "calls")
	body := `echo "$1" >> '` + log + `'
case "$1" in
  status)
    s=` + state + `
    if [ -f '` + log + `.rebuilt' ]; then s=complete; fi
    printf '{"initialized": true, "fileCount": 3, "nodeCount": 9, "edgeCount": 4, "pendingChanges": {"added": 0, "modified": 0}, "index": {"state": "%s"}}\n' "$s";;
  index)
    if [ -n "$REBUILD_OK" ]; then touch '` + log + `.rebuilt'; fi;;
esac`
	return fakeCodegraph(t, body), log
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	raw, _ := os.ReadFile(log)
	return strings.Fields(string(raw))
}

func TestInitCheckoutReadyOnlyWhenStatusConfirmsComplete(t *testing.T) {
	isolate(t)
	_, log := statusFake(t, "complete")
	record := InitCheckout(nil, t.TempDir(), gitRepo(t), "task", nil)
	if get(record, "state") != "ready" {
		t.Fatalf("state = %v, error = %v", get(record, "state"), get(record, "error"))
	}
	if got := strings.Join(calls(t, log), ","); got != "init,status" {
		t.Fatalf("calls = %s, want init,status", got)
	}
	if files := fmt.Sprint(get(asObject(get(record, "index")), "fileCount")); files != "3" {
		t.Fatalf("index fileCount = %v, want 3 from status", files)
	}
}

func TestInitCheckoutRebuildsAnIncompleteIndexOnce(t *testing.T) {
	isolate(t)
	_, log := statusFake(t, "indexing")
	t.Setenv("REBUILD_OK", "1")
	record := InitCheckout(nil, t.TempDir(), gitRepo(t), "task", nil)
	if get(record, "state") != "ready" {
		t.Fatalf("state = %v, error = %v", get(record, "state"), get(record, "error"))
	}
	if got := strings.Join(calls(t, log), ","); got != "init,status,index,status" {
		t.Fatalf("calls = %s, want init,status,index,status", got)
	}
}

func TestInitCheckoutNeverReadyFromAPartialIndex(t *testing.T) {
	isolate(t)
	_, log := statusFake(t, "indexing")
	record := InitCheckout(nil, t.TempDir(), gitRepo(t), "task", nil)
	if get(record, "state") != "failed" {
		t.Fatalf("state = %v, want failed", get(record, "state"))
	}
	if head := get(record, "indexed_head"); head != nil {
		t.Fatalf("indexed_head = %v on a failed build", head)
	}
	if errText := asString(get(lastAttempt(t, record), "error")); !strings.Contains(errText, "indexing") {
		t.Fatalf("attempt error = %q, want the unconfirmed index state", errText)
	}
	if got := strings.Join(calls(t, log), ","); got != "init,status,index,status" {
		t.Fatalf("calls = %s, want exactly one rebuild", got)
	}
}

func TestInitCheckoutUnreadableStatusIsNotReady(t *testing.T) {
	isolate(t)
	fakeCodegraph(t, "if [ \"$1\" = status ]; then echo 'not json'; fi")
	record := InitCheckout(nil, t.TempDir(), gitRepo(t), "task", nil)
	if get(record, "state") != "failed" {
		t.Fatalf("state = %v, want failed", get(record, "state"))
	}
}

func TestInitCheckoutThirdFailureExhausts(t *testing.T) {
	isolate(t)
	fakeCodegraph(t, "echo 'index broke' >&2; exit 3")
	repo := gitRepo(t)
	var record *ordjson.Object
	var states []string
	for i := 0; i < 3; i++ {
		record = InitCheckout(nil, t.TempDir(), repo, "task", record)
		states = append(states, asString(get(record, "state")))
	}
	if got := strings.Join(states, ","); got != "failed,failed,exhausted" {
		t.Fatalf("states = %s", got)
	}
}

func TestInitCheckoutUnavailableNeverExhausts(t *testing.T) {
	isolate(t)
	t.Setenv("SUM_CODEGRAPH_BIN", filepath.Join(t.TempDir(), "missing"))
	repo := gitRepo(t)
	var record *ordjson.Object
	for i := 0; i < 4; i++ {
		record = InitCheckout(nil, t.TempDir(), repo, "task", record)
	}
	if get(record, "state") != "unavailable" {
		t.Fatalf("state = %v", get(record, "state"))
	}
	list, _ := record.Get("attempts")
	if items, _ := list.([]any); len(items) != 0 {
		t.Fatalf("unavailable recorded %d attempts", len(items))
	}
}

func TestInitCheckoutBoundsRecordedDiagnostics(t *testing.T) {
	isolate(t)
	fakeCodegraph(t, "head -c 9000000 /dev/zero | tr '\\0' x; echo 'final: index broke' >&2; exit 3")
	record := InitCheckout(nil, t.TempDir(), gitRepo(t), "task", nil)
	errText := asString(get(lastAttempt(t, record), "error"))
	if len(errText) > 4200 || !strings.Contains(errText, "final: index broke") {
		t.Fatalf("attempt error is %d bytes (want <= ~4 KB) containing the stderr line: %.200q", len(errText), errText)
	}
	fakeCodegraph(t, "head -c 9000000 /dev/zero | tr '\\0' x; exit 3")
	record = InitCheckout(nil, t.TempDir(), gitRepo(t), "task", nil)
	if get(record, "state") != "failed" {
		t.Fatalf("state = %v", get(record, "state"))
	}
	if errText := asString(get(lastAttempt(t, record), "error")); len(errText) > 4200 {
		t.Fatalf("stdout-only attempt error is %d bytes", len(errText))
	}
}
