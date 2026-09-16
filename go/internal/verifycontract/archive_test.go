package verifycontract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeCommitReadsTheApprovedTreeAfterWorkingCopyDrifts(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "sum test")
	run("config", "user.email", "test@example.invalid")
	contractPath := filepath.Join(repo, ContractFile)
	if err := os.WriteFile(contractPath, []byte("committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ContractFile)
	run("commit", "-q", "-m", "contract")
	revision, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contractPath, []byte("dirty working copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	root, cleanup, err := MaterializeCommit(repo, strings.TrimSpace(string(revision)))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	got, err := os.ReadFile(filepath.Join(root, ContractFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "committed\n" {
		t.Fatalf("snapshot = %q, want the committed contract", got)
	}
}
