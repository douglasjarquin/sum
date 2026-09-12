package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func leftoverAliasNames() []string {
	names := make([]string, 0, len(sumSkillNames))
	for _, name := range sumSkillNames {
		names = append(names, strings.TrimPrefix(name, "sum-"))
	}
	return names
}

func errorStrings(view *ordjson.Object) []string {
	value, _ := view.Get("errors")
	list, _ := value.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}

func TestLeftoverUnprefixedAliasesAreAbsent(t *testing.T) {
	root := repoRoot(t)
	for _, parent := range []string{"skills", ".agents/skills", ".claude/skills"} {
		for _, name := range leftoverAliasNames() {
			path := filepath.Join(root, parent, name)
			_, err := os.Lstat(path)
			if err == nil {
				t.Errorf("leftover unprefixed skill alias exists: %s", path)
				continue
			}
			if !os.IsNotExist(err) {
				t.Errorf("stat %s: %v", path, err)
			}
		}
	}
}

func TestCanonicalSumSkillsAreDirectories(t *testing.T) {
	root := repoRoot(t)
	for _, name := range sumSkillNames {
		path := filepath.Join(root, "skills", name)
		info, err := os.Lstat(path)
		if err != nil {
			t.Errorf("missing canonical skill directory: %s", path)
			continue
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Errorf("%s must be a real directory, not a symlink", path)
		}
		skill := filepath.Join(path, "SKILL.md")
		if _, err := os.Stat(skill); err != nil {
			t.Errorf("missing %s", skill)
		}
	}
}

func TestPortableUnprefixedSkillsRemain(t *testing.T) {
	root := repoRoot(t)
	for _, name := range portableSkillNames {
		path := filepath.Join(root, ".agents/skills", name, "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("portable skill missing: %s", path)
		}
	}
}

func TestCheckRepoUsesCanonicalNamesOnly(t *testing.T) {
	root := repoRoot(t)
	view, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	ok, _ := view.Get("ok")
	if ok != true {
		t.Fatalf("skills check ok = %v, errors = %v", ok, errorStrings(view))
	}
	compat, _ := view.Get("compatibility")
	list, _ := compat.([]any)
	if len(list) != 0 {
		t.Fatalf("compatibility = %v, want empty", list)
	}
	active, _ := view.Get("active")
	got := map[string]bool{}
	for _, item := range active.([]any) {
		name, _ := item.(string)
		got[name] = true
	}
	for _, name := range sumSkillNames {
		if !got[name] {
			t.Errorf("active missing %s", name)
		}
	}
}

func TestCheckRefusesLeftoverAlias(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "skills", "worker")
	if err := os.Symlink("sum-worker", alias); err != nil {
		t.Fatal(err)
	}
	view, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, msg := range errorStrings(view) {
		if strings.Contains(msg, "leftover unprefixed skill alias") && strings.Contains(msg, alias) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("errors = %v, want leftover alias %s", errorStrings(view), alias)
	}
	ok, _ := view.Get("ok")
	if ok == true {
		t.Fatal("skills check ok = true, want false when a leftover alias exists")
	}
}
