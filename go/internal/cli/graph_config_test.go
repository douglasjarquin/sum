package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGraphConfig_matchesThePythonReferenceAcrossScenarios(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	reference := filepath.Join(repoRoot, "bin", "sumctl")
	if _, statErr := os.Stat(reference); statErr != nil {
		t.Skipf("reference bin/sumctl not found: %v", statErr)
	}

	installationReleases := filepath.Join(repoRoot, "..", "..", "..", ".local", "releases")
	pinned := findPinnedCodegraph(t, installationReleases)
	if pinned == "" {
		t.Skip("no pinned codegraph release found to exercise the available-tool path")
	}

	cases := []struct {
		name   string
		env    []string
		args   []string
		expect string
	}{
		{name: "claude snippet", env: []string{"SUM_CODEGRAPH_BIN=" + pinned}, args: []string{"graph", "config", "--harness", "claude"}},
		{name: "codex snippet", env: []string{"SUM_CODEGRAPH_BIN=" + pinned}, args: []string{"graph", "config", "--harness", "codex"}},
		{name: "cursor snippet", env: []string{"SUM_CODEGRAPH_BIN=" + pinned}, args: []string{"graph", "config", "--harness", "cursor"}},
		{name: "opencode snippet", env: []string{"SUM_CODEGRAPH_BIN=" + pinned}, args: []string{"graph", "config", "--harness", "opencode"}},
		{name: "raw snippet", env: []string{"SUM_CODEGRAPH_BIN=" + pinned}, args: []string{"graph", "config", "--harness", "claude", "--raw"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			fullArgs := append([]string{"--home", home}, tc.args...)

			pythonCmd := exec.Command(reference, fullArgs...)
			pythonCmd.Env = append(os.Environ(), tc.env...)
			want, err := pythonCmd.Output()
			if err != nil {
				t.Fatalf("python reference failed: %v", err)
			}

			for _, kv := range tc.env {
				parts := splitEnv(kv)
				t.Setenv(parts[0], parts[1])
			}
			var stdout, stderr bytes.Buffer
			root := NewRoot(reference, &stdout, &stderr)
			root.SetArgs(fullArgs)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("go command failed: %v (stderr=%s)", err, stderr.String())
			}

			if stdout.String() != string(want) {
				t.Fatalf("go output =\n%s\nwant (python reference)\n%s", stdout.String(), want)
			}
		})
	}
}

func TestGraphConfig_missingToolIsAGoError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUM_CODEGRAPH_BIN", filepath.Join(home, "missing-codegraph"))
	var stdout, stderr bytes.Buffer
	root := NewRoot("", &stdout, &stderr)
	root.SetArgs([]string{"--home", home, "graph", "config", "--harness", "claude"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected missing codegraph to fail")
	}
}

func findPinnedCodegraph(t *testing.T, releasesGlobRoot string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(releasesGlobRoot, "*", ".local", "bin", "codegraph"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	for _, m := range matches {
		if info, statErr := os.Lstat(m); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			if _, resolveErr := filepath.EvalSymlinks(m); resolveErr == nil {
				return m
			}
			continue
		}
		if _, statErr := os.Stat(m); statErr == nil {
			return m
		}
	}
	return ""
}

func splitEnv(kv string) [2]string {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return [2]string{kv[:i], kv[i+1:]}
		}
	}
	return [2]string{kv, ""}
}
