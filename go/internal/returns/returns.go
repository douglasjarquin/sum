package returns

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

const (
	File           = "returns.json"
	Schema         = 1
	AttemptsBound  = 3
	NotSubmittedAt = "submitted-unconfirmed"
)

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func OpenAttention(task *ordjson.Object) []*ordjson.Object {
	var later []string
	if questionsValue, ok := task.Get("questions"); ok {
		if list, ok := questionsValue.([]any); ok {
			for _, q := range list {
				question, _ := q.(*ordjson.Object)
				if createdAt, ok := question.Get("created_at"); ok {
					if s, ok := createdAt.(string); ok && s != "" {
						later = append(later, s)
					}
				}
			}
		}
	}
	if evidenceValue, ok := task.Get("evidence"); ok {
		if list, ok := evidenceValue.([]any); ok {
			for _, e := range list {
				record, _ := e.(*ordjson.Object)
				kind, _ := record.Get("kind")
				if kind == "report" || kind == "handoff" {
					if at, ok := record.Get("at"); ok {
						if s, ok := at.(string); ok && s != "" {
							later = append(later, s)
						}
					}
				}
			}
		}
	}

	var rows []*ordjson.Object
	attentionValue, _ := task.Get("attention")
	attentionList, _ := attentionValue.([]any)
	for _, a := range attentionList {
		record, _ := a.(*ordjson.Object)
		status, _ := record.Get("status")
		if status != "open" {
			continue
		}
		atValue, _ := record.Get("at")
		at, _ := atValue.(string)
		superseded := false
		for _, t := range later {
			if t > at {
				superseded = true
				break
			}
		}
		if superseded {
			continue
		}
		rows = append(rows, record)
	}
	return rows
}

func OpenObligations(s *store.Store, task *ordjson.Object) ([]*ordjson.Object, error) {
	var items []*ordjson.Object
	statusValue, _ := task.Get("status")
	if statusValue == "archived" {
		return items, nil
	}

	if questionsValue, ok := task.Get("questions"); ok {
		if list, ok := questionsValue.([]any); ok {
			for _, q := range list {
				question, _ := q.(*ordjson.Object)
				qStatus, _ := question.Get("status")
				id, _ := question.Get("id")
				switch qStatus {
				case "open":
					createdAt, _ := question.Get("created_at")
					item := ordjson.NewObject()
					item.Set("id", "question:"+fmt.Sprint(id))
					item.Set("kind", "question")
					item.Set("ref", id)
					item.Set("recipient", "parent")
					item.Set("since", createdAt)
					items = append(items, item)
				case "answered":
					answeredAt, _ := question.Get("answered_at")
					item := ordjson.NewObject()
					item.Set("id", "answer:"+fmt.Sprint(id))
					item.Set("kind", "answer")
					item.Set("ref", id)
					item.Set("recipient", "worker")
					item.Set("since", answeredAt)
					items = append(items, item)
				}
			}
		}
	}

	var closers []string
	type reportRef struct {
		ref string
		at  string
	}
	var reports []reportRef
	if evidenceValue, ok := task.Get("evidence"); ok {
		if list, ok := evidenceValue.([]any); ok {
			for _, e := range list {
				record, _ := e.(*ordjson.Object)
				kind, _ := record.Get("kind")
				source, _ := record.Get("source")
				at, _ := record.Get("at")
				atStr, _ := at.(string)
				if kind == "publication" || (kind == "verification" && source == "coordinator") {
					closers = append(closers, atStr)
				}
				if kind == "report" {
					id, _ := record.Get("id")
					reports = append(reports, reportRef{ref: fmt.Sprint(id), at: atStr})
				}
			}
		}
	}
	if reportValue, hasReport := task.Get("report"); hasReport && reportValue != nil && len(reports) == 0 {
		report, _ := reportValue.(*ordjson.Object)
		submittedAt, _ := report.Get("submitted_at")
		submittedAtStr, _ := submittedAt.(string)
		reports = append(reports, reportRef{ref: "legacy", at: submittedAtStr})
	}
	for _, r := range reports {
		closed := false
		for _, c := range closers {
			if c >= r.at {
				closed = true
				break
			}
		}
		if !closed {
			item := ordjson.NewObject()
			item.Set("id", "report:"+r.ref)
			item.Set("kind", "report")
			item.Set("ref", r.ref)
			item.Set("recipient", "parent")
			item.Set("since", r.at)
			items = append(items, item)
		}
	}

	for _, a := range OpenAttention(task) {
		id, _ := a.Get("id")
		at, _ := a.Get("at")
		kind, _ := a.Get("kind")
		item := ordjson.NewObject()
		item.Set("id", "attention:"+fmt.Sprint(id))
		item.Set("kind", "attention")
		item.Set("ref", id)
		item.Set("recipient", "parent")
		item.Set("since", at)
		item.Set("attention", kind)
		items = append(items, item)
	}

	versionsObj, err := versions.ReadVersions(s, task)
	if err == nil {
		requestedValue, _ := versionsObj.Get("requested")
		requested, requestedIsString := requestedValue.(string)
		if requestedIsString && requested != "" {
			revisionsValue, _ := versionsObj.Get("revisions")
			revisionList, _ := revisionsValue.([]any)
			found := false
			for _, r := range revisionList {
				rev, _ := r.(*ordjson.Object)
				revID, _ := rev.Get("id")
				revStatus, _ := rev.Get("status")
				if revID == requested && revStatus == "requested" {
					found = true
					break
				}
			}
			if found {
				var since any
				refreshValue, _ := versionsObj.Get("refresh")
				refreshList, _ := refreshValue.([]any)
				for i := len(refreshList) - 1; i >= 0; i-- {
					entry, _ := refreshList[i].(*ordjson.Object)
					event, _ := entry.Get("event")
					revision, _ := entry.Get("revision")
					if event == "requested" && revision == requested {
						since, _ = entry.Get("at")
						break
					}
				}
				priorState := versions.RefreshState(versionsObj)
				priorStateValue, _ := priorState.Get("state")
				item := ordjson.NewObject()
				item.Set("id", "refresh:"+requested)
				item.Set("kind", "refresh")
				item.Set("ref", requested)
				item.Set("recipient", "worker")
				item.Set("since", since)
				item.Set("prior", priorStateValue)
				items = append(items, item)
			}
		}
	}

	return items, nil
}

