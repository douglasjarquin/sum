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
	command := exec.Command("git", "-C", repo, "archive", "--format=tar", revision)
	stdout, err := command.StdoutPipe()
	if err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("read contract snapshot: %w", err)
	}
	if err := command.Start(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("start contract snapshot: %w", err)
	}
	extractErr := extractArchive(stdout, root)
	waitErr := command.Wait()
	if extractErr != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("extract contract snapshot: %w", extractErr)
	}
	if waitErr != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("git archive %s: %w", revision, waitErr)
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
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			link := filepath.FromSlash(header.Linkname)
			resolved := filepath.Clean(filepath.Join(filepath.Dir(target), link))
			if filepath.IsAbs(link) || (resolved != root && !strings.HasPrefix(resolved, root+string(os.PathSeparator))) {
				return fmt.Errorf("archive link %q leaves the snapshot", header.Linkname)
			}
			if err := os.Symlink(link, target); err != nil {
				return err
			}
		}
	}
}
