package doctor

import (
	"context"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
	return string(out)
}

// checkoutWithCommits builds a throwaway installation checkout and returns its
// path plus the SHA of every commit, oldest first.
func checkoutWithCommits(t *testing.T, count int) (string, []string) {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "--quiet", "--initial-branch=main")
	git(t, root, "config", "user.email", "doctor@test.invalid")
	git(t, root, "config", "user.name", "doctor test")
	shas := make([]string, 0, count)
	for i := 0; i < count; i++ {
		name := filepath.Join(root, "file")
		if err := os.WriteFile(name, []byte{byte('a' + i)}, 0o600); err != nil {
			t.Fatal(err)
		}
		git(t, root, "add", "file")
		git(t, root, "commit", "--quiet", "-m", "commit")
		shas = append(shas, trimmed(git(t, root, "rev-parse", "HEAD")))
	}
	return root, shas
}

func trimmed(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// stagedRelease writes the one field runtimeRevisionCheck reads out of a
// release tree, so the test does not depend on the packager.
func stagedRelease(t *testing.T, sourceSHA string) string {
	t.Helper()
	root := t.TempDir()
	body := `{"schema": 1, "kind": "sum-release", "source": {"sha": "` + sourceSHA + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "release.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func field(t *testing.T, root string, install string, key string) any {
	t.Helper()
	value, _ := runtimeRevisionCheck(root, install).Get(key)
	return value
}

func TestRuntimeRevision_noStagedReleaseIsNotDrift(t *testing.T) {
	checkout, _ := checkoutWithCommits(t, 1)
	row := runtimeRevisionCheck(checkout, checkout)
	if ok, _ := row.Get("ok"); ok != true {
		t.Fatalf("ok = %v, want true when the runtime is the checkout itself", ok)
	}
	if sha, _ := row.Get("runtime_sha"); sha != nil {
		t.Fatalf("runtime_sha = %v, want unset without a staged release", sha)
	}
}

func TestRuntimeRevision_emptyInstallRootFallsBackToRuntime(t *testing.T) {
	checkout, _ := checkoutWithCommits(t, 1)
	if ok := field(t, checkout, "", "ok"); ok != true {
		t.Fatalf("ok = %v, want true when installRoot is empty", ok)
	}
}

func TestRuntimeRevision_runtimeStagedFromHeadIsCurrent(t *testing.T) {
	checkout, shas := checkoutWithCommits(t, 2)
	runtime := stagedRelease(t, shas[len(shas)-1])
	row := runtimeRevisionCheck(runtime, checkout)
	if ok, _ := row.Get("ok"); ok != true {
		t.Fatalf("ok = %v, want true at HEAD", ok)
	}
	if behind, _ := row.Get("behind"); behind != 0 {
		t.Fatalf("behind = %v, want 0 at HEAD", behind)
	}
}

func TestRuntimeRevision_ancestorRuntimeReportsDistanceAndStaysOK(t *testing.T) {
	checkout, shas := checkoutWithCommits(t, 4)
	runtime := stagedRelease(t, shas[0])
	row := runtimeRevisionCheck(runtime, checkout)

	// Pulling without updating is ordinary; the drift must be visible anyway.
	if ok, _ := row.Get("ok"); ok != true {
		t.Fatalf("ok = %v, want true for an ancestor runtime", ok)
	}
	if behind, _ := row.Get("behind"); behind != 3 {
		t.Fatalf("behind = %v, want 3", behind)
	}
	if sha, _ := row.Get("runtime_sha"); sha != shas[0] {
		t.Fatalf("runtime_sha = %v, want %s", sha, shas[0])
	}
	if sha, _ := row.Get("checkout_sha"); sha != shas[len(shas)-1] {
		t.Fatalf("checkout_sha = %v, want %s", sha, shas[len(shas)-1])
	}
}

func TestRuntimeRevision_runtimeOffTheCheckoutHistoryIsNotOK(t *testing.T) {
	checkout, _ := checkoutWithCommits(t, 2)
	runtime := stagedRelease(t, "0123456789012345678901234567890123456789")
	row := runtimeRevisionCheck(runtime, checkout)
	if ok, _ := row.Get("ok"); ok != false {
		t.Fatalf("ok = %v, want false for a runtime that is not on the checkout's history", ok)
	}
}

func TestRuntimeRevision_unidentifiableRuntimeIsNotOK(t *testing.T) {
	checkout, _ := checkoutWithCommits(t, 1)
	row := runtimeRevisionCheck(t.TempDir(), checkout)
	if ok, _ := row.Get("ok"); ok != false {
		t.Fatalf("ok = %v, want false when release.json is missing", ok)
	}
}

func TestRuntimeRevision_nonGitInstallationCannotDriftAndStaysOK(t *testing.T) {
	runtime := stagedRelease(t, "0123456789012345678901234567890123456789")
	row := runtimeRevisionCheck(runtime, t.TempDir())
	if ok, _ := row.Get("ok"); ok != true {
		t.Fatalf("ok = %v, want true when the installation is not a Git checkout", ok)
	}
}

func TestClockPin_absentWithoutTheVariable(t *testing.T) {
	t.Setenv("SUM_NOW", "")
	os.Unsetenv("SUM_NOW")
	row := clockPinCheck()
	if ok, _ := row.Get("ok"); ok != true {
		t.Fatalf("ok = %v, want true when the clock is not pinned", ok)
	}
	if at, _ := row.Get("pinned_at"); at != nil {
		t.Fatalf("pinned_at = %v, want nil when the clock is not pinned", at)
	}
}

func TestClockPin_warnsAndNamesThePinnedInstant(t *testing.T) {
	t.Setenv("SUM_NOW", "2026-01-02T00:00:00Z")
	row := clockPinCheck()
	if ok, _ := row.Get("ok"); ok != false {
		t.Fatalf("ok = %v, want false while the clock is pinned", ok)
	}
	if at, _ := row.Get("pinned_at"); at != "2026-01-02T00:00:00Z" {
		t.Fatalf("pinned_at = %v, want the pinned instant", at)
	}
	detail, _ := row.Get("detail")
	if text, _ := detail.(string); !strings.Contains(text, "2026-01-02T00:00:00Z") {
		t.Fatalf("detail = %q, want it to name the pinned instant", text)
	}
}

func TestClockPin_warnsThatAnUnparseablePinIsIgnored(t *testing.T) {
	t.Setenv("SUM_NOW", "yesterday")
	row := clockPinCheck()
	if ok, _ := row.Get("ok"); ok != false {
		t.Fatalf("ok = %v, want false whenever the variable is set at all", ok)
	}
	if at, _ := row.Get("pinned_at"); at != nil {
		t.Fatalf("pinned_at = %v, want nil because an unparseable value never takes effect", at)
	}
}

func clearDoctorRuntimeEnv(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "HC_") || strings.HasPrefix(key, "HERDR_") || strings.HasPrefix(key, "SUM_") {
			t.Setenv(key, "")
		}
	}
	t.Setenv("PATH", t.TempDir())
}
func doctorRow(t *testing.T, report *ordjson.Object, name string) *ordjson.Object {
	t.Helper()
	raw, _ := report.Get("checks")
	for _, v := range raw.([]any) {
		row := v.(*ordjson.Object)
		if tool, _ := row.Get("tool"); tool == name {
			return row
		}
	}
	t.Fatalf("missing check %s", name)
	return nil
}
func doctorStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), ".sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestDoctorSeparatesExecutableFromRuntimeAndHarnessQualification(t *testing.T) {
	clearDoctorRuntimeEnv(t)
	runtime := t.TempDir()
	s := doctorStore(t)
	for _, name := range []string{"hc", "codex"} {
		path := filepath.Join(runtime, ".local/bin", name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 91\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	report := Doctor(runtime, runtime, s)
	raw, _ := report.Get("checks")
	for _, v := range raw.([]any) {
		row := v.(*ordjson.Object)
		if tool, _ := row.Get("tool"); tool == "mesh" {
			t.Fatal("obsolete Mesh runtime requirement")
		}
	}
	if ok, _ := doctorRow(t, report, "hc").Get("ok"); ok != true {
		t.Fatal("staged executable discovery failed")
	}
	artifact := doctorRow(t, report, "hc-runtime")
	if qualified, _ := artifact.Get("qualified"); qualified != false {
		t.Fatal("unmatched executable qualified")
	}
	if available, _ := doctorRow(t, report, "hc-core").Get("available"); available != false {
		t.Fatal("missing core guessed available")
	}
	if qualification, _ := doctorRow(t, report, "harness").Get("qualification"); qualification != "unknown" {
		t.Fatal("installed executable guessed ready")
	}
}
func TestDoctorObservesExistingCoreWithoutInputOrStart(t *testing.T) {
	clearDoctorRuntimeEnv(t)
	runtime := t.TempDir()
	s := doctorStore(t)
	f := hctest.New(t)
	client := f.Client()
	terminal, err := client.Run(context.Background(), hcclient.RunParams{Argv: []string{"fixture"}, Cwd: runtime})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HC_SOCKET", f.Socket)
	t.Setenv("SUM_CORE_ID", f.CoreID)
	t.Setenv("HC_TERMINAL_ID", terminal.Address)
	t.Setenv("SUM_SESSION", "sum-test-doctor")
	before := len(f.Calls())
	report := Doctor(runtime, runtime, s)
	if available, _ := doctorRow(t, report, "hc-core").Get("available"); available != true {
		t.Fatal("existing attested core unavailable")
	}
	for _, call := range f.Calls()[before:] {
		switch call.Method {
		case "hello", "terminal.get":
		default:
			t.Fatalf("doctor performed %s", call.Method)
		}
	}
}

func TestDoctorMatchedRuntimeDoesNotImplyRunningCore(t *testing.T) {
	clearDoctorRuntimeEnv(t)
	t.Setenv("PATH", "/usr/bin:/bin")
	runtime := t.TempDir()
	s := doctorStore(t)
	hctest.InstallRuntime(t, runtime)
	report := Doctor(runtime, runtime, s)
	if qualified, _ := doctorRow(t, report, "hc-runtime").Get("qualified"); qualified != true {
		t.Fatal("matched runtime contract not verified")
	}
	if available, _ := doctorRow(t, report, "hc-core").Get("available"); available != false {
		t.Fatal("staged binary guessed to have started a core")
	}
}
