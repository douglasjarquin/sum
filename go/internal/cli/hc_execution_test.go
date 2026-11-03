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
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
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
	_, err = launch.PrepareHCTemplate(runtimeRoot, config, "codex", nil, options)
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
	session, err := s.ClaimCoordinator("", "ordinary HC execution", false)
	if err != nil {
		t.Fatal(err)
	}
	route, _ := ordjson.FromValue(map[string]any{"socket": fixture.Socket, "session": "sum-test", "config_home": config, "state_home": filepath.Join(root, "hc-state"), "core_id": fixture.CoreID})
	if err = ordjson.WriteFile(filepath.Join(s.Root, "runtime-route.json"), route); err != nil {
		t.Fatal(err)
	}
	settings, _ := ordjson.FromValue(map[string]any{"schema": 1, "worker": map[string]any{"harness": "codex"}, "template": map[string]any{"codex_home": configHome}})
	if err = ordjson.WriteFile(filepath.Join(s.Home, "settings.json"), settings); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"SUM_HOME", "SUM_SESSION", "SUM_CORE_ID", "HC_SOCKET", "HC_TERMINAL_ID", "SUM_ROLE", "SUM_TASK", "SUM_ATTEMPT", "SUM_BINDING_GENERATION"} {
		t.Setenv(key, "")
	}
	t.Setenv("SUM_INSTALL_ROOT", runtimeRoot)
	run := func(args ...string) (map[string]any, error) {
		var out, errOut bytes.Buffer
		command := NewRoot(filepath.Join(runtimeRoot, "bin/sumctl"), &out, &errOut)
		command.SetArgs(append([]string{"--home", s.Root, "--format", "json", "--claim", session.Token()}, args...))
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
	runs, sends := 0, 0
	for _, call := range fixture.Calls() {
		if call.Method == "terminal.run" {
			runs++
		}
		if call.Method == "terminal.send" {
			sends++
		}
	}
	if runs != 1 || sends != 1 {
		t.Fatalf("native launches=%d openings=%d", runs, sends)
	}
}
