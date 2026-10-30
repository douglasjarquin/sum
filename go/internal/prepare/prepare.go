package prepare

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/brief"
	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pipeline"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/procedure"
	"github.com/douglasjarquin/sum/go/internal/project"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	sumruntime "github.com/douglasjarquin/sum/go/internal/runtime"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/taskcheckout"
	"github.com/douglasjarquin/sum/go/internal/verifycontract"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

const LaunchUncertainPrefix = "Launch/prompt uncertain"

const MaxText = 256 * 1024

const baseFetchBound = 5 * time.Minute

func jsonInt(n int) json.Number { return json.Number(fmt.Sprint(n)) }

func asString(v any) string { s, _ := v.(string); return s }

func asObject(v any) *ordjson.Object { obj, _ := v.(*ordjson.Object); return obj }

func runGit(args ...string) (string, error) {
	out, err := proc.Run(append([]string{"git"}, args...), "", 0, true, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Stdout), nil
}

type taskBase struct {
	SHA    string
	Branch string
	Source string
}

func branchName(ref string) string {
	for _, prefix := range []string{"refs/remotes/origin/", "origin/", "refs/heads/"} {
		ref = strings.TrimPrefix(ref, prefix)
	}
	return ref
}

func resolveBase(repo, ref, target string) (taskBase, error) {
	ref = strings.TrimSpace(ref)
	target = strings.TrimSpace(target)
	if ref == "" || ref == "HEAD" {
		if target != "" {
			return taskBase{}, fmt.Errorf("--target-branch names the delivery branch for a commit or tag start, so give that start with --base; to branch from %s itself pass --base %s alone.", branchName(target), branchName(target))
		}
		branch, err := defaultBranch(repo)
		if err != nil {
			return taskBase{}, err
		}
		sha, err := fetchBranch(repo, branch)
		if err != nil {
			return taskBase{}, err
		}
		return taskBase{SHA: sha, Branch: branch, Source: pipeline.BaseSourceDefault}, nil
	}
	name := branchName(ref)
	isBranch, err := remoteHasBranch(repo, name)
	if err != nil {
		return taskBase{}, err
	}
	if isBranch {
		if target != "" {
			return taskBase{}, fmt.Errorf("--base %s already names a branch on origin; --target-branch is only for a commit or tag start.", name)
		}
		sha, err := fetchBranch(repo, name)
		if err != nil {
			return taskBase{}, err
		}
		return taskBase{SHA: sha, Branch: name, Source: pipeline.BaseSourceExplicit}, nil
	}
	if target == "" {
		return taskBase{}, fmt.Errorf("--base %s names no branch on origin, and a task needs a delivery branch: pass --base BRANCH, or keep this start and name its delivery branch with --target-branch BRANCH.", name)
	}
	targetName := branchName(target)
	targetIsBranch, err := remoteHasBranch(repo, targetName)
	if err != nil {
		return taskBase{}, err
	}
	if !targetIsBranch {
		return taskBase{}, fmt.Errorf("--target-branch %s names no branch on origin.", targetName)
	}
	targetSHA, err := fetchBranch(repo, targetName)
	if err != nil {
		return taskBase{}, err
	}
	startSHA, err := fetchRef(repo, ref, ref)
	if err != nil {
		return taskBase{}, err
	}
	if ancestor, _ := proc.Run([]string{"git", "-C", repo, "merge-base", "--is-ancestor", startSHA, targetSHA}, "", baseFetchBound, false, gitBaseEnv()); ancestor.Code != 0 {
		return taskBase{}, fmt.Errorf("The start %s (%s) is not an ancestor of origin/%s, so a task from it could not deliver to that branch; choose a start on that branch or another --target-branch.", ref, startSHA[:7], targetName)
	}
	return taskBase{SHA: startSHA, Branch: targetName, Source: pipeline.BaseSourceExplicit}, nil
}

