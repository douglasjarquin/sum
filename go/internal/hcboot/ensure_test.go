package hcboot

import (
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func labRoute(t *testing.T) Route {
	t.Helper()
	route, err := DefaultRoute()
	if err != nil {
		t.Fatal(err)
	}
	return route
}

func TestEnsureSkipsARuntimeWithoutHC(t *testing.T) {
	writeHC(t, t.TempDir(), filepath.Join(t.TempDir(), "unused.log"), "ready")
	if EnsureRoute(t.TempDir(), labRoute(t), false) != nil {
		t.Fatal("missing hc produced a report")
	}
}

func TestEnsureReportsAReadyHolder(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "hc.log")
	writeHC(t, root, log, "ready")
	t.Setenv("HC_SESSION", "not-the-default")
	report := EnsureRoute(root, labRoute(t), false)
	if report == nil || reportValue(t, report, "ok") != true || reportValue(t, report, "action") != "ready" {
		t.Fatalf("ready report = %v", report)
	}
	calls := readLog(t, log)
	if calls != "--json server status\n" {
		t.Fatalf("calls = %q", calls)
	}
	if strings.Contains(calls, "not-the-default") {
		t.Fatal("session leaked into the hc command")
	}
}

func TestEnsureStartsADownCore(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "hc.log")
	writeHC(t, root, log, "down")
	report := EnsureRoute(root, labRoute(t), false)
	if reportValue(t, report, "ok") != true || reportValue(t, report, "action") != "started" {
		t.Fatalf("start report = %v", report)
	}
	if !strings.Contains(readLog(t, log), "--json server start") {
		t.Fatalf("start was not requested: %s", readLog(t, log))
	}
}

func TestEnsureNeverStartsOverAListeningCore(t *testing.T) {
	for _, scenario := range []struct{ mode, action string }{{"ready", "ready"}, {"wedged", "holder-missing"}, {"down", "status"}} {
		t.Run(scenario.mode, func(t *testing.T) {
			root := t.TempDir()
			log := filepath.Join(root, "hc.log")
			writeHC(t, root, log, scenario.mode)
			report := EnsureRoute(root, labRoute(t), true)
			if reportValue(t, report, "action") != scenario.action || (reportValue(t, report, "ok") == true) != (scenario.action == "ready") {
				t.Fatalf("%s report = %v", scenario.mode, report)
			}
			calls := readLog(t, log)
			for _, forbidden := range []string{"server start", "restart", "run true"} {
				if strings.Contains(calls, forbidden) {
					t.Fatalf("listening core was disturbed with %q: %s", forbidden, calls)
				}
			}
		})
	}
}

func TestEnsureRefusesAHolderlessCoreWithoutRestartOrProbe(t *testing.T) {
	for _, mode := range []string{"wedged", "busy"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			log := filepath.Join(root, "hc.log")
			writeHC(t, root, log, mode)
			report := EnsureRoute(root, labRoute(t), false)
			if reportValue(t, report, "ok") != false || reportValue(t, report, "action") != "holder-missing" {
				t.Fatalf("%s report = %v", mode, report)
			}
			if !strings.Contains(reportValue(t, report, "error").(string), "does not restart") {
				t.Fatalf("refusal is not actionable: %v", report)
			}
			calls := readLog(t, log)
			for _, forbidden := range []string{"restart", "run true", "rm ", "server start"} {
				if strings.Contains(calls, forbidden) {
					t.Fatalf("shared core was disturbed with %q: %s", forbidden, calls)
				}
			}
		})
	}
}

func writeHC(t *testing.T, root, log, mode string) {
	t.Helper()
	for _, key := range []string{"HC_SOCKET", "SUM_SESSION", "SUM_CORE_ID", "SUM_HOME", "SUM_ROLE", "SUM_ATTEMPT", "SUM_TASK", "SUM_BINDING_GENERATION"} {
		t.Setenv(key, "")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	path := filepath.Join(root, ".local", "bin", "hc")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$HCBOOT_LOG"
status_n=0
if [ -f "$HCBOOT_LOG.count" ]; then status_n=$(cat "$HCBOOT_LOG.count"); fi
case "$*" in
"--json server status")
  status_n=$((status_n + 1))
  printf '%s' "$status_n" > "$HCBOOT_LOG.count"
  case "$HCBOOT_MODE" in
  ready) printf '%s\n' '{"live":0,"holder":{"current":true,"pid":4}}' ;;
  down)
    if [ "$status_n" -eq 1 ]; then exit 1; fi
    printf '%s\n' '{"live":0,"holder":{"current":true,"pid":5}}'
    ;;
  wedged) printf '%s\n' '{"live":0,"holder":null}' ;;
  busy) printf '%s\n' '{"live":2,"holder":null}' ;;
  esac
  ;;
"--json run true") printf '%s\n' '{"address":"t1","state":"finished"}' ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HCBOOT_LOG", log)
	t.Setenv("HCBOOT_MODE", mode)
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func reportValue(t *testing.T, report interface{ Get(string) (any, bool) }, key string) any {
	t.Helper()
	if report == nil {
		t.Fatal("missing report")
	}
	value, ok := report.Get(key)
	if !ok {
		t.Fatalf("missing %s", key)
	}
	return value
}

func TestSelectedRoutePreservesNamedScope(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "hc.log")
	writeHC(t, root, log, "ready")
	route := Route{Socket: filepath.Join(root, "named.sock"), Session: "sum-test-selected", ConfigHome: root, StateHome: root}
	env := route.environment()
	for _, want := range []string{"HC_SOCKET=" + route.Socket, "HC_SESSION=sum-test-selected", "XDG_CONFIG_HOME=" + root} {
		found := false
		for _, entry := range env {
			if entry == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("selected route lost %s", want)
		}
	}
	if report := EnsureRoute(root, route, false); reportValue(t, report, "ok") != true {
		t.Fatal(report)
	}
}

func TestEnsureRetainsNativeStartFailureAndStopsSequence(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "hc.log")
	writeHC(t, root, log, "ready")
	binary := filepath.Join(root, ".local/bin/hc")
	script := "#!/bin/sh\nprintf '%s\n' \"$*\" >> \"$HCBOOT_LOG\"\ncase \"$*\" in\n'--json server status') exit 1;;\n'--json server start') echo selected-core-start-failed >&2; exit 7;;\nesac\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	report := EnsureRoute(root, labRoute(t), false)
	if reportValue(t, report, "ok") != false || !strings.Contains(reportValue(t, report, "error").(string), "selected-core-start-failed") {
		t.Fatal(report)
	}
	calls := readLog(t, log)
	if strings.Count(calls, "server status") != 1 || strings.Contains(calls, "restart") {
		t.Fatal(calls)
	}
}

func TestEnsureStartsAfterStaleSocketWithoutListener(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "hc.log")
	writeHC(t, root, log, "down")
	fixture := hctest.New(t)
	hctest.PointDefaultRoute(t, filepath.Join(filepath.Dir(fixture.Socket), "stale.sock"))
	route := labRoute(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: route.Socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	report := EnsureRoute(root, route, false)
	if reportValue(t, report, "ok") != true || !strings.Contains(readLog(t, log), "server start") {
		t.Fatalf("stale socket blocked start: %v", report)
	}
}
