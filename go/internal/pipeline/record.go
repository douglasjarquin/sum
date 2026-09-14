package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	File   = "pipeline.json"
	Schema = 1
)

func Path(s *store.Store, taskID string) (string, error) {
	taskPath, err := s.TaskPath(taskID)
	if err != nil {
		return "", err
	}
	return filepath.Join(taskPath, File), nil
}

// Load returns an all-pending record when the file is absent, so no caller branches on existence.
func Load(s *store.Store, taskID string) (Record, error) {
	path, err := Path(s, taskID)
	if err != nil {
		return Record{}, err
	}
	record := New(taskID, "")
	info, statErr := os.Lstat(path)
	if statErr != nil || info.IsDir() {
		return record, nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Record{}, fmt.Errorf("%s must not be a symlink", path)
	}
	value, readErr := ordjson.ReadFile(path)
	if readErr != nil {
		return Record{}, readErr
	}
	stored, isObject := value.(*ordjson.Object)
	if !isObject {
		return Record{}, fmt.Errorf("%s is not a pipeline record", path)
	}
	schema, _ := stored.Get("schema")
	if !intEquals(schema, Schema) {
		return Record{}, fmt.Errorf("%s has pipeline schema %v; this release reads schema %d", path, schema, Schema)
	}
	return decode(taskID, stored), nil
}

func decode(taskID string, stored *ordjson.Object) Record {
	record := New(taskID, stringField(stored, "candidate"))
	record.UpdatedAt = stringField(stored, "updated_at")
	rows, _ := field(stored, "rows").([]any)
	for _, raw := range rows {
		row, _ := raw.(*ordjson.Object)
		if row == nil {
			continue
		}
		record.Set(Row{
			Stage:    Stage(stringField(row, "stage")),
			Status:   Status(stringField(row, "status")),
			Result:   stringField(row, "result"),
			At:       stringField(row, "at"),
			Evidence: stringList(row, "evidence"),
		})
	}
	return record
}

func (r Record) Object() *ordjson.Object {
	out := ordjson.NewObject()
	out.Set("schema", json.Number(fmt.Sprint(Schema)))
	out.Set("task", r.Task)
	out.Set("candidate", nilIfEmpty(r.Candidate))
	out.Set("updated_at", nilIfEmpty(r.UpdatedAt))
	rows := make([]any, 0, Count)
	for i, definition := range Stages {
		row := r.Rows[i]
		item := ordjson.NewObject()
		item.Set("stage", string(definition.Stage))
		item.Set("display", definition.Display)
		item.Set("status", string(row.Status))
		item.Set("mark", Mark(row.Status))
		item.Set("result", row.Result)
		item.Set("at", nilIfEmpty(row.At))
		item.Set("evidence", anyStrings(row.Evidence))
		rows = append(rows, item)
	}
	out.Set("rows", rows)
	return out
}

// save rewrites the whole file, stamping UpdatedAt, and is the single point every writer goes through.
func save(s *store.Store, record Record) error {
	path, err := Path(s, record.Task)
	if err != nil {
		return err
	}
	record.UpdatedAt = store.Now()
	return ordjson.WriteFile(path, record.Object())
}

// Set is the only writer of a single row. Writing the same row twice writes nothing.
func Set(s *store.Store, taskID string, stage Stage, status Status, result string, evidenceIDs ...string) (Record, error) {
	unlock, err := s.Lock()
	if err != nil {
		return Record{}, err
	}
	defer unlock()
	record, err := Load(s, taskID)
	if err != nil {
		return Record{}, err
	}
	next := Row{Stage: stage, Status: status, Result: result, At: store.Now(), Evidence: evidenceIDs}
	if record.Get(stage).same(next) {
		return record, nil
	}
	record.Set(next)
	if err := save(s, record); err != nil {
		return Record{}, err
	}
	return Load(s, taskID)
}

func Refresh(s *store.Store, taskID string) (Record, error) {
	unlock, err := s.Lock()
	if err != nil {
		return Record{}, err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return Record{}, err
	}
	if err := RefreshLocked(s, task); err != nil {
		return Record{}, err
	}
	return Load(s, taskID)
}

// RefreshLocked is Refresh for a caller that already holds the store lock and has the task in hand.
// The store lock is a flock, so a second acquisition inside the same command would deadlock.
func RefreshLocked(s *store.Store, task *ordjson.Object) error {
	taskID := stringField(task, "id")
	if taskID == "" {
		return fmt.Errorf("task record has no id")
	}
	stored, err := Load(s, taskID)
	if err != nil {
		return err
	}
	derived := Derive(task)
	if stored.sameRows(derived) && stored.UpdatedAt != "" {
		return nil
	}
	return save(s, derived)
}

// RefreshNote never fails its caller: it returns nil, or a short note saying why the record was not refreshed.
func RefreshNote(s *store.Store, task *ordjson.Object) any {
	if err := RefreshLocked(s, task); err != nil {
		return "the delivery pipeline record was not refreshed: " + err.Error()
	}
	return nil
}

func intEquals(value any, want int) bool {
	number, isNumber := value.(json.Number)
	if !isNumber {
		return false
	}
	n, err := number.Int64()
	return err == nil && n == int64(want)
}

func field(o *ordjson.Object, key string) any {
	if o == nil {
		return nil
	}
	value, _ := o.Get(key)
	return value
}

func stringField(o *ordjson.Object, key string) string {
	text, _ := field(o, key).(string)
	return text
}

func stringList(o *ordjson.Object, key string) []string {
	items, _ := field(o, key).([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, isText := item.(string); isText {
			out = append(out, text)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func anyStrings(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func nilIfEmpty(text string) any {
	if text == "" {
		return nil
	}
	return text
}
