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

// currentSkillNames is the current name of every Sum action, which is what this tree ships.
func currentSkillNames() []string {
	names := make([]string, 0, len(canonicalSkills))
	for _, group := range canonicalSkills {
		names = append(names, group[0])
	}
	return names
}

func leftoverAliasNames() []string {
	names := make([]string, 0, len(canonicalSkills))
	for _, name := range canonicalNames() {
		names = append(names, strings.TrimPrefix(name, "sum-"))
	}
	return names
}

// writeTree lays out a checkable tree: real canonical skill directories, alias directories whose
// SKILL.md names a target under `metadata`, both projected into each discovery route, plus the
// portable skills and the engineering principles reference.
func writeTree(t *testing.T, root string, canonical []string, aliases map[string]string) {
	t.Helper()
	write := func(path, body string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, path string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	projected := append([]string{}, canonical...)
	for _, name := range canonical {
		write(filepath.Join(root, "skills", name, "SKILL.md"), "---\nname: "+name+"\n---\n")
	}
	for name, target := range aliases {
		write(filepath.Join(root, "skills", name, "SKILL.md"), "---\nname: "+name+"\nmetadata:\n  alias: "+target+"\n---\n")
		projected = append(projected, name)
	}
	for _, route := range []string{".agents/skills", ".claude/skills"} {
		for _, name := range projected {
			link("../../skills/"+name, filepath.Join(root, route, name))
		}
	}
	for _, name := range portableSkillNames {
		write(filepath.Join(root, ".agents/skills", name, "SKILL.md"), "---\nname: "+name+"\n---\n")
		link("../../.agents/skills/"+name, filepath.Join(root, ".claude/skills", name))
	}
	write(filepath.Join(root, engineeringPrinciplesReference), "principles\n")
}

func checkOK(t *testing.T, root string) *ordjson.Object {
	t.Helper()
	view, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := view.Get("ok"); ok != true {
		t.Fatalf("skills check ok = %v, errors = %v", ok, errorStrings(view))
	}
	return view
}

func checkError(t *testing.T, root, want string) {
	t.Helper()
	view, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := view.Get("ok"); ok == true {
		t.Fatalf("skills check ok = true, want an error containing %q", want)
	}
	for _, msg := range errorStrings(view) {
		if strings.Contains(msg, want) {
			return
		}
	}
	t.Fatalf("errors = %v, want one containing %q", errorStrings(view), want)
}

func TestCheckAcceptsEitherNameOfARenamedSkill(t *testing.T) {
	for _, status := range []string{"sum-status", "sum-rundown"} {
		root := t.TempDir()
		names := []string{"sum-delivery", "sum-develop", "sum-dispatch", status, "sum-update", "sum-worker"}
		writeTree(t, root, names, nil)
		view := checkOK(t, root)
		active, _ := view.Get("active")
		if got := activeNames(active); strings.Join(got, ",") != strings.Join(names, ",") {
			t.Errorf("active = %v, want %v", got, names)
		}
	}
}

func TestCheckAcceptsTheEarlierNameAsAnAlias(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, currentSkillNames(), map[string]string{"sum-rundown": "sum-status"})
	view := checkOK(t, root)
	aliases, _ := view.Get("aliases")
	if target, _ := aliases.(*ordjson.Object).Get("sum-rundown"); target != "sum-status" {
		t.Fatalf("sum-rundown -> %v, want sum-status", target)
	}
}

func TestCheckRefusesBothNamesOfARenamedSkill(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, []string{"sum-delivery", "sum-develop", "sum-dispatch", "sum-status", "sum-rundown", "sum-update", "sum-worker"}, nil)
	checkError(t, root, "duplicate canonical skill: sum-status and sum-rundown")
}

func TestCheckAcceptsDeclaredAliases(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, currentSkillNames(), map[string]string{"sum-inbox": "sum-status", "sum-ship": "sum-delivery"})
	view := checkOK(t, root)
	aliases, _ := view.Get("aliases")
	obj, _ := aliases.(*ordjson.Object)
	if obj == nil || strings.Join(obj.Keys(), ",") != "sum-inbox,sum-ship" {
		t.Fatalf("aliases = %v, want sum-inbox and sum-ship", aliases)
	}
	if target, _ := obj.Get("sum-ship"); target != "sum-delivery" {
		t.Errorf("sum-ship -> %v, want sum-delivery", target)
	}
	active, _ := view.Get("active")
	if got := activeNames(active); contains(got, "sum-inbox") {
		t.Errorf("active = %v, must not list an alias", got)
	}
	routes, _ := view.Get("routes")
	claude, _ := routes.(*ordjson.Object).Get("claude")
	if got := activeNames(claude); !contains(got, "sum-inbox") {
		t.Errorf("claude route = %v, want the alias projected", got)
	}
}

func TestCheckRefusesAliasOfUnknownTarget(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, currentSkillNames(), map[string]string{"sum-ship": "sum-shipping"})
	checkError(t, root, "alias target mismatch")
}

func TestCheckRefusesUndeclaredSumSkill(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, currentSkillNames(), nil)
	if err := os.MkdirAll(filepath.Join(root, "skills", "sum-extra"), 0o755); err != nil {
		t.Fatal(err)
	}
	checkError(t, root, "namespace collision: unexpected Sum skill sum-extra")
}

func TestCheckRefusesUnprojectedAlias(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, currentSkillNames(), map[string]string{"sum-inbox": "sum-status"})
	if err := os.Remove(filepath.Join(root, ".claude/skills", "sum-inbox")); err != nil {
		t.Fatal(err)
	}
	checkError(t, root, "projection mismatch")
}

func activeNames(value any) []string {
	list, _ := value.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
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

// presentGroupName returns the one name of a canonical group this tree ships.
func presentGroupName(t *testing.T, root string, group []string) string {
	t.Helper()
	var found []string
	for _, name := range group {
		path := filepath.Join(root, "skills", name)
		if _, err := os.Lstat(path); err != nil || frontmatterField(filepath.Join(path, "SKILL.md"), "alias") != "" {
			continue
		}
		found = append(found, name)
	}
	if len(found) != 1 {
		t.Fatalf("skills/ ships %v of %v, want exactly one", found, group)
	}
	return found[0]
}

func TestCanonicalSumSkillsAreDirectories(t *testing.T) {
	root := repoRoot(t)
	for _, group := range canonicalSkills {
		path := filepath.Join(root, "skills", presentGroupName(t, root, group))
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
	for _, group := range canonicalSkills {
		if name := presentGroupName(t, root, group); !got[name] {
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

func TestCheckRefusesMissingEngineeringPrinciplesReference(t *testing.T) {
	root := t.TempDir()
	for _, name := range currentSkillNames() {
		path := filepath.Join(root, "skills", name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\n---\n"
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, route := range []string{".agents/skills", ".claude/skills"} {
		if err := os.MkdirAll(filepath.Join(root, route), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range currentSkillNames() {
			if err := os.Symlink("../../skills/"+name, filepath.Join(root, route, name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, name := range portableSkillNames {
		path := filepath.Join(root, ".agents/skills", name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\n---\n"
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../../.agents/skills/"+name, filepath.Join(root, ".claude/skills", name)); err != nil {
			t.Fatal(err)
		}
	}

	view, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := view.Get("ok"); ok == true {
		t.Fatal("skills check ok = true, want false when the engineering principles reference is missing")
	}
	found := false
	for _, msg := range errorStrings(view) {
		if strings.Contains(msg, "engineering-principles.md") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("errors = %v, want missing engineering-principles.md", errorStrings(view))
	}
}
