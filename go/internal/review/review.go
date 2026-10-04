package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pipeline"
	"github.com/douglasjarquin/sum/go/internal/prepare"
	"github.com/douglasjarquin/sum/go/internal/procedure"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

var (
	verdicts    = map[string]bool{"approve": true, "changes-requested": true, "blocked": true, "comment": true}
	sha40       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	toolNamePat = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)
)

// Launch opens the independent reviewer in a labeled tab in the task workspace.
// It records the endpoint before starting the agent so ordinary reviewer cleanup
// and occupancy logic can identify the pane even if agent start is uncertain.
func Launch(s *store.Store, ctx *ordjson.Object, runtimeRoot, sumctlPath, taskID string) (*ordjson.Object, error) {
	if ctx == nil {
		return nil, fmt.Errorf("A registered coordinator pane is required to launch a reviewer.")
	}
	session := asString(ctx, "session")
	coordinatorPane := asString(ctx, "pane")
	if session == "" || coordinatorPane == "" {
		return nil, fmt.Errorf("A registered coordinator pane is required to launch a reviewer.")
	}
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		return nil, err
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	if err := s.CheckMachine(task); err != nil {
		return nil, err
	}
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	parent := objectField(task, "parent")
	if parent == nil || !host.SameEndpoint(parent, ctx) {
		return nil, fmt.Errorf("Only the task's recorded coordinator can launch its reviewer.")
	}
	if _, exists := store.RecordedReviewer(task); exists {
		return nil, fmt.Errorf("Task already has a reviewer endpoint; use its recorded pane. If that pane is gone, first prove it stopped, then use `sumctl bind TASK --reviewer-pane PANE`.")
	}
	workspace := asString(task, "workspace")
	worktree := asString(task, "worktree")
	if workspace == "" || worktree == "" {
		return nil, fmt.Errorf("Task workspace or checkout identity is missing; reviewer launch is refused.")
	}
	harness, argv, preset, err := reviewerLaunch(s, ctx, runtimeRoot)
	if err != nil {
		return nil, err
	}
	briefPath, err := versions.ActiveBrief(s, task)
	if err != nil {
		return nil, err
	}
	taskArgs, err := prepare.TaskAccessArgs(s, task, harness, briefPath, worktree)
	if err != nil {
		return nil, err
	}
	argv = append(append([]string(nil), argv...), taskArgs...)
	skillPath, skillSHA, err := pinReviewerSkill(s, task, runtimeRoot)
	if err != nil {
		return nil, err
	}
	created, err := herdrclient.Call(herdrPath, session, 15*time.Second, "tab", "create", "--workspace", workspace, "--cwd", worktree, "--label", "reviewer", "--no-focus")
	if err != nil {
		return nil, fmt.Errorf("Could not create reviewer tab in workspace %s: %w", workspace, err)
	}
	createdObj := objectValue(created)
	rootPane := objectField(createdObj, "root_pane")
	pane := asString(rootPane, "pane_id")
	if pane == "" {
		return nil, fmt.Errorf("Herdr tab create returned no reviewer pane id in workspace %s.", workspace)
	}
	observed, err := herdrclient.Call(herdrPath, session, 5*time.Second, "pane", "get", pane)
	if err != nil {
		return nil, fmt.Errorf("Could not verify new reviewer pane %s: %w", pane, err)
	}
	observedPane := objectField(objectValue(observed), "pane")
	if observedPane == nil {
		observedPane = objectValue(observed)
	}
	if asString(observedPane, "workspace_id") != workspace || resolvePath(asString(observedPane, "cwd")) != resolvePath(worktree) {
		return nil, fmt.Errorf("New reviewer pane %s does not match task workspace %s and checkout %s; it was not started.", pane, workspace, worktree)
	}
	endpoint := ordjson.NewObject()
	endpoint.Set("machine", host.ID)
	endpoint.Set("session", session)
	endpoint.Set("pane", pane)
	endpoint.Set("cwd", worktree)
	endpoint.Set("harness", harness)
	endpoint.Set("argv", stringList(argv))
	endpoint.Set("skill_path", skillPath)
	endpoint.Set("skill_sha256", skillSHA)
	endpoint.Set("bound_at", store.Now())
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	current, err := s.ReadTask(taskID)
	if err == nil {
		if existing, _ := current.Get("reviewer"); existing != nil {
			err = fmt.Errorf("Task already has a reviewer endpoint; the new tab %s was created but not adopted.", pane)
		} else {
			current.Set("reviewer", endpoint)
			err = s.SaveTask(current)
		}
	}
	unlock()
	if err != nil {
		return nil, err
	}
	startArgs := herdrclient.AgentStartArgs("reviewer", harness, pane)
	if len(argv) > 0 {
		startArgs = append(startArgs, "--")
		startArgs = append(startArgs, argv...)
	}
	if _, err := herdrclient.Call(herdrPath, session, herdrclient.AgentStartCallTimeout, startArgs...); err != nil {
		return nil, fmt.Errorf("Reviewer endpoint %s is recorded, but agent start is uncertain: %w", pane, err)
	}
	command := shquote.Quote(sumctlPath)
	prompt := fmt.Sprintf("You are the independent reviewer for %s. Read the task contract and current candidate with `%s context %s --role reviewer`, inspect the candidate read-only, and save findings with `%s review %s --verdict approve|changes-requested|blocked|comment --candidate SHA --file findings.md`. Do not edit the checkout or record verification.", taskID, command, taskID, command, taskID)
	if _, err := herdrclient.Call(herdrPath, session, 10*time.Second, "agent", "prompt", pane, prompt); err != nil {
		return nil, fmt.Errorf("Reviewer endpoint %s is recorded, but its task prompt could not be delivered: %w", pane, err)
	}
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("workspace", workspace)
	result.Set("worktree", worktree)
	result.Set("reviewer", endpoint)
	result.Set("harness", harness)
	result.Set("preset", preset)
	result.Set("placement", "tab")
	result.Set("note", "Reviewer launched in its own labeled task tab; findings must be saved with `review` from that pane.")
	return result, nil
}

