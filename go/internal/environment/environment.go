package environment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	File   = "environment.json"
	Schema = 1

	// DefaultMaxChars mirrors CONTEXT_CHARS: the default bound for each prose field, overridable with --max-chars (0 = unbounded).
	DefaultMaxChars = 4000

	Note = "Recorded from declared configuration and one-shot observation; commands are references, never executed by sum, " +
		"and no port is bound or reserved by being written here. Re-inspect with `env inspect`; nothing polls or restarts."

	ClaimNote = "Agent-written text: a claim to verify, not approval and not verification evidence."
)

var (
	ServiceActive = []string{"intended", "starting", "running", "ready", "unknown", "stopping"}
	ServiceStates = append(append([]string{}, ServiceActive...), "failed", "conflict", "stopped", "lost")
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{16,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)\b(?:api[_-]?key|access[_-]?token|secret|password|passwd)\s*[=:]\s*['"]?[^\s'"]{8,}`),
}

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

// Redact replaces credential-shaped substrings in prose; returns (text, count). Pattern-based, never a guarantee.
func Redact(text string) (string, int) {
	count := 0
	for _, pattern := range secretPatterns {
		matches := pattern.FindAllString(text, -1)
		if len(matches) == 0 {
			continue
		}
		count += len(matches)
		text = pattern.ReplaceAllString(text, "[redacted]")
	}
	return text, count
}

func slicePythonStyle(runes []rune, limit int) []rune {
	if limit >= 0 {
		if limit > len(runes) {
			limit = len(runes)
		}
		return runes[:limit]
	}
	end := len(runes) + limit
	if end < 0 {
		end = 0
	}
	return runes[:end]
}

// BoundedView bounds prose to `limit` characters (0 must be guarded by the caller: unbounded) with explicit
// truncation metadata; credentials are redacted first.
func BoundedView(text string, limit int) *ordjson.Object {
	redacted, redactions := Redact(text)
	runes := []rune(redacted)
	chars := len(runes)
	truncated := limit != 0 && chars > limit
	row := ordjson.NewObject()
	row.Set("chars", jsonInt(chars))
	row.Set("truncated", truncated)
	row.Set("redactions", jsonInt(redactions))
	if truncated {
		row.Set("text", string(slicePythonStyle(runes, limit)))
		row.Set("note", fmt.Sprintf("First %d of %d characters; pass --max-chars 0 or a larger value for the rest.", limit, chars))
	} else {
		row.Set("text", redacted)
	}
	return row
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

func Path(s *store.Store, taskID string) (string, error) {
	taskPath, err := s.TaskPath(taskID)
	if err != nil {
		return "", err
	}
	return filepath.Join(taskPath, File), nil
}

// Read returns the sidecar as recorded, or (nil, nil) if the task has none: a task without one continues exactly as before.
func Read(s *store.Store, taskID string) (*ordjson.Object, error) {
	path, err := Path(s, taskID)
	if err != nil {
		return nil, err
	}
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symlink; the environment record must be a regular file inside the task record.", path)
	}
	info, statErr := os.Stat(path)
	if statErr != nil || info.IsDir() {
		return nil, nil
	}
	value, err := ordjson.ReadFile(path)
	if err != nil {
		return nil, err
	}
	record, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("%s is not a JSON object", path)
	}
	schemaOK := false
	if schema, has := record.Get("schema"); has {
		if num, isNum := schema.(json.Number); isNum {
			if n, convErr := num.Int64(); convErr == nil && n == Schema {
				schemaOK = true
			}
		}
	}
	taskValue, _ := record.Get("task")
	if !schemaOK || taskValue != taskID {
		return nil, fmt.Errorf("Environment record schema/identity mismatch; inspect the sidecar, it is not rewritten.")
	}
	if _, has := record.Get("services"); !has {
		record.Set("services", []any{})
	}
	return record, nil
}

func stringField(o *ordjson.Object, key string) string {
	if o == nil {
		return ""
	}
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}

func objectField(o *ordjson.Object, key string) *ordjson.Object {
	if o == nil {
		return nil
	}
	v, ok := o.Get(key)
	if !ok {
		return nil
	}
	sub, _ := v.(*ordjson.Object)
	return sub
}

func listField(o *ordjson.Object, key string) []any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	list, _ := v.([]any)
	return list
}