func ReturnRoute(task *ordjson.Object, recipient string) *ordjson.Object {
	route := ordjson.NewObject()
	if recipient == "parent" {
		parentValue, _ := task.Get("parent")
		parent, _ := parentValue.(*ordjson.Object)
		route.Set("recipient", "parent")
		route.Set("role", "coordinator")
		if parent != nil {
			machine, _ := parent.Get("machine")
			session, _ := parent.Get("session")
			pane, _ := parent.Get("pane")
			cwd, _ := parent.Get("cwd")
			route.Set("machine", machine)
			route.Set("session", session)
			route.Set("pane", pane)
			route.Set("cwd", cwd)
		} else {
			route.Set("machine", nil)
			route.Set("session", nil)
			route.Set("pane", nil)
			route.Set("cwd", nil)
		}
		return route
	}
	machine, _ := task.Get("machine")
	session, _ := task.Get("session")
	pane, _ := task.Get("pane")
	worktree, _ := task.Get("worktree")
	route.Set("recipient", "worker")
	route.Set("role", "worker")
	route.Set("machine", machine)
	route.Set("session", session)
	route.Set("pane", pane)
	route.Set("cwd", worktree)
	return route
}

func RouteKey(route *ordjson.Object) any {
	machine, _ := route.Get("machine")
	session, _ := route.Get("session")
	pane, _ := route.Get("pane")
	machineStr, _ := machine.(string)
	sessionStr, _ := session.(string)
	paneStr, _ := pane.(string)
	if machineStr == "" || sessionStr == "" || paneStr == "" {
		return nil
	}
	return store.RegistrationKey(store.Endpoint{Machine: machineStr, Session: sessionStr, Pane: paneStr})
}

