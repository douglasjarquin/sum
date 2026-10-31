package hctest

import (
	"context"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeTemplateDirectoryIsReadAtEachAPIBoundary(t *testing.T) {
	f := New(t)
	config := t.TempDir()
	if err := f.LoadTemplatesFrom(config); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(config, "herdr-core/templates")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	client := f.Client()
	first, err := client.Templates(context.Background())
	if err != nil || len(first.Templates) != 0 {
		t.Fatal("template existed before publication")
	}
	path := filepath.Join(directory, "sum-codex.md")
	text := "---\ndescription: fixture agent\ncommand: '/fixture/codex' '--no-daemon'\nsubmit: tab\n---\nRead pointer.\n"
	if err = os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	listed, err := client.Templates(context.Background())
	if err != nil || len(listed.Templates) != 1 || listed.Templates[0].Command != "'/fixture/codex' '--no-daemon'" {
		t.Fatalf("native templates %v %v", listed, err)
	}
	run, err := client.Run(context.Background(), hcclient.RunParams{Argv: []string{"sum-codex", "pointer"}, OutliveParent: true, NoNotify: true})
	if err != nil {
		t.Fatal(err)
	}
	if run.Template == nil || *run.Template != "sum-codex" || run.Submit == nil || *run.Submit != "tab" || len(run.Argv) != 3 || run.Argv[1] != "-c" {
		t.Fatalf("native template run %v", run)
	}
	f.SetTemplateSubmit("sum-codex", "enter")
	run, err = client.Run(context.Background(), hcclient.RunParams{Argv: []string{"sum-codex"}})
	if err != nil || run.Submit == nil || *run.Submit != "enter" {
		t.Fatal("explicit submit mismatch did not override directory fixture")
	}
}
