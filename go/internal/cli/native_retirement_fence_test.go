package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestNativeRetirementFencesPreviouslyBuiltHelper(t *testing.T) {
	oldHelper := buildNativeBaseHelper(t)
	for _, mode := range []string{"new invocation", "physical generation invocation", "in-flight authorization"} {
		t.Run(mode, func(t *testing.T) {
			s, f, endpoint, env := nativeRetirementAuthority(t)
			home := s.Root
			if mode == "physical generation invocation" {
				home = s.Home
			}
			invoke := func(harness string) *exec.Cmd {
				cmd := exec.Command(oldHelper, "--format", "json", "--home", home, "settings", "set", "--worker-harness", harness)
				cmd.Env = env
				return cmd
			}
			if output, err := invoke("codex").CombinedOutput(); err != nil {
				t.Fatalf("base helper cannot write before retirement: %v\n%s", err, output)
			}
			before, err := os.ReadFile(filepath.Join(s.Home, "settings.json"))
			if err != nil {
				t.Fatal(err)
			}
			cmd := invoke("grok")
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			var release func()
			if mode == "in-flight authorization" {
				// Context observes first; RequireCoordinator observes again after its writable check.
				enteredContext, releaseContext := f.HoldNext("terminal.get", endpoint.Address)
				defer releaseContext()
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if cmd.ProcessState == nil {
						_ = cmd.Process.Kill()
						_ = cmd.Wait()
					}
				})
				waitRetirementBoundary(t, enteredContext)
				enteredAuthority, releaseAuthority := f.HoldReplyNext("terminal.get", endpoint.Address)
				release = releaseAuthority
				defer releaseAuthority()
				releaseContext()
				waitRetirementBoundary(t, enteredAuthority)
			}
			if _, err := s.RetireNativeCoordinator("", "ordinary conversation", false); err != nil {
				t.Fatal(err)
			}
			if release != nil {
				release()
				err = cmd.Wait()
			} else {
				err = cmd.Run()
			}
			if err == nil {
				t.Fatalf("retired native helper retained write authority: %s", output.String())
			}
			if !strings.Contains(output.String(), "unsupported state schema") && !strings.Contains(output.String(), "inactive state generation") {
				t.Fatalf("helper was not refused by the state authority fence: %s", output.String())
			}
			after, err := os.ReadFile(filepath.Join(s.Home, "settings.json"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("retired helper changed settings: before=%s after=%s", before, after)
			}
		})
	}
}

func waitRetirementBoundary(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("old helper did not reach the held native authorization boundary")
	}
}

func nativeRetirementAuthority(t *testing.T) (*store.Store, *hctest.Fixture, store.Endpoint, []string) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	f := hctest.New(t)
	cwd := t.TempDir()
	pid := uint32(100)
	terminal := hcclient.Terminal{Address: "t1", CreatedMS: 1, Cwd: cwd, State: "live", PID: &pid, Foreground: &hcclient.Foreground{PID: 101, Name: "codex", Argv: []string{"codex"}, Cwd: cwd}}
	f.SetTerminal(terminal)
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Socket: f.Socket, Session: "sum-test-native-retirement", Address: terminal.Address, CreatedMS: 1, Role: "coordinator", Attempt: "native-retirement-owner", BindingGeneration: 1, Cwd: cwd}
	observed := incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: terminal.Foreground}.Record(store.Now())
	receipt, err := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: "coordinator-init", Attempt: endpoint.Attempt})
	if err != nil {
		t.Fatal(err)
	}
	registration, err := s.Register(endpoint, "coordinator", nil, observed)
	if err != nil {
		t.Fatal(err)
	}
	registration.Set("native_receipt", receipt)
	if err := ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(endpoint)+".json"), registration); err != nil {
		t.Fatal(err)
	}
	owner := endpoint.Object()
	owner.Set("incarnation", observed)
	owner.Set("native_receipt", receipt)
	if err := ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	env := append(proc.ScrubbedEnv(), "SUM_HOME="+s.Root, "SUM_SESSION="+endpoint.Session, "HC_SESSION="+endpoint.Session, "HC_SOCKET="+endpoint.Socket, "HC_TERMINAL_ID="+endpoint.Address, "SUM_CORE_ID="+endpoint.CoreID, "SUM_ROLE=coordinator", "SUM_ATTEMPT="+endpoint.Attempt, "SUM_BINDING_GENERATION=1")
	return s, f, endpoint, env
}

func buildNativeBaseHelper(t *testing.T) string {
	t.Helper()
	return hctest.BuildNativeBaseHelper(t, repoRoot(t))
}
