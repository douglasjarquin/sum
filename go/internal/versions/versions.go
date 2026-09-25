package versions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/procedure"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	File   = "versions.json"
	Schema = 1
	// RefreshUnreachable is terminal delivery for a revision whose worker pane or agent is gone.
	// pending-unreachable remains retryable (other machine, cwd mismatch, prompt refusal).
	RefreshUnreachable = "unreachable"
)

func sha256Text(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func ApprovedFingerprint(task *ordjson.Object) *ordjson.Object {
	briefValue, _ := task.Get("brief")
	brief, _ := briefValue.(string)
	result := ordjson.NewObject()
	result.Set("sha256", sha256Text(brief))
	baseSha, _ := task.Get("base_sha")
	result.Set("base_sha", baseSha)
	repository, _ := task.Get("repository")
	result.Set("repository", repository)
	kind, _ := task.Get("kind")
	result.Set("kind", kind)
	return result
}

func legacyVersions(task *ordjson.Object) *ordjson.Object {
	idValue, _ := task.Get("id")
	id, _ := idValue.(string)
	briefPathValue, hasBriefPath := task.Get("brief_path")

	var revisions []any
	var active any
	if hasBriefPath && briefPathValue != nil {
		policy := ordjson.NewObject()
		policy.Set("sum_version", "0.1.0")
		policy.Set("brief_schema", jsonInt(1))
		rev := ordjson.NewObject()
		rev.Set("id", "legacy")
		rev.Set("path", "brief.md")
		rev.Set("status", "active")
		rev.Set("legacy", true)
		rev.Set("sha256", nil)
		rev.Set("policy", policy)
		revisions = []any{rev}
		active = "legacy"
	} else {
		revisions = []any{}
		active = nil
	}

	runtime := ordjson.NewObject()
	runtime.Set("sum_version", "0.1.0")
	runtime.Set("assumed", true)

	result := ordjson.NewObject()
	result.Set("schema", jsonInt(Schema))
	result.Set("task", id)
	result.Set("legacy", true)
	result.Set("runtime", runtime)
	result.Set("brief_schema", jsonInt(1))
	result.Set("approved", ApprovedFingerprint(task))
	result.Set("revisions", revisions)
	result.Set("active", active)
	result.Set("requested", nil)
	result.Set("refresh", []any{})
	return result
}

func ReadVersions(s *store.Store, task *ordjson.Object) (*ordjson.Object, error) {
	idValue, _ := task.Get("id")
	id, _ := idValue.(string)
	taskPath, err := s.TaskPath(id)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(taskPath, File)
	info, statErr := os.Lstat(path)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return legacyVersions(task), nil
		}
		return nil, statErr
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must not be a symlink", path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file", path)
	}
	value, err := ordjson.ReadFile(path)
	if err != nil {
		return nil, err
	}
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("%s is not a JSON object", path)
	}
	schemaOK := false
	if schema, has := obj.Get("schema"); has {
		if num, isNum := schema.(json.Number); isNum {
			if n, convErr := num.Int64(); convErr == nil && n == Schema {
				schemaOK = true
			}
		}
	}
	taskField, _ := obj.Get("task")
	if !schemaOK || taskField != id {
		return nil, fmt.Errorf("unsupported or mismatched version sidecar %s. Inspect it; sum never migrates it in place.", path)
	}
	return obj, nil
}

func revisionFile(base string, revision *ordjson.Object) (string, error) {
	idValue, _ := revision.Get("id")
	pathValue, _ := revision.Get("path")
	relative, _ := pathValue.(string)
	invalid := filepath.IsAbs(relative)
	if !invalid {
		for _, part := range strings.Split(relative, "/") {
			if part == ".." {
				invalid = true
				break
			}
		}
	}
	if invalid {
		return "", fmt.Errorf("Revision %v has an invalid path %v.", idValue, pathValue)
	}
	return filepath.Join(base, filepath.FromSlash(relative)), nil
}

