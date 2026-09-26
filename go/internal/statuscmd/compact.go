package statuscmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/factoryview"
	"github.com/douglasjarquin/sum/go/internal/inboxview"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/presentation"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const CompactLimit = 20
const CompactChars = 240
const CompactMaxLimit = 100
const CompactMaxChars = 2000

// Compact pages presentation items, never source records or canonical obligations.
// Counts cover the whole saved snapshot, including decisions outside this page.
func Compact(s *store.Store, opts Options) (*ordjson.Object, error) {
	if opts.Limit < 1 || opts.Limit > CompactMaxLimit || opts.After < 0 || opts.MaxChars < 1 || opts.MaxChars > CompactMaxChars {
		return nil, fmt.Errorf("compact --limit must be 1..%d, --after nonnegative, and --max-chars 1..%d; use the detail route for full text", CompactMaxLimit, CompactMaxChars)
	}
	snapshot := inboxview.Read(s)
	tasks := map[string]inboxview.Task{}
	for _, task := range snapshot.Tasks {
		tasks[task.ID] = task
	}
	items := []presentation.Item{}
	for _, item := range snapshot.Items {
		if opts.Inbox && item.Owner == presentation.Nobody {
			continue
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if inboxview.Rank(items[i].Kind) != inboxview.Rank(items[j].Kind) {
			return inboxview.Rank(items[i].Kind) < inboxview.Rank(items[j].Kind)
		}
		return items[i].Identity < items[j].Identity
	})
	after := min(opts.After, len(items))
	end := after + min(opts.Limit, len(items)-after)
	rows := []any{}
	for _, item := range items[after:end] {
		row := ordjson.NewObject()
		row.Set("source", compactJSON(item.Source))
		if item.ProjectID != "" {
			row.Set("project", item.ProjectID)
		}
		row.Set("owner", string(item.Owner))
		row.Set("presentation", string(item.Kind))
		row.Set("reason", item.Reason)
		if item.State != "" {
			row.Set("state", item.State)
		}
		if item.At != "" {
			row.Set("at", item.At)
		}
		if item.Delivery != "" {
			row.Set("delivery", item.Delivery)
		}
		row.Set("uncertain", item.Uncertain)
		if item.Text != "" {
			text := environment.BoundedView(item.Text, opts.MaxChars)
			if truncated, _ := text.Get("truncated"); truncated == true {
				text.Set("note", "Text omitted; use detail for the complete saved record.")
			}
			row.Set("text", text)
		}
		task := tasks[item.Source.TaskID]
		detail := inboxview.DetailRoute(item, task)
		if len(detail) > 0 {
			row.Set("detail", compactJSON(detail))
		}
		if item.Kind == presentation.Decision {
			row.Set("answer", compactJSON([]string{"answer", item.Source.TaskID, strings.TrimPrefix(item.Source.ID, "question:"), "--text"}))
		}
		if item.Source.Kind == presentation.Gap || len(detail) == 0 {
			row.Set("sources", compactJSON(item.Details))
		}
		rows = append(rows, row)
	}
	page := ordjson.NewObject()
	page.Set("total", jsonInt(len(items)))
	page.Set("after", jsonInt(after))
	page.Set("items", rows)
	page.Set("omitted", jsonInt(len(items)-len(rows)))
	page.Set("next_after", nil)
	if end < len(items) {
		page.Set("next_after", jsonInt(end))
	}
	result := ordjson.NewObject()
	result.Set("compact", true)
	result.Set("counts", compactJSON(snapshot.Counts))
	result.Set("complete", snapshot.Complete)
	result.Set("page", page)
	result.Set("live", false)
	if opts.Live {
		observedRows := []*ordjson.Object{}
		records := []*ordjson.Object{}
		ids := map[string]bool{}
		for _, item := range items[after:end] {
			task, ok := tasks[item.Source.TaskID]
			if !ok || ids[task.ID] {
				continue
			}
			ids[task.ID] = true
			row := ordjson.NewObject()
			row.Set("id", task.ID)
			observedRows = append(observedRows, row)
			records = append(records, task.Record)
		}
		observe(s, opts, observedRows, records, result)
		scope := ordjson.NewObject()
		scope.Set("scope", "rendered page tasks only; counts remain saved global facts")
		scope.Set("tasks", jsonInt(len(records)))
		scope.Set("omitted_tasks", jsonInt(len(snapshot.Tasks)-len(records)))
		result.Set("observation_scope", scope)
		views := []any{}
		for _, row := range observedRows {
			views = append(views, row)
		}
		result.Set("observations", views)
	}
	result.Set("detail", compactJSON([]string{"status"}))
	result.Set("note", "Known global counts precede paging; incomplete coverage may hide more work. Routes are sumctl argument arrays; append the user's actual answer after --text. Paging is a fresh read, not a receipt; restart at --after 0 if records change. Full status retains capacity and maintenance. Saved CI and delivery states are not new observations.")
	return result, nil
}

func compactJSON(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	parsed, err := ordjson.Decode(raw)
	if err != nil {
		panic(err)
	}
	return parsed
}

// Grouped is the read-only grouped overview; `project` narrows the groups while every global fact stays.
func Grouped(s *store.Store, project string) (*ordjson.Object, error) {
	overview, err := GroupedOverview(s, project)
	if err != nil {
		return nil, err
	}
	return groupedObject(overview)
}

func groupedObject(overview inboxview.Overview) (*ordjson.Object, error) {
	converted, err := ordjson.FromValue(overview)
	if err != nil {
		return nil, fmt.Errorf("grouped overview did not encode as an object")
	}
	return converted, nil
}

func GroupedOverview(s *store.Store, project string) (inboxview.Overview, error) {
	return inboxview.Focus(inboxview.BuildOverview(inboxview.Read(s)), project), nil
}

// GroupedDigest is the grouped overview with the factory digest attached, from one read of the saved records:
// each group carries its digest row and `digest` carries the envelope (deltas, page, cursor, resync).
func GroupedDigest(s *store.Store, project, since string, limit int) (*ordjson.Object, error) {
	installation, err := s.Instance()
	if err != nil {
		return nil, err
	}
	snapshot := inboxview.Read(s)
	full := inboxview.BuildOverview(snapshot)
	digest := factoryview.BuildFromOverview(snapshot, full, installation, factoryview.Options{Project: project, Since: since, Limit: limit})
	overview := inboxview.Focus(full, project)
	factoryview.Attach(&overview, digest)
	converted, err := groupedObject(overview)
	if err != nil {
		return nil, err
	}
	encoded, err := ordjson.FromValue(digest)
	if err != nil {
		return nil, err
	}
	converted.Set("digest", encoded)
	return converted, nil
}
