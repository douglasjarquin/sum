package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ask"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func decisionActionLab(t *testing.T) (string, *store.Store, *ordjson.Object, string) {
	t.Helper()
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
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
	endpoint := store.Endpoint{Machine: host, Session: "sum-test", Pane: "w-worker:p1"}
	workerIncarnation := incarnation.Evidence{Terminal: "term-worker", Agent: "claude"}.Record(store.Now())
	if _, err := s.Register(endpoint, "worker", "t-aaaaaaaaaaaa", workerIncarnation); err != nil {
		t.Fatal(err)
	}
	herdrStatePath := filepath.Join(os.Getenv("FAKE_HERDR_ROOT"), "state.json")
	stateBytes, err := os.ReadFile(herdrStatePath)
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]any{}
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatal(err)
	}
	panes := state["panes"].(map[string]any)
	panes[endpoint.Pane] = map[string]any{"pane_id": endpoint.Pane, "cwd": home, "workspace_id": "w-worker", "agent_status": "idle", "agent": "claude", "agent_pid": 4242, "terminal_id": "term-worker", "created": true}
	stateBytes, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(herdrStatePath, stateBytes, 0o600); err != nil {
		t.Fatal(err)
	}
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
			t.Setenv("FAKE_PS_STARTS", `{"4242":"2030-01-01T00:00:00Z"}`)
			path := filepath.Join(os.Getenv("FAKE_HERDR_ROOT"), "state.json")
			stateBytes, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			state := map[string]any{}
			if err := json.Unmarshal(stateBytes, &state); err != nil {
				t.Fatal(err)
			}
			panes := state["panes"].(map[string]any)
			worker := panes["w-worker:p1"].(map[string]any)
			worker["terminal_id"] = "term-replaced"
			stateBytes, err = json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, stateBytes, 0o600); err != nil {
				t.Fatal(err)
			}
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
