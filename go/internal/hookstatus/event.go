package hookstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

var sessionName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var paneID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}:[A-Za-z0-9_-]{1,32}$`)
var socketSession = regexp.MustCompile(`/sessions/([^/]+)/herdr\.sock$`)

func Event(s *store.Store, environ map[string]string, runtimeRoot, sumctlPath string) (*ordjson.Object, error) {
	result, err := event(s, environ, runtimeRoot, sumctlPath)
	if err != nil {
		if recordErr := recordEventError(s, environ["HERDR_PLUGIN_EVENT"], err); recordErr != nil {
			return nil, fmt.Errorf("%w; could not record hook failure: %v", err, recordErr)
		}
	}
	return result, err
}

func event(s *store.Store, environ map[string]string, runtimeRoot, sumctlPath string) (*ordjson.Object, error) {
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
	health, err := readHealth(s)
	if err != nil {
		return nil, err
	}
	session, err := eventSession(environ)
	if err != nil {
		if environ["HERDR_SESSION"] != "" {
			return nil, err
		}
		session, err = observeEventSession(s, environ, runtimeRoot)
		if err != nil {
			return nil, err
		}
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
	// One budget for every delivery pass this event runs; maintenance (PR observation, cleanup) is never run here.
	deadline, cancel := context.WithTimeout(context.Background(), returns.DefaultPassBudget)
	defer cancel()
	herdr := returns.NewHerdrSnapshot(deadline, runtimeRoot)
	if event == "startup" {
		result, pumpErr := returns.Pump(s, returns.PumpOpts{
			RuntimeRoot: runtimeRoot,
			SumctlPath:  sumctlPath,
			Inline:      true,
			Parent:      deadline,
			Herdr:       herdr,
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
		changes.Set("degraded", nil)
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
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	owner, err := s.Owner()
	if err != nil {
		return nil, err
	}
	isRoot := owner != nil && pane != "" &&
		host.Is(asString(func() any { v, _ := owner.Get("machine"); return v }())) &&
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
		if !host.Is(asString(func() any { v, _ := task.Get("machine"); return v }())) {
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
			RetryStalled: true,
			Parent:       deadline,
			Snapshot:     tasks,
			Herdr:        herdr,
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
				Parent:       deadline,
				Snapshot:     tasks,
				Herdr:        herdr,
			}); err != nil {
				return nil, err
			}
		}
	}
	row.Set("outcome", "handled")
	row.Set("handler_ms", jsonInt(int(time.Since(started).Milliseconds())))
	changes := ordjson.NewObject()
	changes.Set("last_event", row)
	changes.Set("degraded", nil)
	if _, err := writeHealth(s, map[string]int{"events": 1, "handled": 1}, changes); err != nil {
		return nil, err
	}
	return row, nil
}

func eventSession(environ map[string]string) (string, error) {
	session := environ["HERDR_SESSION"]
	socket := environ["HERDR_SOCKET_PATH"]
	if socket != "" {
		if session != "" {
			if !sessionName.MatchString(session) {
				return "", fmt.Errorf("Event carries no identifiable Herdr session; refusing to guess a default session.")
			}
			return session, nil
		}
		if !filepath.IsAbs(socket) {
			return "", fmt.Errorf("Event carries a non-absolute Herdr socket path; refusing to guess a session.")
		}
		if match := socketSession.FindStringSubmatch(filepath.ToSlash(filepath.Clean(socket))); match != nil {
			session = match[1]
		} else if defaultHerdrSocket(environ) == filepath.Clean(socket) {
			session = "default"
		} else {
			return "", fmt.Errorf("Event carries no identifiable Herdr session; refusing to guess a default session.")
		}
	}
	if !sessionName.MatchString(session) {
		return "", fmt.Errorf("Event carries no identifiable Herdr session; refusing to guess a default session.")
	}
	return session, nil
}

func defaultHerdrSocket(environ map[string]string) string {
	if configPath := environ["HERDR_CONFIG_PATH"]; configPath != "" && filepath.IsAbs(configPath) {
		return filepath.Join(filepath.Dir(configPath), "herdr.sock")
	}
	configHome := environ["XDG_CONFIG_HOME"]
	if configHome == "" {
		home := environ["HOME"]
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		if home == "" {
			return ""
		}
		configHome = filepath.Join(home, ".config")
	}
	if !filepath.IsAbs(configHome) {
		return ""
	}
	return filepath.Join(configHome, "herdr", "herdr.sock")
}

type eventPaneOwner struct {
	session   string
	pane      string
	workspace string
	cwd       string
}

func observeEventSession(s *store.Store, environ map[string]string, runtimeRoot string) (string, error) {
	socket := environ["HERDR_SOCKET_PATH"]
	if !filepath.IsAbs(socket) {
		return "", fmt.Errorf("Event carries no identifiable Herdr session; refusing to guess a default session.")
	}
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(environ["HERDR_PLUGIN_EVENT_JSON"]), &payload); err != nil {
		return "", fmt.Errorf("HERDR_PLUGIN_EVENT_JSON is not JSON: %s", err)
	}
	data, _ := payload["data"].(map[string]any)
	pane, _ := data["pane_id"].(string)
	workspace, _ := data["workspace_id"].(string)
	if pane == "" || !paneID.MatchString(pane) {
		return "", fmt.Errorf("Event carries no identifiable Herdr session; refusing to guess a default session.")
	}
	owners, err := eventPaneOwners(s, pane, workspace)
	if err != nil {
		return "", err
	}
	sessions := map[string]bool{}
	for _, owner := range owners {
		sessions[owner.session] = true
	}
	if len(sessions) != 1 {
		if len(sessions) == 0 {
			return "", nil
		}
		return "", fmt.Errorf("Event session is ambiguous: pane %s matches %d recorded Herdr sessions.", pane, len(sessions))
	}
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		return "", fmt.Errorf("Event session cannot be observed: %s", err)
	}
	observed, err := herdrclient.CallCurrentContext(context.Background(), herdrPath, 5*time.Second, "pane", "get", pane)
	if err != nil {
		return "", fmt.Errorf("Event session cannot be observed through its Herdr socket: %s", err)
	}
	observedObj := asObject(observed)
	if observedObj == nil {
		return "", fmt.Errorf("Event session observation did not return a pane.")
	}
	if nested, ok := observedObj.Get("pane"); ok {
		observedObj = asObject(nested)
	}
	if observedObj == nil {
		return "", fmt.Errorf("Event session observation did not return a pane.")
	}
	observedPane := asString(func() any { v, _ := observedObj.Get("pane_id"); return v }())
	observedWorkspace := asString(func() any { v, _ := observedObj.Get("workspace_id"); return v }())
	observedCwd := asString(func() any { v, _ := observedObj.Get("cwd"); return v }())
	if observedPane != pane || (workspace != "" && observedWorkspace != workspace) {
		return "", fmt.Errorf("Event session observation disagrees with pane %s.", pane)
	}
	for _, owner := range owners {
		if sessions[owner.session] && owner.pane == observedPane && owner.cwd != "" && filepath.Clean(owner.cwd) == filepath.Clean(observedCwd) &&
			(owner.workspace == "" || owner.workspace == observedWorkspace) {
			return owner.session, nil
		}
	}
	return "", fmt.Errorf("Event pane %s is not the recorded pane Herdr currently observes.", pane)
}

func eventPaneOwners(s *store.Store, pane, workspace string) ([]eventPaneOwner, error) {
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	owners := []eventPaneOwner{}
	owner, err := s.Owner()
	if err != nil {
		return nil, err
	}
	if owner != nil && asString(func() any { v, _ := owner.Get("pane"); return v }()) == pane &&
		host.Is(func() any { v, _ := owner.Get("machine"); return v }()) {
		session := asString(func() any { v, _ := owner.Get("session"); return v }())
		if session != "" && sessionName.MatchString(session) {
			owners = append(owners, eventPaneOwner{
				session: session,
				pane:    pane,
				cwd:     asString(func() any { v, _ := owner.Get("cwd"); return v }()),
			})
		}
	}
	tasks, err := s.AllTasks()
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		if asString(func() any { v, _ := task.Get("status"); return v }()) == "archived" ||
			!host.Is(func() any { v, _ := task.Get("machine"); return v }()) ||
			asString(func() any { v, _ := task.Get("pane"); return v }()) != pane {
			continue
		}
		taskWorkspace := asString(func() any { v, _ := task.Get("workspace"); return v }())
		if workspace != "" && taskWorkspace != "" && taskWorkspace != workspace {
			continue
		}
		session := asString(func() any { v, _ := task.Get("session"); return v }())
		if session == "" || !sessionName.MatchString(session) {
			continue
		}
		owners = append(owners, eventPaneOwner{
			session:   session,
			pane:      pane,
			workspace: taskWorkspace,
			cwd:       asString(func() any { v, _ := task.Get("worktree"); return v }()),
		})
	}
	return owners, nil
}

func recordEventError(s *store.Store, eventName string, eventErr error) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	health, err := readHealth(s)
	if err != nil {
		return err
	}
	errors, _ := health.Get("errors")
	entries := asList(errors)
	entry := ordjson.NewObject()
	entry.Set("at", store.Now())
	entry.Set("stage", "handler")
	entry.Set("error", eventErr.Error())
	entry.Set("event", eventName)
	entries = append(entries, entry)
	lastEvent := ordjson.NewObject()
	lastEvent.Set("at", store.Now())
	lastEvent.Set("event", eventName)
	lastEvent.Set("outcome", "error")
	lastEvent.Set("reason", eventErr.Error())
	changes := ordjson.NewObject()
	changes.Set("errors", entries)
	changes.Set("last_event", lastEvent)
	changes.Set("last_error", eventErr.Error())
	changes.Set("degraded", eventErr.Error())
	health.Set("events", jsonInt(intField(health, "events")+1))
	for _, key := range changes.Keys() {
		value, _ := changes.Get(key)
		health.Set(key, value)
	}
	if len(entries) > HookErrors {
		entries = entries[len(entries)-HookErrors:]
	}
	health.Set("errors", entries)
	health.Set("updated_at", store.Now())
	return ordjson.WriteFile(filepath.Join(s.Home, HookDir, HookHealth), health)
}
