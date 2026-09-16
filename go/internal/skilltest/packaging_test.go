package skilltest

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const engineeringPrinciplesReferencePath = ".agents/skills/verify/references/engineering-principles.md"

func TestEngineeringPrinciplesReferenceIsPackagedAndPortable(t *testing.T) {
	root := repoRoot(t)
	reference := filepath.Join(root, filepath.FromSlash(engineeringPrinciplesReferencePath))
	info, err := os.Lstat(reference)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("reference is not a regular file: %s", reference)
	}
	for _, relative := range []string{"skills/sum-worker/SKILL.md", "skills/sum-delivery/SKILL.md"} {
		body := readFile(t, filepath.Join(root, relative))
		if !strings.Contains(body, "`"+engineeringPrinciplesReferencePath+"`") {
			t.Fatalf("%s does not point at %s", relative, engineeringPrinciplesReferencePath)
		}
	}

	archive, err := exec.Command("git", "-C", root, "archive", "--format=tar", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	archived := false
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		if header.Name == filepath.ToSlash(engineeringPrinciplesReferencePath) {
			archived = true
			break
		}
	}
	if !archived {
		t.Fatalf("git archive HEAD does not contain %s", engineeringPrinciplesReferencePath)
	}

	v := newVerifyLab(t)
	repo := v.rawRepo("cli", filepath.Join(v.stop, "packaged-rubric"))
	code, _, stderr := v.scaffold(repo, "--write")
	if code != 0 {
		t.Fatal(stderr)
	}
	copied := filepath.Join(repo, filepath.FromSlash(engineeringPrinciplesReferencePath))
	copiedInfo, err := os.Lstat(copied)
	if err != nil {
		t.Fatal(err)
	}
	if !copiedInfo.Mode().IsRegular() {
		t.Fatalf("portable reference is not a regular file: %s", copied)
	}
	if strings.Contains(readFile(t, copied), root) {
		t.Fatalf("portable reference contains the Sum checkout path: %s", copied)
	}
	v.fill(repo, "cli")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "portable verification contract")
	removeAll(t, filepath.Dir(v.toolkit))
	head := git(t, repo, "rev-parse", "HEAD")
	code, record, stderr := v.runner(repo, "--base", head)
	if code != 0 || asString(record["outcome"]) != "pass" {
		t.Fatalf("portable clone verification failed: %v %s", record, stderr)
	}
}

func TestScaffoldMissingGuidanceStaysDraft(t *testing.T) {
	v := newVerifyLab(t)
	repo := v.rawRepo("service", filepath.Join(v.stop, "missing-guidance"))
	removeAll(t, filepath.Join(repo, "README.md"))
	removeAll(t, filepath.Join(repo, "app.py"))
	code, record, stderr := v.scaffold(repo)
	if code != 0 {
		t.Fatal(stderr)
	}
	inspection := asMap(record["inspection"])
	if len(asSlice(inspection["guidance"])) != 0 || len(asSlice(inspection["cli_entrypoints"])) != 0 || len(asSlice(inspection["routes"])) != 0 {
		t.Fatalf("inspection invented guidance or entrypoints: %v", inspection)
	}
	plan := asMap(record["plan"])
	commands := asSlice(plan["commands"])
	if len(commands) != 1 || asString(commands[0]) != "mise run test" {
		t.Fatalf("commands = %v, want only the declared test task", commands)
	}
	features := asSlice(plan["features"])
	if len(features) != 0 {
		t.Fatalf("features = %v, want no invented examples", features)
	}
	code, written, stderr := v.scaffold(repo, "--write")
	if code != 0 {
		t.Fatal(stderr)
	}
	if !strings.Contains(readFile(t, filepath.Join(repo, "VERIFY.md")), "TODO(verify): how a fresh clone is prepared") {
		t.Fatal("missing setup guidance was not kept as an onboarding placeholder")
	}
	index := filepath.Join(repo, "docs/features/README.md")
	if !strings.Contains(readFile(t, index), "Inventory: **incomplete**") {
		t.Fatal("empty feature inventory was not left incomplete")
	}
	if len(asSlice(written["problems"])) != 0 {
		t.Fatalf("unexpected scaffold problems: %v", written["problems"])
	}
}

