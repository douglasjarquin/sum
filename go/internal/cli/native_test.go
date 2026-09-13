package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(buildCLIForTests(m))
}

func buildCLIForTests(m *testing.M) int {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		fmt.Fprintf(os.Stderr, "repo root: %v\n", err)
		return 1
	}
	out := filepath.Join(root, ".local", "bin", "sumctl")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		return 1
	}
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", out, "./cmd/sumctl")
	cmd.Dir = filepath.Join(root, "go")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "go build sumctl: %v\n%s\n", err, output)
		return 1
	}
	return m.Run()
}

func repoReference(t *testing.T) (repoRoot, reference string) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	reference = filepath.Join(repoRoot, "bin", "sumctl")
	if _, err := os.Stat(reference); err != nil {
		t.Skipf("reference bin/sumctl not found: %v", err)
	}
	return repoRoot, reference
}

func assertCLIMatches(t *testing.T, reference string, args []string) {
	t.Helper()
	want, err := exec.Command(reference, args...).Output()
	if err != nil {
		t.Fatalf("python reference failed: %v\n%s", err, want)
	}
	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(args)
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("go command failed: %v (stderr=%s)", err, stderr.String())
	}
	if stdout.String() != string(want) {
		t.Fatalf("go output =\n%s\nwant (python reference)\n%s", stdout.String(), want)
	}
}

func assertCLIFailureMatches(t *testing.T, reference string, args []string) {
	t.Helper()
	cmd := exec.Command(reference, args...)
	var pyStderr bytes.Buffer
	cmd.Stderr = &pyStderr
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected python reference to fail")
	}
	want := decodeCLIError(t, pyStderr.Bytes())
	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected go command to fail, stdout=%s", stdout.String())
	}
	if err.Error() != want {
		t.Fatalf("go error = %q, want %q", err.Error(), want)
	}
}

func TestHelp_matchesThePythonReference(t *testing.T) {
	_, reference := repoReference(t)
	home := t.TempDir()
	assertCLIMatches(t, reference, []string{"--home", home, "help"})
	assertCLIMatches(t, reference, []string{"--home", home, "help", "ask"})
	assertCLIMatches(t, reference, []string{"--home", home, "help", "skills-check"})
	assertCLIFailureMatches(t, reference, []string{"--home", home, "help", "not-a-command"})
}

func TestSkillsCheck_matchesThePythonReference(t *testing.T) {
	repoRoot, reference := repoReference(t)
	home := t.TempDir()
	assertCLIMatches(t, reference, []string{"--home", home, "skills", "check", "--root", repoRoot})
	empty := t.TempDir()
	assertCLIMatches(t, reference, []string{"--home", home, "skills", "check", "--root", empty})
}

func TestExecutionShow_matchesThePythonReference(t *testing.T) {
	_, reference := repoReference(t)
	home := t.TempDir()
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief_path": "brief.md"}`)
	assertCLIMatches(t, reference, []string{"--home", home, "execution", "show", "t-aaaaaaaaaaaa"})
}

func TestSkillsAndExecutionAreOnTheCommandTree(t *testing.T) {
	root := NewRoot("", &bytes.Buffer{}, &bytes.Buffer{})
	for _, name := range []string{"skills", "execution", "repair", "help", "quota", "notes", "herdr", "archive"} {
		if root.Commands() == nil {
			t.Fatal("no commands")
		}
		found := false
		for _, cmd := range root.Commands() {
			if cmd.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing command %s", name)
		}
	}
}

func TestAsk_matchesPythonWhenTheParentHasNoPane(t *testing.T) {
	_, reference := repoReference(t)
	home := t.TempDir()
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo",
"machine": "test-machine", "questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing",
"base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief_path": "brief.md", "parent": null}`)
	args := []string{"--home", home, "ask", "t-aaaaaaaaaaaa", "--text", "Keep going?"}
	want, err := exec.Command(reference, args...).Output()
	if err != nil {
		t.Fatalf("python reference failed: %v\n%s", err, want)
	}
	var stdout, stderr bytes.Buffer
	root := NewRoot(reference, &stdout, &stderr)
	root.SetArgs(args)
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("go command failed: %v (stderr=%s)", err, stderr.String())
	}
	id := regexp.MustCompile(`q-[0-9a-f]{10}`)
	stamp := regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\+00:00`)
	got := stamp.ReplaceAllString(id.ReplaceAllString(stdout.String(), "q-ID"), "<at>")
	wantNorm := stamp.ReplaceAllString(id.ReplaceAllString(string(want), "q-ID"), "<at>")
	if got != wantNorm {
		t.Fatalf("go output =\n%s\nwant (python reference)\n%s", stdout.String(), want)
	}
}

func TestSettingsSet_requiresCoordinatorLikePython(t *testing.T) {
	_, reference := repoReference(t)
	home := t.TempDir()
	assertCLIFailureMatches(t, reference, []string{"--home", home, "settings", "set", "--global", "3"})
}

func TestDefaultHomeUsesSUMHOME(t *testing.T) {
	_, reference := repoReference(t)
	home := t.TempDir()
	t.Setenv("SUM_HOME", home)
	assertCLIMatches(t, reference, []string{"settings", "show"})
}

func TestHelp_updateRollbackNamesDefaultAndRefusals(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	root := NewRoot("", &stdout, &stderr)
	root.SetArgs([]string{"--home", home, "help", "update-rollback"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("help update-rollback: %v (stderr=%s)", err, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"recorded previous",
		"staging is not approval",
		"No recorded previous known-good",
		"--to checkout",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q\n%s", want, out)
		}
	}
}
