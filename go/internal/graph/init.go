package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/graphview"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	// defaultGraphTimeout is the documented bound on one `codegraph init`.
	defaultGraphTimeout = 300 * time.Second
	// statusTimeout bounds one `codegraph status --json` observation.
	statusTimeout = 60 * time.Second
	// statusPreview is how much of a non-JSON status answer the recorded reason quotes.
	statusPreview = 200
)

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func runGit(worktree string, args ...string) (string, error) {
	result, err := proc.Run(append([]string{"git", "-C", worktree}, args...), "", proc.DefaultTimeout, true, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

// codegraphEnv is the environment every codegraph call runs with: no shared daemon, no download, no color.
func codegraphEnv() []string {
	return append(os.Environ(), "CODEGRAPH_NO_DAEMON=1", "CODEGRAPH_NO_DOWNLOAD=1", "NO_COLOR=1")
}

// graphTimeout is the bound on `codegraph init`: 300 s, or SUM_GRAPH_TIMEOUT positive whole seconds (a lab knob).
func graphTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("SUM_GRAPH_TIMEOUT")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return defaultGraphTimeout
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
	path := asString(func() any { v, _ := tool.Get("path"); return v }())
	if err := runCodegraph(path, worktree, "init"); err != nil {
		appendAttempt(record, "init", false, err.Error())
		record.Set("state", "failed")
		record.Set("error", err.Error())
		return record
	}
	_ = ensureExclude(worktree)
	head, _ := ident.Get("head")
	record.Set("indexed_head", head)
	record.Set("state", "ready")
	record.Set("error", nil)
	index := ordjson.NewObject()
	index.Set("fileCount", nil)
	index.Set("nodeCount", nil)
	record.Set("index", index)
	quoted := fmt.Sprintf("%q", worktree)
	commands := ordjson.NewObject()
	commands.Set("status", fmt.Sprintf("CODEGRAPH_NO_DAEMON=1 %s status --json %s", path, quoted))
	commands.Set("sync", fmt.Sprintf("CODEGRAPH_NO_DAEMON=1 %s sync %s", path, quoted))
	commands.Set("explore", fmt.Sprintf("CODEGRAPH_NO_DAEMON=1 %s query NAME -p %s --json", path, quoted))
	commands.Set("query", fmt.Sprintf("CODEGRAPH_NO_DAEMON=1 %s query NAME -p %s --json", path, quoted))
	commands.Set("node", fmt.Sprintf("CODEGRAPH_NO_DAEMON=1 %s query NAME -p %s --json", path, quoted))
	commands.Set("affected", fmt.Sprintf("CODEGRAPH_NO_DAEMON=1 %s query NAME -p %s --json", path, quoted))
	record.Set("commands", commands)
	appendAttempt(record, "init", true, "")
	appendAttempt(record, "verified", true, "")
	return record
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func ensureExclude(worktree string) error {
	exclude := filepath.Join(worktree, ".git", "info", "exclude")
	if common, err := runGit(worktree, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil && common != "" {
		exclude = filepath.Join(common, "info", "exclude")
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	existing, _ := os.ReadFile(exclude)
	if strings.Contains(string(existing), ".codegraph/") {
		return nil
	}
	f, err := os.OpenFile(exclude, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString("\n.codegraph/\n")
	return err
}

// runCodegraph runs one indexing command under graphTimeout. Its stdout is progress text, never parsed. A timeout
// stops only the direct launcher; the error then says so, including when the indexer behind it still holds the
// output and may still be running.
func runCodegraph(bin, worktree string, args ...string) error {
	argv := append(append([]string{bin}, args...), worktree)
	result, err := proc.RunContext(context.Background(), proc.Cmd{
		Argv:     argv,
		Dir:      worktree,
		Env:      codegraphEnv(),
		Timeout:  graphTimeout(),
		Check:    true,
		FreeText: true,
	})
	if err == nil {
		return nil
	}
	if errors.Is(err, proc.ErrUncertain) || errors.Is(err, proc.ErrNotStarted) {
		return err
	}
	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(result.Stdout)
	}
	if detail == "" {
		return err
	}
	return errors.New(detail)
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
	var live *ordjson.Object
	if record != nil {
		tool := asObject(func() any { v, _ := record.Get("tool"); return v }())
		bin := asString(func() any { v, _ := tool.Get("path"); return v }())
		worktree := asString(func() any { v, _ := record.Get("worktree"); return v }())
		if bin != "" && worktree != "" {
			live = observeStatus(bin, worktree)
		}
	}
	if live == nil {
		result.Set("live", nil)
	} else {
		result.Set("live", live)
	}
	result.Set("note", "Observation only. `stale` means edits are not in the index until `sync`; a `reconcile_needed` action runs only through `graph init`.")
	return result, nil
}

// observeStatus runs `codegraph status --json` once. Only complete, in-limit JSON from a command that exited 0 becomes
// a live status; anything else leaves status and freshness unset and names the reason in `error`.
func observeStatus(bin, worktree string) *ordjson.Object {
	live := ordjson.NewObject()
	result, err := proc.RunContext(context.Background(), proc.Cmd{
		Argv:    []string{bin, "status", "--json", worktree},
		Dir:     worktree,
		Env:     codegraphEnv(),
		Timeout: statusTimeout,
		Check:   true,
	})
	if err != nil {
		live.Set("error", err.Error())
		return live
	}
	decoded, decErr := ordjson.Decode([]byte(result.Stdout))
	if decErr != nil {
		preview := strings.TrimSpace(result.Stdout)
		if len(preview) > statusPreview {
			preview = preview[:statusPreview]
		}
		live.Set("error", fmt.Sprintf("codegraph status did not return JSON (%s): %s", decErr, preview))
		return live
	}
	live.Set("status", decoded)
	fresh := ordjson.NewObject()
	fresh.Set("state", "fresh")
	if obj := asObject(decoded); obj != nil {
		if pending, ok := obj.Get("pendingChanges"); ok {
			if p := asObject(pending); p != nil {
				added, _ := p.Get("added")
				modified, _ := p.Get("modified")
				if fmt.Sprint(added) != "0" || fmt.Sprint(modified) != "0" {
					fresh.Set("state", "stale")
				}
			}
		}
	}
	live.Set("freshness", fresh)
	live.Set("reconcile_needed", nil)
	return live
}

func asObject(v any) *ordjson.Object {
	o, _ := v.(*ordjson.Object)
	return o
}