func RevisionView(base string, revision *ordjson.Object) *ordjson.Object {
	row := ordjson.NewObject()
	for _, key := range []string{"id", "status", "created_at", "policy", "summary", "verification_affected", "legacy"} {
		value, _ := revision.Get(key)
		row.Set(key, value)
	}

	path, err := revisionFile(base, revision)
	if err == nil {
		row.Set("path", path)
		info, statErr := os.Lstat(path)
		switch {
		case statErr == nil && info.Mode()&os.ModeSymlink != 0:
			err = fmt.Errorf("file is missing")
		case statErr != nil:
			err = fmt.Errorf("file is missing")
		default:
			if fileInfo, fiErr := os.Stat(path); fiErr != nil || fileInfo.IsDir() {
				err = fmt.Errorf("file is missing")
			} else {
				content, readErr := os.ReadFile(path)
				if readErr != nil {
					err = readErr
				} else {
					actual := sha256Text(string(content))
					recordedShaValue, hasSha := revision.Get("sha256")
					recordedSha, _ := recordedShaValue.(string)
					policyValue, _ := revision.Get("policy")
					policy, _ := policyValue.(*ordjson.Object)
					if hasSha && recordedSha != "" && actual != recordedSha {
						err = fmt.Errorf("content hash %s does not match the recorded %s", actual[:12], recordedSha[:12])
					} else if procErr := procedure.Verify(base, procedure.Rows(policy)); procErr != nil {
						err = procErr
					} else {
						row.Set("ok", true)
						row.Set("sha256", actual)
					}
				}
			}
		}
	}
	if err != nil {
		idValue, _ := revision.Get("id")
		row.Set("ok", false)
		row.Set("error", fmt.Sprintf("Revision %v is not usable: %s. Do not request it; regenerate a new revision instead.", idValue, err))
	}
	return row
}

func RefreshState(versionsObj *ordjson.Object) *ordjson.Object {
	revisionsValue, _ := versionsObj.Get("revisions")
	revisionList, _ := revisionsValue.([]any)
	var latest *ordjson.Object
	if len(revisionList) > 0 {
		latest, _ = revisionList[len(revisionList)-1].(*ordjson.Object)
	}
	requestedValue, _ := versionsObj.Get("requested")
	activeValue, _ := versionsObj.Get("active")

	row := ordjson.NewObject()
	row.Set("active", activeValue)
	row.Set("requested", requestedValue)
	if latest != nil {
		latestID, _ := latest.Get("id")
		row.Set("latest", latestID)
	} else {
		row.Set("latest", nil)
	}

	requested, requestedIsString := requestedValue.(string)
	if !requestedIsString || requested == "" {
		if latest != nil {
			latestID, _ := latest.Get("id")
			if activeValue == latestID {
				refreshValue, _ := versionsObj.Get("refresh")
				refreshList, _ := refreshValue.([]any)
				var receipt *ordjson.Object
				for i := len(refreshList) - 1; i >= 0; i-- {
					entry, _ := refreshList[i].(*ordjson.Object)
					event, _ := entry.Get("event")
					revision, _ := entry.Get("revision")
					if event == "adopted" && revision == latestID {
						receipt = entry
						break
					}
				}
				latestIDStr, _ := latestID.(string)
				reason := fmt.Sprintf("%s is the revision this session started with", latestIDStr)
				if receipt != nil {
					at, _ := receipt.Get("at")
					reason = fmt.Sprintf("receipt for %s recorded at %v", latestIDStr, at)
				}
				row.Set("state", "confirmed")
				row.Set("reason", reason+"; a receipt shows the revision was read, not that it is obeyed")
			} else {
				row.Set("state", "not-requested")
				row.Set("reason", "no refresh is requested for this target")
			}
		} else {
			row.Set("state", "not-requested")
			row.Set("reason", "no refresh is requested for this target")
		}
		return row
	}

	refreshValue, _ := versionsObj.Get("refresh")
	refreshList, _ := refreshValue.([]any)
	var deliveries []*ordjson.Object
	for _, e := range refreshList {
		entry, _ := e.(*ordjson.Object)
		event, _ := entry.Get("event")
		revision, _ := entry.Get("revision")
		if event == "delivery" && revision == requested {
			deliveries = append(deliveries, entry)
		}
	}
	if len(deliveries) == 0 {
		row.Set("state", "pending-unreachable")
		row.Set("reason", "requested; no delivery attempt recorded yet")
		return row
	}
	last := deliveries[len(deliveries)-1]
	state, _ := last.Get("state")
	reason, _ := last.Get("reason")
	at, _ := last.Get("at")
	observed, hasObserved := last.Get("observed")
	row.Set("state", state)
	row.Set("reason", reason)
	row.Set("attempted_at", at)
	row.Set("attempts", jsonInt(len(deliveries)))
	if hasObserved {
		row.Set("observed", observed)
	} else {
		row.Set("observed", nil)
	}
	return row
}

