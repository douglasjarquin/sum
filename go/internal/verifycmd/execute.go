package verifycmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/graph"
	"github.com/douglasjarquin/sum/go/internal/graphview"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	verificationDir    = "verification"
	verificationRunner = ".agents/skills/verify/scripts/verify_run.py"
	runnerTimeout      = 4000 * time.Second
)

func executeRootVerification(s *store.Store, runtimeRoot string, task *ordjson.Object, candidate, base string, ctx *ordjson.Object) (*ordjson.Object, string, error) {
	worktree := asString(task, "worktree")
	if worktree == "" {
		return nil, "", fmt.Errorf("The task checkout is gone; nothing can be executed against the candidate from here.")
	}
	if info, err := os.Stat(worktree); err != nil || !info.IsDir() {
		return nil, "", fmt.Errorf("The task checkout is gone; nothing can be executed against the candidate from here.")
	}
	cat, err := proc.Run([]string{"git", "-C", worktree, "cat-file", "-e", candidate + "^{commit}"}, "", 20*time.Second, false, nil)
	if err != nil {
		return nil, "", err
	}
	if cat.Code != 0 {
		return nil, "", fmt.Errorf("%s is not a commit in the task repository; verify the SHA the worker reported.", candidate)
	}
	if base == "" {
		base = asString(task, "base_sha")
	}
	taskID := asString(task, "id")
	stamp, err := verificationStamp()
	if err != nil {
		return nil, "", err
	}
	taskPath, err := s.TaskPath(taskID)
	if err != nil {
		return nil, "", err
	}
	checkout := filepath.Join(taskPath, verificationDir, stamp, "checkout")
	host, err := os.Hostname()
	if err != nil {
		return nil, "", err
	}

	attemptID, err := reserveVerifier(s, ctx, taskID, candidate, checkout)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(checkout), 0o700); err != nil {
		teardownVerification(s, taskID, worktree, checkout, attemptID, nil)
		return nil, "", err
	}

	var (
		cmd     *exec.Cmd
		started bool
		exited  bool
	)
	defer func() {
		if started && !exited && cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			exited = true
		}
		teardownVerification(s, taskID, worktree, checkout, attemptID, cmd)
	}()

	if _, err := proc.Run([]string{"git", "-C", worktree, "worktree", "add", "--detach", checkout, candidate}, "", 120*time.Second, true, nil); err != nil {
		return nil, "", err
	}
	if err := setVerifierOccupant(s, taskID, attemptID, host, os.Getpid(), []string{"sumctl", "verify", "--execute"}, checkout); err != nil {
		return nil, "", err
	}

	graphSummary := graphview.Summary(graph.InitCheckout(s, runtimeRoot, checkout, "verification", nil))
	runner := filepath.Join(checkout, filepath.FromSlash(verificationRunner))
	if info, err := os.Stat(runner); err != nil || info.IsDir() {
		return nil, "", fmt.Errorf("Candidate %s carries no %s; the project is not standardized at this SHA. Run its documented commands and record them with --result.", candidate, verificationRunner)
	}
	python3, err := exec.LookPath("python3")
	if err != nil {
		return nil, "", fmt.Errorf("python3 is not on PATH; cannot run %s", verificationRunner)
	}
	argv := []string{python3, runner, "--json"}
	if base != "" {
		argv = append(argv, "--base", base)
	}
	runCtx, cancel := context.WithTimeout(context.Background(), runnerTimeout)
	defer cancel()
	cmd = exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir = checkout
	cmd.Env = runnerEnv()
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}
	started = true
	if err := markVerifierRunning(s, taskID, attemptID, host, cmd.Process.Pid, argv, checkout); err != nil {
		return nil, "", err
	}
	waitErr := cmd.Wait()
	exited = true
	if runCtx.Err() == context.DeadlineExceeded {
		return nil, "", fmt.Errorf("verify_run.py did not finish within 4000s; the run is inconclusive and nothing was recorded")
	}
	exitCode := 0
	if waitErr != nil {
		if ee, ok := waitErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			return nil, "", waitErr
		}
	}
	parsed, err := parseRunnerStdout(stdout.String(), stderr.String(), exitCode)
	if err != nil {
		return nil, "", err
	}
	kept := filepath.Join(filepath.Dir(checkout), "run.json")
	if err := ordjson.WriteFile(kept, parsed); err != nil {
		return nil, "", err
	}
	copyVerifyLog(parsed, checkout, filepath.Dir(checkout))
	removeCheckoutArtifacts(parsed, checkout)
	parsed.Set("root", checkout)
	parsed.Set("graph", graphSummary)
	return parsed, kept, nil
}

