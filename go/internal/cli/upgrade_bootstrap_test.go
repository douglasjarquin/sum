package cli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/release"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/updatecmd"
)

func TestOrdinaryUpgradeFromExitedNativeCoordinator(t *testing.T) {
	clearFallbackIdentity(t)
	old := buildNativeBaseHelper(t)
	for _, boundary := range []string{"none", "prepared", "fenced", "owned", "retired", "selected", "aligned"} {
		t.Run(boundary, func(t *testing.T) {
			root, s, f, sha, env := bootstrapInstallation(t, old)
			target := filepath.Join(root, ".local/releases", sha)
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatal("target was already staged")
			}
			invokeOld := func(args ...string) ([]byte, error) {
				cmd := exec.Command(filepath.Join(root, "bin/sumctl"), append([]string{"--format", "json"}, args...)...)
				cmd.Env = env
				return cmd.CombinedOutput()
			}
			out, err := invokeOld("init")
			if err != nil {
				t.Fatalf("old ordinary init: %v %s", err, out)
			}
			if decodeObject(t, string(out))["role"] != "developer" {
				t.Fatalf("not the actual ordinary old entry: %s", out)
			}
			out, err = invokeOld("update", "stage", "--ref", sha, "--no-fetch")
			if err != nil {
				t.Fatalf("real old staging: %v %s", err, out)
			}
			raw, err := os.ReadFile(filepath.Join(target, "release.json"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), `"owner_schema"`) {
				t.Fatal("old decoder unexpectedly preserved the new owner capability")
			}
			if _, err := release.VerifyRelease(target, sha); err == nil {
				t.Fatal("candidate accepted lossy old manifest")
			}
			if err := os.Rename(target, target+".old-stage-evidence"); err != nil {
				t.Fatal(err)
			}
			ownerBefore, _ := os.ReadFile(filepath.Join(s.Home, "context.json"))
			interrupt := errors.New("simulated process interruption")
			updatecmd.TestBootstrapBoundary = func(phase string) error {
				if phase == boundary {
					return interrupt
				}
				return nil
			}
			t.Cleanup(func() { updatecmd.TestBootstrapBoundary = nil })
			var view *ordjson.Object
			if boundary == "none" {
				view = bootstrapDriver(t, root, sha, env, "")
			} else {
				view, err = updatecmd.Bootstrap(s, sha, true, true, "")
			}
			if boundary != "none" {
				if !errors.Is(err, interrupt) {
					t.Fatalf("boundary %s: %v", boundary, err)
				}
				journalRaw, e := ordjson.ReadFile(filepath.Join(root, ".local/bootstrap.json"))
				if e != nil {
					t.Fatal(e)
				}
				journal := journalRaw.(*ordjson.Object)
				op, _ := journal.Get("operation")
				updatecmd.TestBootstrapBoundary = nil
				fresh, e := store.Open(s.Root)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = updatecmd.Bootstrap(fresh, sha, true, true, "wrong-operation"); e == nil {
					t.Fatal("wrong recovery operation accepted")
				}
				view = bootstrapDriver(t, root, sha, env, op.(string))
				err = nil
			}
			if err != nil {
				t.Fatal(err)
			}
			claim, _ := view.Get("claim")
			if claim == "" || claim == nil {
				t.Fatalf("claim not returned to caller: %v", view)
			}
			manifest, err := release.VerifyRelease(target, sha)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := ordjson.MarshalCompact(manifest)
			if !strings.Contains(string(encoded), `"owner_schema"`) {
				t.Fatal("staging lost owner capability")
			}
			ownerAfter, _ := os.ReadFile(filepath.Join(s.Home, "context.json"))
			if string(ownerBefore) != string(ownerAfter) {
				t.Fatal("historical native owner changed")
			}
			cmd := exec.Command(filepath.Join(root, "bin/sumctl"), "--format", "json", "init", "--claim", claim.(string))
			cmd.Env = env
			out, err = cmd.CombinedOutput()
			if err != nil || decodeObject(t, string(out))["role"] != "coordinator" {
				t.Fatalf("same-conversation continuation: %v %s", err, out)
			}
			for _, call := range f.Calls() {
				if call.Method == "terminal.run" || call.Method == "terminal.send" {
					t.Fatalf("bootstrap created or steered another conversation: %v", call)
				}
			}
			journalRaw, e := ordjson.ReadFile(filepath.Join(root, ".local/bootstrap.json"))
			if e != nil {
				t.Fatal(e)
			}
			op, _ := journalRaw.(*ordjson.Object).Get("operation")
			replayed, e := updatecmd.Bootstrap(s, sha, true, true, op.(string))
			if e != nil {
				t.Fatal(e)
			}
			if token, _ := replayed.Get("claim"); token != claim {
				t.Fatal("lost-response replay changed claim")
			}
			if _, e := s.ClaimCoordinator("", "explicit test transfer", true); e != nil {
				t.Fatal(e)
			}
			if _, e := updatecmd.Bootstrap(s, sha, true, true, op.(string)); e == nil {
				t.Fatal("old bootstrap recovered a transferred claim")
			}
		})
	}
}

