package ask

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type Choice struct {
	ID    string
	Label string
}

type StructuredDecision struct {
	Choices     []Choice
	Recommended string
	Rationale   string
	Sources     []string
}

var choiceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func decisionRecord(decision *StructuredDecision) (*ordjson.Object, error) {
	if decision == nil {
		return nil, nil
	}
	if len(decision.Choices) < 2 || len(decision.Choices) > 8 {
		return nil, fmt.Errorf("Structured questions need between 2 and 8 choices.")
	}
	if strings.TrimSpace(decision.Rationale) == "" || len([]rune(decision.Rationale)) > 280 {
		return nil, fmt.Errorf("Structured questions need a rationale of 1 to 280 characters.")
	}
	if len(decision.Sources) > 8 {
		return nil, fmt.Errorf("Structured questions allow at most 8 source references.")
	}
	choices := make([]any, 0, len(decision.Choices))
	seen := map[string]bool{}
	recommended := false
	for _, choice := range decision.Choices {
		if !choiceIDPattern.MatchString(choice.ID) || seen[choice.ID] {
			return nil, fmt.Errorf("Choice IDs must be unique and use 1 to 64 letters, digits, dots, underscores, or hyphens.")
		}
		if strings.TrimSpace(choice.Label) == "" || len([]rune(choice.Label)) > 120 {
			return nil, fmt.Errorf("Choice labels must contain 1 to 120 characters.")
		}
		seen[choice.ID] = true
		recommended = recommended || choice.ID == decision.Recommended
		row := ordjson.NewObject()
		row.Set("id", choice.ID)
		row.Set("label", choice.Label)
		choices = append(choices, row)
	}
	if !recommended {
		return nil, fmt.Errorf("The recommended choice must name an offered choice ID.")
	}
	sources := make([]any, 0, len(decision.Sources))
	for _, source := range decision.Sources {
		if strings.TrimSpace(source) == "" || len([]rune(source)) > 500 {
			return nil, fmt.Errorf("Source references must contain 1 to 500 characters.")
		}
		sources = append(sources, source)
	}
	row := ordjson.NewObject()
	row.Set("choices", choices)
	row.Set("recommended", decision.Recommended)
	row.Set("rationale", decision.Rationale)
	row.Set("sources", sources)
	return row, nil
}

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
	return AskStructured(s, taskID, key, text, nil, pump)
}

