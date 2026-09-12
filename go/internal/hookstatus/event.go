package hookstatus

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

var sessionName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var paneID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}:[A-Za-z0-9_-]{1,32}$`)
var socketSession = regexp.MustCompile(`/sessions/([^/]+)/herdr\.sock$`)

func Event(s *store.Store, environ map[string]string, runtimeRoot, sumctlPath string) (*ordjson.Object, error) {
	started := time.Now()
	event := environ["HERDR_PLUGIN_EVENT"]
	pluginID := environ["HERDR_PLUGIN_ID"]
	expected, err := hookPluginID(s)
	if err != nil {
		return nil, err
	}
	if pluginID != expected {
		return nil, fmt.Errorf("Event addressed to plugin %q; this home answers only %s. Another sum installation or a stale registration is calling the wrong home.", pluginID, expected)
	}
	session := environ["HERDR_SESSION"]
	if session == "" {
		if match := socketSession.FindStringSubmatch(environ["HERDR_SOCKET_PATH"]); match != nil {
			session = match[1]
		}
	}
	if !sessionName.MatchString(session) {
		return nil, fmt.Errorf("Event carries no identifiable Herdr session; refusing to guess a default session.")
	}
	health, err := readHealth(s)
	if err != nil {
		return nil, err
	}
	if enabled, _ := health.Get("enabled"); !truthy(enabled) {
		row := ordjson.NewObject()
		row.Set("at", store.Now())
		row.Set("event", event)
		row.Set("session", session)
		row.Set("outcome", "disabled")
		row.Set("reason", "this instance has native delivery disabled; nothing handled")
		changes := ordjson.NewObject()
		changes.Set("last_event", row)
		if _, err := writeHealth(s, map[string]int{"events": 1, "ignored": 1}, changes); err != nil {
			return nil, err
		}
		return row, nil
	}
	if event == "startup" {
		result, pumpErr := returns.Pump(s, returns.PumpOpts{
			RuntimeRoot: runtimeRoot,
			SumctlPath:  sumctlPath,
			Reason:      "Herdr started; catching up on saved returns",
			Inline:      true,
		})
		if pumpErr != nil {
			return nil, pumpErr
		}
		row := ordjson.NewObject()
		row.Set("at", store.Now())
		row.Set("event", event)
		row.Set("session", session)
		row.Set("outcome", "reconciled")
		prompts, _ := result.Get("prompts")
		row.Set("prompts", prompts)
		row.Set("handler_ms", jsonInt(int(time.Since(started).Milliseconds())))
		changes := ordjson.NewObject()
		changes.Set("last_event", row)
		if _, err := writeHealth(s, map[string]int{"events": 1, "handled": 1}, changes); err != nil {
			return nil, err
		}
		return row, nil
	}
	var payload any
	raw := environ["HERDR_PLUGIN_EVENT_JSON"]
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("HERDR_PLUGIN_EVENT_JSON is not JSON: %s", err)
	}
	data := payload
	if obj, ok := payload.(map[string]any); ok {
		if inner, has := obj["data"]; has {
			data = inner
		}
	}
	dataObj, _ := data.(map[string]any)
	pane, _ := dataObj["pane_id"].(string)
	workspace, _ := dataObj["workspace_id"].(string)
	status, _ := dataObj["agent_status"].(string)
	if pane != "" && !paneID.MatchString(pane) {
		return nil, fmt.Errorf("Malformed pane id in event payload: %q", pane)
	}
	row := ordjson.NewObject()
	row.Set("at", store.Now())
	row.Set("event", event)
	row.Set("session", session)
	row.Set("pane", pane)
	row.Set("workspace", workspace)
	row.Set("status", status)
	host, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	owner, err := s.Owner()
	if err != nil {
		return nil, err
	}
	isRoot := owner != nil && pane != "" &&
		asString(func() any { v, _ := owner.Get("machine"); return v }()) == host &&
		asString(func() any { v, _ := owner.Get("session"); return v }()) == session &&
		asString(func() any { v, _ := owner.Get("pane"); return v }()) == pane
	tasks, err := s.AllTasks()
	if err != nil {
		return nil, err
	}
	var matched []*ordjson.Object
	for _, task := range tasks {
		if asString(func() any { v, _ := task.Get("status"); return v }()) == "archived" {
			continue
		}
		if asString(func() any { v, _ := task.Get("machine"); return v }()) != host {
			continue
		}
		if asString(func() any { v, _ := task.Get("session"); return v }()) != session {
			continue
		}
		if pane != "" && asString(func() any { v, _ := task.Get("pane"); return v }()) == pane {
			matched = append(matched, task)
			continue
		}
		if event == "workspace.closed" && workspace != "" && asString(func() any { v, _ := task.Get("workspace"); return v }()) == workspace {
			matched = append(matched, task)
		}
	}
	if !isRoot && len(matched) == 0 {
		row.Set("outcome", "ignored")
		row.Set("reason", "pane is neither this instance's coordinator nor a recorded worker in this session")
		changes := ordjson.NewObject()
		changes.Set("last_event", row)
		if _, err := writeHealth(s, map[string]int{"events": 1, "ignored": 1}, changes); err != nil {
			return nil, err
		}
		return row, nil
	}
	if isRoot && event == "pane.agent_status_changed" && (status == "idle" || status == "done") {
		if _, err := returns.Pump(s, returns.PumpOpts{
			RuntimeRoot:  runtimeRoot,
			SumctlPath:   sumctlPath,
			Recipient:    "parent",
			Reason:       "saved task state needs attention",
			RetryStalled: true,
		}); err != nil {
			return nil, err
		}
	}
	for _, task := range matched {
		id := asString(func() any { v, _ := task.Get("id"); return v }())
		if event == "pane.agent_status_changed" && (status == "idle" || status == "done" || status == "blocked") {
			if _, err := returns.Pump(s, returns.PumpOpts{
				RuntimeRoot:  runtimeRoot,
				SumctlPath:   sumctlPath,
				Tasks:        []string{id},
				Recipient:    "worker",
				RetryStalled: true,
			}); err != nil {
				return nil, err
			}
		}
	}
	row.Set("outcome", "handled")
	row.Set("handler_ms", jsonInt(int(time.Since(started).Milliseconds())))
	changes := ordjson.NewObject()
	changes.Set("last_event", row)
	if _, err := writeHealth(s, map[string]int{"events": 1, "handled": 1}, changes); err != nil {
		return nil, err
	}
	return row, nil
}
