package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/inboxview"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

const (
	Dir    = "metadata"
	File   = "state.json"
	Schema = 2

	// MaxErrors bounds the recorded projection error log.
	MaxErrors = 20

	callTimeout  = 10 * time.Second
	probeTimeout = 15 * time.Second
	tokenMax     = 80

	// lockPoll is how often a busy projection lock is retried within lockWait.
	lockPoll = 50 * time.Millisecond
)

// lockWait bounds how long a pass waits for the projection lock: a task-writing command must not stall behind a
// slow neighbour, so a busy lock skips the pass instead. Tests shorten it.
var lockWait = 2 * time.Second

// passBudget bounds every Herdr call of one pass together; once spent, the remaining tasks are reported as skipped
// rather than projected. Tests shorten it.
var passBudget = returns.DefaultPassBudget

// errLockBusy is the bounded lock wait expiring; After turns it into a degraded, state-free result.
var errLockBusy = errors.New("projection lock busy; skipped")

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

var TaskTokens = []string{"sum_state", "sum_pipeline", "sum_task", "sum_repo", "sum_rev", "sum_pr"}

var RootTokens = []string{"sum_inbox", "sum_tasks"}

var SumStates = []string{
	"needs-attention", "needs-decision", "merged-cleanup-pending", "review-ready", "attention-blocked", "attention-exited",
	"attention-closed", "attention-idle", "instruction-refresh-pending", "answer-pending", "pr-open", "verified", "preparing", "running",
}

const note = "Display-only projection. HC's native lifecycle is unchanged; namespaced labels are written only to endpoints this installation recorded and verified, and only when a value changed."

