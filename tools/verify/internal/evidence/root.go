package evidence

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/douglasjarquin/sum/tools/verify/internal/contract"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
)

// Roles are the two sides of a comparison, in the order they are read and rendered.
var Roles = []string{"before", "after"}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// git is a Git command's trimmed output. ok is false when Git could not start, which leaves the reason in stderr, or exited non-zero.
func git(cwd string, args ...string) (ok bool, stdout, stderr string) {
	stdout, stderr, err := contract.GitOutput(cwd, args...)
	var exited *exec.ExitError
	if err != nil && !errors.As(err, &exited) {
		return false, "", err.Error()
	}
	text := func(s string) string {
		return strings.TrimSpace(pycompat.UniversalNewlines(pycompat.DecodeUTF8([]byte(s))))
	}
	return err == nil, text(stdout), text(stderr)
}

// ProjectRoot is the top of the Git work tree the tool was started in.
func ProjectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	ok, out, stderr := git(cwd, "rev-parse", "--show-toplevel")
	if !ok {
		return "", blocked("not inside a Git work tree: " + pycompat.Clip(stderr, 200))
	}
	return pycompat.ResolveExistingPrefix(out), nil
}

// EvidenceRoot is `evidence` from VERIFY.md (default `.artifacts/evidence`), or VERIFY_EVIDENCE_ROOT for a location outside a disposable
// checkout. The second result names which of the two declared it.
func EvidenceRoot(root string) (dir, declaredBy string) {
	if override := contract.EvidenceRootOverride(); override != "" {
		return pycompat.ResolveExistingPrefix(override), "VERIFY_EVIDENCE_ROOT"
	}
	declared := ".artifacts/evidence"
	if data, err := os.ReadFile(filepath.Join(root, contract.File)); err == nil {
		// A block that does not parse declares nothing.
		table, _, _ := contract.Config(pycompat.UniversalNewlines(string(data)))
		if v, ok := table["evidence"].(string); ok && v != "" && !filepath.IsAbs(v) && !slices.Contains(strings.Split(v, "/"), "..") {
			declared = v
		}
	}
	return pycompat.ResolveExistingPrefix(filepath.Join(root, declared)), contract.File
}

// SafeName turns a run or scenario id into one path component.
func SafeName(name string) string {
	return pycompat.Clip(unsafeName.ReplaceAllString(name, "-"), 80)
}

func scenarioDir(rootDir, runID, scenario string) string {
	return filepath.Join(rootDir, SafeName(runID), SafeName(scenario))
}
