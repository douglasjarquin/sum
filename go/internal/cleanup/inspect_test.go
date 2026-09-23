package cleanup

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// sumCheckout is a git checkout that ignores .local/ like sum's own .gitignore and
// optionally tracks go/cmd/sumctl, the source mise run test/verify/demo build from.
func sumCheckout(t *testing.T, withSource bool) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string, mode os.FileMode) {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", ".local/\n.codegraph/\n", 0o644)
	if withSource {
		write("go/cmd/sumctl/main.go", "package main\n", 0o644)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "base"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write(".local/bin/sumctl", "\x7fELF", 0o755)
	write(".codegraph/codegraph.db", "index", 0o644)
	return dir
}

func ignoredLists(t *testing.T, checkout string) (disposable, preserved []any) {
	t.Helper()
	artifacts, err := worktreeArtifacts(checkout)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := artifacts.Get("ignored_disposable")
	p, _ := artifacts.Get("ignored_preserved")
	return d.([]any), p.([]any)
}

// The sumctl binary sum's own verification builds into a sum checkout is a
// disposable cache, the way .codegraph/ is.
func TestWorktreeArtifacts_sumBuiltBinaryIsDisposable(t *testing.T) {
	disposable, preserved := ignoredLists(t, sumCheckout(t, true))
	if len(preserved) != 0 {
		t.Fatalf("ignored_preserved = %v, want none", preserved)
	}
	if want := []any{".codegraph/", ".local/"}; !reflect.DeepEqual(disposable, want) {
		t.Fatalf("ignored_disposable = %v, want %v", disposable, want)
	}
}

// Anything under .local/ that sum did not generate keeps the whole entry preserved.
func TestWorktreeArtifacts_foreignLocalContentStillBlocks(t *testing.T) {
	cases := map[string]func(t *testing.T, checkout string){
		"foreign file beside the binary": func(t *testing.T, checkout string) {
			if err := os.WriteFile(filepath.Join(checkout, ".local/notes.md"), []byte("mine"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"symlink in place of the binary": func(t *testing.T, checkout string) {
			path := filepath.Join(checkout, ".local/bin/sumctl")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/bin/true", path); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			checkout := sumCheckout(t, true)
			mutate(t, checkout)
			disposable, preserved := ignoredLists(t, checkout)
			if want := []any{".local/"}; !reflect.DeepEqual(preserved, want) {
				t.Fatalf("ignored_preserved = %v, want %v", preserved, want)
			}
			if want := []any{".codegraph/"}; !reflect.DeepEqual(disposable, want) {
				t.Fatalf("ignored_disposable = %v, want %v", disposable, want)
			}
		})
	}
	t.Run("checkout that does not track the source", func(t *testing.T) {
		_, preserved := ignoredLists(t, sumCheckout(t, false))
		if want := []any{".local/"}; !reflect.DeepEqual(preserved, want) {
			t.Fatalf("ignored_preserved = %v, want %v", preserved, want)
		}
	})
}
