package skilltest

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/procedure"
)

// roleFiles are the files a session is told to read: the bootstrap every harness loads, the role cores
// `init` names, the worker procedure `procedure.Sources` pins into briefs, and every action skill a
// role core routes to.
func roleFiles(t *testing.T, root string) []string {
	t.Helper()
	files := []string{"AGENTS.md", procedure.Coordinator.Path, procedure.Developer.Path}
	for _, src := range procedure.Sources {
		files = append(files, src.Path)
	}
	skills, err := filepath.Glob(filepath.Join(root, "skills", "*", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range skills {
		rel, _ := filepath.Rel(root, path)
		files = append(files, filepath.ToSlash(rel))
	}
	return files
}

var referencedPath = regexp.MustCompile("`((?:skills|\\.agents/skills|docs|templates)/[^`\\s]+\\.md|[A-Z][A-Z_]*\\.md)`|\\]\\(([^)\\s]+\\.md)\\)")

func trackedFiles(t *testing.T, root string) map[string]bool {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "ls-files").Output()
	if err != nil {
		t.Fatal(err)
	}
	tracked := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		tracked[line] = true
	}
	return tracked
}

// Every file a role is told to read, and every repository file those name, exists as a regular file and
// is tracked, so `git archive` puts it in every release tree and task runtime.
func TestRoleFilesAndTheFilesTheyNameShipInEveryRuntime(t *testing.T) {
	root := repoRoot(t)
	tracked := trackedFiles(t, root)
	seen := map[string]bool{}
	for _, rel := range roleFiles(t, root) {
		if seen["file:"+rel] {
			continue
		}
		seen["file:"+rel] = true
		if !tracked[rel] {
			t.Errorf("%s is not tracked, so no release tree or task runtime carries it", rel)
		}
		body := readFile(t, filepath.Join(root, filepath.FromSlash(rel)))
		for _, match := range referencedPath.FindAllStringSubmatch(body, -1) {
			named := match[1] + match[2]
			if seen[named] {
				continue
			}
			seen[named] = true
			info, err := os.Stat(filepath.Join(root, filepath.FromSlash(named)))
			if err != nil || !info.Mode().IsRegular() {
				t.Errorf("%s names %s, which is not a file in this tree", rel, named)
				continue
			}
			if !tracked[named] && !trackedThroughLink(t, root, named, tracked) {
				t.Errorf("%s names %s, which is not tracked", rel, named)
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("no referenced files found; the pattern no longer matches the role files")
	}
}

// trackedThroughLink accepts a path reached through a tracked projection symlink (.agents/skills/sum-x).
func trackedThroughLink(t *testing.T, root, rel string, tracked map[string]bool) bool {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	realRoot, _ := filepath.EvalSymlinks(root)
	inside, err := filepath.Rel(realRoot, resolved)
	return err == nil && tracked[filepath.ToSlash(inside)]
}

// Every installed helper since the namespace check refuses to stage a tree with an unknown `sum-*` skill
// directory or projection, and `update apply` stages with the installed helper. Adding one would make the
// release uninstallable through `update`, so role and action procedures live inside these skills or beside
// AGENTS.md instead. A canonical rename or a new alias directory needs a release whose checker accepts the
// new name first (see `canonicalSkills` and the `alias` frontmatter line in `internal/skills`); this test pins
// what the tree ships today.
func TestSumSkillSetIsWhatInstalledHelpersAccept(t *testing.T) {
	root := repoRoot(t)
	want := []string{"sum-delivery", "sum-develop", "sum-dispatch", "sum-rundown", "sum-update", "sum-worker"}
	for _, dir := range []string{"skills", ".agents/skills", ".claude/skills"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "sum-") {
				got = append(got, entry.Name())
			}
		}
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s sum-* entries = %v, want %v", dir, got, want)
		}
	}
}
