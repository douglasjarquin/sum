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

func repoReference(t *testing.T) (root, reference string) {
	t.Helper()
	root = repoRoot(t)
	reference = filepath.Join(root, "bin", "sumctl")
	return root, reference
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

func TestHelp_pinsStdout(t *testing.T) {
	home := t.TempDir()
	assertStdoutGolden(t, home, []string{"help"}, "help")
	assertStdoutGolden(t, home, []string{"help", "ask"}, "help-ask")
	assertStdoutGolden(t, home, []string{"help", "skills-check"}, "help-skills-check")
	assertErrorGolden(t, home, []string{"help", "not-a-command"}, "help-unknown")
}

func TestSkillsCheck_pinsStdout(t *testing.T) {
	home := t.TempDir()
	assertStdoutGolden(t, home, []string{"skills", "check", "--root", repoRoot(t)}, "skills-check-repo")
	empty := t.TempDir()
	assertStdoutGolden(t, empty, []string{"skills", "check", "--root", empty}, "skills-check-empty")
}

func TestExecutionShow_pinsStdout(t *testing.T) {
	home := t.TempDir()
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief_path": "brief.md"}`)
	assertStdoutGolden(t, home, []string{"execution", "show", "t-aaaaaaaaaaaa"}, "execution-show")
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

func TestAsk_pinsStdoutWhenTheParentHasNoPane(t *testing.T) {
	home := t.TempDir()
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo",
"machine": "test-machine", "questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing",
"base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief_path": "brief.md", "parent": null}`)
	id := regexp.MustCompile(`q-[0-9a-f]{10}`)
	stamp := regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\+00:00`)
	assertStdoutGoldenNormalized(t, home, []string{"ask", "t-aaaaaaaaaaaa", "--text", "Keep going?"}, "ask-no-parent", func(s string) string {
		return stamp.ReplaceAllString(id.ReplaceAllString(s, "q-ID"), "<at>")
	})
}

func TestSettingsSet_requiresCoordinator(t *testing.T) {
	home := t.TempDir()
	assertErrorGolden(t, home, []string{"settings", "set", "--global", "3"}, "settings-set-requires-coordinator")
}

func TestDefaultHomeUsesSUMHOME(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUM_HOME", home)
	assertStdoutGolden(t, home, []string{"settings", "show"}, "settings-show-sum-home")
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
		"Staging is not approval",
		"No recorded previous known-good",
		"--to checkout",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q\n%s", want, out)
		}
	}
}
