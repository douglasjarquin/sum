package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLspEnsureHook_missingBinaryFailsOpenWithEmptyStdout(t *testing.T) {
	root := t.TempDir()
	script := copyLspEnsureHook(t, root)
	stdout, stderr, code := runLspEnsureHook(t, script, root, grokHookPayload(root))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr=%s)", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout must be empty, got %q", stdout)
	}
}

func TestLspEnsureHook_discardsLaggingHelperStdout(t *testing.T) {
	root := t.TempDir()
	script := copyLspEnsureHook(t, root)
	plantStagedSumctl(t, root, "#!/bin/sh\ncat >/dev/null\necho 'commands:'\necho '  doctor: observe'\nexit 0\n")
	stdout, stderr, code := runLspEnsureHook(t, script, root, grokHookPayload(root))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr=%s)", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("TOON catalog leaked to hook stdout: %q", stdout)
	}
}

func TestLspEnsureHook_helperFailureStillExitsZero(t *testing.T) {
	root := t.TempDir()
	script := copyLspEnsureHook(t, root)
	plantStagedSumctl(t, root, "#!/bin/sh\necho 'sumctl: staged native binary is missing; run mise run setup or stage a release.' >&2\nexit 1\n")
	stdout, stderr, code := runLspEnsureHook(t, script, root, grokHookPayload(root))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr=%s)", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout must be empty, got %q", stdout)
	}
}

func TestLspEnsureHook_invokesEnsureWithGrokAndCodexPayloads(t *testing.T) {
	root := t.TempDir()
	script := copyLspEnsureHook(t, root)
	logPath := filepath.Join(root, "ensure.log")
	plantStagedSumctl(t, root, "#!/bin/sh\ncat >> \"$SUM_FAKE_ENSURE_LOG\"\nprintf '\\n' >> \"$SUM_FAKE_ENSURE_LOG\"\nexit 0\n")
	payloads := []struct {
		name    string
		payload string
		want    string
	}{
		{name: "grok", payload: grokHookPayload(root), want: `"hook_event_name": "PostToolUse"`},
		{name: "codex", payload: codexHookPayload(root), want: `"tool_name": "Bash"`},
	}
	for _, tc := range payloads {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(logPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			cmdEnv := append(os.Environ(), "SUM_FAKE_ENSURE_LOG="+logPath)
			stdout, stderr, code := runLspEnsureHookEnv(t, script, root, tc.payload, cmdEnv)
			if code != 0 {
				t.Fatalf("exit = %d, want 0 (stderr=%s)", code, stderr)
			}
			if stdout != "" {
				t.Fatalf("stdout must be empty, got %q", stdout)
			}
			body, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), tc.want) {
				t.Fatalf("ensure stdin = %q, want %q", body, tc.want)
			}
		})
	}
}

