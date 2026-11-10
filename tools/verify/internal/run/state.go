package run

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/tools/verify/internal/contract"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
)

type freshness struct {
	Fresh  bool    `json:"fresh"`
	Reason *string `json:"reason"`
}

type policy struct {
	Checked bool     `json:"checked"`
	Base    *string  `json:"base"`
	Changed []string `json:"changed"`
}

type evidence struct {
	Dir            string                  `json:"dir"`
	Root           string                  `json:"root"`
	RootSource     string                  `json:"root_source"`
	Required       []string                `json:"required"`
	Present        map[string][]comparison `json:"present"`
	Missing        []string                `json:"missing"`
	MissingDetails []missingEvidence       `json:"missing_details"`
}

type comparison struct {
	Path        string `json:"path"`
	Verdict     any    `json:"verdict"`
	VisualProof any    `json:"visual_proof"`
}

type missingEvidence struct {
	Scenario string `json:"scenario"`
	Feature  string `json:"feature"`
	Map      string `json:"map"`
}

// freshnessState compares the newest declared build output with the newest declared input.
// It is nil when the contract declares no outputs.
func freshnessState(root string, inputs, outputs []string) (*freshness, error) {
	if len(outputs) == 0 {
		return nil, nil
	}
	out, haveOutput, err := newest(root, outputs)
	if err != nil {
		return nil, err
	}
	in, haveInput, err := newest(root, inputs)
	if err != nil {
		return nil, err
	}
	stale := ""
	switch {
	case !haveOutput:
		stale = fmt.Sprintf("build output(s) %s are missing", pycompat.Repr(outputs))
	case haveInput && in.After(out):
		stale = fmt.Sprintf("build output(s) %s are older than input(s) %s; the checked application is stale", pycompat.Repr(outputs), pycompat.Repr(inputs))
	default:
		return &freshness{Fresh: true}, nil
	}
	return &freshness{Reason: &stale}, nil
}

// newest is the latest modification time among the regular files at or under the paths. found is false when there are none.
func newest(root string, paths []string) (stamp time.Time, found bool, err error) {
	files, size := 0, int64(0)
	for _, relative := range paths {
		symlinked := contract.Blockedf("freshness path %s traverses a symlink", relative)
		path := pycompat.Join(root, relative)
		if containsSymlink(path, root) {
			return stamp, false, symlinked
		}
		var candidates []string
		switch {
		case pycompat.IsDir(path):
			// An unreadable directory is passed over, and a link to a file is kept so the check below refuses it.
			_ = filepath.WalkDir(path, func(entry string, d fs.DirEntry, err error) error {
				if err == nil && (d.Type().IsRegular() || (d.Type()&fs.ModeSymlink != 0 && pycompat.IsFile(entry))) {
					candidates = append(candidates, entry)
				}
				return nil
			})
		case pycompat.IsFile(path):
			candidates = []string{path}
		}
		for _, file := range candidates {
			if containsSymlink(file, root) {
				return stamp, false, symlinked
			}
			info, err := os.Lstat(file)
			if err != nil {
				return stamp, false, contract.Blockedf("freshness path %s is unreadable: %v", relative, err)
			}
			if !info.Mode().IsRegular() {
				continue
			}
			files++
			size += info.Size()
			if files > maxContractSnapshotFiles {
				return stamp, false, contract.Blockedf("freshness path %s contains more than %d files", relative, maxContractSnapshotFiles)
			}
			if size > maxContractSnapshotBytes {
				return stamp, false, contract.Blockedf("freshness path %s exceeds %d bytes", relative, maxContractSnapshotBytes)
			}
			if !found || info.ModTime().After(stamp) {
				stamp, found = info.ModTime(), true
			}
		}
	}
	return stamp, found, nil
}

// policyChange lists the policy files that differ between base and HEAD. Without a base nothing is compared.
func policyChange(root, base string, files []string) (*policy, error) {
	if base == "" {
		return &policy{Changed: []string{}}, nil
	}
	if _, _, ok := contract.Git(root, "rev-parse", "--verify", "--quiet", base+"^{commit}"); !ok {
		return nil, contract.Blockedf("--base %s is not a commit in this repository.", pycompat.Repr(base))
	}
	protected := sortedSet(files, policyFilesDefault, policyFilesAtRef(root, base))
	diff, err := git(root, append([]string{"diff", "--name-only", base + "...HEAD", "--"}, protected...)...)
	if err != nil {
		return nil, err
	}
	changed := []string{}
	for _, line := range pycompat.SplitLines(diff) {
		if pycompat.Strip(line) != "" {
			changed = append(changed, line)
		}
	}
	return &policy{Checked: true, Base: &base, Changed: changed}, nil
}