func bootstrapInstallation(t *testing.T, old string) (string, *store.Store, *hctest.Fixture, string, []string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "installation")
	if out, err := exec.Command("git", "clone", "--quiet", repoRoot(t), root).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v %s", err, out)
	}
	root, _ = filepath.EvalSymlinks(root)
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	sha := git("rev-parse", "HEAD")
	git("update-ref", "refs/remotes/origin/main", sha)
	git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	const oldSHA = "8a4300c646cb6dcea8ca6f19f9199268b3adb6e3"
	git("checkout", "--quiet", "--detach", oldSHA)
	s := nativeTestStore(t, filepath.Join(root, ".sum"))
	f := hctest.New(t)
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	e := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Socket: f.Socket, Session: "sum-test-bootstrap", Address: "t1", CreatedMS: 1, Role: "coordinator", Attempt: "old-native-owner", BindingGeneration: 1, Cwd: root}
	finished, exit := uint64(2), int32(0)
	f.SetTerminal(hcclient.Terminal{Address: e.Address, CreatedMS: 1, Cwd: root, State: "finished", FinishedMS: &finished, ExitCode: &exit})
	observed := incarnation.NativeEvidence{Endpoint: e.Native(), PID: 2147480000, Foreground: &hcclient.Foreground{PID: 2147480000, Name: "codex", Argv: []string{"codex"}, Cwd: root}}.Record(store.Now())
	owner, err := s.Register(e, "coordinator", nil, observed)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: e.Native(), Kind: "coordinator-init", Attempt: e.Attempt})
	owner.Set("native_receipt", receipt)
	if err := ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	if err := ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(e)+".json"), owner); err != nil {
		t.Fatal(err)
	}
	observer := e
	observer.Address, observer.Role, observer.Attempt = "t3", "observer", "observer-launch"
	f.SetTerminal(hcclient.Terminal{Address: "t3", CreatedMS: 1, Cwd: root, State: "live"})
	health := ordjson.NewObject()
	health.Set("schema", json.Number("2"))
	health.Set("launch_effect", "submitted")
	health.Set("observer", observer.Object())
	if err := ordjson.WriteFile(filepath.Join(s.Home, "hook/health.json"), health); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".local/bin"), 0755); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".local/bin/sumctl"), bytes, 0700); err != nil {
		t.Fatal(err)
	}
	deps := t.TempDir()
	config, err := os.ReadFile(filepath.Join(root, "config/hc-release.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(deps, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deps, "config/hc-release.json"), config, 0644); err != nil {
		t.Fatal(err)
	}
	hctest.InstallRuntime(t, deps)
	tools := t.TempDir()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	pythonWrapper := filepath.Join(tools, "python3")
	body := "#!/bin/sh\nif [ \"$2\" = --hc-only ]; then\n cp -R " + shquote.Quote(filepath.Join(deps, ".local/share")) + " .local/\n ln -s ../share/herdr-core .local/bin/hc\nelse\n exec " + shquote.Quote(python) + " \"$@\"\nfi\n"
	if err := os.WriteFile(pythonWrapper, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	mise := "#!/bin/sh\ncase \"$1\" in\ninstall) exit 0;;\nwhich) case \"$2\" in\ngo) printf '%s\\n' " + shquote.Quote(filepath.Join(runtime.GOROOT(), "bin/go")) + ";;\npython3) printf '%s\\n' " + shquote.Quote(pythonWrapper) + ";;\n*) printf '%s\\n' " + shquote.Quote(python) + ";;\nesac;;\n*) exit 3;;\nesac\n"
	if err := os.WriteFile(filepath.Join(tools, "mise"), []byte(mise), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SUM_INSTALL_ROOT", root)
	t.Setenv("SUM_STAGE_OFFLINE", "")
	t.Setenv("SUM_GO_BIN", filepath.Join(runtime.GOROOT(), "bin/go"))
	env := append(proc.ScrubbedEnv(), "SUM_INSTALL_ROOT="+root, "SUM_GO_BIN="+filepath.Join(runtime.GOROOT(), "bin/go"))
	cmd := exec.Command(filepath.Join(root, "bin/sumctl"), "--format", "json", "release", "stage", oldSHA)
	cmd.Env = env
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("stage actual old runtime: %v %s", e, out)
	}
	if e := os.Symlink(filepath.Join("releases", oldSHA), filepath.Join(root, ".local/current")); e != nil {
		t.Fatal(e)
	}
	return root, s, f, sha, env
}

func bootstrapDriver(t *testing.T, root, sha string, env []string, op string) *ordjson.Object {
	t.Helper()
	args := []string{filepath.Join(repoRoot(t), "scripts/upgrade.py"), "--installation", root, "--ref", sha, "--no-fetch", "--approve"}
	if op != "" {
		args = append(args, "--operation", op)
	}
	cmd := exec.Command("python3", args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ordinary bootstrap driver: %v %s", err, out)
	}
	raw, err := ordjson.Decode(out)
	if err != nil {
		t.Fatalf("bootstrap result: %v %s", err, out)
	}
	return raw.(*ordjson.Object)
}

func TestBootstrapInspectsRemainingWork(t *testing.T) {
	clearFallbackIdentity(t)
	root, s, f, sha, _ := bootstrapInstallation(t, buildNativeBaseHelper(t))
	inspect := func(want string) {
		t.Helper()
		view, err := updatecmd.Bootstrap(s, sha, true, false, "")
		if err != nil {
			t.Fatal(err)
		}
		blocker, _ := view.Get("blocker")
		if !strings.Contains(asString(blocker), want) {
			t.Fatalf("expected %q, got %v", want, blocker)
		}
	}
	owner, _ := s.Owner()
	worker := store.EndpointFromContext(owner)
	worker.Address, worker.Role, worker.Task, worker.Attempt, worker.Cwd = "t2", "worker", "t-abcdef123456", "worker-attempt", t.TempDir()
	fg := &hcclient.Foreground{PID: 2147480000, Name: "codex", Argv: []string{"codex"}, Cwd: worker.Cwd}
	if _, err := s.Register(worker, "worker", worker.Task, incarnation.NativeEvidence{Endpoint: worker.Native(), PID: 2147480000, Foreground: fg}.Record(store.Now())); err != nil {
		t.Fatal(err)
	}
	f.SetTerminal(hcclient.Terminal{Address: "t2", CreatedMS: 1, Cwd: worker.Cwd, State: "live"})
	inspect("conclusive finished")
	finished, exit := uint64(2), int32(0)
	f.SetTerminal(hcclient.Terminal{Address: "t2", CreatedMS: 1, Cwd: worker.Cwd, State: "finished", FinishedMS: &finished, ExitCode: &exit})
	survivor := exec.Command("sleep", "60")
	survivor.Dir = worker.Cwd
	if err := survivor.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if survivor.ProcessState == nil {
			_ = survivor.Process.Kill()
			_ = survivor.Wait()
		}
	})
	inspect("checkout still has process")
	_ = survivor.Process.Kill()
	_ = survivor.Wait()
	healthPath := filepath.Join(s.Home, "hook/health.json")
	raw, err := ordjson.ReadFile(healthPath)
	if err != nil {
		t.Fatal(err)
	}
	health := raw.(*ordjson.Object)
	health.Set("launch_effect", "unknown")
	if err := ordjson.WriteFile(healthPath, health); err != nil {
		t.Fatal(err)
	}
	inspect("unresolved observer launch")
	health.Set("launch_effect", "submitted")
	health.Set("observer", worker.Object())
	if err := ordjson.WriteFile(healthPath, health); err != nil {
		t.Fatal(err)
	}
	inspect("endpoint is not an observer")
	health.Set("observer", nil)
	health.Set("launch_effect", "refused")
	if err := ordjson.WriteFile(healthPath, health); err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", json.Number("2"))
	task.Set("id", worker.Task)
	task.Set("status", "preparing")
	task.Set("prepare_operation_id", "prepare-in-flight")
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	inspect("unresolved preparation")
	task.Set("status", "archived")
	task.Delete("prepare_operation_id")
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Tasks, worker.Task, "environment.json"), []byte(`{"schema":1,"task":"t-abcdef123456","services":[{"id":"server","state":"running"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	inspect("Owned service reservations")
	for _, role := range []string{"worker", "reviewer"} {
		t.Setenv("SUM_ROLE", role)
		var output strings.Builder
		cmd := NewRoot(filepath.Join(root, "bin/sumctl"), &output, &output)
		cmd.SetArgs([]string{"update", "bootstrap", "--ref", sha, "--no-fetch", "--approve"})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "task identity") {
			t.Fatalf("%s acquired bootstrap authority: %v", role, err)
		}
	}
}
