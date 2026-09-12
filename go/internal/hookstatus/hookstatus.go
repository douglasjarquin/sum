package hookstatus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

const (
	HookDir    = "hook"
	HookHealth = "health.json"
	HookSchema = 1
	HookErrors = 20

	HerdrVersion    = "0.9.0"
	InboxEntrypoint = "inbox"
)

var HookEvents = []string{"pane.agent_status_changed", "pane.agent_detected", "pane.exited", "pane.closed", "workspace.closed"}

var healthViewKeys = []string{"plugin_id", "manifest_path", "command", "linked_at", "disabled_at", "updated_at", "warnings", "manifest_sha256"}

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func intEquals(v any, n int) bool {
	num, ok := v.(json.Number)
	if !ok {
		return false
	}
	i, err := num.Int64()
	return err == nil && i == int64(n)
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

func asList(v any) []any {
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

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func sha256Text(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func defaultHealth() *ordjson.Object {
	h := ordjson.NewObject()
	h.Set("schema", jsonInt(HookSchema))
	h.Set("enabled", false)
	h.Set("plugin_id", nil)
	h.Set("events", jsonInt(0))
	h.Set("ignored", jsonInt(0))
	h.Set("handled", jsonInt(0))
	h.Set("errors", []any{})
	h.Set("last_event", nil)
	h.Set("last_error", nil)
	return h
}

// readHealth ports `read_health`.
func readHealth(s *store.Store) (*ordjson.Object, error) {
	path := filepath.Join(s.Home, HookDir, HookHealth)
	if isSymlink(path) {
		return nil, fmt.Errorf("%s must not be a symlink.", path)
	}
	info, statErr := os.Stat(path)
	if statErr != nil || info.IsDir() {
		return defaultHealth(), nil
	}
	value, err := ordjson.ReadFile(path)
	if err != nil {
		return nil, err
	}
	obj := asObject(value)
	if obj == nil {
		return nil, fmt.Errorf("Unsupported hook health schema in %s; inspect it, sum never migrates it in place.", path)
	}
	schemaValue, _ := obj.Get("schema")
	if !intEquals(schemaValue, HookSchema) {
		return nil, fmt.Errorf("Unsupported hook health schema in %s; inspect it, sum never migrates it in place.", path)
	}
	return obj, nil
}

// pendingSummary ports `pending_summary`: count and age of every open return across tasks, from records only.
func pendingSummary(s *store.Store) (*ordjson.Object, error) {
	tasks, err := s.AllTasks()
	if err != nil {
		return nil, err
	}
	count := 0
	oldest := ""
	for _, t := range tasks {
		obligations, obErr := returns.OpenObligations(s, t)
		if obErr != nil {
			return nil, obErr
		}
		for _, o := range obligations {
			count++
			since := asString(func() any { v, _ := o.Get("since"); return v }())
			if since != "" && (oldest == "" || since < oldest) {
				oldest = since
			}
		}
	}
	result := ordjson.NewObject()
	result.Set("count", jsonInt(count))
	var oldestValue, ageValue any
	if oldest != "" {
		oldestValue = oldest
		if parsed, parseErr := time.Parse(time.RFC3339, oldest); parseErr == nil {
			secs := int(time.Since(parsed).Seconds())
			if secs < 0 {
				secs = 0
			}
			ageValue = jsonInt(secs)
		}
	}
	result.Set("oldest_since", oldestValue)
	result.Set("oldest_age_s", ageValue)
	return result, nil
}

func Summary(s *store.Store) (*ordjson.Object, error) {
	health, err := readHealth(s)
	if err != nil {
		pending, pErr := pendingSummary(s)
		if pErr != nil {
			return nil, pErr
		}
		result := ordjson.NewObject()
		result.Set("enabled", false)
		result.Set("degraded", true)
		result.Set("reason", fmt.Sprintf("health unreadable: %s", err))
		result.Set("pending", pending)
		return result, nil
	}
	enabledValue, _ := health.Get("enabled")
	enabled := truthy(enabledValue)

	result := ordjson.NewObject()
	result.Set("enabled", enabled)
	pluginID, _ := health.Get("plugin_id")
	result.Set("plugin_id", pluginID)
	lastEvent, _ := health.Get("last_event")
	result.Set("last_event", lastEvent)
	lastError, _ := health.Get("last_error")
	result.Set("last_error", lastError)
	eventsValue, hasEvents := health.Get("events")
	if !hasEvents {
		eventsValue = jsonInt(0)
	}
	result.Set("events", eventsValue)
	handledValue, hasHandled := health.Get("handled")
	if !hasHandled {
		handledValue = jsonInt(0)
	}
	result.Set("handled", handledValue)
	ignoredValue, hasIgnored := health.Get("ignored")
	if !hasIgnored {
		ignoredValue = jsonInt(0)
	}
	result.Set("ignored", ignoredValue)
	result.Set("errors", jsonInt(len(asList(func() any { v, _ := health.Get("errors"); return v }()))))

	pending, pErr := pendingSummary(s)
	if pErr != nil {
		return nil, pErr
	}
	result.Set("pending", pending)

	degradedValue, _ := health.Get("degraded")
	degraded := !enabled || truthy(degradedValue)
	result.Set("degraded", degraded)
	var reason any
	if truthy(degradedValue) {
		reason = degradedValue
	} else if !enabled {
		reason = "native event delivery is not enabled; `inbox --live` remains the delivery path"
	}
	result.Set("reason", reason)
	return result, nil
}

func hookPluginID(s *store.Store) (string, error) {
	value, err := ordjson.ReadFile(filepath.Join(s.Home, "state.json"))
	if err != nil {
		return "", err
	}
	obj := asObject(value)
	instance := asString(func() any { v, _ := obj.Get("instance"); return v }())
	if instance == "" {
		return "", fmt.Errorf("This instance has no identity yet; run ./bin/sumctl init in the coordinator pane first.")
	}
	if len(instance) > 12 {
		instance = instance[:12]
	}
	return "sum.returns." + instance, nil
}

func hookCommand(s *store.Store, sumctlPath string) []string {
	return []string{sumctlPath, "--home", s.Home, "hook", "event"}
}

func inboxCommand(s *store.Store, sumctlPath string) []string {
	return []string{"/bin/sh", "-c", "\"$0\" \"$@\"; printf '\\n[sum inbox] records only; press Enter to close\\n'; read _",
		sumctlPath, "--home", s.Home, "inbox"}
}

func tomlList(values []string) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = ordjson.QuoteString(v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// hookManifest ports `hook_manifest`.
func hookManifest(s *store.Store, sumctlPath string) (string, error) {
	pluginID, err := hookPluginID(s)
	if err != nil {
		return "", err
	}
	command := tomlList(hookCommand(s, sumctlPath))
	lines := []string{
		"id = " + ordjson.QuoteString(pluginID),
		"name = " + ordjson.QuoteString("sum returns "+filepath.Base(s.Home)),
		"version = " + ordjson.QuoteString(store.SumVersion),
		"min_herdr_version = " + ordjson.QuoteString(HerdrVersion),
		"description = " + ordjson.QuoteString("Runs the bounded sum returns pump for "+s.Home+" when a recorded pane changes state"),
		`platforms = ["linux", "macos"]`,
		"",
		"[[startup]]",
		"command = " + command,
		"",
	}
	for _, event := range HookEvents {
		lines = append(lines, "[[events]]", "on = "+ordjson.QuoteString(event), "command = "+command, "")
	}
	lines = append(lines, "[[panes]]", "id = "+ordjson.QuoteString(InboxEntrypoint), `title = "sum inbox"`, `placement = "popup"`,
		"command = "+tomlList(inboxCommand(s, sumctlPath)), "")
	return strings.Join(lines, "\n"), nil
}

// observePlugin ports `observe_plugin`: one bounded `plugin list` for this plugin id.
func observePlugin(runtimeRoot, session, pluginID string) *ordjson.Object {
	result := ordjson.NewObject()
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err == nil {
		var listed any
		listed, err = herdrclient.Call(herdrPath, session, 10*time.Second, "plugin", "list", "--plugin", pluginID, "--json")
		if err == nil {
			listedObj := asObject(listed)
			var plugin *ordjson.Object
			for _, pv := range asList(func() any {
				if listedObj == nil {
					return nil
				}
				v, _ := listedObj.Get("plugins")
				return v
			}()) {
				candidate := asObject(pv)
				idValue, _ := candidate.Get("plugin_id")
				if asString(idValue) == pluginID {
					plugin = candidate
					break
				}
			}
			if plugin == nil {
				result.Set("ok", false)
				result.Set("registered", false)
				result.Set("reason", fmt.Sprintf("%s is not linked in Herdr's plugin registry", pluginID))
				return result
			}
			enabledValue, _ := plugin.Get("enabled")
			result.Set("ok", true)
			result.Set("registered", true)
			result.Set("enabled", truthy(enabledValue))
			warnings, hasWarnings := plugin.Get("warnings")
			if !hasWarnings {
				warnings = []any{}
			}
			result.Set("warnings", warnings)
			manifestPath, _ := plugin.Get("manifest_path")
			result.Set("manifest_path", manifestPath)
			events := make([]any, 0)
			for _, ev := range asList(func() any { v, _ := plugin.Get("events"); return v }()) {
				e := asObject(ev)
				on, _ := e.Get("on")
				events = append(events, on)
			}
			result.Set("events", events)
			return result
		}
	}
	result.Set("ok", false)
	result.Set("registered", nil)
	result.Set("reason", fmt.Sprintf("plugin registry cannot be observed: %s", err))
	return result
}

// Status ports `hook_status`: health from records plus one bounded registry observation when a session is known.
func Status(s *store.Store, ctx *ordjson.Object, runtimeRoot, sumctlPath string) (*ordjson.Object, error) {
	value, err := Summary(s)
	if err != nil {
		return nil, err
	}
	health, err := readHealth(s)
	if err != nil {
		return nil, err
	}

	value.Set("health", pick(health, healthViewKeys))
	errList := asList(func() any { v, _ := health.Get("errors"); return v }())
	if len(errList) > HookErrors {
		errList = errList[len(errList)-HookErrors:]
	} else if errList == nil {
		errList = []any{}
	}
	value.Set("errors_log", errList)
	events := make([]any, len(HookEvents))
	for i, e := range HookEvents {
		events[i] = e
	}
	value.Set("supported_events", events)

	pluginIDValue, _ := health.Get("plugin_id")
	if truthy(pluginIDValue) {
		manifest, manifestErr := hookManifest(s, sumctlPath)
		if manifestErr == nil {
			manifestSHA, _ := health.Get("manifest_sha256")
			value.Set("expected_manifest_current", asString(manifestSHA) == sha256Text(manifest))
		} else {
			value.Set("expected_manifest_current", nil)
		}
	}

	if ctx != nil && truthy(pluginIDValue) {
		session := asString(func() any { v, _ := ctx.Get("session"); return v }())
		observed := observePlugin(runtimeRoot, session, asString(pluginIDValue))
		value.Set("registry", observed)
		enabledValue, _ := value.Get("enabled")
		registeredValue, _ := observed.Get("registered")
		enabledInRegistry, _ := observed.Get("enabled")
		if truthy(enabledValue) && !(truthy(registeredValue) && truthy(enabledInRegistry)) {
			value.Set("degraded", true)
			reasonValue, hasReason := observed.Get("reason")
			if !hasReason || !truthy(reasonValue) {
				reasonValue = "registered but disabled in Herdr; `hook enable` re-enables it"
			}
			value.Set("reason", reasonValue)
		}
	}
	value.Set("note", "Health is what this instance recorded; the registry row is what Herdr reports now. Degraded or disabled means the "+
		"synchronous path and `inbox --live` are the delivery path; nothing is lost, only not pushed.")
	return value, nil
}
