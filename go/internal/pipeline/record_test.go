package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/store"
)

func labStore(t *testing.T, taskJSON string) (*store.Store, string) {
	t.Helper()
	home := t.TempDir()
	taskID := "t-aaaaaaaaaaaa"
	dir := filepath.Join(home, "tasks", taskID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"),
		[]byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "task.json"), []byte(taskJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	return s, taskID
}

func TestLoad_withNoFileIsAnAllPendingRecord(t *testing.T) {
	s, taskID := labStore(t, `{"schema": 1, "id": "t-aaaaaaaaaaaa", "brief": "b"}`)

	record, err := Load(s, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Rows) != Count {
		t.Fatalf("rows = %d, want %d", len(record.Rows), Count)
	}
	for i, row := range record.Rows {
		if row.Stage != Stages[i].Stage || row.Status != Pending {
			t.Fatalf("row %d = %+v, want stage %s pending", i, row, Stages[i].Stage)
		}
	}
}

func TestSet_persistsOneRowAndWritesNothingTheSecondTime(t *testing.T) {
	s, taskID := labStore(t, `{"schema": 1, "id": "t-aaaaaaaaaaaa", "brief": "b"}`)

	if _, err := Set(s, taskID, Document, Pass, "Passed", "e-4"); err != nil {
		t.Fatal(err)
	}
	path, err := Path(s, taskID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	record, err := Load(s, taskID)
	if err != nil {
		t.Fatal(err)
	}
	row := record.Get(Document)
	if row.Status != Pass || row.Result != "Passed" || len(row.Evidence) != 1 || row.Evidence[0] != "e-4" {
		t.Fatalf("document row = %+v", row)
	}

	if _, err := Set(s, taskID, Document, Pass, "Passed", "e-4"); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("setting the same row rewrote the file:\n%s\n%s", first, second)
	}
}

func TestRefresh_rebuildsTheRecordFromTheTask(t *testing.T) {
	s, taskID := labStore(t, `{"schema": 1, "id": "t-aaaaaaaaaaaa", "brief": "do the thing",
"report": {"candidate": "cccccccccccccccccccccccccccccccccccccccc"},
"evidence": [{"schema": 1, "id": "e-3", "kind": "verification", "source": "coordinator", "at": "2026-01-01T03:00:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "result": "pass", "run_id": "20260906T010203Z-abcd",
"certifies": "cccccccccccccccccccccccccccccccccccccccc", "requires_root_review": false}]}`)

	record, err := Refresh(s, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if got := record.Get(Test); got.Status != Pass {
		t.Fatalf("test row = %+v, want pass", got)
	}
	if got := record.Get(Intent); got.Status != Pass {
		t.Fatalf("intent row = %+v, want pass", got)
	}
	if record.UpdatedAt == "" {
		t.Fatal("a refreshed record carries no updated_at")
	}
}