func defaultBranch(repo string) (string, error) {
	symref, err := proc.Run([]string{"git", "-C", repo, "ls-remote", "--symref", "origin", "HEAD"}, "", baseFetchBound, false, gitBaseEnv())
	if err == nil && symref.Code == 0 {
		for _, line := range strings.Split(symref.Stdout, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "ref:" && fields[2] == "HEAD" && strings.HasPrefix(fields[1], "refs/heads/") {
				return strings.TrimPrefix(fields[1], "refs/heads/"), nil
			}
		}
	}
	head, err := runGit("-C", repo, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		if name := strings.TrimPrefix(strings.TrimSpace(head), "origin/"); name != "" {
			return name, nil
		}
	}
	return "", fmt.Errorf("Could not determine the repository's default branch from origin or the clone's origin/HEAD; pass --base BRANCH.")
}

func remoteHasBranch(repo, name string) (bool, error) {
	listed, err := proc.Run([]string{"git", "-C", repo, "ls-remote", "--heads", "origin", "refs/heads/" + name}, "", baseFetchBound, false, gitBaseEnv())
	if err != nil || listed.Code != 0 {
		detail := listed.Detail()
		if err != nil {
			detail = err.Error()
		}
		return false, fmt.Errorf("Could not fetch %s from origin for the task base: %s", name, detail)
	}
	for _, line := range strings.Split(listed.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == "refs/heads/"+name {
			return true, nil
		}
	}
	return false, nil
}

func fetchBranch(repo, name string) (string, error) {
	return fetchRef(repo, "refs/heads/"+name, name)
}

func fetchRef(repo, refspec, display string) (string, error) {
	fetch, err := proc.Run([]string{"git", "-C", repo, "fetch", "--quiet", "origin", refspec}, "", baseFetchBound, false, gitBaseEnv())
	if err != nil || fetch.Code != 0 {
		detail := fetch.Detail()
		if err != nil {
			detail = err.Error()
		}
		return "", fmt.Errorf("Could not fetch %s from origin for the task base: %s", display, detail)
	}
	sha, err := runGit("-C", repo, "rev-parse", "--verify", "FETCH_HEAD^{commit}", "--")
	if err != nil {
		return "", fmt.Errorf("Fetched %s but could not resolve the remote tip: %s", display, err)
	}
	return sha, nil
}

func gitBaseEnv() []string {
	return append(proc.ScrubbedEnv(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
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
	Repo         string
	Project      string
	Brief        string
	Harness      string
	Model        string
	Reasoning    string
	SameAsYou    bool
	Preset       string
	PresetSet    bool
	Base         string
	TargetBranch string
	Kind         string
	Approved     bool
	Extra        []string
	RuntimeRoot  string
	SumctlPath   string

	PermissionOverride bool
}

func Prepare(s *store.Store, ctx *ordjson.Object, args Args) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	// The brief references the worker procedure; refuse before any Herdr side effect if it cannot be pinned.
	if err := procedure.Check(args.RuntimeRoot); err != nil {
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

		PermissionOverride: args.PermissionOverride,
	})
	if err != nil {
		return nil, err
	}
	base, err := resolveBase(repo, args.Base, args.TargetBranch)
	if err != nil {
		return nil, err
	}
	baseSHA := base.SHA
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	tasks, err := s.AllTasks()
	if err != nil {
		unlock()
		return nil, err
	}
	admission, err := launch.AdmitProject(s, tasks, repo, projectObj)
	if err != nil {
		unlock()
		return nil, err
	}
	tid, err := newTaskID()
	if err != nil {
		unlock()
		return nil, err
	}
	host, err := s.Machine()
	if err != nil {
		unlock()
		return nil, err
	}
	session := asString(func() any { v, _ := ctx.Get("session"); return v }())
	owner := store.EndpointFromContext(ctx).Object()
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
	task.Set("machine", host.ID)
	task.Set("repository", repo)
	task.Set("project", projectObj)
	task.Set("base_sha", baseSHA)
	task.Set("base_branch", base.Branch)
	task.Set("base_branch_source", base.Source)
	task.Set("branch", "sum/"+tid)
	harness, _ := launchSpec.Get("harness")
	task.Set("harness", harness)
	task.Set("launch", launchSpec)
	task.Set("kind", args.Kind)
	task.Set("brief", string(briefText))
	task.Set("parent", ctx)
	task.Set("session", session)
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
	intent, err := taskcheckout.Plan(s.Root, repo, tid, baseSHA)
	if err != nil {
		unlock()
		return nil, err
	}
	intentObject, err := ordjson.FromValue(intent)
	if err != nil {
		unlock()
		return nil, err
	}
	task.Set("checkout_intent", intentObject)
	task.Set("prepare_operation_pid", jsonInt(os.Getpid()))
	task.Set("preparation_approval", versions.ApprovedFingerprint(task))
	task.Set("preparation_launch_sha256", preparationLaunchHash(task))
	if err := s.SaveTask(task); err != nil {
		unlock()
		return nil, err
	}
	unlock()

	result, err := completePreparation(s, args.RuntimeRoot, args.SumctlPath, tid, true)
	if err != nil {
		return failPrepare(s, tid, err)
	}
	return result, nil
}

