package metadata

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	Dir    = "metadata"
	File   = "state.json"
	Schema = 1
)

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

var TaskTokens = []string{"sum_state", "sum_task", "sum_repo", "sum_rev", "sum_pr"}

var RootTokens = []string{"sum_inbox", "sum_tasks"}

var SumStates = []string{
	"needs-attention", "needs-decision", "merged-cleanup-pending", "review-ready", "attention-blocked", "attention-exited",
	"attention-closed", "attention-idle", "instruction-refresh-pending", "answer-pending", "pr-open", "verified", "preparing", "running",
}

func snippetTOML() string {
	return strings.Join([]string{
		"# sum: optional sidebar rows that render sum's task tokens. Merge into ~/.config/herdr/config.toml, then run",
		"# `herdr server reload-config`. `rows` replaces the whole layout, so keep the built-in tokens you already use.",
		"[ui.sidebar.agents]",
		`rows = [["state_icon", "workspace", "tab"], ["agent", "$sum_state"], ["$sum_task", "$sum_inbox"]]`,
		"",
		"[ui.sidebar.spaces]",
		`rows = [["state_icon", "workspace"], ["branch", "git_status"], ["$sum_state", "$sum_task"]]`,
		"",
		"# Optional: pop-up notifications for sum transitions need a toast delivery; sum sends them only after `metadata enable --notify`.",
		"# [ui.toast]",
		`# delivery = "herdr"`,
		"",
	}, "\n")
}

func Snippet(sumctlPath, home string) *ordjson.Object {
	tokens := ordjson.NewObject()
	tokens.Set("task", toAny(TaskTokens))
	tokens.Set("coordinator", toAny(RootTokens))

	result := ordjson.NewObject()
	result.Set("toml", snippetTOML())
	result.Set("tokens", tokens)
	result.Set("states", toAny(SumStates))
	result.Set("enable", shquote.CommandFor(sumctlPath, home, "metadata", "enable"))
	result.Set("inbox", shquote.CommandFor(sumctlPath, home, "metadata", "inbox"))
	result.Set("note", "Nothing here is written by sum: the snippet is text for the user to merge. Without these rows the tokens exist but stay out of sight; every other setting (theme, keybindings, labels, toast delivery) is the user's.")
	return result
}

func toAny(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

func emptyMetadata() *ordjson.Object {
	m := ordjson.NewObject()
	m.Set("schema", jsonInt(Schema))
	m.Set("enabled", false)
	m.Set("notify", false)
	m.Set("source", nil)
	m.Set("capabilities", ordjson.NewObject())
	m.Set("resources", ordjson.NewObject())
	m.Set("root", nil)
	m.Set("notified", ordjson.NewObject())
	m.Set("errors", []any{})
	m.Set("degraded", nil)
	stats := ordjson.NewObject()
	stats.Set("passes", jsonInt(0))
	stats.Set("writes", jsonInt(0))
	stats.Set("cleared", jsonInt(0))
	stats.Set("notifications", jsonInt(0))
	m.Set("stats", stats)
	return m
}

func Path(s *store.Store) string {
	return filepath.Join(s.Home, Dir, File)
}

func ReadMetadata(s *store.Store) (*ordjson.Object, error) {
	path := Path(s)
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must not be a symlink", path)
	}
	info, statErr := os.Stat(path)
	if statErr != nil || info.IsDir() {
		return emptyMetadata(), nil
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
	if !schemaOK {
		return nil, fmt.Errorf("unsupported metadata schema in %s; inspect it, sum never migrates it in place.", path)
	}
	merged := emptyMetadata()
	for _, k := range obj.Keys() {
		v, _ := obj.Get(k)
		merged.Set(k, v)
	}
	return merged, nil
}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	default:
		return v != nil
	}
}

func Summary(s *store.Store) *ordjson.Object {
	meta, err := ReadMetadata(s)
	if err != nil {
		result := ordjson.NewObject()
		result.Set("enabled", false)
		result.Set("degraded", true)
		result.Set("reason", fmt.Sprintf("metadata state unreadable: %s", err))
		return result
	}
	enabled, _ := meta.Get("enabled")
	notify, hasNotify := meta.Get("notify")
	if !hasNotify {
		notify = false
	}
	source, _ := meta.Get("source")
	capabilities, _ := meta.Get("capabilities")
	resourcesValue, _ := meta.Get("resources")
	resources, _ := resourcesValue.(*ordjson.Object)
	resourceCount := 0
	if resources != nil {
		resourceCount = resources.Len()
	}
	lastPass, _ := meta.Get("last_pass")
	lastNotification, _ := meta.Get("last_notification")
	errorsValue, _ := meta.Get("errors")
	errorsList, _ := errorsValue.([]any)
	lastError, _ := meta.Get("last_error")
	degradedValue, _ := meta.Get("degraded")
	degraded := truthy(degradedValue) || enabled != true

	result := ordjson.NewObject()
	result.Set("enabled", enabled)
	result.Set("notify", notify)
	result.Set("source", source)
	result.Set("capabilities", capabilities)
	result.Set("resources", jsonInt(resourceCount))
	result.Set("last_pass", lastPass)
	result.Set("last_notification", lastNotification)
	result.Set("errors", jsonInt(len(errorsList)))
	result.Set("last_error", lastError)
	result.Set("degraded", degraded)
	switch {
	case truthy(degradedValue):
		result.Set("reason", degradedValue)
	case enabled != true:
		result.Set("reason", "native metadata projection is not enabled; `inbox --live` remains the authoritative view")
	default:
		result.Set("reason", nil)
	}
	return result
}
