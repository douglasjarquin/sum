package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"syscall"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

const Schema = 1

const SumVersion = "0.1.0"

var taskIDPattern = regexp.MustCompile(`^t-[a-f0-9]{12}$`)

type Store struct {
	Home  string
	Tasks string
}

func Open(home string) (*Store, error) {
	resolved, err := resolveHome(home)
	if err != nil {
		return nil, err
	}
	s := &Store{Home: resolved, Tasks: filepath.Join(resolved, "tasks")}
	statePath := filepath.Join(resolved, "state.json")
	if info, statErr := os.Stat(statePath); statErr == nil && !info.IsDir() {
		value, readErr := ordjson.ReadFile(statePath)
		if readErr != nil {
			return nil, readErr
		}
		obj, ok := value.(*ordjson.Object)
		if !ok {
			return nil, fmt.Errorf("state.json is not a JSON object")
		}
		if !schemaMatches(obj) {
			return nil, fmt.Errorf("unsupported state schema. Preserve the original; use the matching sum release. No in-place migration")
		}
	}
	return s, nil
}

func schemaMatches(state *ordjson.Object) bool {
	value, ok := state.Get("schema")
	if !ok {
		return false
	}
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	n, err := number.Int64()
	return err == nil && n == Schema
}

func resolveHome(home string) (string, error) {
	expanded, err := expandUser(home)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved, nil
	}
	return absolute, nil
}

func expandUser(path string) (string, error) {
	if path != "~" && !hasHomePrefix(path) {
		return path, nil
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return dir, nil
	}
	return filepath.Join(dir, path[2:]), nil
}

func hasHomePrefix(path string) bool {
	return len(path) >= 2 && path[0] == '~' && path[1] == filepath.Separator
}

func (s *Store) Init() error {
	if err := os.MkdirAll(s.Home, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(s.Tasks, 0o700); err != nil {
		return err
	}
	statePath := filepath.Join(s.Home, "state.json")
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		state := ordjson.NewObject()
		state.Set("schema", json.Number(fmt.Sprint(Schema)))
		state.Set("sum_version", SumVersion)
		state.Set("created_at", Now())
		return ordjson.WriteFile(statePath, state)
	}
	return nil
}

func (s *Store) Lock() (func() error, error) {
	if err := s.Init(); err != nil {
		return nil, err
	}
	handle, err := os.OpenFile(filepath.Join(s.Home, ".lock"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(handle.Fd()), syscall.LOCK_EX); err != nil {
		handle.Close()
		return nil, err
	}
	return func() error {
		unlockErr := syscall.Flock(int(handle.Fd()), syscall.LOCK_UN)
		closeErr := handle.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, nil
}

func (s *Store) TaskPath(taskID string) (string, error) {
	if !taskIDPattern.MatchString(taskID) {
		return "", fmt.Errorf("invalid task ID")
	}
	path := filepath.Join(s.Tasks, taskID)
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("task directories must not be symlinks")
	}
	return path, nil
}

func (s *Store) ReadTask(taskID string) (*ordjson.Object, error) {
	path, err := s.TaskPath(taskID)
	if err != nil {
		return nil, err
	}
	value, err := ordjson.ReadFile(filepath.Join(path, "task.json"))
	if err != nil {
		return nil, err
	}
	task, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("task.json is not a JSON object")
	}
	schema, _ := task.Get("schema")
	id, _ := task.Get("id")
	number, isNumber := schema.(json.Number)
	schemaOK := isNumber
	if isNumber {
		n, convErr := number.Int64()
		schemaOK = convErr == nil && n == Schema
	}
	if !schemaOK || id != taskID {
		return nil, fmt.Errorf("task identity/schema mismatch")
	}
	return task, nil
}

func (s *Store) SaveTask(task *ordjson.Object) error {
	id, ok := task.Get("id")
	idString, isString := id.(string)
	if !ok || !isString {
		return fmt.Errorf("task has no string id")
	}
	path, err := s.TaskPath(idString)
	if err != nil {
		return err
	}
	task.Set("updated_at", Now())
	return ordjson.WriteFile(filepath.Join(path, "task.json"), task)
}

func (s *Store) AllTasks() ([]*ordjson.Object, error) {
	entries, err := filepath.Glob(filepath.Join(s.Tasks, "t-*", "task.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(entries)
	tasks := make([]*ordjson.Object, 0, len(entries))
	for _, entry := range entries {
		id := filepath.Base(filepath.Dir(entry))
		task, err := s.ReadTask(id)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func Now() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05+00:00")
}
