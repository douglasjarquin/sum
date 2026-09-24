package brief

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/procedure"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

var revisionIDPattern = regexp.MustCompile(`^r[1-9][0-9]*$`)

func DecisionRecords(task *ordjson.Object) []any {
	var rows []any
	questions, _ := task.Get("questions")
	list, _ := questions.([]any)
	for _, raw := range list {
		q, _ := raw.(*ordjson.Object)
		if q == nil {
			continue
		}
		row := ordjson.NewObject()
		id, _ := q.Get("id")
		row.Set("id", id)
		key, _ := q.Get("key")
		row.Set("key", key)
		status, _ := q.Get("status")
		row.Set("status", status)
		answer, _ := q.Get("answer")
		row.Set("answer", answer)
		rows = append(rows, row)
	}
	return rows
}

func objectField(o *ordjson.Object, key string) *ordjson.Object {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	obj, _ := v.(*ordjson.Object)
	return obj
}

func revisionFingerprint(task, policy *ordjson.Object, decisions []any, commands *ordjson.Object) (string, error) {
	payload := ordjson.NewObject()
	payload.Set("approved", versions.ApprovedFingerprint(task))
	payload.Set("policy", policy)
	payload.Set("decisions", decisions)
	payload.Set("commands", commands)
	contractObj := ordjson.NewObject()
	for _, k := range []string{"worktree", "branch", "harness"} {
		v, _ := task.Get(k)
		contractObj.Set(k, v)
	}
	payload.Set("contract", contractObj)
	data, err := ordjson.MarshalSortedCompact(payload)
	if err != nil {
		return "", err
	}
	return sha256Text(string(data)), nil
}

func nextRevisionID(s *store.Store, taskID string, versionsObj *ordjson.Object) (string, error) {
	numbers := []int{}
	revisions, _ := versionsObj.Get("revisions")
	list, _ := revisions.([]any)
	for _, raw := range list {
		rev, _ := raw.(*ordjson.Object)
		id := asString(func() any { v, _ := rev.Get("id"); return v }())
		if revisionIDPattern.MatchString(id) {
			n, _ := strconv.Atoi(id[1:])
			numbers = append(numbers, n)
		}
	}
	taskPath, err := s.TaskPath(taskID)
	if err != nil {
		return "", err
	}
	briefs := filepath.Join(taskPath, "briefs")
	if entries, err := os.ReadDir(briefs); err == nil {
		for _, entry := range entries {
			stem := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			if revisionIDPattern.MatchString(stem) {
				n, _ := strconv.Atoi(stem[1:])
				numbers = append(numbers, n)
			}
		}
	}
	max := 0
	for _, n := range numbers {
		if n > max {
			max = n
		}
	}
	return fmt.Sprintf("r%d", max+1), nil
}

