package cli

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// metaLab is a registered coordinator home beside the fake Herdr with one running task whose pane and workspace the
// fake knows, the shape every metadata scenario starts from.
type metaLab struct {
	t        *testing.T
	home     string
	fake     string
	task     string
	pane     string
	worktree string
}

func newMetaLab(t *testing.T) *metaLab {
	t.Helper()
	return newMetaLabAt(t, writeDesignatedHome(t))
}

func designatedHomeAt(t *testing.T, home string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func newMetaLabAt(t *testing.T, home string) *metaLab {
	t.Helper()
	herdrEnv(t, home)
	if out, err := runCLI(t, home, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	owner := readJSONFile(t, filepath.Join(home, "context.json"))
	// The coordinator pane runs where the owner record says; the fake reports that cwd for the parent pane.
	t.Setenv("FAKE_PARENT_CWD", owner["cwd"].(string))
	lab := &metaLab{t: t, home: home, fake: filepath.Join(home, "fake-herdr"), task: "t-aaaaaaaaaaaa", pane: "w-task:p1", worktree: t.TempDir()}
	lab.writeTask(map[string]any{
		"schema": 1, "id": lab.task, "status": "running", "repository": "owner/repo", "machine": owner["machine"],
		"session": "sum-test", "pane": lab.pane, "workspace": "w-task", "worktree": lab.worktree,
		"questions": []any{}, "evidence": []any{}, "attention": []any{},
	})
	state := lab.fakeState()
	panes := state["panes"].(map[string]any)
	panes[lab.pane] = map[string]any{"pane_id": lab.pane, "cwd": lab.worktree, "workspace_id": "w-task", "agent_status": "idle", "agent": "claude", "created": true, "terminal_id": "term-" + lab.pane}
	state["workspaces"].(map[string]any)["w-task"] = map[string]any{"workspace_id": "w-task", "label": "sum-task", "worktree": nil}
	lab.writeFakeState(state)
	return lab
}

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return out
}

func (l *metaLab) writeTask(task map[string]any) {
	l.t.Helper()
	dir := filepath.Join(l.home, "tasks", task["id"].(string))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		l.t.Fatal(err)
	}
	raw, _ := json.Marshal(task)
	if err := os.WriteFile(filepath.Join(dir, "task.json"), raw, 0o600); err != nil {
		l.t.Fatal(err)
	}
}

func (l *metaLab) fakeState() map[string]any {
	l.t.Helper()
	return readJSONFile(l.t, filepath.Join(l.fake, "state.json"))
}

func (l *metaLab) writeFakeState(state map[string]any) {
	l.t.Helper()
	raw, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(l.fake, "state.json"), raw, 0o600); err != nil {
		l.t.Fatal(err)
	}
}

func (l *metaLab) tokens(target string) map[string]any {
	l.t.Helper()
	table := "workspaces"
	if strings.Contains(target, ":") {
		table = "panes"
	}
	row, _ := l.fakeState()[table].(map[string]any)[target].(map[string]any)
	tokens, _ := row["tokens"].(map[string]any)
	return tokens
}

func (l *metaLab) sources(target string) map[string]any {
	l.t.Helper()
	table := "workspaces"
	if strings.Contains(target, ":") {
		table = "panes"
	}
	row, _ := l.fakeState()[table].(map[string]any)[target].(map[string]any)
	sources, _ := row["token_sources"].(map[string]any)
	return sources
}

