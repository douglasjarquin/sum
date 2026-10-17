package ask

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", 1)
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("status", "running")
	task.Set("questions", []any{})
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	return s
}

func registeredWorker(t *testing.T) (*store.Store, *ordjson.Object, *ordjson.Object) {
	t.Helper()
	s := testStore(t)
	task, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	endpoint := store.Endpoint{Machine: host.ID, Session: "worker-session", Pane: "w-worker:p1"}
	task.Set("machine", host.ID)
	task.Set("session", endpoint.Session)
	task.Set("pane", endpoint.Pane)
	worker := ordjson.NewObject()
	worker.Set("terminal", "worker-one")
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register(endpoint, "worker", "t-aaaaaaaaaaaa", worker); err != nil {
		t.Fatal(err)
	}
	question := ordjson.NewObject()
	question.Set("id", "q-one")
	question.Set("key", "route")
	question.Set("text", "Choose a route.")
	question.Set("status", "open")
	question.Set("answer", nil)
	question.Set("choices", []any{
		func() *ordjson.Object {
			row := ordjson.NewObject()
			row.Set("id", "keep")
			row.Set("label", "Keep the change")
			return row
		}(),
		func() *ordjson.Object {
			row := ordjson.NewObject()
			row.Set("id", "revert")
			row.Set("label", "Revert it")
			return row
		}(),
	})
	task.Set("questions", []any{question})
	coord := ordjson.NewObject()
	coord.Set("machine", host.ID)
	coord.Set("session", "coord-session")
	coord.Set("pane", "w-coord:p1")
	task.Set("parent", coord)
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	ctx := ordjson.NewObject()
	ctx.Set("machine", host.ID)
	ctx.Set("session", "coord-session")
	ctx.Set("pane", "w-coord:p1")
	return s, task, ctx
}

func TestAskStructuredValidatesAndPersistsChoices(t *testing.T) {
	s := testStore(t)
	decision := &StructuredDecision{Choices: []Choice{{ID: "keep", Label: "Keep the change"}, {ID: "revert", Label: "Revert it"}}, Recommended: "keep", Rationale: "The evidence supports keeping it.", Sources: []string{"docs/review.md#evidence"}}
	view, err := AskStructured(s, "t-aaaaaaaaaaaa", "deploy", "Which action should I take?", decision, func() (*ordjson.Object, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	question, _ := view.Get("question")
	row := question.(*ordjson.Object)
	if got, _ := row.Get("recommended"); got != "keep" {
		t.Fatalf("recommended = %v", got)
	}
	if got, _ := row.Get("rationale"); got != decision.Rationale {
		t.Fatalf("rationale = %v", got)
	}
	if got, _ := row.Get("text"); got != "Which action should I take?" {
		t.Fatalf("text = %v", got)
	}
}

func TestAskStructuredPersistsExactDecisionFields(t *testing.T) {
	s := testStore(t)
	decision := &StructuredDecision{Choices: []Choice{{ID: "keep", Label: "Keep the change"}, {ID: "revert", Label: "Revert it"}}, Recommended: "keep", Rationale: "The evidence supports keeping it.", Sources: []string{"docs/review.md#evidence"}}
	if _, err := AskStructured(s, "t-aaaaaaaaaaaa", "deploy", "Which action should I take?", decision, func() (*ordjson.Object, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	saved, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	questions, _ := saved.Get("questions")
	question := questions.([]any)[0].(*ordjson.Object)
	choices, _ := question.Get("choices")
	if len(choices.([]any)) != 2 {
		t.Fatalf("persisted choices = %v", choices)
	}
	first := choices.([]any)[0].(*ordjson.Object)
	if id, _ := first.Get("id"); id != "keep" {
		t.Fatalf("persisted first choice ID = %v", id)
	}
	if label, _ := first.Get("label"); label != "Keep the change" {
		t.Fatalf("persisted first choice label = %v", label)
	}
	if sources, _ := question.Get("sources"); len(sources.([]any)) != 1 || sources.([]any)[0] != decision.Sources[0] {
		t.Fatalf("persisted sources = %v", sources)
	}
}

func TestAskStillPersistsLegacyTextOnlyQuestions(t *testing.T) {
	s := testStore(t)
	view, err := Ask(s, "t-aaaaaaaaaaaa", "legacy", "Continue?", func() (*ordjson.Object, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	question, _ := view.Get("question")
	row := question.(*ordjson.Object)
	if text, _ := row.Get("text"); text != "Continue?" {
		t.Fatalf("text = %v", text)
	}
	if _, ok := row.Get("choices"); ok {
		t.Fatal("legacy question unexpectedly has structured choices")
	}
}

func TestAskStructuredRejectsInvalidChoices(t *testing.T) {
	tests := []struct {
		name     string
		decision StructuredDecision
	}{
		{"too few choices", StructuredDecision{Choices: []Choice{{ID: "a", Label: "A"}}, Recommended: "a", Rationale: "Reason"}},
		{"too many choices", StructuredDecision{Choices: []Choice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}, {ID: "c", Label: "C"}, {ID: "d", Label: "D"}, {ID: "e", Label: "E"}, {ID: "f", Label: "F"}, {ID: "g", Label: "G"}, {ID: "h", Label: "H"}, {ID: "i", Label: "I"}}, Recommended: "a", Rationale: "Reason"}},
		{"invalid ID format", StructuredDecision{Choices: []Choice{{ID: "not an id", Label: "A"}, {ID: "b", Label: "B"}}, Recommended: "b", Rationale: "Reason"}},
		{"duplicate IDs", StructuredDecision{Choices: []Choice{{ID: "same", Label: "A"}, {ID: "same", Label: "B"}}, Recommended: "same", Rationale: "Reason"}},
		{"oversized label", StructuredDecision{Choices: []Choice{{ID: "a", Label: strings.Repeat("l", 121)}, {ID: "b", Label: "B"}}, Recommended: "b", Rationale: "Reason"}},
		{"missing recommendation", StructuredDecision{Choices: []Choice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}, Recommended: "c", Rationale: "Reason"}},
		{"missing rationale", StructuredDecision{Choices: []Choice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}, Recommended: "a"}},
		{"oversized rationale", StructuredDecision{Choices: []Choice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}, Recommended: "a", Rationale: strings.Repeat("r", 281)}},
		{"oversized source", StructuredDecision{Choices: []Choice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}, Recommended: "a", Rationale: "Reason", Sources: []string{strings.Repeat("s", 501)}}},
		{"too many sources", StructuredDecision{Choices: []Choice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}, Recommended: "a", Rationale: "Reason", Sources: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decisionRecord(&test.decision); err == nil {
				t.Fatal("expected invalid structured decision to fail")
			}
		})
	}
}

