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

func latestHandoffFromView(view *ordjson.Object) *ordjson.Object {
	var handoffs []*ordjson.Object
	for _, rv := range listField(view, "records") {
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
	if handoff := latestHandoffFromView(view); handoff != nil {
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

// View ports `context_view` for the bare `context TASK_ID` shape only: no `--section`, `--role`, or `--since`,
// which always resolves to the single "outline" section per `context_view`'s own default-section rule.
func View(s *store.Store, taskID, sumctlPath string) (*ordjson.Object, error) {
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

	view := evidenceview.View(task)
	head := asString(getField(view, "current_candidate"))

	cursor, err := cursorOf(s, task, versionsObj)
	if err != nil {
		return nil, err
	}

	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("status", getField(task, "status"))
	result.Set("cursor", cursor)
	result.Set("read_at", store.Now())
	result.Set("sections", []any{"outline"})
	result.Set("role", nil)
	result.Set("versions_error", versionsErrorText)

	outlineObj, err := sectionOutline(s, task, taskID, sumctlPath, versionsObj, versionsErrorText, view, head)
	if err != nil {
		return nil, err
	}
	result.Set("outline", outlineObj)
	return result, nil
}