// RefreshWorkerGone reports a recorded delivery that cannot reach the worker because
// the pane or agent is absent. That revision's delivery is terminal; it is not a live
// "needs the worker" obligation.
func RefreshWorkerGone(state *ordjson.Object) bool {
	if state == nil {
		return false
	}
	st, _ := state.Get("state")
	s, _ := st.(string)
	if s == RefreshUnreachable {
		return true
	}
	reason, _ := state.Get("reason")
	r, _ := reason.(string)
	return strings.Contains(r, "agent_not_found") || strings.Contains(r, "pane_not_found")
}

const ContractDir = "coordinator"

func emptyContractVersions() *ordjson.Object {
	result := ordjson.NewObject()
	result.Set("schema", jsonInt(Schema))
	result.Set("kind", "coordinator-contract")
	result.Set("revisions", []any{})
	result.Set("active", nil)
	result.Set("requested", nil)
	result.Set("refresh", []any{})
	return result
}

func ReadContractVersions(s *store.Store) (*ordjson.Object, error) {
	path := filepath.Join(s.Home, ContractDir, File)
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must not be a symlink.", path)
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return emptyContractVersions(), nil
	}
	value, err := ordjson.ReadFile(path)
	if err != nil {
		return nil, err
	}
	obj, ok := value.(*ordjson.Object)
	schemaOK := false
	if ok {
		if schema, has := obj.Get("schema"); has {
			if num, isNum := schema.(json.Number); isNum {
				if n, convErr := num.Int64(); convErr == nil && n == Schema {
					schemaOK = true
				}
			}
		}
	}
	kind, _ := obj.Get("kind")
	if !ok || !schemaOK || kind != "coordinator-contract" {
		return nil, fmt.Errorf("Unsupported coordinator contract sidecar %s. Inspect it; sum never migrates it in place.", path)
	}
	return obj, nil
}

func ContractState(s *store.Store) *ordjson.Object {
	versionsObj, err := ReadContractVersions(s)
	if err != nil {
		result := ordjson.NewObject()
		result.Set("error", err.Error())
		return result
	}
	row := RefreshState(versionsObj)
	requested, _ := row.Get("requested")
	requestedStr, ok := requested.(string)
	if ok && requestedStr != "" {
		revisionsValue, _ := versionsObj.Get("revisions")
		list, _ := revisionsValue.([]any)
		for _, raw := range list {
			rev, _ := raw.(*ordjson.Object)
			id, _ := rev.Get("id")
			if id == requestedStr {
				path, pathErr := revisionFile(filepath.Join(s.Home, ContractDir), rev)
				if pathErr == nil {
					row.Set("path", path)
				}
				summary, _ := rev.Get("summary")
				row.Set("summary", summary)
				break
			}
		}
	}
	return row
}

func WriteContractVersions(s *store.Store, value *ordjson.Object) error {
	return ordjson.WriteFile(filepath.Join(s.Home, ContractDir, File), value)
}

func WriteVersions(s *store.Store, value *ordjson.Object) error {
	taskID, _ := value.Get("task")
	id, _ := taskID.(string)
	taskPath, err := s.TaskPath(id)
	if err != nil {
		return err
	}
	return ordjson.WriteFile(filepath.Join(taskPath, File), value)
}

