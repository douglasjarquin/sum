package launchtest

import (
	"context"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"os"
	"path/filepath"
	"testing"
)

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
	client := f.Client()
	existing, err := client.Templates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.SetTemplates(append(existing.Templates, hcclient.TemplateInfo{Name: spec.Name, Command: spec.Command()}))
	f.SetTemplateSubmit(spec.Name, "tab")
	return spec, config, options
}