// Reconcile finishes only the original saved preparation. It never starts a
// terminal, advances the base, resets work, or creates another task identity.
func Reconcile(s *store.Store, ctx *ordjson.Object, runtimeRoot, sumctlPath, taskID string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	return completePreparation(s, runtimeRoot, sumctlPath, taskID, false)
}

func completePreparation(s *store.Store, runtimeRoot, sumctlPath, tid string, initial bool) (*ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.ReadTask(tid)
	if err != nil {
		return nil, err
	}
	if err = s.CheckMachine(task); err != nil {
		return nil, err
	}
	if err = repair.RefuseDuringCleanup(task, "Preparation reconciliation"); err != nil {
		return nil, err
	}
	worker, err := reservations.Worker(task)
	if err != nil {
		return nil, err
	}
	status := asString(policyField(task, "status"))
	if (status != "preparing" && status != "needs-attention" && status != "prepared") || policyField(worker, "native_launch") != nil || policyField(task, "worker_endpoint") != nil {
		return nil, fmt.Errorf("task has an active or uncertain native launch; preparation reconciliation is refused")
	}
	if !initial {
		if pid, ok := policyField(task, "prepare_operation_pid").(json.Number); ok {
			n, e := pid.Int64()
			if e != nil {
				return nil, e
			}
			alive, e := proc.PIDRunning(int(n))
			if e != nil || alive {
				return nil, fmt.Errorf("preparation operation remains active or unobservable; preserve its intent")
			}
		}
	}
	approved, _ := ordjson.MarshalCompact(versions.ApprovedFingerprint(task))
	savedApproval, _ := ordjson.MarshalCompact(policyField(task, "preparation_approval"))
	if string(approved) != string(savedApproval) || policyField(task, "preparation_launch_sha256") != preparationLaunchHash(task) {
		return nil, fmt.Errorf("saved approved scope or launch policy changed; preparation reconciliation is refused")
	}
	var intent taskcheckout.Intent
	if err = decode(policyField(task, "checkout_intent"), &intent); err != nil {
		return nil, err
	}
	if intent.Repository != asString(policyField(task, "repository")) || intent.BaseSHA != asString(policyField(task, "base_sha")) || intent.Branch != asString(policyField(task, "branch")) {
		return nil, fmt.Errorf("saved checkout intent differs from the approved task")
	}
	task.Set("prepare_operation_pid", jsonInt(os.Getpid()))
	if err = s.SaveTask(task); err != nil {
		return nil, err
	}
	checkout, err := taskcheckout.Create(context.Background(), intent)
	if err != nil {
		return nil, err
	}
	task.Set("worktree", checkout.Path)
	policy := asObject(policyField(task, "verification_policy"))
	launchSpec := launch.TaskLaunch(task)
	if policy != nil {
		if err = verifycontract.ValidatePolicySeal(policy); err != nil {
			return nil, err
		}
		prepared := snapshotObject(policy, "prepared_worktree")
		if policyField(policy, "base_sha") != intent.BaseSHA || policyField(prepared, "path") != checkout.Path || policyField(prepared, "git_root") != checkout.GitRoot || policyField(prepared, "head") != checkout.Head || policyField(prepared, "branch") != checkout.Branch || policyField(prepared, "git_common_dir") != checkout.CommonDir {
			return nil, fmt.Errorf("sealed preparation policy differs from the saved Git identity")
		}
	} else {
		contractRoot, cleanup, materializeErr := verifycontract.MaterializeCommit(intent.Repository, intent.BaseSHA)
		if materializeErr != nil {
			policy = verifycontract.PolicyAtDispatchUnavailable(intent.BaseSHA, materializeErr.Error())
		} else {
			defer cleanup()
			policy = verifycontract.PolicyAtDispatch(contractRoot, intent.BaseSHA, environment.VerificationContractStatusAtDispatch(contractRoot, runtimeRoot))
		}
		verifycontract.AddDispatchMetadata(policy, verifycontract.DispatchMetadata{Repository: intent.Repository, Project: asObject(policyField(task, "project")), Launch: launchSpec, RuntimeRoot: runtimeRoot, Worktree: checkout.Path, GitRoot: checkout.GitRoot, Head: checkout.Head, Branch: checkout.Branch, GitCommonDir: checkout.CommonDir})
		verifycontract.SealPolicy(policy)
		task.Set("verification_policy", policy)
		if err = s.SaveTask(task); err != nil {
			return nil, err
		}
	}
	taskPath, err := s.TaskPath(tid)
	if err != nil {
		return nil, err
	}
	var briefPath string
	if _, statErr := os.Lstat(filepath.Join(taskPath, versions.File)); statErr == nil {
		briefPath, err = versions.ActiveBrief(s, task)
	} else if os.IsNotExist(statErr) {
		briefPath, err = brief.WriteInitial(s, runtimeRoot, sumctlPath, task)
	} else {
		err = statErr
	}
	if err != nil {
		return nil, err
	}
	task.Set("brief_path", briefPath)
	task.Set("status", "prepared")
	task.Set("error", nil)
	task.Delete("prepare_operation_pid")
	worker.Set("checkout", checkout.Path)
	if policyField(worker, "state") == "uncertain" {
		id := asString(policyField(worker, "id"))
		if _, err = reservations.Transition(task, id, "held", store.Now(), nil, nil); err != nil {
			return nil, err
		}
	}
	if err = s.SaveTask(task); err != nil {
		return nil, err
	}
	task.Set("confirmation", launch.Confirmation(launchSpec))
	return task, nil
}