func Request(s *store.Store, taskID, revisionID string) (*ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	versionsObj, err := ReadVersions(s, task)
	if err != nil {
		return nil, err
	}
	if legacy, _ := versionsObj.Get("legacy"); legacy == true {
		return nil, fmt.Errorf("This task has no staged revisions; run `brief regenerate` first.")
	}
	revisionsValue, _ := versionsObj.Get("revisions")
	list, _ := revisionsValue.([]any)
	if len(list) == 0 {
		return nil, fmt.Errorf("This task has no staged revisions; run `brief regenerate` first.")
	}
	latest, _ := list[len(list)-1].(*ordjson.Object)
	var target *ordjson.Object
	ids := make([]string, 0, len(list))
	for _, raw := range list {
		rev, _ := raw.(*ordjson.Object)
		id, _ := rev.Get("id")
		idStr, _ := id.(string)
		ids = append(ids, idStr)
		if idStr == revisionID {
			target = rev
		}
	}
	if target == nil {
		return nil, fmt.Errorf("Unknown revision %s. Recorded: %v.", revisionID, ids)
	}
	latestID, _ := latest.Get("id")
	if latestID != revisionID {
		return nil, fmt.Errorf("Stale request: %s is superseded by %v. Request the latest revision or regenerate.", revisionID, latestID)
	}
	active, _ := versionsObj.Get("active")
	if active == revisionID {
		return nil, fmt.Errorf("%s is already the active brief.", revisionID)
	}
	taskPath, err := s.TaskPath(taskID)
	if err != nil {
		return nil, err
	}
	state := RevisionView(taskPath, target)
	if ok, _ := state.Get("ok"); ok != true {
		errText, _ := state.Get("error")
		return nil, fmt.Errorf("%v", errText)
	}
	duplicate := markRequested(versionsObj, target)
	if err := WriteVersions(s, versionsObj); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("requested", revisionID)
	activeNow, _ := versionsObj.Get("active")
	result.Set("active", activeNow)
	result.Set("revision", state)
	result.Set("duplicate", duplicate)
	result.Set("note", "Recorded only. Delivery to the worker is a separate explicit step; the notice slot was not used.")
	return result, nil
}

func markRequested(versionsObj, target *ordjson.Object) bool {
	requested, _ := versionsObj.Get("requested")
	status, _ := target.Get("status")
	id, _ := target.Get("id")
	if requested == id && status == "requested" {
		return true
	}
	revisionsValue, _ := versionsObj.Get("revisions")
	list, _ := revisionsValue.([]any)
	for _, raw := range list {
		rev, _ := raw.(*ordjson.Object)
		if st, _ := rev.Get("status"); st == "requested" {
			rev.Set("status", "superseded")
		}
	}
	target.Set("status", "requested")
	versionsObj.Set("requested", id)
	refreshValue, _ := versionsObj.Get("refresh")
	refresh, _ := refreshValue.([]any)
	row := ordjson.NewObject()
	row.Set("at", store.Now())
	row.Set("event", "requested")
	row.Set("revision", id)
	row.Set("by", "coordinator")
	versionsObj.Set("refresh", append(refresh, row))
	return false
}

func Adopt(s *store.Store, taskID, revisionID string) (*ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	versionsObj, err := ReadVersions(s, task)
	if err != nil {
		return nil, err
	}
	legacy, _ := versionsObj.Get("legacy")
	requested, _ := versionsObj.Get("requested")
	if legacy == true || requested != revisionID {
		return nil, fmt.Errorf("%s is not the requested revision (%v). Adopt only what the coordinator requested.", revisionID, requested)
	}
	revisionsValue, _ := versionsObj.Get("revisions")
	list, _ := revisionsValue.([]any)
	var target *ordjson.Object
	for _, raw := range list {
		rev, _ := raw.(*ordjson.Object)
		id, _ := rev.Get("id")
		if id == revisionID {
			target = rev
			break
		}
	}
	taskPath, err := s.TaskPath(taskID)
	if err != nil {
		return nil, err
	}
	state := RevisionView(taskPath, target)
	if ok, _ := state.Get("ok"); ok != true {
		errText, _ := state.Get("error")
		return nil, fmt.Errorf("%v", errText)
	}
	for _, raw := range list {
		rev, _ := raw.(*ordjson.Object)
		if st, _ := rev.Get("status"); st == "active" {
			rev.Set("status", "superseded")
		}
	}
	target.Set("status", "active")
	versionsObj.Set("active", revisionID)
	versionsObj.Set("requested", nil)
	refreshValue, _ := versionsObj.Get("refresh")
	refresh, _ := refreshValue.([]any)
	row := ordjson.NewObject()
	row.Set("at", store.Now())
	row.Set("event", "adopted")
	row.Set("revision", revisionID)
	versionsObj.Set("refresh", append(refresh, row))
	if err := WriteVersions(s, versionsObj); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("active", revisionID)
	result.Set("revision", state)
	result.Set("note", "Receipt recorded: this revision was read and adopted. A receipt is evidence of reading, not proof the worker follows it.")
	return result, nil
}

