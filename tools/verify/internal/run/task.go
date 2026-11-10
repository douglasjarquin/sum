package run

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/tools/verify/internal/contract"
	"github.com/douglasjarquin/sum/tools/verify/internal/proc"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
	"github.com/douglasjarquin/sum/tools/verify/internal/value"
)

type requirements struct {
	Commands []string `json:"commands"`
}

type task struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Owner  string `json:"owner"`
	Binary string `json:"binary"`
}

type execution struct {
	Command   string      `json:"command"`
	Cwd       string      `json:"cwd"`
	StartedAt string      `json:"started_at"`
	EndedAt   string      `json:"ended_at"`
	Seconds   value.Float `json:"seconds"`
	Exit      *int        `json:"exit"`
	TimedOut  bool        `json:"timed_out"`
	Log       string      `json:"log"`
}

func (e *execution) passed() bool {
	return e.Exit != nil && *e.Exit == 0
}

// git runs a Git command whose failure blocks the run.
func git(root string, args ...string) (string, error) {
	stdout, stderr, ok := contract.Git(root, args...)
	if !ok {
		detail := stderr
		if detail == "" {
			detail = stdout
		}
		return "", contract.Blockedf("git %s failed: %s", strings.Join(args, " "), pycompat.Clip(pycompat.Strip(detail), 300))
	}
	return stdout, nil
}

func checkRequirements(commands []string) (*requirements, error) {
	var missing []string
	for _, command := range commands {
		if _, err := exec.LookPath(command); err != nil {
			missing = append(missing, command)
		}
	}
	if len(missing) > 0 {
		return nil, contract.Blockedf("Required command(s) not found on PATH: %s. The run is blocked, not failed.", strings.Join(missing, ", "))
	}
	return &requirements{Commands: commands}, nil
}

// miseTask asks `mise tasks ls --json` from the root which task has this name. The selected task's source must live
// under this repository (or its documented monorepo owner).
func miseTask(root, ownerRelative, name string) (*task, error) {
	notADirectory := contract.Blockedf("task_owner %s is not a directory of this repository.", pycompat.Repr(ownerRelative))
	ownerPath := pycompat.Join(root, ownerRelative)
	if containsSymlink(ownerPath, root) {
		return nil, contract.Blockedf("task_owner %s traverses a symlink.", pycompat.Repr(ownerRelative))
	}
	owner := pycompat.Resolve(ownerPath)
	if !pycompat.Within(owner, root) {
		return nil, notADirectory
	}
	mise := contract.ListMiseTasks(root)
	binary := mise.Binary
	if binary == "" {
		return nil, contract.Blockedf("mise is not on PATH; the canonical entrypoint cannot run.")
	}
	if mise.Err != nil && pycompat.Strip(string(mise.Stdout)) == "" {
		if mise.State == nil {
			return nil, contract.Blockedf("mise tasks ls could not be started: %v", mise.Err)
		}
		return nil, contract.Blockedf("mise tasks ls exited %d: %s", proc.ExitCode(mise.State), pycompat.Tail(pycompat.Strip(string(mise.Stderr)), 300))
	}
	var listed any = []any{}
	if len(mise.Stdout) > 0 && json.Unmarshal(mise.Stdout, &listed) != nil {
		return nil, contract.Blockedf("mise tasks ls did not return JSON.")
	}
	if !pycompat.IsDir(owner) {
		return nil, notADirectory
	}
	var selected map[string]any
	rows, _ := listed.([]any)
	for _, row := range rows {
		if candidate, ok := row.(map[string]any); ok && candidate["name"] == name {
			selected = candidate
			break
		}
	}
	if selected == nil {
		return nil, contract.Blockedf("mise defines no `%s` task for this repository; VERIFY.md names an entrypoint that does not exist.", name)
	}
	source, _ := selected["source"].(string)
	if source == "" {
		source, _ = selected["file"].(string)
	}
	if source == "" {
		return nil, outsideRepository(name, "unknown source")
	}
	if containsSymlink(pycompat.Join(root, source), root) {
		return nil, contract.Blockedf("`mise run %s` here would execute a task source through a symlink (%s); it is not a trusted project command.", name, source)
	}
	sourcePath := pycompat.Resolve(source)
	if !pycompat.Within(sourcePath, owner) && !pycompat.Within(sourcePath, root) {
		return nil, outsideRepository(name, source)
	}
	return &task{Name: name, Source: sourcePath, Owner: owner, Binary: binary}, nil
}

func outsideRepository(name, source string) error {
	return contract.Blockedf("`mise run %s` here would execute a task defined outside this repository (%s); "+
		"it is another project's command, not this one's verification.", name, source)
}

// runEntrypoint runs `mise run verify` from the root with its output in the run directory's verify.log.
// A run that outlives the timeout is stopped and recorded with no exit code.
func runEntrypoint(root, runDir string, timeout int, binary string) (*execution, error) {
	logPath := runDir + "/verify.log"
	log, err := os.Create(logPath)
	if err != nil {
		return nil, contract.Blockedf("the run log could not be created: %v", err)
	}
	defer log.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "run", "verify")
	cmd.Dir = root
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, log, log
	cmd.Env = append(os.Environ(), "VERIFY_RUN_DIR="+runDir)
	result := &execution{Command: "mise run verify", Cwd: root, StartedAt: tool.UTCNow()}
	result.Log, _ = pycompat.Rel(logPath, root)
	started := time.Now()
	err = cmd.Run()
	var exited *exec.ExitError
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded) && err != nil:
		result.TimedOut = true
	case err == nil || errors.As(err, &exited):
		code := proc.ExitCode(cmd.ProcessState)
		result.Exit = &code
	default:
		return nil, contract.Blockedf("`mise run verify` could not be started: %v", err)
	}
	result.EndedAt, result.Seconds = tool.UTCNow(), tool.Since(started)
	return result, nil
}
