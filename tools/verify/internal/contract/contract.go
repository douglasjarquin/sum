// Package contract reads what the project-root VERIFY.md and its feature maps declare:
// the ```verify configuration block, the repository it belongs to, and the scenario rows of each map.
package contract

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"regexp"

	"github.com/BurntSushi/toml"

	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
)

// File is the contract's name at the project root.
const File = "VERIFY.md"

// Blocked says why no trustworthy verdict can be produced; the reason is recorded, never converted into a pass.
type Blocked struct{ Reason string }

func (b *Blocked) Error() string { return b.Reason }

func Blockedf(format string, args ...any) error {
	return &Blocked{Reason: fmt.Sprintf(format, args...)}
}

var fence = regexp.MustCompile("(?ms)^```verify[ \\t]*\\n(.*?)^```[ \\t]*$")

// Config returns the table in a document's ```verify block. found is false when the document has no block,
// and err is the TOML error when the block does not parse.
func Config(text string) (config map[string]any, found bool, err error) {
	match := fence.FindStringSubmatch(text)
	if match == nil {
		return nil, false, nil
	}
	config = map[string]any{}
	if _, err := toml.Decode(match[1], &config); err != nil {
		return nil, true, err
	}
	return config, true, nil
}

// Git runs a Git command in the repository at root. ok is false when Git could not run or exited non-zero.
func Git(root string, args ...string) (stdout, stderr string, ok bool) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if errOut.Len() == 0 && out.Len() == 0 {
			errOut.WriteString(err.Error())
		}
		return out.String(), errOut.String(), false
	}
	return out.String(), errOut.String(), true
}

// Root is the real Git top level of the work tree holding start. A linked worktree's `.git` is a file and a managed
// clone is nested under another repository; both resolve here. On failure the error text is what Git said.
func Root(start string) (string, error) {
	stdout, stderr, ok := Git(start, "rev-parse", "--show-toplevel")
	if !ok {
		return "", errors.New(pycompat.Strip(stderr))
	}
	return pycompat.Resolve(pycompat.Strip(stdout)), nil
}
