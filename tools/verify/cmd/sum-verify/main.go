// sum-verify is the portable verification and evidence tool that Sum's verification skills drive.
package main

import (
	"fmt"
	"os"

	"github.com/douglasjarquin/sum/tools/verify/internal/audit"
	"github.com/douglasjarquin/sum/tools/verify/internal/capture"
	"github.com/douglasjarquin/sum/tools/verify/internal/evidence"
	"github.com/douglasjarquin/sum/tools/verify/internal/publish"
	"github.com/douglasjarquin/sum/tools/verify/internal/run"
	"github.com/douglasjarquin/sum/tools/verify/internal/scaffold"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
)

const usage = `usage: sum-verify <command> [arguments]

  run        run the canonical entrypoint and write the run record
  capture    capture one CLI or HTTP observation for a feature
  audit      audit the contract and feature maps for stale claims
  scaffold   scaffold VERIFY.md, feature maps, and the vendored skills
  evidence   capture and compare before/after evidence
  publish    publish evidence to a pull request
  version    print the tool version

Each command prints its own flags with --help.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(3)
	}
	commands := map[string]func([]string) int{
		"run": run.Main, "capture": capture.Main, "audit": audit.Main,
		"scaffold": scaffold.Main, "evidence": evidence.Main, "publish": publish.Main,
	}
	name, args := os.Args[1], os.Args[2:]
	switch name {
	case "version", "--version":
		fmt.Println(tool.Version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		command, ok := commands[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "sum-verify: unknown command %q\n%s", name, usage)
			os.Exit(3)
		}
		os.Exit(command(args))
	}
}
