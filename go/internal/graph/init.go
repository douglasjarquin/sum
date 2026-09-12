package graph

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/graphview"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func runGit(worktree string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", worktree}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func identity(worktree string) (*ordjson.Object, error) {
	abs, err := filepath.Abs(worktree)
	if err != nil {
		return nil, err
	}
	if resolved, resErr := filepath.EvalSymlinks(abs); resErr == nil {
		abs = resolved
	}
	toplevel, err := runGit(worktree, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	if resolved, resErr := filepath.EvalSymlinks(toplevel); resErr == nil {
		toplevel = resolved
	}
	if toplevel != abs {
		return nil, fmt.Errorf("%s is not the top level of its checkout (%s); the index is built only at a checkout root", worktree, toplevel)
	}
	head, err := runGit(worktree, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	branch, _ := runGit(worktree, "branch", "--show-current")
	common, _ := runGit(worktree, "rev-parse", "--path-format=absolute", "--git-common-dir")
	row := ordjson.NewObject()
	row.Set("worktree", toplevel)
	row.Set("head", head)
	if branch == "" {
		row.Set("branch", nil)
	} else {
		row.Set("branch", branch)
	}
	row.Set("git_common_dir", common)
	return row, nil
}

func InitCheckout(s *store.Store, runtimeRoot, worktree, purpose string, existing *ordjson.Object) *ordjson.Object {
	record := existing
	if record == nil {
		record = ordjson.NewObject()
		record.Set("schema", jsonInt(graphview.GraphSchema))
		record.Set("purpose", purpose)
		record.Set("attempts", []any{})
		record.Set("indexed_head", nil)
	}
	abs, _ := filepath.Abs(worktree)
	record.Set("worktree", abs)
	record.Set("index_path", filepath.Join(abs, ".codegraph"))
	record.Set("updated_at", store.Now())
	record.Set("fallback", graphview.GraphFallback)
	record.Set("error", nil)
	ident, err := identity(worktree)
	if err != nil {
		appendAttempt(record, "identity", false, err.Error())
		record.Set("state", "failed")
		record.Set("error", err.Error())
		return record
	}
	record.Set("identity", ident)
	tool := Tool(runtimeRoot)
	record.Set("tool", tool)
	available, _ := tool.Get("available")
	if available != true {
		reason, _ := tool.Get("reason")
		record.Set("state", "unavailable")
		record.Set("error", reason)
		return record
	}
	head, _ := ident.Get("head")
	record.Set("indexed_head", head)
	record.Set("state", "ready")
	record.Set("error", nil)
	index := ordjson.NewObject()
	index.Set("fileCount", nil)
	index.Set("nodeCount", nil)
	record.Set("index", index)
	commands := ordjson.NewObject()
	path, _ := tool.Get("path")
	quoted := fmt.Sprintf("%v", worktree)
	commands.Set("status", fmt.Sprintf("CODEGRAPH_NO_DAEMON=1 %v status --json %s", path, quoted))
	record.Set("commands", commands)
	appendAttempt(record, "verified", true, "tool present; full index deferred to an explicit graph init retry")
	return record
}

func appendAttempt(record *ordjson.Object, action string, ok bool, errText string) {
	list, _ := record.Get("attempts")
	items, _ := list.([]any)
	row := ordjson.NewObject()
	row.Set("at", store.Now())
	row.Set("action", action)
	row.Set("ok", ok)
	if errText != "" {
		row.Set("error", errText)
	}
	record.Set("attempts", append(items, row))
}

func WriteTaskGraph(s *store.Store, task, record *ordjson.Object) error {
	id, _ := task.Get("id")
	idStr, _ := id.(string)
	path, err := graphview.Path(s, idStr)
	if err != nil {
		return err
	}
	if err := ordjson.WriteFile(path, record); err != nil {
		return err
	}
	task.Set("graph", graphview.Summary(record))
	return nil
}

func InitTask(s *store.Store, runtimeRoot, taskID string) (*ordjson.Object, error) {
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	worktree, _ := task.Get("worktree")
	worktreeStr, _ := worktree.(string)
	if worktreeStr == "" {
		return nil, fmt.Errorf("This task has no worktree.")
	}
	if _, err := os.Stat(worktreeStr); err != nil {
		return nil, fmt.Errorf("Task worktree is missing: %s", worktreeStr)
	}
	existing, err := graphview.Read(s, taskID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		state, _ := existing.Get("state")
		if state == "exhausted" {
			return nil, fmt.Errorf("Graph initialization for %s is exhausted; the recorded fallback is source inspection. Inspect the attempts in the graph record; sum does not retry beyond the bound.", taskID)
		}
	}
	record := InitCheckout(s, runtimeRoot, worktreeStr, "task", existing)
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	if err := WriteTaskGraph(s, current, record); err != nil {
		return nil, err
	}
	if err := s.SaveTask(current); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	id, _ := current.Get("id")
	result.Set("task", id)
	result.Set("graph", record)
	result.Set("note", "The task, its checkout, and its worker are unchanged; nothing was launched or restarted. A running worker sees the new state through `context --section execution` or a requested brief revision, never through a forced restart.")
	return result, nil
}

func StatusTask(s *store.Store, runtimeRoot, taskID string) (*ordjson.Object, error) {
	if _, err := s.ReadTask(taskID); err != nil {
		return nil, err
	}
	record, err := graphview.Read(s, taskID)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", taskID)
	if record != nil {
		result.Set("recorded", graphview.Summary(record))
		commands, _ := record.Get("commands")
		result.Set("commands", commands)
	} else {
		result.Set("recorded", nil)
		result.Set("commands", nil)
	}
	result.Set("live", nil)
	result.Set("note", "Observation only. `stale` means edits are not in the index until `sync`; a `reconcile_needed` action runs only through `graph init`.")
	return result, nil
}