func BriefList(s *store.Store, taskID string) (*ordjson.Object, error) {
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	view, err := View(s, task)
	if err != nil {
		return nil, err
	}
	briefPathValue, _ := task.Get("brief_path")
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("brief_path", briefPathValue)
	for _, key := range view.Keys() {
		v, _ := view.Get(key)
		result.Set(key, v)
	}
	return result, nil
}

func View(s *store.Store, task *ordjson.Object) (*ordjson.Object, error) {
	versionsObj, err := ReadVersions(s, task)
	if err != nil {
		return nil, err
	}
	idValue, _ := task.Get("id")
	id, _ := idValue.(string)
	taskPath, err := s.TaskPath(id)
	if err != nil {
		return nil, err
	}

	revisionsValue, _ := versionsObj.Get("revisions")
	revisionList, ok := revisionsValue.([]any)
	if !ok {
		return nil, fmt.Errorf("%s revisions must be an array", filepath.Join(taskPath, File))
	}
	revisionViews := make([]any, 0, len(revisionList))
	ids := make([]string, 0, len(revisionList))
	for i, r := range revisionList {
		rev, ok := r.(*ordjson.Object)
		if !ok || rev == nil {
			return nil, fmt.Errorf("%s revisions[%d] must be an object", filepath.Join(taskPath, File), i)
		}
		revisionViews = append(revisionViews, RevisionView(taskPath, rev))
		revID, _ := rev.Get("id")
		revIDStr, _ := revID.(string)
		ids = append(ids, revIDStr)
	}

	var evidence any
	if reportValue, hasReport := task.Get("report"); hasReport && reportValue != nil {
		report, ok := reportValue.(*ordjson.Object)
		if !ok || report == nil {
			return nil, fmt.Errorf("task %s report must be an object or null", id)
		}
		madeUnderValue, _ := report.Get("brief_revision")
		madeUnder, madeUnderIsString := madeUnderValue.(string)
		if !madeUnderIsString {
			submittedAtValue, _ := report.Get("submitted_at")
			submittedAt, _ := submittedAtValue.(string)
			for _, r := range revisionList {
				rev, _ := r.(*ordjson.Object)
				statusValue, _ := rev.Get("status")
				status, _ := statusValue.(string)
				createdAtValue, _ := rev.Get("created_at")
				createdAt, _ := createdAtValue.(string)
				if (status == "active" || status == "superseded") && createdAt <= submittedAt {
					revID, _ := rev.Get("id")
					madeUnder, _ = revID.(string)
					madeUnderIsString = true
					break
				}
			}
		}
		var later []any
		if madeUnderIsString {
			index := -1
			for i, candidateID := range ids {
				if candidateID == madeUnder {
					index = i
					break
				}
			}
			if index >= 0 {
				later = revisionList[index+1:]
			} else {
				later = revisionList
			}
		} else {
			later = revisionList
		}
		verificationChanged := false
		for _, r := range later {
			rev, _ := r.(*ordjson.Object)
			if v, _ := rev.Get("verification_affected"); v == true {
				verificationChanged = true
				break
			}
		}
		ev := ordjson.NewObject()
		if madeUnderIsString {
			ev.Set("brief_revision", madeUnder)
		} else {
			ev.Set("brief_revision", nil)
		}
		sumVersionValue, _ := report.Get("sum_version")
		ev.Set("sum_version", sumVersionValue)
		contractMoved := contractChanged(task)
		ev.Set("contract_changed_since_dispatch", contractMoved)
		ev.Set("verification_policy_changed_since", verificationChanged || contractMoved)
		ev.Set("note", "Evidence is bound to the candidate and the brief revision it was produced under. A later verification-affecting revision, or a VERIFY.md that no longer hashes to what dispatch recorded, means the evidence needs refresh review, not automatic rejection or approval.")
		evidence = ev
	}

	result := ordjson.NewObject()
	for _, key := range []string{"schema", "legacy", "runtime", "brief_schema", "approved", "active", "requested", "refresh"} {
		value, _ := versionsObj.Get(key)
		result.Set(key, value)
	}
	result.Set("revisions", revisionViews)
	result.Set("report_evidence", evidence)
	result.Set("note", "Revisions are staged files; the worker keeps reading its current brief until a refresh is explicitly requested and adopted.")
	return result, nil
}

