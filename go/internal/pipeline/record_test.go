package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/store"
)

func labStore(t *testing.T, taskJSON string) (*store.Store, string) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	taskID := "t-aaaaaaaaaaaa"
	dir := filepath.Join(s.Tasks, taskID)
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "task.json"), []byte(taskJSON), 0600); err != nil {
		t.Fatal(err)
	}
	return s, taskID
}

func TestLoad_withNoFileIsAnAllPendingRecord(t *testing.T) {
	s, taskID := labStore(t, `{"schema": 2, "id": "t-aaaaaaaaaaaa", "brief": "b"}`)

	record, err := Load(s, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Rows) != Count {
		t.Fatalf("rows = %d, want %d", len(record.Rows), Count)
	}
	path, err := Path(s, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("loading an absent pipeline wrote a file: %v", err)
	}
	if settled, detail := GatesSettled(record); settled {
		t.Fatalf("pending delivery gates settled: %s", detail)
	}
	for i, row := range record.Rows {
		if row.Stage != Stages[i].Stage || row.Status != Pending {
			t.Fatalf("row %d = %+v, want stage %s pending", i, row, Stages[i].Stage)
		}
	}
}

func TestSet_persistsOneRowAndWritesNothingTheSecondTime(t *testing.T) {
	t.Setenv("SUM_NOW", "2026-01-01T00:00:00+00:00")
	s, taskID := labStore(t, `{"schema": 2, "id": "t-aaaaaaaaaaaa", "brief": "b"}`)

	taskPath, err := s.TaskPath(taskID)
	if err != nil {
		t.Fatal(err)
	}
	taskBefore, err := os.ReadFile(filepath.Join(taskPath, "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Set(s, taskID, StageDocument, Pass, "Passed", "e-4"); err != nil {
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
	row := record.Get(StageDocument)
	if row.Status != Pass || row.Result != "Passed" || len(row.Evidence) != 1 || row.Evidence[0] != "e-4" {
		t.Fatalf("document row = %+v", row)
	}

	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_NOW", "2026-01-02T00:00:00+00:00")
	if _, err := Set(s, taskID, StageDocument, Pass, "Passed", "e-4"); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("setting the same row rewrote the file:\n%s\n%s", first, second)
	}
	afterInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(beforeInfo, afterInfo) || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("idempotent row update replaced or rewrote the file")
	}
	taskAfter, err := os.ReadFile(filepath.Join(taskPath, "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(taskBefore) != string(taskAfter) {
		t.Fatal("pipeline row update changed task/evidence bytes")
	}
}

func TestRefresh_rebuildsTheRecordFromTheTask(t *testing.T) {
	t.Setenv("SUM_NOW", "2026-01-01T00:00:00+00:00")
	s, taskID := labStore(t, `{"schema": 2, "id": "t-aaaaaaaaaaaa", "brief": "do the thing",
"report": {"candidate": "cccccccccccccccccccccccccccccccccccccccc"},
"evidence": [{"schema": 1, "id": "e-3", "kind": "verification", "source": "coordinator", "at": "2026-01-01T03:00:00+00:00",
"candidate": "cccccccccccccccccccccccccccccccccccccccc", "result": "pass", "run_id": "20260906T010203Z-abcd",
"certifies": "cccccccccccccccccccccccccccccccccccccccc", "requires_root_review": false}]}`)

	taskPath, err := s.TaskPath(taskID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(taskPath, "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := Refresh(s, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if got := record.Get(StageTest); got.Status != Pass {
		t.Fatalf("test row = %+v, want pass", got)
	}
	if got := record.Get(StageIntent); got.Status != Pass {
		t.Fatalf("intent row = %+v, want pass", got)
	}
	if record.UpdatedAt == "" {
		t.Fatal("a refreshed record carries no updated_at")
	}
	if settled, detail := GatesSettled(record); settled {
		t.Fatalf("partial verification settled unfulfilled delivery gates: %s", detail)
	}
	after, err := os.ReadFile(filepath.Join(taskPath, "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("refresh changed the source task/evidence bytes")
	}
	path, err := Path(s, taskID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_NOW", "2026-01-02T00:00:00+00:00")
	repeated, err := Refresh(s, taskID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || record.UpdatedAt != repeated.UpdatedAt {
		t.Fatal("refreshing unchanged task evidence rewrote the pipeline record")
	}
}
