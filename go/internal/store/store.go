package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

const Schema = 1

const SumVersion = "0.1.0"

var taskIDPattern = regexp.MustCompile(`^t-[a-f0-9]{12}$`)

type Store struct {
	Home     string
	Tasks    string
	Sessions string

	machine *machine.Identity
	locks   lockRanks
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

// Lock order. A process takes the delivery compatibility lock, then at most one recipient lock, then the state lock,
// and never the reverse; each Store refuses an out-of-order acquisition with ErrLockOrder instead of deadlocking.
// Only the state lock guards record writes, and nothing holds it across a Herdr call. The compatibility lock is held
// shared by every delivery of this release and exclusively by older releases, so mixed versions stay fully serialized
// while deliveries of this release to different recipients run in parallel.
const (
	rankCompat    = 1 // .deliver.lock
	rankRecipient = 2 // deliver/<hash>.lock
	rankState     = 3 // .lock
)

// ErrLockOrder reports an acquisition that would invert the documented lock order.
var ErrLockOrder = errors.New("lock order violation: delivery compatibility, then recipient, then state lock")

// ErrDeliveryLockBusy reports that another delivery held the compatibility lock until the caller's deadline.
var ErrDeliveryLockBusy = errors.New("delivery lock busy")

// ErrRecipientBusy reports that another operation held this recipient's delivery lock until the caller's deadline.
var ErrRecipientBusy = errors.New("recipient delivery lock busy")

// deliveryLockRetry is how often a bounded acquisition retries a held lock.
const deliveryLockRetry = 20 * time.Millisecond

type lockRanks struct {
	mu   sync.Mutex
	held []int
}

func (s *Store) ranks() *lockRanks { return &s.locks }

func (r *lockRanks) acquire(rank int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, held := range r.held {
		if held >= rank {
			return ErrLockOrder
		}
	}
	r.held = append(r.held, rank)
	return nil
}

func (r *lockRanks) release(rank int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.held) - 1; i >= 0; i-- {
		if r.held[i] == rank {
			r.held = append(r.held[:i], r.held[i+1:]...)
			return
		}
	}
}

// flock takes how (LOCK_SH or LOCK_EX) on path at rank. Without a deadline it blocks; with one it retries until ctx
// ends and then returns busy. An already-ended ctx makes exactly one non-blocking attempt.
func (s *Store) flock(ctx context.Context, path string, how, rank int, busy error) (func() error, error) {
	if err := s.ranks().acquire(rank); err != nil {
		return nil, err
	}
	// Read-write, so a shared lock also works where flock is emulated with byte-range locks (NFS).
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		s.ranks().release(rank)
		return nil, err
	}
	if ctx == nil || ctx.Done() == nil {
		err = syscall.Flock(int(handle.Fd()), how)
	} else {
		for {
			err = syscall.Flock(int(handle.Fd()), how|syscall.LOCK_NB)
			if err != syscall.EWOULDBLOCK {
				break
			}
			select {
			case <-ctx.Done():
				handle.Close()
				s.ranks().release(rank)
				return nil, busy
			case <-time.After(deliveryLockRetry):
			}
		}
	}
	if err != nil {
		handle.Close()
		s.ranks().release(rank)
		return nil, err
	}
	return func() error {
		unlockErr := syscall.Flock(int(handle.Fd()), syscall.LOCK_UN)
		closeErr := handle.Close()
		s.ranks().release(rank)
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, nil
}

// Lock takes the state lock that guards every record write. Hold it only for local reads and writes.
func (s *Store) Lock() (func() error, error) {
	if err := s.Init(); err != nil {
		return nil, err
	}
	return s.flock(nil, filepath.Join(s.Home, ".lock"), syscall.LOCK_EX, rankState, nil)
}

// DeliveryLock takes the delivery compatibility lock exclusively, as releases before recipient-scoped delivery did
// for every delivery. It excludes every delivery of every release.
func (s *Store) DeliveryLock() (func() error, error) {
	return s.DeliveryLockContext(context.Background())
}

