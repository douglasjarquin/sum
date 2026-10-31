package statuscmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/ask"
	"github.com/douglasjarquin/sum/go/internal/cleanup"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/lifecycle"
	"github.com/douglasjarquin/sum/go/internal/metadata"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/panes"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

// Options selects the view. Status and inbox are read-only in every form: --live adds one bounded Herdr observation
// per session and writes nothing. Delivery (`pump`, `init`) and maintenance (`sweep`, `pr reconcile`, `cleanup`) are
// separate commands.
type Options struct {
	Compact     bool
	After       int
	Limit       int
	MaxChars    int
	Inbox       bool
	Live        bool
	Ctx         *ordjson.Object
	RuntimeRoot string
	SumctlPath  string
}

func Status(s *store.Store, opts Options) (*ordjson.Object, error) {
	if opts.Compact {
		return Compact(s, opts)
	}
	tasks, err := s.AllTasks()
	if err != nil {
		return nil, err
	}

	var rows []*ordjson.Object
	var rowTasks []*ordjson.Object
	for _, task := range tasks {
		row := buildRow(s, task)
		questionsValue, _ := row.Get("questions")
		questionList, _ := questionsValue.([]any)
		errorValue, _ := row.Get("error")
		reportAvailable, _ := row.Get("report_available")
		cleanupValue, _ := row.Get("cleanup")
		_, hasAttention := row.Get("attention")
		attentionRecords, _ := row.Get("attention_records")
		attentionList, _ := attentionRecords.([]any)

		reviewThreads := hasOpenReviewThreads(task)
		include := !opts.Inbox || len(questionList) > 0 || errorValue != nil || hasAttention || reportAvailable == true || cleanupValue != nil || len(attentionList) > 0 || reviewThreads
		if !include {
			continue
		}
		statusValue, _ := task.Get("status")
		if statusValue == "archived" && len(questionList) == 0 {
			continue
		}
		rows = append(rows, row)
		rowTasks = append(rowTasks, task)
	}

	for i, row := range rows {
		row.Set("notice", returns.NoticeOf(s, rowTasks[i]))
		view, err := returns.View(s, rowTasks[i])
		if err != nil {
			returnsErr := ordjson.NewObject()
			returnsErr.Set("error", err.Error())
			row.Set("returns", returnsErr)
			continue
		}
		open, _ := view.Get("open")
		row.Set("returns", open)
	}

	rowsAny := make([]any, len(rows))
	for i, r := range rows {
		rowsAny[i] = r
	}

	capacityView, err := settings.CapacityViewOf(s, tasks)
	if err != nil {
		return nil, err
	}

	result := ordjson.NewObject()
	result.Set("tasks", rowsAny)
	result.Set("live", false)
	result.Set("capacity", capacityView)
	result.Set("maintenance", lifecycle.Pending(s, opts.SumctlPath, tasks))
	result.Set("guarantee", "Saved records only. Open attention includes the recorded excerpt. No background monitoring. Nothing is delivered, observed on GitHub, or cleaned up by this view.")
	if opts.Live {
		observe(s, opts, rows, rowTasks, result)
	}
	result.Set("metadata", metadata.Summary(s))
	return result, nil
}

// observe adds one bounded `agent list` per Herdr session of the listed local, unarchived tasks: each such row gets the
// agent state Herdr reports now, and a worker missing from its session's list reads as absent without its own lookup.
// It writes nothing.
func observe(s *store.Store, opts Options, rows, tasks []*ordjson.Object, result *ordjson.Object) {
	host, err := s.Machine()
	if err != nil {
		result.Set("live_reason", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), returns.DefaultPassBudget)
	defer cancel()
	snapshots := map[string]hcclient.ListResult{}
	failures := map[string]error{}
	for i, row := range rows {
		endpoint := store.EndpointFromContext(returns.ReturnRoute(tasks[i], "worker"))
		observed := ordjson.NewObject()
		if endpoint.Validate() != nil || !host.Is(endpoint.Machine) {
			observed.Set("state", "unobserved")
			observed.Set("reason", "no supported local native endpoint")
			row.Set("observed", observed)
			continue
		}
		key := endpoint.CoreID + "/" + endpoint.Socket
		snapshot, ok := snapshots[key]
		if !ok && failures[key] == nil {
			client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID, Timeout: returns.ObserveTimeout}
			snapshot, err = client.List(ctx, hcclient.ListParams{All: true})
			if err != nil {
				failures[key] = err
			} else {
				snapshots[key] = snapshot
			}
		}
		if failures[key] != nil {
			observed.Set("state", "unobserved")
			observed.Set("reason", failures[key].Error())
		} else {
			observed.Set("state", "absent")
			for _, terminal := range snapshot.Terminals {
				if terminal.Address == endpoint.Address {
					observed.Set("native", func() any { o, _ := ordjson.FromValue(terminal); return o }())
					state := "unknown"
					if terminal.Status != nil {
						state = terminal.Status.State
					}
					observed.Set("state", state)
					observed.Set("cwd", terminal.Cwd)
					if terminal.CreatedMS != endpoint.CreatedMS {
						observed.Set("state", "replaced")
					}
					break
				}
			}
		}
		row.Set("observed", observed)
	}
	result.Set("live", true)
	result.Set("guarantee", "Saved records plus bounded read-only native HC snapshots. Native status, exits and turns are observational; they supply no task, review, verification or approval receipt.")
}

