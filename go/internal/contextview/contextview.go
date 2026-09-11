package contextview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/cleanup"
	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/evidenceview"
	"github.com/douglasjarquin/sum/go/internal/graphview"
	"github.com/douglasjarquin/sum/go/internal/notes"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

// ContextSections mirrors CONTEXT_SECTIONS: the section names `--section` accepts, and what `outline.read.sections` reports.
var ContextSections = []string{"outline", "brief", "decisions", "handoff", "evidence", "execution", "environment", "update", "returns", "notes"}

var stateFields = []string{"status", "pane", "session", "machine", "parent", "reviewer", "worktree", "branch", "cleanup", "pr", "error"}
var cursorFields = []string{"questions", "evidence", "answered", "applied", "notes", "refresh", "attention"}
var outlineTaskKeys = []string{"id", "status", "kind", "repository", "branch", "worktree", "harness", "error"}

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func sha256Text(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case json.Number:
		f, err := t.Float64()
		return err != nil || f != 0
	case []any:
		return len(t) > 0
	case *ordjson.Object:
		return t != nil && t.Len() > 0
	default:
		return v != nil
	}
}

func asObject(v any) *ordjson.Object {
	obj, _ := v.(*ordjson.Object)
	return obj
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func getField(o *ordjson.Object, key string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	return v
}

func listField(o *ordjson.Object, key string) []any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	list, _ := v.([]any)
	return list
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func pick(o *ordjson.Object, keys []string) *ordjson.Object {
	result := ordjson.NewObject()
	for _, k := range keys {
		result.Set(k, getField(o, k))
	}
	return result
}

// outstanding ports `outstanding`: every decision not yet applied, in full.
func outstanding(task *ordjson.Object) []any {
	rows := make([]any, 0)
	for _, qv := range listField(task, "questions") {
		q := asObject(qv)
		if asString(getField(q, "status")) == "applied" {
			continue
		}
		row := ordjson.NewObject()
		row.Set("id", getField(q, "id"))
		row.Set("key", getField(q, "key"))
		row.Set("status", getField(q, "status"))
		row.Set("created_at", getField(q, "created_at"))
		rows = append(rows, row)
	}
	return rows
}

// stateDigest ports `state_digest`.
func stateDigest(task *ordjson.Object, environmentStamp string) (string, error) {
	picked := ordjson.NewObject()
	for _, k := range stateFields {
		picked.Set(k, getField(task, k))
	}
	picked.Set("environment", environmentStamp)
	data, err := ordjson.MarshalSortedCompact(picked)
	if err != nil {
		return "", err
	}
	return sha256Text(string(data))[:12], nil
}

func cursorCounters(s *store.Store, task *ordjson.Object, versionsObj *ordjson.Object) (map[string]int, error) {
	questions := listField(task, "questions")
	notesState, err := notes.State(s, asString(getField(task, "id")), 0)
	if err != nil {
		return nil, err
	}
	notesCount := 0
	if truthy(getField(notesState, "ok")) {
		notesCount = len(listField(notesState, "entries"))
	}
	answered, applied := 0, 0
	for _, qv := range questions {
		q := asObject(qv)
		status := asString(getField(q, "status"))
		if status != "open" {
			answered++
		}
		if status == "applied" {
			applied++
		}
	}
	refresh := 0
	if versionsObj != nil {
		refresh = len(listField(versionsObj, "refresh"))
	}
	return map[string]int{
		"questions": len(questions),
		"evidence":  len(listField(task, "evidence")),
		"answered":  answered,
		"applied":   applied,
		"notes":     notesCount,
		"refresh":   refresh,
		"attention": len(listField(task, "attention")),
	}, nil
}

// cursorOf ports `cursor_of`.
func cursorOf(s *store.Store, task *ordjson.Object, versionsObj *ordjson.Object) (string, error) {
	counters, err := cursorCounters(s, task, versionsObj)
	if err != nil {
		return "", err
	}
	taskID := asString(getField(task, "id"))
	digest, err := stateDigest(task, environment.Stamp(s, taskID))
	if err != nil {
		return "", err
	}
	updatedAt := asString(getField(task, "updated_at"))
	if updatedAt == "" {
		updatedAt = asString(getField(task, "created_at"))
	}
	parts := make([]string, len(cursorFields))
	for i, k := range cursorFields {
		parts[i] = fmt.Sprint(counters[k])
	}
	return "c" + strings.Join(parts, ".") + "." + digest + "." + updatedAt, nil
}

// latestHandoff ports `latest_handoff`: the most recent handoff-kind evidence record, from the raw task record.
func latestHandoff(task *ordjson.Object) *ordjson.Object {
	var handoffs []*ordjson.Object
	for _, rv := range listField(task, "evidence") {
		r := asObject(rv)
		if asString(getField(r, "kind")) == "handoff" {
			handoffs = append(handoffs, r)
		}
	}
	if len(handoffs) == 0 {
		return nil
	}
	return handoffs[len(handoffs)-1]
}

func intFromField(o *ordjson.Object, key string) int {
	n, ok := getField(o, key).(json.Number)
	if !ok {
		return 0
	}
	i, _ := n.Int64()
	return int(i)
}

// boundedViewOrNil ports the common `bounded_view(x, limit)` call convention where `x` may be Python `None`.
func boundedViewOrNil(v any, limit int) any {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	return environment.BoundedView(s, limit)
}

// paged ports `paged`: one stable page over an append-only list.
func paged(items []any, after, limit int) *ordjson.Object {
	total := len(items)
	if after < 0 {
		after = 0
	}
	if after > total {
		after = total
	}
	var rows []any
	if limit != 0 {
		end := after + limit
		if end > total {
			end = total
		}
		rows = items[after:end]
	} else {
		rows = items[after:]
	}
	endIdx := after + len(rows)
	result := ordjson.NewObject()
	result.Set("total", jsonInt(total))
	result.Set("after", jsonInt(after))
	result.Set("returned", jsonInt(len(rows)))
	result.Set("omitted", jsonInt(total-len(rows)))
	if endIdx < total {
		result.Set("next_after", jsonInt(endIdx))
	} else {
		result.Set("next_after", nil)
	}
	itemsOut := rows
	if itemsOut == nil {
		itemsOut = []any{}
	}
	result.Set("items", itemsOut)
	return result
}

var questionRowKeys = []string{"id", "key", "status", "created_at", "answered_at", "applied_at"}

// sectionDecisions ports `section_decisions`. `role` is always "" until `--role` is supported.
func sectionDecisions(task *ordjson.Object, role string, after, limit, maxChars int) *ordjson.Object {
	questions := listField(task, "questions")
	filtered := questions
	if role == "worker" || role == "coordinator" {
		want := "open"
		if role == "worker" {
			want = "answered"
		}
		filtered = nil
		for _, qv := range questions {
			if asString(getField(asObject(qv), "status")) == want {
				filtered = append(filtered, qv)
			}
		}
	}
	page := paged(filtered, after, limit)
	items, _ := getField(page, "items").([]any)
	reshaped := make([]any, len(items))
	for i, qv := range items {
		q := asObject(qv)
		row := pick(q, questionRowKeys)
		row.Set("text", environment.BoundedView(asString(getField(q, "text")), maxChars))
		row.Set("answer", boundedViewOrNil(getField(q, "answer"), maxChars))
		reshaped[i] = row
	}
	page.Set("items", reshaped)

	counts := map[string]int{"open": 0, "answered": 0, "applied": 0}
	for _, qv := range questions {
		status := asString(getField(asObject(qv), "status"))
		counts[status]++
	}
	result := ordjson.NewObject()
	for _, k := range page.Keys() {
		result.Set(k, getField(page, k))
	}
	result.Set("filter", nilIfEmpty(role))
	countsObj := ordjson.NewObject()
	for _, k := range []string{"open", "answered", "applied"} {
		countsObj.Set(k, jsonInt(counts[k]))
	}
	result.Set("counts", countsObj)
	result.Set("outstanding", outstanding(task))
	result.Set("note", "`outstanding` lists every unapplied decision regardless of paging. Answers are recorded human decisions; question text is a worker claim.")
	return result
}

// sectionReturns ports the inline `returns` section body of `context_view`.
func sectionReturns(s *store.Store, task *ordjson.Object) *ordjson.Object {
	returnsView, err := returns.View(s, task)
	result := ordjson.NewObject()
	if err != nil {
		result.Set("error", err.Error())
		return result
	}
	result.Set("open", getField(returnsView, "open"))
	result.Set("deliveries", jsonInt(len(listField(returnsView, "deliveries"))))
	result.Set("note", getField(returnsView, "note"))
	return result
}

func handoffStringList(items []any, limit int) *ordjson.Object {
	rows := make([]any, 0, len(items))
	redactions := 0
	for _, item := range items {
		row := boundedViewOrNil(item, limit)
		redactions += intFromField(asObject(row), "redactions")
		rows = append(rows, row)
	}
	result := ordjson.NewObject()
	result.Set("count", jsonInt(len(rows)))
	result.Set("items", rows)
	result.Set("redactions", jsonInt(redactions))
	return result
}

func handoffChecks(checksRaw []any, limit int) []any {
	rows := make([]any, 0, len(checksRaw))
	for _, cv := range checksRaw {
		c := asObject(cv)
		row := ordjson.NewObject()
		row.Set("command", boundedViewOrNil(getField(c, "command"), limit))
		row.Set("exit", getField(c, "exit"))
		row.Set("note", boundedViewOrNil(getField(c, "note"), limit))
		rows = append(rows, row)
	}
	return rows
}

var handoffTopKeys = []string{"id", "at", "source", "candidate", "brief_revision", "endpoint"}

// handoffView ports `handoff_view`: the structured handoff projected field by field, every worker string
// redacted and bounded.
func handoffView(record *ordjson.Object, head string, limit int) *ordjson.Object {
	handoff := asObject(getField(record, "handoff"))
	result := pick(record, handoffTopKeys)
	result.Set("current", head != "" && asString(getField(record, "candidate")) == head)
	result.Set("outcome", getField(handoff, "outcome"))
	result.Set("review", getField(handoff, "review"))
	result.Set("candidate_claimed", getField(handoff, "candidate"))
	result.Set("task_ref", boundedViewOrNil(getField(handoff, "task_ref"), limit))
	result.Set("next_action", boundedViewOrNil(getField(handoff, "next_action"), limit))
	result.Set("review_ref", boundedViewOrNil(getField(handoff, "review_ref"), limit))
	result.Set("files", handoffStringList(listField(handoff, "files"), limit))
	result.Set("artifacts", handoffStringList(listField(handoff, "artifacts"), limit))
	result.Set("decisions_unresolved", handoffStringList(listField(handoff, "decisions_unresolved"), limit))
	result.Set("checks", handoffChecks(listField(handoff, "checks"), limit))
	if prValue := getField(handoff, "pr"); truthy(prValue) {
		prObj := asObject(prValue)
		outPR := ordjson.NewObject()
		for _, k := range prObj.Keys() {
			v, _ := prObj.Get(k)
			if s, ok := v.(string); ok {
				outPR.Set(k, environment.BoundedView(s, limit))
			} else {
				outPR.Set(k, v)
			}
		}
		result.Set("pr", outPR)
	} else {
		result.Set("pr", nil)
	}
	return result
}

// sectionHandoff ports `section_handoff`.
func sectionHandoff(task *ordjson.Object, head string, maxChars int) *ordjson.Object {
	result := ordjson.NewObject()
	result.Set("current_candidate", nilIfEmpty(head))
	if record := latestHandoff(task); record != nil {
		result.Set("handoff", handoffView(record, head, maxChars))
	} else {
		result.Set("handoff", nil)
	}
	if reportValue := getField(task, "report"); truthy(reportValue) {
		report := asObject(reportValue)
		r := ordjson.NewObject()
		r.Set("submitted_at", getField(report, "submitted_at"))
		r.Set("brief_revision", getField(report, "brief_revision"))
		r.Set("candidate", getField(report, "candidate"))
		r.Set("text", boundedViewOrNil(getField(report, "text"), maxChars))
		result.Set("report", r)
	} else {
		result.Set("report", nil)
	}
	result.Set("authority", environment.ClaimNote)
	return result
}

var evidenceRowKeys = []string{"id", "kind", "source", "at", "candidate", "current", "brief_revision", "verdict", "result", "outcome"}

// sectionEvidence ports `section_evidence` (without `--kind` filtering, not yet supported).
func sectionEvidence(view *ordjson.Object, after, limit, maxChars int) *ordjson.Object {
	records := listField(view, "records")
	page := paged(records, after, limit)
	items, _ := getField(page, "items").([]any)
	rows := make([]any, 0, len(items))
	for _, rv := range items {
		record := asObject(rv)
		row := pick(record, evidenceRowKeys)
		if textValue := getField(record, "text"); textValue != nil {
			if s, ok := textValue.(string); ok {
				row.Set("text", environment.BoundedView(s, maxChars))
			}
		}
		if handoffValue := getField(record, "handoff"); truthy(handoffValue) {
			handoff := asObject(handoffValue)
			hh := pick(handoff, []string{"outcome", "candidate", "review"})
			hh.Set("next_action", boundedViewOrNil(getField(handoff, "next_action"), maxChars))
			row.Set("handoff", hh)
		}
		rows = append(rows, row)
	}
	page.Set("items", rows)

	byKind := ordjson.NewObject()
	counts := map[string]int{}
	for _, rv := range records {
		kind := asString(getField(asObject(rv), "kind"))
		if _, has := counts[kind]; !has {
			byKind.Set(kind, jsonInt(0))
		}
		counts[kind]++
	}
	for _, k := range byKind.Keys() {
		byKind.Set(k, jsonInt(counts[k]))
	}

	result := ordjson.NewObject()
	for _, k := range page.Keys() {
		result.Set(k, getField(page, k))
	}
	result.Set("kinds", byKind)
	result.Set("current_candidate", getField(view, "current_candidate"))
	result.Set("closure", getField(view, "closure"))
	result.Set("pr", getField(view, "pr"))
	result.Set("note", "Only `verification` (coordinator) and `publication` (github) records are verification evidence; worker and reviewer records are claims and findings.")
	return result
}

// sectionBrief ports `section_brief` (without `--revision`, not yet supported).
func sectionBrief(task *ordjson.Object, versionsObj *ordjson.Object, maxChars int) *ordjson.Object {
	result := ordjson.NewObject()
	result.Set("approved", environment.BoundedView(asString(getField(task, "brief")), maxChars))
	result.Set("fingerprint", versions.ApprovedFingerprint(task))
	result.Set("brief_path", getField(task, "brief_path"))
	if versionsObj != nil {
		result.Set("active_revision", getField(versionsObj, "active"))
		result.Set("requested_revision", getField(versionsObj, "requested"))
	} else {
		result.Set("active_revision", nil)
		result.Set("requested_revision", nil)
	}
	return result
}

var (
	executionTaskKeys     = []string{"repository", "worktree", "branch", "base_sha", "kind", "harness", "status", "created_at", "started_at"}
	executionLaunchKeys   = []string{"harness", "model", "reasoning", "preset", "argv", "observed"}
	executionEndpointKeys = []string{"machine", "session", "pane"}
)

// sectionExecution ports `section_execution`.
func sectionExecution(s *store.Store, task *ordjson.Object) (*ordjson.Object, error) {
	result := pick(task, executionTaskKeys)
	launch := asObject(getField(task, "launch"))
	result.Set("launch", pick(launch, executionLaunchKeys))
	result.Set("admission", getField(task, "admission"))
	graphV, err := graphview.View(s, task)
	if err != nil {
		return nil, err
	}
	result.Set("graph", graphV)

	endpoints := ordjson.NewObject()
	endpoints.Set("worker", pick(task, executionEndpointKeys))
	if parentValue := getField(task, "parent"); truthy(parentValue) {
		endpoints.Set("parent", pick(asObject(parentValue), executionEndpointKeys))
	} else {
		endpoints.Set("parent", nil)
	}
	if reviewerValue := getField(task, "reviewer"); truthy(reviewerValue) {
		endpoints.Set("reviewer", pick(asObject(reviewerValue), executionEndpointKeys))
	} else {
		endpoints.Set("reviewer", nil)
	}
	result.Set("endpoints", endpoints)
	return result, nil
}

func sectionOutline(s *store.Store, task *ordjson.Object, taskID, sumctlPath string, versionsObj *ordjson.Object, versionsErrorText any, view *ordjson.Object, head string) (*ordjson.Object, error) {
	outlineObj := ordjson.NewObject()
	for _, k := range outlineTaskKeys {
		outlineObj.Set(k, getField(task, k))
	}

	briefRunes := []rune(asString(getField(task, "brief")))
	approved := ordjson.NewObject()
	approved.Set("chars", jsonInt(len(briefRunes)))
	approved.Set("sha256", sha256Text(asString(getField(task, "brief")))[:16])
	approved.Set("base_sha", getField(task, "base_sha"))
	outlineObj.Set("approved", approved)

	questions := listField(task, "questions")
	decisions := ordjson.NewObject()
	decisions.Set("total", jsonInt(len(questions)))
	decisions.Set("outstanding", outstanding(task))
	outlineObj.Set("decisions", decisions)

	evidenceOutline := ordjson.NewObject()
	evidenceOutline.Set("records", jsonInt(len(listField(task, "evidence"))))
	evidenceOutline.Set("current_candidate", nilIfEmpty(head))
	if handoff := latestHandoff(task); handoff != nil {
		hh := asObject(getField(handoff, "handoff"))
		entry := ordjson.NewObject()
		entry.Set("id", getField(handoff, "id"))
		entry.Set("outcome", getField(hh, "outcome"))
		entry.Set("candidate", getField(handoff, "candidate"))
		entry.Set("current", head != "" && asString(getField(handoff, "candidate")) == head)
		evidenceOutline.Set("latest_handoff", entry)
	} else {
		evidenceOutline.Set("latest_handoff", nil)
	}
	closure := asObject(getField(view, "closure"))
	evidenceOutline.Set("closure_missing", getField(closure, "missing"))
	evidenceOutline.Set("verification", getField(view, "verification"))
	outlineObj.Set("evidence", evidenceOutline)

	if reportValue := getField(task, "report"); truthy(reportValue) {
		report := asObject(reportValue)
		r := ordjson.NewObject()
		r.Set("submitted_at", getField(report, "submitted_at"))
		r.Set("brief_revision", getField(report, "brief_revision"))
		outlineObj.Set("report", r)
	} else {
		outlineObj.Set("report", nil)
	}

	if versionsObj != nil {
		b := ordjson.NewObject()
		b.Set("active", getField(versionsObj, "active"))
		b.Set("requested", getField(versionsObj, "requested"))
		outlineObj.Set("brief", b)
	} else {
		errObj := ordjson.NewObject()
		errObj.Set("error", versionsErrorText)
		outlineObj.Set("brief", errObj)
	}

	returnsView, returnsErr := returns.View(s, task)
	if returnsErr != nil {
		errObj := ordjson.NewObject()
		errObj.Set("error", returnsErr.Error())
		outlineObj.Set("returns_open", errObj)
	} else {
		outlineObj.Set("returns_open", jsonInt(len(listField(returnsView, "open"))))
	}

	outlineObj.Set("attention_open", jsonInt(len(returns.OpenAttention(task))))
	outlineObj.Set("cleanup", cleanup.Pending(task))

	notesState, err := notes.State(s, taskID, 0)
	if err != nil {
		return nil, err
	}
	notesSummary := ordjson.NewObject()
	notesSummary.Set("present", getField(notesState, "present"))
	notesSummary.Set("ok", getField(notesState, "ok"))
	notesSummary.Set("entries", jsonInt(len(listField(notesState, "entries"))))
	outlineObj.Set("notes", notesSummary)

	outlineObj.Set("environment", environment.Outline(s, taskID))
	outlineObj.Set("graph", getField(asObject(getField(task, "graph")), "state"))

	readObj := ordjson.NewObject()
	sectionsListAny := make([]any, len(ContextSections))
	for i, sec := range ContextSections {
		sectionsListAny[i] = sec
	}
	readObj.Set("sections", sectionsListAny)
	readObj.Set("example", shquote.CommandFor(sumctlPath, s.Home, "context", taskID, "--section", "decisions", "--section", "handoff"))
	outlineObj.Set("read", readObj)

	return outlineObj, nil
}

// DefaultAfter, DefaultLimit, and DefaultMaxChars mirror the argparse defaults for --after/--limit/--max-chars
// (CONTEXT_LIMIT/CONTEXT_CHARS), used until those flags are supported.
const (
	DefaultAfter    = 0
	DefaultLimit    = 20
	DefaultMaxChars = 4000
)

func needsEvidenceView(sections []string) bool {
	for _, sec := range sections {
		if sec == "outline" || sec == "handoff" || sec == "evidence" {
			return true
		}
	}
	return false
}

// View ports `context_view` for the shapes this checkpoint supports: no flags (resolves to the single "outline"
// section, per `context_view`'s own default-section rule) or one or more `--section NAME` values. `--role` and
// `--since` are not yet supported by this native path.
func View(s *store.Store, taskID, sumctlPath string, sections []string) (*ordjson.Object, error) {
	if len(sections) == 0 {
		sections = []string{"outline"}
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}

	versionsObj, versionsErr := versions.View(s, task)
	var versionsErrorText any
	if versionsErr != nil {
		versionsObj = nil
		versionsErrorText = versionsErr.Error()
	}

	var view *ordjson.Object
	head := ""
	if needsEvidenceView(sections) {
		view = evidenceview.View(task)
		head = asString(getField(view, "current_candidate"))
	}

	cursor, err := cursorOf(s, task, versionsObj)
	if err != nil {
		return nil, err
	}

	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("status", getField(task, "status"))
	result.Set("cursor", cursor)
	result.Set("read_at", store.Now())
	sectionsAny := make([]any, len(sections))
	for i, sec := range sections {
		sectionsAny[i] = sec
	}
	result.Set("sections", sectionsAny)
	result.Set("role", nil)
	result.Set("versions_error", versionsErrorText)

	for _, sec := range sections {
		switch sec {
		case "outline":
			outlineObj, err := sectionOutline(s, task, taskID, sumctlPath, versionsObj, versionsErrorText, view, head)
			if err != nil {
				return nil, err
			}
			result.Set("outline", outlineObj)
		case "brief":
			result.Set("brief", sectionBrief(task, versionsObj, DefaultMaxChars))
		case "decisions":
			result.Set("decisions", sectionDecisions(task, "", DefaultAfter, DefaultLimit, DefaultMaxChars))
		case "handoff":
			result.Set("handoff", sectionHandoff(task, head, DefaultMaxChars))
		case "evidence":
			result.Set("evidence", sectionEvidence(view, DefaultAfter, DefaultLimit, DefaultMaxChars))
		case "returns":
			result.Set("returns", sectionReturns(s, task))
		case "execution":
			executionObj, err := sectionExecution(s, task)
			if err != nil {
				return nil, err
			}
			result.Set("execution", executionObj)
		case "notes":
			notesState, notesErr := notes.State(s, taskID, DefaultMaxChars)
			if notesErr != nil {
				return nil, notesErr
			}
			result.Set("notes", notesState)
		}
	}
	return result, nil
}
