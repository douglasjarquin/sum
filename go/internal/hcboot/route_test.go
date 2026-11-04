package hcboot

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

func writeCache(t *testing.T, s *store.Store, route Route) {
	t.Helper()
	data, _ := json.Marshal(route)
	if err := os.WriteFile(filepath.Join(s.Root, "runtime-route.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultRouteIgnoresInheritedIdentityAndUsesConfiguredSocket(t *testing.T) {
	s := workspaceStore(t)
	config := hctest.PointDefaultRoute(t, "/tmp/sum-test-configured.sock")
	t.Setenv("HC_SOCKET", "/tmp/unrelated-core.sock")
	t.Setenv("HC_SESSION", "coordinator-session")
	t.Setenv("SUM_SESSION", "unrelated-session")
	route, err := SelectedRoute(s)
	if err != nil {
		t.Fatal(err)
	}
	if route.Socket != "/tmp/sum-test-configured.sock" || route.Session != DefaultSession || route.ConfigHome != config || route.CoreID != "" {
		t.Fatalf("inherited native identity or a private route selected creation: %+v", route)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "runtime-route.json")); !os.IsNotExist(err) {
		t.Fatalf("route observation wrote state: %v", err)
	}
}

func TestDefaultRouteFollowsPlatformDefaultsWithoutConfiguredSocket(t *testing.T) {
	config, state, runtime := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	t.Setenv("HC_SOCKET", "/tmp/unrelated-core.sock")
	route, err := DefaultRoute()
	if err != nil {
		t.Fatal(err)
	}
	if route.Socket != filepath.Join(runtime, "herdr-core", "default.sock") || route.RuntimeDir != runtime {
		t.Fatalf("runtime directory default not used: %+v", route)
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	route, err = DefaultRoute()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(state, "herdr-core", "default", "default.sock")
	if platformRuntime := "/run/user/" + strconv.Itoa(os.Getuid()); isDirectory(platformRuntime) {
		want = filepath.Join(platformRuntime, "herdr-core", "default.sock")
	}
	if route.Socket != want || route.StateHome != state || route.ConfigHome != config {
		t.Fatalf("platform default not used: %+v", route)
	}
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func TestPrivateCacheDoesNotSelectCreationAndDefaultCachePinsCore(t *testing.T) {
	s := workspaceStore(t)
	fixture := hctest.New(t)
	fixture.DefaultRoute(t)
	private := Route{Socket: "/tmp/sum-hc-private/core.sock", Session: "sum-private", ConfigHome: filepath.Join(s.Root, "runtime/hc/config"), StateHome: filepath.Join(s.Root, "runtime/hc/state"), RuntimeDir: "/tmp/sum-hc-private", CoreID: "private-core"}
	writeCache(t, s, private)
	route, err := SelectedRoute(s)
	if err != nil {
		t.Fatal(err)
	}
	if route.Socket != fixture.Socket || route.CoreID != "" || route.Session != DefaultSession {
		t.Fatalf("private cache selected creation: %+v", route)
	}
	ensured, err := EnsureWorkspaceRoute(s)
	if err != nil {
		t.Fatal(err)
	}
	if ensured.CoreID != fixture.CoreID || ensured.Socket != fixture.Socket {
		t.Fatalf("default core not pinned: %+v", ensured)
	}
	again, err := SelectedRoute(s)
	if err != nil || again.CoreID != fixture.CoreID {
		t.Fatalf("pinned route = %+v, %v", again, err)
	}
	if _, err = EnsureWorkspaceRoute(s); err != nil {
		t.Fatalf("pinned default route refused its own core: %v", err)
	}
}

func TestPinnedDefaultCoreRefusesReplacementWithoutFallback(t *testing.T) {
	s := workspaceStore(t)
	fixture := hctest.New(t)
	fixture.DefaultRoute(t)
	route, err := SelectedRoute(s)
	if err != nil {
		t.Fatal(err)
	}
	route.CoreID = "different-core"
	writeCache(t, s, route)
	_, err = EnsureWorkspaceRoute(s)
	if err == nil || !strings.Contains(err.Error(), "core changed") {
		t.Fatalf("replacement core accepted: %v", err)
	}
	again, err := SelectedRoute(s)
	if err != nil || again.CoreID != "different-core" {
		t.Fatalf("refusal rewrote the pin: %+v %v", again, err)
	}
}

func TestIncompatibleDefaultCoreVersionIsRefused(t *testing.T) {
	s := workspaceStore(t)
	fixture := hctest.New(t)
	fixture.SetVersion("0.1.13")
	fixture.DefaultRoute(t)
	_, err := EnsureWorkspaceRoute(s)
	if err == nil || !strings.Contains(err.Error(), "0.1.13") || !strings.Contains(err.Error(), "0.1.14") {
		t.Fatalf("incompatible core accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "runtime-route.json")); !os.IsNotExist(err) {
		t.Fatalf("refusal pinned a route: %v", err)
	}
}

func TestConcurrentLaunchesPinOneDefaultCore(t *testing.T) {
	s := workspaceStore(t)
	fixture := hctest.New(t)
	fixture.DefaultRoute(t)
	var wg sync.WaitGroup
	results := make([]Route, 4)
	errs := make([]error, 4)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			own, err := store.Open(s.Root)
			if err != nil {
				errs[i] = err
				return
			}
			results[i], errs[i] = EnsureWorkspaceRoute(own)
		}(i)
	}
	wg.Wait()
	for i := range results {
		if errs[i] != nil || results[i].CoreID != fixture.CoreID {
			t.Fatalf("launch %d: %+v %v", i, results[i], errs[i])
		}
	}
	saved, err := SelectedRoute(s)
	if err != nil || saved.CoreID != fixture.CoreID {
		t.Fatalf("saved route = %+v %v", saved, err)
	}
}

func TestNewerDefaultCoreIsAccepted(t *testing.T) {
	s := workspaceStore(t)
	fixture := hctest.New(t)
	fixture.SetVersion("0.1.17")
	fixture.DefaultRoute(t)
	route, err := EnsureWorkspaceRoute(s)
	if err != nil || route.CoreID != fixture.CoreID {
		t.Fatalf("newer core refused: %+v %v", route, err)
	}
}

func TestMissingDefaultCoreNeverStartsAProcess(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(strconv.FormatBool(stale), func(t *testing.T) {
			s := workspaceStore(t)
			dir, err := os.MkdirTemp("/tmp", "sum-missing-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(dir) })
			socket := filepath.Join(dir, "absent.sock")
			hctest.PointDefaultRoute(t, socket)
			if stale {
				listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				listener.SetUnlinkOnClose(false)
				if err = listener.Close(); err != nil {
					t.Fatal(err)
				}
			}
			_, err = EnsureWorkspaceRoute(s)
			if err == nil || !strings.Contains(err.Error(), socket) || !strings.Contains(err.Error(), "open Herdr") {
				t.Fatalf("missing core: %v", err)
			}
			if _, err = os.Stat(cachePath(s)); !os.IsNotExist(err) {
				t.Fatalf("refusal pinned a route: %v", err)
			}
		})
	}
}
