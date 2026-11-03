package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func developerFallbackLab(t *testing.T) (string, *store.Store, *hctest.Fixture, map[string]string) {
	t.Helper()
	runtimeRoot := t.TempDir()
	procedure := filepath.Join(runtimeRoot, "skills", "sum-develop", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(procedure), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(procedure, []byte("# Developer procedure\nUse an isolated development checkout.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(runtimeRoot, ".sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Init(); err != nil {
		t.Fatal(err)
	}
	f := hctest.New(t)
	pid := uint32(101)
	foreground := &hcclient.Foreground{PID: 102, Name: "codex", Cwd: runtimeRoot}
	terminal := hcclient.Terminal{Address: "t1", CreatedMS: 1, Cwd: runtimeRoot, State: "live", PID: &pid, Foreground: foreground}
	f.SetTerminal(terminal)
	host, err := st.Machine()
	if err != nil {
		t.Fatal(err)
	}
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Session: "fallback-lab", Socket: f.Socket, Address: "t1", CreatedMS: 1, Role: "coordinator", Attempt: "existing-attempt", BindingGeneration: 1, Cwd: runtimeRoot}
	owner := endpoint.Object()
	owner.Set("incarnation", incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: foreground}.Record(store.Now()))
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: "coordinator-init", Attempt: endpoint.Attempt})
	owner.Set("native_receipt", receipt)
	if err = ordjson.WriteFile(filepath.Join(st.Home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"SUM_SESSION": endpoint.Session, "SUM_CORE_ID": endpoint.CoreID, "HC_SOCKET": endpoint.Socket, "HC_TERMINAL_ID": endpoint.Address, "SUM_ROLE": endpoint.Role, "SUM_ATTEMPT": endpoint.Attempt, "SUM_BINDING_GENERATION": "1"}
	return runtimeRoot, st, f, env
}
func clearFallbackIdentity(t *testing.T) {
	t.Helper()
	for _, key := range []string{"SUM_SESSION", "HC_SESSION", "HC_SOCKET", "HC_TERMINAL_ID", "SUM_CORE_ID", "SUM_ROLE", "SUM_TASK", "SUM_ATTEMPT", "SUM_BINDING_GENERATION", "SUM_HOME", "SUM_INSTALL_ROOT"} {
		t.Setenv(key, "")
	}
}
func TestInitNativeOwnerRequiresExplicitRetirement(t *testing.T) {
	clearFallbackIdentity(t)
	root, st, f, _ := developerFallbackLab(t)
	before := snapshotHome(t, st.Root)
	view, err := ordinaryInit(t, root)
	if err == nil || view["blocker"] != "retire-native" {
		t.Fatalf("native owner = %v %v", view, err)
	}
	if snapshotHome(t, st.Root) != before || len(f.Calls()) != 0 {
		t.Fatal("retirement refusal changed state or observed HC")
	}
}

func TestInitExplicitDeveloperNeverObservesNativeOwner(t *testing.T) {
	clearFallbackIdentity(t)
	root, st, f, env := developerFallbackLab(t)
	for key, value := range env {
		t.Setenv(key, value)
	}
	before := snapshotHome(t, st.Root)
	view, err := ordinaryInit(t, root, "--role", "developer")
	if err != nil || view["role"] != "developer" {
		t.Fatalf("developer = %v %v", view, err)
	}
	if snapshotHome(t, st.Root) != before || len(f.Calls()) != 0 {
		t.Fatal("developer entry changed state or observed HC")
	}
}
