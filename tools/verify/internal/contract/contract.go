// Package contract reads what the project-root VERIFY.md and its feature maps declare:
// the ```verify configuration block, the repository it belongs to, and the scenario rows of each map.
package contract

import (
	"bytes"
	"errors"
	"fmt"
	"os"
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

// EvidenceRootOverride is VERIFY_EVIDENCE_ROOT with a leading "~" expanded, or "" when it is not set. The evidence
// capture writes there and the runner reads there, so both take the override from here.
func EvidenceRootOverride() string {
	return pycompat.ExpandUser(os.Getenv("VERIFY_EVIDENCE_ROOT"))
}

// GitOutput runs a Git command in the repository at root and returns its streams as written. err is what running it returned.
func GitOutput(root string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

// Git runs a Git command in the repository at root. ok is false when Git could not run or exited non-zero.
func Git(root string, args ...string) (stdout, stderr string, ok bool) {
	stdout, stderr, err := GitOutput(root, args...)
	if err != nil && stderr == "" && stdout == "" {
		stderr = err.Error()
	}
	return stdout, stderr, err == nil
}

// MiseTasks is one `mise tasks ls --json` from root. It never runs a task.
type MiseTasks struct {
	Binary         string // "" when mise is not on PATH; nothing was run then.
	Stdout, Stderr []byte
	State          *os.ProcessState // nil when mise could not be started.
	Err            error            // What running it returned.
}

// ListMiseTasks asks mise which tasks it resolves from root.
func ListMiseTasks(root string) MiseTasks {
	binary, err := exec.LookPath("mise")
	if err != nil {
		return MiseTasks{Err: err}
	}
	cmd := exec.Command(binary, "tasks", "ls", "--json")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "MISE_QUIET=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	return MiseTasks{Binary: binary, Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), State: cmd.ProcessState, Err: err}
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