func TestAskStructuredRejectsInvalidDecisionAtOperationBoundary(t *testing.T) {
	s := testStore(t)
	decision := &StructuredDecision{Choices: []Choice{{ID: "not an id", Label: "A"}, {ID: "b", Label: "B"}}, Recommended: "b", Rationale: "Reason"}
	pumps := 0
	if _, err := AskStructured(s, "t-aaaaaaaaaaaa", "invalid", "Choose an option.", decision, func() (*ordjson.Object, error) { pumps++; return nil, nil }); err == nil {
		t.Fatal("expected invalid decision to be rejected by AskStructured")
	}
	if pumps != 0 {
		t.Fatalf("invalid decision produced %d notifications", pumps)
	}
	saved, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	questions, _ := saved.Get("questions")
	if len(questions.([]any)) != 0 {
		t.Fatalf("invalid decision persisted as %v", questions)
	}
}

func TestAskStructuredKeyedRetryAndChangedContent(t *testing.T) {
	s := testStore(t)
	pumps := 0
	pump := func() (*ordjson.Object, error) {
		pumps++
		return nil, nil
	}
	decision := &StructuredDecision{Choices: []Choice{{ID: "a", Label: "Option A"}, {ID: "b", Label: "Option B"}}, Recommended: "a", Rationale: "A fits the evidence."}
	if _, err := AskStructured(s, "t-aaaaaaaaaaaa", "route", "Choose a route.", decision, pump); err != nil {
		t.Fatal(err)
	}
	retry, err := AskStructured(s, "t-aaaaaaaaaaaa", "route", "Choose a route.", decision, pump)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, _ := retry.Get("duplicate"); duplicate != true || pumps != 1 {
		t.Fatalf("retry duplicate=%v pumps=%d", duplicate, pumps)
	}
	changed := *decision
	changed.Recommended = "b"
	if _, err := AskStructured(s, "t-aaaaaaaaaaaa", "route", "Choose a route.", &changed, pump); err == nil {
		t.Fatal("expected changed keyed content to fail")
	}
	if pumps != 1 {
		t.Fatalf("changed retry pumped %d times", pumps)
	}
}