func pick(o *ordjson.Object, keys []string) *ordjson.Object {
	result := ordjson.NewObject()
	for _, k := range keys {
		var v any
		if o != nil {
			v, _ = o.Get(k)
		}
		result.Set(k, v)
	}
	return result
}

var (
	launchKeys     = []string{"via", "runner", "project", "pane_created"}
	processKeys    = []string{"pid", "name", "shell_pid", "observed_at"}
	readinessKeys  = []string{"ready", "checked", "waited_s", "reason"}
	stopKeys       = []string{"action", "result", "reasons"}
	serviceTopKeys = []string{"id", "name", "source", "kind", "state", "url", "port", "pane", "workspace", "label", "intent_at", "launched_at", "stopped_at", "exit_verified", "by"}
	endpointKeys   = []string{"id", "url", "port", "local", "label", "ownership", "claimed_ownership", "state", "stale_reason", "config_stale", "observed_at", "recorded_by"}
	listenerKeys   = []string{"pid", "owner"}
	logKeys        = []string{"id", "path", "scope", "label", "ownership", "state", "bytes", "modified_at", "observed_at"}
	resourceKeys   = []string{"kind", "id", "session", "label", "ownership", "state", "cwd", "note", "service", "observed_at"}
	sourceKeys     = []string{"path", "bytes", "sha256", "skipped"}
	discoveryKeys  = []string{"observed_at", "head", "config_revision", "current_revision", "stale", "stale_reason", "checked_at", "summary", "problems", "task_origins", "verification_contract"}
	commandKeys    = []string{"name", "kind", "source", "description", "image", "declared_ports"}
)

func servicesView(record *ordjson.Object) []any {
	services := listField(record, "services")
	result := make([]any, 0, len(services))
	for _, sv := range services {
		s, _ := sv.(*ordjson.Object)
		entry := pick(s, serviceTopKeys)
		entry.Set("launch", pick(objectField(s, "launch"), launchKeys))
		entry.Set("process", pick(objectField(s, "process"), processKeys))
		if readiness, ok := s.Get("readiness"); ok && truthy(readiness) {
			entry.Set("readiness", pick(objectField(s, "readiness"), readinessKeys))
		} else {
			entry.Set("readiness", nil)
		}
		if stop, ok := s.Get("stop"); ok && truthy(stop) {
			entry.Set("stop", pick(objectField(s, "stop"), stopKeys))
		} else {
			entry.Set("stop", nil)
		}
		result = append(result, entry)
	}
	return result
}

func commandView(c *ordjson.Object, limit int) *ordjson.Object {
	text, redactions := Redact(stringField(c, "command"))
	result := pick(c, commandKeys)
	if limit != 0 {
		result.Set("command", BoundedView(text, limit))
	} else {
		result.Set("command", text)
	}
	existing := 0
	if v, has := c.Get("redactions"); has {
		if n, ok := v.(json.Number); ok {
			i64, _ := n.Int64()
			existing = int(i64)
		}
	}
	result.Set("redactions", jsonInt(existing+redactions))
	return result
}

func endpointView(e *ordjson.Object) *ordjson.Object {
	result := pick(e, endpointKeys)
	observation := objectField(e, "observation")
	listeners := make([]any, 0)
	for _, lv := range listField(observation, "listeners") {
		l, _ := lv.(*ordjson.Object)
		listeners = append(listeners, pick(l, listenerKeys))
	}
	result.Set("listeners", listeners)
	conflicts := make([]any, 0)
	for _, cv := range listField(e, "conflicts") {
		c, _ := cv.(*ordjson.Object)
		task, _ := c.Get("task")
		conflicts = append(conflicts, task)
	}
	result.Set("conflicts", conflicts)
	return result
}

func isStale(record *ordjson.Object, discovery *ordjson.Object) bool {
	if discovery != nil {
		if stale, ok := discovery.Get("stale"); ok && truthy(stale) {
			return true
		}
	}
	for _, ev := range listField(record, "endpoints") {
		e, _ := ev.(*ordjson.Object)
		state := stringField(e, "state")
		configStale, _ := e.Get("config_stale")
		if state == "stale" || state == "unverified" || truthy(configStale) {
			return true
		}
	}
	for _, lv := range listField(record, "logs") {
		l, _ := lv.(*ordjson.Object)
		if stringField(l, "state") != "present" {
			return true
		}
	}
	for _, sv := range listField(record, "services") {
		s, _ := sv.(*ordjson.Object)
		switch stringField(s, "state") {
		case "unknown", "stopping", "conflict", "failed":
			return true
		}
	}
	return false
}

