package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/settings"
)

func TestNativeRetirementPreservesConfiguredTemplateInputs(t *testing.T) {
	for _, existing := range []string{"absent", "unrelated", "explicit"} {
		t.Run(existing, func(t *testing.T) {
			clearFallbackIdentity(t)
			root, s, f, _ := developerFallbackLab(t)
			writeFile(t, filepath.Join(root, "COORDINATOR.md"), "# Coordinate approved work\n")
			provider := t.TempDir()
			config := filepath.Join(provider, "extra.toml")
			writeFile(t, config, "model = 'native-custom'\n")
			frozen := map[string]string{"HOME": provider, "CODEX_HOME": filepath.Join(provider, "codex"), "CLAUDE_CONFIG_DIR": filepath.Join(provider, "claude"), "GROK_HOME": filepath.Join(provider, "grok"), "XDG_CONFIG_HOME": filepath.Join(provider, "xdg")}
			want := settings.TemplateOptions{CodexHome: frozen["CODEX_HOME"], ConfigFiles: []string{config}, InputEnv: frozen}
			owner, err := s.Owner()
			if err != nil {
				t.Fatal(err)
			}
			native, err := ordjson.FromValue(map[string]any{"options": launch.HCTemplateOptions{CodexHome: want.CodexHome, ConfigFiles: want.ConfigFiles, InputEnv: map[string]string{"ignored": "options-input-is-not-the-frozen-spec"}}, "spec": launch.HCTemplate{Schema: 2, InputEnv: frozen}})
			if err != nil {
				t.Fatal(err)
			}
			owner.Set("native_template", native)
			ownerPath := filepath.Join(s.Home, "context.json")
			if err := ordjson.WriteFile(ownerPath, owner); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(ownerPath)
			if err != nil {
				t.Fatal(err)
			}
			if existing != "absent" {
				value, err := ordjson.FromValue(map[string]any{"schema": settings.Schema, "hook": map[string]any{"enabled": false}, "worker": map[string]any{"harness": "codex"}})
				if err != nil {
					t.Fatal(err)
				}
				doc := value
				if existing == "explicit" {
					want.CodexHome = filepath.Join(provider, "explicit-codex")
					want.InputEnv = map[string]string{}
					for key, value := range frozen {
						want.InputEnv[key] = value
					}
					want.InputEnv["CODEX_HOME"] = want.CodexHome
					value, err := ordjson.FromValue(want)
					if err != nil {
						t.Fatal(err)
					}
					doc.Set("template", value)
				}
				if err := ordjson.WriteFile(filepath.Join(s.Home, settings.File), doc); err != nil {
					t.Fatal(err)
				}
			}
			view, err := ordinaryInit(t, root, "--retire-native")
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := settings.LoadSettings(s)
			if err != nil || !reflect.DeepEqual(loaded.Template, want) {
				t.Fatalf("retired template = %#v; want %#v; error = %v", loaded, want, err)
			}
			if existing != "absent" {
				if loaded.Worker == nil {
					t.Fatal("retirement removed the worker default")
				}
				harness, _ := loaded.Worker.Get("harness")
				if harness != "codex" {
					t.Fatal("retirement changed the worker default")
				}
			}
			codex := filepath.Join(root, ".local", "bin", "codex")
			writeFile(t, codex, "#!/bin/sh\nprintf '%s\\n' 'codex-cli 0.166.0'\n")
			if err := os.Chmod(codex, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SUM_CODEX_BIN", codex)
			for _, role := range []string{"worker", "reviewer"} {
				cwd := filepath.Join(provider, role)
				if err := os.MkdirAll(cwd, 0700); err != nil {
					t.Fatal(err)
				}
				qualified, err := launch.PrepareHCTemplate(root, t.TempDir(), "codex", []string{codex}, launch.HCTemplateOptions{Cwd: cwd, CodexHome: loaded.Template.CodexHome, ConfigFiles: loaded.Template.ConfigFiles, InputEnv: loaded.Template.InputEnv})
				if err != nil || !reflect.DeepEqual(qualified.InputEnv, want.InputEnv) || qualified.ConfigSHA256[config] == "" {
					t.Fatalf("%s qualification lost retired configuration: %#v %v", role, qualified, err)
				}
			}
			after, err := os.ReadFile(ownerPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("historical owner changed: %v", err)
			}
			settingsPath := filepath.Join(s.Home, settings.File)
			settingsBefore, err := os.ReadFile(settingsPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ordinaryInit(t, root, "--retire-native", "--claim", asString(view["claim"])); err != nil {
				t.Fatal(err)
			}
			settingsAfter, err := os.ReadFile(settingsPath)
			if err != nil || !bytes.Equal(settingsBefore, settingsAfter) || len(f.Calls()) != 0 {
				t.Fatalf("retirement retry changed preferences or contacted HC: %v", err)
			}
		})
	}
}

func TestNativeRetirementInvalidTemplatePreferencesLeaveAuthorityUnchanged(t *testing.T) {
	for _, invalid := range []string{"settings-schema", "native-options", "native-input-env"} {
		t.Run(invalid, func(t *testing.T) {
			clearFallbackIdentity(t)
			root, s, _, _ := developerFallbackLab(t)
			writeFile(t, filepath.Join(root, "COORDINATOR.md"), "# Coordinate approved work\n")
			owner, err := s.Owner()
			if err != nil {
				t.Fatal(err)
			}
			native := map[string]any{"options": map[string]any{"CodexHome": "/private/provider"}, "spec": map[string]any{"input_env": map[string]any{"HOME": "/private/provider"}}}
			switch invalid {
			case "settings-schema":
				writeFile(t, filepath.Join(s.Home, settings.File), `{"schema":999}`)
			case "native-options":
				native["options"] = map[string]any{"CodexHome": 42}
			case "native-input-env":
				native["spec"] = map[string]any{"input_env": map[string]any{"HOME": 42}}
			}
			value, err := ordjson.FromValue(native)
			if err != nil {
				t.Fatal(err)
			}
			owner.Set("native_template", value)
			if err := ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
				t.Fatal(err)
			}
			before := snapshotHome(t, s.Root)
			view, err := ordinaryInit(t, root, "--retire-native")
			if err == nil || view["blocker"] != "retire-native" {
				t.Fatalf("invalid retirement = %v %v", view, err)
			}
			if after := snapshotHome(t, s.Root); after != before {
				t.Fatal("invalid retirement changed authority or preferences")
			}
		})
	}
}
