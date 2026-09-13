package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestGraphInit_unknownFlagOrExtraPositionalIsUsageError(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "unknown flag", args: []string{"--unexpected"}},
		{name: "extra positional", args: []string{"extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, worktree, taskID := graphInitLab(t)
			taskPath := filepath.Join(home, "tasks", taskID, "task.json")
			taskBefore, err := os.ReadFile(taskPath)
			if err != nil {
				t.Fatal(err)
			}
			herdrRoot := filepath.Join(home, "fake-herdr")
			herdrBefore := listRegularFiles(t, herdrRoot)
			excludePath := filepath.Join(worktree, ".git", "info", "exclude")
			excludeBefore, _ := os.ReadFile(excludePath)

			var stdout, stderr bytes.Buffer
			root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &stdout, &stderr)
			fullArgs := append([]string{"--home", home, "graph", "init", taskID}, tc.args...)
			root.SetArgs(fullArgs)
			err = root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout.String(), stderr.String())
			}

			graphPath := filepath.Join(home, "tasks", taskID, "graph.json")
			if _, statErr := os.Stat(graphPath); statErr == nil {
				t.Fatal("graph.json was written")
			}
			if _, statErr := os.Stat(filepath.Join(worktree, ".codegraph")); statErr == nil {
				t.Fatal(".codegraph was created")
			}
			taskAfter, readErr := os.ReadFile(taskPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(taskAfter) != string(taskBefore) {
				t.Fatalf("task.json changed:\n%s", taskAfter)
			}
			excludeAfter, _ := os.ReadFile(excludePath)
			if string(excludeAfter) != string(excludeBefore) {
				t.Fatalf("git exclude changed:\n%s", excludeAfter)
			}
			if got := listRegularFiles(t, herdrRoot); got != herdrBefore {
				t.Fatalf("herdr state changed:\nbefore:\n%s\nafter:\n%s", herdrBefore, got)
			}
			assertGraphUsageError(t, err)
		})
	}
}

func TestGraphCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		lab       bool
		task      bool
		herdr     bool
		success   bool
		usage     bool
		domainErr string
	}{
		{name: "valid init", args: []string{"graph", "init", "t-aaaaaaaaaaaa"}, lab: true, success: true},
		{name: "valid init after dashdash", args: []string{"graph", "init", "--", "t-aaaaaaaaaaaa"}, lab: true, success: true},
		{name: "valid status", args: []string{"graph", "status", "t-aaaaaaaaaaaa"}, task: true, success: true},
		{name: "valid status after dashdash", args: []string{"graph", "status", "--", "t-aaaaaaaaaaaa"}, task: true, success: true},
		{name: "valid config equals harness", args: []string{"graph", "config", "--harness=claude"}, domainErr: "No snippet"},
		{name: "valid config missing tool", args: []string{"graph", "config", "--harness", "claude"}, domainErr: "No snippet"},
		{name: "graph missing subcommand", args: []string{"graph"}, usage: true},
		{name: "init missing task", args: []string{"graph", "init"}, usage: true},
		{name: "status missing task", args: []string{"graph", "status"}, usage: true},
		{name: "config missing harness", args: []string{"graph", "config"}, usage: true},
		{name: "init extra positional", args: []string{"graph", "init", "t-aaaaaaaaaaaa", "extra"}, lab: true, usage: true},
		{name: "init unknown flag", args: []string{"graph", "init", "t-aaaaaaaaaaaa", "--unexpected"}, lab: true, usage: true},
		{name: "init extra after dashdash", args: []string{"graph", "init", "t-aaaaaaaaaaaa", "--", "extra"}, lab: true, usage: true},
		{name: "status extra positional", args: []string{"graph", "status", "t-aaaaaaaaaaaa", "extra"}, task: true, usage: true},
		{name: "status unknown flag", args: []string{"graph", "status", "t-aaaaaaaaaaaa", "--unexpected"}, task: true, usage: true},
		{name: "config extra positional", args: []string{"graph", "config", "--harness", "claude", "extra"}, usage: true},
		{name: "config unknown flag", args: []string{"graph", "config", "--harness", "claude", "--unexpected"}, usage: true},
		{name: "config invalid harness", args: []string{"graph", "config", "--harness", "bogus"}, usage: true},
		{name: "init requires coordinator", args: []string{"graph", "init", "t-aaaaaaaaaaaa"}, task: true, herdr: true, domainErr: "registered coordinator"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := writeDesignatedHome(t)
			t.Setenv("SUM_CODEGRAPH_BIN", filepath.Join(home, "no-such-codegraph"))
			var worktree, taskID string
			switch {
			case tc.lab:
				home, worktree, taskID = graphInitLab(t)
			case tc.task:
				if tc.herdr {
					herdrEnv(t, home)
				}
				worktree = t.TempDir()
				initGitRepoWithCommit(t, worktree)
				taskID = "t-aaaaaaaaaaaa"
				writeGraphTask(t, home, taskID, worktree)
			}
			taskPath := ""
			var taskBefore []byte
			if taskID != "" {
				taskPath = filepath.Join(home, "tasks", taskID, "task.json")
				var readErr error
				taskBefore, readErr = os.ReadFile(taskPath)
				if readErr != nil {
					t.Fatal(readErr)
				}
			}

			stdout, stderr, err := runGraph(t, home, tc.args...)
			switch {
			case tc.success:
				if err != nil {
					t.Fatalf("err = %v stderr=%s stdout=%s", err, stderr, stdout)
				}
				if !strings.Contains(stdout, taskID) {
					t.Fatalf("stdout missing task id:\n%s", stdout)
				}
				if tc.args[1] == "init" {
					if _, statErr := os.Stat(filepath.Join(home, "tasks", taskID, "graph.json")); statErr != nil {
						t.Fatalf("valid init did not write graph.json: %v", statErr)
					}
				}
			case tc.usage:
				assertGraphUsageError(t, err)
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				if taskPath != "" {
					if _, statErr := os.Stat(filepath.Join(home, "tasks", taskID, "graph.json")); statErr == nil {
						t.Fatal("graph.json was written")
					}
					taskAfter, readErr := os.ReadFile(taskPath)
					if readErr != nil {
						t.Fatal(readErr)
					}
					if string(taskAfter) != string(taskBefore) {
						t.Fatalf("task.json changed:\n%s", taskAfter)
					}
				}
				if worktree != "" {
					if _, statErr := os.Stat(filepath.Join(worktree, ".codegraph")); statErr == nil {
						t.Fatal(".codegraph was created")
					}
				}
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s", stdout)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
			}
		})
	}
}

func runGraph(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertGraphUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "registered coordinator") || strings.Contains(msg, "No snippet") || strings.Contains(msg, "This task has no worktree") {
		t.Fatalf("err = %v, want a usage failure", err)
	}
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "accepts 1 arg") ||
		strings.Contains(msg, "required flag") ||
		strings.Contains(msg, "command is required") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid graph")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func writeGraphTask(t *testing.T, home, taskID, worktree string) {
	t.Helper()
	writeTaskFixture(t, home, taskID, fmt.Sprintf(`{"schema": 1, "id": %q, "status": "running", "repository": "owner/repo",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing",
"base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "task", "brief_path": "brief.md", "worktree": %q}`, taskID, worktree))
}

func graphInitLab(t *testing.T) (home, worktree, taskID string) {
	t.Helper()
	home = writeDesignatedHome(t)
	herdrEnv(t, home)
	t.Setenv("SUM_CODEGRAPH_BIN", filepath.Join(home, "no-such-codegraph"))
	if _, err := runCLI(t, home, "init"); err != nil {
		t.Fatalf("coordinator init: %v", err)
	}
	worktree = t.TempDir()
	initGitRepoWithCommit(t, worktree)
	taskID = "t-aaaaaaaaaaaa"
	writeGraphTask(t, home, taskID, worktree)
	return home, worktree, taskID
}

func listRegularFiles(t *testing.T, dir string) string {
	t.Helper()
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return ""
	}
	var names []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		names = append(names, fmt.Sprintf("%s %d", rel, info.Size()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	out := ""
	for _, name := range names {
		out += name + "\n"
	}
	return out
}