// View is the compact, redacted record for context readers, mirroring `environment_view`. From the sidecar only:
// reading never observes, starts, or stops anything.
func View(s *store.Store, taskID, sumctlPath string, limit int) (*ordjson.Object, error) {
	record, err := Read(s, taskID)
	if err != nil {
		result := ordjson.NewObject()
		result.Set("present", false)
		result.Set("ok", false)
		result.Set("error", err.Error())
		return result, nil
	}
	commands := ordjson.NewObject()
	commands.Set("discover", shquote.CommandFor(sumctlPath, s.Home, "env", "discover", taskID))
	commands.Set("record", shquote.CommandFor(sumctlPath, s.Home, "env", "record", taskID, "--url", "http://127.0.0.1:PORT"))
	commands.Set("inspect", shquote.CommandFor(sumctlPath, s.Home, "env", "inspect", taskID))

	if record == nil {
		result := ordjson.NewObject()
		result.Set("present", false)
		result.Set("ok", true)
		result.Set("commands", commands)
		result.Set("note", "No environment record. The task continues normally; `env discover` records the repository's declared commands, `env record` an observed URL, log, pane, or container.")
		return result, nil
	}

	discovery := objectField(record, "discovery")
	discoveryValue, _ := record.Get("discovery")
	if truthy(discoveryValue) {
		commands.Set("start", shquote.CommandFor(sumctlPath, s.Home, "env", "start", taskID, "--command", "NAME", "--url", "http://127.0.0.1:PORT"))
		commands.Set("stop", shquote.CommandFor(sumctlPath, s.Home, "env", "stop", taskID))
	}

	var discoveryView any
	if truthy(discoveryValue) {
		dv := pick(discovery, discoveryKeys)
		sources := make([]any, 0)
		for _, sv := range listField(discovery, "sources") {
			src, _ := sv.(*ordjson.Object)
			sources = append(sources, pick(src, sourceKeys))
		}
		dv.Set("sources", sources)
		cmds := make([]any, 0)
		for _, cv := range listField(discovery, "commands") {
			c, _ := cv.(*ordjson.Object)
			cmds = append(cmds, commandView(c, limit))
		}
		dv.Set("commands", cmds)
		discoveryView = dv
	}

	endpoints := make([]any, 0)
	for _, ev := range listField(record, "endpoints") {
		e, _ := ev.(*ordjson.Object)
		endpoints = append(endpoints, endpointView(e))
	}

	logs := make([]any, 0)
	for _, lv := range listField(record, "logs") {
		l, _ := lv.(*ordjson.Object)
		logs = append(logs, pick(l, logKeys))
	}

	resources := make([]any, 0)
	for _, rv := range listField(record, "resources") {
		r, _ := rv.(*ordjson.Object)
		resources = append(resources, pick(r, resourceKeys))
	}

	history := listField(record, "history")
	if len(history) > 5 {
		history = history[len(history)-5:]
	}
	historyOut := make([]any, len(history))
	copy(historyOut, history)

	path, err := Path(s, taskID)
	if err != nil {
		return nil, err
	}

	result := ordjson.NewObject()
	result.Set("present", true)
	result.Set("ok", true)
	result.Set("path", path)
	updatedAt, _ := record.Get("updated_at")
	result.Set("updated_at", updatedAt)
	result.Set("stale", isStale(record, discovery))
	result.Set("discovery", discoveryView)
	result.Set("endpoints", endpoints)
	result.Set("logs", logs)
	result.Set("resources", resources)
	result.Set("services", servicesView(record))
	result.Set("history", historyOut)
	result.Set("commands", commands)
	result.Set("authority", ClaimNote)
	result.Set("note", Note)
	return result, nil
}

// Show is the `env show TASK_ID [--max-chars N]` command body.
func Show(s *store.Store, taskID, sumctlPath string, maxChars int) (*ordjson.Object, error) {
	view, err := View(s, taskID, sumctlPath, maxChars)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("environment", view)
	return result, nil
}
