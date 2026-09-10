package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPresetListAndShow_matchThePythonReferenceStdout(t *testing.T) {
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

	settingsJSON := `{"schema": 1, "worker": {"preset": "fast"}, "presets": {"fast": {"harness": "codex", "model": "gpt-5", "reasoning": "high", "args": ["--flag"], "revision": 3}, "slow": {"harness": "claude", "revision": 1}}}`

	cases := []struct {
		name          string
		writeSettings bool
		args          []string
	}{
		{name: "list on empty store", args: []string{"preset", "list"}},
		{name: "list with presets", writeSettings: true, args: []string{"preset", "list"}},
		{name: "show a preset with model/reasoning/args", writeSettings: true, args: []string{"preset", "show", "fast"}},
		{name: "show a bare preset", writeSettings: true, args: []string{"preset", "show", "slow"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.writeSettings {
				if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(settingsJSON), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			fullArgs := append([]string{"--home", home}, tc.args...)

			want, err := exec.Command(reference, fullArgs...).Output()
			if err != nil {
				t.Fatalf("python reference failed: %v", err)
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
