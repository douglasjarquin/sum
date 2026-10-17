package notes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func fixture(t *testing.T) (*store.Store, *ordjson.Object, *ordjson.Object, *ordjson.Object) {
	t.Helper()
	home := t.TempDir()
	s, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	worker := endpoint(host.ID, "worker")
	coordinator := endpoint(host.ID, "parent")
	task := ordjson.NewObject()
	task.Set("schema", json.Number("1"))
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("created_at", store.Now())
	task.Set("status", "running")
	task.Set("machine", host.ID)
	task.Set("pane", "worker")
	task.Set("session", "lab")
	task.Set("parent", coordinator)
	task.Set("project", projectFixture())
	task.Set("brief", "approved scope")
	if err := os.MkdirAll(filepath.Join(home, "tasks", "t-aaaaaaaaaaaa"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	return s, task, worker, coordinator
}

func endpoint(machineID, pane string) *ordjson.Object {
	obj := ordjson.NewObject()
	obj.Set("machine", machineID)
	obj.Set("session", "lab")
	obj.Set("pane", pane)
	return obj
}

func projectFixture() *ordjson.Object {
	obj := ordjson.NewObject()
	obj.Set("host", "github.com")
	obj.Set("owner", "team")
	obj.Set("repo", "repo")
	return obj
}

func input() Input {
	return Input{Key: "missing-timeout", Title: "Timeout is unclear", Explanation: "The worker cannot tell whether the remote operation timed out.", NextStep: "Check the adapter timeout behavior.", References: []string{"go/internal/adapter/adapter.go"}, Attempt: "a-1", Candidate: "0123456789abcdef"}
}

func TestCaptureSameKeyIsIdempotentAndConflictIsRefused(t *testing.T) {
	s, _, worker, _ := fixture(t)
	in := input()
	in.References = nil
	first, duplicate, err := Capture(s, "t-aaaaaaaaaaaa", in, worker)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate {
		t.Fatal("first capture reported duplicate")
	}
	second, duplicate, err := Capture(s, "t-aaaaaaaaaaaa", in, worker)
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate || first.ID != second.ID || len(second.History) != 1 {
		t.Fatalf("duplicate result = %#v, duplicate=%v", second, duplicate)
	}
	conflict := in
	conflict.Title = "Different finding"
	if _, _, err := Capture(s, "t-aaaaaaaaaaaa", conflict, worker); err == nil {
		t.Fatal("conflicting content was accepted")
	}
	records, gaps := ReadAll(s)
	if len(gaps) != 0 || len(records) != 1 || records[0].ID != first.ID {
		t.Fatalf("records=%#v gaps=%#v", records, gaps)
	}
}

func TestConcurrentCaptureWithSameKeyCreatesOneRecord(t *testing.T) {
	s, _, worker, _ := fixture(t)
	const submissions = 8
	var group sync.WaitGroup
	group.Add(submissions)
	results := make(chan Record, submissions)
	errors := make(chan error, submissions)
	for i := 0; i < submissions; i++ {
		go func() {
			defer group.Done()
			concurrentStore, err := store.Open(s.Home)
			if err != nil {
				errors <- err
				return
			}
			record, _, err := Capture(concurrentStore, "t-aaaaaaaaaaaa", input(), worker)
			if err != nil {
				errors <- err
				return
			}
			results <- record
		}()
	}
	group.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	var id string
	for record := range results {
		if id == "" {
			id = record.ID
		}
		if record.ID != id {
			t.Fatalf("concurrent capture IDs differ: %s and %s", id, record.ID)
		}
	}
	records, gaps := ReadAll(s)
	if len(gaps) != 0 || len(records) != 1 {
		t.Fatalf("records=%#v gaps=%#v", records, gaps)
	}
}

func TestCaptureSurvivesSourceRemovalWithoutChangingLegacyNotes(t *testing.T) {
	s, _, worker, _ := fixture(t)
	taskPath := filepath.Join(s.Tasks, "t-aaaaaaaaaaaa")
	notes := filepath.Join(taskPath, "notes.md")
	before := []byte("legacy note bytes\n")
	if err := os.WriteFile(notes, before, 0o600); err != nil {
		t.Fatal(err)
	}
	record, _, err := Capture(s, "t-aaaaaaaaaaaa", input(), worker)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	task.Set("status", "archived")
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	archived, gaps := ReadTask(s, "t-aaaaaaaaaaaa")
	if len(gaps) != 0 || len(archived) != 1 || archived[0].ID != record.ID {
		t.Fatalf("archived source findings=%#v gaps=%#v", archived, gaps)
	}
	after, err := os.ReadFile(notes)
	if err != nil || string(after) != string(before) {
		t.Fatalf("legacy notes changed: %q, err=%v", after, err)
	}
	if err := os.RemoveAll(taskPath); err != nil {
		t.Fatal(err)
	}
	records, gaps := ReadAll(s)
	if len(gaps) != 0 || len(records) != 1 || records[0].TaskID != "t-aaaaaaaaaaaa" {
		t.Fatalf("records=%#v gaps=%#v", records, gaps)
	}
}

func TestDismissAndReopenPreserveDispositionHistory(t *testing.T) {
	s, task, worker, coordinator := fixture(t)
	question := ordjson.NewObject()
	question.Set("id", "q-aaaaaaaaaa")
	question.Set("status", "open")
	evidence := ordjson.NewObject()
	evidence.Set("kind", "review")
	evidence.Set("verdict", "needs_changes")
	task.Set("questions", []any{question})
	task.Set("evidence", []any{evidence})
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	record, _, err := Capture(s, "t-aaaaaaaaaaaa", input(), worker)
	if err != nil {
		t.Fatal(err)
	}
	dismissed, err := Change(s, record.ID, "dismiss", "Duplicate report", "", coordinator)
	if err != nil || dismissed.Disposition != "dismissed" {
		t.Fatalf("dismissed=%#v err=%v", dismissed, err)
	}
	reopened, err := Change(s, record.ID, "reopen", "New evidence appeared", "", coordinator)
	if err != nil || reopened.Disposition != "open" || len(reopened.History) != 3 || reopened.History[1].Action != "dismiss" || reopened.History[2].Action != "reopen" {
		t.Fatalf("reopened=%#v err=%v", reopened, err)
	}
	current, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	questions, _ := current.Get("questions")
	evidenceRows, _ := current.Get("evidence")
	if !reflect.DeepEqual(questions, taskField(t, task, "questions")) || !reflect.DeepEqual(evidenceRows, taskField(t, task, "evidence")) {
		t.Fatalf("optional finding changed task questions or evidence: questions=%v evidence=%v", questions, evidenceRows)
	}
}

func taskField(t *testing.T, task *ordjson.Object, key string) any {
	t.Helper()
	value, ok := task.Get(key)
	if !ok {
		t.Fatalf("task missing %s", key)
	}
	return value
}

func TestDispositionHistoryAndPromotionAreIdempotent(t *testing.T) {
	s, _, worker, coordinator := fixture(t)
	record, _, err := Capture(s, "t-aaaaaaaaaaaa", input(), worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Change(s, record.ID, "defer", "Revisit after current task", "", coordinator); err != nil {
		t.Fatal(err)
	}
	if _, err := Change(s, record.ID, "reopen", "New evidence", "", coordinator); err != nil {
		t.Fatal(err)
	}
	if _, err := Change(s, record.ID, "promote", "Approved follow-up", "t-bbbbbbbbbbbb", coordinator); err == nil {
		t.Fatal("promotion linked a missing task")
	}
	target := ordjson.NewObject()
	target.Set("schema", json.Number("1"))
	target.Set("id", "t-bbbbbbbbbbbb")
	target.Set("created_at", store.Now())
	target.Set("status", "running")
	target.Set("machine", taskMachine(t, s))
	target.Set("project", projectFixture())
	target.Set("brief", "approved target")
	if err := os.MkdirAll(filepath.Join(s.Tasks, "t-bbbbbbbbbbbb"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTask(target); err != nil {
		t.Fatal(err)
	}
	linked, err := Change(s, record.ID, "promote", "Approved follow-up", "t-bbbbbbbbbbbb", coordinator)
	if err != nil {
		t.Fatal(err)
	}
	if linked.LinkState != "handoff_outstanding" || linked.Disposition != "open" {
		t.Fatalf("linked status = %s/%s, want handoff_outstanding/open", linked.LinkState, linked.Disposition)
	}
	again, err := Change(s, record.ID, "promote", "Approved follow-up", "t-bbbbbbbbbbbb", coordinator)
	if err != nil {
		t.Fatal(err)
	}
	if len(linked.History) != 4 || len(again.History) != 4 || linked.LinkTask != "t-bbbbbbbbbbbb" {
		t.Fatalf("linked=%#v again=%#v", linked, again)
	}
	if _, err := Change(s, record.ID, "promote", "Other target", "t-cccccccccccc", coordinator); err == nil {
		t.Fatal("different target replaced the original link")
	}
	settled, err := Change(s, record.ID, "settle", "Inspected follow-up result", "", coordinator)
	if err != nil || settled.LinkState != "settled" || settled.Disposition != "resolved" {
		t.Fatalf("settled=%#v err=%v", settled, err)
	}
	if _, err := Change(s, record.ID, "promote", "Approved follow-up", "t-bbbbbbbbbbbb", coordinator); err != nil {
		t.Fatalf("repeated promotion after settlement: %v", err)
	}
	reopened, err := Change(s, record.ID, "reopen", "New independent work", "", coordinator)
	if err != nil || reopened.LinkTask != "" || reopened.LinkState != "none" {
		t.Fatalf("reopened=%#v err=%v", reopened, err)
	}
	relinked, err := Change(s, record.ID, "promote", "New approved follow-up", "t-bbbbbbbbbbbb", coordinator)
	if err != nil || len(relinked.History) != 7 {
		t.Fatalf("relinked=%#v err=%v", relinked, err)
	}
}

func TestOnlyAssignedWorkerOrCoordinatorCanCapture(t *testing.T) {
	s, _, _, coordinator := fixture(t)
	machineID, _ := coordinator.Get("machine")
	unrelated := endpoint(machineID.(string), "unrelated")
	if _, _, err := Capture(s, "t-aaaaaaaaaaaa", input(), unrelated); err == nil {
		t.Fatal("unrelated endpoint captured a finding")
	}
	if _, _, err := Capture(s, "t-aaaaaaaaaaaa", input(), coordinator); err != nil {
		t.Fatalf("coordinator capture: %v", err)
	}
}

func taskMachine(t *testing.T, s *store.Store) string {
	t.Helper()
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	return host.ID
}
