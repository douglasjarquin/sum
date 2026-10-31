package roleinit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeInitDoesNotImplicitlyClaimCoordinator(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	f := hctest.New(t)
	pid := uint32(101)
	terminal := hcclient.Terminal{Address: "t1", CreatedMS: 1, State: "live", Cwd: root, PID: &pid, Foreground: &hcclient.Foreground{PID: 102, Name: "codex", Argv: []string{"codex"}, Cwd: root}}
	f.SetTerminal(terminal)
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Socket: f.Socket, Session: "bootstrap-test", Address: "t1", CreatedMS: 1, Cwd: root}
	view, err := InitDesignated(DesignatedOpts{RuntimeRoot: root, Store: s, Ctx: endpoint.Object()})
	if err != nil {
		t.Fatal(err)
	}
	role, _ := view.Get("role")
	if role != "developer" {
		t.Fatalf("implicit role = %v", role)
	}
	owner, err := s.Owner()
	if err != nil || owner != nil {
		t.Fatalf("owner = %v, %v", owner, err)
	}
	for _, call := range f.Calls() {
		if call.Method != "hello" && call.Method != "terminal.get" {
			t.Fatalf("unexpected mutation %s", call.Method)
		}
	}
}
func TestNativeInitRejectsLegacyContext(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	_, err = InitDesignated(DesignatedOpts{RuntimeRoot: root, Store: s, Ctx: ordjson.NewObject(), Role: "coordinator"})
	if err == nil {
		t.Fatal("legacy context granted authority")
	}
	owner, _ := s.Owner()
	if owner != nil {
		t.Fatal("owner written")
	}
}

