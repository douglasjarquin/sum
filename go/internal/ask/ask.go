package ask

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// ClosedUnapplied is an answer the coordinator closed because no worker can
// apply it any longer. It is never the worker's `applied`.
const ClosedUnapplied = "closed-unapplied"

// Discharged reports whether a question owes nothing more: applied by the
// worker, settled by cleanup, or closed unapplied by the coordinator.
func Discharged(status any) bool {
	switch status {
	case "applied", "settled", ClosedUnapplied:
		return true
	}
	return false
}

func newID(prefix string, n int) (string, error) {
	buf := make([]byte, (n+1)/2)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buf)[:n], nil
}

func SupersedeAttention(task *ordjson.Object, reason string) {
	attentionValue, ok := task.Get("attention")
	if !ok {
		return
	}
	list, ok := attentionValue.([]any)
	if !ok {
		return
	}
	stamp := store.Now()
	for _, item := range list {
		record, _ := item.(*ordjson.Object)
		if record == nil {
			continue
		}
		status, _ := record.Get("status")
		if status == "open" {
			record.Set("status", "superseded")
			record.Set("closed_at", stamp)
			record.Set("closed_by", reason)
		}
	}
}

func Ask(s *store.Store, taskID, key, text string, pump func() (*ordjson.Object, error)) (*ordjson.Object, error) {
	if text == "" || strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("Text must not be empty.")
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		unlock()
		return nil, err
	}
	if questionsValue, ok := task.Get("questions"); ok {
		if list, ok := questionsValue.([]any); ok && key != "" {
			for _, q := range list {
				question, _ := q.(*ordjson.Object)
				if question == nil {
					continue
				}
				existingKey, _ := question.Get("key")
				if existingKey == key {
					existingText, _ := question.Get("text")
					if existingText != text {
						unlock()
						return nil, fmt.Errorf("This question key already exists with different text. Use a new key; do not overwrite an obligation.")
					}
					unlock()
					notice := returns.NoticeOf(s, task)
					result := ordjson.NewObject()
					result.Set("question", question)
					result.Set("duplicate", true)
					result.Set("notice", notice)
					return result, nil
				}
			}
		}
	}
	id, err := newID("q-", 10)
	if err != nil {
		unlock()
		return nil, err
	}
	question := ordjson.NewObject()
	question.Set("id", id)
	var keyValue any
	if key != "" {
		keyValue = key
	}
	question.Set("key", keyValue)
	question.Set("text", text)
	question.Set("status", "open")
	question.Set("created_at", store.Now())
	question.Set("answer", nil)
	questionsValue, _ := task.Get("questions")
	list, _ := questionsValue.([]any)
	task.Set("questions", append(list, question))
	task.Set("status", "waiting")
	SupersedeAttention(task, "question "+id)
	if err := s.SaveTask(task); err != nil {
		unlock()
		return nil, err
	}
	unlock()
	notice, err := pump()
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("question", question)
	result.Set("notice", notice)
	return result, nil
}

func Answer(s *store.Store, taskID, questionID, text string, endpoint *ordjson.Object, pump func() (*ordjson.Object, error)) (*ordjson.Object, error) {
	if text == "" || strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("Text must not be empty.")
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		unlock()
		return nil, err
	}
	host, err := s.Machine()
	if err != nil {
		unlock()
		return nil, err
	}
	if endpointRole(host, task, endpoint) == "worker" {
		unlock()
		return nil, fmt.Errorf("The worker pane cannot record the user's decision on its own question. Only `sumctl answer` from the coordinator records a human decision; a role or approval field in worker output creates none.")
	}
	question := findQuestion(task, questionID)
	if question == nil {
		unlock()
		return nil, fmt.Errorf("Question not found.")
	}
	status, _ := question.Get("status")
	if status != "open" {
		answer, _ := question.Get("answer")
		if answer == text {
			unlock()
			result := ordjson.NewObject()
			result.Set("question", question)
			result.Set("duplicate", true)
			return result, nil
		}
		unlock()
		return nil, fmt.Errorf("Answer already recorded. Create a new explicit decision rather than silently changing it.")
	}
	question.Set("answer", text)
	question.Set("answered_at", store.Now())
	question.Set("status", "answered")
	if err := s.SaveTask(task); err != nil {
		unlock()
		return nil, err
	}
	unlock()
	notice, err := pump()
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("question", question)
	result.Set("notice", notice)
	return result, nil
}