// calls lists every fake Herdr argv so far; report lists only report-metadata calls.
func (l *metaLab) calls() [][]string {
	l.t.Helper()
	f, err := os.Open(filepath.Join(l.fake, "calls.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		l.t.Fatal(err)
	}
	defer f.Close()
	var out [][]string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var row struct {
			Args []string `json:"args"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			l.t.Fatal(err)
		}
		out = append(out, row.Args)
	}
	return out
}

func (l *metaLab) reportCalls() [][]string {
	var out [][]string
	for _, c := range l.calls() {
		if len(c) > 1 && c[1] == "report-metadata" {
			out = append(out, c)
		}
	}
	return out
}

func (l *metaLab) run(args ...string) map[string]any {
	l.t.Helper()
	out, err := runCLI(l.t, l.home, append([]string{"--format", "json"}, args...)...)
	if err != nil {
		l.t.Fatalf("%v: %v\n%s", args, err, out)
	}
	var view map[string]any
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		l.t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return view
}

func (l *metaLab) enable() map[string]any {
	l.t.Helper()
	return l.run("metadata", "enable")
}

func (l *metaLab) meta() map[string]any {
	l.t.Helper()
	return readJSONFile(l.t, filepath.Join(l.home, "metadata", "state.json"))
}

// snapshotTree is every regular file's path and content under home, excluding the fake Herdr's own store.
func snapshotTree(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if filepath.Base(path) == "fake-herdr" {
				return filepath.SkipDir
			}
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		out[path] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertTreeUnchanged(t *testing.T, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("file set changed: %d -> %d", len(before), len(after))
	}
	for path, content := range before {
		if after[path] != content {
			t.Fatalf("%s changed", path)
		}
	}
}

func TestMetadataEnable_probesAndWritesOnlyChangedTokensWithInstanceSource(t *testing.T) {
	lab := newMetaLab(t)
	view := lab.enable()
	source := view["source"].(string)
	if !strings.HasPrefix(source, "sum:") || strings.HasSuffix(source, filepath.Base(lab.home)) {
		t.Fatalf("source must derive from the instance id, not the home basename: %q", source)
	}
	caps := view["capabilities"].(map[string]any)
	if caps["pane_tokens"] != true || caps["workspace_tokens"] != true || caps["plugin_pane_open"] != true {
		t.Fatalf("capabilities from the probed schema: %v", caps)
	}
	if got := lab.tokens(lab.pane)["sum_state"]; got != "running" {
		t.Fatalf("pane tokens: %v", lab.tokens(lab.pane))
	}
	if got := lab.tokens("w-task")["sum_task"]; got != lab.task {
		t.Fatalf("workspace tokens: %v", lab.tokens("w-task"))
	}
	if got := lab.tokens("w-parent:p1")["sum_tasks"]; got != "1 active" {
		t.Fatalf("root tokens: %v", lab.tokens("w-parent:p1"))
	}
	for _, s := range lab.sources(lab.pane) {
		if s != source {
			t.Fatalf("token source %v != %v", s, source)
		}
	}
	first := len(lab.reportCalls())
	if first != 3 {
		t.Fatalf("enable wrote %d endpoints, want 3 (pane, workspace, coordinator)", first)
	}
	again := lab.run("metadata", "sync")
	if n := len(lab.reportCalls()); n != first {
		t.Fatalf("unchanged sync wrote %d more report-metadata calls", n-first)
	}
	for _, row := range again["tasks"].([]any) {
		for _, e := range row.(map[string]any)["endpoints"].([]any) {
			if e.(map[string]any)["outcome"] != "unchanged" {
				t.Fatalf("second pass endpoint: %v", e)
			}
		}
	}
	if again["forgotten"] == nil || len(again["forgotten"].([]any)) != 0 || again["herdr_calls"] == nil {
		t.Fatalf("sync output: %v", again)
	}
}

func TestMetadataEnable_withoutSchemaSupportFailsLocally(t *testing.T) {
	lab := newMetaLab(t)
	t.Setenv("FAKE_NO_METADATA", "1")
	before := readMetadataState(t, lab.home)
	out, err := runCLI(t, lab.home, "metadata", "enable")
	if err == nil {
		t.Fatalf("enable must fail without report-metadata support: %s", out)
	}
	if !strings.Contains(err.Error(), "PaneReportMetadataParams") {
		t.Fatalf("error must name the missing schema fields: %v", err)
	}
	assertMetadataStateUnchanged(t, lab.home, before)
	if len(lab.reportCalls()) != 0 {
		t.Fatal("nothing may be written without capability")
	}
	status := lab.run("metadata", "status")
	if status["enabled"] != false {
		t.Fatalf("status: %v", status)
	}
}

func TestMetadataDisabled_afterAndSyncWriteNothing(t *testing.T) {
	lab := newMetaLab(t)
	before := snapshotTree(t, lab.home)
	callsBefore := len(lab.calls())
	if _, err := runCLI(t, lab.home, "ask", lab.task, "--key", "k", "--text", "which?"); err != nil {
		t.Fatal(err)
	}
	after := snapshotTree(t, lab.home)
	// The ask changed its own task record and nothing else.
	for path := range after {
		if before[path] != after[path] && !strings.Contains(path, "/tasks/") {
			t.Fatalf("disabled projection wrote %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(lab.home, "metadata")); !os.IsNotExist(err) {
		t.Fatal("disabled projection must not create metadata state")
	}
	for _, c := range lab.calls()[callsBefore:] {
		if len(c) > 1 && c[1] == "report-metadata" || len(c) > 1 && c[0] == "api" {
			t.Fatalf("disabled projection called Herdr: %v", c)
		}
	}
	sync := lab.run("metadata", "sync")
	if sync["enabled"] != false || sync["skipped"] != true {
		t.Fatalf("disabled sync: %v", sync)
	}
	if _, err := os.Stat(filepath.Join(lab.home, "metadata")); !os.IsNotExist(err) {
		t.Fatal("disabled sync must not create metadata state")
	}
}

func TestMetadataAfter_projectsChangedFactsOnceAndReadsNever(t *testing.T) {
	lab := newMetaLab(t)
	lab.enable()
	base := len(lab.reportCalls())
	lab.run("ask", lab.task, "--key", "k", "--text", "which?")
	if got := lab.tokens(lab.pane)["sum_state"]; got != "needs-decision" {
		t.Fatalf("after ask: %v", lab.tokens(lab.pane))
	}
	if got := lab.tokens("w-parent:p1")["sum_inbox"]; got != "1 decision" {
		t.Fatalf("root after ask: %v", lab.tokens("w-parent:p1"))
	}
	written := len(lab.reportCalls()) - base
	if written != 3 {
		t.Fatalf("ask projected %d endpoints, want 3 (pane, workspace, coordinator)", written)
	}
	question := lab.run("show", lab.task)["questions"].([]any)[0].(map[string]any)["id"].(string)
	base = len(lab.reportCalls())
	lab.run("answer", lab.task, question, "--text", "left")
	if got := lab.tokens(lab.pane)["sum_state"]; got != "answer-pending" {
		t.Fatalf("after answer: %v", lab.tokens(lab.pane))
	}
	if written := len(lab.reportCalls()) - base; written != 3 {
		t.Fatalf("answer projected %d endpoints, want 3", written)
	}
	base = len(lab.calls())
	for _, read := range [][]string{{"status", "--grouped"}, {"inbox", "--compact"}, {"metadata", "inbox"}, {"metadata", "status"}, {"metadata", "inbox", "--grouped"}, {"show", lab.task}} {
		lab.run(read...)
	}
	if n := len(lab.calls()) - base; n != 0 {
		t.Fatalf("read commands called Herdr %d times: %v", n, lab.calls()[base:])
	}
	meta := lab.meta()
	resources := meta["resources"].(map[string]any)[lab.task].(map[string]any)
	if resources["state"] != "answer-pending" || resources["pane"].(map[string]any)["tokens"].(map[string]any)["sum_state"] != "answer-pending" {
		t.Fatalf("recorded resources: %v", resources)
	}
}

func TestMetadataAfter_refusedWriteIsNotCachedAndDomainSucceeds(t *testing.T) {
	lab := newMetaLab(t)
	lab.enable()
	refusing := filepath.Join(t.TempDir(), "herdr")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$REFUSE_LOG\"\nprintf '{\"error\":{\"code\":\"too_many_tokens\",\"message\":\"refused\"}}' >&2\nexit 1\n"
	if err := os.WriteFile(refusing, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "refused.log")
	t.Setenv("REFUSE_LOG", log)
	t.Setenv("SUM_HERDR_BIN", refusing)
	view := lab.run("ask", lab.task, "--key", "k", "--text", "which?")
	if view["id"] == nil && view["question"] == nil {
		t.Fatalf("ask must still succeed: %v", view)
	}
	task := readJSONFile(t, filepath.Join(lab.home, "tasks", lab.task, "task.json"))
	if len(task["questions"].([]any)) != 1 {
		t.Fatal("question record not saved")
	}
	if _, err := os.Stat(log); err != nil {
		t.Fatal("projection did not try Herdr")
	}
	meta := lab.meta()
	resources := meta["resources"].(map[string]any)[lab.task].(map[string]any)
	if got := resources["pane"].(map[string]any)["tokens"].(map[string]any)["sum_state"]; got != "running" {
		t.Fatalf("a refused write must not be cached as applied: %v", resources)
	}
	if meta["degraded"] == nil || meta["last_error"] == nil {
		t.Fatalf("refusal must be recorded: degraded=%v last_error=%v", meta["degraded"], meta["last_error"])
	}
	status := lab.run("metadata", "status")
	if status["degraded"] != true || len(status["errors_log"].([]any)) == 0 {
		t.Fatalf("status must show the failure: %v", status)
	}

	// Herdr accepts again: the next clean pass clears degraded while the error history stays.
	herdrEnv(t, lab.home)
	t.Setenv("FAKE_PARENT_CWD", readJSONFile(t, filepath.Join(lab.home, "context.json"))["cwd"].(string))
	lab.run("metadata", "sync")
	meta = lab.meta()
	if meta["degraded"] != nil || meta["last_error"] == nil || len(meta["errors"].([]any)) == 0 {
		t.Fatalf("a clean pass clears degraded and keeps history: degraded=%v last_error=%v", meta["degraded"], meta["last_error"])
	}
	status = lab.run("metadata", "status")
	if status["degraded"] != false || len(status["errors_log"].([]any)) == 0 {
		t.Fatalf("status after a clean pass: %v", status)
	}
	if got := lab.tokens(lab.pane)["sum_state"]; got != "needs-decision" {
		t.Fatalf("the clean pass projects the facts the refused pass could not: %v", lab.tokens(lab.pane))
	}
}

// hookEvent delivers one Herdr plugin event in-process, the way Herdr's plugin runner invokes `hook event`.
func (l *metaLab) hookEvent(pluginID, pane, status string) map[string]any {
	l.t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"event": "pane_agent_status_changed",
		"data":  map[string]any{"type": "pane_agent_status_changed", "pane_id": pane, "workspace_id": strings.Split(pane, ":")[0], "agent_status": status, "agent": "claude"},
	})
	l.t.Setenv("HERDR_PLUGIN_ID", pluginID)
	l.t.Setenv("HERDR_PLUGIN_EVENT", "pane.agent_status_changed")
	l.t.Setenv("HERDR_PLUGIN_EVENT_JSON", string(payload))
	return l.run("hook", "event")
}

func TestMetadataHookEvent_projectsHandledEventsOnlyAndNeverIgnoredOnes(t *testing.T) {
	lab := newMetaLab(t)
	lab.enable()
	pluginID := lab.run("hook", "enable")["plugin_id"].(string)
	before := lab.meta()["last_pass"].(map[string]any)
	base := len(lab.reportCalls())

	// A stranger's pane: the event is ignored and no projection runs.
	ignored := lab.hookEvent(pluginID, "w-stranger:p7", "idle")
	if ignored["outcome"] != "ignored" {
		t.Fatalf("stranger event: %v", ignored)
	}
	if n := len(lab.reportCalls()) - base; n != 0 {
		t.Fatalf("an ignored event made %d report-metadata calls", n)
	}
	if last := lab.meta()["last_pass"].(map[string]any); last["at"] != before["at"] || last["reason"] != before["reason"] {
		t.Fatalf("an ignored event must not run a pass: %v -> %v", before, last)
	}

	// The worker's pane goes idle: the event is handled and one projection follows without `metadata sync`.
	handled := lab.hookEvent(pluginID, lab.pane, "idle")
	if handled["outcome"] != "handled" && handled["outcome"] != "reconciled" {
		t.Fatalf("worker event: %v", handled)
	}
	last := lab.meta()["last_pass"].(map[string]any)
	if last["reason"] != "hook event" {
		t.Fatalf("a handled event must run one projection: %v", last)
	}
	if got := lab.tokens(lab.pane)["sum_task"]; got != lab.task {
		t.Fatalf("worker pane tokens after the event: %v", lab.tokens(lab.pane))
	}
}

func TestMetadataPaneVerification_staleAbsentUnobservable(t *testing.T) {
	lab := newMetaLab(t)
	lab.enable()
	state := lab.fakeState()
	pane := state["panes"].(map[string]any)[lab.pane].(map[string]any)
	pane["cwd"] = "/somewhere/else"
	pane["tokens"].(map[string]any)["other"] = "keep"
	pane["token_sources"].(map[string]any)["other"] = "user:jj"
	lab.writeFakeState(state)
	lab.run("ask", lab.task, "--key", "k", "--text", "which?")
	if got := lab.tokens(lab.pane); len(got) != 1 || got["other"] != "keep" {
		t.Fatalf("stale pane must lose only sum's keys and gain nothing: %v", got)
	}
	if got := lab.tokens("w-task")["sum_state"]; got != "needs-decision" {
		t.Fatalf("workspace still belongs to the task: %v", lab.tokens("w-task"))
	}
	sync := lab.run("metadata", "sync")
	paneRow := endpointRow(t, sync, lab.task, "pane")
	if paneRow["outcome"] != "unchanged" && paneRow["outcome"] != "stale" {
		t.Fatalf("pane row: %v", paneRow)
	}
	if _, has := lab.meta()["resources"].(map[string]any)[lab.task].(map[string]any)["pane"]; has {
		t.Fatal("a stale pane must not stay recorded as written")
	}

	// Herdr cannot answer at all: nothing is written or cleared, the record survives for a later pass.
	lab.writeTask(map[string]any{
		"schema": 1, "id": "t-bbbbbbbbbbbb", "status": "running", "repository": "owner/repo", "session": "sum-test",
		"pane": "w-b:p1", "worktree": lab.worktree, "questions": []any{}, "evidence": []any{}, "attention": []any{},
	})
	state = lab.fakeState()
	state["panes"].(map[string]any)["w-b:p1"] = map[string]any{"pane_id": "w-b:p1", "cwd": lab.worktree, "workspace_id": "w-b", "agent_status": "idle", "agent": nil, "created": true, "terminal_id": "term-b"}
	lab.writeFakeState(state)
	lab.run("metadata", "sync")
	if lab.tokens("w-b:p1")["sum_task"] != "t-bbbbbbbbbbbb" {
		t.Fatalf("second task pane: %v", lab.tokens("w-b:p1"))
	}
	broken := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\necho 'connection refused' >&2\nexit 3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_HERDR_BIN", broken)
	lab.run("ask", "t-bbbbbbbbbbbb", "--key", "k", "--text", "which?")
	herdrEnv(t, lab.home)
	t.Setenv("FAKE_PARENT_CWD", readJSONFile(t, filepath.Join(lab.home, "context.json"))["cwd"].(string))
	if lab.tokens("w-b:p1")["sum_state"] != "running" {
		t.Fatalf("unobservable pane must keep its tokens untouched: %v", lab.tokens("w-b:p1"))
	}
	rec := lab.meta()["resources"].(map[string]any)["t-bbbbbbbbbbbb"].(map[string]any)
	if rec["pane"] == nil {
		t.Fatalf("unobservable pane record must survive: %v", rec)
	}
	// The pane closes: the record is dropped without any write.
	state = lab.fakeState()
	delete(state["panes"].(map[string]any), "w-b:p1")
	lab.writeFakeState(state)
	base := len(lab.reportCalls())
	sync = lab.run("metadata", "sync")
	if row := endpointRow(t, sync, "t-bbbbbbbbbbbb", "pane"); row["outcome"] != "absent" {
		t.Fatalf("absent pane: %v", row)
	}
	for _, c := range lab.reportCalls()[base:] {
		if c[2] == "w-b:p1" {
			t.Fatalf("absent pane received a write: %v", c)
		}
	}
	if rec := lab.meta()["resources"].(map[string]any)["t-bbbbbbbbbbbb"].(map[string]any); rec["pane"] != nil {
		t.Fatalf("absent pane must be forgotten: %v", rec)
	}
}

func endpointRow(t *testing.T, sync map[string]any, task, kind string) map[string]any {
	t.Helper()
	for _, row := range sync["tasks"].([]any) {
		r := row.(map[string]any)
		if r["task"] != task {
			continue
		}
		for _, e := range r["endpoints"].([]any) {
			if e.(map[string]any)["kind"] == kind {
				return e.(map[string]any)
			}
		}
	}
	t.Fatalf("no %s row for %s in %v", kind, task, sync)
	return nil
}

func TestMetadataTwoHomes_distinctSourcesAndOwnKeysOnly(t *testing.T) {
	// Both homes end in ".sum": a basename-derived source would collide and let one home clear the other's keys.
	lab := newMetaLabAt(t, designatedHomeAt(t, filepath.Join(t.TempDir(), "a.sum")))
	first := lab.enable()["source"].(string)
	other := designatedHomeAt(t, filepath.Join(t.TempDir(), "b.sum"))
	t.Setenv("HERDR_PANE_ID", "w-other:p1")
	if out, err := runCLI(t, other, "init"); err != nil {
		t.Fatalf("other init: %v\n%s", err, out)
	}
	out, err := runCLI(t, other, "--format", "json", "metadata", "enable")
	if err != nil {
		t.Fatalf("other enable: %v\n%s", err, out)
	}
	var enabled map[string]any
	if err := json.Unmarshal([]byte(out), &enabled); err != nil {
		t.Fatal(err)
	}
	second := enabled["source"].(string)
	if first == second || !strings.HasPrefix(second, "sum:") {
		t.Fatalf("sources must differ: %q %q", first, second)
	}
	if lab.tokens("w-other:p1")["sum_tasks"] != "0 active" || lab.sources("w-other:p1")["sum_tasks"] != second {
		t.Fatalf("other coordinator pane: %v %v", lab.tokens("w-other:p1"), lab.sources("w-other:p1"))
	}
	if lab.sources(lab.pane)["sum_task"] != first || lab.sources("w-parent:p1")["sum_tasks"] != first {
		t.Fatalf("first home's keys carry its source: %v", lab.sources(lab.pane))
	}
	if _, err := runCLI(t, other, "metadata", "disable"); err != nil {
		t.Fatal(err)
	}
	if len(lab.tokens("w-other:p1")) != 0 {
		t.Fatalf("other home cleared its own pane: %v", lab.tokens("w-other:p1"))
	}
	if lab.tokens(lab.pane)["sum_task"] != lab.task || lab.tokens("w-parent:p1")["sum_tasks"] != "1 active" {
		t.Fatalf("the other home's disable must not touch this home's keys: %v %v", lab.tokens(lab.pane), lab.tokens("w-parent:p1"))
	}
}

func TestMetadataLegacySourceIsAmbiguousAndUntouched(t *testing.T) {
	lab := newMetaLab(t)
	state := lab.fakeState()
	pane := state["panes"].(map[string]any)[lab.pane].(map[string]any)
	pane["tokens"] = map[string]any{"sum_state": "old value", "jj_status": "2 changes"}
	pane["token_sources"] = map[string]any{"sum_state": "sum:.sum", "jj_status": "user:jj"}
	lab.writeFakeState(state)
	view := lab.enable()
	sync := view["sync"].(map[string]any)
	ambiguous := sync["ambiguous"].([]any)
	if len(ambiguous) != 1 || ambiguous[0].(map[string]any)["source"] != "sum:.sum" || ambiguous[0].(map[string]any)["key"] != "sum_state" {
		t.Fatalf("ambiguous: %v", ambiguous)
	}
	tokens := lab.tokens(lab.pane)
	if tokens["sum_state"] != "old value" || tokens["jj_status"] != "2 changes" || tokens["sum_task"] != lab.task {
		t.Fatalf("legacy and foreign keys stay, own keys arrive: %v", tokens)
	}
	status := lab.run("metadata", "status")
	if len(status["ambiguous"].([]any)) != 1 {
		t.Fatalf("status ambiguous: %v", status["ambiguous"])
	}
	cleared := lab.run("metadata", "disable")
	tokens = lab.tokens(lab.pane)
	if tokens["sum_state"] != "old value" || tokens["jj_status"] != "2 changes" || tokens["sum_task"] != nil {
		t.Fatalf("disable clears only proven owned keys: %v (%v)", tokens, cleared)
	}
}

func TestMetadataDisable_clearsRecordedKeysAndReportsFailedClears(t *testing.T) {
	lab := newMetaLab(t)
	lab.enable()
	state := lab.fakeState()
	pane := state["panes"].(map[string]any)[lab.pane].(map[string]any)
	pane["tokens"].(map[string]any)["other"] = "keep"
	pane["token_sources"].(map[string]any)["other"] = "user:jj"
	delete(state["workspaces"].(map[string]any), "w-task")
	lab.writeFakeState(state)
	base := len(lab.reportCalls())
	view := lab.run("metadata", "disable")
	if view["enabled"] != false {
		t.Fatalf("disable: %v", view)
	}
	rows := view["cleared"].([]any)
	if len(rows) != 3 {
		t.Fatalf("one clear per recorded endpoint: %v", rows)
	}
	if n := len(lab.reportCalls()) - base; n != 3 {
		t.Fatalf("%d clear calls", n)
	}
	if got := lab.tokens(lab.pane); len(got) != 1 || got["other"] != "keep" {
		t.Fatalf("pane after disable: %v", got)
	}
	if got := lab.tokens("w-parent:p1"); len(got) != 0 {
		t.Fatalf("root after disable: %v", got)
	}
	for _, c := range lab.reportCalls()[base:] {
		for _, a := range c {
			if strings.HasPrefix(a, "--token") {
				t.Fatalf("disable must only clear: %v", c)
			}
		}
	}
	var absentRow map[string]any
	for _, r := range rows {
		if r.(map[string]any)["kind"] == "workspace" {
			absentRow = r.(map[string]any)
		}
	}
	if absentRow["absent"] == nil {
		t.Fatalf("absent workspace clear must be reported as such: %v", absentRow)
	}
	if res := lab.meta()["resources"].(map[string]any); len(res) != 0 {
		t.Fatalf("resources after clean disable: %v", res)
	}

	// A refused clear stays recorded and diagnostic.
	lab.enable()
	// Every report-metadata call is refused; observations still reach the real fake so the coordinator verifies.
	refusing := filepath.Join(t.TempDir(), "herdr")
	script := "#!/bin/sh\ncase \"$*\" in *report-metadata*) printf '{\"error\":{\"code\":\"internal\",\"message\":\"nope\"}}' >&2; exit 1;; esac\nexec \"$REAL_HERDR\" \"$@\"\n"
	if err := os.WriteFile(refusing, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REAL_HERDR", os.Getenv("SUM_HERDR_BIN"))
	t.Setenv("SUM_HERDR_BIN", refusing)
	view = lab.run("metadata", "disable")
	refused := 0
	for _, r := range view["cleared"].([]any) {
		if r.(map[string]any)["refused"] != nil {
			refused++
		}
	}
	if refused != 2 || view["enabled"] != false {
		t.Fatalf("refused clears: %v", view)
	}
	meta := lab.meta()
	if len(meta["resources"].(map[string]any)) != 1 || meta["root"] == nil {
		t.Fatalf("failed clears must stay recorded for a later retry: %v", meta)
	}
}

func TestMetadataInboxOpen_fallsBackWithoutHookAndOpensWithIt(t *testing.T) {
	lab := newMetaLab(t)
	fallback := lab.run("metadata", "inbox", "--open")
	open := fallback["open"].(map[string]any)
	if open["outcome"] != "fallback" || !strings.Contains(open["reason"].(string), "hook enable") {
		t.Fatalf("fallback: %v", open)
	}
	if fallback["groups"] == nil || fallback["counts"] == nil {
		t.Fatalf("fallback must carry the grouped overview: %v", keys(fallback))
	}
	lab.run("hook", "enable")
	manifest, err := os.ReadFile(filepath.Join(lab.home, "hook", "plugin", "herdr-plugin.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), `"inbox", "--grouped", "--view"]`) || strings.Contains(string(manifest), "press Enter") || !strings.Contains(string(manifest), `placement = "popup"`) {
		t.Fatalf("manifest must declare the grouped view entrypoint:\n%s", manifest)
	}
	if hookStatus := lab.run("hook", "status"); hookStatus["expected_manifest_current"] != true {
		t.Fatalf("hook status: %v", hookStatus["expected_manifest_current"])
	}
	before := snapshotTree(t, lab.home)
	stillNoCapability := lab.run("metadata", "inbox", "--open")
	if o := stillNoCapability["open"].(map[string]any); o["outcome"] != "fallback" || !strings.Contains(o["reason"].(string), "metadata enable") {
		t.Fatalf("no saved capability: %v", o)
	}
	lab.enable()
	before = snapshotTree(t, lab.home)
	callsBefore := len(lab.calls())
	popup := lab.run("metadata", "inbox", "--open")
	o := popup["open"].(map[string]any)
	if o["outcome"] != "opened" || o["pane"] != nil || o["placement"] != "popup" || o["entrypoint"] != "inbox" {
		t.Fatalf("popup open: %v", o)
	}
	split := lab.run("metadata", "inbox", "--open", "--placement", "split")
	o = split["open"].(map[string]any)
	if o["outcome"] != "opened" || o["pane"] == nil || !strings.HasPrefix(o["pane"].(string), "w-parent:") {
		t.Fatalf("split open: %v", o)
	}
	var opens [][]string
	for _, c := range lab.calls()[callsBefore:] {
		if len(c) > 2 && c[0] == "plugin" && c[1] == "pane" {
			opens = append(opens, c)
		} else {
			t.Fatalf("open made another Herdr call: %v", c)
		}
	}
	if len(opens) != 2 || contains(opens[0], "--placement") || !contains(opens[1], "--target-pane") || !contains(opens[1], "--no-focus") || !contains(opens[1], "--entrypoint") {
		t.Fatalf("open calls: %v", opens)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, lab.home))
	if _, err := runCLI(t, lab.home, "metadata", "inbox", "--open", "--placement", "sideways"); err == nil {
		t.Fatal("invalid placement must be a usage error")
	}
	status := lab.run("metadata", "status")
	if status["native_open"].(map[string]any)["available"] != true {
		t.Fatalf("native_open: %v", status["native_open"])
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}
