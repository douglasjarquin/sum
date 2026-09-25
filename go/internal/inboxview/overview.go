package inboxview

import (
	"sort"
	"strconv"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pipeline"
	"github.com/douglasjarquin/sum/go/internal/presentation"
)

// OverviewChars bounds prose in overview rows; the detail route reaches the full saved text.
const OverviewChars = 240

// Overview is a grouped, read-only view of one snapshot. Counts, Gaps and NeedsYou are global
// facts computed before any project focus, so a focused response never hides another project's work.
type Overview struct {
	Counts      Counts             `json:"counts"`
	Complete    bool               `json:"complete"`
	Project     string             `json:"project,omitempty"`
	Gaps        []Gap              `json:"gaps"`
	NeedsYou    []Row              `json:"needs_you"`
	Groups      []Group            `json:"groups"`
	Standalone  *Group             `json:"standalone"`
	Coordinator CoordinatorSummary `json:"coordinator"`
}

// CoordinatorSummary is the one global coordinator entry; there is never a coordinator per project.
type CoordinatorSummary struct {
	Items      int `json:"items"`
	Inspection int `json:"inspection"`
	Tasks      int `json:"tasks"`
}

type GroupCounts struct {
	Decisions   int `json:"decisions"`
	Inspection  int `json:"inspection"`
	Coordinator int `json:"coordinator"`
	Worker      int `json:"worker"`
	Resolved    int `json:"resolved"`
	Working     int `json:"working"`
	Tasks       int `json:"tasks"`
}

type Group struct {
	Key     string            `json:"key"`
	Counts  GroupCounts       `json:"counts"`
	Gaps    []Gap             `json:"gaps"`
	Tasks   []TaskRow         `json:"tasks"`
	Factory *FactoryOccupancy `json:"factory"`
	Healthy bool              `json:"healthy"`
}

type FactoryOccupancy struct {
	Lanes []Lane `json:"lanes"`
}

type Lane struct {
	Issue     string `json:"issue"`
	Task      string `json:"task"`
	State     string `json:"state"`
	ClaimedAt string `json:"claimed_at"`
}

// Observed is the saved native lifecycle observation, kept apart from Sum state and never a freshness claim.
type Observed struct {
	Status string `json:"status"`
	At     string `json:"at,omitempty"`
}

