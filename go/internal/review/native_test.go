package review

import (
	"context"
	"github.com/douglasjarquin/sum/go/internal/evidenceview"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/roleinit"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/taskcheckout"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func reviewerLab(t *testing.T) (*store.Store, *ordjson.Object, store.Endpoint, *hctest.Fixture) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	state, err := ordjson.ReadFile(filepath.Join(s.Home, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	state.(*ordjson.Object).Set("instance", "installation-review-native-test")
	if err = ordjson.WriteFile(filepath.Join(s.Home, "state.json"), state); err != nil {
		t.Fatal(err)
	}
	f := hctest.New(t)
	task := ordjson.NewObject()
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("worktree", t.TempDir())
	task.Set("schema", 2)
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	ep := store.Endpoint{Schema: 2, Backend: "hc", Machine: "test", Session: "lab", CoreID: f.CoreID, Socket: f.Socket, Task: "t-aaaaaaaaaaaa", Role: "reviewer", Attempt: "attempt-review", BindingGeneration: 1, Cwd: asString(task, "worktree")}
	return s, task, ep, f
}
func TestNativeReviewerCreationPersistsUnsentFirstMessage(t *testing.T) {
	s, task, ep, f := reviewerLab(t)
	no := false
	f.QueueFault("terminal.run", hctest.Fault{FirstMessage: &hcclient.FirstMessage{Sent: false, Started: &no}})
	reviewer := ep.Object()
	reviewer.Set("candidate", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	result, err := launchPreparedNative(context.Background(), s, task, ep, reviewer, hcclient.RunParams{Argv: []string{"qualified", "review pointer"}, Cwd: ep.Cwd, OutliveParent: true, NoNotify: true})
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := s.ReadTask(ep.Task)
	endpoint := store.EndpointFromContext(objectField(saved, "reviewer"))
	if endpoint.Address == "" {
		t.Fatal("created terminal lost when kickoff unsent")
	}
	if asString(objectField(result, "native_launch"), "state") != "created" {
		t.Fatalf("result %v", result)
	}
	if _, err = launchPreparedNative(context.Background(), s, task, ep, reviewer, hcclient.RunParams{}); err == nil {
		t.Fatal("duplicate launch allowed")
	}
	if len(f.Terminals()) != 1 {
		t.Fatal("duplicate terminal")
	}
}
func TestNativeReviewerLostLaunchResponseCannotReplay(t *testing.T) {
	s, task, ep, f := reviewerLab(t)
	f.QueueFault("terminal.run", hctest.Fault{LoseResponse: true})
	_, err := launchPreparedNative(context.Background(), s, task, ep, ep.Object(), hcclient.RunParams{Argv: []string{"review"}, Cwd: ep.Cwd, OutliveParent: true, NoNotify: true})
	if err == nil {
		t.Fatal("lost response accepted")
	}
	saved, _ := s.ReadTask(ep.Task)
	if asString(objectField(objectField(saved, "reviewer"), "native_launch"), "state") != "uncertain" {
		t.Fatalf("uncertainty lost: %v", saved)
	}
	_, err = launchPreparedNative(context.Background(), s, task, ep, ep.Object(), hcclient.RunParams{})
	if err == nil {
		t.Fatal("uncertain launch replayed")
	}
	if len(f.Terminals()) != 1 {
		t.Fatal("duplicate")
	}
}

func TestReviewerEndpointAuthorityDoesNotUseLabelsOrLegacyPane(t *testing.T) {
	_, task, ep, _ := reviewerLab(t)
	ep.Address = "t7"
	ep.CreatedMS = 3

	task.Set("reviewer", ep.Object())
	if got := endpointRole(machine.Identity{}, task, ep.Object()); got != "reviewer" {
		t.Fatalf("current endpoint role %s", got)
	}
	for _, change := range []func(*store.Endpoint){func(e *store.Endpoint) { e.CoreID = "other" }, func(e *store.Endpoint) { e.Attempt = "other" }, func(e *store.Endpoint) { e.BindingGeneration++ }, func(e *store.Endpoint) { e.Role = "worker" }} {
		other := ep
		change(&other)
		if got := endpointRole(machine.Identity{}, task, other.Object()); got != "other" {
			t.Fatalf("replacement gained authority: %s", got)
		}
	}
	legacy := ordjson.NewObject()
	legacy.Set("machine", ep.Machine)
	legacy.Set("session", ep.Session)
	legacy.Set("pane", "t7")
	if got := endpointRole(machine.Identity{}, task, legacy); got != "other" {
		t.Fatal("legacy pane adopted reviewer")
	}
}
func TestReviewerKickoffPinsCandidateAndReviewerInit(t *testing.T) {
	text := kickoffPrompt("/tmp/sum helper", "t-aaaaaaaaaaaa", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "/tmp/pinned skill", true)
	for _, required := range []string{"init --role reviewer --task t-aaaaaaaaaaaa", "candidate aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "/tmp/pinned skill", "read-only", "do not edit", "--policy-reviewed"} {
		if !strings.Contains(text, required) {
			t.Fatalf("prompt missing %q: %s", required, text)
		}
	}
}

func recoveryLab(t *testing.T) (*store.Store, *ordjson.Object, store.Endpoint, *hctest.Fixture) {
	t.Helper()
	s, task, ep, f := reviewerLab(t)
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-q", "--allow-empty", "-m", "initial"}} {
		cmd := exec.Command("git", append([]string{"-C", ep.Cwd}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	runtime := t.TempDir()
	binary := filepath.Join(runtime, ".local/bin/codex")
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho 'codex-cli 0.166.0'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	config := t.TempDir()
	options := launch.HCTemplateOptions{Cwd: ep.Cwd, CodexHome: t.TempDir()}
	spec, err := launch.PrepareHCTemplate(runtime, config, "codex", nil, options)
	if err != nil {
		t.Fatal(err)
	}
	f.SetTemplates([]hcclient.TemplateInfo{{Name: spec.Name, Command: templateCommand(spec.Argv)}})
	f.SetTemplateSubmit(spec.Name, "tab")
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	ep.Machine = host.ID
	ep.Address = "t7"
	ep.CreatedMS = 7
	coordinator := ep
	coordinator.Address = "t8"
	coordinator.Role = "coordinator"
	coordinator.Task = ""
	coordinator.Attempt = "coord"
	task.Set("parent", coordinator.Object())
	if err = ordjson.WriteFile(filepath.Join(s.Home, "context.json"), coordinator.Object()); err != nil {
		t.Fatal(err)
	}
	candidate := evidenceview.CurrentCandidate(task)
	reviewer := ep.Object()
	reviewer.Set("candidate", candidate)
	nt, _ := ordjson.FromValue(returns.NativeTemplate{Spec: spec, ConfigHome: config, Options: options})
	reviewer.Set("native_template", nt)
	instance, err := s.Instance()
	if err != nil || instance == "" || instance == s.Root {
		t.Fatalf("invalid test installation identity: %q %v", instance, err)
	}
	labels := map[string]string{"sum.installation": instance, "sum.task": ep.Task, "sum.role": "reviewer", "sum.attempt": ep.Attempt}
	params := hcclient.RunParams{Argv: []string{spec.Name, "sealed opening pointer"}, Labels: labels, Env: map[string]string{"SUM_ATTEMPT": ep.Attempt, "SUM_TASK": ep.Task, "SUM_ROLE": "reviewer", "SUM_CORE_ID": ep.CoreID, "HC_SOCKET": ep.Socket}}
	intent := ordjson.NewObject()
	intent.Set("state", "created")
	intent.Set("attempt", ep.Attempt)
	value, _ := ordjson.FromValue(params)
	intent.Set("params", value)
	result, _ := ordjson.FromValue(hcclient.RunResult{FirstMessage: &hcclient.FirstMessage{Sent: false}})
	intent.Set("result", result)
	reviewer.Set("native_launch", intent)
	prompt := ordjson.NewObject()
	prompt.Set("state", "not-delivered")
	prompt.Set("candidate", candidate)
	reviewer.Set("prompt", prompt)
	task.Set("reviewer", reviewer)
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	pid := uint32(800001)
	submit := "tab"
	f.SetTerminal(hcclient.Terminal{Address: ep.Address, CreatedMS: ep.CreatedMS, Cwd: ep.Cwd, State: "live", Template: &spec.Name, Submit: &submit, Labels: labels, PID: &pid, Foreground: &hcclient.Foreground{PID: 800002, Argv: spec.Argv, Cwd: ep.Cwd}, Status: &hcclient.Status{State: "idle"}})
	return s, coordinator.Object(), ep, f
}
func TestReviewerExplicitUnsentRedeliveryUsesNativeGuardAndNoLaunch(t *testing.T) {
	for _, status := range []string{"idle", "done"} {
		t.Run(status, func(t *testing.T) {
			s, ctx, ep, f := recoveryLab(t)
			f.SetStatus(ep.Address, status)
			no := false
			f.QueueFault("terminal.send", hctest.Fault{SendResult: &hcclient.SendResult{Started: &no}})
			out, err := Redeliver(s, ctx, ep.Task, ep.Attempt)
			if err != nil {
				t.Fatal(err)
			}
			if asString(objectField(out, "prompt"), "state") != "submitted" {
				t.Fatal("started:false falsely classified input")
			}
			if len(f.Inputs(ep.Address)) != 1 || f.Inputs(ep.Address)[0] != "sealed opening pointer" {
				t.Fatal("wrong input")
			}
			for _, call := range f.Calls() {
				if call.Method == "terminal.run" {
					t.Fatal("recovery launched duplicate")
				}
				if call.Method == "terminal.send" {
					guard, _ := call.Params["if"].(string)
					if (status == "idle" && guard != "idle") || (status == "done" && guard != "") {
						t.Fatalf("wrong native guard: %v", call)
					}
				}
			}
			f.AssertNoForce(t)
			if _, err = Redeliver(s, ctx, ep.Task, ep.Attempt); err == nil {
				t.Fatal("submitted recovery repeated")
			}
			saved, _ := s.ReadTask(ep.Task)
			if err = validateNativeReview(s, ep.Task, asString(objectField(saved, "reviewer"), "candidate"), "", ep.Object()); err == nil {
				t.Fatal("send granted role authority without init receipt")
			}
		})
	}
}
func TestReviewerUncertainRecoveryIsNotReplayed(t *testing.T) {
	s, ctx, ep, f := recoveryLab(t)
	f.QueueFault("terminal.send", hctest.Fault{LoseResponse: true})
	out, err := Redeliver(s, ctx, ep.Task, ep.Attempt)
	if err != nil {
		t.Fatal(err)
	}
	if asString(objectField(out, "prompt"), "state") != "uncertain" {
		t.Fatal("ambiguous input lost")
	}
	if _, err = Redeliver(s, ctx, ep.Task, ep.Attempt); err == nil {
		t.Fatal("ambiguous input replayed")
	}
	if len(f.Inputs(ep.Address)) != 1 {
		t.Fatal("duplicate input")
	}
}
func TestReviewerRecoveryRefusesWrongAttemptAndBusyProcess(t *testing.T) {
	s, ctx, ep, f := recoveryLab(t)
	if _, err := Redeliver(s, ctx, ep.Task, "wrong"); err == nil {
		t.Fatal("wrong attempt accepted")
	}
	for _, status := range []string{"working", "blocked", ""} {
		f.SetStatus(ep.Address, status)
		if _, err := Redeliver(s, ctx, ep.Task, ep.Attempt); err == nil {
			t.Fatalf("status %s accepted", status)
		}
	}
	if len(f.Inputs(ep.Address)) != 0 {
		t.Fatal("busy recipient received input")
	}
}

func TestNativeReviewerLaunchEndToEndUsesTaskCheckoutAndManagedTemplate(t *testing.T) {
	s, ctx, _, f := recoveryLab(t)
	task, _ := s.ReadTask("t-aaaaaaaaaaaa")
	old := objectField(task, "reviewer")
	var template returns.NativeTemplate
	if err := decodeObject(objectField(old, "native_template"), &template); err != nil {
		t.Fatal(err)
	}
	runtime := filepath.Dir(filepath.Dir(filepath.Dir(template.Spec.Binary)))
	skill := filepath.Join(runtime, "skills/sum-deliver/SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("# Independent reviewer\nRead the candidate.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	base := evidenceview.CurrentCandidate(task)
	intent, err := taskcheckout.Plan(s.Root, asString(task, "worktree"), asString(task, "id"), base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = taskcheckout.Create(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	value, _ := ordjson.FromValue(intent)
	task.Set("checkout_intent", value)
	task.Set("worktree", intent.Path)
	task.Delete("reviewer")
	host, _ := s.Machine()
	task.Set("machine", host.ID)
	dir, _ := s.TaskPath(asString(task, "id"))
	brief := filepath.Join(dir, "brief.md")
	if err = os.WriteFile(brief, []byte("Approved scope\n"), 0600); err != nil {
		t.Fatal(err)
	}
	task.Set("brief_path", brief)
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	owner := ctx
	nativeTemplate, _ := ordjson.FromValue(template)
	owner.Set("native_template", nativeTemplate)
	if err = ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	f.SetTemplates([]hcclient.TemplateInfo{{Name: template.Spec.Name, Command: templateCommand(template.Spec.Argv)}})
	f.SetTemplateSubmit(template.Spec.Name, "tab")
	no := false
	f.QueueFault("terminal.run", hctest.Fault{FirstMessage: &hcclient.FirstMessage{Sent: true, Started: &no}})
	out, err := Launch(s, ctx, runtime, "/tmp/sumctl", LaunchArgs{Task: asString(task, "id")})
	if err != nil {
		t.Fatal(err)
	}
	reviewer := objectField(out, "reviewer")
	if asString(objectField(reviewer, "prompt"), "state") != "submitted" {
		t.Fatal("firstmessage started:false treated as failure")
	}
	if asString(reviewer, "skill_sha256") == "" {
		t.Fatal("procedure was not pinned")
	}
	oldEndpoint := store.EndpointFromContext(reviewer)
	oldTerminal := f.Terminals()[0]
	for _, terminal := range f.Terminals() {
		if terminal.Address == oldEndpoint.Address {
			oldTerminal = terminal
		}
	}
	pid := uint32(800011)
	oldTerminal.PID = &pid
	oldTerminal.Foreground = &hcclient.Foreground{PID: 800012, Argv: template.Spec.Argv, Cwd: intent.Path}
	f.SetTerminal(oldTerminal)
	initialized, err := roleinit.InitDesignated(roleinit.DesignatedOpts{RuntimeRoot: runtime, SumctlPath: "/tmp/sumctl", Store: s, Ctx: oldEndpoint.Object(), Role: "reviewer", Task: oldEndpoint.Task})
	if err != nil {
		t.Fatal(err)
	}
	if asString(initialized, "role") != "reviewer" {
		t.Fatal("launched reviewer did not initialize as reviewer")
	}
	registration, err := s.Registration(oldEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	if asString(objectField(registration, "native_receipt"), "kind") != "reviewer-init" {
		t.Fatal("launched reviewer has no matching application init receipt")
	}
	f.Exit(oldEndpoint.Address, 0)
	replacement, err := Launch(s, ctx, runtime, "/tmp/sumctl", LaunchArgs{Task: oldEndpoint.Task})
	if err != nil {
		t.Fatal(err)
	}
	if asString(objectField(replacement, "reviewer"), "address") == oldEndpoint.Address {
		t.Fatal("stopped reviewer endpoint reused")
	}
	saved, _ := s.ReadTask(oldEndpoint.Task)
	items, _ := saved.Get("evidence")
	foundRebind := false
	for _, raw := range items.([]any) {
		if asString(objectValue(raw), "kind") == "reviewer-rebind" {
			foundRebind = true
		}
	}
	if !foundRebind {
		t.Fatal("reviewer replacement lost stop proof")
	}
	for _, call := range f.Calls() {
		if call.Method == "terminal.run" {
			instance, err := s.Instance()
			if err != nil {
				t.Fatal(err)
			}
			labels := call.Params["labels"].(map[string]any)
			if labels["sum.installation"] != instance || labels["sum.installation"] == s.Root {
				t.Fatalf("reviewer used a route instead of installation identity: %v", labels)
			}
			if call.Params["outlive_parent"] != true || call.Params["no_notify"] != true || call.Params["keep"] != false || call.Params["rm"] != false {
				t.Fatalf("managed flags %v", call.Params)
			}
			if len(call.Params["name"].(string)) > 32 {
				t.Fatal("native name exceeds limit")
			}
			args := call.Params["argv"].([]any)
			if !strings.Contains(args[1].(string), "init --role reviewer") || !strings.Contains(args[1].(string), base) {
				t.Fatal("opening pointer not candidate/init-bound")
			}
		}
	}
}

func TestCandidateBoundReviewerEvidenceDischargesAmbiguousKickoff(t *testing.T) {
	s, _, ep, f := recoveryLab(t)
	task, _ := s.ReadTask(ep.Task)
	reviewer := objectField(task, "reviewer")
	candidate := asString(reviewer, "candidate")
	objectField(reviewer, "prompt").Set("state", "uncertain")
	objectField(reviewer, "native_launch").Set("state", "uncertain")
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	terminal := f.Terminals()[0]
	binding := (incarnation.NativeEvidence{Endpoint: ep.Native(), PID: *terminal.PID, Foreground: terminal.Foreground}).Record(store.Now())
	reviewer.Set("incarnation", binding)
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	registration, err := s.Register(ep, "reviewer", ep.Task, binding)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: ep.Native(), Kind: "reviewer-init", Attempt: ep.Attempt})
	registration.Set("native_receipt", receipt)
	if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(ep)+".json"), registration); err != nil {
		t.Fatal(err)
	}
	if err = validateNativeReview(s, ep.Task, candidate, "", ep.Object()); err != nil {
		t.Fatal(err)
	}
	if _, _, err = record(s, ep.Task, "comment", candidate, "", "reviewed independently", false, nil, nil, ep.Object()); err != nil {
		t.Fatal(err)
	}
	saved, _ := s.ReadTask(ep.Task)
	prompt := objectField(objectField(saved, "reviewer"), "prompt")
	if asString(prompt, "state") != "applied" || asString(objectField(prompt, "application_receipt"), "candidate") != candidate {
		t.Fatal("matching review did not discharge input")
	}
	launch := objectField(objectField(saved, "reviewer"), "native_launch")
	if asString(launch, "state") != "created" || asString(launch, "reconciled_from") != "uncertain" {
		t.Fatal("receipt reconciliation lost uncertainty history")
	}
}

func TestReviewerRecoveryRequiresSelectedCoreTemplateCommand(t *testing.T) {
	s, ctx, ep, f := recoveryLab(t)
	task, err := s.ReadTask(ep.Task)
	if err != nil {
		t.Fatal(err)
	}
	name := asString(objectField(objectField(objectField(task, "reviewer"), "native_template"), "spec"), "name")
	f.SetTemplates([]hcclient.TemplateInfo{{Name: name, Command: "unqualified replacement command"}})
	if _, err = Redeliver(s, ctx, ep.Task, ep.Attempt); err == nil || !strings.Contains(err.Error(), "selected HC core does not advertise") {
		t.Fatalf("mismatched core template accepted: %v", err)
	}
	if len(f.Inputs(ep.Address)) != 0 {
		t.Fatal("unqualified core command received opening input")
	}
	saved, err := s.ReadTask(ep.Task)
	if err != nil {
		t.Fatal(err)
	}
	if asString(objectField(objectField(saved, "reviewer"), "prompt"), "state") != "not-delivered" {
		t.Fatal("qualification failure claimed or submitted opening input")
	}
}

func TestReviewerLaunchAndRecoveryRequirePermanentInstallationIdentity(t *testing.T) {
	s, ctx, ep, f := recoveryLab(t)
	value, err := ordjson.ReadFile(filepath.Join(s.Home, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	value.(*ordjson.Object).Delete("instance")
	if err = ordjson.WriteFile(filepath.Join(s.Home, "state.json"), value); err != nil {
		t.Fatal(err)
	}
	before := len(f.Calls())
	if _, err = Launch(s, ctx, "/tmp/not-used", "/tmp/sumctl", LaunchArgs{Task: ep.Task}); err == nil || !strings.Contains(err.Error(), "installation identity is missing") {
		t.Fatalf("launch without permanent identity: %v", err)
	}
	if _, err = Redeliver(s, ctx, ep.Task, ep.Attempt); err == nil || !strings.Contains(err.Error(), "installation identity is missing") {
		t.Fatalf("recovery without permanent identity: %v", err)
	}
	if len(f.Calls()) != before || len(f.Inputs(ep.Address)) != 0 {
		t.Fatal("missing installation identity caused native I/O")
	}
}

func TestReviewerRecoveryRejectsFilesystemPathAsInstallationIdentity(t *testing.T) {
	s, ctx, ep, f := recoveryLab(t)
	task, err := s.ReadTask(ep.Task)
	if err != nil {
		t.Fatal(err)
	}
	params := objectField(objectField(objectField(task, "reviewer"), "native_launch"), "params")
	objectField(params, "labels").Set("sum.installation", s.Root)
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	terminal := f.Terminals()[0]
	terminal.Labels["sum.installation"] = s.Root
	f.SetTerminal(terminal)
	if _, err = Redeliver(s, ctx, ep.Task, ep.Attempt); err == nil || !strings.Contains(err.Error(), "ownership label differs: sum.installation") {
		t.Fatalf("filesystem path accepted as installation identity: %v", err)
	}
	if len(f.Inputs(ep.Address)) != 0 {
		t.Fatal("wrong installation identity received opening input")
	}
}
