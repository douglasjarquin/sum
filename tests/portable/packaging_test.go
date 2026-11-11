package skilltest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const engineeringPrinciplesReferencePath = ".agents/skills/verify/references/engineering-principles.md"

func TestEngineeringPrinciplesReferenceIsPackagedAndPortable(t *testing.T) {
	root := repoRoot(t)
	for _, relative := range []string{"skills/sum-review/SKILL.md", "skills/sum-finish/SKILL.md"} {
		body := readFile(t, filepath.Join(root, relative))
		if !strings.Contains(body, engineeringPrinciplesReferencePath) {
			t.Fatalf("%s does not point at %s", relative, engineeringPrinciplesReferencePath)
		}
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
	if readFile(t, copied) != readFile(t, filepath.Join(root, "tools/verify/skills/verify/references/engineering-principles.md")) {
		t.Fatalf("the binary vendored a reference that differs from its source: %s", copied)
	}
	wrapper, err := os.Stat(filepath.Join(repo, ".agents/skills/verify/scripts/sum-verify"))
	if err != nil {
		t.Fatal(err)
	}
	if wrapper.Mode().Perm()&0o111 == 0 {
		t.Fatalf("vendored sum-verify wrapper is not executable: %v", wrapper.Mode())
	}
	v.fill(repo, "cli")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "portable verification contract")
	head := git(t, repo, "rev-parse", "HEAD")
	code, record, stderr := v.runner(repo, "--base", head)
	if code != 0 || asString(record["outcome"]) != "certified" {
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

func TestAuditFlagsStaleArchitectureExamplePath(t *testing.T) {
	v := newVerifyLab(t)
	repo := v.rawRepo("cli", filepath.Join(v.stop, "stale-arch"))
	v.scaffold(repo, "--write")
	v.fill(repo, "cli")
	mustWrite(t, filepath.Join(repo, "ARCHITECTURE.md"), "The owner example is `docs/missing-example.md`.\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "architecture example")
	code, audit, stderr := v.audit(repo)
	if code != 1 || asString(audit["outcome"]) != "findings" {
		t.Fatalf("audit %v code=%d stderr=%s, want findings", audit, code, stderr)
	}
	found := false
	for _, item := range asSlice(audit["findings"]) {
		row := asMap(item)
		if asString(row["kind"]) == "stale-path" && asString(row["file"]) == "ARCHITECTURE.md" && strings.Contains(asString(row["detail"]), "docs/missing-example.md") {
			found = true
		}
	}
	if !found {
		t.Fatalf("findings = %v, want stale-path on ARCHITECTURE.md", audit["findings"])
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
	repo := v.rawRepo("cli", filepath.Join(v.stop, "updated"))
	code, written, stderr := v.scaffold(repo, "--write")
	if code != 0 {
		t.Fatal(stderr)
	}
	if asString(written["mode"]) != "write" {
		t.Fatalf("mode = %v, want write", written["mode"])
	}
	provPath := filepath.Join(repo, ".agents/skills/.verification-provenance.json")
	firstProv := mustJSON(t, readFile(t, provPath))
	if firstProv["schema"] != float64(2) || asString(firstProv["tool_version"]) != "dev" {
		t.Fatalf("provenance = schema %v tool_version %v, want schema 2 from the dev build", firstProv["schema"], firstProv["tool_version"])
	}
	if _, present := firstProv["source_revision"]; present {
		t.Fatalf("provenance still records a source revision: %v", firstProv)
	}
	skill := filepath.Join(repo, ".agents/skills/verify/SKILL.md")
	mustWrite(t, skill, readFile(t, skill)+"\nlocal edit\n")
	unrelated := filepath.Join(repo, "notes.txt")
	mustWrite(t, unrelated, "project authored\n")
	contract := readFile(t, filepath.Join(repo, "VERIFY.md"))
	// An older release's stock file: the copy on disk still matches the hash recorded when it was vendored.
	copied := filepath.Join(repo, filepath.FromSlash(engineeringPrinciplesReferencePath))
	embedded := readFile(t, copied)
	older := "older stock\n"
	mustWrite(t, copied, older)
	sum := sha256.Sum256([]byte(older))
	for _, item := range asSlice(firstProv["files"]) {
		if row := asMap(item); asString(row["path"]) == engineeringPrinciplesReferencePath {
			row["sha256"] = hex.EncodeToString(sum[:])
		}
	}
	firstProv["tool_version"] = "0.0.1"
	stale, err := json.Marshal(firstProv)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, provPath, string(stale))
	code, updated, stderr := v.scaffold(repo, "--update")
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
	if readFile(t, copied) != embedded {
		t.Fatal("unchanged stock file was not replaced with the binary's embedded copy")
	}
	statuses := map[string]string{}
	for _, item := range asSlice(updated["results"]) {
		row := asMap(item)
		statuses[asString(row["path"])] = asString(row["status"])
	}
	if statuses[".agents/skills/verify/SKILL.md"] != "conflict" || statuses[engineeringPrinciplesReferencePath] != "updated" {
		t.Fatalf("results = %v, want a conflict for the locally edited skill and an update for the unchanged stock file", updated["results"])
	}
	secondProv := mustJSON(t, readFile(t, provPath))
	if secondProv["schema"] != float64(2) || asString(secondProv["tool_version"]) != "dev" {
		t.Fatalf("updated provenance = schema %v tool_version %v, want schema 2 from the dev build", secondProv["schema"], secondProv["tool_version"])
	}
	code, again, stderr := v.scaffold(repo, "--update")
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

func mustJSON(t *testing.T, body string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("json: %v\n%s", err, body)
	}
	return out
}