func TestLspEnsureHook_usesGitCommonDirHelperFromWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	main := t.TempDir()
	runGit(t, main, "init")
	runGit(t, main, "config", "user.email", "worker@example.test")
	runGit(t, main, "config", "user.name", "worker")
	if err := os.WriteFile(filepath.Join(main, "README"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, main, "add", "README")
	runGit(t, main, "commit", "-m", "init")

	copyLspEnsureHook(t, main)
	logPath := filepath.Join(main, "ensure.log")
	plantStagedSumctl(t, main, "#!/bin/sh\ncat >> \"$SUM_FAKE_ENSURE_LOG\"\nexit 0\n")

	work := t.TempDir()
	runGit(t, main, "worktree", "add", "--detach", work)
	script := copyLspEnsureHook(t, work)

	env := append(os.Environ(), "SUM_FAKE_ENSURE_LOG="+logPath)
	stdout, stderr, code := runLspEnsureHookEnv(t, script, work, grokHookPayload(work), env)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr=%s)", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout must be empty, got %q", stdout)
	}
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"workspaceRoot"`) {
		t.Fatalf("main helper was not invoked from the worktree, log=%q", body)
	}
}

func TestProjectHookFiles_pointAtLspEnsure(t *testing.T) {
	repo := repoRoot(t)
	type grokHook struct {
		Hooks struct {
			PostToolUse []struct {
				Hooks []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
					Timeout int    `json:"timeout"`
				} `json:"hooks"`
			} `json:"PostToolUse"`
		} `json:"hooks"`
	}
	var grok grokHook
	decodeJSONFile(t, filepath.Join(repo, ".grok", "hooks", "lsp-ensure.json"), &grok)
	if len(grok.Hooks.PostToolUse) == 0 || len(grok.Hooks.PostToolUse[0].Hooks) == 0 {
		t.Fatal("grok hook missing PostToolUse command")
	}
	g := grok.Hooks.PostToolUse[0].Hooks[0]
	if g.Command != "./bin/lsp-ensure" || g.Timeout != 120 || g.Type != "command" {
		t.Fatalf("grok hook = %+v", g)
	}

	var cursor struct {
		Hooks struct {
			PostToolUse []struct {
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"postToolUse"`
		} `json:"hooks"`
	}
	decodeJSONFile(t, filepath.Join(repo, ".cursor", "hooks.json"), &cursor)
	if len(cursor.Hooks.PostToolUse) == 0 || cursor.Hooks.PostToolUse[0].Command != "./bin/lsp-ensure" {
		t.Fatalf("cursor hook = %+v", cursor.Hooks.PostToolUse)
	}

	var codex grokHook
	decodeJSONFile(t, filepath.Join(repo, ".codex", "hooks.json"), &codex)
	if len(codex.Hooks.PostToolUse) == 0 || len(codex.Hooks.PostToolUse[0].Hooks) == 0 {
		t.Fatal("codex hook missing PostToolUse command")
	}
	c := codex.Hooks.PostToolUse[0].Hooks[0]
	if !strings.Contains(c.Command, "bin/lsp-ensure") || !strings.Contains(c.Command, "git rev-parse --show-toplevel") {
		t.Fatalf("codex hook command = %q", c.Command)
	}
	if c.Timeout != 120 {
		t.Fatalf("codex timeout = %d", c.Timeout)
	}
}

func copyLspEnsureHook(t *testing.T, root string) string {
	t.Helper()
	src := filepath.Join(repoRoot(t), "bin", "lsp-ensure")
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	destDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(destDir, "lsp-ensure")
	if err := os.WriteFile(dest, body, 0o755); err != nil {
		t.Fatal(err)
	}
	return dest
}

func plantStagedSumctl(t *testing.T, root, script string) {
	t.Helper()
	localBin := filepath.Join(root, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(localBin, "sumctl")
	if err := os.WriteFile(native, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "bin", "sumctl")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := "#!/bin/sh\nexec \"" + native + "\" \"$@\"\n"
	if err := os.WriteFile(launcher, []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
}

func runLspEnsureHook(t *testing.T, script, dir, payload string) (stdout, stderr string, code int) {
	t.Helper()
	return runLspEnsureHookEnv(t, script, dir, payload, os.Environ())
}

func runLspEnsureHookEnv(t *testing.T, script, dir, payload string, env []string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(script)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(payload)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err == nil {
		return out.String(), errBuf.String(), 0
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return out.String(), errBuf.String(), exit.ExitCode()
	}
	t.Fatalf("run %s: %v", script, err)
	return "", "", -1
}

func grokHookPayload(root string) string {
	return `{
  "hookEventName": "post_tool_use",
  "hook_event_name": "PostToolUse",
  "workspaceRoot": "` + root + `",
  "cwd": "` + root + `",
  "toolName": "read_file",
  "toolInput": {"target_file": "go/internal/cli/lsp.go"}
}`
}

func codexHookPayload(root string) string {
	return `{
  "hook_event_name": "PostToolUse",
  "cwd": "` + root + `",
  "tool_name": "Bash",
  "tool_input": {"command": "ls"},
  "tool_response": "ok"
}`
}

func decodeJSONFile(t *testing.T, path string, dest any) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, dest); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=worker", "GIT_AUTHOR_EMAIL=worker@example.test", "GIT_COMMITTER_NAME=worker", "GIT_COMMITTER_EMAIL=worker@example.test")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