// DeliveryLockContext is DeliveryLock giving up with ErrDeliveryLockBusy when ctx ends first.
func (s *Store) DeliveryLockContext(ctx context.Context) (func() error, error) {
	if err := s.Init(); err != nil {
		return nil, err
	}
	return s.flock(ctx, filepath.Join(s.Home, ".deliver.lock"), syscall.LOCK_EX, rankCompat, ErrDeliveryLockBusy)
}

// DeliveryShared takes the delivery compatibility lock shared: it coexists with other deliveries of this release and
// waits for an older release's exclusive holder, giving up with ErrDeliveryLockBusy when ctx ends first.
func (s *Store) DeliveryShared(ctx context.Context) (func() error, error) {
	if err := s.Init(); err != nil {
		return nil, err
	}
	return s.flock(ctx, filepath.Join(s.Home, ".deliver.lock"), syscall.LOCK_SH, rankCompat, ErrDeliveryLockBusy)
}

// RecipientLockPath is the lock file for one canonical recipient endpoint (machine, session, pane). Callers pass the
// machine already canonicalized, so every spelling of one host's endpoint names one file.
func (s *Store) RecipientLockPath(endpoint [3]string) string {
	key := RegistrationKey(Endpoint{Machine: endpoint[0], Session: endpoint[1], Pane: endpoint[2]})
	return filepath.Join(s.Home, "deliver", key+".lock")
}

// WakePath is the coordinator wake sidecar for one canonical recipient endpoint, beside its recipient lock and
// keyed the same way, so every spelling of one host's endpoint names one file.
func (s *Store) WakePath(endpoint [3]string) string {
	key := RegistrationKey(Endpoint{Machine: endpoint[0], Session: endpoint[1], Pane: endpoint[2]})
	return filepath.Join(s.Home, "deliver", key+".wake.json")
}

// Instance is this installation's recorded instance ID from state.json ("" when none is recorded yet).
func (s *Store) Instance() (string, error) {
	statePath := filepath.Join(s.Home, "state.json")
	if info, err := os.Stat(statePath); err != nil || info.IsDir() {
		return "", nil
	}
	value, err := ordjson.ReadFile(statePath)
	if err != nil {
		return "", err
	}
	state, ok := value.(*ordjson.Object)
	if !ok {
		return "", fmt.Errorf("state.json is not a JSON object")
	}
	instance, _ := state.Get("instance")
	text, _ := instance.(string)
	return text, nil
}

// RecipientLock takes one recipient's delivery lock exclusively, after the compatibility lock and before the state
// lock, giving up with ErrRecipientBusy when ctx ends first. Lock files are never removed.
func (s *Store) RecipientLock(ctx context.Context, endpoint [3]string) (func() error, error) {
	path := s.RecipientLockPath(endpoint)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return s.flock(ctx, path, syscall.LOCK_EX, rankRecipient, ErrRecipientBusy)
}

// Machine is this host's identity as seen from this state home, resolved once
// per Store.
func (s *Store) Machine() (machine.Identity, error) {
	if s.machine == nil {
		identity, err := machine.Local(s.Home)
		if err != nil {
			return machine.Identity{}, err
		}
		s.machine = &identity
	}
	return *s.machine, nil
}

