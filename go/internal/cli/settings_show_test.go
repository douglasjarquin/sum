package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsShow_pinsStdoutAcrossScenarios(t *testing.T) {
	cases := []struct {
		name   string
		golden string
		files  map[string]string
	}{
		{name: "fresh store, no settings.json", golden: "settings-show-fresh"},
		{
			name:   "populated settings",
			golden: "settings-show-populated",
			files: map[string]string{
				"settings.json": `{"schema": 1, "capacity": {"global": 3}, "worker": {"harness": "claude", "model": "sonnet"}, "presets": {"fast": {"harness": "codex", "revision": 2}}, "reviewer": {"preset": "fast"}}`,
			},
		},
		{
			name:   "invalid capacity value",
			golden: "settings-show-invalid-capacity",
			files: map[string]string{
				"settings.json": `{"schema": 1, "capacity": {"global": 0}}`,
			},
		},
		{
			name:   "unknown preset reference",
			golden: "settings-show-unknown-preset",
			files: map[string]string{
				"settings.json": `{"schema": 1, "worker": {"preset": "missing"}}`,
			},
		},
		{
			name:   "unknown capacity key",
			golden: "settings-show-unknown-capacity-key",
			files: map[string]string{
				"settings.json": `{"schema": 1, "capacity": {"global": 5, "bogus": 1}}`,
			},
		},
		{
			name:   "preset arg conflicts with resolved model",
			golden: "settings-show-preset-arg-conflict",
			files: map[string]string{
				"settings.json": `{"schema": 1, "presets": {"fast": {"harness": "claude", "model": "sonnet", "args": ["--model", "haiku"]}}}`,
			},
		},
		{
			name:   "occupancy across active and archived tasks",
			golden: "settings-show-occupancy",
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
			assertStdoutGolden(t, home, []string{"settings", "show"}, tc.golden)
		})
	}
}