func revisionSummary(previous, policy *ordjson.Object, decisions []any, commands, approved *ordjson.Object) ([]any, bool, error) {
	if previous == nil {
		return []any{"initial brief"}, false, nil
	}
	prevApproved := objectField(previous, "approved")
	if prevApproved != nil && approved != nil {
		for _, k := range []string{"sha256", "base_sha", "repository", "kind"} {
			a, _ := prevApproved.Get(k)
			b, _ := approved.Get(k)
			if fmt.Sprint(a) != fmt.Sprint(b) {
				return nil, false, fmt.Errorf("Approved task record changed since the previous revision; the approved body, base, repository, and kind are immutable input. Inspect the task before regenerating.")
			}
		}
	}
	var changes []any
	verification := false
	prevPolicy := objectField(previous, "policy")
	for _, key := range []string{"sum_version", "brief_schema"} {
		before, _ := prevPolicy.Get(key)
		after, _ := policy.Get(key)
		if fmt.Sprint(before) != fmt.Sprint(after) {
			changes = append(changes, fmt.Sprintf("%s: %v -> %v", key, before, after))
			if key == "brief_schema" {
				verification = true
			}
		}
	}
	beforeSkill, _ := prevPolicy.Get("worker_skill_sha256")
	afterSkill, _ := policy.Get("worker_skill_sha256")
	if fmt.Sprint(beforeSkill) != fmt.Sprint(afterSkill) {
		before := fmt.Sprint(beforeSkill)
		after := fmt.Sprint(afterSkill)
		if len(before) > 12 {
			before = before[:12]
		}
		if len(after) > 12 {
			after = after[:12]
		}
		changes = append(changes, fmt.Sprintf("worker procedure changed: %s -> %s", before, after))
		verification = true
	} else if beforeRows, afterRows := procedure.Rows(prevPolicy), procedure.Rows(policy); beforeRows == nil && afterRows != nil {
		changes = append(changes, "worker procedure is now a pinned task resource instead of a copy in the brief")
	} else if !versions.SameJSON(beforeRows, afterRows) {
		changes = append(changes, "worker procedure resources changed")
		verification = true
	}
	beforeDecisions := map[string]*ordjson.Object{}
	prevList, _ := previous.Get("decisions")
	for _, raw := range func() []any { list, _ := prevList.([]any); return list }() {
		d, _ := raw.(*ordjson.Object)
		id := asString(func() any { v, _ := d.Get("id"); return v }())
		beforeDecisions[id] = d
	}
	for _, raw := range decisions {
		d, _ := raw.(*ordjson.Object)
		id := asString(func() any { v, _ := d.Get("id"); return v }())
		prev, ok := beforeDecisions[id]
		if !ok {
			changes = append(changes, fmt.Sprintf("decision %s recorded (%s)", id, asString(func() any { v, _ := d.Get("status"); return v }())))
			continue
		}
		if asString(func() any { v, _ := prev.Get("status"); return v }()) != asString(func() any { v, _ := d.Get("status"); return v }()) ||
			fmt.Sprint(func() any { v, _ := prev.Get("answer"); return v }()) != fmt.Sprint(func() any { v, _ := d.Get("answer"); return v }()) {
			changes = append(changes, fmt.Sprintf("decision %s: %s -> %s", id, asString(func() any { v, _ := prev.Get("status"); return v }()), asString(func() any { v, _ := d.Get("status"); return v }())))
		}
	}
	prevCommands := objectField(previous, "commands")
	if prevCommands != nil && commands != nil {
		encodedPrev, _ := ordjson.MarshalSortedCompact(prevCommands)
		encodedNow, _ := ordjson.MarshalSortedCompact(commands)
		if string(encodedPrev) != string(encodedNow) {
			changes = append(changes, "return-channel commands changed")
		}
	} else if (prevCommands == nil) != (commands == nil) {
		changes = append(changes, "return-channel commands changed")
	}
	if len(changes) == 0 {
		changes = []any{"no recorded change"}
	}
	return changes, verification, nil
}

func pickApproved(approved *ordjson.Object) *ordjson.Object {
	result := ordjson.NewObject()
	for _, k := range []string{"sha256", "base_sha", "repository", "kind"} {
		v, _ := approved.Get(k)
		result.Set(k, v)
	}
	return result
}

func fingerprintsEqual(recorded, current *ordjson.Object) bool {
	if recorded == nil {
		return true
	}
	for _, k := range []string{"sha256", "base_sha", "repository", "kind"} {
		a, _ := recorded.Get(k)
		b, _ := current.Get(k)
		if fmt.Sprint(a) != fmt.Sprint(b) {
			return false
		}
	}
	return true
}

