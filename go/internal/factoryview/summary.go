// Package factoryview projects saved factory, task, pipeline, decision, PR and cleanup facts into a bounded
// digest. It reads a snapshot and writes nothing: no GitHub, no Herdr, no tick, claim, merge or cleanup.
package factoryview

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/cleanup"
	"github.com/douglasjarquin/sum/go/internal/inboxview"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pipeline"
	"github.com/douglasjarquin/sum/go/internal/presentation"
)

const (
	Schema       = 1
	DefaultLimit = 20
	MaxLimit     = 100

	IntakeUnknown = "unknown (not yet observed)"

	AttributionFactory = "factory"
	AttributionProject = "project (factory linkage not recorded)"

	Note = "A view of saved records: nothing was observed, ticked, claimed, merged or cleaned up. next_tick_at is the time the last idle tick recorded, not a scheduled wake. pr/ci times are saved observations, not freshness. Counts precede project focus and paging; a resync means the cursor could not be honoured and nothing here is labelled new."
)

// Options selects the scope, the caller-held cursor and the page bound of one read. FactoryOnly limits the
// rows and coverage to projects with a factory registry entry (the `factory status` scope); the grouped views
// cover every recorded project. A cursor is bound to its scope and resyncs on the other.
type Options struct {
	Project     string
	Since       string
	Limit       int
	FactoryOnly bool
}

const (
	ScopeAll     = "all"
	ScopeFactory = "factory"
)

func (o Options) scope() string {
	if o.FactoryOnly {
		return ScopeFactory
	}
	return ScopeAll
}

// Digest is one bounded response. Counts, Complete, Gaps and NeedsYou come from the full readable snapshot
// before any project focus or paging, so a focused page never hides another project's decision.
type Digest struct {
	Schema       int              `json:"schema"`
	Installation string           `json:"installation"`
	Scope        string           `json:"scope"`
	Project      string           `json:"project,omitempty"`
	Counts       inboxview.Counts `json:"counts"`
	Complete     bool             `json:"complete"`
	Gaps         []inboxview.Gap  `json:"gaps"`
	NeedsYou     []inboxview.Row  `json:"needs_you"`
	Rows         []FactoryRow     `json:"rows"`
	Since        bool             `json:"since"`
	Deltas       []Delta          `json:"deltas"`
	Page         Page             `json:"page"`
	Resync       *Resync          `json:"resync"`
	Cursor       string           `json:"cursor"`
	Note         string           `json:"note"`
}

// FactoryRow is one project's current lane, stage, owner, blocker and rendered outcomes.
type FactoryRow struct {
	Project      string          `json:"project"`
	Lane         string          `json:"lane"`
	CurrentIssue string          `json:"current_issue"`
	CurrentTask  string          `json:"current_task"`
	Stage        string          `json:"stage"`
	ActionOwner  string          `json:"action_owner"`
	Blocker      *Blocker        `json:"blocker"`
	Outcomes     []Outcome       `json:"outcomes"`
	Observed     Observed        `json:"observed"`
	Next         Next            `json:"next"`
	Gaps         []inboxview.Gap `json:"gaps"`
	Detail       []string        `json:"detail"`
}

// Blocker is a saved decision, attention or failed/blocked gate record; never a diagnosis.
type Blocker struct {
	Kind   string   `json:"kind"`
	Ref    string   `json:"ref"`
	Detail []string `json:"detail"`
}

type Observed struct {
	LastTickAt   string `json:"last_tick_at"`
	NextTickAt   string `json:"next_tick_at"`
	PRObservedAt string `json:"pr_observed_at"`
	CIObservedAt string `json:"ci_observed_at"`
	CIStale      bool   `json:"ci_stale"`
	Note         string `json:"note"`
}

type Next struct {
	Intake  string   `json:"intake"`
	Command []string `json:"command"`
}

