package evidence

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/douglasjarquin/sum/tools/verify/internal/evidence/proc"
)

// Roles are the two sides of a comparison, in the order they are read and rendered.
var Roles = []string{"before", "after"}

var (
	contractFence = regexp.MustCompile("(?ms)^```verify[ \\t]*\\n(.*?)^```[ \\t]*$")
	unsafeName    = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

// ExpandUser replaces a leading "~" with the home directory.
func ExpandUser(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return home + path[1:]
}

// ResolvePath is the absolute path with symlinks resolved as far as the path exists; whatever does not exist yet is kept as written.
func ResolvePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	rest := ""
	for current := abs; ; {
		if real, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return abs
		}
		current, rest = parent, filepath.Join(filepath.Base(current), rest)
	}
}

func git(cwd string, args ...string) (code int, stdout, stderr string) {
	result, err := proc.Run(append([]string{"git", "-C", cwd}, args...), proc.Options{})
	if err != nil {
		return 1, "", err.Error()
	}
	return result.Code, strings.TrimSpace(result.Stdout), strings.TrimSpace(result.Stderr)
}

// ProjectRoot is the top of the Git work tree the tool was started in.
func ProjectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	code, out, stderr := git(cwd, "rev-parse", "--show-toplevel")
	if code != 0 {
		return "", blocked("not inside a Git work tree: " + head(stderr, 200))
	}
	return ResolvePath(out), nil
}

// EvidenceRoot is `evidence` from VERIFY.md (default `.artifacts/evidence`), or VERIFY_EVIDENCE_ROOT for a location outside a disposable
// checkout. The second result names which of the two declared it.
func EvidenceRoot(root string) (dir, declaredBy string) {
	if override := os.Getenv("VERIFY_EVIDENCE_ROOT"); override != "" {
		return ResolvePath(ExpandUser(override)), "VERIFY_EVIDENCE_ROOT"
	}
	declared := ".artifacts/evidence"
	if contract, err := os.ReadFile(filepath.Join(root, "VERIFY.md")); err == nil {
		text := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(string(contract))
		if match := contractFence.FindStringSubmatch(text); match != nil {
			var table map[string]any
			if _, err := toml.Decode(match[1], &table); err == nil {
				if v, ok := table["evidence"].(string); ok && v != "" && !filepath.IsAbs(v) && !slices.Contains(strings.Split(v, "/"), "..") {
					declared = v
				}
			}
		}
	}
	return ResolvePath(filepath.Join(root, declared)), "VERIFY.md"
}

func newRunID() string {
	token := make([]byte, 4)
	_, _ = rand.Read(token)
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(token)
}

// SafeName turns a run or scenario id into one path component.
func SafeName(name string) string {
	return head(unsafeName.ReplaceAllString(name, "-"), 80)
}

func scenarioDir(rootDir, runID, scenario string) string {
	return filepath.Join(rootDir, SafeName(runID), SafeName(scenario))
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
