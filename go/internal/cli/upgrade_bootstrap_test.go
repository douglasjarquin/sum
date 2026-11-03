package cli

import (
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
			view, err := updatecmd.Bootstrap(s, sha, true, true, "")
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
				view, err = updatecmd.Bootstrap(fresh, sha, true, true, op.(string))
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
			if !strings.Contains(string(encoded), `"owner_schema":[2]`) {
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
	s := nativeTestStore(t, filepath.Join(root, ".sum"))
	f := hctest.New(t)
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	e := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Socket: f.Socket, Session: "sum-test-bootstrap", Address: "old-coordinator", CreatedMS: 1, Role: "coordinator", Attempt: "old-native-owner", BindingGeneration: 1, Cwd: root}
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
	return root, s, f, sha, env
}
