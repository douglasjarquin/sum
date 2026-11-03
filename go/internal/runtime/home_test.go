package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultHomeScopesInheritedStateToInstallation(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	t.Setenv("SUM_HOME", outside)
	if got := DefaultHome(root); got != filepath.Join(root, ".sum") {
		t.Fatalf("unrelated inherited home = %s", got)
	}
	inside := filepath.Join(root, ".sum", "generations", "g-test")
	t.Setenv("SUM_HOME", inside)
	if got := DefaultHome(root); got != resolve(inside) {
		t.Fatalf("generation home = %s", got)
	}
	link := filepath.Join(root, "outside")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_HOME", link)
	if got := DefaultHome(root); got != filepath.Join(root, ".sum") {
		t.Fatalf("symlink escaped home = %s", got)
	}
}
