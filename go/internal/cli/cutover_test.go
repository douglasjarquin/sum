package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func runCLI(t *testing.T, home string, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &stdout, &stderr)
	root.SetArgs(append([]string{"--home", home}, args...))
	err := root.ExecuteContext(context.Background())
	if err != nil {
		return stderr.String(), err
	}
	return stdout.String(), nil
}

func writeDesignatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func herdrEnv(t *testing.T, home string) {
	t.Helper()
	root := repoRoot(t)
	fake := filepath.Join(root, "tests", "fixtures", "herdr.py")
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w-parent:p1")
	t.Setenv("HERDR_SESSION", "sum-test")
	t.Setenv("SUM_SESSION", "sum-test")
	t.Setenv("SUM_HERDR_BIN", fake)
	t.Setenv("FAKE_HERDR_ROOT", filepath.Join(home, "fake-herdr"))
	t.Setenv("FAKE_SESSION", "sum-test")
	t.Setenv("FAKE_PARENT_CWD", home)
	t.Setenv("FAKE_PARENT_STATUS", "idle")
	t.Setenv("FAKE_PARENT_KIND", "claude")
}

func decodeObject(t *testing.T, raw string) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("json: %v\n%s", err, raw)
	}
	return value
}

func TestInit_designatedFirstPaneClaimsCoordinator(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	out, err := runCLI(t, home, "init")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	value := decodeObject(t, out)
	if value["role"] != "coordinator" {
		t.Fatalf("role = %v, want coordinator", value["role"])
	}
	if value["installation"] != true {
		t.Fatalf("installation = %v", value["installation"])
	}
	if value["registered"] == false {
		t.Fatal("expected registration")
	}
	ownerRaw, err := os.ReadFile(filepath.Join(home, "context.json"))
	if err != nil {
		t.Fatal(err)
	}
	var owner map[string]any
	if err := json.Unmarshal(ownerRaw, &owner); err != nil {
		t.Fatal(err)
	}
	if owner["role"] != "coordinator" || owner["pane"] != "w-parent:p1" {
		t.Fatalf("owner = %v", owner)
	}
}

func TestBackup_createsRecordsOnlyArchive(t *testing.T) {
	home := writeDesignatedHome(t)
	dest := filepath.Join(t.TempDir(), "records.tar.gz")
	out, err := runCLI(t, home, "backup", dest)
	if err != nil {
		t.Fatalf("backup: %v\n%s", err, out)
	}
	value := decodeObject(t, out)
	if value["backup"] != dest {
		t.Fatalf("backup path = %v", value["backup"])
	}
	if _, ok := value["sha256"].(string); !ok {
		t.Fatalf("missing sha256 in %v", value)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("backup archive is empty")
	}
	again, err := runCLI(t, home, "backup", dest)
	if err == nil {
		t.Fatalf("expected existing destination to fail, got %s", again)
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v", err)
	}
}

func TestReport_recordsWorkerClaim(t *testing.T) {
	home := writeDesignatedHome(t)
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing",
"base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief_path": "brief.md"}`)
	out, err := runCLI(t, home, "report", "t-aaaaaaaaaaaa", "--text", "done")
	if err != nil {
		t.Fatalf("report: %v\n%s", err, out)
	}
	value := decodeObject(t, out)
	if value["status"] != "reported-not-verified" {
		t.Fatalf("status = %v", value["status"])
	}
	evidence, _ := value["evidence"].([]any)
	if len(evidence) != 1 {
		t.Fatalf("evidence = %v", evidence)
	}
	raw, err := os.ReadFile(filepath.Join(home, "tasks", "t-aaaaaaaaaaaa", "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	var task map[string]any
	if err := json.Unmarshal(raw, &task); err != nil {
		t.Fatal(err)
	}
	if task["status"] != "reported" {
		t.Fatalf("task status = %v", task["status"])
	}
	report, _ := task["report"].(map[string]any)
	if report["text"] != "done" {
		t.Fatalf("report text = %v", report["text"])
	}
}

func TestAttention_marksOpenRecordSeen(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	if _, err := runCLI(t, home, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo",
"questions": [], "evidence": [], "report": null, "notice": null,
"attention": [{"id": "a-aaaaaaaaaa", "kind": "blocked", "status": "open", "at": "2026-01-01T00:00:00+00:00"}],
"brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief_path": "brief.md"}`)
	out, err := runCLI(t, home, "attention", "t-aaaaaaaaaaaa", "a-aaaaaaaaaa", "--seen")
	if err != nil {
		t.Fatalf("attention: %v\n%s", err, out)
	}
	value := decodeObject(t, out)
	if value["status"] != "seen" {
		t.Fatalf("status = %v", value["status"])
	}
	if value["closed_by"] != "coordinator" {
		t.Fatalf("closed_by = %v", value["closed_by"])
	}
}