func TestAnswerExpectedRejectsChangedRecipientAndAppliesChoice(t *testing.T) {
	s, task, ctx := registeredWorker(t)
	expected, err := ExpectedWorkerRecipient(s, task)
	if err != nil || expected == "" {
		t.Fatalf("recipient identity = %q, err=%v", expected, err)
	}
	host, _ := s.Machine()
	worker := ordjson.NewObject()
	worker.Set("terminal", "worker-two")
	if _, err := s.Register(store.Endpoint{Machine: host.ID, Session: "worker-session", Pane: "w-worker:p1"}, "worker", "t-aaaaaaaaaaaa", worker); err != nil {
		t.Fatal(err)
	}
	pumps := 0
	pump := func() (*ordjson.Object, error) { pumps++; return nil, nil }
	if _, err := AnswerExpected(s, "t-aaaaaaaaaaaa", "q-one", "", "keep", expected, ctx, pump); err == nil {
		t.Fatal("expected stale worker identity to be rejected")
	}
	if pumps != 0 {
		t.Fatalf("stale action pumped %d times", pumps)
	}
	current, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	currentExpected, err := ExpectedWorkerRecipient(s, current)
	if err != nil {
		t.Fatal(err)
	}
	result, err := AnswerExpected(s, "t-aaaaaaaaaaaa", "q-one", "", "keep", currentExpected, ctx, pump)
	if err != nil {
		t.Fatal(err)
	}
	question, _ := result.Get("question")
	if status, _ := question.(*ordjson.Object).Get("status"); status != "answered" {
		t.Fatalf("status = %v", status)
	}
	if choice, _ := question.(*ordjson.Object).Get("answer_choice"); choice != "keep" {
		t.Fatalf("answer_choice = %v", choice)
	}
	if answer, _ := question.(*ordjson.Object).Get("answer"); answer != "Keep the change" {
		t.Fatalf("answer = %v", answer)
	}
	if pumps != 1 {
		t.Fatalf("pumps = %d", pumps)
	}
	duplicate, err := AnswerExpected(s, "t-aaaaaaaaaaaa", "q-one", "", "keep", currentExpected, ctx, pump)
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := duplicate.Get("duplicate"); value != true {
		t.Fatalf("same choice retry duplicate = %v", value)
	}
	if _, err := AnswerExpected(s, "t-aaaaaaaaaaaa", "q-one", "", "revert", currentExpected, ctx, pump); err == nil {
		t.Fatal("expected a different choice after answering to be rejected")
	}
	if pumps != 1 {
		t.Fatalf("retries changed notification count to %d", pumps)
	}
}

func TestAnswerExpectedRequiresTaskCoordinator(t *testing.T) {
	s, task, _ := registeredWorker(t)
	expected, err := ExpectedWorkerRecipient(s, task)
	if err != nil {
		t.Fatal(err)
	}
	host, _ := s.Machine()
	stranger := ordjson.NewObject()
	stranger.Set("machine", host.ID)
	stranger.Set("session", "other-session")
	stranger.Set("pane", "w-other:p1")
	pumps := 0
	if _, err := AnswerExpected(s, "t-aaaaaaaaaaaa", "q-one", "", "keep", expected, stranger, func() (*ordjson.Object, error) { pumps++; return nil, nil }); err == nil {
		t.Fatal("expected non-coordinator answer to be rejected")
	}
	if pumps != 0 {
		t.Fatalf("unauthorized answer produced %d notifications", pumps)
	}
	saved, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := findQuestion(saved, "q-one").Get("status"); status != "open" {
		t.Fatalf("unauthorized answer changed status to %v", status)
	}
}

func TestAnswerExpectedPersistsBeforeNotificationFailure(t *testing.T) {
	s, task, ctx := registeredWorker(t)
	expected, err := ExpectedWorkerRecipient(s, task)
	if err != nil {
		t.Fatal(err)
	}
	_, err = AnswerExpected(s, "t-aaaaaaaaaaaa", "q-one", "Custom answer", "", expected, ctx, func() (*ordjson.Object, error) { return nil, errNotification })
	if err == nil {
		t.Fatal("expected notification failure")
	}
	saved, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	question := findQuestion(saved, "q-one")
	if status, _ := question.Get("status"); status != "answered" {
		t.Fatalf("status after notification failure = %v", status)
	}
	if answer, _ := question.Get("answer"); answer != "Custom answer" {
		t.Fatalf("answer after notification failure = %v", answer)
	}
}

