package run

import (
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/douglasjarquin/sum/tools/verify/internal/contract"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
)

const (
	maxContractSnapshotFiles = 256
	maxContractSnapshotBytes = 8 * 1024 * 1024
)

var (
	snapshotBasePaths = []string{"VERIFY.md", "mise.toml", ".mise.toml", ".mise/config.toml", "mise-tasks/verify", ".mise/tasks/verify", "mise-tasks/test", ".mise/tasks/test"}
	visualProof       = regexp.MustCompile(`(?i)screenshot|screencast|red/green|before/after`)
)

type featureMap struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type scenario struct {
	ID               string `json:"id"`
	Map              string `json:"map"`
	Feature          string `json:"feature"`
	Driver           string `json:"driver"`
	DriverText       string `json:"driver_text"`
	Description      string `json:"description"`
	RequiresEvidence bool   `json:"requires_evidence"`
}

// loadFeatureMaps reads the index and every map it links. The index links every feature map; each map lists scenarios
// as table rows `| id | ... | automated|manual ... |`.
func loadFeatureMaps(root, indexRelative, ownerRelative string) ([]featureMap, []scenario, error) {
	index := pycompat.Join(root, indexRelative)
	if containsSymlink(index, root) {
		return nil, nil, contract.Blockedf("Feature-map index %s traverses a symlink.", indexRelative)
	}
	snapshot := map[string]bool{indexRelative: true}
	for _, relative := range snapshotBasePaths {
		snapshot[relative] = true
	}
	if ownerRelative != "." {
		for _, relative := range snapshotBasePaths[1:] {
			snapshot[pycompat.Join(ownerRelative, relative)] = true
		}
	}
	tooManyFiles := contract.Blockedf("verification contract snapshot references more than %d files", maxContractSnapshotFiles)
	tooManyBytes := contract.Blockedf("verification contract snapshot exceeds %d bytes", maxContractSnapshotBytes)
	if len(snapshot) > maxContractSnapshotFiles {
		return nil, nil, tooManyFiles
	}
	snapshotBytes, err := snapshotFileBytes(root, snapshot)
	if err != nil {
		return nil, nil, err
	}
	indexBytes, err := readBounded(index)
	if err != nil {
		return nil, nil, contract.Blockedf("Feature-map index %s is missing.", indexRelative)
	}
	indexText, err := decodeUTF8(indexBytes)
	if err != nil {
		return nil, nil, contract.Blockedf("Feature-map index %s is not valid UTF-8: %v", indexRelative, err)
	}

	maps := []featureMap{{Path: indexRelative, SHA256: tool.SHA256(indexBytes)}}
	scenarios := []scenario{}
	seen := map[string]bool{}
	for _, link := range contract.Links(indexText) {
		if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
			continue
		}
		if !safeRelativePath(link) {
			return nil, nil, contract.Blockedf("Feature map link %s in %s is not a safe repository path.", link, indexRelative)
		}
		rawTarget := pycompat.Join(index[:strings.LastIndex(index, "/")], link)
		if containsSymlink(rawTarget, root) {
			return nil, nil, contract.Blockedf("Feature map link %s in %s traverses a symlink.", link, indexRelative)
		}
		target := pycompat.Resolve(rawTarget)
		relative, ok := pycompat.Rel(target, root)
		if !ok {
			return nil, nil, contract.Blockedf("Feature map link %s in %s leaves the repository.", link, indexRelative)
		}
		mapBytes, err := readBounded(target)
		if err != nil {
			return nil, nil, contract.Blockedf("Feature map %s linked from %s is missing.", relative, indexRelative)
		}
		if !snapshot[relative] {
			if len(snapshot)+1 > maxContractSnapshotFiles {
				return nil, nil, tooManyFiles
			}
			if snapshotBytes+int64(len(mapBytes)) > maxContractSnapshotBytes {
				return nil, nil, tooManyBytes
			}
			snapshot[relative] = true
			snapshotBytes += int64(len(mapBytes))
		}
		maps = append(maps, featureMap{Path: relative, SHA256: tool.SHA256(mapBytes)})
		mapText, err := decodeUTF8(mapBytes)
		if err != nil {
			return nil, nil, contract.Blockedf("Feature map %s is not valid UTF-8: %v", relative, err)
		}
		for _, row := range contract.Rows(mapText) {
			automated, ok := contract.Automated(row.Driver)
			if !ok {
				return nil, nil, contract.Blockedf("Scenario %s in %s has driver %s; it must start with `automated` or `manual`.", row.ID, relative, pycompat.Repr(row.Driver))
			}
			if seen[row.ID] {
				return nil, nil, contract.Blockedf("Scenario id %s is defined twice across the feature maps.", row.ID)
			}
			seen[row.ID] = true
			driver := "manual"
			if automated {
				driver = "automated"
			}
			scenarios = append(scenarios, scenario{
				ID: row.ID, Map: relative, Feature: stem(relative), Driver: driver,
				DriverText: pycompat.Clip(row.Driver, 200), Description: pycompat.Clip(row.Description, 200),
				RequiresEvidence: namesVisualProof(row.Evidence),
			})
		}
	}
	return maps, scenarios, nil
}

// snapshotFileBytes totals the regular files among the contract's own paths, refusing any that is oversized.
func snapshotFileBytes(root string, paths map[string]bool) (int64, error) {
	var total int64
	for _, relative := range sortedKeys(paths) {
		info, err := os.Lstat(pycompat.Join(root, relative))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > maxVerificationFileBytes {
			return 0, contract.Blockedf("committed path %s exceeds %d bytes", relative, maxVerificationFileBytes)
		}
		total += info.Size()
		if total > maxContractSnapshotBytes {
			return 0, contract.Blockedf("verification contract snapshot exceeds %d bytes", maxContractSnapshotBytes)
		}
	}
	return total, nil
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// stem is the file name without its last extension.
func stem(path string) string {
	name := path[strings.LastIndex(path, "/")+1:]
	if dot := strings.LastIndex(name, "."); dot > 0 {
		return name[:dot]
	}
	return name
}

// namesVisualProof reports whether an Evidence cell names visual proof as a whole word; such a row needs a comparison manifest.
// No alternative can begin inside another's match at a word boundary, so checking the boundaries of each match found is exact.
func namesVisualProof(cell string) bool {
	for _, match := range visualProof.FindAllStringIndex(cell, -1) {
		if !pycompat.WordBefore(cell, match[0]) && !pycompat.WordAfter(cell, match[1]) {
			return true
		}
	}
	return false
}
