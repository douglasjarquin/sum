package roleinit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeReviewerPendingLaunchReceipt(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", t.TempDir())
	config := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".local/bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".local/bin/codex"), []byte("#!/bin/sh\nprintf '%s\\n' 'codex-cli 0.166.0'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(root, ".sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	stateValue, err := ordjson.ReadFile(filepath.Join(s.Home, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	state := stateValue.(*ordjson.Object)
	state.Set("instance", "review-installation")
	if err = ordjson.WriteFile(filepath.Join(s.Home, "state.json"), state); err != nil {
		t.Fatal(err)
	}
	spec, err := launch.PrepareHCTemplate(root, config, "codex", nil, launch.HCTemplateOptions{Cwd: root})
	if err != nil {
		t.Fatal(err)
	}
	f := hctest.New(t)
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Socket: f.Socket, Session: "review-lab", Address: "t1", CreatedMS: 1, Role: "reviewer", Task: "t-aaaaaaaaaaaa", Attempt: "review-attempt", BindingGeneration: 1, Cwd: root}
	task := ordjson.NewObject()
	task.Set("schema", json.Number(fmt.Sprint(store.Schema)))
	task.Set("id", endpoint.Task)
	task.Set("status", "running")
	taskPath, err := s.TaskPath(endpoint.Task)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(taskPath, "procedure", "sum-deliver.md")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	content := []byte("Pinned independent reviewer procedure.\n")
	if err = os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	candidate := strings.Repeat("a", 40)
	labels := map[string]string{"sum.installation": "review-installation", "sum.task": endpoint.Task, "sum.role": endpoint.Role, "sum.attempt": endpoint.Attempt}
	params := hcclient.RunParams{Argv: spec.RunArgv("Review candidate " + candidate + " using " + path), Cwd: root, Labels: labels, OutliveParent: true, NoNotify: true, Env: map[string]string{"SUM_HOME": s.Root, "SUM_SESSION": endpoint.Session, "SUM_CORE_ID": endpoint.CoreID, "HC_SOCKET": endpoint.Socket, "SUM_TASK": endpoint.Task, "SUM_ROLE": endpoint.Role, "SUM_ATTEMPT": endpoint.Attempt, "SUM_BINDING_GENERATION": "1"}}
	reviewer := endpoint.Object()
	reviewer.Set("address", "")
	reviewer.Set("created_ms", json.Number("0"))
	reviewer.Set("candidate", candidate)
	reviewer.Set("skill_path", path)
	reviewer.Set("skill_sha256", hex.EncodeToString(digest[:]))
	reviewer.Set("native_template", mustObject(nativeTemplate{Spec: spec, ConfigHome: config, Options: launch.HCTemplateOptions{Cwd: root}}))
	pending := ordjson.NewObject()
	pending.Set("attempt", endpoint.Attempt)
	pending.Set("state", "in-flight")
	pending.Set("params", mustObject(params))
	reviewer.Set("native_launch", pending)
	task.Set("reviewer", reviewer)
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	pid := uint32(101)
	template := spec.Name
	submit := "tab"
	terminal := hcclient.Terminal{Address: endpoint.Address, CreatedMS: 1, State: "live", Labels: labels, Cwd: root, Argv: []string{"sh", "-c", templateCommand(spec)}, Template: &template, Submit: &submit, PID: &pid, Foreground: &hcclient.Foreground{PID: 102, Name: "codex", Argv: spec.Argv, Cwd: root}}
	f.SetTerminal(terminal)
	registration, err := s.Register(endpoint, "reviewer", endpoint.Task, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.NativeReceipt(registration); err == nil {
		t.Fatal("pre-init launch registration has application authority")
	}
	caller := endpoint
	caller.Cwd = t.TempDir()
	view, err := InitDesignated(DesignatedOpts{RuntimeRoot: root, Store: s, Ctx: caller.Object(), Role: "reviewer", Task: endpoint.Task})
	if err != nil {
		t.Fatal(err)
	}
	role, _ := view.Get("role")
	if role != "reviewer" {
		t.Fatalf("role %v", role)
	}
	task, err = s.ReadTask(endpoint.Task)
	if err != nil {
		t.Fatal(err)
	}
	reviewerValue, _ := task.Get("reviewer")
	reviewer = reviewerValue.(*ordjson.Object)
	if asString(reviewer, "cwd") != terminal.Cwd {
		t.Fatalf("reviewer cwd %q differs from observed terminal cwd %q", asString(reviewer, "cwd"), terminal.Cwd)
	}
	if asString(reviewer, "address") != endpoint.Address {
		t.Fatal("pending reviewer endpoint was not adopted")
	}
	receipt, err := app.NativeReceipt(reviewer)
	if err != nil || receipt.Kind != "reviewer-init" {
		t.Fatalf("receipt %v %v", receipt, err)
	}
	if _, duplicate := task.Get("review_endpoint"); duplicate {
		t.Fatal("duplicate reviewer authority persisted")
	}
	if _, err = InitDesignated(DesignatedOpts{RuntimeRoot: root, Store: s, Ctx: endpoint.Object(), Role: "reviewer", Task: endpoint.Task}); err != nil {
		t.Fatal(err)
	}
	f.SetForeground(endpoint.Address, hcclient.Foreground{PID: 103, Name: "codex", Argv: spec.Argv, Cwd: root})
	if _, err = InitDesignated(DesignatedOpts{RuntimeRoot: root, Store: s, Ctx: endpoint.Object(), Role: "reviewer", Task: endpoint.Task}); err == nil {
		t.Fatal("reviewer replacement acquired existing receipt")
	}
}

func mustObject(value any) any { o, _ := ordjson.FromValue(value); return o }
