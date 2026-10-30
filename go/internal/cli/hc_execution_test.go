package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/prepare"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestHCPrepareReconcileAndStartThroughCLI(t *testing.T) {
	runtimeRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	fixture := hctest.New(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(root, "repo")
	if err = os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(cwd string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		data, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %v %s", args, e, data)
		}
		return strings.TrimSpace(string(data))
	}
	git(repo, "init", "-b", "main")
	if err = os.WriteFile(filepath.Join(repo, "README"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(repo, "add", "README")
	git(repo, "commit", "-m", "base")
	remote := filepath.Join(root, "origin.git")
	git(root, "clone", "--bare", repo, remote)
	git(repo, "remote", "add", "origin", remote)
	codex := filepath.Join(root, "codex")
	if err = os.WriteFile(codex, []byte("#!/bin/sh\nprintf '%s\\n' 'codex-cli 0.166.0'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_CODEX_BIN", codex)
	config, configHome := filepath.Join(root, "config"), filepath.Join(root, "codex-home")
	if err = os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(configHome, 0700); err != nil {
		t.Fatal(err)
	}
	options := launch.HCTemplateOptions{Cwd: runtimeRoot, CodexHome: configHome}
	spec, err := launch.PrepareHCTemplate(runtimeRoot, config, "codex", nil, options)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	machine, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	coordinator := store.Endpoint{Schema: 2, Backend: "hc", Machine: machine.ID, CoreID: fixture.CoreID, Socket: fixture.Socket, Session: "sum-test", Address: "t1", CreatedMS: 1, Role: "coordinator", Attempt: "bootstrap", BindingGeneration: 1, Cwd: runtimeRoot}
	pid := uint32(100000)
	fg := &hcclient.Foreground{PID: 100001, Name: "codex", Argv: []string{codex}, Cwd: runtimeRoot}
	fixture.SetTerminal(hcclient.Terminal{Address: "t1", CreatedMS: 1, Cwd: runtimeRoot, State: "live", PID: &pid, Foreground: fg})
	bound := incarnation.NativeEvidence{Endpoint: coordinator.Native(), PID: pid, Foreground: fg}
	owner := coordinator.Object()
	owner.Set("incarnation", bound.Record(store.Now()))
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: coordinator.Native(), Kind: "coordinator-init", Attempt: "bootstrap"})
	owner.Set("native_receipt", receipt)
	provenance, _ := ordjson.FromValue(prepare.NativeTemplate{Spec: spec, ConfigHome: config, Options: options})
	owner.Set("native_template", provenance)
	if _, err = s.Register(coordinator, "coordinator", nil, bound.Record(store.Now())); err != nil {
		t.Fatal(err)
	}
	if err = ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"SUM_HOME": "", "SUM_INSTALL_ROOT": runtimeRoot, "SUM_SESSION": "sum-test", "SUM_CORE_ID": fixture.CoreID, "HC_SOCKET": fixture.Socket, "HC_TERMINAL_ID": "t1", "SUM_ROLE": "coordinator", "SUM_ATTEMPT": "bootstrap", "SUM_BINDING_GENERATION": "1"} {
		t.Setenv(key, value)
	}
	run := func(args ...string) (map[string]any, error) {
		var out, errOut bytes.Buffer
		command := NewRoot(filepath.Join(runtimeRoot, "bin/sumctl"), &out, &errOut)
		command.SetArgs(append([]string{"--home", s.Root, "--format", "json"}, args...))
		e := command.ExecuteContext(context.Background())
		if e != nil {
			return nil, e
		}
		return decodeCLIMap(t, out.String()), nil
	}
	brief := filepath.Join(root, "approved.md")
	if err = os.WriteFile(brief, []byte("Only the approved task."), 0600); err != nil {
		t.Fatal(err)
	}
	prepared, err := run("prepare", "--repo", repo, "--base", "main", "--brief", brief, "--harness", "codex", "--approved")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := prepared["id"].(string)
	if id == "" {
		t.Fatal("no task id")
	}
	if _, err = run("prepare", "--reconcile", id); err != nil {
		t.Fatal(err)
	}
	task, err := s.ReadTask(id)
	if err != nil {
		t.Fatal(err)
	}
	rawLaunch, _ := task.Get("launch")
	launchObj := rawLaunch.(*ordjson.Object)
	rawArgs, _ := launchObj.Get("argv")
	var argv []string
	for _, a := range rawArgs.([]any) {
		argv = append(argv, a.(string))
	}
	checkout, _ := task.Get("worktree")
	child, err := launch.PrepareHCTemplate(runtimeRoot, config, "codex", argv, launch.HCTemplateOptions{Cwd: checkout.(string), CodexHome: configHome})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(config, "herdr-core/templates", child.Name+".md"))
	if err != nil {
		t.Fatal(err)
	}
	command := ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "command: ") {
			command = strings.TrimPrefix(line, "command: ")
		}
	}
	fixture.SetTemplates([]hcclient.TemplateInfo{{Name: child.Name, Command: command}})
	fixture.SetTemplateSubmit(child.Name, "tab")
	started := true
	fixture.QueueFault("terminal.run", hctest.Fault{FirstMessage: &hcclient.FirstMessage{Sent: true, Started: &started}})
	result, err := run("start", id)
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "starting" {
		t.Fatal("native started flag was adopted as application receipt")
	}
	current, err := s.ReadTask(id)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := reservations.Worker(current)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := worker.Get("state")
	if state != "starting" {
		t.Fatal("attempt adopted without worker-init receipt")
	}
	if _, err = run("start", id); err == nil {
		t.Fatal("repeated start replayed the native launch")
	}
	runs := 0
	for _, call := range fixture.Calls() {
		if call.Method == "terminal.run" {
			runs++
		}
		if call.Method == "terminal.send" {
			t.Fatal("CLI blindly resent first message")
		}
	}
	if runs != 1 {
		t.Fatalf("native launches=%d", runs)
	}
}
