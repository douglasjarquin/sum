// Package audit checks a repository's verification contract and feature maps against the repository itself,
// without running anything.
//
// Findings (each fails the audit until resolved):
//
//	placeholder        a `TODO(verify)` left by the scaffold: the map or contract is still a draft
//	stale-task         `mise run NAME` mentioned in VERIFY.md or a map, but this repository defines no such task
//	stale-path         a backticked path in VERIFY.md, a map, ARCHITECTURE.md, or CODEOWNERS that no longer exists
//	unlinked-map       a Markdown file beside the index that the index does not link
//	missing-link       an index link to a map that does not exist
//	coverage-claim     an `automated` scenario row that names no test/command path, or names one that is missing or contains no test
//	duplicate-id       a scenario ID defined twice; bad-driver: a Driver cell that is neither `automated` nor `manual`
//	unmapped-change    (with --base) a changed source file no feature map references and no --rationale explains
//
// Notes (informational): features affected by the changes since --base, policy files changed since --base, and whether
// the newest run record under the artifact directory still matches the current contract and maps (`proof: current|stale|none`).
//
// The audit reads text and Git; it never edits a map, runs a check, or marks anything verified. Authored map changes
// and actual run results stay separate: the record it writes lists both, side by side.
package audit

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/douglasjarquin/sum/tools/verify/internal/contract"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
)

const usage = `usage: sum-verify audit [--root DIR] [--base REF] [--rationale TEXT] [--json] [--no-record]

Audit a repository's verification contract and feature maps against the repository itself, without running anything.

  --root DIR        any directory inside the repository (default .)
  --base REF        commit/ref; changed files since it are matched against the maps' references
  --rationale TEXT  why the changes since --base need no map change (recorded; turns unmapped-change findings into an acknowledged note)
  --json            print the full audit record instead of a summary
  --no-record       do not write the audit record under the artifact directory

Exit codes: 0 clean, 1 findings, 2 blocked (no repository or no VERIFY.md), 3 usage.
`

var (
	guidance            = []string{"ARCHITECTURE.md", "docs/ARCHITECTURE.md", "CODEOWNERS", ".github/CODEOWNERS"}
	inventoryIncomplete = regexp.MustCompile(`(?i)inventory:[` + pycompat.SpaceClass + `]*\*{0,2}incomplete`)
)

type options struct {
	root, base, rationale string
	json, noRecord        bool
}

type record struct {
	Schema        int       `json:"schema"`
	AuditID       string    `json:"audit_id"`
	At            string    `json:"at"`
	Outcome       string    `json:"outcome"`
	Findings      []finding `json:"findings"`
	Notes         []note    `json:"notes"`
	Authored      any       `json:"authored"`
	Runs          *proof    `json:"runs"`
	Changes       *changes  `json:"changes"`
	Root          string    `json:"root,omitempty"`
	BlockedReason string    `json:"blocked_reason,omitempty"`
	Record        string    `json:"record,omitempty"`
}

type finding struct {
	Kind   string `json:"kind"`
	File   string `json:"file"`
	Line   int    `json:"line,omitempty"`
	Detail string `json:"detail"`
}

type note struct {
	Kind  string `json:"kind"`
	Proof string `json:"proof,omitempty"`
	*latestRun
	File   string   `json:"file,omitempty"`
	Detail string   `json:"detail"`
	Files  []string `json:"files,omitempty"`
}

type authored struct {
	Contract            hashedFile       `json:"contract"`
	FeatureMaps         *pycompat.Object `json:"feature_maps"`
	Scenarios           []scenario       `json:"scenarios"`
	References          *pycompat.Object `json:"references"`
	InventoryIncomplete bool             `json:"inventory_incomplete"`
}

type hashedFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// document is one file the audit reads: where it is, what findings call it, and its text.
type document struct {
	path, relative, text, sha256 string
}

// dir is the directory relative references in the document resolve against.
func (d *document) dir() string {
	return d.path[:strings.LastIndex(d.path, "/")]
}

