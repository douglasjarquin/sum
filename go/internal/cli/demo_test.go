package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hctest"
)

func TestOfflineDemo(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "demo", map[string]string{"README.md": "A disposable demo project.\n"})
	git := func(cwd string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", cwd}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	mainSHA := git(repo, "rev-parse", "HEAD")
	coordinator := nativeCoordinatorTerminal(t, d)
	stranger := coordinator
	stranger.Address = "t9"
	stranger.CreatedMS = 9
	pid := uint32(300000)
	stranger.PID = &pid
	foreground := *stranger.Foreground
	foreground.PID = 300001
	stranger.Foreground = &foreground
	d.native.SetTerminal(stranger)
	if view := d.ctlPane("t9", true, "init"); view["role"] != "developer" {
		t.Fatalf("unregistered native terminal inherited authority: %v", view)
	}
	beforeOwner := readFileString(t, filepath.Join(d.home, "context.json"))
	if view := d.ctlPane("t9", false, "init", "--role", "coordinator"); asString(view["error"]) == "" {
		t.Fatalf("second live terminal took over: %v", view)
	}
	if readFileString(t, filepath.Join(d.home, "context.json")) != beforeOwner {
		t.Fatal("failed takeover rewrote owner")
	}
	codegraphCalls := filepath.Join(d.base, "fake-codegraph", "calls.jsonl")
	beforeCalls := countLines(t, codegraphCalls)
	brief := filepath.Join(d.base, "brief.md")
	if err := os.WriteFile(brief, []byte("Add greeting.py returning Hello, <name>! Ask before changing punctuation.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", brief, "--approved")
	id, address, checkout := asString(task["id"]), workerAddress(task), asString(task["worktree"])
	if countLines(t, codegraphCalls) != beforeCalls {
		t.Fatal("dispatch indexed a checkout without request")
	}
	if asString(asMap(asMap(task["launch"])["source"])["harness"]) != "root" {
		t.Fatalf("coordinator harness resolution: %v", task["launch"])
	}
	if launch := asMap(task["launch"]); launch["model"] != nil || asString(asMap(launch["source"])["model"]) != "native-default" {
		t.Fatalf("native default model provenance: %v", launch)
	}
	if value := d.ctl(false, "prepare", "--repo", repo, "--brief", brief, "--approved", "--preset", "deep"); !strings.Contains(asString(value["error"]), "Unknown preset 'deep'") {
		t.Fatalf("unknown preset accepted: %v", value)
	}
	d.ctl(true, "preset", "set", "deep", "--harness", "codex", "--model", "gpt-5-codex", "--reasoning", "high")
	d.ctl(true, "settings", "set", "--global", "2", "--per-repository", "1")
	if refused := d.ctl(false, "prepare", "--repo", repo, "--brief", brief, "--approved"); !strings.Contains(asString(refused["error"]), "1 of 1 slots for") {
		t.Fatalf("capacity admission: %v", refused)
	}
	if git(checkout, "status", "--porcelain", "--untracked-files=all") != "" {
		t.Fatal("new task checkout is dirty")
	}
	if _, err := os.Stat(filepath.Join(checkout, ".codegraph")); !os.IsNotExist(err) {
		t.Fatalf("unsolicited codegraph: %v", err)
	}
	body, err := os.ReadFile(asString(task["brief_path"]))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "not built") || !strings.Contains(string(body), "graph init "+id) {
		t.Fatal("brief did not expose unbuilt graph and explicit action")
	}
	if worker := d.ctlPane(address, true, "init"); worker["role"] != "worker" || worker["task"] != id {
		t.Fatalf("native worker receipt: %v", worker)
	}
	startNativeObserver(t, d)
	d.native.SetStatus("t1", "working")
	first := d.ctlPane(address, true, "ask", id, "--key", "punctuation", "--text", "Keep the exclamation mark?")
	second := d.ctlPane(address, true, "ask", id, "--key", "second", "--text", "Make another change?")
	firstID, secondID := asString(asMap(first["question"])["id"]), asString(asMap(second["question"])["id"])
	if firstID == "" || secondID == "" || firstID == secondID {
		t.Fatal("two durable decisions were not preserved")
	}
	d.native.SetStatus("t1", "done")
	waitNativeCondition(t, func() bool { return len(d.native.Inputs("t1")) > 0 })
	shown := d.ctl(true, "show", id)
	if len(asSlice(shown["questions"])) != 2 {
		t.Fatal("native wake closed decisions without answers")
	}
	for _, decision := range []struct{ id, text string }{{secondID, "No second change."}, {firstID, "Yes, keep it."}} {
		d.ctl(true, "answer", id, decision.id, "--text", decision.text)
		d.ctlPane(address, true, "resolve", id, decision.id)
	}
	if err = os.WriteFile(filepath.Join(checkout, "greeting.py"), []byte("def greet(name):\n    return f'Hello, {name}!'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git(checkout, "add", "greeting.py")
	git(checkout, "commit", "-m", "Add greeting")
	candidate := git(checkout, "rev-parse", "HEAD")
	handoff := filepath.Join(d.base, "handoff.json")
	if err = os.WriteFile(handoff, []byte(`{"outcome":"completed","candidate":"`+candidate+`","files":["greeting.py"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	d.workerReport(true, id, "--text", "Scripted worker added greeting.py.", "--handoff", handoff)
	d.independentReview(true, id, "--verdict", "comment", "--candidate", candidate, "--text", "Independent reviewer: greeting lacks a docstring; not blocking.")
	d.ctl(true, "verify", id, "--candidate", candidate, "--result", "pass", "--text", "Coordinator re-ran the assertion in the task checkout.")
	if git(repo, "rev-parse", "HEAD") != mainSHA {
		t.Fatal("primary checkout moved")
	}
	if _, err = os.Stat(filepath.Join(repo, "greeting.py")); !os.IsNotExist(err) {
		t.Fatalf("primary checkout gained worker changes: %v", err)
	}
	t.Log("native offline demo proved explicit roles, isolated dispatch, capacity, real observer startup, durable decisions, worker report, independent review and coordinator verification")
}

// countLines counts newline-terminated records in a fake's call log; a missing log counts zero.
func countLines(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(raw), "\n")
}

type demoLab struct {
	t            *testing.T
	root, helper string
	home, base   string
	env          []string
	native       *hctest.Fixture
	config       string
}

func demoEnv(t *testing.T, root, base string) []string {
	t.Helper()
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "SUM_") || strings.HasPrefix(e, "HERDR_") || strings.HasPrefix(e, "HC_") || strings.HasPrefix(e, "PATH=") {
			continue
		}
		env = append(env, e)
	}
	path := filepath.Dir(pinnedFixturePython(t)) + string(os.PathListSeparator) + os.Getenv("PATH")
	if goroot := os.Getenv("GOROOT"); goroot != "" {
		path = filepath.Join(goroot, "bin") + string(os.PathListSeparator) + path
	}
	env = append(env, "PATH="+path)
	env = append(env,
		"SUM_STAGE_OFFLINE=1",
		"SUM_GH_BIN="+filepath.Join(root, "tests/fixtures/gh.py"),
		"FAKE_GH_ROOT="+filepath.Join(base, "fake-gh"),
		"SUM_CODEGRAPH_BIN="+filepath.Join(root, "tests/fixtures/codegraph.py"),
		"FAKE_CODEGRAPH_ROOT="+filepath.Join(base, "fake-codegraph"),
		"SUM_MISE_BIN="+filepath.Join(root, "tests/fixtures/mise.py"),
		"FAKE_MISE_STOP="+base,
		"SUM_LSOF_BIN="+filepath.Join(root, "tests/fixtures/lsof.py"),
		"FAKE_LSOF_ROOT="+filepath.Join(base, "fake-lsof"),
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
		env = append([]string{}, d.env...)
		if d.native != nil {
			for key, value := range d.callerEnv(pane, args) {
				env = append(env, key+"="+value)
			}
		} else {
			env = append(env, "HC_TERMINAL_ID="+pane)
		}
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