// Outcome is one evidence-backed state with its source, candidate and time. Identity is stable across reads.
type Outcome struct {
	Identity    string   `json:"identity"`
	Project     string   `json:"project"`
	Kind        string   `json:"kind"`
	Attribution string   `json:"attribution"`
	New         bool     `json:"new"`
	Source      Source   `json:"source"`
	Detail      []string `json:"detail"`
}

type Source struct {
	Task      string `json:"task"`
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Candidate string `json:"candidate,omitempty"`
	At        string `json:"at"`
}

type Delta struct {
	Task   string `json:"task"`
	Reason string `json:"reason"`
}

type Resync struct {
	Reason string `json:"reason"`
}

const observedNote = "next_tick_at is recorded by the last idle tick, not scheduled; pr/ci times are saved observations"

// Build is pure: it joins the snapshot's saved facts and never reads or writes a source.
func Build(snapshot inboxview.Snapshot, installation string, opts Options) Digest {
	return BuildFromOverview(snapshot, inboxview.BuildOverview(snapshot), installation, opts)
}

// BuildFromOverview is Build with the snapshot's unfocused overview already built (inboxview.BuildOverview),
// so a grouped read derives each task's pipeline once and builds the overview once.
func BuildFromOverview(snapshot inboxview.Snapshot, overview inboxview.Overview, installation string, opts Options) Digest {
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	derived := derivedOf(snapshot, overview)
	out := Digest{Schema: Schema, Installation: installation, Scope: opts.scope(), Project: opts.Project, Counts: snapshot.Counts, Complete: snapshot.Complete, Gaps: []inboxview.Gap{}, NeedsYou: overview.NeedsYou, Rows: []FactoryRow{}, Deltas: []Delta{}, Page: Page{Limit: limit}, Note: Note}
	tasks := map[string]inboxview.Task{}
	for _, task := range snapshot.Tasks {
		tasks[task.ID] = task
	}
	registry := registryOf(snapshot.Factory)
	laneTasks := map[string]string{}
	for name, rec := range registry {
		for _, lane := range rec.lanes {
			if lane.task != "" {
				laneTasks[lane.task] = name
			}
		}
	}
	rows := map[string]*FactoryRow{}
	order := []string{}
	row := func(key string) *FactoryRow {
		if r, ok := rows[key]; ok {
			return r
		}
		r := &FactoryRow{Project: key, Lane: "none", Outcomes: []Outcome{}, Gaps: []inboxview.Gap{}, Detail: []string{"status", "--grouped", "--project", key}, Observed: Observed{Note: observedNote}, Next: Next{Intake: IntakeUnknown, Command: []string{"status", "--grouped", "--project", key}}}
		rows[key] = r
		order = append(order, key)
		return r
	}
	for _, group := range overview.Groups {
		r := row(group.Key)
		r.Gaps = append(r.Gaps, group.Gaps...)
	}
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rec := registry[name]
		r := row(name)
		r.Detail = []string{"factory", "status", "--project", name}
		r.Lane = "disabled"
		if rec.enabled {
			r.Lane = "enabled"
		}
		r.Observed.LastTickAt, r.Observed.NextTickAt = rec.lastTick, rec.nextTick
		r.Next.Command = []string{"factory", "status", "--project", name}
		if rec.enabled {
			r.Next.Command = []string{"factory", "tick", "--project", name}
		}
		if len(rec.lanes) > 0 {
			lane := rec.lanes[0]
			r.Lane = "held"
			r.CurrentIssue, r.CurrentTask = lane.issue, lane.task
			if lane.task != "" {
				r.Next.Command = []string{"show", lane.task}
			}
		}
	}
	// Global gaps stay global unless a lane names the unreadable task; then the factory row owns it.
	for _, gap := range snapshot.Gaps {
		if name, held := laneTasks[gap.TaskID]; held && gap.TaskID != "" && tasks[gap.TaskID].Record == nil {
			row(name).Gaps = append(row(name).Gaps, gap)
			continue
		}
		if _, known := tasks[gap.TaskID]; gap.TaskID == "" || !known {
			out.Gaps = append(out.Gaps, gap)
		}
	}
	taskRows := map[string]inboxview.TaskRow{}
	for _, group := range overview.Groups {
		for _, task := range group.Tasks {
			taskRows[task.ID] = task
		}
	}
	for _, r := range rows {
		if r.CurrentTask == "" {
			continue
		}
		task, ok := tasks[r.CurrentTask]
		if !ok {
			continue
		}
		r.Stage = inboxview.StageOf(derived[task.ID])
		r.ActionOwner, r.Blocker = ownerAndBlocker(taskRows[task.ID])
		r.Next.Command = []string{"context", task.ID, "--role", "coordinator"}
		r.Observed.PRObservedAt = str(obj(field(task.Record, "pr")), "observed_at")
		r.Observed.CIObservedAt, r.Observed.CIStale = ciObservation(task.Record)
	}
	inScope := func(project string) bool {
		if opts.Project != "" && project != opts.Project {
			return false
		}
		if _, enrolled := registry[project]; opts.FactoryOnly && !enrolled {
			return false
		}
		return true
	}
	all := outcomes(snapshot, laneTasks, derived)
	scoped := all[:0]
	for _, o := range all {
		if inScope(o.Project) {
			scoped = append(scoped, o)
		}
	}
	all = scoped
	leaves := []Leaf{}
	for _, task := range snapshot.Tasks {
		if inScope(task.ProjectID) {
			leaves = append(leaves, leafOf(task))
		}
	}
	sortLeaves(leaves)
	rendered := page(&out, all, leaves, snapshot.Gaps, installation, opts, limit)
	for _, o := range rendered {
		if r, ok := rows[o.Project]; ok {
			r.Outcomes = append(r.Outcomes, o)
		}
	}
	for _, key := range order {
		if key == "" || !inScope(key) {
			continue
		}
		out.Rows = append(out.Rows, *rows[key])
	}
	return out
}