func pinReviewerSkill(s *store.Store, task *ordjson.Object, runtimeRoot string) (string, string, error) {
	taskID := asString(task, "id")
	taskDir, err := s.TaskPath(taskID)
	if err != nil {
		return "", "", err
	}
	source := procedure.Source{Name: "sum-deliver", Path: "skills/sum-deliver/SKILL.md", Load: procedure.Required}
	_, data, err := procedure.Load(runtimeRoot, source, "reviewer launch was refused")
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(data)
	sha := hex.EncodeToString(digest[:])
	policy := objectField(task, "verification_policy")
	runtime := objectField(policy, "source_runtime")
	if expected := asString(runtime, "reviewer_skill_sha256"); expected != "" && expected != sha {
		return "", "", fmt.Errorf("The delivery skill does not match the task's dispatch snapshot; reviewer launch is refused.")
	}
	name := "sum-deliver-" + sha[:16] + ".md"
	path := filepath.Join(taskDir, procedure.Dir, name)
	if info, statErr := os.Lstat(path); statErr == nil {
		if !info.Mode().IsRegular() {
			return "", "", fmt.Errorf("Pinned delivery skill %s is not a regular file.", path)
		}
		current, readErr := os.ReadFile(path)
		if readErr != nil || sha256.Sum256(current) != digest {
			return "", "", fmt.Errorf("Pinned delivery skill %s does not match its recorded content.", path)
		}
	} else if !os.IsNotExist(statErr) {
		return "", "", statErr
	} else if err := procedure.WriteOnce(path, data); err != nil {
		return "", "", err
	}
	return path, sha, nil
}

func stringList(values []string) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}

