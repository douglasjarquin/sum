package statuscmd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ask"
	"github.com/douglasjarquin/sum/go/internal/cleanup"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/lifecycle"
	"github.com/douglasjarquin/sum/go/internal/metadata"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

// Options selects the view. Status and inbox are read-only in every form: --live adds one bounded Herdr observation
// per session and writes nothing. Delivery (`pump`, `init`) and maintenance (`sweep`, `pr reconcile`, `cleanup`) are
// separate commands.
type Options struct {
	Inbox       bool
	Live        bool
	Ctx         *ordjson.Object
	RuntimeRoot string
	SumctlPath  string
}

func Status(s *store.Store, opts Options) (*ordjson.Object, error) {
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

		include := !opts.Inbox || len(questionList) > 0 || errorValue != nil || hasAttention || reportAvailable == true || cleanupValue != nil || len(attentionList) > 0
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

	capacityView, err := settings.CapacityView(s)
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
	if opts.Ctx == nil {
		result.Set("live_reason", "this pane has no Herdr context; the saved-record view is shown and nothing was observed")
		return
	}
	host, err := s.Machine()
	if err != nil {
		result.Set("live_reason", err.Error())
		return
	}
	budget := returns.DefaultPassBudget
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	sn := herdrclient.NewSnapshot(ctx, func() (string, error) { return toolpath.Find(opts.RuntimeRoot, "herdr") }, returns.ObserveTimeout)
	for i, row := range rows {
		task := tasks[i]
		status, _ := task.Get("status")
		machineValue, _ := task.Get("machine")
		session, _ := task.Get("session")
		pane, _ := task.Get("pane")
		sessionStr, _ := session.(string)
		paneStr, _ := pane.(string)
		if status == "archived" || sessionStr == "" || paneStr == "" {
			continue
		}
		observed := ordjson.NewObject()
		switch {
		case !host.Is(machineValue):
			observed.Set("state", "unobserved")
			observed.Set("reason", "task belongs to another machine")
		case !sn.Listed(sessionStr) && deadlineLeft(ctx) < returns.ObserveTimeout:
			observed.Set("state", "unobserved")
			observed.Set("reason", "the observation budget ran out before this session was listed")
		default:
			agent, err := sn.Agent(sessionStr, paneStr)
			switch {
			case err == nil:
				observed.Set("state", herdrclient.AgentStatus(agent))
				observed.Set("cwd", herdrclient.AgentCwd(agent))
			case herdrclient.ErrorIsAbsent(err):
				observed.Set("state", "absent")
				observed.Set("reason", "not in the session's agent list; inspect before assuming anything")
			default:
				observed.Set("state", "unobserved")
				observed.Set("reason", err.Error())
			}
		}
		row.Set("observed", observed)
	}
	fanout := ordjson.NewObject()
	fanout.Set("sessions", jsonInt(sn.Sessions()))
	fanout.Set("herdr_calls", jsonInt(sn.Calls()))
	fanout.Set("elapsed_ms", jsonInt(int(sn.Elapsed().Milliseconds())))
	fanout.Set("budget_ms", jsonInt(int(budget.Milliseconds())))
	result.Set("live", true)
	result.Set("fanout", fanout)
	result.Set("guarantee", "Saved records plus one bounded Herdr agent list per session, observed now. Nothing is written, delivered, observed on GitHub, or cleaned up by this view; `sumctl pump` delivers and `sumctl sweep` maintains. No background monitoring.")
}

func deadlineLeft(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return time.Hour
	}
	return time.Until(deadline)
}

func buildRow(s *store.Store, task *ordjson.Object) *ordjson.Object {
	row := ordjson.NewObject()
	for _, key := range []string{"id", "status", "repository", "harness", "pane", "session", "worktree", "error"} {
		v, _ := task.Get(key)
		row.Set(key, v)
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

	noticeValue, _ := task.Get("notice")
	row.Set("notice", noticeValue)

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
