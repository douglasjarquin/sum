package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// closeLab registers this pane as coordinator and writes one task whose worker
// attempt is in workerState. Its questions are an answered one (q-aaaaaaaaaa),
// an open one (q-bbbbbbbbbb), and an answered one (q-cccccccccc) for the worker
// to apply. A workerState of "" writes a legacy task with no execution record.
func closeLab(t *testing.T, workerState string) string {
	t.Helper()
	home := writeDesignatedHome(t)
	nativeEnv(t, home)
	if _, err := runCLI(t, home, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}

	s, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", json.Number("2"))
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("status", "waiting")
	task.Set("repository", "owner/repo")
	owner, err := s.Owner()
	if err != nil {
		t.Fatal(err)
	}
	task.Set("parent", owner)
	questions, err := ordjson.Decode([]byte(`[
 {"id":"q-aaaaaaaaaa","key":"gone","status":"answered","text":"keep going?","answer":"yes","created_at":"2026-01-01T00:00:00+00:00","answered_at":"2026-01-02T00:00:00+00:00"},
 {"id":"q-bbbbbbbbbb","key":"unanswered","status":"open","text":"which one?","answer":null,"created_at":"2026-01-01T00:00:00+00:00"},
 {"id":"q-cccccccccc","key":"applied","status":"answered","text":"rename it?","answer":"no","created_at":"2026-01-01T00:00:00+00:00","answered_at":"2026-01-02T00:00:00+00:00"}]`))
	if err != nil {
		t.Fatal(err)
	}
	task.Set("questions", questions)
	task.Set("evidence", []any{})
	task.Set("brief", "do the thing")
	task.Set("kind", "ship")
	task.Set("base_sha", strings.Repeat("a", 40))
	if workerState != "" {
		seedNativeWorker(t, nativeLabFor(t, home), s, task)
		worker, err := reservations.Worker(task)
		if err != nil {
			t.Fatal(err)
		}
		worker.Set("state", workerState)
	}
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}

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
	home := closeLab(t, "running")
	closeApplyThenRelease(t, home)
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
	home := closeLab(t, "running")
	closeApplyThenRelease(t, home)
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
	if closed["answer"] != "yes" || closed["close_reason"] != "worker pane is gone" || closed["closed_attempt"] != closeAttempt(t, home) {
		t.Fatalf("closed record = %v", closed)
	}
	if at, _ := closed["closed_at"].(string); at == "" {
		t.Fatalf("closed_at = %v", closed["closed_at"])
	}
	by, _ := closed["closed_by"].(map[string]any)
	if by["role"] != "coordinator" || by["address"] != "t1" || by["core_id"] != os.Getenv("SUM_CORE_ID") || by["attempt"] != "bootstrap" || by["created_ms"] == nil || by["binding_generation"] == nil || by["session"] != "sum-test" {
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
	beforeResolve := readNamedTask(t, home, "t-aaaaaaaaaaaa")
	refused := nativeLabFor(t, home).ctlPane("t2", false, "resolve", "t-aaaaaaaaaaaa", "q-aaaaaaaaaa")
	if asString(refused["error"]) == "" {
		t.Fatalf("worker resolve applied a closed answer: %v", refused)
	}
	if readNamedTask(t, home, "t-aaaaaaaaaaaa") != beforeResolve {
		t.Fatal("refused worker application rewrote the task")
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
	t.Setenv("SUM_ROLE", "worker")
	t.Setenv("SUM_TASK", "t-aaaaaaaaaaaa")
	t.Setenv("SUM_ATTEMPT", "not-the-coordinator")
	_, err := closeQuestion(t, home, "q-aaaaaaaaaa", "worker pane is gone")
	if err == nil || !strings.Contains(err.Error(), "session registration identity mismatch") {
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

func closeAttempt(t *testing.T, home string) string {
	t.Helper()
	s, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	worker, err := reservations.Worker(task)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := worker.Get("id")
	return id.(string)
}
func closeApplyThenRelease(t *testing.T, home string) {
	t.Helper()
	d := nativeLabFor(t, home)
	s, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	value, _ := task.Get("worker_endpoint")
	endpoint := store.EndpointFromContext(value.(*ordjson.Object))
	d.ctlPane(endpoint.Address, true, "resolve", "t-aaaaaaaaaaaa", "q-cccccccccc")
	task, err = s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	worker, err := reservations.Worker(task)
	if err != nil {
		t.Fatal(err)
	}
	worker.Set("state", "released")
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
}
