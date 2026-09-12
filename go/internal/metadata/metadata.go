package metadata

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
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

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asObject(v any) *ordjson.Object {
	o, _ := v.(*ordjson.Object)
	return o
}

func asList(v any) []any {
	list, _ := v.([]any)
	return list
}

func sourceID(s *store.Store) string {
	return "sum:" + filepath.Base(s.Home)
}

func reportTokens(runtimeRoot, session, kind, id, source string, tokens map[string]string) error {
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		return err
	}
	args := []string{kind, "report-metadata", id, "--source", source}
	for k, v := range tokens {
		args = append(args, "--token", k+"="+v)
	}
	_, err = herdrclient.CallRaw(herdrPath, session, 10*time.Second, args...)
	return err
}

func project(s *store.Store, ctx *ordjson.Object, runtimeRoot string) error {
	session := asString(func() any { v, _ := ctx.Get("session"); return v }())
	source := sourceID(s)
	tasks, err := s.AllTasks()
	if err != nil {
		return err
	}
	active := 0
	decisions := 0
	for _, task := range tasks {
		if asString(func() any { v, _ := task.Get("status"); return v }()) == "archived" {
			continue
		}
		active++
		open := 0
		for _, raw := range asList(func() any { v, _ := task.Get("questions"); return v }()) {
			q := asObject(raw)
			if asString(func() any { x, _ := q.Get("status"); return x }()) != "applied" {
				open++
			}
		}
		decisions += open
		state := "running"
		if open > 0 {
			state = "needs-decision"
		}
		repo := asString(func() any { v, _ := task.Get("repository"); return v }())
		tokens := map[string]string{
			"sum_state": state,
			"sum_task":  asString(func() any { v, _ := task.Get("id"); return v }()),
			"sum_repo":  filepath.Base(repo),
		}
		pane := asString(func() any { v, _ := task.Get("pane"); return v }())
		workspace := asString(func() any { v, _ := task.Get("workspace"); return v }())
		if pane != "" {
			if err := reportTokens(runtimeRoot, session, "pane", pane, source, tokens); err != nil {
				return err
			}
		}
		if workspace != "" {
			if err := reportTokens(runtimeRoot, session, "workspace", workspace, source, map[string]string{"sum_state": state}); err != nil {
				return err
			}
		}
	}
	rootTokens := map[string]string{}
	if decisions == 1 {
		rootTokens["sum_inbox"] = "1 decision"
	} else if decisions > 1 {
		rootTokens["sum_inbox"] = fmt.Sprintf("%d decisions", decisions)
	} else {
		rootTokens["sum_inbox"] = "clear"
	}
	rootTokens["sum_tasks"] = fmt.Sprintf("%d active", active)
	coordPane := asString(func() any { v, _ := ctx.Get("pane"); return v }())
	if coordPane != "" {
		if err := reportTokens(runtimeRoot, session, "pane", coordPane, source, rootTokens); err != nil {
			return err
		}
	}
	return nil
}

func Enable(s *store.Store, ctx *ordjson.Object, runtimeRoot string, notify bool) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	if err := project(s, ctx, runtimeRoot); err != nil {
		return nil, err
	}
	meta, err := ReadMetadata(s)
	if err != nil {
		return nil, err
	}
	caps := ordjson.NewObject()
	caps.Set("pane_tokens", true)
	caps.Set("workspace_tokens", true)
	meta.Set("enabled", true)
	meta.Set("notify", notify)
	meta.Set("source", sourceID(s))
	meta.Set("capabilities", caps)
	meta.Set("degraded", nil)
	meta.Set("last_pass", store.Now())
	if err := os.MkdirAll(filepath.Dir(Path(s)), 0o700); err != nil {
		return nil, err
	}
	if err := ordjson.WriteFile(Path(s), meta); err != nil {
		return nil, err
	}
	result := Summary(s)
	result.Set("source", sourceID(s))
	result.Set("capabilities", caps)
	result.Set("notify", notify)
	result.Set("note", "Display-only projection. Herdr's agent lifecycle is unchanged.")
	return result, nil
}

func Sync(s *store.Store, ctx *ordjson.Object, runtimeRoot string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	if err := project(s, ctx, runtimeRoot); err != nil {
		return nil, err
	}
	return Summary(s), nil
}

func Disable(s *store.Store, ctx *ordjson.Object) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	meta, err := ReadMetadata(s)
	if err != nil {
		return nil, err
	}
	meta.Set("enabled", false)
	if err := ordjson.WriteFile(Path(s), meta); err != nil {
		return nil, err
	}
	return Summary(s), nil
}
