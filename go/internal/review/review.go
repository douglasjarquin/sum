package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/evidenceview"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pipeline"
	"github.com/douglasjarquin/sum/go/internal/procedure"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
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

	PermissionOverride bool
}

func (a LaunchArgs) explicit() bool {
	return a.Harness != "" || a.Model != "" || a.Reasoning != "" || a.PresetSet
}

func (a LaunchArgs) newReviewerOnly() bool {
	return a.explicit() || a.PermissionOverride
}

func kickoffPrompt(sumctlPath, taskID, candidate, skillPath string, policy bool) string {
	command := shquote.Quote(sumctlPath)
	target := "the current candidate"
	if candidate != "" {
		target = "candidate " + candidate
	}
	text := fmt.Sprintf("You are the independent reviewer for %s. Review %s. First run `%s init --role reviewer --task %s`. Read the task contract, candidate, and evidence with `%s context %s --role reviewer`, then follow the reviewer section of the delivery skill at %s. Inspect the candidate read-only in this checkout: do not edit the checkout, commit, push, or record verification. Save your verdict with `%s review %s --verdict approve|changes-requested|blocked|comment --candidate SHA --file findings.md` plus `--focus`, `--finding`, and `--limitation` notes.", taskID, target, command, taskID, command, taskID, shquote.Quote(skillPath), command, taskID)
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

func reviewerLaunch(s *store.Store, ctx *ordjson.Object, runtimeRoot string, args LaunchArgs) (string, []string, any, *ordjson.Object, error) {
	loaded, err := settings.LoadSettings(s)
	if err != nil {
		return "", nil, nil, nil, err
	}
	resolve := launch.ResolveArgs{Harness: args.Harness, Model: args.Model, Reasoning: args.Reasoning, Preset: args.Preset, PresetSet: args.PresetSet, PermissionOverride: args.PermissionOverride, RuntimeRoot: runtimeRoot}
	if loaded.Reviewer != nil && !args.explicit() {
		resolve.Preset = asString(loaded.Reviewer, "preset")
		resolve.PresetSet = true
	}
	if resolve.PresetSet || args.newReviewerOnly() {
		if resolve.Harness == "" && !resolve.PresetSet {
			owner, err := s.Owner()
			if err != nil {
				return "", nil, nil, nil, err
			}
			resolve.Harness = asString(objectField(objectField(owner, "native_template"), "spec"), "harness")
			if resolve.Harness == "" {
				return "", nil, nil, nil, fmt.Errorf("coordinator has no qualified native harness; select a harness explicitly")
			}
		}
		resolved, err := launch.Resolve(s, ctx, resolve)
		if err != nil {
			return "", nil, nil, nil, err
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
		return asString(resolved, "harness"), argv, preset, objectField(resolved, "permissions"), nil
	}
	owner, err := s.Owner()
	if err != nil {
		return "", nil, nil, nil, err
	}
	root := objectField(objectField(owner, "native_template"), "spec")
	harness := asString(root, "harness")
	if harness == "" {
		return "", nil, nil, nil, fmt.Errorf("Cannot determine the coordinator's harness (%s); configure `reviewer.preset` or start from the coordinator terminal.", asString(root, "error"))
	}
	return harness, nil, nil, launch.HarnessDefaultPermissions(harness), nil
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
	current := store.EndpointFromContext(endpoint)
	if current.Schema == 2 {
		for _, entry := range []struct{ key, role string }{{"worker_endpoint", "worker"}, {"parent", "coordinator"}, {"reviewer", "reviewer"}} {
			expected := store.EndpointFromContext(objectField(task, entry.key))
			if current.Native().SameAuthority(expected.Native()) {
				return entry.role
			}
		}
		return "other"
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
	if err := validateNativeReview(s, taskID, candidate, toolName, endpoint); err != nil {
		return nil, err
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
	result.Set("note", "Findings saved. The task records no parent terminal, so no return was created and no one was notified. They do not verify the candidate or close anything.")
	return result, nil
}

func record(s *store.Store, taskID, verdict, candidate, toolName, text string, policyReviewed bool, notes []Note, made, endpoint *ordjson.Object) (*ordjson.Object, *ordjson.Object, error) {
	priorTask, err := s.ReadTask(taskID)
	if err != nil {
		return nil, nil, err
	}
	candidateSnapshot := reviewerCandidateState(priorTask)
	observedCandidate := ""
	if store.EndpointFromContext(endpoint).Role == "reviewer" {
		observedCandidate = evidenceview.CurrentCandidate(priorTask)
	}
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
	if role == "reviewer" {
		if candidate == "" || candidate != observedCandidate || reviewerCandidateState(task) != candidateSnapshot || candidate != asString(objectField(objectField(task, "reviewer"), "prompt"), "candidate") {
			return nil, nil, fmt.Errorf("review candidate changed or was not assigned to this reviewer")
		}
	}

	if role == "worker" {
		return nil, nil, fmt.Errorf("The worker terminal cannot record independent review of its own candidate. Save findings from the reviewer terminal, or record `verify` as the coordinator.")
	}
	if endpoint != nil && role == "other" && toolName == "" {
		return nil, nil, fmt.Errorf("independent review requires the recorded native reviewer endpoint and matching init receipt; use explicit reviewer binding for replacement")
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
	if role == "reviewer" {
		reviewer := objectField(task, "reviewer")
		prompt := objectField(reviewer, "prompt")
		if prompt != nil {
			receipt := ordjson.NewObject()
			receipt.Set("kind", "review")
			id, _ := record.Get("id")
			receipt.Set("evidence_id", id)
			receipt.Set("candidate", candidate)
			receipt.Set("endpoint", store.EndpointFromContext(endpoint).Object())
			receipt.Set("at", store.Now())
			prompt.Set("state", "applied")
			prompt.Set("application_receipt", receipt)
			if kickoff := objectField(reviewer, "kickoff"); validBoundKickoff(kickoff, store.EndpointFromContext(endpoint)) && asString(kickoff, "candidate") == candidate {
				kickoff.Set("state", "applied")
				kickoff.Set("application_receipt", receipt)
			}
			intent := objectField(reviewer, "native_launch")
			state := asString(intent, "state")
			if intent != nil && (state == "in-flight" || state == "uncertain") {
				intent.Set("reconciled_from", state)
				intent.Set("state", "created")
				intent.Set("reconciled_by", receipt)
			}
		}
	}
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
	result.Set("note", "Findings saved as a return to the parent. They do not verify the candidate or close anything; a reviewer terminal with saved findings is closable later, one without is not.")
	return result, task, nil
}