func buildRow(s *store.Store, task *ordjson.Object) *ordjson.Object {
	row := ordjson.NewObject()
	for _, key := range []string{"id", "status", "repository", "harness", "worker_endpoint", "worktree", "error"} {
		v, _ := task.Get(key)
		row.Set(key, v)
	}
	if panes.WorkerIsClosed(task) {
		id, _ := task.Get("id")
		row.Set("pane_closed", true)
		row.Set("pane_note", panes.ResumeNote(fmt.Sprint(id)))
	}

	var model any
	if launchValue, ok := task.Get("launch"); ok {
		if launch, ok := launchValue.(*ordjson.Object); ok {
			model, _ = launch.Get("model")
		}
	}
	row.Set("model", model)

	var graphState any
	if graphValue, ok := task.Get("graph"); ok {
		if graph, ok := graphValue.(*ordjson.Object); ok {
			graphState, _ = graph.Get("state")
		}
	}
	row.Set("graph", graphState)

	var openQuestions []any
	if questionsValue, ok := task.Get("questions"); ok {
		if list, ok := questionsValue.([]any); ok {
			for _, q := range list {
				question, _ := q.(*ordjson.Object)
				if status, _ := question.Get("status"); !ask.Discharged(status) {
					openQuestions = append(openQuestions, question)
				}
			}
		}
	}
	if openQuestions == nil {
		openQuestions = []any{}
	}
	row.Set("questions", openQuestions)

	reportValue, hasReport := task.Get("report")
	row.Set("report_available", hasReport && reportValue != nil)

	evidenceValue, _ := task.Get("evidence")
	evidenceList, _ := evidenceValue.([]any)
	var prNumber any
	var mergedForTask bool
	if prValue, ok := task.Get("pr"); ok {
		if pr, ok := prValue.(*ordjson.Object); ok && pr != nil {
			if identityValue, ok := pr.Get("identity"); ok {
				if identity, ok := identityValue.(*ordjson.Object); ok {
					prNumber, _ = identity.Get("number")
				}
			}
			if merged, _ := pr.Get("merged_for_task"); merged == true {
				mergedForTask = true
			}
		}
	}
	evidence := ordjson.NewObject()
	evidence.Set("records", jsonInt(len(evidenceList)))
	evidence.Set("pr", prNumber)
	evidence.Set("merged_for_task", mergedForTask)
	row.Set("evidence", evidence)
	if saved := savedReviewThreads(task); saved != nil {
		row.Set("review_threads", saved)
	}

	row.Set("notice", nil) // holds the key's place; Status sets the derived view for each row it keeps

	var attentionRows []any
	for _, a := range returns.OpenAttention(task) {
		entry := ordjson.NewObject()
		for _, key := range []string{"id", "kind", "at", "observed", "excerpt", "source"} {
			v, _ := a.Get(key)
			entry.Set(key, v)
		}
		attentionRows = append(attentionRows, entry)
	}
	if attentionRows == nil {
		attentionRows = []any{}
	}
	row.Set("attention_records", attentionRows)

	cleanupPending := cleanup.Pending(task)
	if cleanupPending != nil {
		row.Set("cleanup", cleanupPending)
	} else {
		row.Set("cleanup", nil)
	}

	versionsObj, err := versions.ReadVersions(s, task)
	if err != nil {
		brief := ordjson.NewObject()
		brief.Set("error", err.Error())
		row.Set("brief", brief)
	} else {
		active, _ := versionsObj.Get("active")
		requestedValue, _ := versionsObj.Get("requested")
		brief := ordjson.NewObject()
		brief.Set("active", active)
		brief.Set("requested", requestedValue)
		row.Set("brief", brief)
		if requested, ok := requestedValue.(string); ok && requested != "" {
			row.Set("refresh", versions.RefreshState(versionsObj))
		}
	}

	return row
}

