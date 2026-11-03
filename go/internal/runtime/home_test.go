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

func TestInheritedHomeBelongsToInstallation(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	insideLink := filepath.Join(root, "outside")
	if err := os.Symlink(outside, insideLink); err != nil {
		t.Fatal(err)
	}
	outsideLink := filepath.Join(outside, "installation")
	if err := os.Symlink(root, outsideLink); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		home string
		want bool
	}{
		{"unset", "", false},
		{"root", root, true},
		{"local store", filepath.Join(root, ".sum"), true},
		{"generation", filepath.Join(root, ".sum", "generations", "g-test"), true},
		{"foreign", outside, false},
		{"same prefix", root + "-other", false},
		{"parent traversal", filepath.Join(root, "..", "foreign"), false},
		{"symlink escape", filepath.Join(insideLink, ".sum"), false},
		{"symlink into installation", filepath.Join(outsideLink, ".sum"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SUM_HOME", tc.home)
			if got := InheritedHomeBelongsToInstallation(root); got != tc.want {
				t.Fatalf("inherited home belongs = %v, want %v", got, tc.want)
			}
		})
	}
}
