package settings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestTemplateOptionsSurviveWorkerSettingsChanges(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	original := `{"schema":1,"template":{"codex_home":"/private/provider-home","config_files":["/private/keymap.toml"],"input_env":{"GROK_HOME":"/private/grok"}}}`
	if err := os.WriteFile(filepath.Join(s.Home, File), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(s, WriteArgs{WorkerHarness: "codex"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSettings(s)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Template.CodexHome != "/private/provider-home" || len(loaded.Template.ConfigFiles) != 1 || loaded.Template.InputEnv["GROK_HOME"] != "/private/grok" {
		t.Fatalf("template options lost: %+v", loaded.Template)
	}
}
