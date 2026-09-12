package prepare

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/brief"
	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/graph"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

const MaxText = 256 * 1024

func jsonInt(n int) json.Number { return json.Number(fmt.Sprint(n)) }

func asString(v any) string { s, _ := v.(string); return s }

func asObject(v any) *ordjson.Object { obj, _ := v.(*ordjson.Object); return obj }

func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func resolvePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func newTaskID() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "t-" + hex.EncodeToString(buf), nil
}

type Args struct {
	Repo        string
	Project     string
	Brief       string
	Harness     string
	Model       string
	Reasoning   string
	SameAsYou   bool
	Preset      string
	PresetSet   bool
	Base        string
	Kind        string
	Approved    bool
	Extra       []string
	RuntimeRoot string
	SumctlPath  string
}

func Prepare(s *store.Store, ctx *ordjson.Object, args Args) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	herdrPath, err := toolpath.Find(args.RuntimeRoot, "herdr")
	if err != nil {
		return nil, err
	}
	if _, err := herdrclient.EnsureVersion(herdrPath, contract.HerdrCLI); err != nil {
		return nil, err
	}
	if args.Repo == "" && args.Project == "" {
		return nil, fmt.Errorf("Give --repo PATH or --project NAME.")
	}
	if args.Repo != "" && args.Project != "" {
		return nil, fmt.Errorf("Give either --repo PATH or --project NAME, not both.")
	}
	repoArg := args.Repo
	if args.Project != "" {
		return nil, fmt.Errorf("No enrolled project %q; `project list` shows the registry and `project enroll owner/repo` adds exactly one repository.", args.Project)
	}
	repo, err := runGit("-C", repoArg, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	repo = resolvePath(repo)
	if args.Base == "" {
		args.Base = "HEAD"
	}
	if args.Kind == "" {
		args.Kind = "ship"
	}
	baseSHA, err := runGit("-C", repo, "rev-parse", "--verify", args.Base+"^{commit}", "--")
	if err != nil {
		return nil, err
	}
	briefText, err := os.ReadFile(args.Brief)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(briefText)) == "" || len(briefText) > MaxText {
		return nil, fmt.Errorf("Provide a nonempty, bounded task brief.")
	}
	if !args.Approved {
		return nil, fmt.Errorf("Dispatch requires --approved: record explicit user-approved work, not a self-generated backlog item.")
	}
	launchSpec, err := launch.Resolve(s, ctx, launch.ResolveArgs{
		Harness:     args.Harness,
		Model:       args.Model,
		Reasoning:   args.Reasoning,
		SameAsRoot:  args.SameAsYou,
		Extra:       args.Extra,
		Preset:      args.Preset,
		PresetSet:   args.PresetSet,
		RuntimeRoot: args.RuntimeRoot,
	})
	if err != nil {
		return nil, err
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	tasks, err := s.AllTasks()
	if err != nil {
		unlock()
		return nil, err
	}
	admission, err := launch.Admit(s, tasks, repo)
	if err != nil {
		unlock()
		return nil, err
	}
	tid, err := newTaskID()
	if err != nil {
		unlock()
		return nil, err
	}
	host, err := os.Hostname()
	if err != nil {
		unlock()
		return nil, err
	}
	session := asString(func() any { v, _ := ctx.Get("session"); return v }())
	owner := ordjson.NewObject()
	owner.Set("machine", host)
	owner.Set("session", session)
	owner.Set("pane", nil)
	attempt, err := reservations.NewAttempt("worker", owner, nil, store.Now(), "", nil)
	if err != nil {
		unlock()
		return nil, err
	}
	task := ordjson.NewObject()
	task.Set("schema", jsonInt(store.Schema))
	task.Set("id", tid)
	task.Set("created_at", store.Now())
	task.Set("status", "preparing")
	task.Set("machine", host)
	task.Set("repository", repo)
	task.Set("project", nil)
	task.Set("base_sha", baseSHA)
	task.Set("branch", "sum/"+tid)
	harness, _ := launchSpec.Get("harness")
	task.Set("harness", harness)
	task.Set("launch", launchSpec)
	task.Set("kind", args.Kind)
	task.Set("brief", string(briefText))
	task.Set("parent", ctx)
	task.Set("session", session)
	task.Set("pane", nil)
	task.Set("workspace", nil)
	task.Set("worktree", nil)
	task.Set("questions", []any{})
	task.Set("report", nil)
	task.Set("evidence", []any{})
	task.Set("reviewer", nil)
	task.Set("pr", nil)
	task.Set("notice", nil)
	task.Set("error", nil)
	task.Set("admission", admission)
	task.Set("execution", reservations.NewExecution(attempt))
	if err := s.SaveTask(task); err != nil {
		unlock()
		return nil, err
	}
	unlock()

	branch := "sum/" + tid
	created, err := herdrclient.Call(herdrPath, session, 30*time.Second, "worktree", "create", "--cwd", repo, "--branch", branch, "--base", baseSHA, "--label", "sum-"+tid, "--no-focus")
	if err != nil {
		return failPrepare(s, tid, err)
	}
	createdObj := asObject(created)
	rootPane := asObject(func() any { v, _ := createdObj.Get("root_pane"); return v }())
	workspace := asObject(func() any { v, _ := createdObj.Get("workspace"); return v }())
	worktreeObj := asObject(func() any { v, _ := createdObj.Get("worktree"); return v }())
	if worktreeObj == nil && workspace != nil {
		worktreeObj = asObject(func() any { v, _ := workspace.Get("worktree"); return v }())
	}
	paneID := asString(func() any { v, _ := rootPane.Get("pane_id"); return v }())
	workspaceID := asString(func() any { v, _ := workspace.Get("workspace_id"); return v }())
	worktreePath := asString(func() any { v, _ := worktreeObj.Get("path"); return v }())
	worktreePath = resolvePath(worktreePath)
	task.Set("pane", paneID)
	task.Set("workspace", workspaceID)
	task.Set("worktree", worktreePath)
	actualRoot, err := runGit("-C", worktreePath, "rev-parse", "--show-toplevel")
	if err != nil {
		return failPrepare(s, tid, err)
	}
	actualRoot = resolvePath(actualRoot)
	actualHead, err := runGit("-C", worktreePath, "rev-parse", "HEAD")
	if err != nil {
		return failPrepare(s, tid, err)
	}
	actualBranch, err := runGit("-C", worktreePath, "branch", "--show-current")
	if err != nil {
		return failPrepare(s, tid, err)
	}
	if actualRoot == repo || actualRoot != worktreePath || actualHead != baseSHA || actualBranch != branch {
		return failPrepare(s, tid, fmt.Errorf("Herdr returned a checkout that does not match the task. Work is preserved; inspect it manually."))
	}
	record := graph.InitCheckout(s, args.RuntimeRoot, worktreePath, "task", nil)
	if err := graph.WriteTaskGraph(s, task, record); err != nil {
		return failPrepare(s, tid, err)
	}
	briefPath, err := brief.WriteInitial(s, args.RuntimeRoot, args.SumctlPath, task)
	if err != nil {
		return failPrepare(s, tid, err)
	}
	task.Set("brief_path", briefPath)
	task.Set("status", "prepared")
	unlock, err = s.Lock()
	if err != nil {
		return failPrepare(s, tid, err)
	}
	current, err := s.ReadTask(tid)
	if err != nil {
		unlock()
		return failPrepare(s, tid, err)
	}
	for _, key := range []string{"pane", "workspace", "worktree", "verification_policy", "brief_path", "status", "graph"} {
		v, _ := task.Get(key)
		current.Set(key, v)
	}
	worker, err := reservations.Worker(current)
	if err != nil {
		unlock()
		return failPrepare(s, tid, err)
	}
	worker.Set("checkout", worktreePath)
	if err := s.SaveTask(current); err != nil {
		unlock()
		return failPrepare(s, tid, err)
	}
	unlock()
	result := ordjson.NewObject()
	for _, key := range current.Keys() {
		v, _ := current.Get(key)
		result.Set(key, v)
	}
	result.Set("confirmation", launch.Confirmation(launchSpec))
	return result, nil
}