func (s *Store) CheckMachine(task *ordjson.Object) error {
	host, err := s.Machine()
	if err != nil {
		return err
	}
	if recorded, _ := task.Get("machine"); !host.Is(recorded) {
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

// ClockPinEnv names the test-only variable that pins the clock. It is never exported in a live shell.
const ClockPinEnv = "SUM_NOW"

// ClockPin reports the instant the clock is pinned to. It is not in effect unless ClockPinEnv holds an
// RFC 3339 stamp, so a value that does not parse leaves the real clock in place.
func ClockPin() (time.Time, bool) {
	if raw := os.Getenv(ClockPinEnv); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// NowTime is the one clock every sum process reads, so that two processes whose output is compared can be
// pinned to the same instant instead of racing a second boundary.
func NowTime() time.Time {
	if pinned, ok := ClockPin(); ok {
		return pinned
	}
	return time.Now()
}

func Now() string {
	return NowTime().UTC().Format("2006-01-02T15:04:05+00:00")
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

func identityMatches(host machine.Identity, value *ordjson.Object, e Endpoint) bool {
	recorded, _ := value.Get("machine")
	session, _ := value.Get("session")
	pane, _ := value.Get("pane")
	return host.Same(recorded, e.Machine) && session == e.Session && pane == e.Pane
}

// Matches reports whether the machine, session, and pane recorded in value
// name endpoint, reading a legacy hostname that provably names this host as
// this host.
func (s *Store) Matches(value *ordjson.Object, endpoint Endpoint) (bool, error) {
	host, err := s.Machine()
	if err != nil {
		return false, err
	}
	return identityMatches(host, value, endpoint), nil
}

// registrationPaths lists where endpoint's registration may live: under its
// key first, then, when endpoint is this host, under the keys a legacy
// hostname identity produced before the stable identity existed.
func (s *Store) registrationPaths(host machine.Identity, endpoint Endpoint) []string {
	endpoint.Machine = host.Canonical(endpoint.Machine)
	paths := []string{filepath.Join(s.Sessions, RegistrationKey(endpoint)+".json")}
	if endpoint.Machine != host.ID {
		return paths
	}
	for _, legacy := range host.Legacy() {
		alias := endpoint
		alias.Machine = legacy
		paths = append(paths, filepath.Join(s.Sessions, RegistrationKey(alias)+".json"))
	}
	return paths
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
	_, obj, err := s.registrationFile(endpoint)
	return obj, err
}

// registrationFile finds endpoint's registration file (under its key, then a legacy hostname key of this host) and
// reads it, refusing one whose recorded identity is not endpoint. A missing registration is "", nil, nil.
func (s *Store) registrationFile(endpoint Endpoint) (string, *ordjson.Object, error) {
	host, err := s.Machine()
	if err != nil {
		return "", nil, err
	}
	for _, path := range s.registrationPaths(host, endpoint) {
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			continue
		}
		value, err := ordjson.ReadFile(path)
		if err != nil {
			return "", nil, err
		}
		obj, ok := value.(*ordjson.Object)
		if !ok {
			return "", nil, fmt.Errorf("session registration is not a JSON object")
		}
		if !identityMatches(host, obj, endpoint) {
			return "", nil, fmt.Errorf("session registration identity mismatch; inspect the sessions directory")
		}
		return path, obj, nil
	}
	return "", nil, nil
}

// Register records endpoint's role. incarnation is the occupant Herdr reported for the pane when it was bound (an
// incarnation record, see package incarnation), or nil when the caller observed none: a record without one is legacy
// and proves nothing about who occupies the pane later.
func (s *Store) Register(endpoint Endpoint, role string, task any, incarnation any) (*ordjson.Object, error) {
	stateValue, err := ordjson.ReadFile(filepath.Join(s.Home, "state.json"))
	if err != nil {
		return nil, err
	}
	state, ok := stateValue.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("state.json is not a JSON object")
	}
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	previous, err := s.Registration(endpoint)
	if err != nil {
		return nil, err
	}
	endpoint.Machine = host.Canonical(endpoint.Machine)
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
	value.Set("incarnation", incarnation)
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
	if err := machine.Record(s.Home); err != nil {
		return nil, err
	}
	for _, legacy := range s.registrationPaths(host, endpoint)[1:] {
		if err := os.Remove(legacy); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return value, nil
}

// SetIncarnation replaces the incarnation recorded on endpoint's registration and changes nothing else. The caller
// holds the state lock and has just judged the current occupant verified against the record it replaces.
func (s *Store) SetIncarnation(endpoint Endpoint, incarnation any) error {
	path, obj, err := s.registrationFile(endpoint)
	if err != nil {
		return err
	}
	if obj == nil {
		return fmt.Errorf("no session registration for pane %s in session %s", endpoint.Pane, endpoint.Session)
	}
	obj.Set("incarnation", incarnation)
	return ordjson.WriteFile(path, obj)
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