func failPrepare(s *store.Store, tid string, cause error) (*ordjson.Object, error) {
	errorText := fmt.Sprintf("Prepare failed or became uncertain: %s. Do not blindly create a replacement; inspect the saved Git checkout intent first.", cause)
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
	worker, err := reservations.Worker(task)
	if err != nil {
		unlock()
		return nil, err
	}
	if worker == nil {
		unlock()
		return nil, fmt.Errorf("Worker execution reservation is missing; start is refused.")
	}
	resuming := asString(func() any { v, _ := worker.Get("resumes"); return v }()) != ""
	identityMismatch := rootErr != nil || headErr != nil || branchErr != nil || commonErr != nil || repositoryCommonErr != nil || recordedWorktree == nil || resolvePath(preparedWorktree) == resolvePath(repository) || resolvePath(actualRoot) != resolvePath(preparedWorktree) || resolvePath(actualRoot) != resolvePath(asString(policyField(recordedWorktree, "git_root"))) || resolvePath(preparedWorktree) != resolvePath(asString(policyField(recordedWorktree, "path"))) || actualBranch != asString(policyField(recordedWorktree, "branch")) || resolveGitPath(preparedWorktree, actualCommon) != resolvePath(asString(policyField(recordedWorktree, "git_common_dir"))) || resolveGitPath(preparedWorktree, actualCommon) != resolveGitPath(repository, repositoryCommon)
	headPinned := actualHead == asString(func() any { v, _ := task.Get("base_sha"); return v }()) && actualHead == asString(policyField(recordedWorktree, "head"))
	if identityMismatch || (!resuming && !headPinned) {
		unlock()
		return nil, fmt.Errorf("The prepared checkout does not match its saved Git identity; start is refused.")
	}
	if err := verifycontract.ValidateCommittedPolicy(preparedWorktree, asObject(policy)); err != nil {
		unlock()
		return nil, fmt.Errorf("The coordinator-owned verification snapshot does not match its committed base: %w", err)
	}
	// A fresh session reads the active revision and its pinned procedure; refuse to launch an incomplete one.
	briefFile, err := versions.ActiveBrief(s, task)
	if err != nil {
		unlock()
		return nil, err
	}
	launchSpec := launch.TaskLaunch(task)
	harness := asString(func() any { v, _ := launchSpec.Get("harness"); return v }())
	taskArgs, err := TaskAccessArgs(s, task, harness, briefFile, preparedWorktree)
	if err != nil {
		unlock()
		return nil, err
	}
	isolationArgs := ClaudeWorkerMCPIsolationArgs(harness, preparedWorktree)
	state := asString(func() any { v, _ := worker.Get("state"); return v }())
	if state != "held" && state != "running" {
		id, _ := worker.Get("id")
		unlock()
		return nil, fmt.Errorf("Worker execution reservation %v is %s; start is refused.", id, state)
	}
	argv := []string{}
	for _, a := range func() []any { v, _ := launchSpec.Get("argv"); list, _ := v.([]any); return list }() {
		if s, ok := a.(string); ok {
			argv = append(argv, s)
		}
	}
	if harness == "claude" {
		argv, err = applyTaskAccessArgs(launchSpec, argv, extra, taskArgs)
		if err != nil {
			unlock()
			return nil, err
		}
		argv = append(argv, isolationArgs...)
	} else {
		argv = append(argv, extra...)
	}
	launchSpec.Set("argv", anyStrings(argv))
	if err := launch.CheckRecorded(launchSpec); err != nil {
		unlock()
		return nil, err
	}
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
	return startNative(s, ctx, runtimeRoot, taskID, briefFile, task, worker, launchSpec, argv, unlock)
}

