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
	defer removeCheckout(worktree, checkout)

	body, summary := RunLint(runtimeRoot, checkout, dir)
	return recordGate(s, ctx, args.Task, lintGate, candidate, body, summary)
}

// RunLint is shared with `verify --execute`, which runs the same step in the checkout it already made, so one
// command yields both the Test and the Lint row.
func RunLint(runtimeRoot, checkout, dir string) (*ordjson.Object, string) {
	body := ordjson.NewObject()
	command, declared := discoverLint(runtimeRoot, checkout)
	body.Set("command", nilIfEmpty(command.Display))
	body.Set("declared", nilIfEmpty(command.Declared))
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
	cmd := exec.CommandContext(runCtx, command.Argv[0], command.Argv[1:]...)
	cmd.Dir = checkout
	cmd.Env = append(proc.ScrubbedEnv(), "MISE_QUIET=1")
	// The lint's own findings go to stdout; the task runner writes its "task failed" chrome to stderr. Keeping the
	// streams apart lets the row quote the project's last line instead of the runner's.
	var output, stdout strings.Builder
	cmd.Stdout = io.MultiWriter(&output, &stdout)
	cmd.Stderr = &output
	started := time.Now()
	runErr := cmd.Run()
	seconds := int(time.Since(started).Seconds())

	log := filepath.Join(dir, "lint.log")
	if writeErr := os.WriteFile(log, []byte(output.String()), 0o600); writeErr != nil {
		log = ""
	}
	body.Set("seconds", jsonNumber(seconds))
	body.Set("log", nilIfEmpty(log))
	if runCtx.Err() == context.DeadlineExceeded {
		body.Set("outcome", "timeout")
		body.Set("exit", nil)
		return body, fmt.Sprintf("Did not finish within %ds (`%s`)", int(bound/time.Second), command.Display)
	}
	exit := 0
	if runErr != nil {
		exitErr, isExit := runErr.(*exec.ExitError)
		if !isExit {
			body.Set("outcome", outcomeUnavailable)
			body.Set("exit", nil)
			return body, fmt.Sprintf("Could not run `%s`: %s", command.Display, runErr)
		}
		exit = exitErr.ExitCode()
	}
	body.Set("exit", jsonNumber(exit))
	if exit == 0 {
		body.Set("outcome", "pass")
		return body, fmt.Sprintf("Passed (`%s`)", command.Display)
	}
	body.Set("outcome", "fail")
	detail := lastLineOf(stdout.String())
	if detail == "" {
		detail = lastLineOf(output.String())
	}
	if detail == "" {
		detail = fmt.Sprintf("exit %d with no output", exit)
	}
	return body, fmt.Sprintf("Failed (`%s`): %s", command.Display, detail)
}

// discoverLint reports whether the project declares a lint task at all, separately from whether its runner is here:
// a declared task whose runner is missing is unobservable, never "no lint task".
func discoverLint(runtimeRoot, checkout string) (lintCommand, bool) {
	if declared := miseLintSource(runtimeRoot, checkout); declared != "" {
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

// A task mise resolves from outside the checkout belongs to another repository; running it here would lint the wrong tree.
func miseLintSource(runtimeRoot, checkout string) string {
	for _, name := range []string{filepath.Join("mise-tasks", "lint"), "mise.toml", ".mise.toml"} {
		path := filepath.Join(checkout, name)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if strings.HasPrefix(name, "mise-tasks") {
			return name
		}
		data, readErr := os.ReadFile(path)
		if readErr == nil && strings.Contains(string(data), "[tasks.lint]") {
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
	for _, task := range tasks {
		if task.Name != "lint" || task.Source == "" {
			continue
		}
		source, _ := filepath.EvalSymlinks(task.Source)
		if source == "" {
			source = task.Source
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
