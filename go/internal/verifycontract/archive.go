package verifycontract

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func MaterializeCommit(repo, revision string) (string, func(), error) {
	root, err := os.MkdirTemp(filepath.Dir(repo), ".sum-contract-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create contract snapshot: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	archive, err := os.CreateTemp(filepath.Dir(repo), ".sum-contract-archive-")
	if err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("create contract archive: %w", err)
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)
	command := exec.Command("git", "-C", repo, "archive", "--format=tar", revision)
	command.Stdout = archive
	if err := command.Run(); err != nil {
		_ = archive.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("git archive %s: %w", revision, err)
	}
	if err := archive.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("close contract archive: %w", err)
	}
	input, err := os.Open(archivePath)
	if err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("read contract archive: %w", err)
	}
	extractErr := extractArchive(input, root)
	closeErr := input.Close()
	if extractErr != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("extract contract snapshot: %w", extractErr)
	}
	if closeErr != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("close contract archive: %w", closeErr)
	}
	return root, cleanup, nil
}

func extractArchive(input io.Reader, root string) error {
	reader := tar.NewReader(input)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		relative := filepath.Clean(filepath.FromSlash(header.Name))
		if relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("archive path %q leaves the snapshot", header.Name)
		}
		target := filepath.Join(root, relative)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, reader)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			continue
		}
	}
}