type TaskRow struct {
	ID         string    `json:"id"`
	Project    string    `json:"project"`
	State      string    `json:"state"`
	Stage      string    `json:"stage"`
	Status     string    `json:"status"`
	Repository string    `json:"repository,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	At         string    `json:"at,omitempty"`
	Observed   *Observed `json:"observed"`
	Decisions  int       `json:"decisions"`
	Items      []Row     `json:"items"`
	Detail     []string  `json:"detail"`
	Archived   bool      `json:"archived"`
}

type Text struct {
	Text      string `json:"text"`
	Chars     int    `json:"chars"`
	Truncated bool   `json:"truncated"`
}

// Row is one presentation item with its routes; identical shape in NeedsYou and TaskRow.Items.
type Row struct {
	Project   string              `json:"project"`
	Task      string              `json:"task"`
	Identity  string              `json:"identity"`
	Source    presentation.Source `json:"source"`
	Owner     presentation.Owner  `json:"owner"`
	Kind      presentation.Kind   `json:"presentation"`
	Reason    string              `json:"reason"`
	State     string              `json:"state,omitempty"`
	At        string              `json:"at,omitempty"`
	Delivery  string              `json:"delivery,omitempty"`
	Uncertain bool                `json:"uncertain"`
	Text      Text                `json:"text"`
	Detail    []string            `json:"detail"`
	Answer    []string            `json:"answer,omitempty"`
}

// Rank orders presentation kinds: decisions before inspection before routine.
func Rank(item presentation.Item) int {
	switch item.Kind {
	case presentation.Decision:
		return 0
	case presentation.Inspection:
		return 1
	case presentation.Routine:
		return 2
	default:
		return 3
	}
}

// TaskState is the one per-task Sum state label; metadata projection and the overview share it.
func TaskState(row Task, gapped bool) string {
	decision, inspection, review, answer, refresh := false, false, false, false, false
	for _, item := range row.Items {
		decision = decision || item.Kind == presentation.Decision
		inspection = inspection || item.Kind == presentation.Inspection
		review = review || (item.Owner == presentation.Coordinator && item.Kind == presentation.Routine)
		answer = answer || item.Reason == presentation.AnswerUnapplied
		refresh = refresh || item.Reason == presentation.RefreshUnapplied
	}
	switch {
	case decision:
		return "needs-decision"
	case inspection || gapped:
		return "needs-attention"
	case review:
		return "review-ready"
	case answer:
		return "answer-pending"
	case refresh:
		return "instruction-refresh-pending"
	}
	return "running"
}

// Stage is the one gate the task is waiting on, or "settled".
func Stage(task *ordjson.Object) string {
	if stage := pipeline.FirstUnsettled(pipeline.Derive(task)); stage != "" {
		return stage
	}
	return "settled"
}

// DetailRoute is the sumctl argument array that reaches the full saved record behind an item.
func DetailRoute(item presentation.Item, task Task) []string {
	id := item.Source.TaskID
	if item.Source.Kind == presentation.Factory {
		return []string{"factory", "status"}
	}
	if id == "" {
		return nil
	}
	if item.Source.Kind == presentation.Pipeline {
		return []string{"pipeline", "show", id}
	}
	if item.Source.Kind == presentation.Question && task.Record != nil {
		for i, raw := range list(task.Record, "questions") {
			if str(obj(raw), "id") == strings.TrimPrefix(item.Source.ID, "question:") {
				return []string{"context", id, "--section", "decisions", "--after", strconv.Itoa(i), "--limit", "1", "--max-chars", "0"}
			}
		}
	}
	return []string{"show", id}
}

// AnswerRoute is the answer command for a decision, without the user's text.
func AnswerRoute(item presentation.Item) []string {
	if item.Kind != presentation.Decision || item.Source.Kind != presentation.Question {
		return nil
	}
	return []string{"answer", item.Source.TaskID, strings.TrimPrefix(item.Source.ID, "question:"), "--text"}
}

func boundedText(text string) Text {
	if text == "" {
		return Text{}
	}
	view := environment.BoundedView(text, OverviewChars)
	out := Text{Text: str(view, "text")}
	out.Truncated, _ = field(view, "truncated").(bool)
	out.Chars = len([]rune(text))
	return out
}

func rowOf(item presentation.Item, task Task) Row {
	return Row{Project: item.ProjectID, Task: item.Source.TaskID, Identity: item.Identity, Source: item.Source, Owner: item.Owner, Kind: item.Kind, Reason: item.Reason, State: item.State, At: item.At, Delivery: item.Delivery, Uncertain: item.Uncertain, Text: boundedText(item.Text), Detail: DetailRoute(item, task), Answer: AnswerRoute(item)}
}

func sortItems(items []presentation.Item) {
	sort.SliceStable(items, func(i, j int) bool {
		if Rank(items[i]) != Rank(items[j]) {
			return Rank(items[i]) < Rank(items[j])
		}
		return items[i].Identity < items[j].Identity
	})
}

func (c *GroupCounts) add(item presentation.Item) {
	switch item.Kind {
	case presentation.Decision:
		c.Decisions++
	case presentation.Inspection:
		c.Inspection++
	case presentation.Resolved:
		c.Resolved++
	}
	if item.Owner == presentation.Worker {
		c.Worker++
	}
	if item.Owner == presentation.Coordinator {
		c.Coordinator++
	}
}

// BuildOverview groups a snapshot by canonical recorded project identity. It is pure: no I/O, no Herdr.
func BuildOverview(snapshot Snapshot) Overview {
	out := Overview{Counts: snapshot.Counts, Complete: snapshot.Complete, Gaps: []Gap{}, NeedsYou: []Row{}, Groups: []Group{}}
	tasks := map[string]Task{}
	for _, task := range snapshot.Tasks {
		tasks[task.ID] = task
	}
	groups := map[string]*Group{}
	group := func(key string) *Group {
		if g, ok := groups[key]; ok {
			return g
		}
		g := &Group{Key: key, Gaps: []Gap{}, Tasks: []TaskRow{}}
		groups[key] = g
		return g
	}
	gapped := map[string][]Gap{}
	for _, gap := range snapshot.Gaps {
		if _, known := tasks[gap.TaskID]; gap.TaskID != "" && known {
			gapped[gap.TaskID] = append(gapped[gap.TaskID], gap)
			continue
		}
		out.Gaps = append(out.Gaps, gap)
	}
	coordinatorTasks := map[string]bool{}
	for _, item := range snapshot.Items {
		if item.Owner == presentation.Coordinator {
			out.Coordinator.Items++
			if item.Source.TaskID != "" {
				coordinatorTasks[item.Source.TaskID] = true
			}
		}
		if item.Kind == presentation.Inspection && item.Owner == presentation.Coordinator {
			out.Coordinator.Inspection++
		}
		if item.Kind == presentation.Decision {
			task := tasks[item.Source.TaskID]
			out.NeedsYou = append(out.NeedsYou, rowOf(item, task))
		}
		task, known := tasks[item.Source.TaskID]
		switch {
		case known:
			if item.Source.Kind == presentation.Gap || item.Source.Kind == presentation.Factory {
				group(task.ProjectID).Counts.add(item)
			}
		case item.Source.Kind == presentation.Factory && item.ProjectID != "":
			group(item.ProjectID).Counts.add(item)
		}
	}
	out.Coordinator.Tasks = len(coordinatorTasks)
	sort.SliceStable(out.NeedsYou, func(i, j int) bool { return out.NeedsYou[i].Identity < out.NeedsYou[j].Identity })
	for _, task := range snapshot.Tasks {
		g := group(task.ProjectID)
		g.Gaps = append(g.Gaps, gapped[task.ID]...)
		row := taskRow(task, len(gapped[task.ID]) > 0)
		for _, item := range task.Items {
			g.Counts.add(item)
		}
		g.Counts.Tasks++
		if row.State == "running" && !row.Archived {
			g.Counts.Working++
		}
		g.Tasks = append(g.Tasks, row)
	}
	for name, lanes := range factoryLanes(snapshot.Factory) {
		group(name).Factory = &FactoryOccupancy{Lanes: lanes}
	}
	for _, g := range groups {
		sort.SliceStable(g.Tasks, func(i, j int) bool { return taskLess(g.Tasks[i], g.Tasks[j]) })
		g.Healthy = g.Counts.Decisions == 0 && g.Counts.Inspection == 0 && len(g.Gaps) == 0
		if g.Key == "" {
			out.Standalone = g
			continue
		}
		out.Groups = append(out.Groups, *g)
	}
	sort.SliceStable(out.Groups, func(i, j int) bool { return groupLess(out.Groups[i], out.Groups[j]) })
	return out
}

func taskRow(task Task, gapped bool) TaskRow {
	row := TaskRow{ID: task.ID, Project: task.ProjectID, State: TaskState(task, gapped), Stage: Stage(task.Record), Status: str(task.Record, "status"), Repository: str(task.Record, "repository"), Branch: str(task.Record, "branch"), Items: []Row{}, Detail: []string{"context", task.ID, "--role", "coordinator"}}
	row.Archived = row.Status == "archived"
	if observed := obj(field(obj(field(task.Record, "launch")), "observed")); observed != nil && str(observed, "status") != "" {
		row.Observed = &Observed{Status: str(observed, "status"), At: str(observed, "at")}
	}
	items := append([]presentation.Item(nil), task.Items...)
	sortItems(items)
	for _, item := range items {
		if item.Kind == presentation.Decision {
			row.Decisions++
		}
		if item.At > row.At {
			row.At = item.At
		}
		row.Items = append(row.Items, rowOf(item, task))
	}
	return row
}

func taskRank(row TaskRow) int {
	if row.Archived {
		return 4
	}
	if len(row.Items) == 0 {
		return 3
	}
	best := 3
	for _, item := range row.Items {
		r := Rank(presentation.Item{Kind: item.Kind})
		if r < best {
			best = r
		}
	}
	return best
}

func taskLess(a, b TaskRow) bool {
	if taskRank(a) != taskRank(b) {
		return taskRank(a) < taskRank(b)
	}
	return a.ID < b.ID
}

func groupLess(a, b Group) bool {
	ra, rb := groupRank(a), groupRank(b)
	if ra != rb {
		return ra < rb
	}
	return a.Key < b.Key
}

func groupRank(g Group) int {
	switch {
	case g.Counts.Decisions > 0:
		return 0
	case g.Counts.Inspection > 0 || len(g.Gaps) > 0:
		return 1
	}
	for _, task := range g.Tasks {
		if !task.Archived {
			return 2
		}
	}
	return 3
}

func factoryLanes(record *ordjson.Object) map[string][]Lane {
	out := map[string][]Lane{}
	projects := obj(field(record, "projects"))
	if projects == nil {
		return out
	}
	for _, name := range projects.Keys() {
		project := obj(field(projects, name))
		lanes := []Lane{}
		for _, raw := range list(project, "lanes_held") {
			lane := obj(raw)
			if lane == nil {
				continue
			}
			issue := ""
			if v := field(lane, "issue"); v != nil {
				issue = strings.TrimSpace(strings.Trim(mustJSON(v), `"`))
			}
			lanes = append(lanes, Lane{Issue: issue, Task: str(lane, "task"), State: str(lane, "state"), ClaimedAt: str(lane, "claimed_at")})
		}
		if project != nil {
			out[name] = lanes
		}
	}
	return out
}

func mustJSON(v any) string {
	raw, err := ordjson.MarshalCompact(v)
	if err != nil {
		return ""
	}
	return string(raw)
}

// Focus keeps every global fact and narrows the groups to one project key.
func Focus(overview Overview, project string) Overview {
	if project == "" {
		return overview
	}
	overview.Project = project
	groups := []Group{}
	for _, group := range overview.Groups {
		if group.Key == project {
			groups = append(groups, group)
		}
	}
	overview.Groups = groups
	overview.Standalone = nil
	return overview
}