func reserveVerifier(s *store.Store, ctx *ordjson.Object, taskID, candidate, checkout string) (string, error) {
	unlock, err := s.Lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	current, err := s.ReadTask(taskID)
	if err != nil {
		return "", err
	}
	if err := repair.RefuseDuringCleanup(current, "Root verification"); err != nil {
		return "", err
	}
	tasks, err := s.AllTasks()
	if err != nil {
		return "", err
	}
	admission, err := launch.Admit(s, tasks, asString(current, "repository"))
	if err != nil {
		return "", err
	}
	owner := ordjson.NewObject()
	owner.Set("machine", asString(ctx, "machine"))
	owner.Set("session", asString(ctx, "session"))
	owner.Set("pane", asString(ctx, "pane"))
	attempt, err := reservations.NewAttempt("verifier", owner, checkout, store.Now(), "starting", candidate)
	if err != nil {
		return "", wrapReservation(taskID, err)
	}
	attempt.Set("admission", admission)
	attempt.Set("operation_pid", json.Number(fmt.Sprint(os.Getpid())))
	if err := reservations.AddVerifier(current, attempt); err != nil {
		return "", wrapReservation(taskID, err)
	}
	if err := s.SaveTask(current); err != nil {
		return "", err
	}
	return asString(attempt, "id"), nil
}

func wrapReservation(taskID string, err error) error {
	var format *reservations.FormatError
	if errors.As(err, &format) {
		return fmt.Errorf("Malformed execution reservation for %s: %s. Root verification is refused.", taskID, err)
	}
	return err
}

func verificationStamp() (string, error) {
	buf := make([]byte, 3)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(buf), nil
}

func runnerEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(key, "HERDR_") || key == "SUM_HOME" || key == "SUM_SESSION" || key == "SUM_INSTALL_ROOT" {
			continue
		}
		env = append(env, e)
	}
	return env
}

func anyStrings(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

func verifierOccupant(machine string, pid int, argv []string, checkout string) *ordjson.Object {
	row := ordjson.NewObject()
	row.Set("machine", machine)
	row.Set("pid", json.Number(fmt.Sprint(pid)))
	row.Set("argv", anyStrings(argv))
	row.Set("checkout", checkout)
	return row
}

func setVerifierOccupant(s *store.Store, taskID, attemptID, machine string, pid int, argv []string, checkout string) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	current, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	row, err := findVerifier(current, attemptID)
	if err != nil {
		return err
	}
	row.Set("occupant", verifierOccupant(machine, pid, argv, checkout))
	return s.SaveTask(current)
}

func markVerifierRunning(s *store.Store, taskID, attemptID, machine string, pid int, argv []string, checkout string) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	current, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	obs := ordjson.NewObject()
	obs.Set("at", store.Now())
	obs.Set("outcome", "started")
	obs.Set("pid", json.Number(fmt.Sprint(pid)))
	obs.Set("argv", anyStrings(argv))
	obs.Set("checkout", checkout)
	row, err := reservations.Transition(current, attemptID, "running", store.Now(), obs, nil)
	if err != nil {
		return fmt.Errorf("Root verification reservation %s changed during execution; it remains held for inspection.", attemptID)
	}
	row.Set("occupant", verifierOccupant(machine, pid, argv, checkout))
	return s.SaveTask(current)
}

func findVerifier(task *ordjson.Object, attemptID string) (*ordjson.Object, error) {
	execution, err := reservations.GetExecution(task)
	if err != nil {
		return nil, wrapReservation(asString(task, "id"), err)
	}
	if execution == nil {
		return nil, fmt.Errorf("Root verification reservation %s changed during execution; it remains held for inspection.", attemptID)
	}
	for _, row := range execution.Verifiers {
		if asString(row, "id") == attemptID {
			return row, nil
		}
	}
	return nil, fmt.Errorf("Root verification reservation %s changed during execution; it remains held for inspection.", attemptID)
}

func parseRunnerStdout(stdout, stderr string, exitCode int) (*ordjson.Object, error) {
	value, err := ordjson.Decode([]byte(stdout))
	if err != nil {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = strings.TrimSpace(stdout)
		}
		if len(detail) > 600 {
			detail = detail[len(detail)-600:]
		}
		return nil, fmt.Errorf("verify_run.py exited %d without a JSON record: %s", exitCode, detail)
	}
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("verify_run.py returned an unrecognized record")
	}
	schema, _ := obj.Get("schema")
	n, _ := schema.(json.Number)
	i, _ := n.Int64()
	if i != 1 || !runIDPattern.MatchString(asString(obj, "run_id")) {
		return nil, fmt.Errorf("verify_run.py returned an unrecognized record")
	}
	return obj, nil
}