func failPrepare(s *store.Store, tid string, cause error) (*ordjson.Object, error) {
	errorText := fmt.Sprintf("Prepare failed or became uncertain: %s. Do not blindly create a replacement; inspect Herdr first.", cause)
	unlock, err := s.Lock()
	if err == nil {
		defer unlock()
		current, readErr := s.ReadTask(tid)
		if readErr == nil {
			current.Set("status", "needs-attention")
			current.Set("error", errorText)
			if worker, werr := reservations.Worker(current); werr == nil {
				id, _ := worker.Get("id")
				obs := ordjson.NewObject()
				obs.Set("at", store.Now())
				obs.Set("outcome", "prepare-uncertain")
				reason := cause.Error()
				if len(reason) > 500 {
					reason = reason[:500]
				}
				obs.Set("reason", reason)
				_, _ = reservations.Transition(current, fmt.Sprint(id), "uncertain", store.Now(), obs, nil)
			}
			_ = s.SaveTask(current)
		}
	}
	return nil, fmt.Errorf("%s: %s", tid, errorText)
}

func Start(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID string, extra []string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		return nil, err
	}
	if _, err := herdrclient.EnsureVersion(herdrPath, contract.HerdrCLI); err != nil {
		return nil, err
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		unlock()
		return nil, err
	}
	if err := s.CheckMachine(task); err != nil {
		unlock()
		return nil, err
	}
	if err := repair.RefuseDuringCleanup(task, "Worker start"); err != nil {
		unlock()
		return nil, err
	}
	status, _ := task.Get("status")
	if status != "prepared" {
		unlock()
		return nil, fmt.Errorf("Only a prepared task can be started. sum never retries an uncertain launch automatically.")
	}
	worker, err := reservations.Worker(task)
	if err != nil {
		unlock()
		return nil, err
	}
	state := asString(func() any { v, _ := worker.Get("state"); return v }())
	if state != "held" && state != "running" {
		id, _ := worker.Get("id")
		unlock()
		return nil, fmt.Errorf("Worker execution reservation %v is %s; start is refused.", id, state)
	}
	launchSpec := launch.TaskLaunch(task)
	harness := asString(func() any { v, _ := launchSpec.Get("harness"); return v }())
	argv := []string{}
	for _, a := range func() []any { v, _ := launchSpec.Get("argv"); list, _ := v.([]any); return list }() {
		if s, ok := a.(string); ok {
			argv = append(argv, s)
		}
	}
	argv = append(argv, extra...)
	launchSpec.Set("argv", anyStrings(argv))
	explicit := []any{}
	for _, a := range func() []any { v, _ := launchSpec.Get("explicit_args"); list, _ := v.([]any); return list }() {
		explicit = append(explicit, a)
	}
	for _, a := range extra {
		explicit = append(explicit, a)
	}
	launchSpec.Set("explicit_args", explicit)
	launchSpec.Set("started_argv", anyStrings(argv))
	task.Set("launch", launchSpec)
	task.Set("status", "starting")
	wid, _ := worker.Get("id")
	if _, err := reservations.Transition(task, fmt.Sprint(wid), "starting", store.Now(), nil, nil); err != nil {
		unlock()
		return nil, err
	}
	if err := s.SaveTask(task); err != nil {
		unlock()
		return nil, err
	}
	pane := asString(func() any { v, _ := task.Get("pane"); return v }())
	session := asString(func() any { v, _ := task.Get("session"); return v }())
	unlock()

	startArgs := []string{"agent", "start", taskID, "--kind", harness, "--pane", pane, "--timeout", "30000"}
	if len(argv) > 0 {
		startArgs = append(startArgs, "--")
		startArgs = append(startArgs, argv...)
	}
	started, startErr := herdrclient.Call(herdrPath, session, 40*time.Second, startArgs...)
	if startErr != nil {
		return failStart(s, taskID, startErr)
	}
	startedObj := asObject(started)
	agent := startedObj
	if nested, ok := startedObj.Get("agent"); ok {
		if inner := asObject(nested); inner != nil {
			agent = inner
		}
	}
	observedKind := asString(func() any { v, _ := agent.Get("agent"); return v }())
	briefPath := asString(func() any { v, _ := task.Get("brief_path"); return v }())
	prompt := fmt.Sprintf("You are the sum worker for %s, not the coordinator. Read the complete file %s, then execute only that approved task. Questions and results must be saved using the commands in that brief.", taskID, quoteJSON(briefPath))
	if _, err := herdrclient.Call(herdrPath, session, 10*time.Second, "agent", "prompt", pane, prompt); err != nil {
		return failStart(s, taskID, err)
	}
	unlock, err = s.Lock()
	if err != nil {
		return failStart(s, taskID, err)
	}
	defer unlock()
	current, err := s.ReadTask(taskID)
	if err != nil {
		return failStart(s, taskID, err)
	}
	if st, _ := current.Get("status"); st == "starting" {
		current.Set("status", "running")
	}
	current.Set("started_at", store.Now())
	currentLaunch := launch.TaskLaunch(current)
	observed := ordjson.NewObject()
	observed.Set("harness", observedKind)
	observed.Set("model", "not-exposed")
	if observedKind == harness {
		observed.Set("status", "harness-observed")
	} else {
		observed.Set("status", "harness-mismatch")
	}
	currentLaunch.Set("observed", observed)
	current.Set("launch", currentLaunch)
	currentWorker, err := reservations.Worker(current)
	if err != nil {
		return failStart(s, taskID, err)
	}
	occupant := ordjson.NewObject()
	machine, _ := current.Get("machine")
	occupant.Set("machine", machine)
	occupant.Set("session", session)
	occupant.Set("pane", pane)
	worktree, _ := current.Get("worktree")
	occupant.Set("checkout", worktree)
	occupant.Set("harness", observedKind)
	name, _ := agent.Get("name")
	occupant.Set("name", name)
	occupant.Set("shell_pid", nil)
	occupant.Set("pid", nil)
	occupant.Set("argv", nil)
	currentWorker.Set("occupant", occupant)
	wid, _ = currentWorker.Get("id")
	if _, err := reservations.Transition(current, fmt.Sprint(wid), "uncertain", store.Now(), func() *ordjson.Object {
		obs := ordjson.NewObject()
		obs.Set("at", store.Now())
		obs.Set("outcome", "occupant-uncertain")
		obs.Set("reason", "not exactly one foreground process")
		return obs
	}(), nil); err != nil {
		return failStart(s, taskID, err)
	}
	if err := s.SaveTask(current); err != nil {
		return failStart(s, taskID, err)
	}
	endpoint := store.Endpoint{
		Machine: asString(machine),
		Session: session,
		Pane:    pane,
		Cwd:     asString(worktree),
	}
	if _, err := s.Register(endpoint, "worker", taskID); err != nil {
		return failStart(s, taskID, err)
	}
	result := ordjson.NewObject()
	for _, key := range current.Keys() {
		v, _ := current.Get(key)
		result.Set(key, v)
	}
	result.Set("confirmation", launch.Confirmation(currentLaunch))
	return result, nil
}

