package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// stagingInstallation clones this checkout's HEAD into a disposable installation with a designated state home,
// and configures offline staging so no pinned tool is downloaded.
func stagingInstallation(t *testing.T) (string, *store.Store) {
	t.Helper()
	source, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "installation")
	if out, err := exec.Command("git", "clone", "--quiet", strings.TrimSpace(string(source)), root).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v: %s", err, out)
	}
	home := filepath.Join(root, ".sum")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("staging builds native artifacts and needs go on PATH: %v", err)
	}
	t.Setenv("SUM_STAGE_OFFLINE", "1")
	t.Setenv("SUM_GO_BIN", goBin)
	for _, name := range []string{"SUM_HERDR_BIN", "SUM_GH_BIN", "SUM_CODEGRAPH_BIN"} {
		t.Setenv(name, "")
	}
	return root, s
}

// installRemainder mimics what setup leaves behind: the checksum-verified archive extracted under
// .deps/remainder/<version>-<platform> and the installation's .local/bin/remainder link to its executable.
func installRemainder(t *testing.T, root string) string {
	t.Helper()
	raw, err := ordjson.ReadFile(filepath.Join(root, "docs", "dependency-inventory.json"))
	if err != nil {
		t.Fatal(err)
	}
	var version string
	for _, item := range asList(getPath(asObject(raw), "dependencies")) {
		if id, _ := asObject(item).Get("id"); id == "remainder" {
			version = asString(getPath(asObject(item), "pins", nativePlatform(), "version"))
		}
	}
	if version == "" {
		t.Skipf("no Remainder pin for %s", nativePlatform())
	}
	binary := filepath.Join(root, ".deps", "remainder", version+"-"+nativePlatform(), "remainder_v"+version, "remainder")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho remainder\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".local", "bin", "remainder")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(filepath.Dir(link), binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relative, link); err != nil {
		t.Fatal(err)
	}
	return binary
}

func stagedManifest(t *testing.T, s *store.Store, root string) (string, *ordjson.Object) {
	t.Helper()
	summary, err := Stage(s, "HEAD")
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	path := asString(getPath(summary, "release"))
	if filepath.Dir(path) != filepath.Join(root, ".local", "releases") {
		t.Fatalf("staged path = %q, want a bundle under the installation's releases", path)
	}
	raw, err := ordjson.ReadFile(filepath.Join(path, Manifest))
	if err != nil {
		t.Fatal(err)
	}
	return path, asObject(raw)
}

func TestStage_linksTheInstallationsPinnedRemainder(t *testing.T) {
	root, s := stagingInstallation(t)
	binary := installRemainder(t, root)
	final, manifest := stagedManifest(t, s, root)

	link := filepath.Join(final, ".local", "bin", "remainder")
	dest, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("staged release has no remainder link: %v", err)
	}
	if dest != binary {
		t.Fatalf("remainder link = %q, want the installation's pinned executable %q", dest, binary)
	}
	if !isExecutable(link) {
		t.Fatalf("remainder link %s does not resolve to an executable", link)
	}
	if got := asString(getPath(manifest, "dependencies", "tools", "paths", "remainder")); got != binary {
		t.Fatalf("manifest remainder path = %q, want %q", got, binary)
	}
	if _, err := VerifyRelease(final, asString(getPath(manifest, "source", "sha"))); err != nil {
		t.Fatalf("VerifyRelease: %v", err)
	}
}

func TestStage_withoutRemainderStagesAndRecordsNoLink(t *testing.T) {
	root, s := stagingInstallation(t)
	final, manifest := stagedManifest(t, s, root)

	if _, err := os.Lstat(filepath.Join(final, ".local", "bin", "remainder")); !os.IsNotExist(err) {
		t.Fatalf("staged release has a remainder link without an installed Remainder: %v", err)
	}
	if _, has := asObject(getPath(manifest, "dependencies", "tools", "paths")).Get("remainder"); has {
		t.Fatal("manifest records remainder without an installed Remainder")
	}
}
