package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

const (
	lintGate         = "lint"
	lintUnobserved   = "Not observed; run `pipeline lint`"
	defaultLintBound = 20 * time.Minute
)

var lintOutcomes = map[string]Status{
	"pass":             Pass,
	"fail":             Fail,
	"timeout":          Blocked,
	outcomeUnavailable: Blocked,
	"not-declared":     NotDeclared,
}

var (
	verifyFence = regexp.MustCompile("(?s)```verify[ \t]*\n(.*?)\n```")
	makeLint    = regexp.MustCompile(`(?m)^lint[ \t]*:(?:[^=]|$)`)
)

// lintCommand is the project's own lint, discovered from what the project declares. sum never supplies one:
// a repository that declares no lint task has nothing for this gate to run, which is not a failure.
type lintCommand struct {
	Declared string
	Display  string
	Argv     []string
}

type LintArgs struct {
	Task      string
	Candidate string
}

// Lint runs the project's lint in a throwaway detached checkout of the candidate, the same isolation root verification uses.
func Lint(s *store.Store, ctx *ordjson.Object, runtimeRoot string, args LintArgs) (*ordjson.Object, error) {
	task, candidate, err := gateTask(s, ctx, args.Task, args.Candidate)
	if err != nil {
		return nil, err
	}
	worktree := stringField(task, "worktree")
	if info, statErr := os.Stat(worktree); worktree == "" || statErr != nil || !info.IsDir() {
		return nil, fmt.Errorf("The task checkout is gone; nothing can be linted against the candidate from here.")
	}
	dir, err := runDir(s, args.Task, lintGate)
	if err != nil {
		return nil, err
	}
	checkout := filepath.Join(dir, "checkout")
	if out, addErr := git(worktree, "worktree", "add", "--detach", checkout, candidate); addErr != nil {
		return nil, fmt.Errorf("could not check out %s to lint it: %s", candidate, out)
	}
	defer removeLintCheckout(worktree, checkout)

	body, summary := RunLint(runtimeRoot, checkout, dir)
	return recordGate(s, ctx, args.Task, lintGate, candidate, body, summary)
}

// Bootstrap writes into the throwaway tree (node_modules, lockfile installs, markers), so remove must force.
func removeLintCheckout(worktree, checkout string) {
	if out, err := git(worktree, "worktree", "remove", "--force", checkout); err != nil {
		note := fmt.Sprintf("git worktree remove --force failed: %s\nThe lint checkout was left in place; inspect it, then remove it with `git worktree remove --force`.\n", out)
		_ = os.WriteFile(filepath.Join(filepath.Dir(checkout), "checkout-not-removed.txt"), []byte(note), 0o600)
	}
}

// RunLint is shared with `verify --execute`, which runs the same step in the checkout it already made, so one
// command yields both the Test and the Lint row.
func RunLint(runtimeRoot, checkout, dir string) (*ordjson.Object, string) {
	body := ordjson.NewObject()
	command, declared := discoverLint(runtimeRoot, checkout)
	body.Set("command", nilIfEmpty(command.Display))
	body.Set("declared", nilIfEmpty(command.Declared))
	clearBootstrap(body)
	if !declared {
		body.Set("outcome", "not-declared")
		body.Set("exit", nil)
		body.Set("seconds", nil)
		body.Set("log", nil)
		return body, "This project declares no lint task"
	}
	if len(command.Argv) == 0 {
		body.Set("outcome", outcomeUnavailable)
		body.Set("exit", nil)
		body.Set("seconds", nil)
		body.Set("log", nil)
		return body, fmt.Sprintf("This project declares a lint task in %s, but the runner it needs is not available here", command.Declared)
	}

	bound := contractTimeout(checkout)
	runCtx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()

	if summary, failed := runBootstrap(runCtx, body, runtimeRoot, checkout, dir, bound); failed {
		body.Set("exit", nil)
		body.Set("seconds", nil)
		body.Set("log", nil)
		return body, summary
	}

	result := runDeclared(runCtx, command, checkout, filepath.Join(dir, "lint.log"))
	body.Set("seconds", jsonNumber(result.seconds))
	body.Set("log", nilIfEmpty(result.log))
	if result.timedOut {
		body.Set("outcome", "timeout")
		body.Set("exit", nil)
		return body, fmt.Sprintf("Did not finish within %ds (`%s`)", int(bound/time.Second), command.Display)
	}
	if result.unavailable {
		body.Set("outcome", outcomeUnavailable)
		body.Set("exit", nil)
		return body, fmt.Sprintf("Could not run `%s`: %s", command.Display, result.runErr)
	}
	body.Set("exit", jsonNumber(result.exit))
	if result.exit == 0 {
		body.Set("outcome", "pass")
		return body, fmt.Sprintf("Passed (`%s`)", command.Display)
	}
	body.Set("outcome", "fail")
	return body, fmt.Sprintf("Failed (`%s`): %s", command.Display, result.detail())
}

