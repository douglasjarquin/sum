package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func runPythonSettingsShow(t *testing.T, reference, home string) []byte {
	t.Helper()
	out, err := exec.Command(reference, "--home", home, "settings", "show").Output()
	if err != nil {
		t.Fatalf("python reference failed: %v", err)
	}
	return out
}

func runGoSettingsShow(t *testing.T, reference, home string) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs([]string{"--home", home, "settings", "show"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("go settings show failed: %v (stderr=%s)", err, stderr.String())
	}
	return stdout.String(), stderr.String()
}

func TestSettingsShow_matchesThePythonReferenceAcrossScenarios(t *testing.T) {
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

	cases := []struct {
		name  string
		files map[string]string
	}{
		{name: "fresh store, no settings.json"},
		{
			name: "populated settings",
			files: map[string]string{
				"settings.json": `{"schema": 1, "capacity": {"global": 3}, "worker": {"harness": "claude", "model": "sonnet"}, "presets": {"fast": {"harness": "codex", "revision": 2}}, "reviewer": {"preset": "fast"}}`,
			},
		},
		{
			name: "invalid capacity value",
			files: map[string]string{
				"settings.json": `{"schema": 1, "capacity": {"global": 0}}`,
			},
		},
		{
			name: "unknown preset reference",
			files: map[string]string{
				"settings.json": `{"schema": 1, "worker": {"preset": "missing"}}`,
			},
		},
		{
			name: "unknown capacity key",
			files: map[string]string{
				"settings.json": `{"schema": 1, "capacity": {"global": 5, "bogus": 1}}`,
			},
		},
		{
			name: "preset arg conflicts with resolved model",
			files: map[string]string{
				"settings.json": `{"schema": 1, "presets": {"fast": {"harness": "claude", "model": "sonnet", "args": ["--model", "haiku"]}}}`,
			},
		},
		{
			name: "occupancy across active and archived tasks",
			files: map[string]string{
				"tasks/t-aaaaaaaaaaaa/task.json": `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo"}`,
				"tasks/t-bbbbbbbbbbbb/task.json": `{"schema": 1, "id": "t-bbbbbbbbbbbb", "status": "archived", "repository": "owner/repo"}`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			for relPath, content := range tc.files {
				full := filepath.Join(home, relPath)
				if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want := runPythonSettingsShow(t, reference, home)
			got, _ := runGoSettingsShow(t, reference, home)
			if got != string(want) {
				t.Fatalf("go output =\n%s\nwant (python reference)\n%s", got, want)
			}
		})
	}
}
