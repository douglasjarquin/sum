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
func (s *Store) initializeGeneration(recordsOnly bool) error {
	if err := os.MkdirAll(s.Root, 0700); err != nil {
		return err
	}

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
		if recordsOnly {
			return fmt.Errorf("records restore requires a new state home")
		}
		current, err := Open(s.Root)
		if err != nil {
			return err
		}
		s.Home = current.Home
		s.Tasks = current.Tasks
		s.Sessions = current.Sessions
		s.Generation = current.Generation
		s.readOnly = current.readOnly
		s.recordsOnly = current.recordsOnly
		return s.CheckWritable()
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
	if recordsOnly {
		state.Set("backend", "records")
		state.Set("records_only", true)
	}
	if err = ordjson.WriteFile(filepath.Join(home, "state.json"), state); err != nil {
		return err
	}
	pointer := ordjson.NewObject()
	pointer.Set("schema", json.Number("2"))
	pointer.Set("backend", "hc")
	pointer.Set("generation", generation)
	if recordsOnly {
		pointer.Set("backend", "records")
		pointer.Set("records_only", true)
	}
	if err = ordjson.WriteFile(filepath.Join(s.Root, "state.json"), pointer); err != nil {
		return err
	}
	s.Generation = generation
	s.recordsOnly = recordsOnly
	s.readOnly = recordsOnly
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

func (s *Store) InitRecordsOnly() error {
	if s.Generation != "" || s.readOnly {
		return fmt.Errorf("records restore requires a new state home")
	}
	return s.initializeGeneration(true)
}

func (s *Store) RecordsOnly() bool { return s.recordsOnly }