type commandResult struct {
	exit        int
	seconds     int
	log         string
	stdout      string
	output      string
	runErr      error
	timedOut    bool
	unavailable bool
}

func (r commandResult) detail() string {
	text := lastLineOf(r.stdout)
	if text == "" {
		text = lastLineOf(r.output)
	}
	if text == "" {
		return fmt.Sprintf("exit %d with no output", r.exit)
	}
	return text
}

func clearBootstrap(body *ordjson.Object) {
	body.Set("bootstrap_command", nil)
	body.Set("bootstrap_exit", nil)
	body.Set("bootstrap_seconds", nil)
	body.Set("bootstrap_log", nil)
}

// runBootstrap installs project-declared dependencies inside the detached checkout before lint.
// A failed, missing, or timed-out bootstrap is the lint outcome; lint itself does not run.
func runBootstrap(runCtx context.Context, body *ordjson.Object, runtimeRoot, checkout, dir string, bound time.Duration) (string, bool) {
	command, declared := discoverBootstrap(runtimeRoot, checkout)
	if !declared {
		return "", false
	}
	body.Set("bootstrap_command", nilIfEmpty(command.Display))
	if len(command.Argv) == 0 {
		body.Set("outcome", outcomeUnavailable)
		return fmt.Sprintf("This project declares a bootstrap in %s, but the runner it needs is not available here", command.Declared), true
	}
	result := runDeclared(runCtx, command, checkout, filepath.Join(dir, "bootstrap.log"))
	body.Set("bootstrap_seconds", jsonNumber(result.seconds))
	body.Set("bootstrap_log", nilIfEmpty(result.log))
	if result.timedOut {
		body.Set("outcome", "timeout")
		return fmt.Sprintf("Did not finish within %ds (`%s`)", int(bound/time.Second), command.Display), true
	}
	if result.unavailable {
		body.Set("outcome", outcomeUnavailable)
		return fmt.Sprintf("Could not run `%s`: %s", command.Display, result.runErr), true
	}
	body.Set("bootstrap_exit", jsonNumber(result.exit))
	if result.exit == 0 {
		return "", false
	}
	body.Set("outcome", "fail")
	return fmt.Sprintf("Bootstrap failed (`%s`): %s", command.Display, result.detail()), true
}

