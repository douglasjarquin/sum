package cleanup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"context"
	"github.com/douglasjarquin/sum/go/internal/ask"
	"github.com/douglasjarquin/sum/go/internal/bindcmd"
	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/evidenceview"
	"github.com/douglasjarquin/sum/go/internal/execution"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pipeline"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const ignoredPathLimit = 50

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

func worktreeArtifacts(worktree string) (*ordjson.Object, error) {
	out, err := proc.Run([]string{"git", "-C", worktree, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=matching"}, "", 30*time.Second, true, nil)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	tracked := []any{}
	staged := []any{}
	untracked := []any{}
	ignored := []any{}
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
			ignored = append(ignored, path)
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
	shownIgnored := ignored
	if len(shownIgnored) > ignoredPathLimit {
		shownIgnored = shownIgnored[:ignoredPathLimit]
	}
	result.Set("ignored_paths", shownIgnored)
	result.Set("ignored_count", json.Number(fmt.Sprint(len(ignored))))
	result.Set("ignored_omitted", json.Number(fmt.Sprint(len(ignored)-len(shownIgnored))))
	return result, nil
}

func commitsNotCovered(worktree, mergedHead, baseRef string) (string, string, error) {
	headOut, err := proc.Run([]string{"git", "-C", worktree, "rev-parse", "HEAD"}, "", 20*time.Second, true, nil)
	if err != nil {
		return "", "", err
	}
	head := strings.TrimSpace(headOut.Stdout)
	if head == mergedHead {
		return head, "", nil
	}
	var delivered []string
	for _, ref := range []string{mergedHead, baseRef} {
		if ref == "" {
			continue
		}
		known, _ := proc.Run([]string{"git", "-C", worktree, "cat-file", "-e", ref + "^{commit}"}, "", 20*time.Second, false, nil)
		if known.Code == 0 {
			delivered = append(delivered, ref)
		}
	}
	if len(delivered) == 0 {
		return head, fmt.Sprintf("checkout HEAD %s differs from the merged PR head %s, which is not a local object", head, mergedHead), nil
	}
	args := []string{"git", "-C", worktree, "rev-list", "HEAD", "--not"}
	args = append(args, delivered...)
	listOut, err := proc.Run(args, "", 20*time.Second, true, nil)
	if err != nil {
		return head, "", err
	}
	var extra []string
	for _, line := range strings.Split(strings.TrimSpace(listOut.Stdout), "\n") {
		if line != "" {
			extra = append(extra, line)
		}
	}
	if len(extra) == 0 {
		return head, "", nil
	}
	shown := extra
	if len(shown) > 5 {
		shown = shown[:5]
	}
	cover := "the merged PR head " + mergedHead
	if baseRef != "" {
		cover += " or the recorded base branch on the remote (" + baseRef + ")"
	}
	return head, fmt.Sprintf("%d commit(s) in the checkout are not covered by %s: %v", len(extra), cover, shown), nil
}

func processesBoundTo(worktree string, exclude map[int]bool) ([]any, string) {
	found, err := proc.ProcessesBoundTo(worktree, exclude)
	if err != nil {
		return nil, err.Error()
	}
	var bound []any
	for _, row := range found {
		item := ordjson.NewObject()
		item.Set("pid", json.Number(fmt.Sprint(row.PID)))
		item.Set("cwd", row.CWD)
		item.Set("bound", row.Bound)
		bound = append(bound, item)
	}
	return bound, ""
}

// boundDetail describes one bound process in a blocker: `pid N at <cwd>` for a
// process inside the checkout, or `pid N bound to the checkout by argv` for a
// detached daemon (like `serve --mcp --path <checkout>`) running elsewhere.
func boundDetail(raw any) string {
	row := asObject(raw)
	pid := func() any { v, _ := row.Get("pid"); return v }()
	cwd := func() any { v, _ := row.Get("cwd"); return v }()
	if asString(func() any { v, _ := row.Get("bound"); return v }()) == "argv" {
		return fmt.Sprintf("pid %v bound to the checkout by argv (cwd %v)", pid, cwd)
	}
	return fmt.Sprintf("pid %v at %v", pid, cwd)
}

type inspection struct {
	store             *store.Store
	task              *ordjson.Object
	ctx               *ordjson.Object
	runtimeRoot       string
	blockers          []any
	resources         *ordjson.Object
	view              *ordjson.Object
	stoppable         []any
	environment       *ordjson.Object
	orphans           []any
	orphanPIDs        map[int]bool
	descendants       map[int]bool
	descendantsFailed bool
	workerStopped     bool
	settleable        []any
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

// workerTerminalGone distinguishes native absence from a status update.
func (ins *inspection) workerTerminalGone() bool {
	return stringField(ins.resources, "worker_terminal") == "absent"
}

// intField reads a pid-sized json.Number field.
func intField(o *ordjson.Object, key string) (int, bool) {
	v, has := o.Get(key)
	if !has {
		return 0, false
	}
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return int(i), err == nil && i > 0
}

// attemptRecordedPIDs returns the process identities an attempt record claims:
// the operation/observer pids and the occupant's pid and shell pid.
func attemptRecordedPIDs(attempt *ordjson.Object) []int {
	var pids []int
	for _, key := range []string{"operation_pid", "observer_pid"} {
		if n, ok := intField(attempt, key); ok {
			pids = append(pids, n)
		}
	}
	if occupant := asObject(func() any { v, _ := attempt.Get("occupant"); return v }()); occupant != nil {
		for _, key := range []string{"pid", "shell_pid"} {
			if n, ok := intField(occupant, key); ok {
				pids = append(pids, n)
			}
		}
	}
	return pids
}

// ownedDescendants protects live recorded native programs and descendants.
// Unobservable ownership never authorizes orphan termination.
func (ins *inspection) ownedDescendants() (map[int]bool, bool) {
	if ins.descendants != nil {
		return ins.descendants, true
	}
	if ins.descendantsFailed {
		return nil, false
	}
	owned := map[int]bool{}
	execVal, err := reservations.GetExecution(ins.task)
	if err != nil {
		ins.descendantsFailed = true
		return nil, false
	}
	var roots []int
	for _, key := range []string{"worker_endpoint", "reviewer"} {
		endpoint := store.EndpointFromContext(asObject(func() any { v, _ := ins.task.Get(key); return v }()))
		if endpoint.Validate() != nil {
			continue
		}
		reg, e := ins.store.Registration(endpoint)
		if e != nil {
			ins.descendantsFailed = true
			return nil, false
		}
		raw, _ := ordjson.MarshalCompact(func() any { v, _ := reg.Get("incarnation"); return v }())
		var rec incarnation.NativeEvidence
		if json.Unmarshal(raw, &rec) != nil || !rec.Endpoint.SameAuthority(endpoint.Native()) {
			ins.descendantsFailed = true
			return nil, false
		}
		roots = append(roots, int(rec.PID))
		if rec.Foreground != nil {
			roots = append(roots, int(rec.Foreground.PID))
		}
	}

	for pid := range map[int]bool{} {
		roots = append(roots, pid)
	}
	if execVal != nil {
		for _, attempt := range append([]*ordjson.Object{execVal.Worker}, execVal.Verifiers...) {
			roots = append(roots, attemptRecordedPIDs(attempt)...)
		}
	}
	for _, pid := range roots {
		alive, e := proc.PIDRunning(pid)
		if e != nil {
			ins.descendantsFailed = true
			return nil, false
		}
		if alive {
			owned[pid] = true
		}
		kids, err := proc.Descendants(pid)
		if err != nil {
			ins.descendantsFailed = true
			return nil, false
		}
		for _, k := range kids {
			owned[k] = true
		}
	}
	ins.descendants = owned
	return owned, true
}

// addOrphans records surviving writers only after their native owner is gone
// and every other recorded owner and descendant is excluded from orphan status.
// They remain named blockers until explicit cleanup stops and reobserves them.
func (ins *inspection) addOrphans(inside []any) {
	if ins.orphanPIDs == nil {
		ins.orphanPIDs = map[int]bool{}
	}
	owned, proven := ins.ownedDescendants()
	for _, raw := range inside {
		row := asObject(raw)
		if row == nil {
			continue
		}
		pid, _ := row.Get("pid")
		n, _ := pid.(json.Number)
		i, _ := n.Int64()
		if ins.orphanPIDs[int(i)] || !proven || owned[int(i)] {
			continue
		}
		ins.orphanPIDs[int(i)] = true
		ins.orphans = append(ins.orphans, raw)
	}
}

// host is this machine's identity. When it cannot be resolved the zero
// Identity names no recorded machine, so every machine check fails closed.
func (ins *inspection) host() machine.Identity {
	h, _ := ins.store.Machine()
	return h
}

func taskWorker(task *ordjson.Object) store.Endpoint {
	return store.EndpointFromContext(asObject(func() any { v, _ := task.Get("worker_endpoint"); return v }()))
}
func (ins *inspection) identity() {
	task := ins.task
	for _, key := range []string{"worktree", "branch", "repository"} {
		if stringField(task, key) == "" {
			ins.block("identity", "task record has no "+key)
		}
	}
	endpoint := taskWorker(task)
	if err := endpoint.Validate(); err != nil {
		ins.block("identity", err.Error())
	}
	host := ins.host()
	if !host.Is(endpoint.Machine) {
		ins.block("identity", "task belongs to another machine")
	}
	caller := store.EndpointFromContext(ins.ctx)
	if endpoint.Native().SameTerminal(caller.Native()) {
		ins.block("identity", "task endpoint is the calling coordinator")
	}
	if endpoint.CoreID != caller.CoreID || endpoint.Socket != caller.Socket {
		ins.block("identity", "task uses a different native core route")
	}
	worktree, repo := stringField(task, "worktree"), stringField(task, "repository")
	if samePath(worktree, repo) || strings.HasPrefix(worktree, repo+string(os.PathSeparator)) || strings.HasPrefix(repo, worktree+string(os.PathSeparator)) {
		ins.block("identity", "task worktree overlaps repository")
	}
}
func nativeTerminal(endpoint store.Endpoint) (hcclient.Terminal, error) {
	if err := endpoint.Validate(); err != nil {
		return hcclient.Terminal{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID}
	t, err := client.Get(ctx, endpoint.Address)
	if err == nil && (t.Address != endpoint.Address || t.CreatedMS != endpoint.CreatedMS || t.Keep) {
		return t, fmt.Errorf("native terminal creation or lifetime changed")
	}
	return t, err
}
func nativeAbsent(err error) bool {
	var e *hcclient.RPCError
	if !errors.As(err, &e) {
		return false
	}
	var d struct {
		Kind string `json:"kind"`
	}
	return json.Unmarshal(e.Data, &d) == nil && d.Kind == "not_found"
}
func (ins *inspection) native() error {

	t, err := nativeTerminal(taskWorker(ins.task))
	if err != nil {
		if nativeAbsent(err) {
			ins.resources.Set("worker_terminal", "absent")
		} else {
			ins.resources.Set("worker_terminal", "uncertain")
			ins.block("terminal", err.Error())
			return nil
		}
	} else {
		ins.resources.Set("worker_terminal", "present")
	}

	ins.resources.Set("terminal_state", t.State)
	if err == nil && t.Cwd != stringField(ins.task, "worktree") {
		ins.block("terminal", "native terminal cwd differs from task checkout")
	}
	if t.State == "live" {
		ins.block("occupant", "native worker is live; idle or Done status is not exit")
	} else {
		proof := execution.NativeProgramStopped(ins.store, taskWorker(ins.task))
		if proof.Outcome != execution.OutcomeStopped {
			ins.block("terminal", proof.Reason)
		}
	}
	// List catches unrecorded native launches from an interrupted intent, without adopting by label.
	endpoint := taskWorker(ins.task)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID}
	listed, err := client.List(ctx, hcclient.ListParams{All: true})
	if err != nil {
		ins.block("terminals", err.Error())
		return nil
	}
	known := map[string]bool{endpoint.Address: true}
	reviewer := store.EndpointFromContext(asObject(func() any { v, _ := ins.task.Get("reviewer"); return v }()))
	if reviewer.CoreID == endpoint.CoreID {
		known[reviewer.Address] = true
	}
	for _, raw := range asList(func() any { v, _ := ins.environment.Get("services"); return v }()) {
		service := asObject(raw)
		e := store.EndpointFromContext(asObject(func() any { v, _ := service.Get("endpoint"); return v }()))
		if e.CoreID == endpoint.CoreID {
			known[e.Address] = true
		}
	}
	for _, t := range listed.Terminals {
		if !known[t.Address] && (t.Cwd == stringField(ins.task, "worktree") || t.Labels["sum.task"] == stringField(ins.task, "id")) {
			ins.block("terminals", fmt.Sprintf("unrecorded native terminal %s requires inspection", t.Address))
		}
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
			resolved := worktree
			if target, e := filepath.EvalSymlinks(worktree); e == nil {
				resolved = target
			}
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
		resolved := worktree
		if target, e := filepath.EvalSymlinks(worktree); e == nil {
			resolved = target
		}
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
	if err := RefusePendingEffects(ins.store, stringField(ins.task, "id")); err != nil {
		ins.block("pending-effect", err.Error())
	}
	obligations, err := returns.OpenObligations(ins.store, ins.task)
	if err != nil {
		ins.block("obligations", err.Error())
	}
	for _, o := range obligations {
		if stringField(o, "kind") == "refresh" {
			ins.block("obligations", "procedure refresh remains pending")
		}
	}
	var openIDs []string
	for _, raw := range asList(func() any { v, _ := ins.task.Get("questions"); return v }()) {
		q := asObject(raw)
		status := stringField(q, "status")
		switch {
		case ask.Discharged(status):
			continue
		case status == "answered":
			// A worker-only `resolve` can never run once every attempt that could
			// consume the answer has verified stop evidence. The recorded answer
			// then settles durably during apply instead of blocking archive forever.
			if ins.workerStopped {
				ins.settleable = append(ins.settleable, stringField(q, "id"))
				continue
			}
			openIDs = append(openIDs, stringField(q, "id"))
		default:
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
			row.Set("notes", func() any { v, _ := recorded.Get("notes"); return v }())
			ins.view.Set("pr", row)
			if stringField(recorded, "state") != "merged" {
				ins.block("pr", fmt.Sprintf("PR #%d is %s, not merged with a merge commit; closed or open PRs never justify cleanup", number, stringField(recorded, "state")))
			}
			return recorded, nil
		}
		if stringField(recorded, "state") == "merged" {
			if findings := asList(func() any { v, _ := recorded.Get("findings"); return v }()); len(findings) > 0 {
				ins.block("pr", fmt.Sprintf("PR #%d is merged but its observation carries findings: %v; resolve each through the action it names (a coordinator `verify --result pass` on the merged head explains a post-review push; `pr reconcile --accept-base-branch` records an accepted base), then reconcile again", number, findings))
			} else {
				ins.block("pr", fmt.Sprintf("PR #%d is merged but the observation recorded no merge commit; reconcile again to re-observe it", number))
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
		baseRef := ""
		if base, _ := pipeline.ResolveBase(stringField(ins.task, "repository"), ins.task); base != "" {
			baseRef = "refs/remotes/origin/" + base
		}
		head, detail, err := commitsNotCovered(worktree, mergedHead, baseRef)
		if err != nil {
			return err
		}
		ins.view.Set("head", head)
		if detail != "" {
			ins.block("commits", detail)
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
		if state != "failed" && state != "ready" && state != "starting" && state != "running" && state != "active" && state != "unknown" && state != "stopping" && state != "intended" {
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
		terminalState := asString(func() any { v, _ := view.Get("terminal_state"); return v }())
		if terminalState == "none" || terminalState == "absent" {
			continue
		}
		if asString(func() any { v, _ := view.Get("ownership"); return v }()) == "owned" {
			ins.stoppable = append(ins.stoppable, stringField(service, "id"))
			if running, _ := view.Get("running"); running == true {
				ins.block("service", fmt.Sprintf("service %s (%s) still runs in terminal %s; run env stop before cleanup", stringField(service, "id"), stringField(service, "name"), store.EndpointFromContext(asObject(func() any { v, _ := service.Get("endpoint"); return v }())).Address))
			}
		} else {
			ins.block("service-unknown", fmt.Sprintf("service %s (%s) terminal %s has uncertain ownership; inspect it before cleanup", stringField(service, "id"), stringField(service, "name"), store.EndpointFromContext(asObject(func() any { v, _ := service.Get("endpoint"); return v }())).Address))
		}
	}
	ins.view.Set("services", rows)
	return nil
}

func (ins *inspection) occupancy() error {
	if stringField(ins.resources, "worktree") != "present" {
		return nil
	}
	inside, errorText := processesBoundTo(stringField(ins.task, "worktree"), nil)
	if errorText != "" {
		ins.block("occupant", "process inspection failed: "+errorText)
	} else if len(inside) > 0 {
		if ins.workerTerminalGone() {
			ins.addOrphans(inside)
		}
		var parts []string
		for _, raw := range inside {
			parts = append(parts, boundDetail(raw))
		}
		ins.block("occupant", "surviving checkout writers: "+strings.Join(parts, ", "))
	}
	return nil
}
func (ins *inspection) reviewer() error {
	reviewer := asObject(func() any { v, _ := ins.task.Get("reviewer"); return v }())
	if reviewer == nil {
		ins.resources.Set("reviewer_terminal", nil)
		return nil
	}
	endpoint := store.EndpointFromContext(reviewer)
	row := ordjson.NewObject()
	row.Set("endpoint", reviewer)
	row.Set("closable", false)
	ins.view.Set("reviewer", row)
	if endpoint.Native().SameTerminal(taskWorker(ins.task).Native()) || endpoint.Native().SameTerminal(store.EndpointFromContext(ins.ctx).Native()) {
		ins.block("reviewer", "reviewer equals worker or coordinator")
		return nil
	}
	found := false
	candidate := evidenceview.CurrentCandidate(ins.task)
	for _, raw := range asList(func() any { v, _ := ins.task.Get("evidence"); return v }()) {
		e := asObject(raw)
		ep := store.EndpointFromContext(asObject(func() any { v, _ := e.Get("endpoint"); return v }()))
		if stringField(e, "kind") == "review" && stringField(e, "verdict") != "" && candidate != "" && stringField(e, "candidate") == candidate && ep.Native().SameAuthority(endpoint.Native()) {
			found = true
		}
	}
	if !found {
		ins.block("reviewer", "no findings for current reviewer endpoint")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := bindcmd.ReviewerNativeStopped(ctx, ins.store, ins.task, reviewer); err != nil {
		ins.block("reviewer", err.Error())
		return nil
	}
	t, err := nativeTerminal(endpoint)
	if nativeAbsent(err) {
		ins.resources.Set("reviewer_terminal", "absent")
	} else if err != nil {
		ins.block("reviewer", err.Error())
		return nil
	} else {
		ins.resources.Set("reviewer_terminal", "present")
		if t.State != "finished" {
			ins.block("reviewer", "reviewer is not finished")
			return nil
		}
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
	orphans := ins.orphans
	if orphans == nil {
		orphans = []any{}
	}
	result.Set("orphans", orphans)
	settleable := ins.settleable
	if settleable == nil {
		settleable = []any{}
	}
	result.Set("settled_questions", settleable)
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
	if err := ins.native(); err != nil {
		return nil, err
	}
	if err := ins.git(); err != nil {
		return nil, err
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
		isWorker := stringField(row, "kind") == "worker"
		if isWorker && (proof.Outcome == execution.OutcomeStopped || len(proof.Orphans) > 0) {
			// The worker terminal is verified absent; surviving checkout processes are
			// orphans, not a consuming worker. No live attempt can apply an answer.
			ins.workerStopped = true
		}
		if proof.Outcome != execution.OutcomeStopped {
			if isWorker && len(proof.Orphans) > 0 && ins.workerTerminalGone() {
				var inside []any
				for _, p := range proof.Orphans {
					item := ordjson.NewObject()
					item.Set("pid", json.Number(fmt.Sprint(p.PID)))
					item.Set("cwd", p.CWD)
					item.Set("bound", p.Bound)
					inside = append(inside, item)
				}
				ins.addOrphans(inside)
			}
			ins.block("execution", proof.Reason)
		}
	}
	add(execVal.Worker)
	for _, v := range execVal.Verifiers {
		add(v)
	}
	return rows
}