// contractChanged compares the VERIFY.md hash the worker's own run saw against the one dispatch
// recorded. A candidate that edited the gate certifying it is evidence to review, never to accept.
func contractChanged(task *ordjson.Object) bool {
	policyValue, _ := task.Get("verification_policy")
	policy, _ := policyValue.(*ordjson.Object)
	if policy == nil {
		return false
	}
	dispatchedValue, _ := policy.Get("contract_sha256")
	dispatched, _ := dispatchedValue.(string)
	if dispatched == "" {
		return false
	}
	evidenceValue, _ := task.Get("evidence")
	records, _ := evidenceValue.([]any)
	for _, raw := range records {
		record, _ := raw.(*ordjson.Object)
		if record == nil {
			continue
		}
		kindValue, _ := record.Get("kind")
		if kind, _ := kindValue.(string); kind != "verification" {
			continue
		}
		ranValue, _ := record.Get("contract_sha256")
		if ran, _ := ranValue.(string); ran != "" && ran != dispatched {
			return true
		}
	}
	return false
}

// SameJSON reports whether two recorded values encode identically with sorted keys.
func SameJSON(a, b any) bool {
	encodedA, errA := ordjson.MarshalSortedCompact(a)
	encodedB, errB := ordjson.MarshalSortedCompact(b)
	return errA == nil && errB == nil && string(encodedA) == string(encodedB)
}

// DecisionsOnly reports whether a requested revision differs from the task's active revision only
// in recorded decisions: same policy (pinned procedure included), return commands, and approved
// fingerprint. The active revision is the baseline because it is what the worker actually read; a
// procedure change staged in between and never adopted keeps a later revision from qualifying.
func DecisionsOnly(versionsObj, target *ordjson.Object) bool {
	if versionsObj == nil || target == nil {
		return false
	}
	activeID, _ := versionsObj.Get("active")
	targetID, _ := target.Get("id")
	if activeID == nil || activeID == targetID {
		return false
	}
	revisionsValue, _ := versionsObj.Get("revisions")
	list, _ := revisionsValue.([]any)
	var active *ordjson.Object
	for _, raw := range list {
		rev, _ := raw.(*ordjson.Object)
		if id, _ := rev.Get("id"); id == activeID {
			active = rev
		}
	}
	if active == nil {
		return false
	}
	for _, key := range []string{"policy", "commands", "approved"} {
		before, hasBefore := active.Get(key)
		after, hasAfter := target.Get(key)
		if !hasBefore || !hasAfter || before == nil || after == nil || !SameJSON(before, after) {
			return false
		}
	}
	return true
}

// ActiveRevision is the revision a sidecar marks active, or nil.
func ActiveRevision(versionsObj *ordjson.Object) *ordjson.Object {
	if versionsObj == nil {
		return nil
	}
	activeID, _ := versionsObj.Get("active")
	revisionsValue, _ := versionsObj.Get("revisions")
	list, _ := revisionsValue.([]any)
	for _, raw := range list {
		rev, _ := raw.(*ordjson.Object)
		if rev == nil {
			continue
		}
		if id, _ := rev.Get("id"); activeID != nil && id == activeID {
			return rev
		}
	}
	return nil
}

// ActiveBrief is the file a freshly launched worker is told to read: the active revision, verified
// together with its pinned procedure. Launch refuses rather than prompting an incomplete brief.
func ActiveBrief(s *store.Store, task *ordjson.Object) (string, error) {
	versionsObj, err := ReadVersions(s, task)
	if err != nil {
		return "", err
	}
	active := ActiveRevision(versionsObj)
	if active == nil {
		return "", fmt.Errorf("The task has no active brief revision; no worker was started.")
	}
	idValue, _ := task.Get("id")
	id, _ := idValue.(string)
	taskPath, err := s.TaskPath(id)
	if err != nil {
		return "", err
	}
	view := RevisionView(taskPath, active)
	if ok, _ := view.Get("ok"); ok != true {
		errText, _ := view.Get("error")
		return "", fmt.Errorf("No worker was started: %v", errText)
	}
	path, _ := view.Get("path")
	file, _ := path.(string)
	return file, nil
}