// derivedOf is the overview's per-task pipeline records, derived here only for tasks the overview did not cover.
func derivedOf(snapshot inboxview.Snapshot, overview inboxview.Overview) map[string]pipeline.Record {
	derived := make(map[string]pipeline.Record, len(snapshot.Tasks))
	for _, task := range snapshot.Tasks {
		if record, ok := overview.Derived[task.ID]; ok {
			derived[task.ID] = record
			continue
		}
		derived[task.ID] = pipeline.Derive(task.Record)
	}
	return derived
}

// Attach places each project's row on its overview group so the grouped views carry the same digest facts.
func Attach(overview *inboxview.Overview, digest Digest) {
	for i := range overview.Groups {
		for _, r := range digest.Rows {
			if r.Project == overview.Groups[i].Key {
				row := r
				overview.Groups[i].Digest = &row
			}
		}
	}
}

func ownerAndBlocker(task inboxview.TaskRow) (string, *Blocker) {
	owner := "none"
	var blocker *Blocker
	for _, item := range task.Items {
		if item.Kind == presentation.Decision {
			if blocker == nil {
				blocker = &Blocker{Kind: string(item.Source.Kind), Ref: item.Source.ID, Detail: item.Detail}
			}
			owner = "human decision"
		}
	}
	if blocker == nil {
		for _, item := range task.Items {
			if item.Source.Kind == presentation.Pipeline && (item.State == string(pipeline.Blocked) || item.State == string(pipeline.Fail)) {
				blocker = &Blocker{Kind: "gate", Ref: item.Source.ID, Detail: item.Detail}
				break
			}
		}
	}
	if owner != "none" {
		return owner, blocker
	}
	for _, item := range task.Items {
		if item.Owner == presentation.Coordinator {
			return "coordinator", blocker
		}
	}
	for _, item := range task.Items {
		if item.Owner == presentation.Worker {
			return "worker", blocker
		}
	}
	return owner, blocker
}

