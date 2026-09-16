package verifycontract

import (
	"archive/tar"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestExtractArchiveSkipsSymlinksBeforeWritingChildren(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{Name: "escape", Typeflag: tar.TypeSymlink, Linkname: "../../outside"}); err != nil {
		t.Fatal(err)
	}
	contents := []byte("inside\n")
	if err := writer.WriteHeader(&tar.Header{Name: "escape/VERIFY.md", Mode: 0o644, Size: int64(len(contents))}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	if err := extractArchive(bytes.NewReader(archive.Bytes()), root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "escape", "VERIFY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(contents) {
		t.Fatalf("extracted contents = %q, want %q", got, contents)
	}
}

func TestMaterializeCommitStopsAfterRejectedArchiveEntry(t *testing.T) {
	repo := t.TempDir()
	binDir := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "archive.tar")
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	payload := bytes.Repeat([]byte("x"), 8<<20)
	if err := writer.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o644, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath, archive.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	gitPath := filepath.Join(binDir, "git")
	if err := os.WriteFile(gitPath, []byte("#!/bin/sh\ncat \"$SUM_TEST_ARCHIVE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_TEST_ARCHIVE", archivePath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	done := make(chan error, 1)
	go func() {
		_, cleanup, err := MaterializeCommit(repo, "HEAD")
		cleanup()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("MaterializeCommit succeeded for a rejected archive")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("MaterializeCommit did not stop the rejected archive process")
	}
}
