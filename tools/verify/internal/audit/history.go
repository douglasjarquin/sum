package audit

import (
	"encoding/json"
	"os"
	"slices"
	"strings"

	"github.com/douglasjarquin/sum/tools/verify/internal/contract"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
)

var (
	codeSuffixes    = []string{".py", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".go", ".rs", ".rb", ".sh", ".html", ".css", ".in", ".toml", ".json", ".yaml", ".yml"}
	notCodePrefixes = []string{".artifacts/", ".agents/", ".claude/", ".cursor/", ".github/", "docs/"}
	notCodeNames    = []string{"VERIFY.md", "README.md", "AGENTS.md", "CLAUDE.md", ".gitignore", "LICENSE", "mise.toml", ".mise.toml", "package-lock.json"}
	policyPrefixes  = []string{"VERIFY.md", "mise.toml", ".mise.toml", "mise-tasks/", ".agents/skills/verify/", ".agents/skills/evidence/"}
)

// proof says whether the newest run record still proves the current contract and maps.
type proof struct {
	Proof string `json:"proof"`
	*latestRun
	Detail string `json:"detail"`
}

// latestRun is what the newest run record says about itself, copied as found.
type latestRun struct {
	RunID        any `json:"run_id"`
	Outcome      any `json:"outcome"`
	Candidate    any `json:"candidate"`
	NotExercised any `json:"not_exercised"`
	Record       any `json:"record"`
}

type changes struct {
	Base         string           `json:"base"`
	Files        []string         `json:"files"`
	Policy       []string         `json:"policy"`
	AffectedMaps *pycompat.Object `json:"affected_maps"`
	Unmapped     []string         `json:"unmapped"`
	Rationale    *string          `json:"rationale"`
}

// latestRunProof compares the newest run record under the artifact directory with the current contract and map hashes.
func latestRunProof(root, artifacts, contractSHA string, mapSHAs *pycompat.Object) *proof {
	data, err := os.ReadFile(pycompat.Join(root, artifacts) + "/latest.json")
	if err != nil {
		return &proof{Proof: "none", Detail: "no run record under the artifact directory; the maps have never been exercised here"}
	}
	var latest struct {
		RunID     any `json:"run_id"`
		Outcome   any `json:"outcome"`
		Candidate struct {
			SHA any `json:"sha"`
		} `json:"candidate"`
		NotExercised any `json:"not_exercised"`
		Contract     struct {
			SHA256 any `json:"sha256"`
		} `json:"contract"`
		FeatureMaps []struct {
			Path   string `json:"path"`
			SHA256 any    `json:"sha256"`
		} `json:"feature_maps"`
		Artifacts struct {
			RunDir any `json:"run_dir"`
		} `json:"artifacts"`
	}
	if json.Unmarshal(data, &latest) != nil {
		return &proof{Proof: "none", Detail: "latest.json is not valid JSON"}
	}
	recorded := map[string]any{}
	for _, featureMap := range latest.FeatureMaps {
		recorded[featureMap.Path] = featureMap.SHA256
	}
	current := latest.Contract.SHA256 == contractSHA && len(recorded) == len(mapSHAs.Keys())
	for _, path := range mapSHAs.Keys() {
		sha, _ := mapSHAs.Get(path)
		current = current && recorded[path] == sha
	}
	run := &latestRun{RunID: latest.RunID, Outcome: latest.Outcome, Candidate: latest.Candidate.SHA, NotExercised: latest.NotExercised, Record: latest.Artifacts.RunDir}
	if current {
		return &proof{Proof: "current", latestRun: run, Detail: "the newest run record was produced against the current contract and maps"}
	}
	return &proof{Proof: "stale", latestRun: run, Detail: "the contract or a map changed after the newest run record; that record does not prove the current maps"}
}

// compare matches the files changed since the base against the maps' references and records what a reviewer must re-read.
func (r *record) compare(root string, opts options, mapSHAs, references *pycompat.Object) error {
	changed, err := changedSince(root, opts.base)
	if err != nil {
		return err
	}
	found := &changes{Base: opts.base, Files: changed, Policy: []string{}, AffectedMaps: &pycompat.Object{}, Unmapped: []string{}}
	if opts.rationale != "" {
		found.Rationale = &opts.rationale
	}
	for _, change := range changed {
		if hasAnyPrefix(change, policyPrefixes) || hasAnyPrefix(change, mapSHAs.Keys()) {
			found.Policy = append(found.Policy, change)
		}
		owned := false
		for _, owner := range references.Keys() {
			paths, _ := references.Get(owner)
			if !slices.ContainsFunc(paths.([]string), func(path string) bool { return change == path || strings.HasPrefix(change, path+"/") }) {
				continue
			}
			owned = true
			files, _ := found.AffectedMaps.Get(owner)
			touched, _ := files.([]string)
			found.AffectedMaps.Set(owner, append(touched, change))
		}
		if !owned && isCode(change) {
			found.Unmapped = append(found.Unmapped, change)
		}
	}
	r.Changes = found
	if len(found.Policy) > 0 {
		r.Notes = append(r.Notes, note{Kind: "policy-changed", Detail: "verification policy files changed since base; the root's independent review must inspect them", Files: found.Policy})
	}
	for _, owner := range found.AffectedMaps.Keys() {
		files, _ := found.AffectedMaps.Get(owner)
		r.Notes = append(r.Notes, note{Kind: "affected-map", File: owner, Detail: "references changed files; re-read its entry points, variants, expected states, and coverage", Files: files.([]string)})
	}
	if opts.rationale != "" {
		if len(found.Unmapped) > 0 {
			r.Notes = append(r.Notes, note{Kind: "no-map-change", Detail: opts.rationale, Files: found.Unmapped})
		}
		return nil
	}
	for _, change := range found.Unmapped {
		r.add(finding{Kind: "unmapped-change", File: change, Detail: "changed since base but no feature map references it; update a map or record --rationale for a purely internal change"})
	}
	return nil
}

// changedSince lists the files that differ from the base commit, tracked or not yet tracked.
func changedSince(root, base string) ([]string, error) {
	if _, _, ok := contract.Git(root, "rev-parse", "--verify", "--quiet", base+"^{commit}"); !ok {
		return nil, contract.Blockedf("--base %s is not a commit in this repository", pycompat.Repr(base))
	}
	diff, stderr, ok := contract.Git(root, "diff", "--name-only", base)
	if !ok {
		return nil, contract.Blockedf("git diff --name-only %s failed: %s", base, pycompat.Strip(stderr))
	}
	untracked, stderr, ok := contract.Git(root, "ls-files", "--others", "--exclude-standard")
	if !ok {
		return nil, contract.Blockedf("git ls-files failed: %s", pycompat.Strip(stderr))
	}
	changed := []string{}
	for _, line := range pycompat.SplitLines(diff + untracked) {
		if pycompat.Strip(line) != "" {
			changed = append(changed, line)
		}
	}
	slices.Sort(changed)
	return slices.Compact(changed), nil
}

// isCode reports whether a changed file is source a feature map should account for, not documentation or tooling.
func isCode(path string) bool {
	name := path[strings.LastIndex(path, "/")+1:]
	if slices.Contains(notCodeNames, path) || hasAnyPrefix(path, notCodePrefixes) || strings.HasPrefix(name, ".") {
		return false
	}
	return hasAnySuffix(path, codeSuffixes)
}
