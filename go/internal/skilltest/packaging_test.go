package skilltest

import (
	"archive/tar"
	"bytes"
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
