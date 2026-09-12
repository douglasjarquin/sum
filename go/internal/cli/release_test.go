package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/release"
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeReleaseFile(t *testing.T, dir, rel string, content []byte, mode os.FileMode) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, content, mode); err != nil {
		t.Fatal(err)
	}
}

// buildValidRelease creates a release directory tree at <releasesRoot>/<sha> that satisfies every check in
// verify_release, with an empty "native"/"inventory" to skip that nested (and separately testable) branch.
func buildValidRelease(t *testing.T, releasesRoot, sha string) {
	t.Helper()
	dir := filepath.Join(releasesRoot, sha)

	requiredContents := []struct{ rel, content string }{
		{"bin/sumctl", "#!/bin/sh\necho sumctl\n"},
		{"bin/herdr-mesh", "#!/bin/sh\necho herdr-mesh\n"},
		{"bin/herdr-scoped", "#!/bin/sh\necho herdr-scoped\n"},
		{"lib/sumctl.py", "# sumctl\n"},
		{"skills/sum-worker/SKILL.md", "# worker\n"},
	}
	var filesEntries []string
	for _, entry := range requiredContents {
		writeReleaseFile(t, dir, entry.rel, []byte(entry.content), 0o644)
		filesEntries = append(filesEntries, fmt.Sprintf("%q: %q", entry.rel, "sha256:"+sha256Hex([]byte(entry.content))))
	}
	if err := os.Chmod(filepath.Join(dir, "bin", "sumctl"), 0o755); err != nil {
		t.Fatal(err)
	}

	var toolPaths []string
	for _, name := range release.CoreTools {
		targetRel := filepath.Join("native", name)
		writeReleaseFile(t, dir, targetRel, []byte("#!/bin/sh\n"), 0o755)
		target := filepath.Join(dir, targetRel)
		link := filepath.Join(dir, ".local", "bin", name)
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		toolPaths = append(toolPaths, fmt.Sprintf("%q: %q", name, target))
	}

	manifest := fmt.Sprintf(`{
  "schema": 1, "kind": "sum-release", "sum_version": "0.1.0",
  "source": {"sha": %q, "tree": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "repository": "/tmp/installation"},
  "files": {%s},
  "dependencies": {
    "tools": {"pins": {}, "paths": {%s}},
    "codegraph": {"package": "@colbymchenry/codegraph", "version": "1.5.0", "license": "MIT"},
    "inventory": null,
    "native": {}
  },
  "contracts": {}, "supports": {},
  "staged_at": "2026-01-01T00:00:00+00:00", "staged_by": {"machine": "m1", "installation": "/tmp/installation", "instance": null}
}`, sha, strings.Join(filesEntries, ", "), strings.Join(toolPaths, ", "))
	writeReleaseFile(t, dir, "release.json", []byte(manifest), 0o644)
}

// buildInstallation creates a synthetic "installation" root: a git repo whose .sum/state.json designates it
// (state.json present, no dev.json), matching what `installation_root` requires without touching the real
// installation this dev checkout is a worktree of.
func buildInstallation(t *testing.T) (root, home string) {
	t.Helper()
	root = t.TempDir()
	if out, err := exec.Command("git", "init", "--quiet", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	home = filepath.Join(root, ".sum")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, home
}

func TestReleaseList_matchesThePythonReferenceAcrossScenarios(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	reference := filepath.Join(repoRoot, "bin", "sumctl")
	if _, statErr := os.Stat(reference); statErr != nil {
		t.Skipf("reference bin/sumctl not found: %v", statErr)
	}

	t.Run("no releases directory", func(t *testing.T) {
		_, home := buildInstallation(t)
		assertReleaseMatches(t, reference, home, []string{"release", "list"})
	})

	t.Run("one valid release, one broken (missing manifest), one in-progress staging dir", func(t *testing.T) {
		root, home := buildInstallation(t)
		releasesRoot := filepath.Join(root, ".local", "releases")
		validSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		buildValidRelease(t, releasesRoot, validSHA)
		brokenSHA := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		if err := os.MkdirAll(filepath.Join(releasesRoot, brokenSHA), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(releasesRoot, ".staging-ccccccccc"), 0o755); err != nil {
			t.Fatal(err)
		}
		assertReleaseMatches(t, reference, home, []string{"release", "list"})
	})
}

func TestReleaseShow_matchesThePythonReferenceAcrossScenarios(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	reference := filepath.Join(repoRoot, "bin", "sumctl")
	if _, statErr := os.Stat(reference); statErr != nil {
		t.Skipf("reference bin/sumctl not found: %v", statErr)
	}

	t.Run("full SHA match", func(t *testing.T) {
		root, home := buildInstallation(t)
		validSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		buildValidRelease(t, filepath.Join(root, ".local", "releases"), validSHA)
		assertReleaseMatches(t, reference, home, []string{"release", "show", validSHA})
	})

	t.Run("short SHA prefix match", func(t *testing.T) {
		root, home := buildInstallation(t)
		validSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		buildValidRelease(t, filepath.Join(root, ".local", "releases"), validSHA)
		assertReleaseMatches(t, reference, home, []string{"release", "show", "aaaaaaa"})
	})

	t.Run("no match is a command-level failure", func(t *testing.T) {
		root, home := buildInstallation(t)
		validSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		buildValidRelease(t, filepath.Join(root, ".local", "releases"), validSHA)
		assertReleaseFailureMatches(t, reference, home, []string{"release", "show", "ffffffff"})
	})
}

func assertReleaseMatches(t *testing.T, reference, home string, args []string) {
	t.Helper()
	fullArgs := append([]string{"--home", home}, args...)
	want, err := exec.Command(reference, fullArgs...).Output()
	if err != nil {
		t.Fatalf("python reference failed: %v", err)
	}
	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(fullArgs)
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("go command failed: %v (stderr=%s)", err, stderr.String())
	}
	if stdout.String() != string(want) {
		t.Fatalf("go output =\n%s\nwant (python reference)\n%s", stdout.String(), want)
	}
}

// assertReleaseFailureMatches covers the command-level error path: `verify_release`/`release_show` raise, main()
// catches and prints `{"error": ...}` to stderr with exit 1. The Go RunE returns a bare error whose .Error() text
// must equal that JSON's "error" field, since main.go wraps it in the identical shape.
func assertReleaseFailureMatches(t *testing.T, reference, home string, args []string) {
	t.Helper()
	fullArgs := append([]string{"--home", home}, args...)
	cmd := exec.Command(reference, fullArgs...)
	var pyStderr bytes.Buffer
	cmd.Stderr = &pyStderr
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected python reference to fail, got success with stderr=%s", pyStderr.String())
	}
	var pyPayload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(pyStderr.Bytes(), &pyPayload); err != nil {
		t.Fatalf("python stderr is not the expected error JSON: %v (stderr=%s)", err, pyStderr.String())
	}

	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(fullArgs)
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected go command to fail, got success with stdout=%s", stdout.String())
	}
	if err.Error() != pyPayload.Error {
		t.Fatalf("go error = %q, want (python reference) %q", err.Error(), pyPayload.Error)
	}
}

func TestRelease_fallsBackToReferenceForStage(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	reference := filepath.Join(dir, "reference.sh")
	if err := os.WriteFile(reference, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$SUM_GO_ARGS_FILE\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_GO_ARGS_FILE", argsFile)

	home := filepath.Join(dir, "state")
	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs([]string{"--home", home, "release", "stage", "--ref", "HEAD"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "--home\n" + home + "\nrelease\nstage\n--ref\nHEAD\n"
	if string(got) != want {
		t.Fatalf("reference argv = %q, want %q", got, want)
	}
}
