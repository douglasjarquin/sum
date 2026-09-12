package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
		{"go/cmd/sumctl/main.go", "package main\n"},
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

func TestReleaseList_emptyAndMixedBundles(t *testing.T) {
	t.Run("no releases directory", func(t *testing.T) {
		_, home := buildInstallation(t)
		out := runRelease(t, home, []string{"release", "list"})
		if !strings.Contains(out, "releases:") && !strings.Contains(out, `"releases"`) {
			t.Fatalf("missing releases key: %s", out)
		}
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
		out := runRelease(t, home, []string{"release", "list"})
		if !strings.Contains(out, validSHA) {
			t.Fatalf("valid SHA missing: %s", out)
		}
		if !strings.Contains(out, "ok: false") && !strings.Contains(out, `"ok": false`) {
			t.Fatalf("broken bundle should be ok=false: %s", out)
		}
	})
}

func TestReleaseShow_shaAndPrefix(t *testing.T) {
	t.Run("full SHA match", func(t *testing.T) {
		root, home := buildInstallation(t)
		validSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		buildValidRelease(t, filepath.Join(root, ".local", "releases"), validSHA)
		out := runRelease(t, home, []string{"release", "show", validSHA})
		if !strings.Contains(out, validSHA) {
			t.Fatalf("show missing SHA: %s", out)
		}
	})

	t.Run("short SHA prefix match", func(t *testing.T) {
		root, home := buildInstallation(t)
		validSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		buildValidRelease(t, filepath.Join(root, ".local", "releases"), validSHA)
		out := runRelease(t, home, []string{"release", "show", "aaaaaaa"})
		if !strings.Contains(out, validSHA) {
			t.Fatalf("prefix show missing SHA: %s", out)
		}
	})

	t.Run("no match is a command-level failure", func(t *testing.T) {
		root, home := buildInstallation(t)
		validSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		buildValidRelease(t, filepath.Join(root, ".local", "releases"), validSHA)
		var stdout, stderr bytes.Buffer
		cli := NewRoot("", &stdout, &stderr)
		cli.SetArgs([]string{"--home", home, "release", "show", "ffffffff"})
		err := cli.ExecuteContext(context.Background())
		if err == nil {
			t.Fatalf("expected failure, got %s", stdout.String())
		}
	})
}

func runRelease(t *testing.T, home string, args []string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := NewRoot("", &stdout, &stderr)
	root.SetArgs(append([]string{"--home", home}, args...))
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("go command failed: %v (stderr=%s)", err, stderr.String())
	}
	return stdout.String()
}

func TestReleaseStage_requiresInstallationHome(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRoot("", &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"--home", home, "release", "stage", "--ref", "HEAD"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected installation home requirement")
	}
}
