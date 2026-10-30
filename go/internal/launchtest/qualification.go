package launchtest

import (
	"context"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// QualifyCodex seals fixture-local command/keymap provenance and advertises the
// exact matching native template. No actual coding agent or credentials run.
func QualifyCodex(t testing.TB, f *hctest.Fixture, cwd string) (launch.HCTemplate, string, launch.HCTemplateOptions) {
	t.Helper()
	runtime := t.TempDir()
	config := t.TempDir()
	home := t.TempDir()
	binary := filepath.Join(runtime, ".local/bin/codex")
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' 'codex-cli 0.166.0'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	options := launch.HCTemplateOptions{Cwd: cwd, CodexHome: home}
	spec, err := launch.PrepareHCTemplate(runtime, config, "codex", nil, options)
	if err != nil {
		t.Fatal(err)
	}
	quoted := make([]string, len(spec.Argv))
	for i, arg := range spec.Argv {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	client := f.Client()
	existing, err := client.Templates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.SetTemplates(append(existing.Templates, hcclient.TemplateInfo{Name: spec.Name, Command: strings.Join(quoted, " ")}))
	f.SetTemplateSubmit(spec.Name, "tab")
	return spec, config, options
}