func reviewerLaunch(s *store.Store, ctx *ordjson.Object, runtimeRoot string) (string, []string, any, error) {
	loaded, err := settings.LoadSettings(s)
	if err != nil {
		return "", nil, nil, err
	}
	if loaded.Reviewer != nil {
		name := asString(loaded.Reviewer, "preset")
		resolved, err := launch.Resolve(s, ctx, launch.ResolveArgs{Preset: name, PresetSet: true, RuntimeRoot: runtimeRoot})
		if err != nil {
			return "", nil, nil, err
		}
		argv := []string{}
		if list, ok := resolved.Get("argv"); ok {
			if values, is := list.([]any); is {
				for _, value := range values {
					if arg, is := value.(string); is {
						argv = append(argv, arg)
					}
				}
			}
		}
		preset, _ := resolved.Get("preset")
		return asString(resolved, "harness"), argv, preset, nil
	}
	root := launch.RootLaunch(runtimeRoot, ctx)
	harness := asString(root, "harness")
	if harness == "" {
		return "", nil, nil, fmt.Errorf("Cannot determine the coordinator's harness (%s); configure `reviewer.preset` or start from the coordinator pane.", asString(root, "error"))
	}
	return harness, nil, nil, nil
}

func asString(obj *ordjson.Object, key string) string {
	if obj == nil {
		return ""
	}
	value, _ := obj.Get(key)
	text, _ := value.(string)
	return text
}

func objectField(obj *ordjson.Object, key string) *ordjson.Object {
	if obj == nil {
		return nil
	}
	value, _ := obj.Get(key)
	result, _ := value.(*ordjson.Object)
	return result
}

func objectValue(value any) *ordjson.Object {
	result, _ := value.(*ordjson.Object)
	return result
}

func resolvePath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func endpointRole(host machine.Identity, task, endpoint *ordjson.Object) string {
	if endpoint == nil {
		return ""
	}
	if pane, ok := task.Get("pane"); ok && pane != nil && host.SameEndpoint(task, endpoint) {
		return "worker"
	}
	if parent, ok := task.Get("parent"); ok {
		if p, is := parent.(*ordjson.Object); is && host.SameEndpoint(p, endpoint) {
			return "coordinator"
		}
	}
	if reviewer, ok := task.Get("reviewer"); ok {
		if r, is := reviewer.(*ordjson.Object); is && host.SameEndpoint(r, endpoint) {
			return "reviewer"
		}
	}
	return "other"
}

// Args is one review recording. Text is the durable findings; Notes are the only fields published into the PR.
type Args struct {
	Task           string
	Verdict        string
	Candidate      string
	Tool           string
	Text           string
	RunPath        string
	PolicyReviewed bool
	Notes          []Note
}

func Run(s *store.Store, args Args, endpoint *ordjson.Object, pump returns.PumpOpts) (*ordjson.Object, error) {
	verdict := args.Verdict
	candidate := args.Candidate
	toolName := args.Tool
	text := args.Text
	runPath := args.RunPath
	policyReviewed := args.PolicyReviewed
	notes := args.Notes
	taskID := args.Task
	if !verdicts[verdict] {
		return nil, fmt.Errorf("--verdict must be one of ['approve', 'changes-requested', 'blocked', 'comment']")
	}
	if candidate != "" && !sha40.MatchString(candidate) {
		return nil, fmt.Errorf("--candidate must be a full 40-hex commit SHA")
	}
	if toolName != "" && !toolNamePat.MatchString(toolName) {
		return nil, fmt.Errorf("--tool must be a short name of the review facility that produced these findings (for example `made`)")
	}
	for i, note := range notes {
		if err := validateNote(note, i); err != nil {
			return nil, err
		}
		notes[i] = normalizeNote(note)
	}
	var made *ordjson.Object
	if runPath != "" {
		if toolName == "" {
			toolName = "made"
		}
		if toolName != "made" {
			return nil, fmt.Errorf("--run is the Made report path; pass --tool made")
		}
		report, err := loadMadeReport(runPath)
		if err != nil {
			return nil, err
		}
		reportCandidate := ""
		if value, ok := report.Get("candidate"); ok {
			reportCandidate, _ = value.(string)
		}
		if candidate == "" {
			candidate = reportCandidate
		}
		if candidate != reportCandidate {
			return nil, fmt.Errorf("Made report candidate %s does not match --candidate %s", reportCandidate, candidate)
		}
		made = report
	}
	result, task, err := record(s, taskID, verdict, candidate, toolName, text, policyReviewed, notes, made, endpoint)
	if err != nil {
		return nil, err
	}
	// Like a report, the saved verdict is a return to the parent, and one bounded pass tries to tell it now.
	saved, _ := result.Get("evidence")
	id, _ := saved.(*ordjson.Object).Get("id")
	owed, err := returns.OpenObligations(s, task)
	if err != nil {
		return nil, err
	}
	for _, obligation := range owed {
		if oid, _ := obligation.Get("id"); oid == fmt.Sprintf("review:%v", id) {
			notice, err := returns.Notify(s, pump, taskID, "parent", "a review verdict is recorded", false)
			if err != nil {
				return nil, err
			}
			result.Set("notice", notice)
			return result, nil
		}
	}
	result.Set("notice", nil)
	result.Set("note", "Findings saved. The task records no parent pane, so no return was created and no one was notified. They do not verify the candidate or close anything.")
	return result, nil
}