func snippetTOML() string {
	return "# Sum projects saved task state as namespaced sum.* HC terminal labels.\n# Read the task ledger with sumctl inbox --grouped.\n"
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
// lock during Herdr I/O. It never blocks past lockWait: a held lock returns errLockBusy.
func lock(s *store.Store) (func(), error) {
	if err := os.MkdirAll(filepath.Join(s.Home, Dir), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.Home, Dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, errLockBusy
		}
		time.Sleep(lockPoll)
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
			row.Set("endpoint", get(rec, "endpoint"))
			row.Set("labels", get(rec, "tokens"))
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

// Native labels project saved facts. They grant no task authority and supply no
// receipt. Each mutation revalidates the current binding and public process.
type pass struct {
	s     *store.Store
	meta  *ordjson.Object
	force bool
}

func newPass(s *store.Store, meta *ordjson.Object, runtimeRoot string, force bool) *pass {
	return &pass{s: s, meta: meta, force: force}
}
func recordTokens(record *ordjson.Object) map[string]string {
	out := map[string]string{}
	if tokens := asObject(get(record, "tokens")); tokens != nil {
		for _, key := range tokens.Keys() {
			out[key] = asString(get(tokens, key))
		}
	}
	return out
}
func (p *pass) project(ctx context.Context, endpoint store.Endpoint, tokens map[string]string, prior *ordjson.Object) *ordjson.Object {
	row := ordjson.NewObject()
	row.Set("endpoint", endpoint.Object())
	fail := func(err error) *ordjson.Object {
		row.Set("outcome", "failed")
		row.Set("reason", err.Error())
		return row
	}
	if endpoint.Validate() != nil {
		return fail(fmt.Errorf("no current native endpoint"))
	}
	host, err := p.s.Machine()
	if err != nil {
		return fail(err)
	}
	if !host.Is(endpoint.Machine) {
		return fail(fmt.Errorf("remote labels are unsupported"))
	}
	labels := map[string]string{}
	for key, value := range tokens {
		labels["sum."+strings.TrimPrefix(key, "sum_")] = value
	}
	if prior != nil && !store.EndpointFromContext(asObject(get(prior, "endpoint"))).Native().SameAuthority(endpoint.Native()) {
		if err := p.clearNative(ctx, prior); err != nil {
			return fail(err)
		}
	}
	// A saved identical projection performs no runtime calls.
	if !p.force && prior != nil && store.EndpointFromContext(asObject(get(prior, "endpoint"))).Native().SameAuthority(endpoint.Native()) && reflect.DeepEqual(recordTokens(prior), labels) {
		row.Set("outcome", "unchanged")
		return row
	}
	release, err := returns.LockRecipient(p.s, ctx, endpoint.Object())
	if err != nil {
		return fail(err)
	}
	defer release()
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID, Timeout: callTimeout}
	terminal, err := client.Get(ctx, endpoint.Address)
	if err != nil {
		return fail(err)
	}
	// Native observation for projection may be busy; input eligibility is irrelevant.
	recipient := &returns.NativeRecipient{Endpoint: endpoint, Terminal: terminal}
	unlock, err := p.s.Lock()
	if err != nil {
		return fail(err)
	}
	err = recipient.Check(p.s)
	unlock()
	if err != nil {
		return fail(err)
	}
	sets := map[string]string{}
	for key, value := range labels {
		if terminal.Labels[key] != value {
			sets[key] = value
		}
	}
	if prior != nil {
		for key := range recordTokens(prior) {
			if _, ok := labels[key]; !ok {
				sets[key] = ""
			}
		}
	}
	if len(sets) > 0 {
		if _, err = client.Label(ctx, endpoint.Address, sets); err != nil {
			return fail(err)
		}
	}
	record := ordjson.NewObject()
	record.Set("endpoint", endpoint.Object())
	binding, readErr := p.s.Registration(endpoint)
	if endpoint.Role == "coordinator" {
		binding, readErr = p.s.Owner()
	}
	if readErr != nil {
		return fail(readErr)
	}
	record.Set("incarnation", get(binding, "incarnation"))
	record.Set("native_receipt", get(binding, "native_receipt"))

	record.Set("tokens", func() any { o, _ := ordjson.FromValue(labels); return o }())
	row.Set("record", record)
	row.Set("outcome", "written")
	return row
}
func (p *pass) run(scope []string, reason string) *ordjson.Object {
	ctx, cancel := context.WithTimeout(context.Background(), passBudget)
	defer cancel()
	snapshot := inboxview.Read(p.s)
	tasks := make([]*ordjson.Object, 0, len(snapshot.Tasks))
	for _, row := range snapshot.Tasks {
		tasks = append(tasks, row.Record)
	}
	resources := asObject(get(p.meta, "resources"))
	if resources == nil {
		resources = ordjson.NewObject()
		p.meta.Set("resources", resources)
	}
	selected := map[string]bool{}
	for _, id := range scope {
		selected[id] = true
	}
	rows := []any{}
	active := 0
	for _, task := range tasks {
		id := asString(get(task, "id"))
		if get(task, "status") != "archived" {
			active++
		}
		if scope != nil && !selected[id] {
			continue
		}
		if ctx.Err() != nil {
			row := ordjson.NewObject()
			row.Set("task", id)
			row.Set("outcome", "skipped-budget")
			rows = append(rows, row)
			continue
		}
		taskRow := inboxview.Task{ID: id, Record: task}
		for _, candidate := range snapshot.Tasks {
			if candidate.ID == id {
				taskRow = candidate
				break
			}
		}
		gapped := false
		for _, gap := range snapshot.Gaps {
			if gap.TaskID == id {
				gapped = true
			}
		}
		state := inboxview.TaskState(taskRow, gapped)
		tokens := p.taskTokens(task, state)
		endpoint := store.EndpointFromContext(returns.ReturnRoute(task, "worker"))
		row := p.project(ctx, endpoint, tokens, asObject(get(resources, id)))
		row.Set("task", id)
		rows = append(rows, row)
		if record := asObject(get(row, "record")); record != nil {
			resources.Set(id, record)
		}
	}
	owner, err := p.s.Owner()
	var root *ordjson.Object
	if err == nil && owner != nil {
		root = p.project(ctx, store.EndpointFromContext(owner), rootTokens(snapshot, active), asObject(get(p.meta, "root")))
		if rec := asObject(get(root, "record")); rec != nil {
			p.meta.Set("root", rec)
		}
	}
	result := ordjson.NewObject()
	result.Set("tasks", rows)
	result.Set("root", root)
	result.Set("note", note)
	p.meta.Set("last_pass", store.Now())
	return result
}
func degradedResult(enabled bool, reason string) *ordjson.Object {
	row := ordjson.NewObject()
	row.Set("enabled", enabled)
	row.Set("degraded", true)
	row.Set("reason", reason)
	return row
}
func After(s *store.Store, runtimeRoot string, tasks []string, reason string) *ordjson.Object {
	_ = returns.MarkDirty(s)
	meta, err := ReadMetadata(s)
	if err != nil {
		return degradedResult(false, err.Error())
	}
	if !truthy(get(meta, "enabled")) {
		row := ordjson.NewObject()
		row.Set("enabled", false)
		return row
	}
	release, err := lock(s)
	if err != nil {
		return degradedResult(true, err.Error())
	}
	defer release()
	meta, err = ReadMetadata(s)
	if err != nil {
		return degradedResult(true, err.Error())
	}
	result := newPass(s, meta, runtimeRoot, false).run(tasks, reason)
	if err = writeMetadata(s, meta); err != nil {
		return degradedResult(true, err.Error())
	}
	return result
}
func Enable(s *store.Store, ctx *ordjson.Object, runtimeRoot string, notify bool) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	release, err := lock(s)
	if err != nil {
		return nil, err
	}
	defer release()
	meta, err := ReadMetadata(s)
	if err != nil {
		return nil, err
	}
	meta.Set("enabled", true)
	meta.Set("notify", notify)
	source, err := Source(s)
	if err != nil {
		return nil, err
	}
	meta.Set("source", source)
	caps := ordjson.NewObject()
	caps.Set("native_labels", true)
	meta.Set("capabilities", caps)
	result := newPass(s, meta, runtimeRoot, false).run(nil, "enabled")
	return result, writeMetadata(s, meta)
}
func Sync(s *store.Store, ctx *ordjson.Object, runtimeRoot string, force bool) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	release, err := lock(s)
	if err != nil {
		return nil, err
	}
	defer release()
	meta, err := ReadMetadata(s)
	if err != nil {
		return nil, err
	}
	if !truthy(get(meta, "enabled")) {
		return nil, fmt.Errorf("metadata projection is disabled")
	}
	result := newPass(s, meta, runtimeRoot, force).run(nil, "sync")
	return result, writeMetadata(s, meta)
}
func Disable(s *store.Store, caller *ordjson.Object, runtimeRoot string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, caller); err != nil {
		return nil, err
	}
	release, err := lock(s)
	if err != nil {
		return nil, err
	}
	defer release()
	meta, err := ReadMetadata(s)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), passBudget)
	defer cancel()
	p := newPass(s, meta, runtimeRoot, false)
	resources := asObject(get(meta, "resources"))
	failures := []any{}
	if resources != nil {
		for _, id := range resources.Keys() {
			prior := asObject(get(resources, id))
			if err = p.clearNative(ctx, prior); err != nil {
				row := ordjson.NewObject()
				row.Set("task", id)
				row.Set("reason", err.Error())
				failures = append(failures, row)
			} else {
				resources.Delete(id)
			}
		}
	}
	if prior := asObject(get(meta, "root")); prior != nil {
		if err = p.clearNative(ctx, prior); err != nil {
			row := ordjson.NewObject()
			row.Set("role", "coordinator")
			row.Set("reason", err.Error())
			failures = append(failures, row)
		} else {
			meta.Set("root", nil)
		}
	}
	meta.Set("enabled", false)
	meta.Set("disabled_at", store.Now())
	meta.Set("pending_clears", failures)
	return meta, writeMetadata(s, meta)
}

