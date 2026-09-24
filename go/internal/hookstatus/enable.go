package hookstatus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

func writeHealth(s *store.Store, increments map[string]int, changes *ordjson.Object) (*ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	health, err := readHealth(s)
	if err != nil {
		return nil, err
	}
	for key, amount := range increments {
		health.Set(key, jsonInt(intField(health, key)+amount))
	}
	if changes != nil {
		for _, k := range changes.Keys() {
			v, _ := changes.Get(k)
			health.Set(k, v)
		}
	}
	errors := asList(func() any { v, _ := health.Get("errors"); return v }())
	if len(errors) > HookErrors {
		errors = errors[len(errors)-HookErrors:]
	}
	health.Set("errors", errors)
	health.Set("updated_at", store.Now())
	if err := ordjson.WriteFile(filepath.Join(s.Home, HookDir, HookHealth), health); err != nil {
		return nil, err
	}
	return health, nil
}

func intField(o *ordjson.Object, key string) int {
	v, _ := o.Get(key)
	switch n := v.(type) {
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case int:
		return n
	}
	return 0
}

func Enable(s *store.Store, ctx *ordjson.Object, runtimeRoot, sumctlPath string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		return nil, err
	}
	if _, err := herdrclient.EnsureVersion(herdrPath, contract.HerdrCLI); err != nil {
		return nil, err
	}
	pluginID, err := hookPluginID(s)
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(s.Home, HookDir, "plugin")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	manifest, err := hookManifest(s, sumctlPath)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "herdr-plugin.toml")
	changed := true
	if data, readErr := os.ReadFile(path); readErr == nil {
		changed = string(data) != manifest
	}
	if changed {
		if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
			return nil, err
		}
	}
	session := asString(func() any { v, _ := ctx.Get("session"); return v }())
	linked, err := herdrclient.Call(herdrPath, session, 15*time.Second, "plugin", "link", directory)
	if err != nil {
		return nil, err
	}
	linkedObj := asObject(linked)
	plugin := asObject(func() any {
		if linkedObj == nil {
			return nil
		}
		if v, ok := linkedObj.Get("plugin"); ok {
			return v
		}
		return linkedObj
	}())
	linkedID := asString(func() any { v, _ := plugin.Get("plugin_id"); return v }())
	if linkedID != pluginID {
		return nil, fmt.Errorf("Herdr linked %q instead of %s; inspect %s.", linkedID, pluginID, path)
	}
	if enabled, _ := plugin.Get("enabled"); !truthy(enabled) {
		if _, err := herdrclient.Call(herdrPath, session, 10*time.Second, "plugin", "enable", pluginID); err != nil {
			return nil, err
		}
	}
	from := ordjson.NewObject()
	from.Set("session", session)
	from.Set("pane", func() any { v, _ := ctx.Get("pane"); return v }())
	changes := ordjson.NewObject()
	changes.Set("enabled", true)
	changes.Set("plugin_id", pluginID)
	changes.Set("manifest_sha256", sha256Text(manifest))
	changes.Set("manifest_path", path)
	cmd := make([]any, 0)
	for _, c := range hookCommand(s, sumctlPath) {
		cmd = append(cmd, c)
	}
	changes.Set("command", cmd)
	changes.Set("linked_at", store.Now())
	changes.Set("linked_from", from)
	changes.Set("degraded", nil)
	warnings, has := plugin.Get("warnings")
	if !has {
		warnings = []any{}
	}
	changes.Set("warnings", warnings)
	if _, err := writeHealth(s, nil, changes); err != nil {
		return nil, err
	}
	reconciliation, err := returns.Pump(s, returns.PumpOpts{
		RuntimeRoot: runtimeRoot,
		SumctlPath:  sumctlPath,
		Ctx:         ctx,
		Reason:      "native event delivery enabled; catching up on saved returns",
		Inline:      true,
	})
	if err != nil {
		return nil, err
	}
	events := make([]any, len(HookEvents))
	for i, e := range HookEvents {
		events[i] = e
	}
	result := ordjson.NewObject()
	result.Set("plugin_id", pluginID)
	result.Set("manifest", path)
	result.Set("manifest_changed", changed)
	result.Set("command", cmd)
	result.Set("events", events)
	result.Set("warnings", warnings)
	result.Set("reconciliation", reconciliation)
	result.Set("note", "Linked live without stopping Herdr. Registration is user-global; the handler acts only on panes recorded by this instance in the event's own Herdr session. Startup hooks do not run at link time, so one bounded reconciliation ran now. Disabling or a handler failure returns to the synchronous ask/report path and `inbox --live`; nothing stops.")
	return result, nil
}

func Disable(s *store.Store, ctx *ordjson.Object, runtimeRoot string, unlink bool) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	pluginID, err := hookPluginID(s)
	if err != nil {
		return nil, err
	}
	session := asString(func() any { v, _ := ctx.Get("session"); return v }())
	observed := observePlugin(runtimeRoot, session, pluginID)
	var action any
	if registered, _ := observed.Get("registered"); registered == true {
		herdrPath, findErr := toolpath.Find(runtimeRoot, "herdr")
		if findErr != nil {
			return nil, findErr
		}
		verb := "disable"
		if unlink {
			verb = "unlink"
		}
		if _, err := herdrclient.Call(herdrPath, session, 10*time.Second, "plugin", verb, pluginID); err != nil {
			return nil, err
		}
		if unlink {
			action = "unlinked"
		} else {
			action = "disabled"
		}
	}
	changes := ordjson.NewObject()
	changes.Set("enabled", false)
	changes.Set("disabled_at", store.Now())
	changes.Set("degraded", nil)
	if _, err := writeHealth(s, nil, changes); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("plugin_id", pluginID)
	result.Set("action", action)
	result.Set("previously", observed)
	result.Set("note", "Native delivery is off; saved returns keep accumulating and `inbox --live`, `init`, `bind`, and `pump` still deliver them. No work stopped.")
	return result, nil
}