// ciObservation is the latest saved CI record's time; stale when any record on the task was written after it.
func ciObservation(record *ordjson.Object) (string, bool) {
	at := ""
	for _, raw := range list(record, "evidence") {
		rec := obj(raw)
		if str(rec, "kind") == "ci" && str(rec, "source") == "coordinator" {
			at = str(rec, "at")
		}
	}
	if at == "" {
		return "", false
	}
	return at, at < latestWrite(record)
}

func latestWrite(record *ordjson.Object) string {
	latest := ""
	bump := func(at string) {
		if at > latest {
			latest = at
		}
	}
	for _, raw := range list(record, "evidence") {
		bump(str(obj(raw), "at"))
	}
	for _, raw := range list(record, "questions") {
		bump(str(obj(raw), "created_at"))
	}
	bump(str(obj(field(record, "pr")), "observed_at"))
	bump(str(obj(field(record, "report")), "submitted_at"))
	bump(str(obj(field(record, "cleanup")), "at"))
	return latest
}

type laneRecord struct{ issue, task string }

type projectRecord struct {
	enabled            bool
	lanes              []laneRecord
	lastTick, nextTick string
}

func registryOf(record *ordjson.Object) map[string]projectRecord {
	out := map[string]projectRecord{}
	projects := obj(field(record, "projects"))
	if projects == nil {
		return out
	}
	for _, name := range projects.Keys() {
		rec := obj(field(projects, name))
		if rec == nil {
			continue
		}
		enabled, _ := field(rec, "enabled").(bool)
		p := projectRecord{enabled: enabled, lastTick: str(rec, "last_tick_at"), nextTick: str(rec, "next_tick_at")}
		for _, raw := range list(rec, "lanes_held") {
			lane := obj(raw)
			if lane == nil {
				continue
			}
			issue := ""
			if v := field(lane, "issue"); v != nil {
				issue = fmt.Sprint(v)
			}
			p.lanes = append(p.lanes, laneRecord{issue: issue, task: str(lane, "task")})
		}
		out[name] = p
	}
	return out
}