func (p *pass) clearNative(ctx context.Context, prior *ordjson.Object) error {
	if prior == nil {
		return nil
	}
	endpoint := store.EndpointFromContext(asObject(get(prior, "endpoint")))
	if err := endpoint.Validate(); err != nil {
		return err
	}
	release, err := returns.LockRecipient(p.s, ctx, endpoint.Object())
	if err != nil {
		return err
	}
	defer release()
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID, Timeout: callTimeout}
	terminal, err := client.Get(ctx, endpoint.Address)
	if err != nil {
		return err
	}
	if terminal.State != "live" {
		return nil
	}
	var receipt incarnation.NativeReceipt
	raw, err := ordjson.MarshalCompact(get(prior, "native_receipt"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &receipt); err != nil {
		return err
	}
	verdict := incarnation.JudgeNative(get(prior, "incarnation"), endpoint.Native(), terminal, &receipt)
	if !verdict.Verified {
		return fmt.Errorf("old display endpoint is no longer the recorded process: %s", verdict.Reason)
	}
	clears := map[string]string{}
	for key, value := range recordTokens(prior) {
		if terminal.Labels[key] == value {
			clears[key] = ""
		}
	}
	if len(clears) == 0 {
		return nil
	}
	if err = p.s.CheckWritable(); err != nil {
		return err
	}
	_, err = client.Label(ctx, endpoint.Address, clears)
	return err
}
