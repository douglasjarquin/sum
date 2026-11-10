package scaffold

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
	"github.com/douglasjarquin/sum/tools/verify/skills"
)

const (
	provenancePath = ".agents/skills/.verification-provenance.json"
	// lockPath is the project's pin. The wrapper writes it; nothing here reads past its existence or writes it.
	lockPath = ".agents/skills/verify/sum-verify.lock"
)

type stockFile struct {
	// path is where the file lives in a project, relative to its root.
	path string
	data []byte
	mode fs.FileMode
}

func (f stockFile) writeTo(dest string) error {
	if err := writeFile(dest, f.data, f.mode); err != nil {
		return err
	}
	return os.Chmod(dest, f.mode)
}

// stock lists one vendored skill's files as the running binary embeds them.
func stock(name string) []stockFile {
	var files []stockFile
	_ = fs.WalkDir(skills.Files, name, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := skills.Files.ReadFile(p)
		if err != nil {
			return err
		}
		// The embed drops file modes; the only executables are the extensionless commands under scripts/.
		mode := fs.FileMode(0o644)
		if path.Ext(p) == "" && slices.Contains(strings.Split(path.Dir(p), "/"), "scripts") {
			mode = 0o755
		}
		files = append(files, stockFile{path: ".agents/skills/" + p, data: data, mode: mode})
		return nil
	})
	return files
}

type provenanceFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type provenance struct {
	Schema      int              `json:"schema"`
	ToolVersion string           `json:"tool_version"`
	At          string           `json:"at"`
	Files       []provenanceFile `json:"files"`
}

func loadProvenance(root string) provenance {
	var recorded provenance
	if json.Unmarshal([]byte(read(filepath.Join(root, provenancePath))), &recorded) != nil {
		return provenance{}
	}
	return recorded
}

func writeProvenance(root string, files []provenanceFile) error {
	if files == nil {
		files = []provenanceFile{}
	}
	data, err := tool.EncodeJSON(provenance{Schema: 2, ToolVersion: tool.Version, At: tool.UTCNow(), Files: files})
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(root, provenancePath), data, 0o644)
}

// stockRecord lists the hashes to record after a write: a stock file that matches this binary's copy under its own hash, and every other
// recorded file under the hash it was last recorded with. A local edit is never recorded as stock, so a later update still sees it as an edit.
func stockRecord(root string) []provenanceFile {
	var files []provenanceFile
	current := map[string]bool{}
	for _, name := range skills.Vendored {
		for _, file := range stock(name) {
			if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.path))); err == nil && bytes.Equal(data, file.data) {
				files = append(files, provenanceFile{Path: file.path, SHA256: tool.SHA256(file.data)})
				current[file.path] = true
			}
		}
	}
	for _, row := range loadProvenance(root).Files {
		if row.Path != "" && row.SHA256 != "" && !current[row.Path] {
			files = append(files, row)
		}
	}
	return files
}

// applyUpdate replaces unchanged stock copies with the ones this binary embeds. Local edits and VERIFY.md stay.
func applyUpdate(root string) ([]result, error) {
	before := loadProvenance(root)
	previous := map[string]string{}
	var order []string
	for _, row := range before.Files {
		if row.Path == "" || row.SHA256 == "" {
			continue
		}
		if _, ok := previous[row.Path]; !ok {
			order = append(order, row.Path)
		}
		previous[row.Path] = row.SHA256
	}
	var files []stockFile
	for _, name := range skills.Vendored {
		files = append(files, stock(name)...)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })

	results := []result{}
	var recorded []provenanceFile
	changed := false
	isStock := map[string]bool{}
	for _, file := range files {
		isStock[file.path] = true
		dest := filepath.Join(root, filepath.FromSlash(file.path))
		stockHash := tool.SHA256(file.data)
		status := "vendored"
		if exists(dest) {
			current, err := os.ReadFile(dest)
			if err != nil {
				return nil, err
			}
			prior := previous[file.path]
			if bytes.Equal(current, file.data) {
				results = append(results, result{Path: file.path, Status: "unchanged"})
				recorded = append(recorded, provenanceFile{Path: file.path, SHA256: stockHash})
				continue
			}
			if tool.SHA256(current) != prior {
				results = append(results, result{Path: file.path, Status: "conflict", Note: "locally edited; kept"})
				if prior != "" {
					recorded = append(recorded, provenanceFile{Path: file.path, SHA256: prior})
				}
				continue
			}
			status = "updated"
		}
		if err := file.writeTo(dest); err != nil {
			return nil, err
		}
		changed = true
		results = append(results, result{Path: file.path, Status: status})
		recorded = append(recorded, provenanceFile{Path: file.path, SHA256: stockHash})
	}
	for _, rel := range order {
		dest := filepath.Join(root, filepath.FromSlash(rel))
		if isStock[rel] || !isFile(dest) {
			continue
		}
		current, err := os.ReadFile(dest)
		if err != nil {
			return nil, err
		}
		row := result{Path: rel, Status: "kept", Note: "project-authored or locally edited; not removed"}
		hash := tool.SHA256(current)
		if hash == previous[rel] {
			row = result{Path: rel, Status: "would-delete", Note: "unchanged stock absent from this tool version; not removed"}
		}
		results = append(results, row)
		recorded = append(recorded, provenanceFile{Path: rel, SHA256: hash})
	}
	// The record follows the binary: a version whose stock is byte-identical to the last one still becomes the recorded version.
	if changed || before.ToolVersion != tool.Version {
		return results, writeProvenance(root, recorded)
	}
	return results, nil
}