// outcomes derives every outcome from readable tasks. PR-based outcomes are keyed by canonical project and PR
// number so one PR observed from two tasks or under two URL spellings counts once, while the same number on
// another project stays distinct. Attribution is factory only when a retained lane record (or a saved
// task.factory field) names the task; a removed lane leaves a project outcome.
func outcomes(snapshot inboxview.Snapshot, laneTasks map[string]string, derived map[string]pipeline.Record) []Outcome {
	var out []Outcome
	byPR := map[string]int{}
	for _, task := range snapshot.Tasks {
		if task.ProjectID == "" || task.Record == nil {
			continue
		}
		record := task.Record
		attribution := AttributionProject
		if _, held := laneTasks[task.ID]; held || obj(field(record, "factory")) != nil {
			attribution = AttributionFactory
		}
		context := []string{"context", task.ID, "--role", "coordinator"}
		show := []string{"show", task.ID}
		add := func(kind string, key string, source Source, detail []string) {
			out = append(out, Outcome{Identity: kind + ":" + key, Project: task.ProjectID, Kind: kind, Attribution: attribution, Source: source, Detail: detail})
		}
		pipe := derived[task.ID]
		candidate := pipe.Candidate
		if report := reportSource(task.ID, record); report != nil {
			add("reported", task.ID+":"+report.Candidate, *report, context)
		}
		if row := pipe.Rows[pipeline.Index(pipeline.StageTest)]; row.Status == pipeline.Pass {
			add("verified", task.ID+":"+candidate, Source{Task: task.ID, Kind: "verification", ID: first(row.Evidence), Candidate: candidate, At: row.At}, context)
		}
		if row := pipe.Rows[pipeline.Index(pipeline.StageReview)]; row.Status == pipeline.Pass {
			add("review-accepted", task.ID+":"+candidate, Source{Task: task.ID, Kind: "review", ID: last(row.Evidence), Candidate: candidate, At: row.At}, context)
		}
		pr := obj(field(record, "pr"))
		identity := obj(field(pr, "identity"))
		number := prNumber(identity)
		merged := false
		if number != "" {
			key := task.ProjectID + "#" + number
			source := Source{Task: task.ID, Kind: "pr", ID: str(identity, "url"), Candidate: str(identity, "head_sha"), At: str(pr, "observed_at")}
			switch str(pr, "state") {
			case "open":
				addPR(&out, byPR, Outcome{Identity: "pr-open:" + key, Project: task.ProjectID, Kind: "pr-open", Attribution: attribution, Source: source, Detail: show})
			case "merged":
				if field(pr, "merge_commit") != nil {
					merged = true
					addPR(&out, byPR, Outcome{Identity: "observed-merged:" + key, Project: task.ProjectID, Kind: "observed-merged", Attribution: attribution, Source: source, Detail: show})
				}
			}
		}
		if pending := cleanup.Pending(record); pending != nil {
			add("cleanup-pending", task.ID, Source{Task: task.ID, Kind: "cleanup", ID: str(pending, "state"), At: fmt.Sprint(zero(field(pending, "at")))}, show)
		}
		if _, held := laneTasks[task.ID]; !held && (merged || str(record, "status") == "archived") {
			at := str(obj(field(record, "cleanup")), "at")
			if at == "" {
				at = str(pr, "observed_at")
			}
			add("lane-free", task.ID, Source{Task: task.ID, Kind: "registry", ID: "no lane names this task", At: at}, show)
		}
	}
	return out
}

// addPR keeps one outcome per identity, preferring the latest saved observation for its source reference.
func addPR(out *[]Outcome, byPR map[string]int, o Outcome) {
	if i, seen := byPR[o.Identity]; seen {
		if o.Source.At > (*out)[i].Source.At {
			(*out)[i] = o
		}
		return
	}
	byPR[o.Identity] = len(*out)
	*out = append(*out, o)
}

func reportSource(taskID string, record *ordjson.Object) *Source {
	var latest *ordjson.Object
	for _, raw := range list(record, "evidence") {
		if rec := obj(raw); str(rec, "kind") == "report" {
			latest = rec
		}
	}
	if latest != nil {
		return &Source{Task: taskID, Kind: "report", ID: str(latest, "id"), Candidate: str(latest, "candidate"), At: str(latest, "at")}
	}
	report := obj(field(record, "report"))
	if report == nil {
		return nil
	}
	return &Source{Task: taskID, Kind: "report", ID: "report", Candidate: str(report, "candidate"), At: str(report, "submitted_at")}
}

func prNumber(identity *ordjson.Object) string {
	if identity == nil {
		return ""
	}
	if v := field(identity, "number"); v != nil {
		if n, err := strconv.Atoi(fmt.Sprint(v)); err == nil && n > 0 {
			return strconv.Itoa(n)
		}
	}
	parts := strings.Split(strings.TrimRight(str(identity, "url"), "/"), "/")
	if n, err := strconv.Atoi(parts[len(parts)-1]); err == nil && n > 0 {
		return strconv.Itoa(n)
	}
	return ""
}

func first(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return items[0]
}

func last(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return items[len(items)-1]
}

func zero(v any) any {
	if v == nil {
		return ""
	}
	return v
}

func field(o *ordjson.Object, key string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	return v
}
func str(o *ordjson.Object, key string) string { v, _ := field(o, key).(string); return v }
func obj(v any) *ordjson.Object                { o, _ := v.(*ordjson.Object); return o }
func list(o *ordjson.Object, key string) []any { v, _ := field(o, key).([]any); return v }
