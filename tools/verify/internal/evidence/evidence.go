// Package evidence captures truthful before/after evidence for one mapped scenario: screenshots, short screencasts, CLI transcripts, or
// HTTP responses, each bound to the exact checkout it was taken from, then compares the two roles into one manifest.
//
// The browser recipe additionally needs Node 22+ (built-in WebSocket) and a Chromium-family binary; `capabilities` says what this machine
// supports, and a recipe without its driver is recorded `blocked` with diagnostics, never a pass and never a fabricated image. Nothing here
// starts, stops, or resets an application: you run the base and the candidate in their own checkouts and point each capture at the one you
// drove. Originals are never edited; every derived file says what it is.
package evidence

import (
	"errors"
	"fmt"
	"os"

	"github.com/douglasjarquin/sum/tools/verify/internal/evidence/args"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
)

const help = `Capture truthful before/after evidence for one mapped scenario and compare the two roles into one manifest.

Usage:
  sum-verify evidence capabilities [--json]
  sum-verify evidence capture --scenario ID --role before|after --kind bugfix|feature|visual|nonvisual [--feature ID] [--run ID] [--checkout DIR]
                              [--expect-sha SHA] [--build-id TEXT] [--note TEXT] [--redact REGEX]... [--intentional KEY=VALUE]... RECIPE ...
      RECIPE browser --url URL [--step ACTION=ARG]... [--viewport WxH] [--theme light|dark] [--locale TAG] [--timezone TZ]
                     [--expect-text T]... [--expect-selector SEL=TEXT]... [--observe SEL] [--no-video] [--convert] [--max-seconds N]
                     [--max-frames N] [--step-timeout N] [--side-effect METHOD URL [--side-effect-text T]...]
             steps: goto=URL click=SELECTOR type=SELECTOR=TEXT press=Enter|Tab|Escape wait-text=TEXT wait=MS
      RECIPE cli [--expect-exit N] [--expect-text T]... [--timeout N] -- COMMAND [ARGS...]
      RECIPE http [--data JSON] [--expect-status N] [--expect-text T]... [--timeout N] METHOD URL
  sum-verify evidence unavailable --scenario ID --role before --reason TEXT [--run ID] [--kind KIND]
  sum-verify evidence compare --scenario ID --run ID [--base SHA] [--candidate SHA] [--json]
  sum-verify evidence inspect PATH [--json]
  sum-verify evidence promote --run ID --to DIR [--json]
Exit codes: 0 expectations met / comparison shows what was asked, 1 an expectation was not met or the comparison does not prove the claim,
2 blocked (driver, build, or recorder problem; diagnostics retained), 3 usage.
`

// blocked says the capture or comparison cannot be trusted; the reason is recorded, never converted into a pass.
type blocked string

func (b blocked) Error() string { return string(b) }

// usage is a request the tool understood but cannot act on as given.
type usage string

func (u usage) Error() string { return string(u) }

var capabilitiesSpec = args.Spec{Options: []args.Option{{Name: "--json"}}}

func cmdCapabilities(argv []string) (int, error) {
	opts, err := args.Parse(capabilitiesSpec, argv)
	if err != nil {
		return 0, err
	}
	info := capabilities()
	if opts.Bool("--json") {
		return 0, tool.WriteJSON(os.Stdout, info)
	}
	fmt.Println(capabilitiesText(info))
	return 0, nil
}

// Main runs the subcommand with its arguments and returns the process exit code.
func Main(argv []string) int {
	commands := map[string]func([]string) (int, error){
		"capabilities": cmdCapabilities, "capture": cmdCapture, "unavailable": cmdUnavailable,
		"compare": cmdCompare, "inspect": cmdInspect, "promote": cmdPromote,
	}
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
	var isBlocked blocked
	var isUsage usage
	var badArguments args.UsageError
	switch {
	case err == nil:
		return code
	case err == args.ErrHelp:
		fmt.Print(help)
		return 0
	case errors.As(err, &badArguments):
		fmt.Fprintf(os.Stderr, "sum-verify evidence: error: %s\nSee `sum-verify evidence --help`.\n", badArguments)
		return 2
	case errors.As(err, &isBlocked):
		fmt.Fprintf(os.Stderr, "blocked: %s\n", isBlocked)
		return 2
	case errors.As(err, &isUsage):
		fmt.Fprintln(os.Stderr, isUsage)
		return 3
	}
	fmt.Fprintf(os.Stderr, "sum-verify evidence: %v\n", err)
	return 1
}