func TestBind_parentOnlyUpdatesParent(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	if _, err := runCLI(t, home, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{
"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [],
"brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship",
"machine": "`+host+`", "session": "sum-test", "pane": "w-worker:p1", "worktree": "/tmp/work",
"parent": {"machine": "old", "session": "old", "pane": "old:p1"}
}`)
	out, err := runCLI(t, home, "bind", "t-aaaaaaaaaaaa", "--parent-only")
	if err != nil {
		t.Fatalf("bind: %v\n%s", err, out)
	}
	value := decodeObject(t, out)
	parent, _ := value["parent"].(map[string]any)
	if parent["pane"] != "w-parent:p1" {
		t.Fatalf("parent = %v", parent)
	}
}

func TestPrepare_createsIsolatedWorktree(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	if _, err := runCLI(t, home, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := execGit(t, repo, args...)
		if cmd != "" {
			t.Fatal(cmd)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "sum test")
	run("config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "fixture")
	briefPath := filepath.Join(home, "brief.md")
	if err := os.WriteFile(briefPath, []byte("Add a greeting and test it. Do not publish or merge.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, home, "prepare", "--repo", repo, "--brief", briefPath, "--harness", "codex", "--approved")
	if err != nil {
		t.Fatalf("prepare: %v\n%s", err, out)
	}
	value := decodeObject(t, out)
	if value["status"] != "prepared" {
		t.Fatalf("status = %v\n%s", value["status"], out)
	}
	worktree, _ := value["worktree"].(string)
	if worktree == "" || worktree == repo {
		t.Fatalf("worktree = %q", worktree)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("worktree missing: %v", err)
	}
	briefFile, _ := value["brief_path"].(string)
	text, err := os.ReadFile(briefFile)
	if err != nil {
		t.Fatal(err)
	}
	body := string(text)
	if !strings.Contains(body, "not the coordinator") || !strings.Contains(body, "ask") {
		t.Fatalf("brief missing required contract text:\n%s", body)
	}
}

func execGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := execCommand("git", append([]string{"-C", repo}, args...)...)
	if cmd.err != nil {
		return cmd.err.Error() + "\n" + cmd.out
	}
	return ""
}

type cmdResult struct {
	out string
	err error
}

func execCommand(name string, args ...string) cmdResult {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return cmdResult{out: string(out), err: err}
}

func TestReview_appendsFindings(t *testing.T) {
	home := writeDesignatedHome(t)
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "reported", "repository": "owner/repo",
"questions": [], "evidence": [], "report": {"text": "done"}, "notice": null, "attention": [], "brief": "do the thing",
"base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship"}`)
	out, err := runCLI(t, home, "review", "t-aaaaaaaaaaaa", "--verdict", "comment", "--text", "looks fine")
	if err != nil {
		t.Fatalf("review: %v\n%s", err, out)
	}
	value := decodeObject(t, out)
	if value["task"] != "t-aaaaaaaaaaaa" {
		t.Fatalf("task = %v", value["task"])
	}
	ev, _ := value["evidence"].(map[string]any)
	if ev["kind"] != "review" {
		t.Fatalf("evidence = %v", ev)
	}
	if ev["verdict"] != "comment" {
		t.Fatalf("verdict = %v", ev["verdict"])
	}
}

func TestExecutionPark_releasesVerifierAttempt(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	if _, err := runCLI(t, home, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", fmt.Sprintf(`{
"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo",
"machine": %q, "session": "sum-test", "pane": "w-worker:p1",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [],
"brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship",
"execution": {"schema": 1,
  "worker": {"id": "x-aaaaaaaaaaaa", "kind": "worker", "state": "running", "generation": 1,
             "owner": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1"}, "checkout": "/tmp/x",
             "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": []},
  "verifiers": [{"id": "x-bbbbbbbbbbbb", "kind": "verifier", "state": "running", "generation": 1,
             "owner": {"machine": %q, "session": "sum-test", "pane": "w-rev:p1"}, "checkout": "/tmp/y",
             "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": []}]
}}`, host, host, host))
	out, err := runCLI(t, home, "execution", "park", "t-aaaaaaaaaaaa", "--attempt", "x-bbbbbbbbbbbb")
	if err != nil {
		t.Fatalf("park: %v\n%s", err, out)
	}
	value := decodeObject(t, out)
	if value["released"] != true {
		t.Fatalf("released = %v\n%s", value["released"], out)
	}
}

func TestRepairExtend_requiresCoordinator(t *testing.T) {
	home := writeDesignatedHome(t)
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "waiting", "repository": "owner/repo",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing",
"base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship"}`)
	_, err := runCLI(t, home, "repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional", "2", "--approved", "--text", "grant more")
	if err == nil {
		t.Fatal("expected coordinator requirement")
	}
	if !strings.Contains(err.Error(), "not the registered coordinator") && !strings.Contains(err.Error(), "Herdr pane") {
		t.Fatalf("err = %v", err)
	}
}
