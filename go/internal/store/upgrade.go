package store

import (
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func (s *Store) RetireForUpgrade(id string, owner *ordjson.Object, validate func() error, afterFence func() error) (*CoordinatorSession, error) {
	if !regexp.MustCompile(`^c-[a-f0-9]{32}$`).MatchString(id) {
		return nil, fmt.Errorf("invalid upgrade session identity")
	}
	release, err := s.DeliveryLock()
	if err != nil {
		return nil, err
	}
	defer release()
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	current, err := s.CoordinatorSession()
	if err != nil {
		return nil, err
	}
	if current != nil {
		if current.SessionID != id || current.Generation != 1 || !host.Is(current.Machine) {
			return nil, ErrClaimConflict
		}
		return current, nil
	}
	native, err := s.Owner()
	if err != nil {
		return nil, err
	}
	want, _ := ordjson.MarshalCompact(owner)
	got, _ := ordjson.MarshalCompact(native)
	endpoint := EndpointFromContext(native)
	if native == nil || string(want) != string(got) || endpoint.Validate() != nil || endpoint.Role != "coordinator" || !host.Is(endpoint.Machine) {
		return nil, fmt.Errorf("recorded native owner changed or cannot be identified")
	}
	if err = s.checkRetirementLaunches(); err != nil {
		return nil, err
	}
	if err = validate(); err != nil {
		return nil, err
	}
	session := &CoordinatorSession{Schema: 2, Kind: "coordinator-session", Machine: host.ID, SessionID: id, Generation: 1, ClaimedAt: Now(), Label: "upgrade in agent conversation", RetiredNative: native}
	if err = s.fenceNativeWriters(); err != nil {
		return nil, err
	}
	if afterFence != nil {
		if err = afterFence(); err != nil {
			return nil, err
		}
	}
	if err = ordjson.WriteFile(filepath.Join(s.Home, "coordinator.json"), session.Object()); err != nil {
		return nil, err
	}
	return session, nil
}