func TestNativeCoordinatorLaunchAndReceipt(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(root, ".local/bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".local/bin/codex"), []byte("#!/bin/sh\nprintf '%s\\n' 'codex-cli 0.166.0'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "COORDINATOR.md"), []byte("Coordinator procedure.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git := exec.Command("git", "init", root)
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
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
	state.Set("instance", "lab-installation")
	if err = ordjson.WriteFile(filepath.Join(s.Home, "state.json"), state); err != nil {
		t.Fatal(err)
	}
	config := t.TempDir()
	spec, err := launch.PrepareHCTemplate(root, config, "codex", nil, launch.HCTemplateOptions{Cwd: root})
	if err != nil {
		t.Fatal(err)
	}
	f := hctest.New(t)
	f.SetTemplates([]hcclient.TemplateInfo{{Name: spec.Name, Command: templateCommand(spec)}})
	f.SetTemplateSubmit(spec.Name, "tab")
	f.SetTemplates([]hcclient.TemplateInfo{{Name: spec.Name, Command: "different command"}})
	if _, err = StartCoordinator(s, CoordinatorStartOpts{RuntimeRoot: root, SumctlPath: filepath.Join(root, "bin/sumctl"), Socket: f.Socket, Session: "coordinator-lab", CoreConfigHome: config}); err == nil {
		t.Fatal("mismatched selected-core command accepted")
	}
	for _, call := range f.Calls() {
		if call.Method == "terminal.run" {
			t.Fatal("bad core template launched a process")
		}
	}
	f.SetTemplates([]hcclient.TemplateInfo{{Name: spec.Name, Command: templateCommand(spec)}})
	result, err := StartCoordinator(s, CoordinatorStartOpts{RuntimeRoot: root, SumctlPath: filepath.Join(root, "bin/sumctl"), Socket: f.Socket, Session: "coordinator-lab", CoreConfigHome: config})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.Owner()
	if err != nil || owner != nil {
		t.Fatalf("native launch claimed role before receipt: %v %v", owner, err)
	}
	endpointValue, _ := result.Get("endpoint")
	e := store.EndpointFromContext(endpointValue.(*ordjson.Object))

	pendingPath := filepath.Join(s.Home, "coordinator-launch.json")
	savedValue, err := ordjson.ReadFile(pendingPath)
	if err != nil {
		t.Fatal(err)
	}
	saved := savedValue.(*ordjson.Object)
	pendingEndpoint := e
	pendingEndpoint.Address = ""
	pendingEndpoint.CreatedMS = 0
	saved.Set("effect", "claimed")
	saved.Set("endpoint", pendingEndpoint.Object())
	if err = ordjson.WriteFile(pendingPath, saved); err != nil {
		t.Fatal(err)
	}
	client := f.Client()
	terminal, err := client.Get(context.Background(), e.Address)
	if err != nil {
		t.Fatal(err)
	}
	pid := uint32(101)
	terminal.PID = &pid
	terminal.Foreground = &hcclient.Foreground{PID: 102, Name: "codex", Argv: spec.Argv, Cwd: root}
	f.SetTerminal(terminal)
	view, err := InitDesignated(DesignatedOpts{RuntimeRoot: root, Store: s, Ctx: e.Object()})
	if err != nil {
		t.Fatal(err)
	}
	role, _ := view.Get("role")
	if role != "coordinator" {
		t.Fatalf("role %v", role)
	}
	if err = app.RequireCoordinator(s, e.Object()); err != nil {
		t.Fatal(err)
	}

	if _, err = InitDesignated(DesignatedOpts{RuntimeRoot: root, Store: s, Ctx: e.Object()}); err != nil {
		t.Fatal(err)
	}
	owner, err = s.Owner()
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := app.NativeReceipt(owner)
	if err != nil || receipt.Attempt != e.Attempt {
		t.Fatalf("receipt changed: %v %v", receipt, err)
	}
	f.SetForeground(e.Address, hcclient.Foreground{PID: 103, Name: "codex", Argv: spec.Argv, Cwd: root})
	if err = app.RequireCoordinator(s, e.Object()); err == nil {
		t.Fatal("replacement foreground inherited coordinator authority")
	}
	beforeOwner, _ := ordjson.MarshalCompact(owner)
	if _, err = InitDesignated(DesignatedOpts{RuntimeRoot: root, Store: s, Ctx: e.Object(), Role: "coordinator", Reclaim: true, CoreConfigHome: config}); err == nil || !strings.Contains(err.Error(), "only a native finished terminal") {
		t.Fatalf("live foreground replacement reclaim = %v", err)
	}
	afterOwner, err := s.Owner()
	if err != nil {
		t.Fatal(err)
	}
	afterBytes, _ := ordjson.MarshalCompact(afterOwner)
	if string(beforeOwner) != string(afterBytes) {
		t.Fatal("denied live reclaim changed owner")
	}
	if _, err = StartCoordinator(s, CoordinatorStartOpts{RuntimeRoot: root, SumctlPath: filepath.Join(root, "bin/sumctl"), Socket: f.Socket, Session: "coordinator-lab", CoreConfigHome: config}); err == nil {
		t.Fatal("second coordinator launch accepted")
	}
	count := 0
	for _, call := range f.Calls() {
		if call.Method == "terminal.run" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("launch count %d", count)
	}
}

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

func TestCoordinatorLostResponseReconcilesOnlyExactInitReceipt(t *testing.T) {
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: "machine", CoreID: "core", Socket: "/tmp/coordinator-receipt.sock", Session: "receipt-lab", Address: "t1", CreatedMS: 1, Role: "coordinator", Attempt: "coordinator-attempt", BindingGeneration: 1}
	receipt := incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: "coordinator-init", Attempt: endpoint.Attempt}
	record := ordjson.NewObject()
	record.Set("endpoint", endpoint.Object())
	record.Set("effect", "uncertain")
	record.Set("error", "native launch response lost")
	record.Set("native_receipt", mustObject(receipt))
	expected := endpoint
	expected.Address = ""
	expected.CreatedMS = 0
	reconcileCoordinatorReceipt(record, expected)
	if value, _ := record.Get("effect"); value != "uncertain" {
		t.Fatalf("native uncertainty erased: %v", value)
	}
	if _, exists := record.Get("first_message"); exists {
		t.Fatal("application receipt fabricated first-message outcome")
	}
	value, exists := record.Get("application_reconciliation")
	if !exists || asString(value.(*ordjson.Object), "state") != "receipt-confirmed" {
		t.Fatalf("receipt not reconciled: %v", value)
	}
	wrong := ordjson.NewObject()
	wrong.Set("endpoint", endpoint.Object())
	wrongReceipt := receipt
	wrongReceipt.Endpoint.CreatedMS++
	wrong.Set("native_receipt", mustObject(wrongReceipt))
	reconcileCoordinatorReceipt(wrong, expected)
	if _, exists := wrong.Get("application_reconciliation"); exists {
		t.Fatal("mismatched terminal creation receipt reconciled")
	}
}