// policyFilesAtRef is the policy_files list of the contract committed at ref, or nothing when that contract is absent or malformed.
func policyFilesAtRef(root, ref string) []string {
	shown, _, ok := contract.Git(root, "show", ref+":"+contract.File)
	if !ok {
		return nil
	}
	config, _, err := contract.Config(shown)
	if err != nil {
		return nil
	}
	extra, err := boundedStringList(lookup(config, "policy_files", []any{}), contract.File+" `policy_files`", true)
	if err != nil {
		return nil
	}
	return extra
}

// evidenceRoot is where comparison manifests are read from. The evidence capture honours VERIFY_EVIDENCE_ROOT,
// so the runner reads the same override or it looks where nothing was written.
func evidenceRoot(root, evidenceRelative string) (dir, source string) {
	override := os.Getenv("VERIFY_EVIDENCE_ROOT")
	if override == "" {
		return pycompat.Join(root, evidenceRelative), "contract"
	}
	if home, err := os.UserHomeDir(); err == nil && (override == "~" || strings.HasPrefix(override, "~/")) {
		override = home + override[1:]
	}
	return pycompat.Resolve(override), "VERIFY_EVIDENCE_ROOT"
}

// evidenceState finds the comparison manifests the evidence skill wrote for this candidate SHA, and the mapped scenarios
// that name visual evidence but have none. Missing evidence is reported, never invented and never turned into a pass
// for that scenario.
func evidenceState(root, evidenceRelative, head string, scenarios []scenario) *evidence {
	dir, source := evidenceRoot(root, evidenceRelative)
	state := &evidence{
		Dir: evidenceRelative, Root: dir, RootSource: source,
		Required: []string{}, Present: map[string][]comparison{}, Missing: []string{}, MissingDetails: []missingEvidence{},
	}
	for _, scenario := range scenarios {
		if scenario.RequiresEvidence {
			state.Required = append(state.Required, scenario.ID)
		}
	}
	for _, manifest := range comparisonManifests(dir) {
		data, err := os.ReadFile(manifest)
		if err != nil {
			continue
		}
		var found struct {
			Scenario    string `json:"scenario"`
			Verdict     any    `json:"verdict"`
			VisualProof any    `json:"visual_proof"`
			After       struct {
				SHA string `json:"sha"`
			} `json:"after"`
			Candidate struct {
				SHA string `json:"sha"`
			} `json:"candidate"`
		}
		if json.Unmarshal(data, &found) != nil {
			continue
		}
		usable := found.Verdict != nil && found.Verdict != "mismatch" && found.Verdict != "capture-failed" &&
			found.After.SHA != "" && strings.HasPrefix(head, pycompat.Clip(found.After.SHA, 12)) &&
			strings.HasPrefix(head, pycompat.Clip(found.Candidate.SHA, 12))
		if !usable || !slices.Contains(state.Required, found.Scenario) {
			continue
		}
		path, ok := pycompat.Rel(manifest, root)
		if !ok {
			path = manifest
		}
		state.Present[found.Scenario] = append(state.Present[found.Scenario], comparison{Path: path, Verdict: found.Verdict, VisualProof: found.VisualProof})
	}
	for _, scenario := range scenarios {
		if scenario.RequiresEvidence && state.Present[scenario.ID] == nil {
			state.Missing = append(state.Missing, scenario.ID)
			state.MissingDetails = append(state.MissingDetails, missingEvidence{Scenario: scenario.ID, Feature: scenario.Feature, Map: scenario.Map})
		}
	}
	return state
}

// comparisonManifests lists `<dir>/<run>/<scenario>/comparison.json`.
func comparisonManifests(dir string) []string {
	var manifests []string
	for _, run := range subdirectories(dir) {
		for _, scenario := range subdirectories(run) {
			if manifest := scenario + "/comparison.json"; pycompat.Exists(manifest) {
				manifests = append(manifests, manifest)
			}
		}
	}
	return manifests
}

func subdirectories(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var found []string
	for _, entry := range entries {
		if path := dir + "/" + entry.Name(); pycompat.IsDir(path) {
			found = append(found, path)
		}
	}
	return found
}
