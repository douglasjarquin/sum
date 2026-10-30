// Package taskcheckout owns Git checkout isolation independently of the terminal runtime.
package taskcheckout

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/proc"
)

var taskPattern = regexp.MustCompile(`^t-[a-f0-9]+$`)
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)

// Intent must be persisted in the task before Create is called.
// Its path stays outside writable ledger generations so migration never copies a checkout.
type Intent struct {
	Repository string `json:"repository"`
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	BaseSHA    string `json:"base_sha"`
}
type Evidence struct {
	Path      string `json:"path"`
	GitRoot   string `json:"git_root"`
	CommonDir string `json:"git_common_dir"`
	Branch    string `json:"branch"`
	Head      string `json:"head"`
}

func Plan(home, repository, taskID, base string) (Intent, error) {
	if !taskPattern.MatchString(taskID) || !commitPattern.MatchString(base) {
		return Intent{}, fmt.Errorf("task checkout requires an exact task ID and base commit")
	}
	root, err := futurePath(home)
	if err != nil {
		return Intent{}, err
	}
	repo, err := filepath.EvalSymlinks(repository)
	if err != nil {
		return Intent{}, err
	}
	repo, err = filepath.Abs(repo)
	if err != nil {
		return Intent{}, err
	}
	intent := Intent{Repository: repo, Path: filepath.Join(root, "checkouts", taskID), Branch: "sum/" + taskID, BaseSHA: base}
	if err = validate(intent); err != nil {
		return Intent{}, err
	}
	return intent, nil
}
func validate(i Intent) error {
	if !filepath.IsAbs(i.Repository) || !filepath.IsAbs(i.Path) || filepath.Clean(i.Path) != i.Path || i.Path == i.Repository || !commitPattern.MatchString(i.BaseSHA) || !strings.HasPrefix(i.Branch, "sum/") || !taskPattern.MatchString(strings.TrimPrefix(i.Branch, "sum/")) || filepath.Base(i.Path) != strings.TrimPrefix(i.Branch, "sum/") {
		return fmt.Errorf("invalid persisted checkout intent")
	}
	// Existing path components must be real directories; never follow an injected link.
	for p := i.Path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("checkout path has a non-directory or symlink component: %s", p)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
func run(ctx context.Context, repo string, args ...string) (string, error) {
	out, err := proc.RunContext(ctx, proc.Cmd{Argv: append([]string{"git", "-C", repo}, args...), Timeout: 2 * time.Minute, Env: gitEnv()})
	if err != nil {
		return "", err
	}
	if out.Code != 0 {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), out.Detail())
	}
	return strings.TrimSpace(out.Stdout), nil
}
func registered(ctx context.Context, i Intent) (bool, error) {
	data, err := run(ctx, i.Repository, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return false, err
	}
	for _, field := range strings.Split(data, "\x00") {
		if strings.HasPrefix(field, "worktree ") {
			path := strings.TrimPrefix(field, "worktree ")
			if filepath.Clean(path) == i.Path {
				return true, nil
			}
			if resolved, e := filepath.EvalSymlinks(path); e == nil && resolved == i.Path {
				return true, nil
			}
		}
	}
	return false, nil
}

// Create reconciles Git registration before acting. It never resets, forces,
// prunes, removes or automatically substitutes a branch or checkout.
func Create(ctx context.Context, i Intent) (Evidence, error) {
	if err := validate(i); err != nil {
		return Evidence{}, err
	}
	exists, err := registered(ctx, i)
	if err != nil {
		return Evidence{}, err
	}
	if exists {
		return Verify(ctx, i, true)
	}
	if _, err = os.Lstat(i.Path); err == nil {
		return Evidence{}, fmt.Errorf("unregistered checkout path exists; preserve and inspect %s", i.Path)
	} else if !os.IsNotExist(err) {
		return Evidence{}, err
	}
	if err = os.MkdirAll(filepath.Dir(i.Path), 0700); err != nil {
		return Evidence{}, err
	}
	if err = validate(i); err != nil {
		return Evidence{}, err
	}
	if _, err = run(ctx, i.Repository, "worktree", "add", "-b", i.Branch, i.Path, i.BaseSHA); err != nil {
		return Evidence{}, fmt.Errorf("checkout creation failed or is uncertain; reconcile saved intent before retry: %w", err)
	}
	return Verify(ctx, i, true)
}
func canonicalGitPath(cwd, value string) (string, error) {
	if !filepath.IsAbs(value) {
		value = filepath.Join(cwd, value)
	}
	return filepath.EvalSymlinks(filepath.Clean(value))
}

// Verify requires initial HEAD equality only while reconciling creation.
// Later recovery permits legitimate commits while preserving repository/branch ownership.
func Verify(ctx context.Context, i Intent, initial bool) (Evidence, error) {
	if err := validate(i); err != nil {
		return Evidence{}, err
	}
	registered, err := registered(ctx, i)
	if err != nil {
		return Evidence{}, err
	}
	if !registered {
		return Evidence{}, fmt.Errorf("checkout is not registered with its repository")
	}
	root, err := run(ctx, i.Path, "rev-parse", "--show-toplevel")
	if err != nil {
		return Evidence{}, err
	}
	root, err = canonicalGitPath(i.Path, root)
	if err != nil {
		return Evidence{}, err
	}
	head, err := run(ctx, i.Path, "rev-parse", "HEAD")
	if err != nil {
		return Evidence{}, err
	}
	branch, err := run(ctx, i.Path, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return Evidence{}, err
	}
	common, err := run(ctx, i.Path, "rev-parse", "--git-common-dir")
	if err != nil {
		return Evidence{}, err
	}
	common, err = canonicalGitPath(i.Path, common)
	if err != nil {
		return Evidence{}, err
	}
	ownerCommon, err := run(ctx, i.Repository, "rev-parse", "--git-common-dir")
	if err != nil {
		return Evidence{}, err
	}
	ownerCommon, err = canonicalGitPath(i.Repository, ownerCommon)
	if err != nil {
		return Evidence{}, err
	}
	if root != i.Path || common != ownerCommon || branch != i.Branch || (initial && head != i.BaseSHA) {
		return Evidence{}, fmt.Errorf("registered checkout identity differs from saved intent; preserve and inspect %s", i.Path)
	}
	return Evidence{Path: i.Path, GitRoot: root, CommonDir: common, Branch: branch, Head: head}, nil
}

// Resolve an existing ancestor before sealing intent (macOS /var and /tmp are
// system symlinks). After sealing, validate rejects any path substitution.
func futurePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var tail []string
	ancestor := absolute
	for {
		_, err := os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		tail = append(tail, filepath.Base(ancestor))
		ancestor = filepath.Dir(ancestor)
	}
	root, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", err
	}
	for n := len(tail) - 1; n >= 0; n-- {
		root = filepath.Join(root, tail[n])
	}
	return root, nil
}

func gitEnv() []string {
	var env []string
	for _, entry := range proc.ScrubbedEnv() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "GIT_") {
			env = append(env, entry)
		}
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
}
