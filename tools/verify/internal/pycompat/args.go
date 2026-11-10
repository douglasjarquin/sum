package pycompat

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// ParseFlags handles a command line the way argparse did: `--help` prints the usage and exits 0, a bad flag prints it
// and exits 2. It returns the arguments that are not flags; done is true when the caller should return exit at once.
// With interspersed set, flags may come before, between, or after positional arguments; otherwise parsing stops at the first one.
func ParseFlags(fs *flag.FlagSet, args []string, usage string, interspersed bool) (rest []string, exit int, done bool) {
	fs.SetOutput(io.Discard)
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fmt.Print(usage)
				return nil, 0, true
			}
			return nil, UsageError(fs, usage, "%v", err), true
		}
		remaining := fs.Args()
		terminated := len(args) > len(remaining) && args[len(args)-len(remaining)-1] == "--"
		if !interspersed || terminated || len(remaining) == 0 {
			return append(rest, remaining...), 0, false
		}
		rest = append(rest, remaining[0])
		args = remaining[1:]
	}
}

// UsageError reports a command-line mistake and returns argparse's exit code for one.
func UsageError(fs *flag.FlagSet, usage, format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "%s%s: error: %s\n", usage, fs.Name(), fmt.Sprintf(format, args...))
	return 2
}
