package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func codexBinary(t *testing.T, root, version string) {
	t.Helper()
	p := filepath.Join(root, ".local/bin/codex")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\nprintf '%s\\n' 'codex-cli "+version+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
}
func TestHCTemplateQualifiedCodex(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	config := t.TempDir()
	codexBinary(t, root, "0.166.0")
	spec, err := PrepareHCTemplate(root, config, "codex", []string{"-m", "chosen-model", "-c", "model_reasoning_effort=high", "--sandbox", "workspace-write"}, HCTemplateOptions{Cwd: root, CodexHome: home})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(config, "herdr-core/templates", spec.Name+".md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "submit: tab") || !strings.Contains(text, "'--no-daemon'") || !strings.Contains(text, "'chosen-model'") {
		t.Fatal(text)
	}
	run := spec.RunArgv("read pointer; $(touch must-not-run)")
	if len(run) != 2 || run[1] != "read pointer; $(touch must-not-run)" {
		t.Fatalf("task interpolated: %v", run)
	}
	if strings.Contains(text, "must-not-run") || spec.SHA256 == "" || spec.CommandSHA256 == "" || spec.KeymapSHA256 == "" {
		t.Fatal("template lacks bound provenance")
	}
	again, err := PrepareHCTemplate(root, config, "codex", []string{"-m", "chosen-model", "-c", "model_reasoning_effort=high", "--sandbox", "workspace-write"}, HCTemplateOptions{Cwd: root, CodexHome: home})
	if err != nil || again.SHA256 != spec.SHA256 {
		t.Fatalf("not idempotent: %v", err)
	}
	if err = os.WriteFile(filepath.Join(config, "herdr-core/templates", spec.Name+".md"), []byte("custom"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = PrepareHCTemplate(root, config, "codex", []string{"-m", "chosen-model", "-c", "model_reasoning_effort=high", "--sandbox", "workspace-write"}, HCTemplateOptions{Cwd: root, CodexHome: home}); err == nil {
		t.Fatal("customized template overwritten")
	}
}
func TestHCTemplateRejectsUnqualifiedStatusAndKeymaps(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	config := t.TempDir()
	codexBinary(t, root, "0.153.4")
	if _, err := PrepareHCTemplate(root, config, "codex", nil, HCTemplateOptions{Cwd: root, CodexHome: home}); err == nil {
		t.Fatal("unqualified version accepted")
	}
	codexBinary(t, root, "0.166.0")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model='keep'\n[tui.keymap.composer]\nqueue_message='ctrl+q'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareHCTemplate(root, config, "codex", nil, HCTemplateOptions{Cwd: root, CodexHome: home}); err == nil {
		t.Fatal("custom keymap accepted without queue qualification")
	}
	if _, err := PrepareHCTemplate(root, config, "codex", []string{"-c", "tui={keymap={}}"}, HCTemplateOptions{Cwd: root, CodexHome: t.TempDir()}); err == nil {
		t.Fatal("CLI keymap override accepted")
	}
	if _, err := PrepareHCTemplate(root, config, "unqualified", nil, HCTemplateOptions{Cwd: root}); err == nil {
		t.Fatal("unknown harness enabled")
	}
}

func TestHCTemplateValidationDetectsChanges(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	config := t.TempDir()
	codexBinary(t, root, "0.166.0")
	options := HCTemplateOptions{Cwd: root, CodexHome: home}
	spec, err := PrepareHCTemplate(root, config, "codex", nil, options)
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateHCTemplate(config, spec, options); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "config.toml"), []byte("model='changed'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = ValidateHCTemplate(config, spec, options); err == nil {
		t.Fatal("config changed without invalidating recorded qualification")
	}
	if _, err = PrepareHCTemplate(root, config, "codex", []string{"-c", `"tui" . "keymap" = {}`}, HCTemplateOptions{Cwd: root, CodexHome: t.TempDir()}); err == nil {
		t.Fatal("quoted keymap override accepted")
	}
}