func ReadReturns(s *store.Store, taskID string) (*ordjson.Object, error) {
	taskPath, err := s.TaskPath(taskID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(taskPath, File)
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must not be a symlink", path)
	}
	info, statErr := os.Stat(path)
	if statErr != nil || info.IsDir() {
		empty := ordjson.NewObject()
		empty.Set("schema", jsonInt(Schema))
		empty.Set("task", taskID)
		empty.Set("deliveries", []any{})
		return empty, nil
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
	if !schemaOK || taskField != taskID {
		return nil, fmt.Errorf("unsupported or mismatched returns sidecar %s. Inspect it; sum never migrates it in place.", path)
	}
	return obj, nil
}

func NotificationState(returnsObj *ordjson.Object, obligation *ordjson.Object, key any) *ordjson.Object {
	obligationID, _ := obligation.Get("id")
	deliveriesValue, _ := returnsObj.Get("deliveries")
	deliveryList, _ := deliveriesValue.([]any)

	var attempts []*ordjson.Object
	for _, d := range deliveryList {
		delivery, _ := d.(*ordjson.Object)
		obligationsValue, _ := delivery.Get("obligations")
		obligationIDs, _ := obligationsValue.([]any)
		matches := false
		for _, o := range obligationIDs {
			if o == obligationID {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		recipientValue, _ := delivery.Get("recipient")
		recipient, _ := recipientValue.(*ordjson.Object)
		var recipientKey any
		if recipient != nil {
			recipientKey, _ = recipient.Get("key")
		}
		if recipientKey == key {
			attempts = append(attempts, delivery)
		}
	}

	if len(attempts) == 0 {
		prior, _ := obligation.Get("prior")
		result := ordjson.NewObject()
		if prior == NotSubmittedAt {
			result.Set("state", "submitted")
			result.Set("attempts", jsonInt(0))
			result.Set("via", "refresh")
			result.Set("reason", "the refresh instruction itself was submitted; nothing read yet")
		} else {
			result.Set("state", "pending")
			result.Set("attempts", jsonInt(0))
			result.Set("reason", "no delivery attempted to this recipient yet")
		}
		return result
	}

	last := attempts[len(attempts)-1]
	lastState, _ := last.Get("state")
	lastAt, _ := last.Get("at")
	lastVia, _ := last.Get("via")
	lastID, _ := last.Get("id")
	lastReason, _ := last.Get("reason")

	base := func() *ordjson.Object {
		row := ordjson.NewObject()
		row.Set("attempts", jsonInt(len(attempts)))
		row.Set("last_at", lastAt)
		row.Set("via", lastVia)
		row.Set("delivery", lastID)
		return row
	}

	if lastState == "in-flight" {
		row := base()
		row.Set("state", "uncertain")
		row.Set("reason", "an attempt was interrupted before its outcome was recorded; delivery unknown, not retried by itself")
		return row
	}
	if lastState == "not-delivered" {
		failed := 0
		for _, d := range attempts {
			if s, _ := d.Get("state"); s == "not-delivered" {
				failed++
			}
		}
		if failed >= AttemptsBound {
			row := base()
			row.Set("state", "stalled")
			row.Set("reason", fmt.Sprintf("%d known-not-delivered attempts; only an explicit `notice` tries again: %v", failed, lastReason))
			return row
		}
		row := base()
		row.Set("state", "not-delivered")
		row.Set("reason", lastReason)
		return row
	}
	row := base()
	row.Set("state", lastState)
	row.Set("reason", lastReason)
	return row
}

func View(s *store.Store, task *ordjson.Object) (*ordjson.Object, error) {
	taskID, _ := task.Get("id")
	taskIDStr, _ := taskID.(string)
	returnsObj, err := ReadReturns(s, taskIDStr)
	if err != nil {
		return nil, err
	}
	obligations, err := OpenObligations(s, task)
	if err != nil {
		return nil, err
	}

	rows := make([]any, 0, len(obligations))
	openIDs := map[any]bool{}
	for _, obligation := range obligations {
		recipientValue, _ := obligation.Get("recipient")
		recipient, _ := recipientValue.(string)
		route := ReturnRoute(task, recipient)
		row := ordjson.NewObject()
		for _, key := range []string{"id", "kind", "ref", "recipient", "since"} {
			v, _ := obligation.Get(key)
			row.Set(key, v)
		}
		row.Set("obligation", "open")
		routeView := ordjson.NewObject()
		for _, key := range []string{"role", "session", "pane"} {
			v, _ := route.Get(key)
			routeView.Set(key, v)
		}
		row.Set("route", routeView)
		row.Set("notification", NotificationState(returnsObj, obligation, RouteKey(route)))
		rows = append(rows, row)
		id, _ := obligation.Get("id")
		openIDs[id] = true
	}

	deliveriesValue, _ := returnsObj.Get("deliveries")
	deliveryList, _ := deliveriesValue.([]any)
	start := len(deliveryList) - 20
	if start < 0 {
		start = 0
	}
	history := make([]any, 0, len(deliveryList)-start)
	for _, d := range deliveryList[start:] {
		delivery, _ := d.(*ordjson.Object)
		entry := ordjson.NewObject()
		for _, key := range delivery.Keys() {
			v, _ := delivery.Get(key)
			entry.Set(key, v)
		}
		obligationsValue, _ := delivery.Get("obligations")
		obligationIDs, _ := obligationsValue.([]any)
		var closed []any
		for _, o := range obligationIDs {
			if !openIDs[o] {
				closed = append(closed, o)
			}
		}
		if closed == nil {
			closed = []any{}
		}
		entry.Set("closed", closed)
		history = append(history, entry)
	}

	result := ordjson.NewObject()
	result.Set("open", rows)
	result.Set("deliveries", history)
	result.Set("note", "Obligations come from the records; a submitted or presented notice closes none of them. `closed` names obligations a later record has since settled.")
	return result, nil
}
