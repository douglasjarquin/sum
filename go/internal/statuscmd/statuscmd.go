package statuscmd

import (
	"encoding/json"
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/cleanup"
	"github.com/douglasjarquin/sum/go/internal/metadata"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func Status(s *store.Store, inbox bool) (*ordjson.Object, error) {
	tasks, err := s.AllTasks()
	if err != nil {
		return nil, err
	}

	var rows []*ordjson.Object
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

		include := !inbox || len(questionList) > 0 || errorValue != nil || hasAttention || reportAvailable == true || cleanupValue != nil || len(attentionList) > 0
		if !include {
			continue
		}
		statusValue, _ := task.Get("status")
		if statusValue == "archived" && len(questionList) == 0 {
			continue
		}
		rows = append(rows, row)
	}

	for _, row := range rows {
		idValue, _ := row.Get("id")
		id, _ := idValue.(string)
		task, err := s.ReadTask(id)
		if err != nil {
			returnsErr := ordjson.NewObject()
			returnsErr.Set("error", err.Error())
			row.Set("returns", returnsErr)
			continue
		}
		view, err := returns.View(s, task)
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
	result.Set("guarantee", "Saved records only. Open attention includes the recorded excerpt. No background monitoring.")
	result.Set("metadata", metadata.Summary(s))
	return result, nil
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
				if status, _ := question.Get("status"); status != "applied" {
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