func copyVerifyLog(record *ordjson.Object, checkout, destDir string) {
	artifacts := objectField(record, "artifacts")
	runDir := asString(artifacts, "run_dir")
	if runDir == "" {
		return
	}
	src := filepath.Join(checkout, runDir, "verify.log")
	if info, err := os.Stat(src); err != nil || info.IsDir() {
		return
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(destDir, "verify.log"), data, 0o644)
}

func removeCheckoutArtifacts(record *ordjson.Object, checkout string) {
	artifacts := objectField(record, "artifacts")
	dir := asString(artifacts, "dir")
	if dir == "" {
		return
	}
	path := filepath.Join(checkout, dir)
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return
	}
	_ = os.RemoveAll(path)
}

func teardownVerification(s *store.Store, taskID, worktree, checkout, attemptID string, cmd *exec.Cmd) {
	exclude := map[int]bool{}
	processStopped := false
	var descendants any
	var descendantError any
	checkoutPresent := pathExists(checkout)
	exited := cmd == nil || cmd.Process == nil || cmd.ProcessState != nil
	switch {
	case !checkoutPresent && exited:
		processStopped = true
	case checkoutPresent && exited:
		if cmd != nil && cmd.Process != nil {
			exclude[cmd.Process.Pid] = true
		}
		inside, procErr := proc.ProcessesIn(checkout, exclude)
		if procErr != nil {
			descendantError = procErr.Error()
		} else {
			rows := make([]any, 0, len(inside))
			for _, row := range inside {
				item := ordjson.NewObject()
				item.Set("pid", json.Number(fmt.Sprint(row.PID)))
				item.Set("cwd", row.CWD)
				rows = append(rows, item)
			}
			descendants = rows
			processStopped = len(inside) == 0
			if !processStopped {
				descendantError = "verification descendants remain"
			}
		}
	default:
		descendantError = "verification parent did not exit"
	}

	removeCode := 1
	removeDetail := ""
	if processStopped && checkoutPresent {
		removed, removeErr := proc.Run([]string{"git", "-C", worktree, "worktree", "remove", checkout}, "", 60*time.Second, false, nil)
		if removeErr != nil {
			removeDetail = removeErr.Error()
		} else {
			removeCode = removed.Code
			removeDetail = strings.TrimSpace(removed.Stderr)
			if removeDetail == "" {
				removeDetail = strings.TrimSpace(removed.Stdout)
			}
		}
	} else if processStopped {
		removeCode = 0
	} else if msg, ok := descendantError.(string); ok {
		removeDetail = msg
	} else {
		removeDetail = "verification descendants remain"
	}
	if len(removeDetail) > 1000 {
		removeDetail = removeDetail[len(removeDetail)-1000:]
	}
	if removeCode != 0 && pathExists(checkout) {
		note := fmt.Sprintf("git worktree remove exited %d: %s\nThe verification checkout was left in place; inspect it, then remove it with `git worktree remove`.\n", removeCode, removeDetail)
		_ = os.WriteFile(filepath.Join(filepath.Dir(checkout), "checkout-not-removed.txt"), []byte(note), 0o644)
	}

	// Empty-boundary teardown releases even when the run failed. Missing identity would not.
	state, outcome := "uncertain", "uncertain"
	if processStopped && removeCode == 0 && !pathExists(checkout) {
		state, outcome = "released", "stopped"
	}

	unlock, err := s.Lock()
	if err != nil {
		return
	}
	defer unlock()
	current, err := s.ReadTask(taskID)
	if err != nil {
		return
	}
	obs := ordjson.NewObject()
	obs.Set("at", store.Now())
	obs.Set("outcome", outcome)
	obs.Set("checkout_present", pathExists(checkout))
	obs.Set("remove_exit", json.Number(fmt.Sprint(removeCode)))
	obs.Set("descendants", descendants)
	obs.Set("descendant_error", descendantError)
	row, transErr := reservations.Transition(current, attemptID, state, store.Now(), obs, nil)
	if transErr != nil {
		return
	}
	if state == "released" {
		row.Delete("operation_pid")
	}
	_ = s.SaveTask(current)
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
