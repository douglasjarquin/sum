// Package scaffold inspects a repository and scaffolds its project-local verification: VERIFY.md, the feature-map index and seed feature files,
// the vendored `verify`, `evidence`, and `maintain-verification` skills with thin harness aliases, and a `verify` mise task built from the
// checks the project already declares.
//
// Nothing here invents commands, selectors, ports, or credentials: every generated line names something the inspection found, and everything it
// could not find is written as an explicit `TODO(verify): ...` placeholder that the audit reports until a person resolves it. Generation is
// idempotent and never overwrites: an existing file without placeholders is the user's and is kept; an existing draft (still carrying
// placeholders) that the inspection would now generate differently is kept too, with the proposal written under the Git-ignored artifact
// directory and the conflict listed. A vendored skill file that differs from the shipped copy is a conflict as well. An explicitly declared
// `feature_maps` location in an existing VERIFY.md is preserved.
package scaffold

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
)

const usage = `usage: sum-verify scaffold [--root DIR] [--inspect] [--write] [--update] [--surface web|cli|service]... [--json]

  --root DIR   any directory inside the target repository (default: .)
  --inspect    report findings and the plan without writing (default)
  --write      create missing files; never overwrite existing ones
  --update     replace unchanged stock skill files with this binary's copies; never overwrite VERIFY.md or local edits
  --surface S  declare a surface the inspection cannot see: web, cli, or service (repeatable)
  --json       print the record as JSON

Exit codes: 0 ok, 1 conflicts or nothing verifiable found (the repository still needs a person), 2 not a repository, 3 usage.
`

var errHelp = errors.New("help")

type options struct {
	root                string
	write, update, json bool
	surfaces            []string
}

func parseArgs(args []string) (options, error) {
	opts := options{root: "."}
	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		takeValue := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", name)
			}
			i++
			return args[i], nil
		}
		var err error
		switch name {
		case "--root":
			opts.root, err = takeValue()
		case "--surface":
			var surface string
			if surface, err = takeValue(); err == nil && !slices.Contains([]string{"web", "cli", "service"}, surface) {
				err = fmt.Errorf("--surface must be web, cli, or service, not %q", surface)
			}
			opts.surfaces = append(opts.surfaces, surface)
		case "--inspect", "--write", "--update", "--json", "-h", "--help":
			if hasValue {
				return opts, fmt.Errorf("%s takes no value", name)
			}
			switch name {
			case "--write":
				opts.write = true
			case "--update":
				opts.update = true
			case "--json":
				opts.json = true
			case "-h", "--help":
				return opts, errHelp
			}
		default:
			err = fmt.Errorf("unrecognized argument %q", args[i])
		}
		if err != nil {
			return opts, err
		}
	}
	return opts, nil
}

func resolveRoot(start string) (string, error) {
	start = resolve(start)
	var stderr strings.Builder
	cmd := exec.Command("git", "-C", start, "rev-parse", "--show-toplevel")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := []rune(strings.TrimSpace(stderr.String()))
		return "", fmt.Errorf("%s is not inside a Git work tree: %s", start, string(detail[:min(200, len(detail))]))
	}
	return resolve(strings.TrimSpace(string(out))), nil
}

type record struct {
	Schema       int         `json:"schema"`
	At           string      `json:"at"`
	Mode         string      `json:"mode"`
	Inspection   inspection  `json:"inspection"`
	Plan         planned     `json:"plan"`
	PlannedFiles []string    `json:"planned_files"`
	ReadmeMap    []readmeRow `json:"readme_map"`
	Results      []result    `json:"results"`
	Conflicts    []result    `json:"conflicts"`
	Problems     []string    `json:"problems"`
	Next         []string    `json:"next"`
}

