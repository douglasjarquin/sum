package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGraphConfig_pinsStdoutAcrossScenarios(t *testing.T) {
	installationReleases := filepath.Join(repoRoot(t), "..", "..", "..", ".local", "releases")
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
			for _, kv := range tc.env {
				parts := splitEnv(kv)
				t.Setenv(parts[0], parts[1])
			}
			assertStdoutGolden(t, home, tc.args, scenarioGolden(t))
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
