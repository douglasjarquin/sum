package prepare

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/procedure"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

func taskAccessArgs(s *store.Store, task *ordjson.Object, briefPath, worktree string) ([]string, error) {
	if asString(func() any { v, _ := task.Get("harness"); return v }()) != "claude" {
		return nil, nil
	}
	taskID := asString(func() any { v, _ := task.Get("id"); return v }())
	taskDir, err := s.TaskPath(taskID)
	if err != nil {
		return nil, fmt.Errorf("Cannot grant Claude access to task instructions: %w", err)
	}
	canonicalTasks, err := filepath.EvalSymlinks(s.Tasks)
	if err != nil {
		return nil, fmt.Errorf("Cannot grant Claude access to task instructions: resolve task directory %s: %w", s.Tasks, err)
	}
	canonicalTask, err := filepath.EvalSymlinks(taskDir)
	if err != nil {
		return nil, fmt.Errorf("Cannot grant Claude access to task instructions: resolve task directory %s: %w", taskDir, err)
	}
	if filepath.Dir(canonicalTask) != canonicalTasks || filepath.Base(canonicalTask) != taskID {
		return nil, fmt.Errorf("Cannot grant Claude access to task instructions: task directory %s is outside its task-specific state directory", canonicalTask)
	}
	if err := validateTaskInstructionFile(canonicalTask, briefPath); err != nil {
		return nil, fmt.Errorf("Cannot grant Claude access to the active brief: %w", err)
	}
	versionsObj, err := versions.ReadVersions(s, task)
	if err != nil {
		return nil, fmt.Errorf("Cannot grant Claude access to required task procedures: %w", err)
	}
	active := versions.ActiveRevision(versionsObj)
	if active == nil {
		return nil, fmt.Errorf("Cannot grant Claude access to required task procedures: the active brief revision is missing")
	}
	policy := asObject(func() any { v, _ := active.Get("policy"); return v }())
	for _, raw := range procedure.Rows(policy) {
		row := asObject(raw)
		if row == nil {
			return nil, fmt.Errorf("Cannot grant Claude access to required task procedures: an active procedure reference is malformed")
		}
		load := asString(func() any { v, _ := row.Get("load"); return v }())
		if load == procedure.OnDemand {
			continue
		}
		if load != procedure.Required {
			return nil, fmt.Errorf("Cannot grant Claude access to required task procedures: procedure %q has an invalid load condition", asString(func() any { v, _ := row.Get("name"); return v }()))
		}
		if err := procedure.VerifyRow(canonicalTask, row); err != nil {
			return nil, fmt.Errorf("Cannot grant Claude access to required task procedure %q: %w", asString(func() any { v, _ := row.Get("name"); return v }()), err)
		}
		resourcePath, ok := procedure.ResourcePath(canonicalTask, row)
		if !ok {
			return nil, fmt.Errorf("Cannot grant Claude access to required task procedure %q: its path is invalid", asString(func() any { v, _ := row.Get("name"); return v }()))
		}
		if err := validateTaskInstructionFile(canonicalTask, resourcePath); err != nil {
			return nil, fmt.Errorf("Cannot grant Claude access to required task procedure %q: %w", asString(func() any { v, _ := row.Get("name"); return v }()), err)
		}
	}
	canonicalWorktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return nil, fmt.Errorf("Cannot grant Claude access to task instructions: resolve worker checkout %s: %w", worktree, err)
	}
	if pathWithin(canonicalWorktree, canonicalTask) {
		return nil, nil
	}
	return []string{"--add-dir", canonicalTask}, nil
}

func validateTaskInstructionFile(taskDir, path string) error {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", path, err)
	}
	if !pathWithin(taskDir, canonical) {
		return fmt.Errorf("%s resolves outside this task's directory", path)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", canonical, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", canonical)
	}
	return nil
}

func pathWithin(parent, path string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func applyTaskAccessArgs(launchSpec *ordjson.Object, argv, extra, taskArgs []string) ([]string, error) {
	previous := stringValues(func() any { v, _ := launchSpec.Get("task_access_args"); return v }())
	if len(previous) > len(argv) {
		return nil, fmt.Errorf("Saved Claude task access arguments do not match the recorded launch arguments; inspect the task record before starting")
	}
	for i := range previous {
		if argv[len(argv)-len(previous)+i] != previous[i] {
			return nil, fmt.Errorf("Saved Claude task access arguments do not match the recorded launch arguments; inspect the task record before starting")
		}
	}
	base := append([]string(nil), argv[:len(argv)-len(previous)]...)
	base = append(base, extra...)
	base = append(base, taskArgs...)
	access := make([]any, len(taskArgs))
	for i, arg := range taskArgs {
		access[i] = arg
	}
	launchSpec.Set("task_access_args", access)
	return base, nil
}

func stringValues(value any) []string {
	list, _ := value.([]any)
	values := make([]string, 0, len(list))
	for _, item := range list {
		if value, ok := item.(string); ok {
			values = append(values, value)
		}
	}
	return values
}
