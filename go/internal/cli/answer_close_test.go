package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/machine"
)

// closeLab registers this pane as coordinator and writes one task whose worker
// attempt is in workerState. Its questions are an answered one (q-aaaaaaaaaa),
// an open one (q-bbbbbbbbbb), and an answered one (q-cccccccccc) for the worker
// to apply. A workerState of "" writes a legacy task with no execution record.
func closeLab(t *testing.T, workerState string) string {
	t.Helper()
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	if _, err := runCLI(t, home, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	host, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	execution := ""
	if workerState != "" {
		execution = fmt.Sprintf(`,
"execution": {"schema": 1,
  "worker": {"id": "x-aaaaaaaaaaaa", "kind": "worker", "state": %q, "generation": 1,
             "owner": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1"}, "checkout": "/tmp/x",
             "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": []},
  "verifiers": []}`, workerState, host)
	}
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", fmt.Sprintf(`{
"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "waiting", "repository": "owner/repo",
"machine": %q, "session": "sum-test", "pane": "w-worker:p1",
"parent": {"machine": %q, "session": "sum-test", "pane": "w-parent:p1"},
"questions": [
  {"id": "q-aaaaaaaaaa", "key": "gone", "status": "answered", "text": "keep going?", "answer": "yes", "created_at": "2026-01-01T00:00:00+00:00", "answered_at": "2026-01-02T00:00:00+00:00"},
  {"id": "q-bbbbbbbbbb", "key": "unanswered", "status": "open", "text": "which one?", "answer": null, "created_at": "2026-01-01T00:00:00+00:00"},
  {"id": "q-cccccccccc", "key": "applied", "status": "answered", "text": "rename it?", "answer": "no", "created_at": "2026-01-01T00:00:00+00:00", "answered_at": "2026-01-02T00:00:00+00:00"}
],
"evidence": [], "report": {"text": "done"}, "notice": null, "attention": [],
"brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship"%s
}`, host, host, execution))
	return home
}

func closeQuestion(t *testing.T, home, questionID, reason string) (string, error) {
	t.Helper()
	return runCLI(t, home, "answer", "t-aaaaaaaaaaaa", questionID, "--close", "--reason", reason)
}

func questionRecord(t *testing.T, home, questionID string) map[string]any {
	t.Helper()
	var task map[string]any
	if err := json.Unmarshal([]byte(readNamedTask(t, home, "t-aaaaaaaaaaaa")), &task); err != nil {
		t.Fatal(err)
	}
	questions, _ := task["questions"].([]any)
	for _, raw := range questions {
		q, _ := raw.(map[string]any)
		if q["id"] == questionID {
			return q
		}
	}
	t.Fatalf("question %s not found", questionID)
	return nil
}

func TestAnswerClose_releasedAttemptLetsArchiveSucceed(t *testing.T) {
	home := closeLab(t, "released")
	if _, err := runCLI(t, home, "resolve", "t-aaaaaaaaaaaa", "q-cccccccccc"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if out, err := runCLI(t, home, "archive", "t-aaaaaaaaaaaa", "--acknowledge"); err == nil || !strings.Contains(err.Error(), "Outstanding questions") {
		t.Fatalf("archive before closing: err=%v out=%s", err, out)
	}
	if out, err := closeQuestion(t, home, "q-aaaaaaaaaa", "worker pane is gone; the user superseded the answer"); err != nil {
		t.Fatalf("close: %v\n%s", err, out)
	}
	// q-bbbbbbbbbb is still open; the user answers it, then no worker exists to apply it.
	if out, err := runCLI(t, home, "answer", "t-aaaaaaaaaaaa", "q-bbbbbbbbbb", "--text", "the first one"); err != nil {
		t.Fatalf("answer: %v\n%s", err, out)
	}
	if out, err := closeQuestion(t, home, "q-bbbbbbbbbb", "answered after the worker was parked"); err != nil {
		t.Fatalf("close second: %v\n%s", err, out)
	}
	out, err := runCLI(t, home, "archive", "t-aaaaaaaaaaaa", "--acknowledge")
	if err != nil {
		t.Fatalf("archive after closing: %v\n%s", err, out)
	}
	if got := decodeObject(t, out)["archived"]; got != "t-aaaaaaaaaaaa" {
		t.Fatalf("archived = %v", got)
	}
}

func TestAnswerClose_recordDistinguishesCoordinatorClosureFromWorkerApplication(t *testing.T) {
	home := closeLab(t, "released")
	if _, err := runCLI(t, home, "resolve", "t-aaaaaaaaaaaa", "q-cccccccccc"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if out, err := closeQuestion(t, home, "q-aaaaaaaaaa", "worker pane is gone"); err != nil {
		t.Fatalf("close: %v\n%s", err, out)
	}
	closed := questionRecord(t, home, "q-aaaaaaaaaa")
	if closed["status"] != "closed-unapplied" {
		t.Fatalf("closed status = %v", closed["status"])
	}
	if _, ok := closed["applied_at"]; ok {
		t.Fatalf("closed question carries applied_at: %v", closed)
	}
	if closed["answer"] != "yes" || closed["close_reason"] != "worker pane is gone" || closed["closed_attempt"] != "x-aaaaaaaaaaaa" {
		t.Fatalf("closed record = %v", closed)
	}
	if at, _ := closed["closed_at"].(string); at == "" {
		t.Fatalf("closed_at = %v", closed["closed_at"])
	}
	by, _ := closed["closed_by"].(map[string]any)
	if by["role"] != "coordinator" || by["pane"] != "w-parent:p1" || by["session"] != "sum-test" {
		t.Fatalf("closed_by = %v", closed["closed_by"])
	}
	applied := questionRecord(t, home, "q-cccccccccc")
	if applied["status"] != "applied" || applied["closed_by"] != nil || applied["close_reason"] != nil {
		t.Fatalf("worker-applied record = %v", applied)
	}
	if at, _ := applied["applied_at"].(string); at == "" {
		t.Fatalf("applied_at = %v", applied["applied_at"])
	}
	// A worker can never turn a coordinator closure into its own application.
	if out, err := runCLI(t, home, "resolve", "t-aaaaaaaaaaaa", "q-aaaaaaaaaa"); err == nil {
		t.Fatalf("resolve applied a closed answer\n%s", out)
	}
	if got := questionRecord(t, home, "q-aaaaaaaaaa")["status"]; got != "closed-unapplied" {
		t.Fatalf("status after resolve attempt = %v", got)
	}
	// Repeating the same closure is idempotent; a different reason changes nothing.
	if out, err := closeQuestion(t, home, "q-aaaaaaaaaa", "worker pane is gone"); err != nil || decodeObject(t, out)["duplicate"] != true {
		t.Fatalf("repeat close: err=%v out=%s", err, out)
	}
	if _, err := closeQuestion(t, home, "q-aaaaaaaaaa", "another reason"); err == nil {
		t.Fatal("close with a different reason rewrote the record")
	}
	if got := questionRecord(t, home, "q-aaaaaaaaaa")["close_reason"]; got != "worker pane is gone" {
		t.Fatalf("close_reason = %v", got)
	}
}

func TestAnswerClose_refusesLiveOrUnprovenAttempt(t *testing.T) {
	for _, state := range []string{"held", "running", "uncertain", "starting", "observing", ""} {
		name := state
		if name == "" {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			home := closeLab(t, state)
			before := readNamedTask(t, home, "t-aaaaaaaaaaaa")
			out, err := closeQuestion(t, home, "q-aaaaaaaaaa", "worker looks gone")
			if err == nil {
				t.Fatalf("close accepted a %s attempt\n%s", name, out)
			}
			if !strings.Contains(err.Error(), "execution park") {
				t.Fatalf("err = %v, want the park hint", err)
			}
			if got := readNamedTask(t, home, "t-aaaaaaaaaaaa"); got != before {
				t.Fatalf("task changed:\n%s", got)
			}
		})
	}
}

func TestAnswerClose_refusesUnansweredQuestion(t *testing.T) {
	home := closeLab(t, "released")
	before := readNamedTask(t, home, "t-aaaaaaaaaaaa")
	_, err := closeQuestion(t, home, "q-bbbbbbbbbb", "nobody will answer")
	if err == nil || !strings.Contains(err.Error(), "needs the user's answer") {
		t.Fatalf("err = %v", err)
	}
	if got := readNamedTask(t, home, "t-aaaaaaaaaaaa"); got != before {
		t.Fatalf("task changed:\n%s", got)
	}
}

func TestAnswerClose_refusesNonCoordinatorPane(t *testing.T) {
	home := closeLab(t, "released")
	before := readNamedTask(t, home, "t-aaaaaaaaaaaa")
	t.Setenv("HERDR_PANE_ID", "w-worker:p1")
	_, err := closeQuestion(t, home, "q-aaaaaaaaaa", "worker pane is gone")
	if err == nil || !strings.Contains(err.Error(), "not the registered coordinator") {
		t.Fatalf("err = %v", err)
	}
	if got := readNamedTask(t, home, "t-aaaaaaaaaaaa"); got != before {
		t.Fatalf("task changed:\n%s", got)
	}
}

func TestAnswerClose_flagsAreUsageErrors(t *testing.T) {
	cases := [][]string{
		{"answer", "t-aaaaaaaaaaaa", "q-aaaaaaaaaa", "--close"},
		{"answer", "t-aaaaaaaaaaaa", "q-aaaaaaaaaa", "--reason", "gone"},
		{"answer", "t-aaaaaaaaaaaa", "q-aaaaaaaaaa", "--close", "--reason", "gone", "--text", "yes"},
		{"answer", "t-aaaaaaaaaaaa", "q-aaaaaaaaaa", "--text", "yes", "--reason", "gone"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args[3:], " "), func(t *testing.T) {
			home := closeLab(t, "released")
			before := readNamedTask(t, home, "t-aaaaaaaaaaaa")
			_, err := runCLI(t, home, args...)
			assertNativeUsageError(t, err)
			if got := readNamedTask(t, home, "t-aaaaaaaaaaaa"); got != before {
				t.Fatalf("task changed:\n%s", got)
			}
		})
	}
}