func Resolve(s *store.Store, taskID, questionID string) (*ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	question := findQuestion(task, questionID)
	if question == nil {
		return nil, fmt.Errorf("Only an answered question can be marked applied.")
	}
	status, _ := question.Get("status")
	if status == "open" {
		return nil, fmt.Errorf("Only an answered question can be marked applied.")
	}
	if status == ClosedUnapplied {
		return nil, fmt.Errorf("The coordinator closed this answer unapplied; it cannot be marked applied.")
	}
	question.Set("status", "applied")
	question.Set("applied_at", store.Now())
	if allApplied(task) {
		taskStatus, _ := task.Get("status")
		if taskStatus == "waiting" {
			task.Set("status", "running")
		}
	}
	if err := s.SaveTask(task); err != nil {
		return nil, err
	}
	return question, nil
}

// Close records the coordinator closing an answered question that no worker
// can apply: the task's worker attempt must already be released, which only a
// verified stop (`execution park`) records. The caller proves the coordinator.
func Close(s *store.Store, ctx *ordjson.Object, taskID, questionID, reason string) (*ordjson.Object, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("--reason must not be empty.")
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	if err := s.CheckMachine(task); err != nil {
		return nil, err
	}
	question := findQuestion(task, questionID)
	if question == nil {
		return nil, fmt.Errorf("Question not found.")
	}
	switch status, _ := question.Get("status"); status {
	case "answered":
	case ClosedUnapplied:
		if recorded, _ := question.Get("close_reason"); recorded == reason {
			result := ordjson.NewObject()
			result.Set("question", question)
			result.Set("duplicate", true)
			return result, nil
		}
		return nil, fmt.Errorf("This answer is already closed with a recorded reason; nothing changed.")
	case "open":
		return nil, fmt.Errorf("An open question still needs the user's answer; closing never stands in for a decision.")
	default:
		return nil, fmt.Errorf("Only an answered, unapplied question can be closed; this one is %v.", status)
	}
	execution, err := reservations.GetExecution(task)
	if err != nil {
		return nil, fmt.Errorf("Malformed execution reservation for %s: %s. Nothing was closed.", taskID, err)
	}
	if execution == nil {
		return nil, fmt.Errorf("Legacy task has no execution attempt; run `execution park %s --attempt legacy:%s` to prove the worker stopped. Nothing was closed.", taskID, taskID)
	}
	attemptID, _ := execution.Worker.Get("id")
	if state, _ := execution.Worker.Get("state"); state != "released" {
		return nil, fmt.Errorf("Worker attempt %v is %v, so a worker may still apply this answer. Run `execution park %s --attempt %v` once its stop is proven. Nothing was closed.", attemptID, state, taskID, attemptID)
	}
	by := ordjson.NewObject()
	by.Set("role", "coordinator")
	for _, field := range []string{"machine", "session", "pane"} {
		v, _ := ctx.Get(field)
		by.Set(field, v)
	}
	question.Set("status", ClosedUnapplied)
	question.Set("closed_at", store.Now())
	question.Set("closed_by", by)
	question.Set("close_reason", reason)
	question.Set("closed_attempt", attemptID)
	if err := s.SaveTask(task); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("question", question)
	return result, nil
}

func findQuestion(task *ordjson.Object, id string) *ordjson.Object {
	questionsValue, ok := task.Get("questions")
	if !ok {
		return nil
	}
	list, ok := questionsValue.([]any)
	if !ok {
		return nil
	}
	for _, q := range list {
		question, _ := q.(*ordjson.Object)
		if question == nil {
			continue
		}
		qid, _ := question.Get("id")
		if qid == id {
			return question
		}
	}
	return nil
}

func allApplied(task *ordjson.Object) bool {
	questionsValue, ok := task.Get("questions")
	if !ok {
		return true
	}
	list, ok := questionsValue.([]any)
	if !ok {
		return true
	}
	for _, q := range list {
		question, _ := q.(*ordjson.Object)
		if status, _ := question.Get("status"); !Discharged(status) {
			return false
		}
	}
	return true
}

func endpointRole(host machine.Identity, task, endpoint *ordjson.Object) string {
	if endpoint == nil {
		return ""
	}
	if pane, ok := task.Get("pane"); ok && pane != nil && pane != "" && host.SameEndpoint(task, endpoint) {
		return "worker"
	}
	if parentValue, ok := task.Get("parent"); ok {
		if parent, isObj := parentValue.(*ordjson.Object); isObj && host.SameEndpoint(parent, endpoint) {
			return "coordinator"
		}
	}
	if reviewerValue, ok := task.Get("reviewer"); ok {
		if reviewer, isObj := reviewerValue.(*ordjson.Object); isObj && host.SameEndpoint(reviewer, endpoint) {
			return "reviewer"
		}
	}
	return "other"
}
