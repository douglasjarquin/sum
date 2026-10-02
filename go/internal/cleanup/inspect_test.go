package cleanup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func ignoredPaths(t *testing.T, checkout string) (*ordjson.Object, []any) {
	t.Helper()
	artifacts, err := worktreeArtifacts(checkout)
	if err != nil {
		t.Fatal(err)
	}
	paths, _ := artifacts.Get("ignored_paths")
	return artifacts, paths.([]any)
}

func TestWorktreeArtifacts_listsIgnoredPathsWithBoundedOutput(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env.*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", ".gitignore"},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "base"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	for i := 0; i < ignoredPathLimit+5; i++ {
		path := filepath.Join(dir, fmt.Sprintf(".env.%02d", i))
		if err := os.WriteFile(path, []byte("build output"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	artifacts, paths := ignoredPaths(t, dir)
	if len(paths) != ignoredPathLimit {
		t.Fatalf("ignored_paths has %d entries, want limit %d", len(paths), ignoredPathLimit)
	}
	if count, _ := artifacts.Get("ignored_count"); fmt.Sprint(count) != fmt.Sprint(ignoredPathLimit+5) {
		t.Fatalf("ignored_count = %v, want %d", count, ignoredPathLimit+5)
	}
	if omitted, _ := artifacts.Get("ignored_omitted"); fmt.Sprint(omitted) != "5" {
		t.Fatalf("ignored_omitted = %v, want 5", omitted)
	}
	if _, exists := artifacts.Get("ignored_preserved"); exists {
		t.Fatal("ignored_preserved still exists")
	}
}

func TestWorktreeArtifacts_includesIgnoredDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".astro/\ndist/\nbuild/\n.env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", ".gitignore"},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "base"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	for _, rel := range []string{"web/.astro/cache", "web/dist/index.html", "build/output", ".env"} {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("generated"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, paths := ignoredPaths(t, dir)
	want := []any{".env", "build/", "web/.astro/", "web/dist/"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("ignored_paths = %v, want %v", paths, want)
	}
}
