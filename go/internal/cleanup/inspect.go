package cleanup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/execution"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

var disposableIgnored = []string{"__pycache__", "*.pyc", ".pytest_cache", ".mypy_cache", ".ruff_cache", "node_modules", ".DS_Store", ".artifacts", ".codegraph"}

var shells = map[string]bool{
	"bash": true, "zsh": true, "sh": true, "fish": true, "dash": true, "ksh": true, "tcsh": true, "csh": true, "nu": true, "pwsh": true,
	"-bash": true, "-zsh": true, "-sh": true, "-fish": true,
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asObject(v any) *ordjson.Object {
	o, _ := v.(*ordjson.Object)
	return o
}

func asList(v any) []any {
	list, _ := v.([]any)
	return list
}

func stringField(o *ordjson.Object, key string) string {
	if o == nil {
		return ""
	}
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}

func workspaceOf(paneID string) string {
	if paneID == "" {
		return ""
	}
	if i := strings.IndexByte(paneID, ':'); i >= 0 {
		return paneID[:i]
	}
	return paneID
}

func unwrap(value any, key string) *ordjson.Object {
	obj := asObject(value)
	if obj == nil {
		return nil
	}
	if inner, ok := obj.Get(key); ok {
		if innerObj := asObject(inner); innerObj != nil {
			return innerObj
		}
	}
	return obj
}

func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	if errA != nil {
		ra, _ = filepath.Abs(a)
	}
	rb, errB := filepath.EvalSymlinks(b)
	if errB != nil {
		rb, _ = filepath.Abs(b)
	}
	return ra == rb
}

func identityEquals(a, b *ordjson.Object) bool {
	if a == nil || b == nil {
		return false
	}
	return stringField(a, "machine") == stringField(b, "machine") &&
		stringField(a, "session") == stringField(b, "session") &&
		stringField(a, "pane") == stringField(b, "pane")
}

func herdrPath(runtimeRoot string) (string, error) {
	return toolpath.Find(runtimeRoot, "herdr")
}

func observe(runtimeRoot, session string, timeout time.Duration, args ...string) (any, string, error) {
	path, err := herdrPath(runtimeRoot)
	if err != nil {
		return nil, "", err
	}
	return herdrclient.Observe(path, session, timeout, args...)
}

func worktreePaths(repo string) ([]string, error) {
	out, err := proc.Run([]string{"git", "-C", repo, "worktree", "list", "--porcelain"}, "", 20*time.Second, true, nil)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out.Stdout, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			p := strings.TrimPrefix(line, "worktree ")
			resolved, resErr := filepath.EvalSymlinks(p)
			if resErr != nil {
				resolved, _ = filepath.Abs(p)
			}
			paths = append(paths, resolved)
		}
	}
	return paths, nil
}

func disposable(relative string) bool {
	parts := strings.Split(filepath.ToSlash(relative), "/")
	for _, part := range parts {
		for _, pattern := range disposableIgnored {
			if matched, _ := filepath.Match(pattern, part); matched {
				return true
			}
		}
	}
	return false
}

