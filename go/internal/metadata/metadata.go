package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/inboxview"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

const (
	Dir    = "metadata"
	File   = "state.json"
	Schema = 1

	// MaxErrors bounds the recorded projection error log.
	MaxErrors = 20

	callTimeout  = 10 * time.Second
	probeTimeout = 15 * time.Second
	tokenMax     = 80
)

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

var TaskTokens = []string{"sum_state", "sum_pipeline", "sum_task", "sum_repo", "sum_rev", "sum_pr"}

var RootTokens = []string{"sum_inbox", "sum_tasks"}

var SumStates = []string{
	"needs-attention", "needs-decision", "merged-cleanup-pending", "review-ready", "attention-blocked", "attention-exited",
	"attention-closed", "attention-idle", "instruction-refresh-pending", "answer-pending", "pr-open", "verified", "preparing", "running",
}

const note = "Display-only projection. Herdr's agent lifecycle is unchanged; tokens are written only to endpoints this installation recorded and verified, and only when a value changed."

func snippetTOML() string {
	return strings.Join([]string{
		"# sum: optional sidebar rows that render sum's task tokens. Merge into ~/.config/herdr/config.toml, then run",
		"# `herdr server reload-config`. `rows` replaces the whole layout, so keep the built-in tokens you already use.",
		"# Tokens: sum_state, sum_pipeline, sum_task, sum_repo, sum_rev (active>requested brief revision), sum_pr (recorded PR URL)",
		"# on task panes and workspaces; sum_inbox and sum_tasks on the coordinator pane. Rows show one endpoint's tokens;",
		"# the project grouping lives in `inbox --grouped`, and `metadata inbox --open` opens that view natively after `hook enable`.",
		"[ui.sidebar.agents]",
		`rows = [["state_icon", "workspace", "tab"], ["agent", "$sum_state", "$sum_pipeline"], ["$sum_task", "$sum_inbox"]]`,
		"",
		"[ui.sidebar.spaces]",
		`rows = [["state_icon", "workspace"], ["branch", "git_status"], ["$sum_state", "$sum_task"]]`,
		"",
		"# Reserved: `metadata enable --notify` records a preference; transition notification delivery is not implemented.",
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
	m.Set("ambiguous", []any{})
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

func writeMetadata(s *store.Store, meta *ordjson.Object) error {
	errs := asList(get(meta, "errors"))
	if len(errs) > MaxErrors {
		errs = errs[len(errs)-MaxErrors:]
	}
	meta.Set("errors", errs)
	meta.Set("updated_at", store.Now())
	if err := os.MkdirAll(filepath.Dir(Path(s)), 0o700); err != nil {
		return err
	}
	return ordjson.WriteFile(Path(s), meta)
}

// lock serializes projection passes (a task write and a native event may coincide) without holding the task-state
// lock during Herdr I/O.
func lock(s *store.Store) (func(), error) {
	if err := os.MkdirAll(filepath.Join(s.Home, Dir), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.Home, Dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
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

func get(o *ordjson.Object, key string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	return v
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

func Summary(s *store.Store) *ordjson.Object {
	meta, err := ReadMetadata(s)
	if err != nil {
		result := ordjson.NewObject()
		result.Set("enabled", false)
		result.Set("degraded", true)
		result.Set("reason", fmt.Sprintf("metadata state unreadable: %s", err))
		return result
	}
	return summaryOf(meta)
}

func summaryOf(meta *ordjson.Object) *ordjson.Object {
	enabled := get(meta, "enabled")
	notify, hasNotify := meta.Get("notify")
	if !hasNotify {
		notify = false
	}
	resources := asObject(get(meta, "resources"))
	resourceCount := 0
	if resources != nil {
		resourceCount = resources.Len()
	}
	degradedValue := get(meta, "degraded")

	result := ordjson.NewObject()
	result.Set("enabled", enabled)
	result.Set("notify", notify)
	result.Set("source", get(meta, "source"))
	result.Set("capabilities", get(meta, "capabilities"))
	result.Set("resources", jsonInt(resourceCount))
	result.Set("last_pass", get(meta, "last_pass"))
	result.Set("last_notification", get(meta, "last_notification"))
	result.Set("errors", jsonInt(len(asList(get(meta, "errors")))))
	result.Set("last_error", get(meta, "last_error"))
	result.Set("degraded", truthy(degradedValue) || enabled != true)
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

// Status is Summary plus the recorded detail: saved capabilities, per-task applied tokens, the coordinator pane
// record, ambiguous legacy sources seen in the last pass, and the bounded error log. Records only, no Herdr call.
func Status(s *store.Store) *ordjson.Object {
	meta, err := ReadMetadata(s)
	if err != nil {
		return Summary(s)
	}
	result := summaryOf(meta)
	detail := ordjson.NewObject()
	if resources := asObject(get(meta, "resources")); resources != nil {
		for _, taskID := range resources.Keys() {
			rec := asObject(get(resources, taskID))
			row := ordjson.NewObject()
			row.Set("state", get(rec, "state"))
			for _, kind := range []string{"pane", "workspace"} {
				if endpoint := asObject(get(rec, kind)); endpoint != nil {
					view := ordjson.NewObject()
					view.Set("id", get(endpoint, "id"))
					view.Set("tokens", get(endpoint, "tokens"))
					row.Set(kind, view)
				}
			}
			detail.Set(taskID, row)
		}
	}
	result.Set("resources_detail", detail)
	result.Set("root", get(meta, "root"))
	ambiguous := asList(get(meta, "ambiguous"))
	if ambiguous == nil {
		ambiguous = []any{}
	}
	result.Set("ambiguous", ambiguous)
	errs := asList(get(meta, "errors"))
	if errs == nil {
		errs = []any{}
	}
	result.Set("errors_log", errs)
	result.Set("states", toAny(SumStates))
	result.Set("note", note)
	return result
}

// Capability is one saved probe result; false when never probed.
func Capability(s *store.Store, name string) bool {
	meta, err := ReadMetadata(s)
	if err != nil {
		return false
	}
	return truthy(get(asObject(get(meta, "capabilities")), name))
}

// Source is this instance's reporter identity, derived from the instance id like the hook plugin id, so two homes
// with the same basename never share a source.
func Source(s *store.Store) (string, error) {
	value, err := ordjson.ReadFile(filepath.Join(s.Home, "state.json"))
	if err != nil {
		return "", err
	}
	instance := asString(get(asObject(value), "instance"))
	if instance == "" {
		return "", fmt.Errorf("This instance has no identity yet; run ./bin/sumctl init in the coordinator pane first.")
	}
	if len(instance) > 12 {
		instance = instance[:12]
	}
	return "sum:" + instance, nil
}

// tokenValue is Herdr's own normalization: one line, printable, 80 characters.
func tokenValue(text string) string {
	var b strings.Builder
	for _, r := range text {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	value := strings.Join(strings.Fields(b.String()), " ")
	if len(value) > tokenMax {
		value = value[:tokenMax]
	}
	return value
}

func probeCapabilities(herdrPath, session string) *ordjson.Object {
	caps := ordjson.NewObject()
	schema, err := herdrclient.Call(herdrPath, session, probeTimeout, "api", "schema", "--json")
	has := func(name, field string) bool {
		defs := asObject(get(asObject(get(asObject(get(asObject(schema), "schemas")), "request")), "$defs"))
		props := asObject(get(asObject(get(defs, name)), "properties"))
		if props == nil {
			return false
		}
		_, ok := props.Get(field)
		return ok
	}
	caps.Set("pane_tokens", err == nil && has("PaneReportMetadataParams", "tokens"))
	caps.Set("workspace_tokens", err == nil && has("WorkspaceReportMetadataParams", "tokens"))
	caps.Set("notification", err == nil && has("NotificationShowParams", "title"))
	caps.Set("plugin_pane_open", err == nil && has("PluginPaneOpenParams", "entrypoint"))
	caps.Set("protocol", get(asObject(schema), "protocol"))
	caps.Set("probed_at", store.Now())
	if err != nil {
		msg := err.Error()
		if len(msg) > 200 {
			msg = msg[:200]
		}
		caps.Set("error", "api schema unavailable: "+msg)
	}
	return caps
}

func supportsTokens(caps *ordjson.Object) bool {
	return truthy(get(caps, "pane_tokens")) || truthy(get(caps, "workspace_tokens"))
}

// pass is one bounded projection over the records, counting every Herdr call it makes.
type pass struct {
	s         *store.Store
	meta      *ordjson.Object
	herdrPath string
	source    string
	force     bool
	calls     int
	written   int
	ambiguous []any
}

func (p *pass) observe(session string, args ...string) (any, string, error) {
	p.calls++
	return herdrclient.Observe(p.herdrPath, session, callTimeout, args...)
}

func (p *pass) sessionCall(session string) incarnation.Call {
	return func(timeout time.Duration, args ...string) (any, string, error) {
		p.calls++
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return herdrclient.ObserveContext(ctx, p.herdrPath, session, timeout, args...)
	}
}

// report is one report-metadata call: ok on success, Herdr's code when refused, err when the helper failed.
func (p *pass) report(kind, session, id string, sets map[string]string, clears []string) (bool, string, error) {
	args := []string{kind, "report-metadata", id, "--source", p.source}
	for _, key := range sortedKeys(sets) {
		args = append(args, "--token", key+"="+sets[key])
	}
	sort.Strings(clears)
	for _, key := range clears {
		args = append(args, "--clear-token", key)
	}
	p.calls++
	code, err := herdrclient.CallCode(p.herdrPath, session, callTimeout, args...)
	if err != nil {
		return false, "", err
	}
	return code == "", code, nil
}

func (p *pass) recordError(stage string, detail string, kind, id string) {
	if len(detail) > 500 {
		detail = detail[:500]
	}
	row := ordjson.NewObject()
	row.Set("at", store.Now())
	row.Set("stage", stage)
	row.Set("error", detail)
	if kind != "" {
		row.Set("kind", kind)
		row.Set("id", id)
	}
	errs := append(asList(get(p.meta, "errors")), row)
	p.meta.Set("errors", errs)
	p.meta.Set("last_error", row)
}

func (p *pass) degrade(reason string) {
	if len(reason) > 200 {
		reason = reason[:200]
	}
	p.meta.Set("degraded", reason)
}

func (p *pass) bump(key string, n int) {
	stats := asObject(get(p.meta, "stats"))
	if stats == nil {
		stats = ordjson.NewObject()
		p.meta.Set("stats", stats)
	}
	current := 0
	if num, ok := get(stats, key).(json.Number); ok {
		if i, err := num.Int64(); err == nil {
			current = int(i)
		}
	}
	stats.Set(key, jsonInt(current+n))
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func isAbsentCode(code string) bool {
	return code == "pane_not_found" || code == "workspace_not_found"
}

// record helpers: {id, session, tokens} objects as stored under resources and root.

func recordTokens(rec *ordjson.Object) map[string]string {
	out := map[string]string{}
	if tokens := asObject(get(rec, "tokens")); tokens != nil {
		for _, k := range tokens.Keys() {
			out[k] = asString(get(tokens, k))
		}
	}
	return out
}

func newRecord(id, session string, tokens map[string]string) *ordjson.Object {
	rec := ordjson.NewObject()
	rec.Set("id", id)
	rec.Set("session", session)
	obj := ordjson.NewObject()
	for _, k := range sortedKeys(tokens) {
		obj.Set(k, tokens[k])
	}
	rec.Set("tokens", obj)
	return rec
}

func tokensToAny(list []string) []any {
	sort.Strings(list)
	return toAny(list)
}

// clear clears exactly the recorded keys on one endpoint. An absent endpoint counts as cleared; a refused or failed
// clear is recorded and reported, and the record stays for a later attempt. ok is whether the keys are gone.
func (p *pass) clear(kind, session, id string, tokens map[string]string) (row *ordjson.Object, ok bool) {
	row = ordjson.NewObject()
	keys := sortedKeys(tokens)
	if len(keys) == 0 {
		row.Set("cleared", []any{})
		return row, true
	}
	succeeded, code, err := p.report(kind, session, id, nil, append([]string{}, keys...))
	switch {
	case err != nil:
		p.recordError("clear", err.Error(), kind, id)
		row.Set("cleared", []any{})
		row.Set("failed", truncate(err.Error(), 200))
		return row, false
	case succeeded:
		p.bump("cleared", len(keys))
		row.Set("cleared", tokensToAny(keys))
		return row, true
	case isAbsentCode(code):
		row.Set("cleared", tokensToAny(keys))
		row.Set("absent", code)
		return row, true
	default:
		p.recordError("clear", code, kind, id)
		row.Set("cleared", []any{})
		row.Set("refused", code)
		return row, false
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// observation is what one `pane get` said about an endpoint, for identity and legacy-source checks.
type observation struct {
	identity string
	sources  map[string]string
	value    any
}

// verify is an endpoint identity check run before the first differing write in a pass.
type verify func() observation

// endpoint brings one pane or workspace to desired, writing only the differing keys. It returns the outcome row and
// the record of what is now known to be applied there (nil when nothing owned remains).
func (p *pass) endpoint(kind, session, id string, desired map[string]string, rec *ordjson.Object, check verify) (*ordjson.Object, *ordjson.Object) {
	row := ordjson.NewObject()
	row.Set("kind", kind)
	row.Set("id", id)
	previous := map[string]string{}
	if rec != nil && asString(get(rec, "id")) == id {
		previous = recordTokens(rec)
	} else if rec != nil && asString(get(rec, "id")) != "" && len(recordTokens(rec)) > 0 {
		// The task moved to another endpoint (rebind): clear only sum's keys on the old one, without observing it.
		cleared, _ := p.clear(kind, asString(get(rec, "session")), asString(get(rec, "id")), recordTokens(rec))
		cleared.Set("id", get(rec, "id"))
		row.Set("cleared_previous", cleared)
	}
	keep := func() *ordjson.Object {
		if len(previous) == 0 {
			return nil
		}
		return newRecord(id, session, previous)
	}
	sets := map[string]string{}
	for k, v := range desired {
		if p.force || previous[k] != v {
			sets[k] = v
		}
	}
	var clears []string
	for k := range previous {
		if _, wanted := desired[k]; !wanted {
			clears = append(clears, k)
		}
	}
	if len(sets) == 0 && len(clears) == 0 {
		row.Set("outcome", "unchanged")
		return row, keep()
	}
	capability := kind + "_tokens"
	if !truthy(get(asObject(get(p.meta, "capabilities")), capability)) {
		row.Set("outcome", "unsupported")
		row.Set("reason", capability+" not available in the probed Herdr build")
		return row, keep()
	}
	if check != nil && len(sets) > 0 {
		obs := check()
		row.Set("identity", obs.identity)
		switch obs.identity {
		case "absent":
			row.Set("outcome", "absent")
			return row, nil
		case "unobservable":
			row.Set("outcome", "unobservable")
			row.Set("reason", "the endpoint cannot be observed; nothing was written or cleared")
			return row, keep()
		case "ok":
			// Keys another sum source holds (a legacy `sum:<basename>` reporter, another installation) are neither
			// overwritten nor cleared; they are reported instead.
			for key, source := range obs.sources {
				if !strings.HasPrefix(source, "sum:") || source == p.source {
					continue
				}
				if _, wanted := sets[key]; !wanted && !containsString(clears, key) {
					continue
				}
				delete(sets, key)
				clears = removeString(clears, key)
				delete(previous, key)
				entry := ordjson.NewObject()
				entry.Set("kind", kind)
				entry.Set("id", id)
				entry.Set("key", key)
				entry.Set("source", source)
				p.ambiguous = append(p.ambiguous, entry)
			}
			if len(sets) == 0 && len(clears) == 0 {
				row.Set("outcome", "unchanged")
				return row, keep()
			}
		default:
			// stale: our own keys sit on a pane that no longer runs the task. Clear them, write nothing new.
			if len(previous) > 0 {
				cleared, ok := p.clear(kind, session, id, previous)
				for _, k := range cleared.Keys() {
					row.Set(k, get(cleared, k))
				}
				if !ok {
					row.Set("outcome", "stale")
					row.Set("reason", "pane identity stale and its recorded keys could not be cleared")
					return row, keep()
				}
			}
			row.Set("outcome", "stale")
			row.Set("reason", "pane identity stale; tokens are written only to a verified endpoint")
			return row, nil
		}
	}
	ok, code, err := p.report(kind, session, id, sets, append([]string{}, clears...))
	switch {
	case err != nil:
		p.degrade(kind + " report-metadata failed: " + err.Error())
		p.recordError("report", err.Error(), kind, id)
		row.Set("outcome", "failed")
		row.Set("reason", truncate(err.Error(), 200))
		return row, keep()
	case !ok && isAbsentCode(code):
		row.Set("outcome", "absent")
		row.Set("code", code)
		return row, nil
	case !ok:
		p.degrade(kind + " report-metadata refused: " + code)
		p.recordError("report", code, kind, id)
		row.Set("outcome", "refused")
		row.Set("code", code)
		return row, keep()
	}
	p.written++
	p.bump("writes", 1)
	p.bump("cleared", len(clears))
	applied := map[string]string{}
	for k, v := range previous {
		if !containsString(clears, k) {
			applied[k] = v
		}
	}
	for k, v := range sets {
		applied[k] = v
	}
	row.Set("outcome", "written")
	row.Set("set", tokensToAny(sortedKeys(sets)))
	row.Set("cleared", tokensToAny(clears))
	if len(applied) == 0 {
		return row, nil
	}
	return row, newRecord(id, session, applied)
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func removeString(list []string, s string) []string {
	out := list[:0]
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

func paneObject(value any) *ordjson.Object {
	obj := asObject(value)
	if inner := asObject(get(obj, "pane")); inner != nil {
		return inner
	}
	return obj
}

func paneSources(obj *ordjson.Object) map[string]string {
	out := map[string]string{}
	if sources := asObject(get(obj, "token_sources")); sources != nil {
		for _, k := range sources.Keys() {
			out[k] = asString(get(sources, k))
		}
	}
	return out
}

// verifyPane observes one pane: ok when it still runs in the expected checkout, absent, stale (another cwd: a reused
// or rebound pane), or unobservable.
func (p *pass) verifyPane(session, pane, expectedCwd string) observation {
	value, code, err := p.observe(session, "pane", "get", pane)
	if err != nil {
		return observation{identity: "unobservable"}
	}
	if code == "pane_not_found" {
		return observation{identity: "absent"}
	}
	if code != "" {
		return observation{identity: "unobservable"}
	}
	obj := paneObject(value)
	cwd := asString(get(obj, "cwd"))
	if cwd == "" {
		cwd = asString(get(obj, "working_directory"))
	}
	obs := observation{identity: "unobservable", sources: paneSources(obj), value: value}
	if cwd == "" || expectedCwd == "" {
		return obs
	}
	if samePath(cwd, expectedCwd) {
		obs.identity = "ok"
	} else {
		obs.identity = "stale"
	}
	return obs
}

// verifyOwner is verifyPane for the coordinator pane plus the occupant check the coordinator role itself uses.
func (p *pass) verifyOwner(owner *ordjson.Object) observation {
	session, pane := asString(get(owner, "session")), asString(get(owner, "pane"))
	obs := p.verifyPane(session, pane, asString(get(owner, "cwd")))
	if obs.identity != "ok" {
		return obs
	}
	recorded, occupiedAt := incarnation.CoordinatorRecord(owner)
	call := p.sessionCall(session)
	probe := func() (*incarnation.Shell, error) { return incarnation.ProbeShell(call, pane) }
	verdict := incarnation.Judge(recorded, occupiedAt, incarnation.FromInfo(obs.value), probe)
	if !verdict.Verified {
		if verdict.Outcome == incarnation.Unobservable {
			obs.identity = "unobservable"
		} else {
			obs.identity = "stale"
		}
	}
	return obs
}

func (p *pass) taskTokens(task *ordjson.Object, state string) map[string]string {
	if state == "" {
		return map[string]string{}
	}
	tokens := map[string]string{
		"sum_state":    state,
		"sum_pipeline": inboxview.Stage(task),
		"sum_task":     asString(get(task, "id")),
		"sum_repo":     tokenValue(filepath.Base(asString(get(task, "repository")))),
	}
	if v, err := versions.ReadVersions(p.s, task); err == nil && v != nil {
		requested, active := get(v, "requested"), get(v, "active")
		if requested != nil && requested != active {
			tokens["sum_rev"] = tokenValue(fmt.Sprintf("%v>%v", active, requested))
		}
	}
	if url := asString(get(asObject(get(asObject(get(task, "pr")), "identity")), "url")); url != "" {
		tokens["sum_pr"] = tokenValue(url)
	}
	return tokens
}

func (p *pass) projectTask(row inboxview.Task, gapped, archived bool, resources *ordjson.Object) *ordjson.Object {
	task := row.Record
	state := ""
	if !archived {
		state = inboxview.TaskState(row, gapped)
	}
	desired := p.taskTokens(task, state)
	rec := asObject(get(resources, row.ID))
	out := ordjson.NewObject()
	out.Set("task", row.ID)
	if state == "" {
		out.Set("state", nil)
	} else {
		out.Set("state", state)
	}
	out.Set("previous", get(rec, "state"))
	endpoints := []any{}
	updated := ordjson.NewObject()
	if state == "" {
		updated.Set("state", nil)
	} else {
		updated.Set("state", state)
	}
	session := asString(get(task, "session"))
	if session == "" {
		session = asString(get(asObject(get(p.meta, "root")), "session"))
	}

	workspace := asString(get(task, "workspace"))
	if workspace != "" && session != "" {
		endpointRow, record := p.endpoint("workspace", session, workspace, desired, asObject(get(rec, "workspace")), nil)
		endpoints = append(endpoints, endpointRow)
		if record != nil {
			updated.Set("workspace", record)
		}
	} else if old := asObject(get(rec, "workspace")); old != nil {
		endpoints = append(endpoints, p.release("workspace", old))
	}

	pane := asString(get(task, "pane"))
	if pane != "" && session != "" {
		worktree := asString(get(task, "worktree"))
		check := func() observation { return p.verifyPane(session, pane, worktree) }
		endpointRow, record := p.endpoint("pane", session, pane, desired, asObject(get(rec, "pane")), check)
		endpoints = append(endpoints, endpointRow)
		if record != nil {
			updated.Set("pane", record)
		}
	} else if old := asObject(get(rec, "pane")); old != nil {
		endpoints = append(endpoints, p.release("pane", old))
	}
	out.Set("endpoints", endpoints)
	_, hasPane := updated.Get("pane")
	_, hasWorkspace := updated.Get("workspace")
	if state == "" && !hasPane && !hasWorkspace {
		resources.Delete(row.ID)
		out.Set("released", true)
	} else {
		resources.Set(row.ID, updated)
	}
	return out
}

// release clears what sum wrote on an endpoint a task no longer names; a failed clear keeps the row for a retry.
func (p *pass) release(kind string, old *ordjson.Object) *ordjson.Object {
	cleared, _ := p.clear(kind, asString(get(old, "session")), asString(get(old, "id")), recordTokens(old))
	cleared.Set("kind", kind)
	cleared.Set("id", get(old, "id"))
	cleared.Set("outcome", "released")
	return cleared
}

func inboxToken(snapshot inboxview.Snapshot) string {
	labels := []string{}
	if !snapshot.Complete {
		labels = append(labels, "unknown")
	}
	for _, count := range []struct {
		n                int
		singular, plural string
	}{
		{snapshot.Counts.Decisions, "decision", "decisions"},
		{snapshot.Counts.Coordinator - snapshot.Counts.Inspection, "coordinator", "coordinator"},
		{snapshot.Counts.Worker, "worker", "worker"},
		{snapshot.Counts.Inspection, "inspection", "inspections"},
	} {
		if count.n == 1 {
			labels = append(labels, "1 "+count.singular)
		} else if count.n > 1 {
			labels = append(labels, fmt.Sprintf("%d %s", count.n, count.plural))
		}
	}
	if len(labels) == 0 {
		return "clear"
	}
	text := strings.Join(labels, "; ")
	if len(text) <= tokenMax {
		return text
	}
	// Herdr caps tokens at 80 bytes. Keep exact decisions and unknown coverage;
	// full compact counts remain available when the other counts will not fit.
	prefix := ""
	if !snapshot.Complete {
		prefix = "unknown; "
	}
	return fmt.Sprintf("%s%d decisions; other work pending", prefix, snapshot.Counts.Decisions)
}

func rootTokens(snapshot inboxview.Snapshot, active int) map[string]string {
	tasks := fmt.Sprintf("%d active", active)
	if !snapshot.Complete {
		tasks += "; coverage unknown"
	}
	return map[string]string{"sum_inbox": inboxToken(snapshot), "sum_tasks": tasks}
}

func (p *pass) projectRoot(snapshot inboxview.Snapshot, active int) *ordjson.Object {
	previous := asObject(get(p.meta, "root"))
	owner, err := p.s.Owner()
	host, hostErr := p.s.Machine()
	if err != nil || owner == nil || hostErr != nil || !host.Is(get(owner, "machine")) {
		row := ordjson.NewObject()
		row.Set("kind", "pane")
		row.Set("role", "coordinator")
		if previous != nil && len(recordTokens(previous)) > 0 {
			cleared, ok := p.clear("pane", asString(get(previous, "session")), asString(get(previous, "id")), recordTokens(previous))
			for _, k := range cleared.Keys() {
				row.Set(k, get(cleared, k))
			}
			row.Set("outcome", "cleared")
			if ok {
				p.meta.Set("root", nil)
			}
			return row
		}
		row.Set("outcome", "no-owner")
		return row
	}
	check := func() observation { return p.verifyOwner(owner) }
	row, record := p.endpoint("pane", asString(get(owner, "session")), asString(get(owner, "pane")), rootTokens(snapshot, active), previous, check)
	row.Set("role", "coordinator")
	if record == nil {
		p.meta.Set("root", nil)
	} else {
		p.meta.Set("root", record)
	}
	return row
}

// run is one pass: every task in scope is compared with its record and only differences are written; the coordinator
// summary is always recomputed from the full snapshot.
func (p *pass) run(scope []string, reason string) *ordjson.Object {
	snapshot := inboxview.Read(p.s)
	gapped := map[string]bool{}
	for _, gap := range snapshot.Gaps {
		gapped[gap.TaskID] = true
	}
	var inScope map[string]bool
	if scope != nil {
		inScope = map[string]bool{}
		for _, id := range scope {
			inScope[id] = true
		}
	}
	resources := asObject(get(p.meta, "resources"))
	if resources == nil {
		resources = ordjson.NewObject()
		p.meta.Set("resources", resources)
	}
	host, hostErr := p.s.Machine()
	rows := []any{}
	seen := map[string]bool{}
	active := 0
	for _, row := range snapshot.Tasks {
		seen[row.ID] = true
		archived := asString(get(row.Record, "status")) == "archived"
		if !archived {
			active++
		}
		if inScope != nil && !inScope[row.ID] {
			continue
		}
		if recorded := get(row.Record, "machine"); recorded != nil && (hostErr != nil || !host.Is(recorded)) {
			continue
		}
		rows = append(rows, p.projectTask(row, gapped[row.ID], archived, resources))
	}
	for _, taskID := range resources.Keys() {
		if seen[taskID] || gapped[taskID] {
			continue
		}
		// A task directory removed by hand: release what sum wrote, never anything else.
		gone := asObject(get(resources, taskID))
		out := ordjson.NewObject()
		out.Set("task", taskID)
		out.Set("state", nil)
		out.Set("previous", get(gone, "state"))
		endpoints := []any{}
		for _, kind := range []string{"pane", "workspace"} {
			if old := asObject(get(gone, kind)); old != nil {
				endpoints = append(endpoints, p.release(kind, old))
			}
		}
		out.Set("endpoints", endpoints)
		out.Set("released", true)
		resources.Delete(taskID)
		rows = append(rows, out)
	}
	root := p.projectRoot(snapshot, active)
	p.bump("passes", 1)
	last := ordjson.NewObject()
	last.Set("at", store.Now())
	last.Set("reason", reason)
	last.Set("tasks", jsonInt(len(rows)))
	last.Set("written", jsonInt(p.written))
	last.Set("herdr_calls", jsonInt(p.calls))
	p.meta.Set("last_pass", last)
	ambiguous := p.ambiguous
	if ambiguous == nil {
		ambiguous = []any{}
	}
	p.meta.Set("ambiguous", ambiguous)

	result := ordjson.NewObject()
	result.Set("enabled", true)
	result.Set("tasks", rows)
	result.Set("root", root)
	result.Set("forgotten", []any{})
	result.Set("ambiguous", ambiguous)
	result.Set("herdr_calls", jsonInt(p.calls))
	result.Set("degraded", get(p.meta, "degraded"))
	result.Set("note", note+" Herdr keeps tokens in memory: after a Herdr restart the sidebar is empty while records say written; `metadata sync --force` rewrites every recorded endpoint.")
	return result
}

func newPass(s *store.Store, meta *ordjson.Object, herdrPath string, force bool) *pass {
	return &pass{s: s, meta: meta, herdrPath: herdrPath, source: asString(get(meta, "source")), force: force}
}

func degradedResult(enabled bool, reason string) *ordjson.Object {
	result := ordjson.NewObject()
	result.Set("enabled", enabled)
	result.Set("degraded", true)
	result.Set("reason", truncate(reason, 300))
	return result
}

// After is the post-commit projection: after a successful domain write or native event, project the tasks it touched
// (nil: every task) and the coordinator summary. It never fails the caller and needs no coordinator authority: it
// writes display tokens only to endpoints this installation already recorded, in the recorded coordinator's session.
// A disabled projection returns without reading Herdr or writing anything.
func After(s *store.Store, runtimeRoot string, tasks []string, reason string) (result *ordjson.Object) {
	defer func() {
		if r := recover(); r != nil {
			result = degradedResult(true, fmt.Sprintf("projection panicked: %v", r))
		}
	}()
	meta, err := ReadMetadata(s)
	if err != nil {
		return degradedResult(false, "metadata state unreadable: "+err.Error())
	}
	if !truthy(get(meta, "enabled")) {
		result := ordjson.NewObject()
		result.Set("enabled", false)
		result.Set("skipped", true)
		return result
	}
	unlock, err := lock(s)
	if err != nil {
		return degradedResult(true, err.Error())
	}
	defer unlock()
	meta, err = ReadMetadata(s)
	if err != nil {
		return degradedResult(false, "metadata state unreadable: "+err.Error())
	}
	if !truthy(get(meta, "enabled")) {
		result := ordjson.NewObject()
		result.Set("enabled", false)
		result.Set("skipped", true)
		return result
	}
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	p := newPass(s, meta, herdrPath, false)
	if err != nil {
		p.degrade("projection pass failed: " + err.Error())
		p.recordError("sync", err.Error(), "", "")
		_ = writeMetadata(s, meta)
		return degradedResult(true, err.Error())
	}
	result = p.run(tasks, reason)
	if writeErr := writeMetadata(s, meta); writeErr != nil {
		return degradedResult(true, "metadata state not saved: "+writeErr.Error())
	}
	return result
}

func Enable(s *store.Store, ctx *ordjson.Object, runtimeRoot string, notify bool) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	source, err := Source(s)
	if err != nil {
		return nil, err
	}
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		return nil, err
	}
	session := asString(get(ctx, "session"))
	caps := probeCapabilities(herdrPath, session)
	if !supportsTokens(caps) {
		reason := asString(get(caps, "error"))
		if reason == "" {
			reason = "the schema lacks PaneReportMetadataParams.tokens and WorkspaceReportMetadataParams.tokens"
		}
		return nil, fmt.Errorf("The installed Herdr does not expose report-metadata tokens (%s); nothing was enabled or written.", reason)
	}
	unlock, err := lock(s)
	if err != nil {
		return nil, err
	}
	defer unlock()
	meta, err := ReadMetadata(s)
	if err != nil {
		return nil, err
	}
	from := ordjson.NewObject()
	from.Set("session", session)
	from.Set("pane", get(ctx, "pane"))
	meta.Set("enabled", true)
	meta.Set("notify", notify)
	meta.Set("source", source)
	meta.Set("capabilities", caps)
	meta.Set("degraded", nil)
	meta.Set("enabled_at", store.Now())
	meta.Set("enabled_from", from)
	if err := writeMetadata(s, meta); err != nil {
		return nil, err
	}
	p := newPass(s, meta, herdrPath, false)
	p.calls++ // the probe above
	sync := p.run(nil, "metadata enabled; projecting saved task state")
	if err := writeMetadata(s, meta); err != nil {
		return nil, err
	}
	tokens := ordjson.NewObject()
	tokens.Set("task", toAny(TaskTokens))
	tokens.Set("coordinator", toAny(RootTokens))
	result := ordjson.NewObject()
	result.Set("enabled", true)
	result.Set("notify", notify)
	result.Set("source", source)
	result.Set("capabilities", caps)
	result.Set("tokens", tokens)
	result.Set("sync", sync)
	result.Set("note", note+" `--notify` records a preference only; transition notification delivery is not implemented.")
	return result, nil
}

// Sync is an explicit full pass from the coordinator pane: re-probe capabilities, then compare every task. force
// treats every recorded token as unknown, so each recorded endpoint is written again (after a Herdr restart).
func Sync(s *store.Store, ctx *ordjson.Object, runtimeRoot string, force bool) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	meta, err := ReadMetadata(s)
	if err != nil {
		return nil, err
	}
	if !truthy(get(meta, "enabled")) {
		result := ordjson.NewObject()
		result.Set("enabled", false)
		result.Set("skipped", true)
		result.Set("reason", "native metadata projection is not enabled; `metadata enable` starts it")
		return result, nil
	}
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		return nil, err
	}
	unlock, err := lock(s)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if meta, err = ReadMetadata(s); err != nil {
		return nil, err
	}
	caps := probeCapabilities(herdrPath, asString(get(ctx, "session")))
	meta.Set("capabilities", caps)
	p := newPass(s, meta, herdrPath, force)
	p.calls++ // the probe above
	result := p.run(nil, "metadata sync")
	result.Set("capabilities", caps)
	result.Set("forced", force)
	if err := writeMetadata(s, meta); err != nil {
		return nil, err
	}
	return result, nil
}

// Disable clears exactly the keys sum recorded on each endpoint, then stops projecting. A refused or failed clear
// stays in the record and in the result, so a later disable or sync can try again.
func Disable(s *store.Store, ctx *ordjson.Object, runtimeRoot string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	unlock, err := lock(s)
	if err != nil {
		return nil, err
	}
	defer unlock()
	meta, err := ReadMetadata(s)
	if err != nil {
		return nil, err
	}
	herdrPath, findErr := toolpath.Find(runtimeRoot, "herdr")
	p := newPass(s, meta, herdrPath, false)
	cleared := []any{}
	clearOne := func(kind string, rec *ordjson.Object) (*ordjson.Object, bool) {
		if findErr != nil {
			row := ordjson.NewObject()
			row.Set("cleared", []any{})
			row.Set("failed", findErr.Error())
			p.recordError("clear", findErr.Error(), kind, asString(get(rec, "id")))
			return row, false
		}
		return p.clear(kind, asString(get(rec, "session")), asString(get(rec, "id")), recordTokens(rec))
	}
	resources := asObject(get(meta, "resources"))
	if resources == nil {
		resources = ordjson.NewObject()
	}
	for _, taskID := range resources.Keys() {
		rec := asObject(get(resources, taskID))
		for _, kind := range []string{"pane", "workspace"} {
			endpoint := asObject(get(rec, kind))
			if endpoint == nil {
				continue
			}
			row, ok := clearOne(kind, endpoint)
			row.Set("task", taskID)
			row.Set("kind", kind)
			row.Set("id", get(endpoint, "id"))
			cleared = append(cleared, row)
			if ok {
				rec.Delete(kind)
			}
		}
		if _, hasPane := rec.Get("pane"); !hasPane {
			if _, hasWorkspace := rec.Get("workspace"); !hasWorkspace {
				resources.Delete(taskID)
			}
		}
	}
	meta.Set("resources", resources)
	if root := asObject(get(meta, "root")); root != nil {
		row, ok := clearOne("pane", root)
		row.Set("role", "coordinator")
		row.Set("kind", "pane")
		row.Set("id", get(root, "id"))
		cleared = append(cleared, row)
		if ok {
			meta.Set("root", nil)
		}
	}
	meta.Set("enabled", false)
	meta.Set("disabled_at", store.Now())
	if err := writeMetadata(s, meta); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("enabled", false)
	result.Set("cleared", cleared)
	result.Set("herdr_calls", jsonInt(p.calls))
	result.Set("note", "Native metadata projection is off and sum's recorded keys were cleared where the endpoint still exists; a `refused` or `failed` row keeps its record for a later attempt. The user's labels, rows, theme, and every other reporter's tokens were never touched.")
	return result, nil
}