func scaffold(root string, opts options) (record, error) {
	info := inspect(root, opts.surfaces)
	p := plan(root, info)
	rec := record{Schema: 1, Mode: "inspect", Inspection: info, Plan: p, PlannedFiles: []string{}, ReadmeMap: readmeMap(root, info), Conflicts: []result{}, Problems: []string{}}
	var err error
	switch {
	case opts.update:
		rec.Mode = "update"
		rec.Results, err = applyUpdate(root)
	case opts.write:
		rec.Mode = "write"
		if rec.Results, err = apply(root, p, true); err == nil {
			err = writeProvenance(root, stockRecord(root))
		}
	default:
		rec.Results, err = apply(root, p, false)
	}
	if err != nil {
		return rec, err
	}
	for _, file := range p.files {
		rec.PlannedFiles = append(rec.PlannedFiles, file.path)
	}
	sort.Strings(rec.PlannedFiles)
	for _, r := range rec.Results {
		if r.Status == "conflict" {
			rec.Conflicts = append(rec.Conflicts, r)
		}
	}
	if len(p.Commands) == 0 && !info.HasVerifyTask {
		rec.Problems = append(rec.Problems, "no existing check was found (no test/lint/check task, package script, make target, or test directory); VERIFY.md names no runnable entrypoint until you declare one. "+
			"A `verify` task that merely succeeds is refused.")
	}
	if info.InheritedVerify && !info.HasVerifyTask {
		rec.Problems = append(rec.Problems, "a `verify` task is inherited from a parent directory; it belongs to another project and the runner will block on it until this repository defines its own.")
	}
	if info.VerifyCycle {
		rec.Problems = append(rec.Problems, "the `verify` task runs `mise run verify`; that cycle is refused. Point verify at the checks the repository already declares.")
	}
	if !info.Mise.Available {
		rec.Problems = append(rec.Problems, "mise is not on PATH; task ownership could not be checked.")
	}
	// Without a pin the wrapper has no binary to run in an ordinary clone, so pinning comes before everything else.
	if !exists(filepath.Join(root, filepath.FromSlash(lockPath))) {
		rec.Next = append(rec.Next, wrapper+" pin <version>")
	}
	rec.Next = append(rec.Next,
		wrapper+" run --check",
		"resolve every TODO(verify) placeholder from what you observed, not from guesses",
		wrapper+" audit",
		wrapper+" run --base <merge-base>",
		"drive one mapped feature and capture evidence with "+wrapper+" capture, then run the teardown and confirm the evidence is still there")
	rec.At = tool.UTCNow()
	return rec, nil
}

func printRecord(rec record) {
	info := rec.Inspection
	orNone := func(values []string, none string) string {
		if len(values) == 0 {
			return none
		}
		return strings.Join(values, ", ")
	}
	var own, capabilities []string
	for _, task := range info.Mise.Own {
		own = append(own, task.Name)
	}
	for _, name := range info.Capabilities.keys {
		installed := "no"
		if info.Capabilities.values[name] {
			installed = "yes"
		}
		capabilities = append(capabilities, name+"="+installed)
	}
	fmt.Printf("scaffold: %s at %s\n", rec.Mode, info.Root)
	fmt.Printf("surfaces: %s; own mise tasks: %s; checks reused: %s\n", orNone(info.Surfaces, "none detected"), orNone(own, "none"), orNone(rec.Plan.Commands, "none"))
	fmt.Println("capabilities: " + strings.Join(capabilities, ", "))
	for _, r := range rec.Results {
		detail := r.Note
		if detail == "" {
			detail = strings.Join(r.Differs, ", ")
		}
		if detail != "" {
			detail = "  (" + detail + ")"
		}
		fmt.Printf("  %14s  %s%s\n", r.Status, r.Path, detail)
	}
	for _, c := range rec.Conflicts {
		where := ""
		if c.Proposal != nil && *c.Proposal != "" {
			where = ", proposal at " + string(*c.Proposal)
		}
		fmt.Printf("conflict: %s differs from what would be generated; kept yours%s\n", c.Path, where)
	}
	for _, problem := range rec.Problems {
		fmt.Println("problem: " + problem)
	}
	fmt.Println("next: " + strings.Join(rec.Next, " ; "))
}

// Main runs the subcommand with its arguments and returns the process exit code.
func Main(args []string) int {
	opts, err := parseArgs(args)
	if errors.Is(err, errHelp) {
		fmt.Print(usage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sum-verify scaffold: %v\n%s", err, usage)
		return 3
	}
	root, err := resolveRoot(opts.root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	rec, err := scaffold(root, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sum-verify scaffold: %v\n", err)
		return 1
	}
	if opts.json {
		if err := tool.WriteJSON(os.Stdout, rec); err != nil {
			fmt.Fprintf(os.Stderr, "sum-verify scaffold: %v\n", err)
			return 1
		}
	} else {
		printRecord(rec)
	}
	if len(rec.Problems) > 0 {
		return 1
	}
	return 0
}
