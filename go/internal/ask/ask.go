package ask

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

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
					notice, _ := task.Get("notice")
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
	if endpointRole(task, endpoint) == "worker" {
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
		status, _ := question.Get("status")
		if status != "applied" {
			return false
		}
	}
	return true
}

func endpointRole(task, endpoint *ordjson.Object) string {
	if endpoint == nil {
		return ""
	}
	if pane, ok := task.Get("pane"); ok && pane != nil && pane != "" && identitiesEqual(task, endpoint) {
		return "worker"
	}
	if parentValue, ok := task.Get("parent"); ok {
		if parent, isObj := parentValue.(*ordjson.Object); isObj && identitiesEqual(parent, endpoint) {
			return "coordinator"
		}
	}
	if reviewerValue, ok := task.Get("reviewer"); ok {
		if reviewer, isObj := reviewerValue.(*ordjson.Object); isObj && identitiesEqual(reviewer, endpoint) {
			return "reviewer"
		}
	}
	return "other"
}

func identitiesEqual(a, b *ordjson.Object) bool {
	for _, field := range []string{"machine", "session", "pane"} {
		av, _ := a.Get(field)
		bv, _ := b.Get(field)
		if av != bv {
			return false
		}
	}
	return true
}