func hasOpenReviewThreads(task *ordjson.Object) bool {
	pr, _ := task.Get("pr")
	prRecord, _ := pr.(*ordjson.Object)
	observationValue, _ := func() (any, bool) {
		if prRecord == nil {
			return nil, false
		}
		return prRecord.Get("review_threads")
	}()
	observation, _ := observationValue.(*ordjson.Object)
	threadsValue, _ := func() (any, bool) {
		if observation == nil {
			return nil, false
		}
		return observation.Get("threads")
	}()
	threads, _ := threadsValue.([]any)
	for _, raw := range threads {
		thread, _ := raw.(*ordjson.Object)
		if thread != nil {
			resolved, _ := thread.Get("resolved")
			if resolved != true {
				return true
			}
		}
	}
	return false
}

func savedReviewThreads(task *ordjson.Object) *ordjson.Object {
	prValue, _ := task.Get("pr")
	pr, _ := prValue.(*ordjson.Object)
	identity, _ := func() (any, bool) {
		if pr == nil {
			return nil, false
		}
		return pr.Get("identity")
	}()
	prIdentity, _ := identity.(*ordjson.Object)
	observationValue, _ := func() (any, bool) {
		if pr == nil {
			return nil, false
		}
		return pr.Get("review_threads")
	}()
	observation, _ := observationValue.(*ordjson.Object)
	if observation == nil {
		return nil
	}
	result := ordjson.NewObject()
	for _, key := range []string{"status", "repository", "pr_number", "observed_at", "observed_head_sha", "review_decision", "detail"} {
		value, _ := observation.Get(key)
		result.Set(key, value)
	}
	result.Set("pr_url", value(prIdentity, "url"))
	rows := []any{}
	threadsValue, _ := observation.Get("threads")
	threads, _ := threadsValue.([]any)
	for _, raw := range threads {
		thread, _ := raw.(*ordjson.Object)
		if thread == nil {
			continue
		}
		row := ordjson.NewObject()
		for _, key := range []string{"id", "path", "line", "resolved", "outdated", "latest_comment_id", "latest_updated_at", "latest_author", "source_url", "observed_at", "observed_head_sha", "actions", "request_id"} {
			value, _ := thread.Get(key)
			row.Set(key, value)
		}
		requestID, requestStatus := latestReviewThreadRequest(task, fmt.Sprint(value(thread, "id")), fmt.Sprint(value(thread, "latest_comment_id")), fmt.Sprint(value(thread, "observed_head_sha")), fmt.Sprint(value(prIdentity, "url")))
		row.Set("request_id", requestID)
		row.Set("request_status", requestStatus)
		commentsValue, _ := thread.Get("comments")
		comments, _ := commentsValue.([]any)
		if len(comments) > 0 {
			comment, _ := comments[0].(*ordjson.Object)
			if comment != nil {
				for _, key := range []string{"id", "body", "author", "url", "updated_at"} {
					value, _ := comment.Get(key)
					row.Set("latest_"+key, value)
				}
			}
		}
		rows = append(rows, row)
	}
	result.Set("threads", rows)
	return result
}

func latestReviewThreadRequest(task *ordjson.Object, threadID, revision, head, prURL string) (string, string) {
	requestsValue, _ := task.Get("github_review_actions")
	requests, _ := requestsValue.([]any)
	for i := len(requests) - 1; i >= 0; i-- {
		request, _ := requests[i].(*ordjson.Object)
		if request == nil || value(request, "head_sha") != head || value(request, "pr_url") != prURL {
			continue
		}
		threadsValue, _ := request.Get("threads")
		threads, _ := threadsValue.([]any)
		for _, raw := range threads {
			thread, _ := raw.(*ordjson.Object)
			if thread != nil && value(thread, "id") == threadID && value(thread, "revision") == revision {
				return fmt.Sprint(value(request, "id")), fmt.Sprint(value(request, "status"))
			}
		}
	}
	return "", ""
}

func value(record *ordjson.Object, key string) any {
	if record == nil {
		return nil
	}
	value, _ := record.Get(key)
	return value
}