// Main runs the subcommand with its arguments and returns the process exit code.
func Main(args []string) int {
	var opts options
	fs := flag.NewFlagSet("sum-verify audit", flag.ContinueOnError)
	fs.StringVar(&opts.root, "root", ".", "")
	fs.StringVar(&opts.base, "base", "", "")
	fs.StringVar(&opts.rationale, "rationale", "", "")
	fs.BoolVar(&opts.json, "json", false, "")
	fs.BoolVar(&opts.noRecord, "no-record", false, "")
	rest, exit, done := pycompat.ParseFlags(fs, args, usage, false)
	if done {
		return exit
	}
	if len(rest) > 0 {
		return pycompat.UsageError(fs, usage, "unrecognized arguments: %s", strings.Join(rest, " "))
	}

	rec := &record{Schema: 1, AuditID: pycompat.Stamp(4), At: tool.UTCNow(), Findings: []finding{}, Notes: []note{}, Authored: struct{}{}}
	artifacts, err := rec.inspect(opts)
	var blocked *contract.Blocked
	switch {
	case errors.As(err, &blocked):
		rec.Outcome, rec.BlockedReason = "blocked", blocked.Reason
	case len(rec.Findings) > 0:
		rec.Outcome = "findings"
	default:
		rec.Outcome = "clean"
	}
	if rec.Outcome != "blocked" && !opts.noRecord {
		// The stored record does not name itself; the printed one says where it was stored.
		path := pycompat.Join(rec.Root, artifacts) + "/audit/" + rec.AuditID + ".json"
		if err := rec.write(path); err != nil {
			fmt.Fprintf(os.Stderr, "sum-verify audit: the audit record could not be written: %v\n", err)
			return 2
		}
		rec.Record = relativeTo(rec.Root, path)
	}
	if opts.json {
		if err := tool.WriteJSON(os.Stdout, rec); err != nil {
			fmt.Fprintf(os.Stderr, "sum-verify audit: %v\n", err)
			return 2
		}
	} else {
		rec.summarize()
	}
	return map[string]int{"clean": 0, "findings": 1, "blocked": 2}[rec.Outcome]
}

// inspect fills the record and returns the contract's artifact directory. Its only errors are *contract.Blocked.
func (r *record) inspect(opts options) (artifacts string, err error) {
	start := pycompat.Resolve(opts.root)
	root, err := contract.Root(start)
	if err != nil {
		return "", contract.Blockedf("%s is not inside a Git work tree", start)
	}
	r.Root = root
	if !pycompat.IsFile(root + "/" + contract.File) {
		return "", contract.Blockedf("%s is missing at the project root; run the create-verification skill first", contract.File)
	}
	terms, err := read(root, root+"/"+contract.File)
	if err != nil {
		return "", err
	}
	config, found, err := contract.Config(terms.text)
	if !found {
		return "", contract.Blockedf("%s has no ```verify block", contract.File)
	}
	if err != nil {
		return "", contract.Blockedf("%s ```verify block is not valid TOML: %v", contract.File, err)
	}
	mapsIndex := stringOr(config["feature_maps"], "docs/features/README.md")
	artifacts = stringOr(config["artifacts"], ".artifacts/verification")

	tasks, known := ownTasks(root)
	if known && !tasks["verify"] {
		r.add(finding{Kind: "stale-task", File: contract.File, Detail: "this repository defines no `verify` task; the entrypoint `mise run verify` does not exist"})
	}
	r.checkText(root, terms, tasks, known)

	index := pycompat.Join(root, mapsIndex)
	var maps []*document
	if !pycompat.IsFile(index) {
		r.add(finding{Kind: "missing-link", File: contract.File, Detail: fmt.Sprintf("feature-map index `%s` does not exist", mapsIndex)})
	} else if maps, err = r.linkedMaps(root, index, mapsIndex); err != nil {
		return "", err
	}
	seen := map[string]string{}
	scenarios := []scenario{}
	hashes, references := &pycompat.Object{}, &pycompat.Object{}
	for _, featureMap := range maps {
		r.checkText(root, featureMap, tasks, known)
		if pycompat.Resolve(featureMap.path) != pycompat.Resolve(index) {
			scenarios = append(scenarios, r.scenariosOf(root, featureMap, seen)...)
		}
		references.Set(featureMap.relative, referencesOf(root, featureMap))
		hashes.Set(featureMap.relative, featureMap.sha256)
	}
	for _, name := range guidance {
		if path := root + "/" + name; pycompat.IsFile(path) {
			advice, err := read(root, path)
			if err != nil {
				return "", err
			}
			r.checkPaths(root, advice)
		}
	}
	r.Authored = authored{
		Contract: hashedFile{Path: contract.File, SHA256: terms.sha256}, FeatureMaps: hashes, Scenarios: scenarios, References: references,
		InventoryIncomplete: len(maps) > 0 && inventoryIncomplete.MatchString(maps[0].text),
	}
	r.Runs = latestRunProof(root, artifacts, terms.sha256, hashes)
	r.Notes = append(r.Notes, note{Kind: "proof", Proof: r.Runs.Proof, latestRun: r.Runs.latestRun, Detail: r.Runs.Detail})
	if opts.base != "" {
		if err := r.compare(root, opts, hashes, references); err != nil {
			return "", err
		}
	}
	return artifacts, nil
}

