package verifycontract

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

const (
	commitFileMaxBytes     = 256 * 1024
	commitSnapshotMaxSize  = 8 * 1024 * 1024
	commitSnapshotMaxFiles = 256
)

func readBounded(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing symlinked verification file %s", path)
	}
	if !info.Mode().IsRegular() || info.Size() > commitFileMaxBytes {
		return nil, fmt.Errorf("verification file %s exceeds %d bytes or is not regular", path, commitFileMaxBytes)
	}
	return os.ReadFile(path)
}

func MaterializeCommit(repo, revision string) (string, func(), error) {
	if _, err := exec.Command("git", "-C", repo, "rev-parse", "--verify", revision+"^{commit}").Output(); err != nil {
		return "", func() {}, fmt.Errorf("resolve contract base %s: %w", revision, err)
	}
	root, err := os.MkdirTemp(filepath.Dir(repo), ".sum-contract-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create contract snapshot: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	paths := []string{ContractFile, "mise.toml", ".mise.toml", ".mise/config.toml", "mise-tasks/verify", ".mise/tasks/verify", "mise-tasks/test", ".mise/tasks/test"}
	contract, found, err := commitFile(repo, revision, ContractFile)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	if found {
		config := map[string]any{}
		matches := fence.FindStringSubmatch(string(contract))
		if len(matches) > 1 {
			_ = toml.Unmarshal([]byte(matches[1]), &config)
		}
		if len(matches) > 1 {
			if maps, ok := config["feature_maps"].(string); ok && relativeInside(maps) {
				paths = append(paths, maps)
				index, indexFound, indexErr := commitFile(repo, revision, maps)
				if indexErr != nil {
					cleanup()
					return "", func() {}, indexErr
				}
				if indexFound {
					for _, match := range link.FindAllStringSubmatch(string(index), -1) {
						if strings.HasPrefix(match[1], "http://") || strings.HasPrefix(match[1], "https://") {
							continue
						}
						relative := filepath.ToSlash(filepath.Join(filepath.Dir(maps), match[1]))
						if relativeInside(relative) {
							paths = append(paths, relative)
						}
					}
				}
			}
			if owner, ok := config["task_owner"].(string); ok && relativeInside(owner) {
				if err := materializeDirectory(repo, revision, owner, root); err != nil {
					cleanup()
					return "", func() {}, err
				}
				if owner != "." {
					for _, relative := range []string{"mise.toml", ".mise.toml", ".mise/config.toml", "mise-tasks/verify", ".mise/tasks/verify", "mise-tasks/test", ".mise/tasks/test"} {
						paths = append(paths, filepath.ToSlash(filepath.Join(owner, relative)))
					}
				}
			}
		}
	}
	seen := map[string]bool{}
	totalBytes := 0
	for _, relative := range paths {
		if seen[relative] || !relativeInside(relative) {
			continue
		}
		if len(seen) >= commitSnapshotMaxFiles {
			cleanup()
			return "", func() {}, fmt.Errorf("contract snapshot references more than %d files", commitSnapshotMaxFiles)
		}
		seen[relative] = true
		data, found, err := commitFile(repo, revision, relative)
		if err != nil {
			cleanup()
			return "", func() {}, err
		}
		if !found {
			continue
		}
		totalBytes += len(data)
		if totalBytes > commitSnapshotMaxSize {
			cleanup()
			return "", func() {}, fmt.Errorf("contract snapshot exceeds %d bytes", commitSnapshotMaxSize)
		}
		target := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("create contract path: %w", err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("write contract path: %w", err)
		}
	}
	return root, cleanup, nil
}

func commitFile(repo, revision, relative string) ([]byte, bool, error) {
	listing, err := exec.Command("git", "-C", repo, "ls-tree", "-z", revision, "--", relative).Output()
	if err != nil {
		return nil, false, fmt.Errorf("inspect committed path %s: %w", relative, err)
	}
	entry := strings.TrimSuffix(string(listing), "\x00")
	if entry == "" {
		return nil, false, nil
	}
	tab := strings.IndexByte(entry, '\t')
	if tab < 0 {
		return nil, false, fmt.Errorf("inspect committed path %s: malformed Git tree entry", relative)
	}
	fields := strings.Fields(entry[:tab])
	if len(fields) < 2 || (fields[0] != "100644" && fields[0] != "100755") {
		return nil, false, nil
	}
	sizeOutput, err := exec.Command("git", "-C", repo, "cat-file", "-s", revision+":"+relative).Output()
	if err != nil {
		return nil, false, fmt.Errorf("size committed path %s: %w", relative, err)
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(sizeOutput)), 10, 64)
	if err != nil || size < 0 {
		return nil, false, fmt.Errorf("size committed path %s: malformed Git size", relative)
	}
	if size > commitFileMaxBytes {
		return nil, false, fmt.Errorf("committed path %s exceeds %d bytes", relative, commitFileMaxBytes)
	}
	data, err := exec.Command("git", "-C", repo, "show", revision+":"+relative).Output()
	if err != nil {
		return nil, false, fmt.Errorf("read committed path %s: %w", relative, err)
	}
	if int64(len(data)) > commitFileMaxBytes {
		return nil, false, fmt.Errorf("committed path %s exceeds %d bytes", relative, commitFileMaxBytes)
	}
	return data, true, nil
}

func materializeDirectory(repo, revision, relative, root string) error {
	if relative == "." {
		return nil
	}
	listing, err := exec.Command("git", "-C", repo, "ls-tree", "-d", "-z", revision, "--", relative).Output()
	if err != nil {
		return fmt.Errorf("inspect committed directory %s: %w", relative, err)
	}
	if strings.TrimSpace(strings.TrimSuffix(string(listing), "\x00")) == "" {
		return nil
	}
	return os.MkdirAll(filepath.Join(root, filepath.FromSlash(relative)), 0o755)
}
