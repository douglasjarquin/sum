package project

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestStagedRuntimeKeepsStableInstallationProjectRoot(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, output)
	}
	s, err := store.Open(filepath.Join(root, ".sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(root, ".local/releases/candidate")
	if s.Home == s.Root {
		t.Fatal("test did not open a physical generation")
	}
	if got := InstallationOf(s, release); got != root {
		t.Fatalf("installation=%s want %s", got, root)
	}
	got, err := InstallationRootLike(s, release)
	if err != nil || got != root {
		t.Fatalf("root=%s error=%v", got, err)
	}
}
