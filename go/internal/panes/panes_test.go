package panes

import (
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func TestClosed_recordsOnceAndMatchesCurrentPane(t *testing.T) {
	t.Setenv("SUM_NOW", "2026-01-02T00:00:00Z")
	task := ordjson.NewObject()
	task.Set("pane", "w-worker:p1")
	task.Set("id", "t-aaaaaaaaaaaa")
	reviewer := ordjson.NewObject()
	reviewer.Set("pane", "w-rev:p1")
	task.Set("reviewer", reviewer)
	if Closed(task, "w-worker:p1") || WorkerIsClosed(task) {
		t.Fatal("empty record is closed")
	}
	got := Append(task, "w-worker:p1", "worker", "codex", "report-submitted")
	if got == nil {
		t.Fatal("first close was ignored")
	}
	if !WorkerIsClosed(task) || ReviewerIsClosed(task) {
		t.Fatal("worker close leaked onto the reviewer")
	}
	if Append(task, "w-worker:p1", "worker", "codex", "report-submitted") != nil {
		t.Fatal("duplicate close rewrote the record")
	}
	if n := len(Entries(task)); n != 1 {
		t.Fatalf("entries = %d", n)
	}
	task.Set("pane", "w-worker:p2")
	if WorkerIsClosed(task) {
		t.Fatal("a successor pane inherited the closed record")
	}
	if note := ResumeNote("t-aaaaaaaaaaaa"); note != "pane closed, resume via execution resume t-aaaaaaaaaaaa" {
		t.Fatalf("note = %q", note)
	}
}
