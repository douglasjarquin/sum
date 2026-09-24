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
	// maxFailures is the documented bound: the third failed attempt records `exhausted`.
	maxFailures = 3
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
		return failAttempt(record, "identity", err)
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
		return failAttempt(record, "init", err)
	}
	// `init` exits 0 on an index an interrupted build left behind, so only status can confirm a complete one; an
	// incomplete index gets one full rebuild.
	status, err := completeIndex(path, worktree)
	rebuilt := false
	if errors.Is(err, errIncomplete) {
		if err := runCodegraph(path, worktree, "index"); err != nil {
			return failAttempt(record, "index", err)
		}
		rebuilt = true
		status, err = completeIndex(path, worktree)
	}
	if err != nil {
		return failAttempt(record, "verified", err)
	}
	_ = ensureExclude(worktree)
	head, _ := ident.Get("head")
	record.Set("indexed_head", head)
	record.Set("state", "ready")
	record.Set("error", nil)
	index := ordjson.NewObject()
	for _, key := range []string{"fileCount", "nodeCount", "edgeCount"} {
		v, _ := status.Get(key)
		index.Set(key, v)
	}
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
	if rebuilt {
		appendAttempt(record, "index", true, "")
	}
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

// runCodegraph runs one indexing command under graphTimeout. Its stdout is progress text, never parsed. A failure
// keeps proc's bounded detail (stderr tail, else the stdout head, at most 4000 bytes). A timeout stops only the
// direct launcher; the error then says so, including when the indexer behind it still holds the output and may
// still be running.
func runCodegraph(bin, worktree string, args ...string) error {
	_, err := proc.RunContext(context.Background(), proc.Cmd{
		Argv:     append(append([]string{bin}, args...), worktree),
		Dir:      worktree,
		Env:      codegraphEnv(),
		Timeout:  graphTimeout(),
		Check:    true,
		FreeText: true,
	})
	return err
}

// errIncomplete marks a status that answered cleanly but does not report a complete index for this checkout.
var errIncomplete = errors.New("index is not complete")

// completeIndex returns the status of a complete index built for this checkout, or why it cannot be confirmed.
func completeIndex(bin, worktree string) (*ordjson.Object, error) {
	live := observeStatus(bin, worktree)
	if errValue, ok := live.Get("error"); ok {
		return nil, fmt.Errorf("codegraph status could not confirm the index: %s", asString(errValue))
	}
	status := asObject(func() any { v, _ := live.Get("status"); return v }())
	initialized, _ := status.Get("initialized")
	state, _ := asObject(func() any { v, _ := status.Get("index"); return v }()).Get("state")
	mismatch, _ := status.Get("worktreeMismatch")
	if initialized != true || state != "complete" || mismatch != nil {
		return nil, fmt.Errorf("%w: codegraph status reports initialized %v, index state %v, worktree mismatch %v", errIncomplete, initialized, state, mismatch)
	}
	return status, nil
}

// failAttempt records one failed action; the maxFailures-th failure exhausts the record.
func failAttempt(record *ordjson.Object, action string, err error) *ordjson.Object {
	appendAttempt(record, action, false, err.Error())
	record.Set("state", "failed")
	if graphview.FailureCount(record) >= maxFailures {
		record.Set("state", "exhausted")
	}
	record.Set("error", err.Error())
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
	writers, err := liveWriters(worktreeStr)
	if err != nil {
		return nil, fmt.Errorf("Cannot prove that no codegraph writer is running for %s (%s); nothing was started or recorded.", worktreeStr, err)
	}
	if len(writers) > 0 {
		return nil, fmt.Errorf("A codegraph writer is still running for %s (%s). Nothing was started or recorded, and sum stops no process; retry `graph init %s` after it exits. `graph status %s` observes the index meanwhile.", worktreeStr, strings.Join(writers, "; "), taskID, taskID)
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

// writerCommands are the codegraph subcommands that write an index.
var writerCommands = map[string]bool{"init": true, "index": true, "sync": true}

// liveWriters names live codegraph processes writing this checkout's index. The real indexer runs behind the npm
// shim as `<bundle>/node ... lib/dist/bin/codegraph.js init <checkout>`, never under the runtime's tool path, so
// the match is by argv shape rather than by the recorded binary.
func liveWriters(worktree string) ([]string, error) {
	roots := []string{worktree}
	if resolved, err := filepath.EvalSymlinks(worktree); err == nil && resolved != worktree {
		roots = append(roots, resolved)
	}
	table, err := proc.ProcessTable()
	if err != nil {
		return nil, err
	}
	self := os.Getpid()
	var writers []string
	for _, row := range table {
		if row.PID != self && isWriter(row.Args, roots) {
			args := row.Args
			if len(args) > statusPreview {
				args = args[:statusPreview] + "..."
			}
			writers = append(writers, fmt.Sprintf("pid %d: %s", row.PID, args))
		}
	}
	return writers, nil
}

// isWriter reports an argument string whose program is codegraph (any path, any extension), whose first non-flag
// argument after it is a writing subcommand, and which names one of roots or a path inside it. The checkout is
// matched in the raw string because checkout paths may contain spaces.
func isWriter(args string, roots []string) bool {
	fields := strings.Fields(args)
	for i, field := range fields {
		base := filepath.Base(field)
		if strings.TrimSuffix(base, filepath.Ext(base)) != "codegraph" {
			continue
		}
		for _, next := range fields[i+1:] {
			if strings.HasPrefix(next, "-") {
				continue
			}
			return writerCommands[next] && namesRoot(args, roots)
		}
		return false
	}
	return false
}

// namesRoot reports a root that appears in args as a whole path: preceded by the start, a space, or `=`, and
// followed by the end, a space, or `/`.
func namesRoot(args string, roots []string) bool {
	for _, root := range roots {
		for offset := 0; ; {
			i := strings.Index(args[offset:], root)
			if i < 0 {
				break
			}
			start, end := offset+i, offset+i+len(root)
			before := start == 0 || args[start-1] == ' ' || args[start-1] == '='
			after := end == len(args) || args[end] == ' ' || args[end] == '/'
			if before && after {
				return true
			}
			offset = start + 1
		}
	}
	return false
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
	if hasPendingChanges(decoded) {
		fresh.Set("state", "stale")
	}
	live.Set("freshness", fresh)
	live.Set("reconcile_needed", nil)
	return live
}

// hasPendingChanges reports a status whose pendingChanges count added or modified files.
func hasPendingChanges(status any) bool {
	obj := asObject(status)
	if obj == nil {
		return false
	}
	pending, ok := obj.Get("pendingChanges")
	if !ok {
		return false
	}
	p := asObject(pending)
	if p == nil {
		return false
	}
	added, _ := p.Get("added")
	modified, _ := p.Get("modified")
	return fmt.Sprint(added) != "0" || fmt.Sprint(modified) != "0"
}

func asObject(v any) *ordjson.Object {
	o, _ := v.(*ordjson.Object)
	return o
}