func failStart(s *store.Store, taskID string, cause error) (*ordjson.Object, error) {
	errorText := fmt.Sprintf("Launch/prompt uncertain: %s. Inspect the saved pane; do not relaunch. Trust/auth prompts need your action.", cause)
	unlock, err := s.Lock()
	if err == nil {
		defer unlock()
		task, readErr := s.ReadTask(taskID)
		if readErr == nil {
			if st, _ := task.Get("status"); st == "starting" {
				task.Set("status", "needs-attention")
			}
			if worker, werr := reservations.Worker(task); werr == nil {
				if state, _ := worker.Get("state"); state == "starting" {
					id, _ := worker.Get("id")
					obs := ordjson.NewObject()
					obs.Set("at", store.Now())
					obs.Set("outcome", "launch-uncertain")
					reason := cause.Error()
					if len(reason) > 500 {
						reason = reason[:500]
					}
					obs.Set("reason", reason)
					_, _ = reservations.Transition(task, fmt.Sprint(id), "uncertain", store.Now(), obs, nil)
				}
			}
			task.Set("error", errorText)
			_ = s.SaveTask(task)
		}
	}
	return nil, fmt.Errorf("%s: %s", taskID, errorText)
}

func anyStrings(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func Dispatch(s *store.Store, ctx *ordjson.Object, args Args) (*ordjson.Object, error) {
	prepared, err := Prepare(s, ctx, args)
	if err != nil {
		return nil, err
	}
	id := asString(func() any { v, _ := prepared.Get("id"); return v }())
	return Start(s, ctx, args.RuntimeRoot, id, args.Extra)
}