func TestScaffoldInspectListsArchitectureFilesAndWritesNothing(t *testing.T) {
	v := newVerifyLab(t)
	repo := v.rawRepo("cli", filepath.Join(v.stop, "architecture-inspect"))
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), "# Agents\n")
	mustWrite(t, filepath.Join(repo, "CODEOWNERS"), "* @owner\n")
	mustWrite(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), "name: ci\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "architecture files")
	code, record, stderr := v.scaffold(repo)
	if code != 0 {
		t.Fatal(stderr)
	}
	if asString(record["mode"]) != "inspect" {
		t.Fatalf("mode = %v, want inspect", record["mode"])
	}
	guidance := asSlice(asMap(record["inspection"])["guidance"])
	found := map[string]bool{}
	for _, item := range guidance {
		found[asString(item)] = true
	}
	if !found["AGENTS.md"] || !found["CODEOWNERS"] || !found[".github/workflows/"] {
		t.Fatalf("guidance = %v, want AGENTS.md, CODEOWNERS, and .github/workflows/", guidance)
	}
	if _, err := os.Stat(filepath.Join(repo, "VERIFY.md")); err == nil {
		t.Fatal("inspect wrote VERIFY.md")
	}
}

func TestScaffoldReportsVerifyTaskCycle(t *testing.T) {
	v := newVerifyLab(t)
	repo := v.rawRepo("cli", filepath.Join(v.stop, "verify-cycle"))
	mustWrite(t, filepath.Join(repo, "mise.toml"), "[tasks]\nverify = \"mise run verify\"\ntest = \"python3 -m unittest discover -s tests -p 'test_*.py'\"\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "cyclic verify")
	code, record, stderr := v.scaffold(repo)
	if asMap(record["inspection"])["verify_cycle"] != true {
		t.Fatalf("verify_cycle = %v stderr=%s, want true", asMap(record["inspection"])["verify_cycle"], stderr)
	}
	found := false
	for _, item := range asSlice(record["problems"]) {
		if strings.Contains(asString(item), "cycle") || strings.Contains(asString(item), "mise run verify") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("problems = %v code=%d, want a cycle problem", record["problems"], code)
	}
}

func TestScaffoldReadmeMapMarksObservedRowsAndGaps(t *testing.T) {
	v := newVerifyLab(t)
	repo := v.rawRepo("cli", filepath.Join(v.stop, "readme-map"))
	code, record, stderr := v.scaffold(repo)
	if code != 0 {
		t.Fatal(stderr)
	}
	if _, err := os.Stat(filepath.Join(repo, "VERIFY.md")); err == nil {
		t.Fatal("inspect wrote VERIFY.md")
	}
	var testRow map[string]any
	for _, item := range asSlice(record["readme_map"]) {
		row := asMap(item)
		if asString(row["task"]) == "test" {
			testRow = row
			break
		}
	}
	if asString(testRow["kind"]) != "observed" || asString(testRow["owner"]) != "mise.toml" || asString(testRow["example"]) != "tests/test_hello.py" || asString(testRow["check"]) != "mise run test" {
		t.Fatalf("test row = %v, want observed mise.toml tests/test_hello.py mise run test", testRow)
	}
	var lintRow map[string]any
	for _, item := range asSlice(record["readme_map"]) {
		row := asMap(item)
		if asString(row["task"]) == "lint" {
			lintRow = row
			break
		}
	}
	if asString(lintRow["kind"]) != "gap" || lintRow["owner"] != nil || lintRow["example"] != nil || lintRow["check"] != nil {
		t.Fatalf("lint row = %v, want an explicit gap with no invented owner", lintRow)
	}
}

func TestScaffoldUpdateKeepsLocalEditsAndRecordsProvenance(t *testing.T) {
	v := newVerifyLab(t)
	first := commitToolkit(t, v, "stock skills")
	repo := v.rawRepo("cli", filepath.Join(v.stop, "revisioned"))
	code, written, stderr := v.scaffold(repo, "--write", "--revision", first)
	if code != 0 {
		t.Fatal(stderr)
	}
	provPath := filepath.Join(repo, ".agents/skills/.verification-provenance.json")
	firstProv := asMap(mustJSON(t, readFile(t, provPath)))
	if asString(firstProv["source_revision"]) != first {
		t.Fatalf("source_revision = %v, want %s", firstProv["source_revision"], first)
	}
	if asString(written["mode"]) != "write" {
		t.Fatalf("mode = %v, want write", written["mode"])
	}
	skill := filepath.Join(repo, ".agents/skills/verify/SKILL.md")
	mustWrite(t, skill, readFile(t, skill)+"\nlocal edit\n")
	unrelated := filepath.Join(repo, "notes.txt")
	mustWrite(t, unrelated, "project authored\n")
	contract := readFile(t, filepath.Join(repo, "VERIFY.md"))
	stock := filepath.Join(v.stop, "toolkit/.agents/skills/verify/references/engineering-principles.md")
	mustWrite(t, stock, readFile(t, stock)+"\nstock bump\n")
	second := commitToolkit(t, v, "newer stock")
	code, updated, stderr := v.scaffold(repo, "--update", "--revision", second)
	if code != 0 {
		t.Fatal(stderr)
	}
	if asString(updated["mode"]) != "update" {
		t.Fatalf("mode = %v, want update", updated["mode"])
	}
	if !strings.Contains(readFile(t, skill), "local edit") {
		t.Fatal("update overwrote a locally edited skill")
	}
	if readFile(t, filepath.Join(repo, "VERIFY.md")) != contract {
		t.Fatal("update rewrote VERIFY.md")
	}
	if readFile(t, unrelated) != "project authored\n" {
		t.Fatal("update deleted a project-authored file")
	}
	copied := filepath.Join(repo, ".agents/skills/verify/references/engineering-principles.md")
	if !strings.Contains(readFile(t, copied), "stock bump") {
		t.Fatal("unchanged stock file was not replaced from the named revision")
	}
	var conflict bool
	for _, item := range asSlice(updated["results"]) {
		row := asMap(item)
		if asString(row["path"]) == ".agents/skills/verify/SKILL.md" && asString(row["status"]) == "conflict" {
			conflict = true
		}
	}
	if !conflict {
		t.Fatalf("results = %v, want a conflict for the locally edited skill", updated["results"])
	}
	secondProv := asMap(mustJSON(t, readFile(t, provPath)))
	if asString(secondProv["source_revision"]) != second {
		t.Fatalf("updated source_revision = %v, want %s", secondProv["source_revision"], second)
	}
	code, again, stderr := v.scaffold(repo, "--update", "--revision", second)
	if code != 0 {
		t.Fatal(stderr)
	}
	for _, item := range asSlice(again["results"]) {
		status := asString(asMap(item)["status"])
		if status != "unchanged" && status != "conflict" && status != "kept" {
			t.Fatalf("second update wrote %v", item)
		}
	}
}

func TestScaffoldUpdateRefusesFloatingRevision(t *testing.T) {
	v := newVerifyLab(t)
	commitToolkit(t, v, "stock skills")
	repo := v.rawRepo("cli", filepath.Join(v.stop, "floating"))
	v.scaffold(repo, "--write")
	for _, name := range []string{"main", "latest", "HEAD", "abc"} {
		code, record, stderr := v.scaffold(repo, "--update", "--revision", name)
		if code == 0 {
			t.Fatalf("revision %q succeeded: %v %s", name, record, stderr)
		}
		if record == nil {
			t.Fatalf("revision %q produced no JSON record: %s", name, stderr)
		}
		text := ""
		for _, item := range asSlice(record["problems"]) {
			text += asString(item)
		}
		if !strings.Contains(text, "immutable") {
			t.Fatalf("revision %q problems = %v, want an immutable-revision refusal", name, record["problems"])
		}
	}
}

func commitToolkit(t *testing.T, v *verifyLab, message string) string {
	t.Helper()
	root := filepath.Join(v.stop, "toolkit")
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		git(t, root, "init", "-q", "-b", "main")
		git(t, root, "config", "user.email", "lab@example.invalid")
		git(t, root, "config", "user.name", "verify lab")
		git(t, root, "config", "commit.gpgsign", "false")
	}
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "--allow-empty", "-m", message)
	return git(t, root, "rev-parse", "HEAD")
}

func mustJSON(t *testing.T, body string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("json: %v\n%s", err, body)
	}
	return out
}
