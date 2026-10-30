package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

var generationPattern = regexp.MustCompile(`^g-[a-f0-9]{24}$`)
var ErrInactiveGeneration = errors.New("inactive state generation; reopen the current authority before writing or sending")
var ErrHistoricalReadOnly = errors.New("historical evidence is read-only; use an explicit HC handover for new work")

// CheckWritable fences helpers opened before an authority transfer.
// Legacy binaries cannot make this check; physical isolation prevents their old
// task paths from changing the new ledger, and migration additionally requires
// an exclusion lease for every old writer and sender before publication.
func (s *Store) CheckWritable() error {
	if s.readOnly {
		return ErrHistoricalReadOnly
	}
	if s.Generation == "" {
		return fmt.Errorf("state is not initialized")
	}
	return s.checkGeneration()
}
func (s *Store) checkGeneration() error {
	value, err := ordjson.ReadFile(filepath.Join(s.Root, "state.json"))
	if err != nil {
		return err
	}
	state, ok := value.(*ordjson.Object)
	if !ok {
		return fmt.Errorf("state.json is not an object")
	}
	generation, _ := state.Get("generation")
	if !schemaMatches(state) || generation != s.Generation {
		return ErrInactiveGeneration
	}
	return nil
}
func (s *Store) initializeGeneration() error {
	if err := os.MkdirAll(s.Root, 0700); err != nil {
		return err
	}
	// Keep this inode forever: concurrent initializers must not publish different roots.
	lock, err := os.OpenFile(filepath.Join(s.Root, ".init.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if _, err = os.Stat(filepath.Join(s.Root, "state.json")); err == nil {
		current, err := Open(s.Root)
		if err != nil {
			return err
		}
		s.Home = current.Home
		s.Tasks = current.Tasks
		s.Sessions = current.Sessions
		s.Generation = current.Generation
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	bytes := make([]byte, 16)
	if _, err = rand.Read(bytes); err != nil {
		return err
	}
	generation := "g-" + hex.EncodeToString(bytes[:12])
	home := filepath.Join(s.Root, "generations", generation)
	if err = os.MkdirAll(filepath.Join(home, "tasks"), 0700); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(home, "sessions"), 0700); err != nil {
		return err
	}
	state := ordjson.NewObject()
	state.Set("schema", json.Number("2"))
	state.Set("backend", "hc")
	state.Set("instance", hex.EncodeToString(bytes))
	state.Set("generation", generation)
	state.Set("root", s.Root)
	state.Set("sum_version", SumVersion)
	state.Set("created_at", Now())
	if err = ordjson.WriteFile(filepath.Join(home, "state.json"), state); err != nil {
		return err
	}
	pointer := ordjson.NewObject()
	pointer.Set("schema", json.Number("2"))
	pointer.Set("backend", "hc")
	pointer.Set("generation", generation)
	if err = ordjson.WriteFile(filepath.Join(s.Root, "state.json"), pointer); err != nil {
		return err
	}
	s.Generation = generation
	s.Home = home
	s.Tasks = filepath.Join(home, "tasks")
	s.Sessions = filepath.Join(home, "sessions")
	return nil
}

func (s *Store) authorityLock(ctx context.Context, path string, how, rank int, busy error) (func() error, error) {
	unlock, err := s.flock(ctx, path, how, rank, busy)
	if err != nil {
		return nil, err
	}
	if err = s.CheckWritable(); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}

// Register is a deliberate domain mutation; a replacement binding may overwrite
// only the same permanent native terminal. Passive Registration remains strict
// about the attempt and role, so stale callers cannot inherit a new binding.
func (s *Store) registrationForReplacement(endpoint Endpoint) (*ordjson.Object, error) {
	path := filepath.Join(s.Sessions, RegistrationKey(endpoint)+".json")
	value, err := ordjson.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	object, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("registration is not an object")
	}
	recorded := EndpointFromContext(object)
	if !recorded.Native().SameTerminal(endpoint.Native()) {
		return nil, fmt.Errorf("registration key names another permanent terminal")
	}
	if !recorded.Native().SameAuthority(endpoint.Native()) {
		return nil, nil
	}
	return object, nil
}
