package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineDemo(t *testing.T) {
	root, helper := repoReference(t)
	base := t.TempDir()
	repo := filepath.Join(base, "project")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(cwd string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(repo, "init", "-b", "main")
	git(repo, "config", "user.name", "sum demo")
	git(repo, "config", "user.email", "demo@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("A disposable demo project.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(repo, "add", ".")
	git(repo, "commit", "-m", "Initial fixture")
	mainSHA := git(repo, "rev-parse", "HEAD")
	brief := filepath.Join(base, "brief.md")
	if err := os.WriteFile(brief, []byte("Add greeting.py with greet(name) returning 'Hello, <name>!' and verify it. Ask whether to preserve punctuation. Do not publish."), 0o644); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "state")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte("{\"schema\": 1, \"sum_version\": \"0.1.0\", \"created_at\": \"2026-09-05T00:00:00+00:00\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &demoLab{t: t, root: root, helper: helper, home: home, base: base, env: demoEnv(t, root, base)}
	d.ctl(false, "doctor")
	if _, err := os.Stat(filepath.Join(home, "context.json")); err == nil {
		t.Fatal("doctor must not bind")
	}
	if asString(d.ctl(true, "init")["role"]) != "coordinator" {
		t.Fatal("first init")
	}
	if asString(d.ctl(true, "init")["role"]) != "coordinator" {
		t.Fatal("repeat init")
	}
	second := d.ctlPane("w-second:p1", true, "init")
	if asString(second["role"]) != "developer" {
		t.Fatalf("second role %v", second["role"])
	}
	owned := d.ctlPane("w-second:p1", false, "init", "--role", "coordinator")
	if !strings.HasPrefix(asString(owned["error"]), "Coordinator is owned by pane w-parent:p1") {
		t.Fatalf("owned %v", owned)
	}
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", brief, "--approved")
	if asString(asMap(asMap(task["launch"])["source"])["harness"]) != "root" {
		t.Fatalf("launch %v", task["launch"])
	}
	if !strings.Contains(asString(task["confirmation"]), "model native default") {
		t.Fatalf("confirmation %v", task["confirmation"])
	}
	taskID := asString(task["id"])
	if "Unknown preset 'deep'" != "" {
		err := asString(d.ctl(false, "prepare", "--repo", repo, "--brief", brief, "--approved", "--preset", "deep")["error"])
		if !strings.Contains(err, "Unknown preset 'deep'") {
			t.Fatalf("preset %q", err)
		}
	}
	d.ctl(true, "preset", "set", "deep", "--harness", "codex", "--model", "gpt-5-codex", "--reasoning", "high")
	d.ctl(true, "settings", "set", "--global", "2", "--per-repository", "1")
	refused := d.ctl(false, "prepare", "--repo", repo, "--brief", brief, "--harness", "codex", "--approved")
	if !strings.Contains(asString(refused["error"]), "1 of 1 slots for") {
		t.Fatalf("admission %v", refused)
	}
	if asString(asMap(task["graph"])["state"]) != "ready" {
		t.Fatalf("graph %v", task["graph"])
	}
	worktree := asString(task["worktree"])
	if git(worktree, "status", "--porcelain", "--untracked-files=all") != "" {
		t.Fatal("dirty worktree")
	}
	worker := d.ctlPane(asString(task["pane"]), true, "init")
	if asString(worker["role"]) != "worker" || asString(worker["task"]) != taskID {
		t.Fatalf("worker %v", worker)
	}
	hook := d.ctl(true, "hook", "enable")
	pluginID := asString(hook["plugin_id"])
	d.ctl(true, "ask", taskID, "--key", "punctuation", "--text", "Keep the exclamation mark?")
	d.ctl(true, "ask", taskID, "--key", "second", "--text", "Second question while busy?")
	ignored := d.herdrEvent(pluginID, "w-stranger:p7", "idle", "sum-test")
	if asString(ignored["outcome"]) != "ignored" {
		t.Fatalf("stranger %v", ignored)
	}
	d.setEnv("FAKE_PARENT_STATUS", "idle")
	edge := d.herdrEvent(pluginID, "w-parent:p1", "idle", "sum-test")
	if asString(edge["outcome"]) != "handled" && asString(edge["outcome"]) != "reconciled" && edge["prompts"] == nil {
		t.Fatalf("idle edge %v", edge)
	}
	d.setEnv("FAKE_PARENT_STATUS", "working")
	q := asMap(d.ctl(true, "inbox")["tasks"])
	if q == nil && len(asSlice(d.ctl(true, "inbox")["tasks"])) == 0 {
		t.Fatal("inbox empty")
	}
	shown := d.ctl(true, "show", taskID)
	var ids []string
	for _, item := range asSlice(shown["questions"]) {
		row := asMap(item)
		if asString(row["id"]) != "" {
			ids = append(ids, asString(row["id"]))
		}
	}
	if len(ids) < 2 {
		t.Fatalf("want two questions, got %v from %v", ids, shown["questions"])
	}
	qid, secondID := ids[0], ids[1]
	d.ctl(true, "answer", taskID, secondID, "--text", "No second change.")
	d.ctl(true, "resolve", taskID, secondID)
	d.ctl(true, "answer", taskID, qid, "--text", "Yes, keep it.")
	d.ctl(true, "resolve", taskID, qid)
	if err := os.WriteFile(filepath.Join(worktree, "greeting.py"), []byte("def greet(name):\n    return f\"Hello, {name}!\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(worktree, "add", "greeting.py")
	git(worktree, "commit", "-m", "Add greeting")
	candidate := git(worktree, "rev-parse", "HEAD")
	handoff := filepath.Join(base, "handoff.json")
	if err := os.WriteFile(handoff, []byte(`{"outcome":"completed","candidate":"`+candidate+`","files":["greeting.py"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	d.ctl(true, "report", taskID, "--text", "Scripted worker added greeting.py. Candidate "+candidate+".", "--handoff", handoff)
	d.ctlPane("w-review:p1", true, "review", taskID, "--verdict", "comment", "--candidate", candidate, "--text", "Reviewer: greeting lacks a docstring; not blocking.")
	d.ctl(true, "verify", taskID, "--candidate", candidate, "--result", "pass", "--text", "Coordinator re-ran the assertion in the task checkout.")
	if git(repo, "rev-parse", "HEAD") != mainSHA {
		t.Fatal("primary checkout moved")
	}
	if _, err := os.Stat(filepath.Join(repo, "greeting.py")); err == nil {
		t.Fatal("primary gained greeting.py")
	}
	_ = io.Discard
	t.Log("offline demo covered init, dispatch, remainder-free quota path skipped, hook event, ask/answer, report, review, verify")
}

type demoLab struct {
	t            *testing.T
	root, helper string
	home, base   string
	env          []string
}

func demoEnv(t *testing.T, root, base string) []string {
	t.Helper()
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "SUM_") || strings.HasPrefix(e, "HERDR_") {
			continue
		}
		env = append(env, e)
	}
	if goroot := os.Getenv("GOROOT"); goroot != "" {
		env = append(env, "PATH="+filepath.Join(goroot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	env = append(env,
		"SUM_STAGE_OFFLINE=1",
		"SUM_HERDR_BIN="+filepath.Join(root, "tests/fixtures/herdr.py"),
		"SUM_GH_BIN="+filepath.Join(root, "tests/fixtures/gh.py"),
		"FAKE_GH_ROOT="+filepath.Join(base, "fake-gh"),
		"SUM_CODEGRAPH_BIN="+filepath.Join(root, "tests/fixtures/codegraph.py"),
		"FAKE_CODEGRAPH_ROOT="+filepath.Join(base, "fake-codegraph"),
		"SUM_MISE_BIN="+filepath.Join(root, "tests/fixtures/mise.py"),
		"FAKE_MISE_STOP="+base,
		"SUM_LSOF_BIN="+filepath.Join(root, "tests/fixtures/lsof.py"),
		"FAKE_LSOF_ROOT="+filepath.Join(base, "fake-lsof"),
		"FAKE_HERDR_ROOT="+filepath.Join(base, "fake"),
		"FAKE_PARENT_CWD="+root,
		"HERDR_ENV=1",
		"HERDR_PANE_ID=w-parent:p1",
		"HERDR_SESSION=sum-test",
		"FAKE_PARENT_STATUS=working",
	)
	return env
}

func (d *demoLab) setEnv(key, value string) {
	prefix := key + "="
	for i, e := range d.env {
		if strings.HasPrefix(e, prefix) {
			d.env[i] = prefix + value
			return
		}
	}
	d.env = append(d.env, prefix+value)
}

func (d *demoLab) ctl(check bool, args ...string) map[string]any {
	return d.ctlPane("", check, args...)
}

func (d *demoLab) ctlPane(pane string, check bool, args ...string) map[string]any {
	d.t.Helper()
	cmd := exec.Command(d.helper, append([]string{"--format", "json", "--home", d.home}, args...)...)
	env := d.env
	if pane != "" {
		env = append(append([]string{}, d.env...), "HERDR_PANE_ID="+pane)
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if check && err != nil {
		d.t.Fatalf("sumctl %v: %v\n%s%s", args, err, stdout.String(), stderr.String())
	}
	raw := stdout.Bytes()
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = stderr.Bytes()
	}
	var out map[string]any
	if unmarshalErr := json.Unmarshal(raw, &out); unmarshalErr != nil {
		if check {
			d.t.Fatalf("json %v: %v\n%s", args, unmarshalErr, raw)
		}
		return map[string]any{"error": strings.TrimSpace(stderr.String() + stdout.String())}
	}
	return out
}

func (d *demoLab) herdrEvent(pluginID, pane, status, session string) map[string]any {
	d.t.Helper()
	ws := strings.Split(pane, ":")[0]
	payload, _ := json.Marshal(map[string]any{
		"event": "pane_agent_status_changed",
		"data": map[string]any{
			"type": "pane_agent_status_changed", "pane_id": pane, "workspace_id": ws, "agent_status": status, "agent": "claude",
		},
	})
	cmd := exec.Command(d.helper, "--format", "json", "--home", d.home, "hook", "event")
	cmd.Env = append(append([]string{}, d.env...),
		"HERDR_PLUGIN_ID="+pluginID,
		"HERDR_PLUGIN_EVENT=pane.agent_status_changed",
		"HERDR_PLUGIN_EVENT_JSON="+string(payload),
		"HERDR_SESSION="+session,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		d.t.Fatalf("hook event: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		d.t.Fatalf("hook event json: %v\n%s", err, stdout.String())
	}
	return out
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