func record(s *store.Store, taskID, verdict, candidate, toolName, text string, policyReviewed bool, notes []Note, made, endpoint *ordjson.Object) (*ordjson.Object, *ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, nil, err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, nil, err
	}
	if made != nil {
		if repo, ok := made.Get("repository"); ok {
			if name, _ := repo.(string); name != "" {
				taskRepo, _ := task.Get("repository")
				if taskRepo != name {
					return nil, nil, fmt.Errorf("Made report repository %s does not match task repository %v", name, taskRepo)
				}
			}
		}
		if blocking, _ := made.Get("blocking"); blocking == true && verdict == "approve" {
			return nil, nil, fmt.Errorf("Made report has blocking findings; an approve is refused")
		}
		if text == "" {
			runID, _ := made.Get("run_id")
			text = fmt.Sprintf("Imported Made report %v", runID)
		}
	}
	host, err := s.Machine()
	if err != nil {
		return nil, nil, err
	}
	role := endpointRole(host, task, endpoint)
	if role == "worker" {
		return nil, nil, fmt.Errorf("The worker pane cannot record independent review of its own candidate. Save findings from the reviewer pane, or record `verify` as the coordinator.")
	}
	if endpoint != nil && role == "other" && toolName == "" {
		if existing, ok := task.Get("reviewer"); ok && existing != nil {
			rev := existing.(*ordjson.Object)
			pane, _ := rev.Get("pane")
			session, _ := rev.Get("session")
			return nil, nil, fmt.Errorf("Task already has reviewer pane %v in session %v; after proving that endpoint gone, adopt its replacement with `sumctl bind TASK --reviewer-pane PANE`.", pane, session)
		}
		bound := ordjson.NewObject()
		for _, key := range []string{"machine", "session", "pane", "cwd"} {
			v, _ := endpoint.Get(key)
			bound.Set(key, v)
		}
		bound.Set("bound_at", store.Now())
		task.Set("reviewer", bound)
		role = "reviewer"
	}
	if role == "" {
		role = "unattributed"
	}
	if len(notes) == 0 {
		notes = notesFromMade(made)
	}
	body := ordjson.NewObject()
	body.Set("verdict", verdict)
	body.Set("text", text)
	body.Set("notes", encodeNotes(notes))
	if toolName == "" {
		body.Set("tool", nil)
	} else {
		body.Set("tool", toolName)
	}
	body.Set("policy_reviewed", policyReviewed)
	if made != nil {
		body.Set("made", made)
	} else {
		body.Set("made", nil)
	}
	var cand any
	if candidate != "" {
		cand = candidate
	}
	record, err := evidence.Append(task, "review", role, body, cand, endpoint)
	if err != nil {
		return nil, nil, err
	}
	record.Set("brief_revision", evidence.ActiveRevision(s, task))
	if err := s.SaveTask(task); err != nil {
		return nil, nil, err
	}
	note := pipeline.RefreshNote(s, task)
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("evidence", record)
	result.Set("pipeline_note", note)
	reviewer, _ := task.Get("reviewer")
	result.Set("reviewer", reviewer)
	result.Set("note", "Findings saved as a return to the parent. They do not verify the candidate or close anything; a reviewer pane with saved findings is closable later, one without is not.")
	return result, task, nil
}
