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
	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/graph"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/project"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
	"github.com/douglasjarquin/sum/go/internal/verifycontract"
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

func resolveGitPath(root, value string) string {
	if !filepath.IsAbs(value) {
		value = filepath.Join(root, value)
	}
	return resolvePath(value)
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
	repoArg, projectObj, err := project.ResolveTaskRepository(s, args.Repo, args.Project)
	if err != nil {
		return nil, err
	}
	repo, err := runGit("-C", repoArg, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	repo = resolvePath(repo)
	if projectObj == nil {
		projectObj = project.ByPath(s, repo)
	}
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
	task.Set("project", projectObj)
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
	contractRoot, cleanupContract, err := verifycontract.MaterializeCommit(repo, baseSHA)
	var policy *ordjson.Object
	if err != nil {
		policy = verifycontract.PolicyAtDispatch(worktreePath, baseSHA, environment.VerificationContractStatusAtDispatch(worktreePath, args.RuntimeRoot))
		policy.Set("snapshot_error", err.Error())
	} else {
		defer cleanupContract()
		policy = verifycontract.PolicyAtDispatch(contractRoot, baseSHA, environment.VerificationContractStatusAtDispatch(contractRoot, args.RuntimeRoot))
	}
	verifycontract.AddDispatchMetadata(policy, verifycontract.DispatchMetadata{
		Repository:  repo,
		Project:     projectObj,
		Launch:      launchSpec,
		RuntimeRoot: args.RuntimeRoot,
		Worktree:    worktreePath,
		GitRoot:     actualRoot,
		Head:        actualHead,
		Branch:      actualBranch,
		Workspace:   workspaceID,
	})
	verifycontract.SealPolicy(policy)
	task.Set("verification_policy", policy)
	if err := s.SaveTask(task); err != nil {
		return failPrepare(s, tid, err)
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
	policy, hasPolicy := task.Get("verification_policy")
	if !hasPolicy || !validVerificationSnapshot(policy, task) {
		unlock()
		return nil, fmt.Errorf("The task has no coordinator-owned verification snapshot; start is refused.")
	}
	if err := verifycontract.ValidatePolicySeal(asObject(policy)); err != nil {
		unlock()
		return nil, fmt.Errorf("The coordinator-owned verification snapshot is not intact: %w", err)
	}
	preparedWorktree := asString(func() any { v, _ := task.Get("worktree"); return v }())
	repository := asString(func() any { v, _ := task.Get("repository"); return v }())
	recordedWorktree := snapshotObject(asObject(policy), "prepared_worktree")
	actualRoot, rootErr := runGit("-C", preparedWorktree, "rev-parse", "--show-toplevel")
	actualHead, headErr := runGit("-C", preparedWorktree, "rev-parse", "HEAD")
	actualBranch, branchErr := runGit("-C", preparedWorktree, "branch", "--show-current")
	actualCommon, commonErr := runGit("-C", preparedWorktree, "rev-parse", "--git-common-dir")
	repositoryCommon, repositoryCommonErr := runGit("-C", repository, "rev-parse", "--git-common-dir")
	if rootErr != nil || headErr != nil || branchErr != nil || commonErr != nil || repositoryCommonErr != nil || recordedWorktree == nil || resolvePath(actualRoot) != resolvePath(preparedWorktree) || resolvePath(actualRoot) != resolvePath(asString(policyField(recordedWorktree, "git_root"))) || resolvePath(preparedWorktree) != resolvePath(asString(policyField(recordedWorktree, "path"))) || actualHead != asString(func() any { v, _ := task.Get("base_sha"); return v }()) || actualHead != asString(policyField(recordedWorktree, "head")) || actualBranch != asString(policyField(recordedWorktree, "branch")) || asString(func() any { v, _ := task.Get("workspace"); return v }()) != asString(policyField(recordedWorktree, "workspace")) || resolveGitPath(preparedWorktree, actualCommon) != resolveGitPath(repository, repositoryCommon) {
		unlock()
		return nil, fmt.Errorf("The prepared checkout does not match its saved Git identity; start is refused.")
	}
	if err := verifycontract.ValidateCommittedPolicy(preparedWorktree, asObject(policy)); err != nil {
		unlock()
		return nil, fmt.Errorf("The coordinator-owned verification snapshot does not match its committed base: %w", err)
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
	info, _, procErr := environment.PaneProcesses(runtimeRoot, session, pane)
	var shellPID, occPID, occArgv any
	captured := false
	if procErr == nil && info != nil {
		if v, ok := info.Get("shell_pid"); ok {
			shellPID = v
		}
		processes, _ := info.Get("processes")
		list, _ := processes.([]any)
		if len(list) == 1 {
			p := asObject(list[0])
			if p != nil {
				occPID, _ = p.Get("pid")
				occArgv, _ = p.Get("argv")
				captured = occPID != nil && occArgv != nil
			}
		}
	}
	occupant.Set("shell_pid", shellPID)
	occupant.Set("pid", occPID)
	occupant.Set("argv", occArgv)
	currentWorker.Set("occupant", occupant)
	wid, _ = currentWorker.Get("id")
	next := "uncertain"
	obs := ordjson.NewObject()
	obs.Set("at", store.Now())
	if captured {
		next = "running"
		obs.Set("outcome", "occupant")
		obs.Set("pid", occPID)
	} else {
		obs.Set("outcome", "occupant-uncertain")
		obs.Set("reason", "not exactly one foreground process")
	}
	if _, err := reservations.Transition(current, fmt.Sprint(wid), next, store.Now(), obs, nil); err != nil {
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

func validVerificationSnapshot(value any, task *ordjson.Object) bool {
	policy := asObject(value)
	if policy == nil {
		return false
	}
	status, _ := policy.Get("status")
	base, _ := policy.Get("base_sha")
	statusText, statusOK := status.(string)
	baseText, baseOK := base.(string)
	if !statusOK || (statusText != "standardized" && statusText != "not-yet-standardized") || !baseOK || strings.TrimSpace(baseText) == "" {
		return false
	}
	taskBase, _ := task.Get("base_sha")
	if taskBaseText, ok := taskBase.(string); !ok || baseText != taskBaseText {
		return false
	}
	if !snapshotString(policy, "contract_path") || !snapshotString(policy, "repository_path") || !snapshotString(policy, "observed_at") {
		return false
	}
	preparedWorktree := snapshotObject(policy, "prepared_worktree")
	if preparedWorktree == nil || !snapshotString(preparedWorktree, "path") || !snapshotString(preparedWorktree, "git_root") || !snapshotString(preparedWorktree, "head") || !snapshotString(preparedWorktree, "branch") || !snapshotString(preparedWorktree, "workspace") {
		return false
	}
	snapshotHash, _ := policy.Get("snapshot_sha256")
	if hash, ok := snapshotHash.(string); !ok || len(hash) != 64 {
		return false
	} else if _, err := hex.DecodeString(hash); err != nil {
		return false
	}
	if !snapshotOptionalString(policy, "contract_sha256") || !snapshotOptionalString(policy, "entrypoint") || !snapshotOptionalString(policy, "task_owner") || !snapshotOptionalString(policy, "feature_maps_index") {
		return false
	}
	if statusText == "standardized" && (!snapshotString(policy, "contract_sha256") || !snapshotString(policy, "entrypoint") || !snapshotString(policy, "task_owner") || !snapshotString(policy, "feature_maps_index")) {
		return false
	}
	if !snapshotStrings(policy, "feature_maps") || !snapshotStrings(policy, "required_checks") || !snapshotStrings(policy, "scenario_ids") || !snapshotStrings(policy, "policy_files") {
		return false
	}
	if !snapshotObjects(policy, "feature_map_hashes", []string{"path", "sha256"}) || !snapshotObjects(policy, "evidence_required", []string{"scenario", "feature", "map"}) {
		return false
	}
	requirements := snapshotObject(policy, "requirements")
	if requirements == nil || !snapshotStrings(requirements, "commands") || !snapshotStrings(requirements, "missing") {
		return false
	}
	freshness := snapshotObject(policy, "freshness")
	if freshness == nil || !snapshotStrings(freshness, "inputs") || !snapshotStrings(freshness, "outputs") || !snapshotPositiveNumber(freshness, "timeout_seconds") {
		return false
	}
	identity := snapshotObject(policy, "project_identity")
	if identity == nil || !snapshotString(identity, "path") {
		return false
	}
	taskRepository, _ := task.Get("repository")
	if repository, ok := taskRepository.(string); !ok || asString(policyField(identity, "path")) != repository || asString(policyField(policy, "repository_path")) != repository {
		return false
	}
	runtime := snapshotObject(policy, "source_runtime")
	if runtime == nil || !snapshotString(runtime, "sum_version") || !snapshotString(runtime, "worker_skill_path") || !snapshotString(runtime, "reviewer_skill_path") {
		return false
	}
	if !snapshotNumber(runtime, "brief_schema") || !snapshotOptionalString(runtime, "runtime_revision") || !snapshotString(runtime, "worker_skill_sha256") || !snapshotString(runtime, "reviewer_skill_sha256") {
		return false
	}
	rubric := snapshotObject(runtime, "rubric")
	if rubric == nil || !snapshotString(rubric, "path") || !snapshotString(rubric, "sha256") {
		return false
	}
	delivery := snapshotObject(policy, "delivery")
	tool := snapshotObject(delivery, "tool")
	if delivery == nil || !snapshotString(delivery, "mode") || tool == nil || !snapshotString(tool, "harness") || snapshotObject(tool, "source") == nil {
		return false
	}
	return true
}

func snapshotObject(value *ordjson.Object, key string) *ordjson.Object {
	if value == nil {
		return nil
	}
	field, _ := value.Get(key)
	return asObject(field)
}

func policyField(value *ordjson.Object, key string) any {
	if value == nil {
		return nil
	}
	field, _ := value.Get(key)
	return field
}

func snapshotString(value *ordjson.Object, key string) bool {
	if value == nil {
		return false
	}
	field, ok := value.Get(key)
	text, textOK := field.(string)
	return ok && textOK && strings.TrimSpace(text) != ""
}

func snapshotStrings(value *ordjson.Object, key string) bool {
	if value == nil {
		return false
	}
	field, ok := value.Get(key)
	values, listOK := field.([]any)
	if !ok || !listOK {
		return false
	}
	for _, item := range values {
		if text, textOK := item.(string); !textOK || strings.TrimSpace(text) == "" {
			return false
		}
	}
	return true
}

func snapshotObjects(value *ordjson.Object, key string, required []string) bool {
	if value == nil {
		return false
	}
	field, ok := value.Get(key)
	values, listOK := field.([]any)
	if !ok || !listOK {
		return false
	}
	for _, item := range values {
		object := asObject(item)
		if object == nil {
			return false
		}
		for _, requiredKey := range required {
			if !snapshotString(object, requiredKey) {
				return false
			}
		}
	}
	return true
}

func snapshotPositiveNumber(value *ordjson.Object, key string) bool {
	if value == nil {
		return false
	}
	field, ok := value.Get(key)
	number, numberOK := field.(json.Number)
	if !ok || !numberOK {
		return false
	}
	parsed, err := number.Int64()
	return err == nil && parsed > 0
}

func snapshotNumber(value *ordjson.Object, key string) bool {
	if value == nil {
		return false
	}
	field, ok := value.Get(key)
	_, numberOK := field.(json.Number)
	return ok && numberOK
}

func snapshotOptionalString(value *ordjson.Object, key string) bool {
	if value == nil {
		return false
	}
	field, ok := value.Get(key)
	if !ok || field == nil {
		return true
	}
	_, stringOK := field.(string)
	return stringOK
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
