// Package publish puts before/after evidence into one pull request's marked block: validated media and text, receipts per content hash,
// scoped body replacement.
//
// Capture (package evidence), this publisher, and the PR body format stay separate. Nothing here starts a browser, converts video, or hosts
// media: the only upload path is the GitHub CLI's own `gh pr edit --attach` (GitHub CLI 2.99+), running under whoever invokes the tool. A
// comparison manifest is a claim about files; every file is re-read, hash-checked, and copied into an approved publish copy before it is
// offered to `gh`. Anything that cannot be proven leaves the local evidence and the previous PR body exactly as they were.
package publish

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/douglasjarquin/sum/tools/verify/internal/args"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
	"github.com/douglasjarquin/sum/tools/verify/internal/value"
)

const schema = 1

const help = `Publish before/after evidence into one pull request's marked block.

Usage:
  sum-verify publish capabilities [--gh PATH] [--json]
  sum-verify publish plan --run ID --repo OWNER/NAME --pr N --candidate SHA [--scenario ID]... [--base SHA] [--verification-run ID]...
                          [--captured-by TEXT] [--evidence-root DIR] [--publish-dir DIR] [--redact REGEX]... [--json]
  sum-verify publish block --plan PATH [--receipts PATH] [--final] [--attach-list]
  sum-verify publish publish --plan PATH --visibility public|private|internal [--receipts PATH] [--gh PATH] [--timeout SECONDS]
                             [--allow-head-mismatch] [--replace-foreign-block] [--dry-run] [--json]
Exit codes: 0 published, unchanged, or planned, 1 refused or failed, 2 deferred (no usable gh --attach), 4 uncertain (an edit may have landed).
`

// refused means nothing external was written; the reason names what could not be proven.
type refused string

func (r refused) Error() string { return string(r) }

var capabilitiesSpec = args.Spec{Options: []args.Option{{Name: "--gh", Values: 1}, {Name: "--json"}}}

var blockSpec = args.Spec{Options: []args.Option{
	{Name: "--plan", Values: 1, Required: true},
	{Name: "--receipts", Values: 1},
	{Name: "--final"},
	{Name: "--attach-list"},
}}

func cmdCapabilities(argv []string) (int, error) {
	opts, err := args.Parse(capabilitiesSpec, argv)
	if err != nil {
		return 0, err
	}
	info := newGh(opts.String("--gh"), 60).capabilities()
	if opts.Bool("--json") {
		if err := tool.WriteJSON(os.Stdout, info); err != nil {
			return 0, err
		}
	} else {
		lines := make([]string, 0, info.Len())
		for _, key := range info.Keys() {
			lines = append(lines, key+": "+value.Text(info.Get(key)))
		}
		fmt.Println(strings.Join(lines, "\n"))
	}
	if info.Get("attach") != true {
		return 2, nil
	}
	return 0, nil
}

// cmdBlock renders the marked block from a plan (and receipts) without touching GitHub.
func cmdBlock(argv []string) (int, error) {
	opts, err := args.Parse(blockSpec, argv)
	if err != nil {
		return 0, err
	}
	plan, err := loadJSON(opts.String("--plan"), "publish plan")
	if err != nil {
		return 0, err
	}
	receipts := value.NewObject()
	if path := opts.String("--receipts"); path != "" {
		if receipts, err = loadReceipts(path); err != nil {
			return 0, err
		}
	}
	destination := value.Obj(plan.Get("destination"))
	urlFor := func(sha string) string { return recordedURL(receipts, sha, destination) }
	if opts.Bool("--attach-list") {
		for _, r := range value.List(plan.Get("scenarios")) {
			row := value.Obj(r)
			if row.Get("publishable") != true {
				continue
			}
			for _, m := range value.List(row.Get("media")) {
				if entry := value.Obj(m); entry.Get("kind") != "text" && urlFor(value.Str(entry.Get("sha256"))) == "" {
					fmt.Println("./" + value.Str(entry.Get("publish_copy")))
				}
			}
		}
		return 0, nil
	}
	block, err := renderBlock(plan, urlFor, opts.Bool("--final"))
	if err != nil {
		return 0, err
	}
	fmt.Print(block)
	return 0, nil
}

// Main runs the subcommand with its arguments and returns the process exit code.
func Main(argv []string) int {
	commands := map[string]func([]string) (int, error){"capabilities": cmdCapabilities, "plan": cmdPlan, "block": cmdBlock, "publish": cmdPublish}
	var code int
	var err error
	switch {
	case len(argv) == 0:
		err = args.UsageError("the following arguments are required: command")
	case argv[0] == "-h" || argv[0] == "--help":
		err = args.ErrHelp
	case commands[argv[0]] == nil:
		err = args.UsageError("unknown command " + argv[0])
	default:
		code, err = commands[argv[0]](argv[1:])
	}
	var isRefused refused
	var timedOut ghTimeout
	var badArguments args.UsageError
	switch {
	case err == nil:
		return code
	case err == args.ErrHelp:
		fmt.Print(help)
		return 0
	case errors.As(err, &badArguments):
		fmt.Fprintf(os.Stderr, "sum-verify publish: error: %s\nSee `sum-verify publish --help`.\n", badArguments)
		return 2
	case errors.As(err, &isRefused):
		fmt.Fprintf(os.Stderr, "refused: %s\n", isRefused)
		return 1
	case errors.As(err, &timedOut):
		fmt.Fprintf(os.Stderr, "uncertain: %s\n", timedOut)
		return 4
	}
	fmt.Fprintf(os.Stderr, "sum-verify publish: %v\n", err)
	return 1
}
