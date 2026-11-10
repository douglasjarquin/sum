// Package capture records evidence for one mapped feature while you drive it: the exact command or HTTP request,
// its observable result, and the time.
//
// Evidence lands under the run directory (`<artifacts>/<run-id>/evidence/NNN-<feature>.json`), beside `run.json`, so a
// teardown that removes what the run started never removes the proof. Nothing is started, stopped, or cleaned here;
// you drive the feature through its real entrypoint and this records what happened.
package capture

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/tools/verify/internal/contract"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
	"github.com/douglasjarquin/sum/tools/verify/internal/value"
)

const usage = `usage: sum-verify capture --feature ID [--scenario ID] [--run-dir DIR] [--note TEXT] run [--expect-exit N] [--expect-text T]... [--timeout SECONDS] -- COMMAND [ARGS...]
       sum-verify capture --feature ID [--scenario ID] [--run-dir DIR] [--note TEXT] http [--data JSON] [--expect-status N] [--expect-text T]... [--timeout SECONDS] METHOD URL

Capture evidence for one mapped feature while you drive it: the exact command or HTTP request, its observable result, and the time.

  --feature ID    feature or scenario ID from the feature maps
  --scenario ID   scenario ID when it differs from --feature
  --run-dir DIR   run directory to write into (default: the run directory named by <artifacts>/latest.json,
                  or a new <artifacts>/manual-<stamp>/ when no run record exists yet)
  --note TEXT     what this capture is meant to show

  run             run a command and record stdout, stderr, and the exit code
    --expect-exit N    exit code the command must return (default 0)
    --expect-text T    text that must appear in stdout
    --timeout SECONDS  how long the command may run (default 120)
  http            send one HTTP request and record status and body
    --data JSON        request body (sent as JSON)
    --expect-status N  status the response must have
    --expect-text T    text that must appear in the body
    --timeout SECONDS  how long the request may take (default 10)

Recipes are limited to a subprocess (CLI) and plain HTTP (service). A browser or desktop surface without an installed
driver stays a manual scenario; report it with the runner's --scenario instead of pretending this captured it.
Exit codes: 0 when the observed result met every expectation you stated, 1 otherwise, 2 on usage.
`

// limit is the most characters of an output or body a record keeps.
const limit = 20000

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// expectation is what one recipe run must show, as stated on the command line.
type expectation struct {
	texts   []string
	timeout int
}

func (e *expectation) register(fs *flag.FlagSet, timeout int) {
	fs.Func("expect-text", "", func(value string) error {
		e.texts = append(e.texts, value)
		return nil
	})
	fs.IntVar(&e.timeout, "timeout", timeout, "")
}

// lacking lists the expected texts the observed text does not contain.
func (e *expectation) lacking(where, observed string) []string {
	var unmet []string
	for _, text := range e.texts {
		if !strings.Contains(observed, text) {
			unmet = append(unmet, where+" lacks "+pycompat.Repr(text))
		}
	}
	return unmet
}

func (e *expectation) contains(where string) []string {
	stated := make([]string, len(e.texts))
	for i, text := range e.texts {
		stated[i] = where + " contains " + pycompat.Repr(text)
	}
	return stated
}

// Main runs the subcommand with its arguments and returns the process exit code.
func Main(args []string) int {
	var feature, scenario, runDir string
	var note *string
	fs := flag.NewFlagSet("sum-verify capture", flag.ContinueOnError)
	fs.StringVar(&feature, "feature", "", "")
	fs.StringVar(&scenario, "scenario", "", "")
	fs.StringVar(&runDir, "run-dir", "", "")
	fs.Func("note", "", func(value string) error {
		note = &value
		return nil
	})
	rest, exit, done := pycompat.ParseFlags(fs, args, usage, false)
	if done {
		return exit
	}
	if feature == "" {
		return pycompat.UsageError(fs, usage, "the following arguments are required: --feature")
	}
	if len(rest) == 0 {
		return pycompat.UsageError(fs, usage, "the following arguments are required: recipe")
	}
	if scenario == "" {
		scenario = feature
	}
	record := value.NewObject()
	record.Set("schema", 1)
	record.Set("feature", feature)
	record.Set("scenario", scenario)
	record.Set("note", note)
	record.Set("recipe", rest[0])

	var drive recipe
	switch rest[0] {
	case "run":
		drive, exit, done = commandRecipe(rest[1:])
	case "http":
		drive, exit, done = httpRecipe(rest[1:])
	default:
		return pycompat.UsageError(fs, usage, "argument recipe: invalid choice: %s (choose from 'run', 'http')", pycompat.Repr(rest[0]))
	}
	if done {
		return exit
	}

	root, err := contract.Root(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "not inside a Git work tree")
		return 1
	}
	evidenceDir := chooseRunDir(root, artifactsDir(root), runDir) + "/evidence"
	if err := os.MkdirAll(evidenceDir, 0o777); err != nil {
		fmt.Fprintf(os.Stderr, "sum-verify capture: %v\n", err)
		return 1
	}
	record.Set("started_at", tool.UTCNow())
	record.Set("cwd", pycompat.Resolve("."))
	record.Set("expectations", []string{})
	record.Set("unmet", []string{})
	started := time.Now()
	unmet := append([]string{}, drive(record)...)
	record.Set("unmet", unmet)
	record.Set("ended_at", tool.UTCNow())
	record.Set("seconds", tool.Since(started))
	record.Set("met", len(unmet) == 0)

	path := fmt.Sprintf("%s/%03d-%s.json", evidenceDir, countJSON(evidenceDir)+1, pycompat.Clip(unsafeName.ReplaceAllString(scenario, "-"), 60))
	data, err := tool.EncodeJSON(record)
	if err == nil {
		err = os.WriteFile(path, data, 0o666)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sum-verify capture: %v\n", err)
		return 1
	}
	shown, ok := pycompat.Rel(path, root)
	if !ok {
		shown = path
	}
	if len(unmet) > 0 {
		fmt.Printf("evidence: %s (UNMET: %s)\n", shown, strings.Join(unmet, "; "))
		return 1
	}
	fmt.Printf("evidence: %s (met)\n", shown)
	return 0
}

// artifactsDir is the contract's artifact directory, or the default one when VERIFY.md does not name it.
func artifactsDir(root string) string {
	artifacts := ".artifacts/verification"
	if data, err := os.ReadFile(root + "/" + contract.File); err == nil {
		config, _, _ := contract.Config(pycompat.UniversalNewlines(string(data)))
		if named, _ := config["artifacts"].(string); named != "" {
			artifacts = named
		}
	}
	return pycompat.Join(root, artifacts)
}

// chooseRunDir is the explicit directory, else the newest run record's directory, else a new manual one.
func chooseRunDir(root, artifacts, explicit string) string {
	if explicit != "" {
		return pycompat.Resolve(explicit)
	}
	if data, err := os.ReadFile(artifacts + "/latest.json"); err == nil {
		var latest struct {
			Artifacts struct {
				RunDir string `json:"run_dir"`
			} `json:"artifacts"`
		}
		if json.Unmarshal(data, &latest) == nil && latest.Artifacts.RunDir != "" {
			if runDir := pycompat.Join(root, latest.Artifacts.RunDir); pycompat.IsDir(runDir) {
				return pycompat.Resolve(runDir)
			}
		}
	}
	return artifacts + "/manual-" + tool.Stamp(3)
}

func countJSON(dir string) int {
	entries, _ := os.ReadDir(dir)
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			count++
		}
	}
	return count
}
