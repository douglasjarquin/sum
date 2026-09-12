package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

const Schema = 1

const SumVersion = "0.1.0"

var taskIDPattern = regexp.MustCompile(`^t-[a-f0-9]{12}$`)

type Store struct {
	Home     string
	Tasks    string
	Sessions string
}

func Open(home string) (*Store, error) {
	resolved, err := resolveHome(home)
	if err != nil {
		return nil, err
	}
	s := &Store{Home: resolved, Tasks: filepath.Join(resolved, "tasks"), Sessions: filepath.Join(resolved, "sessions")}
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

func (s *Store) DeliveryLock() (func() error, error) {
	if err := s.Init(); err != nil {
		return nil, err
	}
	handle, err := os.OpenFile(filepath.Join(s.Home, ".deliver.lock"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
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

func (s *Store) CheckMachine(task *ordjson.Object) error {
	machine, _ := task.Get("machine")
	host, err := os.Hostname()
	if err != nil {
		return err
	}
	if machine != host {
		return fmt.Errorf("Task belongs to another machine. Inspect saved work and use bind explicitly; stale pane IDs are not portable.")
	}
	return nil
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

type Endpoint struct {
	Machine string
	Session string
	Pane    string
	Cwd     string
}

func RegistrationKey(e Endpoint) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{e.Machine, e.Session, e.Pane}, "\n")))
	return hex.EncodeToString(sum[:])[:16]
}

func identityMatches(value *ordjson.Object, e Endpoint) bool {
	machine, _ := value.Get("machine")
	session, _ := value.Get("session")
	pane, _ := value.Get("pane")
	return machine == e.Machine && session == e.Session && pane == e.Pane
}

func (s *Store) Designated() bool {
	if info, err := os.Stat(filepath.Join(s.Home, "state.json")); err != nil || info.IsDir() {
		return false
	}
	if info, err := os.Stat(filepath.Join(s.Home, "dev.json")); err == nil && !info.IsDir() {
		return false
	}
	return true
}

func (s *Store) Owner() (*ordjson.Object, error) {
	path := filepath.Join(s.Home, "context.json")
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return nil, nil
	}
	value, err := ordjson.ReadFile(path)
	if err != nil {
		return nil, err
	}
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("context.json is not a JSON object")
	}
	return obj, nil
}

func (s *Store) Registration(endpoint Endpoint) (*ordjson.Object, error) {
	path := filepath.Join(s.Sessions, RegistrationKey(endpoint)+".json")
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return nil, nil
	}
	value, err := ordjson.ReadFile(path)
	if err != nil {
		return nil, err
	}
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("session registration is not a JSON object")
	}
	if !identityMatches(obj, endpoint) {
		return nil, fmt.Errorf("session registration identity mismatch; inspect the sessions directory")
	}
	return obj, nil
}

func (s *Store) Register(endpoint Endpoint, role string, task any) (*ordjson.Object, error) {
	stateValue, err := ordjson.ReadFile(filepath.Join(s.Home, "state.json"))
	if err != nil {
		return nil, err
	}
	state, ok := stateValue.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("state.json is not a JSON object")
	}
	previous, err := s.Registration(endpoint)
	if err != nil {
		return nil, err
	}
	instance, _ := state.Get("instance")
	registeredAt := Now()
	if previous != nil {
		if v, ok := previous.Get("registered_at"); ok {
			if s, isString := v.(string); isString {
				registeredAt = s
			}
		}
	}

	key := RegistrationKey(endpoint)
	value := ordjson.NewObject()
	value.Set("schema", json.Number(fmt.Sprint(Schema)))
	value.Set("key", key)
	value.Set("role", role)
	value.Set("task", task)
	value.Set("machine", endpoint.Machine)
	value.Set("session", endpoint.Session)
	value.Set("pane", endpoint.Pane)
	value.Set("cwd", endpointCwd(endpoint))
	value.Set("instance", instance)
	value.Set("sum_version", contract.SumVersion)
	mcp := ordjson.NewObject()
	mcp.Set("server", contract.MCP.Server)
	mcp.Set("version", contract.MCP.Version)
	mcp.Set("tools", json.Number(fmt.Sprint(contract.MCP.Tools)))
	value.Set("mcp", mcp)
	value.Set("registered_at", registeredAt)
	value.Set("updated_at", Now())

	if err := ordjson.WriteFile(filepath.Join(s.Sessions, key+".json"), value); err != nil {
		return nil, err
	}
	return value, nil
}

func endpointCwd(e Endpoint) any {
	if e.Cwd == "" {
		return nil
	}
	return e.Cwd
}

func (s *Store) Registrations() ([]*ordjson.Object, error) {
	entries, err := filepath.Glob(filepath.Join(s.Sessions, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(entries)
	registrations := make([]*ordjson.Object, 0, len(entries))
	for _, entry := range entries {
		value, err := ordjson.ReadFile(entry)
		if err != nil {
			return nil, err
		}
		obj, ok := value.(*ordjson.Object)
		if !ok {
			return nil, fmt.Errorf("%s is not a JSON object", entry)
		}
		registrations = append(registrations, obj)
	}
	return registrations, nil
}
