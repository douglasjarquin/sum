package verifycontract

import (
	"bytes"
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

func TestMaterializeCommitSkipsUnrelatedLargeFiles(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, ContractFile), []byte("committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "unrelated.bin"), bytes.Repeat([]byte("x"), 2<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "contract")
	revision, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	root, cleanup, err := MaterializeCommit(repo, strings.TrimSpace(string(revision)))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := os.Stat(filepath.Join(root, "unrelated.bin")); !os.IsNotExist(err) {
		t.Fatalf("unrelated file was materialized: %v", err)
	}
}
