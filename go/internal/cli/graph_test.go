package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
		})
	}
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
	writeTaskFixture(t, home, taskID, fmt.Sprintf(`{"schema": 1, "id": %q, "status": "running", "repository": "owner/repo",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing",
"base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "task", "brief_path": "brief.md", "worktree": %q}`, taskID, worktree))
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
