package hcboot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func workspaceStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestWorkspaceRouteIgnoresInheritedCoordinatorRoute(t *testing.T) {
	s := workspaceStore(t)
	t.Setenv("HC_SOCKET", "/tmp/unrelated-core.sock")
	t.Setenv("HC_SESSION", "default")
	t.Setenv("SUM_SESSION", "unrelated-session")
	route, err := SelectedRoute(s, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if route.Socket == "/tmp/unrelated-core.sock" || route.Session == "default" || route.Session == "unrelated-session" {
		t.Fatalf("inherited native identity selected the worker route: %+v", route)
	}
	if !strings.HasPrefix(route.ConfigHome, s.Root+string(filepath.Separator)) {
		t.Fatalf("config is not workspace owned: %+v", route)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "runtime-route.json")); !os.IsNotExist(err) {
		t.Fatalf("route observation wrote state: %v", err)
	}
}

func TestWorkspaceRoutePinsCoreAndRefusesReplacement(t *testing.T) {
	s := workspaceStore(t)
	fixture := hctest.New(t)
	base := t.TempDir()
	saved := Route{Socket: fixture.Socket, Session: "sum-test-route", ConfigHome: filepath.Join(base, "config"), StateHome: filepath.Join(base, "state")}
	data, _ := json.Marshal(saved)
	if err := os.WriteFile(filepath.Join(s.Root, "runtime-route.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	route, err := EnsureWorkspaceRoute(s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if route.CoreID != fixture.CoreID {
		t.Fatalf("core not pinned: %+v", route)
	}
	again, err := SelectedRoute(s, "", "", "")
	if err != nil || again.CoreID != fixture.CoreID {
		t.Fatalf("saved route = %+v, %v", again, err)
	}
	saved.CoreID = "different-core"
	data, _ = json.Marshal(saved)
	if err := os.WriteFile(filepath.Join(s.Root, "runtime-route.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureWorkspaceRoute(s, t.TempDir()); err == nil || !strings.Contains(err.Error(), "core changed") {
		t.Fatalf("replacement core accepted: %v", err)
	}
}