func worktreeArtifacts(worktree string) (*ordjson.Object, error) {
	out, err := proc.Run([]string{"git", "-C", worktree, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=matching"}, "", 30*time.Second, true, nil)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	tracked := []any{}
	staged := []any{}
	untracked := []any{}
	ignoredPreserved := []any{}
	ignoredDisposable := []any{}
	entries := strings.Split(out.Stdout, "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}
		code, path := entry[:2], entry[3:]
		if code[0] == 'R' || code[0] == 'C' {
			i++
		}
		switch code {
		case "!!":
			if disposable(path) {
				ignoredDisposable = append(ignoredDisposable, path)
			} else {
				ignoredPreserved = append(ignoredPreserved, path)
			}
		case "??":
			untracked = append(untracked, path)
		default:
			if code[0] != ' ' {
				staged = append(staged, path)
			}
			if code[1] != ' ' {
				tracked = append(tracked, path)
			}
		}
	}
	result.Set("tracked_modified", tracked)
	result.Set("staged", staged)
	result.Set("untracked", untracked)
	result.Set("ignored_preserved", ignoredPreserved)
	result.Set("ignored_disposable", ignoredDisposable)
	return result, nil
}

func commitsNotCovered(worktree, mergedHead string) (string, []any, error) {
	headOut, err := proc.Run([]string{"git", "-C", worktree, "rev-parse", "HEAD"}, "", 20*time.Second, true, nil)
	if err != nil {
		return "", nil, err
	}
	head := strings.TrimSpace(headOut.Stdout)
	if head == mergedHead {
		return head, nil, nil
	}
	known, _ := proc.Run([]string{"git", "-C", worktree, "cat-file", "-e", mergedHead + "^{commit}"}, "", 20*time.Second, false, nil)
	if known.Code != 0 {
		return head, []any{fmt.Sprintf("checkout HEAD %s differs from the merged PR head %s, which is not a local object", head, mergedHead)}, nil
	}
	listOut, err := proc.Run([]string{"git", "-C", worktree, "rev-list", mergedHead + "..HEAD"}, "", 20*time.Second, true, nil)
	if err != nil {
		return head, nil, err
	}
	var extra []any
	for _, line := range strings.Split(strings.TrimSpace(listOut.Stdout), "\n") {
		if line != "" {
			extra = append(extra, line)
		}
	}
	return head, extra, nil
}

func processesIn(worktree string, exclude map[int]bool) ([]any, string) {
	found, err := proc.ProcessesIn(worktree, exclude)
	if err != nil {
		return nil, err.Error()
	}
	var inside []any
	for _, row := range found {
		item := ordjson.NewObject()
		item.Set("pid", json.Number(fmt.Sprint(row.PID)))
		item.Set("cwd", row.CWD)
		inside = append(inside, item)
	}
	return inside, ""
}

func paneOccupancy(runtimeRoot, session, paneID, worktree string) (*ordjson.Object, error) {
	view := ordjson.NewObject()
	view.Set("pane", paneID)
	view.Set("agent", nil)
	view.Set("foreground", []any{})
	view.Set("detached", []any{})
	var blockers []any
	agent, code, err := observe(runtimeRoot, session, 5*time.Second, "agent", "get", paneID)
	if err != nil {
		return nil, err
	}
	if agent != nil {
		agentObj := unwrap(agent, "agent")
		row := ordjson.NewObject()
		for _, k := range []string{"name", "agent", "agent_status"} {
			v, _ := agentObj.Get(k)
			row.Set(k, v)
		}
		view.Set("agent", row)
		blockers = append(blockers, fmt.Sprintf("pane %s still hosts agent %q (%v); Herdr idle/done is not exit, wait for the agent process to end", paneID, stringField(agentObj, "agent"), func() any { v, _ := agentObj.Get("agent_status"); return v }()))
	} else if code != "agent_not_found" {
		blockers = append(blockers, fmt.Sprintf("agent observation for pane %s is uncertain (%s)", paneID, code))
	}
	info, code, err := observe(runtimeRoot, session, 5*time.Second, "pane", "process-info", "--pane", paneID)
	if err != nil {
		return nil, err
	}
	if info == nil {
		blockers = append(blockers, fmt.Sprintf("process observation for pane %s is uncertain (%s)", paneID, code))
		view.Set("blockers", blockers)
		return view, nil
	}
	infoObj := unwrap(info, "process_info")
	shell, _ := infoObj.Get("shell_pid")
	view.Set("shell_pid", shell)
	var foreground []any
	fg, _ := infoObj.Get("foreground_processes")
	for _, raw := range asList(fg) {
		procObj := asObject(raw)
		pid, _ := procObj.Get("pid")
		name := stringField(procObj, "argv0")
		if name == "" {
			name = stringField(procObj, "name")
		}
		if fmt.Sprint(pid) == fmt.Sprint(shell) && shells[name] {
			continue
		}
		row := ordjson.NewObject()
		for _, k := range []string{"pid", "name", "cmdline", "cwd"} {
			v, _ := procObj.Get(k)
			row.Set(k, v)
		}
		foreground = append(foreground, row)
	}
	view.Set("foreground", foreground)
	if len(foreground) > 0 {
		var names []string
		for _, raw := range foreground {
			p := asObject(raw)
			names = append(names, fmt.Sprintf("%v (pid %v)", func() any { v, _ := p.Get("name"); return v }(), func() any { v, _ := p.Get("pid"); return v }()))
		}
		blockers = append(blockers, fmt.Sprintf("pane %s has foreground processes besides its shell: %s", paneID, strings.Join(names, ", ")))
	}
	if shell == nil {
		blockers = append(blockers, fmt.Sprintf("pane %s reported no shell pid; occupancy cannot be established", paneID))
	}
	if worktree != "" {
		exclude := map[int]bool{}
		if num, ok := shell.(json.Number); ok {
			if n, convErr := num.Int64(); convErr == nil {
				exclude[int(n)] = true
			}
		}
		inside, errorText := processesIn(worktree, exclude)
		if errorText != "" {
			blockers = append(blockers, "processes with a cwd in the checkout cannot be established: "+errorText)
		} else if len(inside) > 0 {
			view.Set("detached", inside)
			var parts []string
			for i, raw := range inside {
				if i >= 10 {
					break
				}
				p := asObject(raw)
				parts = append(parts, fmt.Sprintf("pid %v at %v", func() any { v, _ := p.Get("pid"); return v }(), func() any { v, _ := p.Get("cwd"); return v }()))
			}
			blockers = append(blockers, "processes still run inside the checkout (detached from the pane or another pane): "+strings.Join(parts, ", "))
		}
	}
	view.Set("blockers", blockers)
	return view, nil
}

type inspection struct {
	store       *store.Store
	task        *ordjson.Object
	ctx         *ordjson.Object
	runtimeRoot string
	blockers    []any
	resources   *ordjson.Object
	view        *ordjson.Object
	stoppable   []any
	environment *ordjson.Object
}

func newInspection(s *store.Store, task, ctx *ordjson.Object, runtimeRoot string) *inspection {
	ins := &inspection{
		store:       s,
		task:        task,
		ctx:         ctx,
		runtimeRoot: runtimeRoot,
		resources:   ordjson.NewObject(),
		view:        ordjson.NewObject(),
	}
	id, _ := task.Get("id")
	ins.view.Set("task", id)
	ins.view.Set("at", store.Now())
	env, err := environment.Read(s, asString(id))
	if err != nil {
		ins.block("environment", "environment record unreadable: "+err.Error())
		ins.environment = ordjson.NewObject()
	} else if env == nil {
		ins.environment = ordjson.NewObject()
	} else {
		ins.environment = env
	}
	return ins
}

func (ins *inspection) block(code, detail string) {
	row := ordjson.NewObject()
	row.Set("code", code)
	row.Set("detail", detail)
	ins.blockers = append(ins.blockers, row)
}

func (ins *inspection) hostname() string {
	h, _ := os.Hostname()
	return h
}

func (ins *inspection) identity() {
	task := ins.task
	for _, key := range []string{"pane", "workspace", "worktree", "branch", "repository", "session"} {
		if stringField(task, key) == "" {
			ins.block("identity", fmt.Sprintf("task record has no %s; nothing can be matched to a Herdr resource", key))
		}
	}
	if stringField(task, "machine") != ins.hostname() {
		ins.block("identity", fmt.Sprintf("task belongs to machine %s, this is %s", stringField(task, "machine"), ins.hostname()))
	}
	if stringField(task, "session") != stringField(ins.ctx, "session") {
		ins.block("identity", fmt.Sprintf("task lives in Herdr session %s, the coordinator runs in %s", stringField(task, "session"), stringField(ins.ctx, "session")))
	}
	if len(ins.blockers) > 0 {
		return
	}
	own := workspaceOf(stringField(ins.ctx, "pane"))
	pane, _, _ := observe(ins.runtimeRoot, stringField(ins.ctx, "session"), 5*time.Second, "pane", "get", stringField(ins.ctx, "pane"))
	if pane != nil {
		paneObj := unwrap(pane, "pane")
		if ws := stringField(paneObj, "workspace_id"); ws != "" {
			own = ws
		}
	}
	parent := asObject(func() any { v, _ := task.Get("parent"); return v }())
	parentWS := workspaceOf(stringField(parent, "pane"))
	if stringField(task, "workspace") == own || stringField(task, "workspace") == parentWS {
		ins.block("identity", fmt.Sprintf("task workspace %s is the coordinator's own workspace; refusing", stringField(task, "workspace")))
	}
	if stringField(task, "pane") == stringField(ins.ctx, "pane") {
		ins.block("identity", "task pane is the calling coordinator pane; refusing")
	}
	worktree := stringField(task, "worktree")
	repo := stringField(task, "repository")
	if samePath(worktree, repo) || strings.HasPrefix(worktree, repo+string(os.PathSeparator)) || strings.HasPrefix(repo, worktree+string(os.PathSeparator)) {
		ins.block("identity", fmt.Sprintf("task worktree %s overlaps the source repository %s; refusing", worktree, repo))
	}
}

func (ins *inspection) herdr() error {
	task := ins.task
	session := stringField(ins.ctx, "session")
	workspace, code, err := observe(ins.runtimeRoot, session, 5*time.Second, "workspace", "get", stringField(task, "workspace"))
	if err != nil {
		return err
	}
	if workspace == nil {
		if code == "workspace_not_found" {
			ins.resources.Set("workspace", "absent")
		} else {
			ins.block("workspace", fmt.Sprintf("workspace %s cannot be observed (%s)", stringField(task, "workspace"), code))
			ins.resources.Set("workspace", "uncertain")
		}
	} else {
		ws := unwrap(workspace, "workspace")
		ins.resources.Set("workspace", "present")
		checkout := ""
		if wt := asObject(func() any { v, _ := ws.Get("worktree"); return v }()); wt != nil {
			checkout = stringField(wt, "checkout_path")
		}
		if checkout == "" || !samePath(checkout, stringField(task, "worktree")) {
			ins.block("workspace", fmt.Sprintf("workspace %s is not the task checkout (checkout_path %q, expected %s)", stringField(task, "workspace"), checkout, stringField(task, "worktree")))
		}
		panes, code, err := observe(ins.runtimeRoot, session, 5*time.Second, "pane", "list", "--workspace", stringField(task, "workspace"))
		if err != nil {
			return err
		}
		if panes == nil {
			ins.block("panes", fmt.Sprintf("panes of workspace %s cannot be listed (%s)", stringField(task, "workspace"), code))
		} else {
			var listed []any
			if obj := asObject(panes); obj != nil {
				if v, ok := obj.Get("panes"); ok {
					listed = asList(v)
				}
			} else {
				listed = asList(panes)
			}
			var view []any
			reviewerPane := stringField(asObject(func() any { v, _ := task.Get("reviewer"); return v }()), "pane")
			servicePanes := map[string]bool{}
			for _, raw := range asList(func() any { v, _ := ins.environment.Get("services"); return v }()) {
				svc := asObject(raw)
				if p := stringField(svc, "pane"); p != "" {
					servicePanes[p] = true
				}
			}
			for _, raw := range listed {
				p := asObject(raw)
				row := ordjson.NewObject()
				for _, k := range []string{"pane_id", "cwd", "agent", "agent_status"} {
					v, _ := p.Get(k)
					row.Set(k, v)
				}
				view = append(view, row)
				paneID := stringField(p, "pane_id")
				if paneID == stringField(task, "pane") {
					continue
				}
				if paneID == reviewerPane {
					ins.resources.Set("reviewer_in_task_workspace", true)
					continue
				}
				if servicePanes[paneID] {
					continue
				}
				ins.block("panes", fmt.Sprintf("unknown pane %s (cwd %q, agent %q) in the task workspace; a service or helper pane sum did not create blocks removal", paneID, stringField(p, "cwd"), stringField(p, "agent")))
			}
			ins.view.Set("panes", view)
		}
	}
	pane, code, err := observe(ins.runtimeRoot, session, 5*time.Second, "pane", "get", stringField(task, "pane"))
	if err != nil {
		return err
	}
	if pane == nil {
		if code == "pane_not_found" {
			ins.resources.Set("pane", "absent")
		} else {
			ins.block("pane", fmt.Sprintf("pane %s cannot be observed (%s)", stringField(task, "pane"), code))
			ins.resources.Set("pane", "uncertain")
		}
		return nil
	}
	paneObj := unwrap(pane, "pane")
	ins.resources.Set("pane", "present")
	wsID, _ := paneObj.Get("workspace_id")
	if wsID != nil && fmt.Sprint(wsID) != stringField(task, "workspace") {
		ins.block("pane", fmt.Sprintf("pane %s now sits in workspace %v, not %s; possible moved or reused pane", stringField(task, "pane"), wsID, stringField(task, "workspace")))
	}
	cwd := stringField(paneObj, "cwd")
	if cwd == "" {
		cwd = stringField(paneObj, "foreground_cwd")
	}
	if cwd == "" || !samePath(cwd, stringField(task, "worktree")) {
		ins.block("pane", fmt.Sprintf("pane %s runs in %q, not the task checkout; refusing a possibly reused pane", stringField(task, "pane"), cwd))
	}
	return nil
}

func (ins *inspection) git() error {
	task := ins.task
	worktree := stringField(task, "worktree")
	repo := stringField(task, "repository")
	registered, err := worktreePaths(repo)
	if err != nil {
		ins.block("git", fmt.Sprintf("cannot list worktrees of %s: %s", repo, err))
		registered = nil
	}
	branchOut, _ := proc.Run([]string{"git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/" + stringField(task, "branch")}, "", 20*time.Second, false, nil)
	if branchOut.Code == 0 {
		ins.resources.Set("branch", "present")
	} else {
		ins.resources.Set("branch", "absent")
		ins.block("git", fmt.Sprintf("branch %s does not exist in %s; history must survive cleanup, inspect before continuing", stringField(task, "branch"), repo))
	}
	info, statErr := os.Stat(worktree)
	if statErr != nil || !info.IsDir() {
		ins.resources.Set("worktree", "absent")
		if registered != nil {
			resolved, _ := filepath.EvalSymlinks(worktree)
			for _, p := range registered {
				if p == resolved {
					ins.block("git", fmt.Sprintf("%s is gone but still registered as a worktree; inspect `git worktree list` before continuing", worktree))
					break
				}
			}
		}
		return nil
	}
	ins.resources.Set("worktree", "present")
	topOut, err := proc.Run([]string{"git", "-C", worktree, "rev-parse", "--show-toplevel"}, "", 20*time.Second, true, nil)
	if err != nil {
		ins.block("git", fmt.Sprintf("%s is not a readable Git checkout: %s", worktree, err))
		return nil
	}
	commonOut, err := proc.Run([]string{"git", "-C", worktree, "rev-parse", "--path-format=absolute", "--git-common-dir"}, "", 20*time.Second, true, nil)
	if err != nil {
		ins.block("git", fmt.Sprintf("%s is not a readable Git checkout: %s", worktree, err))
		return nil
	}
	branchShow, err := proc.Run([]string{"git", "-C", worktree, "branch", "--show-current"}, "", 20*time.Second, true, nil)
	if err != nil {
		ins.block("git", fmt.Sprintf("%s is not a readable Git checkout: %s", worktree, err))
		return nil
	}
	toplevel := strings.TrimSpace(topOut.Stdout)
	if !samePath(toplevel, worktree) {
		ins.block("git", fmt.Sprintf("%s is not the top level of its checkout (%s)", worktree, toplevel))
	}
	common := filepath.Dir(strings.TrimSpace(commonOut.Stdout))
	if !samePath(common, repo) {
		ins.block("git", fmt.Sprintf("%s does not belong to %s (common dir %s)", worktree, repo, strings.TrimSpace(commonOut.Stdout)))
	}
	if strings.TrimSpace(branchShow.Stdout) != stringField(task, "branch") {
		ins.block("git", fmt.Sprintf("checkout is on %q, not the task branch %q", strings.TrimSpace(branchShow.Stdout), stringField(task, "branch")))
	}
	if registered != nil {
		resolved, _ := filepath.EvalSymlinks(worktree)
		found := false
		for _, p := range registered {
			if p == resolved {
				found = true
				break
			}
		}
		if !found {
			ins.block("git", fmt.Sprintf("%s is not a registered worktree of %s", worktree, repo))
		}
	}
	return nil
}

func (ins *inspection) obligations() string {
	var openIDs []string
	for _, raw := range asList(func() any { v, _ := ins.task.Get("questions"); return v }()) {
		q := asObject(raw)
		if stringField(q, "status") != "applied" {
			openIDs = append(openIDs, stringField(q, "id"))
		}
	}
	if len(openIDs) > 0 {
		ins.block("obligations", fmt.Sprintf("questions not yet answered and applied: %s", openIDs))
	}
	var handoffs []any
	for _, raw := range asList(func() any { v, _ := ins.task.Get("evidence"); return v }()) {
		e := asObject(raw)
		if stringField(e, "kind") == "handoff" && stringField(e, "source") == "worker" {
			if cand, ok := e.Get("candidate"); ok && cand != nil && cand != false {
				handoffs = append(handoffs, e)
			}
		}
	}
	if len(handoffs) == 0 {
		ins.block("handoff", "no structured worker handoff saved; a prose report is not a handoff")
		return ""
	}
	last := asObject(handoffs[len(handoffs)-1])
	return stringField(last, "candidate")
}

func (ins *inspection) github(number int) (*ordjson.Object, error) {
	recorded := asObject(func() any { v, _ := ins.task.Get("pr"); return v }())
	if number == 0 {
		complete := false
		if recorded != nil {
			if v, ok := recorded.Get("complete"); ok && v == true {
				complete = true
			}
		}
		if !complete {
			ins.block("pr", "no complete PR identity recorded; run `pr reconcile TASK --number N` or pass --number")
			return nil, nil
		}
		ident := asObject(func() any { v, _ := recorded.Get("identity"); return v }())
		if ident != nil {
			if n, ok := ident.Get("number"); ok {
				switch t := n.(type) {
				case json.Number:
					i, _ := t.Int64()
					number = int(i)
				case float64:
					number = int(t)
				}
			}
		}
	}
	if recorded != nil {
		if merged, _ := recorded.Get("merged_for_task"); merged == true {
			ident := asObject(func() any { v, _ := recorded.Get("identity"); return v }())
			row := ordjson.NewObject()
			row.Set("number", func() any { v, _ := ident.Get("number"); return v }())
			row.Set("state", func() any { v, _ := recorded.Get("state"); return v }())
			row.Set("head_sha", func() any { v, _ := ident.Get("head_sha"); return v }())
			row.Set("merge_commit", func() any { v, _ := recorded.Get("merge_commit"); return v }())
			row.Set("findings", func() any { v, _ := recorded.Get("findings"); return v }())
			ins.view.Set("pr", row)
			if stringField(recorded, "state") != "merged" {
				ins.block("pr", fmt.Sprintf("PR #%d is %s, not merged with a merge commit; closed or open PRs never justify cleanup", number, stringField(recorded, "state")))
			}
			return recorded, nil
		}
	}
	ins.block("pr", fmt.Sprintf("PR #%d is not merged with a merge commit; closed or open PRs never justify cleanup", number))
	return recorded, nil
}

func (ins *inspection) checkout(mergedHead string) error {
	if asString(func() any { v, _ := ins.resources.Get("worktree"); return v }()) != "present" {
		return nil
	}
	worktree := stringField(ins.task, "worktree")
	if mergedHead != "" {
		head, extra, err := commitsNotCovered(worktree, mergedHead)
		if err != nil {
			return err
		}
		ins.view.Set("head", head)
		if len(extra) > 0 {
			shown := extra
			if len(shown) > 5 {
				shown = shown[:5]
			}
			ins.block("commits", fmt.Sprintf("%d commit(s) in the checkout are not in the merged PR head %s: %v", len(extra), mergedHead, shown))
		}
	}
	artifacts, err := worktreeArtifacts(worktree)
	if err != nil {
		return err
	}
	ins.view.Set("artifacts", artifacts)
	labels := []struct{ key, label string }{
		{"staged", "staged changes"},
		{"tracked_modified", "modified tracked files"},
		{"untracked", "untracked files"},
		{"ignored_preserved", "ignored files that are not known disposable caches"},
	}
	for _, item := range labels {
		list := asList(func() any { v, _ := artifacts.Get(item.key); return v }())
		if len(list) > 0 {
			shown := list
			suffix := ""
			if len(shown) > 10 {
				shown = shown[:10]
				suffix = " ..."
			}
			ins.block("artifacts", fmt.Sprintf("%s: %v%s; move or commit them, sum never runs git clean", item.label, shown, suffix))
		}
	}
	return nil
}

func (ins *inspection) services() error {
	var rows []any
	for _, raw := range asList(func() any { v, _ := ins.environment.Get("services"); return v }()) {
		service := asObject(raw)
		state := stringField(service, "state")
		if state != "failed" && state != "ready" && state != "starting" && state != "running" && state != "active" {
			continue
		}
		view, err := environment.ObserveService(ins.task, service, ins.runtimeRoot)
		if err != nil {
			return err
		}
		row := ordjson.NewObject()
		row.Set("id", stringField(service, "id"))
		row.Set("name", stringField(service, "name"))
		row.Set("state", state)
		row.Set("observed", view)
		rows = append(rows, row)
		paneState := asString(func() any { v, _ := view.Get("pane_state"); return v }())
		if paneState == "none" || paneState == "absent" {
			continue
		}
		if asString(func() any { v, _ := view.Get("ownership"); return v }()) == "owned" {
			ins.stoppable = append(ins.stoppable, stringField(service, "id"))
			if running, _ := view.Get("running"); running == true {
				ins.block("service", fmt.Sprintf("service %s (%s) still runs in pane %s; cleanup --apply stops it gracefully first", stringField(service, "id"), stringField(service, "name"), stringField(service, "pane")))
			} else {
				ins.block("service", fmt.Sprintf("service %s (%s) has exited but its pane %s is still open; cleanup --apply closes it", stringField(service, "id"), stringField(service, "name"), stringField(service, "pane")))
			}
		} else {
			ins.block("service-unknown", fmt.Sprintf("service %s (%s) in pane %s is not the recorded instance; sum stops nothing it cannot prove, inspect the pane", stringField(service, "id"), stringField(service, "name"), stringField(service, "pane")))
		}
	}
	ins.view.Set("services", rows)
	return nil
}

func (ins *inspection) occupancy() error {
	if asString(func() any { v, _ := ins.resources.Get("pane"); return v }()) == "present" {
		worktree := ""
		if asString(func() any { v, _ := ins.resources.Get("worktree"); return v }()) == "present" {
			worktree = stringField(ins.task, "worktree")
		}
		view, err := paneOccupancy(ins.runtimeRoot, stringField(ins.ctx, "session"), stringField(ins.task, "pane"), worktree)
		if err != nil {
			return err
		}
		ins.view.Set("occupancy", view)
		for _, detail := range asList(func() any { v, _ := view.Get("blockers"); return v }()) {
			ins.block("occupant", fmt.Sprint(detail))
		}
	} else if asString(func() any { v, _ := ins.resources.Get("worktree"); return v }()) == "present" {
		inside, errorText := processesIn(stringField(ins.task, "worktree"), map[int]bool{})
		if errorText != "" {
			ins.block("occupant", "processes with a cwd in the checkout cannot be established: "+errorText)
		} else if len(inside) > 0 {
			var parts []string
			for i, raw := range inside {
				if i >= 10 {
					break
				}
				p := asObject(raw)
				parts = append(parts, fmt.Sprintf("pid %v at %v", func() any { v, _ := p.Get("pid"); return v }(), func() any { v, _ := p.Get("cwd"); return v }()))
			}
			ins.block("occupant", "processes still run inside the checkout: "+strings.Join(parts, ", "))
		}
	}
	return nil
}

func (ins *inspection) reviewer() error {
	reviewer := asObject(func() any { v, _ := ins.task.Get("reviewer"); return v }())
	if reviewer == nil {
		ins.resources.Set("reviewer_pane", nil)
		return nil
	}
	row := ordjson.NewObject()
	row.Set("pane", stringField(reviewer, "pane"))
	row.Set("closable", false)
	row.Set("reason", nil)
	ins.view.Set("reviewer", row)
	var findings []any
	for _, raw := range asList(func() any { v, _ := ins.task.Get("evidence"); return v }()) {
		e := asObject(raw)
		if stringField(e, "kind") == "review" {
			findings = append(findings, e)
		}
	}
	if len(findings) == 0 {
		row.Set("reason", "no saved reviewer findings")
		ins.block("reviewer", fmt.Sprintf("reviewer pane %s has no saved findings; it stays open", stringField(reviewer, "pane")))
		return nil
	}
	parent := asObject(func() any { v, _ := ins.task.Get("parent"); return v }())
	if identityEquals(reviewer, ins.task) || identityEquals(reviewer, parent) || identityEquals(reviewer, ins.ctx) {
		row.Set("reason", "reviewer endpoint is the worker or coordinator pane")
		ins.block("reviewer", "reviewer endpoint equals the worker or coordinator pane; refusing")
		return nil
	}
	if stringField(reviewer, "machine") != ins.hostname() || stringField(reviewer, "session") != stringField(ins.ctx, "session") {
		row.Set("reason", "reviewer pane is in another session or machine")
		ins.block("reviewer", fmt.Sprintf("reviewer pane %s is in session %s on %s; not observable from here", stringField(reviewer, "pane"), stringField(reviewer, "session"), stringField(reviewer, "machine")))
		return nil
	}
	pane, code, err := observe(ins.runtimeRoot, stringField(ins.ctx, "session"), 5*time.Second, "pane", "get", stringField(reviewer, "pane"))
	if err != nil {
		return err
	}
	if pane == nil {
		if code == "pane_not_found" {
			ins.resources.Set("reviewer_pane", "absent")
			row.Set("closable", false)
			row.Set("reason", "already absent")
		} else {
			ins.block("reviewer", fmt.Sprintf("reviewer pane %s cannot be observed (%s)", stringField(reviewer, "pane"), code))
		}
		return nil
	}
	ins.resources.Set("reviewer_pane", "present")
	view, err := paneOccupancy(ins.runtimeRoot, stringField(ins.ctx, "session"), stringField(reviewer, "pane"), "")
	if err != nil {
		return err
	}
	row.Set("occupancy", view)
	if list := asList(func() any { v, _ := view.Get("blockers"); return v }()); len(list) > 0 {
		row.Set("reason", "reviewer occupant has not exited")
		for _, detail := range list {
			ins.block("reviewer", fmt.Sprint(detail))
		}
		return nil
	}
	row.Set("closable", true)
	return nil
}

func (ins *inspection) plan() *ordjson.Object {
	state := "ready"
	if len(ins.blockers) > 0 {
		state = "blocked"
	}
	result := ordjson.NewObject()
	for _, k := range ins.view.Keys() {
		v, _ := ins.view.Get(k)
		result.Set(k, v)
	}
	result.Set("state", state)
	result.Set("blockers", ins.blockers)
	result.Set("resources", ins.resources)
	result.Set("stoppable", ins.stoppable)
	return result
}

func inspectTask(s *store.Store, task, ctx *ordjson.Object, runtimeRoot string, number int, scope string) (*ordjson.Object, error) {
	ins := newInspection(s, task, ctx, runtimeRoot)
	ins.identity()
	if len(ins.blockers) > 0 {
		return ins.plan(), nil
	}
	if scope == "reviewer" {
		if err := ins.reviewer(); err != nil {
			return nil, err
		}
		return ins.plan(), nil
	}
	if err := repair.RefuseActive(task, false); err != nil {
		ins.block("execution", err.Error())
	} else {
		execVal, execErr := reservations.GetExecution(task)
		if execErr != nil {
			ins.block("execution", "malformed execution reservation: "+execErr.Error())
		} else if execVal == nil {
			ins.block("execution", "legacy task has no exact execution owner; adopt it through explicit stop inspection before cleanup")
		} else {
			attempts := executionStopProof(ins, execVal)
			row := ordjson.NewObject()
			row.Set("attempts", attempts)
			ins.view.Set("execution", row)
			if asString(func() any { v, _ := execVal.Worker.Get("state"); return v }()) == "starting" {
				ins.block("execution", "worker launch is in progress; its checkout cannot be removed")
			}
		}
	}
	if err := ins.herdr(); err != nil {
		return nil, err
	}
	if err := ins.git(); err != nil {
		return nil, err
	}
	ins.obligations()
	pr, err := ins.github(number)
	if err != nil {
		return nil, err
	}
	mergedHead := ""
	if pr != nil {
		ident := asObject(func() any { v, _ := pr.Get("identity"); return v }())
		mergedHead = stringField(ident, "head_sha")
	}
	if err := ins.checkout(mergedHead); err != nil {
		return nil, err
	}
	if err := ins.services(); err != nil {
		return nil, err
	}
	if err := ins.occupancy(); err != nil {
		return nil, err
	}
	if err := ins.reviewer(); err != nil {
		return nil, err
	}
	return ins.plan(), nil
}

func executionStopProof(ins *inspection, execVal *reservations.Execution) []any {
	var rows []any
	add := func(row *ordjson.Object) {
		proof := execution.ObserveStop(ins.store, ins.runtimeRoot, ins.task, row)
		item := ordjson.NewObject()
		item.Set("id", func() any { v, _ := row.Get("id"); return v }())
		item.Set("generation", func() any { v, _ := row.Get("generation"); return v }())
		item.Set("outcome", proof.Outcome)
		if proof.Reason != "" {
			item.Set("reason", proof.Reason)
		}
		rows = append(rows, item)
		if proof.Outcome != execution.OutcomeStopped {
			ins.block("execution", proof.Reason)
		}
	}
	add(execVal.Worker)
	for _, v := range execVal.Verifiers {
		add(v)
	}
	return rows
}
