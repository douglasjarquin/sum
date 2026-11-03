package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launchtest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/release"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// This invokes the immutable pre-change updater, including its real target gates
// and observer handoff, rather than approximating them with the candidate updater.
func TestNativeBaseUpdaterSelectsCandidateWithEventsDisabled(t *testing.T) {
	old := buildNativeBaseHelper(t)
	source, _ := repoReference(t)
	root := filepath.Join(t.TempDir(), "installation")
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "clone", "--quiet", source, root).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v %s", err, out)
	}
	canonical, canonicalErr := filepath.EvalSymlinks(root)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	root = canonical
	sha := git("rev-parse", "HEAD")
	git("branch", "-f", "candidate-main", sha)
	git("checkout", "--quiet", "candidate-main")
	git("update-ref", "refs/remotes/origin/main", sha)
	git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	s := nativeTestStore(t, filepath.Join(root, ".sum"))
	f := hctest.New(t)
	spec, config, options := launchtest.QualifyCodex(t, f, root)
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	pid := uint32(100)
	terminal := hcclient.Terminal{Address: "t1", CreatedMS: 1, Cwd: root, State: "live", PID: &pid, Foreground: &hcclient.Foreground{PID: 101, Name: "codex", Argv: spec.Argv, Cwd: root}, Template: &spec.Name, Submit: &spec.Submit}
	f.SetTerminal(terminal)
	f.SetStatus("t1", "idle")
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Socket: f.Socket, Session: "sum-test-old-update", Address: "t1", CreatedMS: 1, Cwd: root, Role: "coordinator", Attempt: "base-updater", BindingGeneration: 1}
	observed := incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: terminal.Foreground}.Record(store.Now())
	owner := endpoint.Object()
	owner.Set("incarnation", observed)
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: "coordinator-init", Attempt: endpoint.Attempt})
	owner.Set("native_receipt", receipt)
	template, _ := ordjson.FromValue(returns.NativeTemplate{Spec: spec, ConfigHome: config, Options: options})
	owner.Set("native_template", template)
	registration, err := s.Register(endpoint, "coordinator", nil, observed)
	if err != nil {
		t.Fatal(err)
	}
	registration.Set("native_receipt", receipt)
	registration.Set("native_template", template)
	if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(endpoint)+".json"), registration); err != nil {
		t.Fatal(err)
	}
	if err = ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	// The old native-events preference is intentionally consumed by its old updater.
	if err = os.WriteFile(filepath.Join(s.Home, "settings.json"), []byte(`{"schema":1,"hook":{"enabled":false}}`), 0600); err != nil {
		t.Fatal(err)
	}
	hctest.InstallRuntime(t, root)
	oldBytes, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, ".local/bin/sumctl"), oldBytes, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, ".local/releases", sha)
	if err = os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	archive := exec.Command("git", "-C", root, "archive", "--format=tar", sha)
	extract := exec.Command("tar", "-xf", "-", "-C", target)
	pipe, err := archive.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	extract.Stdin = pipe
	if err = archive.Start(); err != nil {
		t.Fatal(err)
	}
	if out, err := extract.CombinedOutput(); err != nil {
		t.Fatalf("extract: %v %s", err, out)
	}
	if err = archive.Wait(); err != nil {
		t.Fatal(err)
	}
	hctest.InstallRuntime(t, target)
	candidate, err := os.ReadFile(filepath.Join(source, ".local/bin/sumctl"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(target, ".local/bin/sumctl"), candidate, 0700); err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"python3", "node", "gh", "quota-axi", "codegraph", "skills"} {
		if err = os.Symlink(python, filepath.Join(target, ".local/bin", tool)); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := release.BuildManifest(s, root, sha, target)
	if err != nil {
		t.Fatal(err)
	}
	if err = ordjson.WriteFile(filepath.Join(target, "release.json"), manifest); err != nil {
		t.Fatal(err)
	}
	if _, err = release.VerifyRelease(target, sha); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(root, ".local/bin/sumctl"), "--format", "json", "--home", s.Root, "update", "apply", "--ref", sha, "--no-fetch")
	cmd.Env = append(proc.ScrubbedEnv(), "SUM_INSTALL_ROOT="+root, "SUM_SESSION="+endpoint.Session, "HC_SESSION="+endpoint.Session, "HC_SOCKET="+endpoint.Socket, "HC_TERMINAL_ID=t1", "SUM_CORE_ID="+endpoint.CoreID, "SUM_ROLE=coordinator", "SUM_ATTEMPT="+endpoint.Attempt, "SUM_BINDING_GENERATION=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("immutable old updater refused candidate: %v\n%s", err, out)
	}
	var result map[string]any
	if err = json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if asMap(result["observer_activation"])["skipped"] != true {
		t.Fatalf("old observer handoff not explicitly skipped: %s", out)
	}
	selected, err := filepath.EvalSymlinks(filepath.Join(root, ".local/current"))
	if err != nil || selected != target {
		t.Fatalf("selection=%s err=%v target=%s", selected, err, target)
	}
	for _, call := range f.Calls() {
		if call.Method == "terminal.run" {
			t.Fatalf("old updater launched a native terminal: %v", call)
		}
	}
	// The ordinary candidate can read the selected bundle contract without HC.
	if _, err := release.ReadTargetContract(context.Background(), target); err != nil {
		t.Fatal(err)
	}
}