func runDeclared(runCtx context.Context, command lintCommand, dir, logPath string) commandResult {
	cmd := exec.CommandContext(runCtx, command.Argv[0], command.Argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(proc.ScrubbedEnv(), "MISE_QUIET=1")
	// The command's own findings go to stdout; the task runner writes its "task failed" chrome to stderr. Keeping the
	// streams apart lets the row quote the project's last line instead of the runner's.
	var output, stdout strings.Builder
	cmd.Stdout = io.MultiWriter(&output, &stdout)
	cmd.Stderr = &output
	started := time.Now()
	runErr := cmd.Run()
	result := commandResult{
		seconds: int(time.Since(started).Seconds()),
		stdout:  stdout.String(),
		output:  output.String(),
		runErr:  runErr,
		log:     logPath,
	}
	if writeErr := os.WriteFile(logPath, []byte(output.String()), 0o600); writeErr != nil {
		result.log = ""
	}
	if runCtx.Err() == context.DeadlineExceeded {
		result.timedOut = true
		return result
	}
	if runErr == nil {
		return result
	}
	exitErr, isExit := runErr.(*exec.ExitError)
	if !isExit {
		result.unavailable = true
		return result
	}
	result.exit = exitErr.ExitCode()
	return result
}

// discoverLint reports whether the project declares a lint task at all, separately from whether its runner is here:
// a declared task whose runner is missing is unobservable, never "no lint task".
func discoverLint(runtimeRoot, checkout string) (lintCommand, bool) {
	if declared := miseTaskSource(runtimeRoot, checkout, "lint"); declared != "" {
		command := lintCommand{Declared: declared, Display: "mise run lint"}
		if mise, err := toolpath.Find(runtimeRoot, "mise"); err == nil {
			command.Argv = []string{mise, "run", "lint"}
		}
		return command, true
	}
	if packageScript(filepath.Join(checkout, "package.json"), "lint") {
		command := lintCommand{Declared: "package.json", Display: "npm run lint"}
		if npm, err := exec.LookPath("npm"); err == nil {
			command.Argv = []string{npm, "run", "lint"}
		}
		return command, true
	}
	if makeTarget(filepath.Join(checkout, "Makefile")) {
		command := lintCommand{Declared: "Makefile", Display: "make lint"}
		if make, err := exec.LookPath("make"); err == nil {
			command.Argv = []string{make, "lint"}
		}
		return command, true
	}
	return lintCommand{}, false
}

// discoverBootstrap prefers a checkout-owned mise task named deps, then a lockfile-driven frozen install.
// A package.json with no lockfile is skipped rather than guessed.
func discoverBootstrap(runtimeRoot, checkout string) (lintCommand, bool) {
	if declared := miseTaskSource(runtimeRoot, checkout, "deps"); declared != "" {
		command := lintCommand{Declared: declared, Display: "mise run deps"}
		if mise, err := toolpath.Find(runtimeRoot, "mise"); err == nil {
			command.Argv = []string{mise, "run", "deps"}
		}
		return command, true
	}
	return lockfileInstall(checkout)
}

func lockfileInstall(checkout string) (lintCommand, bool) {
	if _, err := os.Stat(filepath.Join(checkout, "package.json")); err != nil {
		return lintCommand{}, false
	}
	for _, spec := range []struct {
		lock    string
		bin     string
		display string
		args    []string
	}{
		{"pnpm-lock.yaml", "pnpm", "pnpm install --frozen-lockfile", []string{"install", "--frozen-lockfile"}},
		{"package-lock.json", "npm", "npm ci", []string{"ci"}},
		{"yarn.lock", "yarn", "yarn install --frozen-lockfile", []string{"install", "--frozen-lockfile"}},
	} {
		if _, err := os.Stat(filepath.Join(checkout, spec.lock)); err != nil {
			continue
		}
		command := lintCommand{Declared: spec.lock, Display: spec.display}
		if bin, lookErr := exec.LookPath(spec.bin); lookErr == nil {
			command.Argv = append([]string{bin}, spec.args...)
		}
		return command, true
	}
	return lintCommand{}, false
}

// A task mise resolves from outside the checkout belongs to another repository; running it here would prepare the wrong tree.
func miseTaskSource(runtimeRoot, checkout, task string) string {
	fileTask := filepath.Join("mise-tasks", task)
	section := "[tasks." + task + "]"
	for _, name := range []string{fileTask, "mise.toml", ".mise.toml"} {
		path := filepath.Join(checkout, name)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if name == fileTask {
			return name
		}
		data, readErr := os.ReadFile(path)
		if readErr == nil && strings.Contains(string(data), section) {
			return name
		}
	}
	mise, err := toolpath.Find(runtimeRoot, "mise")
	if err != nil {
		return ""
	}
	run, runErr := proc.Run([]string{mise, "tasks", "ls", "--json"}, checkout, 30*time.Second, false,
		append(proc.ScrubbedEnv(), "MISE_QUIET=1"))
	if runErr != nil || run.Code != 0 {
		return ""
	}
	var tasks []struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	if json.Unmarshal([]byte(run.Stdout), &tasks) != nil {
		return ""
	}
	root, _ := filepath.EvalSymlinks(checkout)
	if root == "" {
		root, _ = filepath.Abs(checkout)
	}
	for _, listed := range tasks {
		if listed.Name != task || listed.Source == "" {
			continue
		}
		source, _ := filepath.EvalSymlinks(listed.Source)
		if source == "" {
			source = listed.Source
		}
		if source == root || strings.HasPrefix(source, root+string(os.PathSeparator)) {
			return strings.TrimPrefix(strings.TrimPrefix(source, root), string(os.PathSeparator))
		}
	}
	return ""
}

func packageScript(path, name string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return false
	}
	return strings.TrimSpace(manifest.Scripts[name]) != ""
}

func makeTarget(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return makeLint.MatchString(string(data))
}

// contractTimeout honours the project's own VERIFY.md bound, so sum never cuts a lint the project expects to be slow.
func contractTimeout(checkout string) time.Duration {
	data, err := os.ReadFile(filepath.Join(checkout, contractFile))
	if err != nil {
		return defaultLintBound
	}
	fence := verifyFence.FindStringSubmatch(string(data))
	if fence == nil {
		return defaultLintBound
	}
	for _, line := range strings.Split(fence[1], "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(key) != "timeout_seconds" {
			continue
		}
		seconds := 0
		if _, scanErr := fmt.Sscanf(strings.TrimSpace(value), "%d", &seconds); scanErr == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return defaultLintBound
}