// linkedMaps reads the index and every map it links, index first, reporting links to nothing and maps nothing links.
func (r *record) linkedMaps(root, index, mapsIndex string) ([]*document, error) {
	indexMap, err := read(root, index)
	if err != nil {
		return nil, err
	}
	maps := []*document{indexMap}
	linked := map[string]bool{}
	for _, link := range contract.Links(indexMap.text) {
		if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
			continue
		}
		target := pycompat.Resolve(pycompat.Join(indexMap.dir(), link))
		if !pycompat.IsFile(target) {
			r.add(finding{Kind: "missing-link", File: mapsIndex, Detail: fmt.Sprintf("links `%s`, which does not exist", link)})
			continue
		}
		featureMap, err := read(root, target)
		if err != nil {
			return nil, err
		}
		linked[target] = true
		maps = append(maps, featureMap)
	}
	entries, _ := os.ReadDir(indexMap.dir())
	for _, entry := range entries {
		sibling := indexMap.dir() + "/" + entry.Name()
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if resolved := pycompat.Resolve(sibling); resolved != pycompat.Resolve(index) && !linked[resolved] {
			r.add(finding{Kind: "unlinked-map", File: relativeTo(root, sibling), Detail: fmt.Sprintf("not linked from `%s`", mapsIndex)})
		}
	}
	return maps, nil
}

func (r *record) add(found finding) {
	r.Findings = append(r.Findings, found)
}

// read loads a file as text the way the checks expect it: valid UTF-8 with `\n` line endings.
func read(root, path string) (*document, error) {
	relative := relativeTo(root, path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, contract.Blockedf("%s is unreadable: %v", relative, err)
	}
	if !utf8.Valid(data) {
		return nil, contract.Blockedf("%s is not valid UTF-8", relative)
	}
	return &document{path: path, relative: relative, text: pycompat.UniversalNewlines(string(data)), sha256: tool.SHA256(data)}, nil
}

// relativeTo names a path by its place in the repository, or in full when it lies outside.
func relativeTo(root, path string) string {
	if relative, ok := pycompat.Rel(path, root); ok {
		return relative
	}
	return path
}

func stringOr(value any, fallback string) string {
	if text, _ := value.(string); text != "" {
		return text
	}
	return fallback
}

func (r *record) write(path string) error {
	data, err := tool.EncodeJSON(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(path[:strings.LastIndex(path, "/")], 0o777); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o666)
}

func (r *record) summarize() {
	line := "audit: " + r.Outcome
	if r.BlockedReason != "" {
		line += " (" + r.BlockedReason + ")"
	}
	fmt.Println(line)
	for _, found := range r.Findings {
		place := found.File
		if found.Line != 0 {
			place += fmt.Sprintf(":%d", found.Line)
		}
		fmt.Printf("  %16s  %s  %s\n", found.Kind, place, found.Detail)
	}
	for _, noted := range r.Notes {
		line := "  note " + noted.Kind
		if noted.File != "" {
			line += " " + noted.File
		}
		line += ": " + noted.Detail
		if len(noted.Files) > 0 {
			line += " [" + strings.Join(noted.Files, ", ") + "]"
		}
		fmt.Println(line)
	}
	if r.Record != "" {
		fmt.Println("record: " + r.Record)
	}
}

func dedupe(items []string) []string {
	var unique []string
	for _, item := range items {
		if !slices.Contains(unique, item) {
			unique = append(unique, item)
		}
	}
	return unique
}
