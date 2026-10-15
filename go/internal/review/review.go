package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/douglasjarquin/sum/go/internal/bindcmd"
	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/evidenceview"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/panes"
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

type LaunchArgs struct {
	Task      string
	Harness   string
	Model     string
	Reasoning string
	Preset    string
	PresetSet bool
}

func (a LaunchArgs) explicit() bool {
	return a.Harness != "" || a.Model != "" || a.Reasoning != "" || a.PresetSet
}

func Launch(s *store.Store, ctx *ordjson.Object, runtimeRoot, sumctlPath string, args LaunchArgs) (*ordjson.Object, error) {
	taskID := args.Task
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
	workspace := asString(task, "workspace")
	worktree := asString(task, "worktree")
	if workspace == "" || worktree == "" {
		return nil, fmt.Errorf("Task workspace or checkout identity is missing; reviewer launch is refused.")
	}
	var rebind *ordjson.Object
	if recorded, exists := store.RecordedReviewer(task); exists {
		agent, stop, err := recordedReviewerGone(host, runtimeRoot, herdrPath, session, task, recorded, worktree)
		if err != nil {
			return nil, err
		}
		if stop == nil {
			if args.explicit() {
				return nil, fmt.Errorf("Task already has a live reviewer endpoint; --harness/--model/--reasoning/--preset apply only to a new reviewer. Prompt the recorded pane with a plain `reviewer launch`.")
			}
			return promptRecordedReviewer(s, herdrPath, session, sumctlPath, task, recorded, agent, worktree)
		}
		rebind = ordjson.NewObject()
		rebind.Set("from", bindcmd.EndpointRecord(recorded))
		rebind.Set("stop_evidence", stop)
	}
	if _, wsCode, err := herdrclient.Observe(herdrPath, session, 5*time.Second, "workspace", "get", workspace); err != nil {
		return nil, err
	} else if wsCode != "" {
		return nil, fmt.Errorf("Worker workspace %s is gone (%s); reviewer launch never opens a new workspace. Re-register the intact checkout with `herdr worktree open --path %s --no-focus`, then bind the worker with `sumctl bind %s --worker-pane PANE` before launching the reviewer.", workspace, wsCode, worktree, taskID)
	}
	harness, argv, preset, err := reviewerLaunch(s, ctx, runtimeRoot, args)
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
		existing, _ := store.RecordedReviewer(current)
		switch {
		case rebind == nil && existing != nil:
			err = fmt.Errorf("Task already has a reviewer endpoint; the new tab %s was created but not adopted.", pane)
		case rebind != nil && !bindcmd.SameEndpoint(existing, objectField(rebind, "from")):
			err = fmt.Errorf("The recorded reviewer endpoint changed concurrently; the new tab %s was created but not adopted.", pane)
		default:
			if rebind != nil {
				rebind.Set("to", bindcmd.EndpointRecord(endpoint))
				var record *ordjson.Object
				record, err = evidence.Append(current, "reviewer-rebind", "coordinator", rebind, nil, ctx)
				if err == nil {
					record.Set("brief_revision", evidence.ActiveRevision(s, current))
				}
			}
			if err == nil {
				current.Set("reviewer", endpoint)
				err = s.SaveTask(current)
			}
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
	delivery, err := deliverKickoff(s, herdrPath, session, sumctlPath, task, pane, skillPath, worktree)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("workspace", workspace)
	result.Set("worktree", worktree)
	result.Set("reviewer", endpoint)
	result.Set("harness", harness)
	result.Set("preset", preset)
	result.Set("placement", "tab")
	if rebind == nil {
		result.Set("action", "launched")
		result.Set("rebind", nil)
		result.Set("note", "Reviewer launched in its own labeled task tab; findings must be saved with `review` from that pane.")
	} else {
		result.Set("action", "relaunched")
		result.Set("rebind", rebind)
		result.Set("note", "The recorded reviewer pane was proven gone, so a fresh labeled task tab replaced it and the rebind is recorded; findings must be saved with `review` from the new pane.")
	}
	result.Set("prompt", delivery)
	return result, nil
}

func recordedReviewerGone(host machine.Identity, runtimeRoot, herdrPath, session string, task, recorded *ordjson.Object, worktree string) (*ordjson.Object, *ordjson.Object, error) {
	pane := asString(recorded, "pane")
	if !host.Is(asString(recorded, "machine")) || asString(recorded, "session") != session {
		return nil, nil, fmt.Errorf("Task already has a reviewer endpoint %s on another machine or session; use it from there, or prove it stopped there and use `sumctl bind %s --reviewer-pane PANE`.", pane, asString(task, "id"))
	}
	agent, code, err := herdrclient.Observe(herdrPath, session, 5*time.Second, "agent", "get", pane)
	if err != nil {
		return nil, nil, err
	}
	if code == "" && !panes.ReviewerIsClosed(task) {
		return herdrclient.UnwrapAgent(agent), nil, nil
	}
	if code != "" && !herdrclient.IsAbsent(code) {
		return nil, nil, fmt.Errorf("Recorded reviewer pane %s cannot be observed (%s); nothing was prompted or launched.", pane, code)
	}
	stop, err := bindcmd.ReviewerStopped(runtimeRoot, session, pane, worktree, asString(task, "pane"), "", 0, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("Recorded reviewer pane %s is gone (%s) but its replacement is refused: %w", pane, code, err)
	}
	return nil, stop, nil
}

func promptRecordedReviewer(s *store.Store, herdrPath, session, sumctlPath string, task, recorded, agent *ordjson.Object, worktree string) (*ordjson.Object, error) {
	taskID := asString(task, "id")
	pane := asString(recorded, "pane")
	if got := herdrclient.AgentCwd(agent); got != "" && resolvePath(got) != resolvePath(worktree) {
		return nil, fmt.Errorf("Recorded reviewer pane %s is live but its cwd %s is not the task checkout %s; close it so Herdr reports pane_not_found, then run `reviewer launch` again.", pane, got, worktree)
	}
	delivery, err := deliverKickoff(s, herdrPath, session, sumctlPath, task, pane, asString(recorded, "skill_path"), worktree)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("workspace", asString(task, "workspace"))
	result.Set("worktree", worktree)
	result.Set("reviewer", recorded)
	result.Set("harness", asString(recorded, "harness"))
	result.Set("preset", nil)
	result.Set("placement", "recorded-pane")
	result.Set("action", "prompted")
	result.Set("prompt", delivery)
	result.Set("note", "The recorded reviewer pane is live and was prompted to review the current candidate; no second tab was opened.")
	return result, nil
}

func deliverKickoff(s *store.Store, herdrPath, session, sumctlPath string, task *ordjson.Object, pane, skillPath, worktree string) (*ordjson.Object, error) {
	taskID := asString(task, "id")
	candidate := evidenceview.CurrentCandidate(task)
	text := kickoffPrompt(sumctlPath, taskID, candidate, skillPath, policyCandidate(task, candidate))
	delivery := ordjson.NewObject()
	delivery.Set("pane", pane)
	delivery.Set("candidate", orNil(candidate))
	delivery.Set("at", store.Now())
	if _, err := herdrclient.Call(herdrPath, session, 10*time.Second, "agent", "prompt", pane, text); err != nil {
		delivery.Set("state", "not-delivered")
		delivery.Set("detail", err.Error())
	} else {
		delivery.Set("state", "delivered")
		delivery.Set("detail", nil)
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	reviewer, exists := store.RecordedReviewer(current)
	if !exists || asString(reviewer, "pane") != pane {
		return nil, fmt.Errorf("Reviewer endpoint changed while prompting pane %s; the prompt outcome was not recorded.", pane)
	}
	reviewer.Set("prompt", delivery)
	if err := s.SaveTask(current); err != nil {
		return nil, err
	}
	return delivery, nil
}

func kickoffPrompt(sumctlPath, taskID, candidate, skillPath string, policy bool) string {
	command := shquote.Quote(sumctlPath)
	target := "the current candidate"
	if candidate != "" {
		target = "candidate " + candidate
	}
	text := fmt.Sprintf("You are the independent reviewer for %s. Review %s. Read the task contract, candidate, and evidence with `%s context %s --role reviewer`, then follow the reviewer section of the delivery skill at %s. Inspect the candidate read-only in this checkout: do not edit the checkout, commit, push, or record verification. Save your verdict with `%s review %s --verdict approve|changes-requested|blocked|comment --candidate SHA --file findings.md` plus `--focus`, `--finding`, and `--limitation` notes.", taskID, target, command, taskID, shquote.Quote(skillPath), command, taskID)
	if policy {
		text += fmt.Sprintf(" This candidate changed verification policy files (VERIFY.md, mise.toml, mise-tasks/, the feature maps, or the verify skill); read every changed policy file line by line and record an approving verdict only with `%s review %s --policy-reviewed ...`.", command, taskID)
	}
	return text
}

func policyCandidate(task *ordjson.Object, candidate string) bool {
	items, _ := task.Get("evidence")
	list, _ := items.([]any)
	for _, raw := range list {
		record := objectValue(raw)
		if asString(record, "kind") != "verification" {
			continue
		}
		if named := asString(record, "candidate"); candidate != "" && named != "" && named != candidate {
			continue
		}
		if requires, _ := record.Get("requires_root_review"); requires == true {
			return true
		}
	}
	return false
}

func orNil(text string) any {
	if text == "" {
		return nil
	}
	return text
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

func reviewerLaunch(s *store.Store, ctx *ordjson.Object, runtimeRoot string, args LaunchArgs) (string, []string, any, error) {
	loaded, err := settings.LoadSettings(s)
	if err != nil {
		return "", nil, nil, err
	}
	resolve := launch.ResolveArgs{Harness: args.Harness, Model: args.Model, Reasoning: args.Reasoning, Preset: args.Preset, PresetSet: args.PresetSet, RuntimeRoot: runtimeRoot}
	if loaded.Reviewer != nil && !args.explicit() {
		resolve.Preset = asString(loaded.Reviewer, "preset")
		resolve.PresetSet = true
	}
	if resolve.PresetSet || args.explicit() {
		resolved, err := launch.Resolve(s, ctx, resolve)
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