func Regenerate(s *store.Store, runtimeRoot, sumctlPath, taskID string) (*ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	if func() any { v, _ := task.Get("brief_path"); return v }() == nil || asString(func() any { v, _ := task.Get("worktree"); return v }()) == "" {
		return nil, fmt.Errorf("This task has no brief to regenerate; prepare it first.")
	}
	versionsObj, err := versions.ReadVersions(s, task)
	if err != nil {
		return nil, err
	}
	approved := versions.ApprovedFingerprint(task)
	if legacy, _ := versionsObj.Get("legacy"); legacy == true {
		revisions, _ := versionsObj.Get("revisions")
		list, _ := revisions.([]any)
		var recorded *ordjson.Object
		if len(list) > 0 {
			recorded, _ = list[0].(*ordjson.Object)
		}
		taskPath, pathErr := s.TaskPath(taskID)
		if pathErr != nil {
			return nil, pathErr
		}
		legacyPath := filepath.Join(taskPath, "brief.md")
		var legacyHash any
		if data, readErr := os.ReadFile(legacyPath); readErr == nil {
			legacyHash = sha256Text(string(data))
		}
		runtime := ordjson.NewObject()
		runtime.Set("sum_version", contract.SumVersion)
		runtime.Set("brief_schema", jsonInt(contract.BriefSchema))
		runtime.Set("assumed", true)
		runtime.Set("recorded_at", store.Now())
		approvedCopy := versions.ApprovedFingerprint(task)
		approvedCopy.Set("recorded_at", store.Now())
		approvedCopy.Set("from_legacy_record", true)
		var newRevisions []any
		var active any
		if recorded != nil {
			rev := ordjson.NewObject()
			for _, k := range recorded.Keys() {
				v, _ := recorded.Get(k)
				rev.Set(k, v)
			}
			rev.Set("id", "r1")
			rev.Set("sha256", legacyHash)
			rev.Set("approved", approved)
			rev.Set("decisions", nil)
			rev.Set("commands", nil)
			rev.Set("summary", []any{"legacy brief adopted as r1; its policy and decisions were not recorded at dispatch"})
			rev.Set("verification_affected", false)
			newRevisions = []any{rev}
			active = "r1"
		}
		versionsObj = ordjson.NewObject()
		versionsObj.Set("schema", jsonInt(versions.Schema))
		versionsObj.Set("task", taskID)
		versionsObj.Set("legacy", false)
		versionsObj.Set("runtime", runtime)
		versionsObj.Set("brief_schema", jsonInt(contract.BriefSchema))
		versionsObj.Set("approved", approvedCopy)
		versionsObj.Set("revisions", newRevisions)
		versionsObj.Set("active", active)
		versionsObj.Set("requested", nil)
		versionsObj.Set("refresh", []any{})
	}
	recordedApproved := objectField(versionsObj, "approved")
	if recordedApproved != nil && !fingerprintsEqual(recordedApproved, approved) {
		return nil, fmt.Errorf("Approved task record differs from the recorded fingerprint; the approved body, base, repository, and kind are immutable. Inspect the task; nothing was regenerated.")
	}
	taskPath, err := s.TaskPath(taskID)
	if err != nil {
		return nil, err
	}
	rows, err := procedure.Pin(runtimeRoot, taskPath)
	if err != nil {
		return nil, err
	}
	policy := policyFor(rows)
	decisions := DecisionRecords(task)
	commands := Commands(sumctlPath, s.Home, taskID)
	fingerprint, err := revisionFingerprint(task, policy, decisions, commands)
	if err != nil {
		return nil, err
	}
	revisions, _ := versionsObj.Get("revisions")
	list, _ := revisions.([]any)
	var previous *ordjson.Object
	if len(list) > 0 {
		previous, _ = list[len(list)-1].(*ordjson.Object)
	}
	if previous != nil && asString(func() any { v, _ := previous.Get("fingerprint"); return v }()) == fingerprint {
		state := versions.RevisionView(taskPath, previous)
		if ok, _ := state.Get("ok"); ok == true {
			result := ordjson.NewObject()
			result.Set("task", taskID)
			result.Set("duplicate", true)
			result.Set("revision", state)
			result.Set("note", "Nothing changed since the latest revision; no file was written.")
			return result, nil
		}
	}
	var summary []any
	var verification bool
	if previous != nil && func() any { v, _ := previous.Get("decisions"); return v }() == nil {
		summary = []any{fmt.Sprintf("regenerated from a legacy brief; first revision with recorded policy and decisions (%d decisions)", len(decisions))}
		verification = true
	} else {
		var sumErr error
		summary, verification, sumErr = revisionSummary(previous, policy, decisions, commands, approved)
		if sumErr != nil {
			return nil, sumErr
		}
	}
	rid, err := nextRevisionID(s, taskID, versionsObj)
	if err != nil {
		return nil, err
	}
	var previousID any
	if previous != nil {
		previousID, _ = previous.Get("id")
	}
	text := render(s, sumctlPath, briefData{Task: task, TaskDir: taskPath, Revision: rid, Previous: asString(previousID), Summary: summary, Policy: policy, Decisions: decisions, Commands: commands})
	relative := "briefs/" + rid + ".md"
	if err := writeOnce(filepath.Join(taskPath, filepath.FromSlash(relative)), text); err != nil {
		return nil, err
	}
	revision := ordjson.NewObject()
	revision.Set("id", rid)
	revision.Set("path", relative)
	revision.Set("status", "staged")
	revision.Set("created_at", store.Now())
	revision.Set("sha256", sha256Text(text))
	revision.Set("fingerprint", fingerprint)
	revision.Set("policy", policy)
	revision.Set("decisions", decisions)
	revision.Set("commands", commands)
	revision.Set("approved", approved)
	revision.Set("summary", summary)
	revision.Set("verification_affected", verification)
	revision.Set("previous", previousID)
	versionsObj.Set("revisions", append(list, revision))
	if err := versions.WriteVersions(s, versionsObj); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("duplicate", false)
	result.Set("revision", versions.RevisionView(taskPath, revision))
	active, _ := versionsObj.Get("active")
	result.Set("active", active)
	result.Set("note", "Staged only. The worker's current brief and brief_path are unchanged; use `brief request` to ask for a refresh explicitly.")
	return result, nil
}