func AskStructured(s *store.Store, taskID, key, text string, decision *StructuredDecision, pump func() (*ordjson.Object, error)) (*ordjson.Object, error) {
	if text == "" || strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("Text must not be empty.")
	}
	record, err := decisionRecord(decision)
	if err != nil {
		return nil, err
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
					oldDecision := ordjson.NewObject()
					for _, field := range []string{"choices", "recommended", "rationale", "sources"} {
						value, _ := question.Get(field)
						oldDecision.Set(field, value)
					}
					newDecision := ordjson.NewObject()
					for _, field := range []string{"choices", "recommended", "rationale", "sources"} {
						value := any(nil)
						if record != nil {
							value, _ = record.Get(field)
						}
						newDecision.Set(field, value)
					}
					oldBytes, _ := ordjson.MarshalSortedCompact(oldDecision)
					newBytes, _ := ordjson.MarshalSortedCompact(newDecision)
					if existingText != text || string(oldBytes) != string(newBytes) {
						unlock()
						return nil, fmt.Errorf("This question key already exists with different content. Use a new key; do not overwrite an obligation.")
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
	if record != nil {
		for _, field := range record.Keys() {
			value, _ := record.Get(field)
			question.Set(field, value)
		}
	}
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
	return answerOperation(s, taskID, questionID, text, "", "", endpoint, nil, false, pump)
}

func ExpectedWorkerRecipient(s *store.Store, task *ordjson.Object) (string, error) {
	route := returns.ReturnRoute(task, "worker")
	field := func(key string) string {
		value, _ := route.Get(key)
		result, _ := value.(string)
		return result
	}
	endpoint := store.Endpoint{Machine: field("machine"), Session: field("session"), Pane: field("pane")}
	if endpoint.Machine == "" || endpoint.Session == "" || endpoint.Pane == "" {
		return "", nil
	}
	registration, err := s.Registration(endpoint)
	if err != nil || registration == nil {
		return "", err
	}
	role, _ := registration.Get("role")
	registeredTask, _ := registration.Get("task")
	if role != "worker" || registeredTask != routeTask(task) {
		return "", nil
	}
	identity := ordjson.NewObject()
	identity.Set("task", routeTask(task))
	identity.Set("machine", endpoint.Machine)
	identity.Set("session", endpoint.Session)
	identity.Set("pane", endpoint.Pane)
	for _, key := range []string{"registered_at", "incarnation"} {
		value, _ := registration.Get(key)
		identity.Set(key, value)
	}
	data, err := ordjson.MarshalSortedCompact(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "wr-" + hex.EncodeToString(sum[:12]), nil
}

func routeTask(task *ordjson.Object) any {
	value, _ := task.Get("id")
	return value
}

func Explain(s *store.Store, taskID, questionID, text, expectedRecipient string, pump func() (*ordjson.Object, error)) (*ordjson.Object, error) {
	return ExplainWithRecipientCheck(s, taskID, questionID, text, expectedRecipient, nil, pump)
}

type RecipientCheck func(*store.Store, *ordjson.Object) error

func ExplainWithRecipientCheck(s *store.Store, taskID, questionID, text, expectedRecipient string, checkRecipient RecipientCheck, pump func() (*ordjson.Object, error)) (*ordjson.Object, error) {
	if strings.TrimSpace(text) == "" || len([]rune(text)) > 2000 {
		return nil, fmt.Errorf("Explain text must contain 1 to 2000 characters.")
	}
	if expectedRecipient == "" {
		return nil, fmt.Errorf("Explain requires a captured worker recipient. Refresh the inbox and use its action route.")
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
	currentRecipient, err := ExpectedWorkerRecipient(s, task)
	if err != nil {
		unlock()
		return nil, err
	}
	if currentRecipient == "" || currentRecipient != expectedRecipient {
		unlock()
		return nil, fmt.Errorf("The worker recipient changed since this action was presented. Refresh the inbox and ask again.")
	}
	if checkRecipient != nil {
		if err := checkRecipient(s, task); err != nil {
			unlock()
			return nil, err
		}
	}
	question := findQuestion(task, questionID)
	if question == nil {
		unlock()
		return nil, fmt.Errorf("Question not found.")
	}
	if status, _ := question.Get("status"); status != "open" {
		unlock()
		return nil, fmt.Errorf("Explain is available only while the question is open.")
	}
	identity := sha256.Sum256([]byte(taskID + "\n" + questionID + "\n" + expectedRecipient + "\n" + text))
	id := "clar-" + hex.EncodeToString(identity[:12])
	value, _ := question.Get("clarifications")
	clarifications, _ := value.([]any)
	for _, raw := range clarifications {
		row, _ := raw.(*ordjson.Object)
		if row == nil {
			continue
		}
		if savedID, _ := row.Get("id"); savedID == id {
			result := ordjson.NewObject()
			result.Set("question", question)
			result.Set("duplicate", true)
			unlock()
			return result, nil
		}
	}
	clarification := ordjson.NewObject()
	clarification.Set("id", id)
	clarification.Set("text", text)
	clarification.Set("created_at", store.Now())
	question.Set("clarifications", append(clarifications, clarification))
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

func AnswerExpected(s *store.Store, taskID, questionID, text, choiceID, expectedRecipient string, endpoint *ordjson.Object, pump func() (*ordjson.Object, error)) (*ordjson.Object, error) {
	return AnswerExpectedWithRecipientCheck(s, taskID, questionID, text, choiceID, expectedRecipient, endpoint, nil, pump)
}

func AnswerExpectedWithRecipientCheck(s *store.Store, taskID, questionID, text, choiceID, expectedRecipient string, endpoint *ordjson.Object, checkRecipient RecipientCheck, pump func() (*ordjson.Object, error)) (*ordjson.Object, error) {
	return answerOperation(s, taskID, questionID, text, choiceID, expectedRecipient, endpoint, checkRecipient, true, pump)
}

func answerOperation(s *store.Store, taskID, questionID, text, choiceID, expectedRecipient string, endpoint *ordjson.Object, checkRecipient RecipientCheck, requireCoordinator bool, pump func() (*ordjson.Object, error)) (*ordjson.Object, error) {
	if choiceID == "" && (text == "" || strings.TrimSpace(text) == "") {
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
	role := endpointRole(host, task, endpoint)
	if role == "worker" {
		unlock()
		return nil, fmt.Errorf("The worker pane cannot record the user's decision on its own question. Only `sumctl answer` from the coordinator records a human decision; a role or approval field in worker output creates none.")
	}
	if requireCoordinator && role != "coordinator" {
		unlock()
		return nil, fmt.Errorf("Only the task's registered coordinator may record the user's decision.")
	}
	if expectedRecipient != "" {
		currentRecipient, identityErr := ExpectedWorkerRecipient(s, task)
		if identityErr != nil {
			unlock()
			return nil, identityErr
		}
		if currentRecipient == "" || currentRecipient != expectedRecipient {
			unlock()
			return nil, fmt.Errorf("The worker recipient changed since this action was presented. Refresh the inbox and choose again.")
		}
		if checkRecipient != nil {
			if err := checkRecipient(s, task); err != nil {
				unlock()
				return nil, err
			}
		}
	}
	question := findQuestion(task, questionID)
	if question == nil {
		unlock()
		return nil, fmt.Errorf("Question not found.")
	}
	if choiceID != "" {
		if expectedRecipient == "" {
			unlock()
			return nil, fmt.Errorf("Choosing a listed answer requires a captured worker recipient. Refresh the inbox and choose again.")
		}
		choices, _ := question.Get("choices")
		list, _ := choices.([]any)
		label := ""
		for _, raw := range list {
			choice, _ := raw.(*ordjson.Object)
			if choice == nil {
				continue
			}
			id, _ := choice.Get("id")
			if id == choiceID {
				value, _ := choice.Get("label")
				label, _ = value.(string)
				break
			}
		}
		if label == "" {
			unlock()
			return nil, fmt.Errorf("Choice %q is not offered by this question.", choiceID)
		}
		text = label
	}
	status, _ := question.Get("status")
	if status != "open" {
		answer, _ := question.Get("answer")
		savedChoice, _ := question.Get("answer_choice")
		if answer == text && (choiceID == "" || savedChoice == choiceID) {
			unlock()
			result := ordjson.NewObject()
			result.Set("question", question)
			result.Set("duplicate", true)
			return result, nil
		}
		unlock()
		return nil, fmt.Errorf("Answer already recorded. Create a new explicit decision rather than silently changing it.")
	}
	if choiceID != "" {
		question.Set("answer_choice", choiceID)
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
