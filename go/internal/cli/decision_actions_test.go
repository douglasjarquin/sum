package cli

import (
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ask"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func decisionActionLab(t *testing.T) (string, *store.Store, *ordjson.Object, string) {
	t.Helper()
	home := writeDesignatedHome(t)
	nativeEnv(t, home)
	if out, err := runCLI(t, home, "init"); err != nil {
		t.Fatalf("init coordinator: %v\n%s", err, out)
	}
	s, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	host, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := store.Context(home)
	if err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", 1)
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("status", "running")
	task.Set("repository", "owner/repo")
	task.Set("machine", host)
	task.Set("session", "sum-test")
	task.Set("pane", "w-worker:p1")
	task.Set("worktree", home)
	task.Set("parent", ctx)
	task.Set("questions", []any{func() *ordjson.Object {
		question := ordjson.NewObject()
		question.Set("id", "q-one")
		question.Set("key", "route")
		question.Set("text", "Choose a route.")
		question.Set("status", "open")
		question.Set("answer", nil)
		question.Set("choices", []any{func() *ordjson.Object {
			choice := ordjson.NewObject()
			choice.Set("id", "keep")
			choice.Set("label", "Keep the change")
			return choice
		}(), func() *ordjson.Object {
			choice := ordjson.NewObject()
			choice.Set("id", "revert")
			choice.Set("label", "Revert it")
			return choice
		}()})
		return question
	}()})
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	seedNativeWorker(t, nativeLabFor(t, home), s, task)
	recipient, err := ask.ExpectedWorkerRecipient(s, task)
	if err != nil {
		t.Fatal(err)
	}
	return home, s, task, recipient
}

func TestAnswerChoiceCommandRecordsListedChoice(t *testing.T) {
	home, s, _, recipient := decisionActionLab(t)
	out, err := runCLI(t, home, "answer", "t-aaaaaaaaaaaa", "q-one", "--choice", "keep", "--expected-recipient", recipient)
	if err != nil {
		t.Fatalf("answer listed choice: %v\n%s", err, out)
	}
	saved, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	question, _ := saved.Get("questions")
	row := question.([]any)[0].(*ordjson.Object)
	if choice, _ := row.Get("answer_choice"); choice != "keep" {
		t.Fatalf("answer_choice = %v", choice)
	}
	if answer, _ := row.Get("answer"); answer != "Keep the change" {
		t.Fatalf("answer = %v", answer)
	}
}

func TestExplainCommandLeavesQuestionOpen(t *testing.T) {
	home, s, _, recipient := decisionActionLab(t)
	out, err := runCLI(t, home, "explain", "t-aaaaaaaaaaaa", "q-one", "--text", "Explain the tradeoffs.", "--expected-recipient", recipient)
	if err != nil {
		t.Fatalf("request Explain: %v\n%s", err, out)
	}
	saved, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	question, _ := saved.Get("questions")
	row := question.([]any)[0].(*ordjson.Object)
	if status, _ := row.Get("status"); status != "open" {
		t.Fatalf("status = %v", status)
	}
	if answer, _ := row.Get("answer"); answer != nil {
		t.Fatalf("answer = %v", answer)
	}
	clarifications, _ := row.Get("clarifications")
	if len(clarifications.([]any)) != 1 {
		t.Fatalf("clarifications = %v", clarifications)
	}
}

func TestDecisionCommandsRejectReplacedWorkerBeforeWriting(t *testing.T) {
	cases := []struct {
		name string
		args func(string) []string
	}{
		{"answer", func(recipient string) []string {
			return []string{"answer", "t-aaaaaaaaaaaa", "q-one", "--choice", "keep", "--expected-recipient", recipient}
		}},
		{"explain", func(recipient string) []string {
			return []string{"explain", "t-aaaaaaaaaaaa", "q-one", "--text", "Explain the tradeoffs.", "--expected-recipient", recipient}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, s, _, recipient := decisionActionLab(t)
			nativeLabFor(t, home).native.SetForeground("t2", hcclient.Foreground{PID: 300001, Name: "codex", Argv: []string{"codex"}, Cwd: home})
			out, err := runCLI(t, home, tc.args(recipient)...)
			if err == nil {
				t.Fatalf("%s action accepted a replaced worker: %s", tc.name, out)
			}
			saved, err := s.ReadTask("t-aaaaaaaaaaaa")
			if err != nil {
				t.Fatal(err)
			}
			questions, _ := saved.Get("questions")
			question := questions.([]any)[0].(*ordjson.Object)
			if status, _ := question.Get("status"); status != "open" {
				t.Fatalf("status after refused %s = %v", tc.name, status)
			}
			if answer, _ := question.Get("answer"); answer != nil {
				t.Fatalf("answer after refused %s = %v", tc.name, answer)
			}
			if _, ok := question.Get("clarifications"); ok {
				t.Fatalf("refused %s recorded a clarification", tc.name)
			}
		})
	}
}