func WorkerPrompt(s *store.Store, runtimeRoot, taskID, briefFile string, task *ordjson.Object) string {
	installRoot := sumruntime.ResolveInstallation(runtimeRoot, os.Getenv("SUM_INSTALL_ROOT"))
	initCommand := shquote.CommandFor(filepath.Join(installRoot, "bin", "sumctl"), s.Root, "init")
	prompt := fmt.Sprintf("You are the sum worker for %s, not the coordinator. First run `%s` with the installed helper, never `./bin/sumctl` from your checkout. Then read the complete file %s and every required worker procedure file it names, and execute only that approved task. Questions and results must be saved using the commands in that brief.", taskID, initCommand, quoteJSON(briefFile))
	if extras := repair.PendingResumeTexts(task); len(extras) > 0 {
		prompt += " Recorded repairs waiting on this resume:"
		for _, extra := range extras {
			prompt += " " + extra
		}
	}
	return prompt
}

func validVerificationSnapshot(value any, task *ordjson.Object) bool {
	policyObject, _ := value.(*ordjson.Object)
	if fmt.Sprint(policyField(policyObject, "dispatch_schema")) != "2" {
		return false
	}
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
	if preparedWorktree == nil || !snapshotString(preparedWorktree, "path") || !snapshotString(preparedWorktree, "git_root") || !snapshotString(preparedWorktree, "head") || !snapshotString(preparedWorktree, "branch") || !snapshotString(preparedWorktree, "git_common_dir") {
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

func FailStart(s *store.Store, taskID string, cause error) (*ordjson.Object, error) {
	errorText := fmt.Sprintf("%s: %s. Inspect the saved native terminal; do not relaunch. Trust/auth prompts are for the user, never the coordinator. `execution redeliver %s --attempt X` resends the recorded brief; `execution confirm %s --attempt X` records a running attempt from the worker's own receipt.", LaunchUncertainPrefix, cause, taskID, taskID)
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
	return Start(s, ctx, args.RuntimeRoot, id, nil)
}

func preparationLaunchHash(task *ordjson.Object) string {
	data, _ := ordjson.MarshalCompact(launch.TaskLaunch(task))
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