func TestAnswerExpectedRejectsWrongTaskAndUnavailableChoice(t *testing.T) {
	s, task, ctx := registeredWorker(t)
	expected, err := ExpectedWorkerRecipient(s, task)
	if err != nil {
		t.Fatal(err)
	}
	wrongTask, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	wrongTask.Set("id", "t-bbbbbbbbbbbb")
	if err := s.SaveTask(wrongTask); err != nil {
		t.Fatal(err)
	}
	pumps := 0
	pump := func() (*ordjson.Object, error) { pumps++; return nil, nil }
	if _, err := AnswerExpected(s, "t-bbbbbbbbbbbb", "q-one", "Custom answer", "", expected, ctx, pump); err == nil {
		t.Fatal("expected captured task identity to be rejected")
	}
	if _, err := AnswerExpected(s, "t-aaaaaaaaaaaa", "q-one", "", "missing", expected, ctx, pump); err == nil {
		t.Fatal("expected unavailable choice to be rejected")
	}
	if pumps != 0 {
		t.Fatalf("invalid actions pumped %d times", pumps)
	}
	saved, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := findQuestion(saved, "q-one").Get("status"); status != "open" {
		t.Fatalf("invalid action changed status to %v", status)
	}
}

func TestExplainIsDistinctIdempotentAndLeavesQuestionOpen(t *testing.T) {
	s, task, _ := registeredWorker(t)
	recipient, err := ExpectedWorkerRecipient(s, task)
	if err != nil {
		t.Fatal(err)
	}
	pumps := 0
	pump := func() (*ordjson.Object, error) { pumps++; return nil, nil }
	if _, err := Explain(s, "t-aaaaaaaaaaaa", "q-one", "Stale request.", "wr-old", pump); err == nil {
		t.Fatal("expected stale Explain recipient to be rejected")
	}
	first, err := Explain(s, "t-aaaaaaaaaaaa", "q-one", "Explain the tradeoffs.", recipient, pump)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Explain(s, "t-aaaaaaaaaaaa", "q-one", "Explain the tradeoffs.", recipient, pump); err != nil {
		t.Fatal(err)
	}
	if _, err := Explain(s, "t-aaaaaaaaaaaa", "q-one", "Also explain the rollback cost.", recipient, pump); err != nil {
		t.Fatal(err)
	}
	question, _ := first.Get("question")
	status, _ := question.(*ordjson.Object).Get("status")
	answer, _ := question.(*ordjson.Object).Get("answer")
	if status != "open" || answer != nil {
		t.Fatalf("Explain changed decision state: status=%v answer=%v", status, answer)
	}
	if pumps != 2 {
		t.Fatalf("duplicate/new explanations produced %d notifications", pumps)
	}
	if _, err := Explain(s, "t-aaaaaaaaaaaa", "q-one", "Explain persistence.", recipient, func() (*ordjson.Object, error) { return nil, errNotification }); err == nil {
		t.Fatal("expected notification failure")
	}
	saved, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	stored := findQuestion(saved, "q-one")
	clarifications, _ := stored.Get("clarifications")
	if len(clarifications.([]any)) != 3 {
		t.Fatalf("saved clarifications = %v", clarifications)
	}
}

func TestExplainRejectsAnsweredQuestionBeforeWritingOrNotifying(t *testing.T) {
	s, task, _ := registeredWorker(t)
	recipient, err := ExpectedWorkerRecipient(s, task)
	if err != nil {
		t.Fatal(err)
	}
	question := findQuestion(task, "q-one")
	question.Set("status", "answered")
	question.Set("answer", "Keep the change")
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	pumps := 0
	if _, err := Explain(s, "t-aaaaaaaaaaaa", "q-one", "Explain the tradeoffs.", recipient, func() (*ordjson.Object, error) { pumps++; return nil, nil }); err == nil {
		t.Fatal("expected Explain on an answered question to be rejected")
	}
	if pumps != 0 {
		t.Fatalf("answered question produced %d notifications", pumps)
	}
	saved, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findQuestion(saved, "q-one").Get("clarifications"); ok {
		t.Fatal("answered question recorded a clarification")
	}
}

var errNotification = &notificationError{}

type notificationError struct{}

func (*notificationError) Error() string { return "notification failed" }
