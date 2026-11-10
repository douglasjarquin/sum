package scaffold

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/tools/verify/skills"
)

const harnessAliasDir = ".claude/skills"

// proposal is where a conflicting draft's regenerated content was written; it is null when nothing was written.
type proposal string

func (p proposal) MarshalJSON() ([]byte, error) {
	if p == "" {
		return []byte("null"), nil
	}
	return json.Marshal(string(p))
}

type result struct {
	Path     string    `json:"path"`
	Status   string    `json:"status"`
	Proposal *proposal `json:"proposal,omitempty"`
	Note     string    `json:"note,omitempty"`
	Differs  []string  `json:"differs,omitempty"`
}

func writeFile(target string, content []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, content, mode)
}

// apply creates what is missing and keeps what exists. Differing existing files are conflicts whose proposal is written under the artifact directory.
func apply(root string, p planned, write bool) ([]result, error) {
	results := []result{}
	proposals := path.Join(artifacts, "scaffold", time.Now().UTC().Format("20060102T150405Z"))
	for _, file := range p.files {
		target := filepath.Join(root, filepath.FromSlash(file.path))
		status, err := applyFile(root, target, file, proposals, write)
		if err != nil {
			return nil, err
		}
		results = append(results, status)
	}
	for _, name := range skills.Vendored {
		vendored, err := vendor(root, name, write)
		if err != nil {
			return nil, err
		}
		results = append(results, vendored...)
	}
	return results, nil
}

func applyFile(root, target string, file plannedFile, proposals string, write bool) (result, error) {
	if file.kind == "append" && isFile(target) {
		current := read(target)
		if current == file.content ||
			(file.path == "mise.toml" && definesVerifyTask(current)) ||
			(file.path == ".gitignore" && strings.Contains(current, ".artifacts/")) {
			return result{Path: file.path, Status: "unchanged"}, nil
		}
		if !write {
			return result{Path: file.path, Status: "would-append"}, nil
		}
		return result{Path: file.path, Status: "appended"}, os.WriteFile(target, []byte(file.content), 0o644)
	}
	if exists(target) {
		current := read(target)
		if current == file.content {
			return result{Path: file.path, Status: "unchanged"}, nil
		}
		if !strings.Contains(current, todo) {
			return result{Path: file.path, Status: "kept", Note: "exists without placeholders: yours, never regenerated"}, nil
		}
		var written proposal
		if write {
			written = proposal(path.Join(proposals, file.path))
			if err := writeFile(filepath.Join(root, filepath.FromSlash(string(written))), []byte(file.content), 0o644); err != nil {
				return result{}, err
			}
		}
		return result{Path: file.path, Status: "conflict", Proposal: &written,
			Note: "still a draft (has placeholders) but the inspection now generates it differently; yours is kept, the proposal is written beside the run records"}, nil
	}
	if !write {
		return result{Path: file.path, Status: "would-create"}, nil
	}
	return result{Path: file.path, Status: "created"}, writeFile(target, []byte(file.content), 0o644)
}

// vendor copies one embedded skill byte for byte and links its harness alias; a differing existing copy is kept and reported.
func vendor(root, name string, write bool) ([]result, error) {
	var differs []string
	missing := false
	for _, file := range stock(name) {
		dest := filepath.Join(root, filepath.FromSlash(file.path))
		current, err := os.ReadFile(dest)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			missing = true
			if write {
				if err := file.writeTo(dest); err != nil {
					return nil, err
				}
			}
		case err != nil || !bytes.Equal(current, file.data):
			differs = append(differs, file.path)
		}
	}
	skill := result{Path: ".agents/skills/" + name, Status: "unchanged"}
	switch {
	case differs != nil:
		skill.Status, skill.Differs = "conflict", differs
	case missing && write:
		skill.Status = "vendored"
	case missing:
		skill.Status = "would-vendor"
	}
	alias := result{Path: harnessAliasDir + "/" + name, Status: "unchanged"}
	link := filepath.Join(root, filepath.FromSlash(alias.Path))
	if _, err := os.Lstat(link); err != nil {
		alias.Status = "would-link"
		if write {
			alias.Status = "linked"
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				return nil, err
			}
			if err := os.Symlink(filepath.Join("..", "..", ".agents", "skills", name), link); err != nil {
				return nil, err
			}
		}
	}
	return []result{skill, alias}, nil
}
