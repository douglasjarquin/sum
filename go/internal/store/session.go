package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

var (
	ErrClaimRequired = errors.New("claim_required: run init and carry its claim in --claim")
	ErrClaimConflict = errors.New("conflict: another conversation holds the coordinator claim")
	ErrStaleClaim    = errors.New("stale-claim: coordinator claim changed; run init to inspect the current owner")
)

type CoordinatorSession struct {
	Schema        int
	Kind          string
	Machine       string
	SessionID     string
	Generation    uint64
	ClaimedAt     string
	Label         string
	RetiredNative *ordjson.Object
}

func (c *CoordinatorSession) Token() string {
	return c.SessionID + "." + strconv.FormatUint(c.Generation, 10)
}

func (c *CoordinatorSession) Owner() *ordjson.Object {
	o := ordjson.NewObject()
	o.Set("schema", json.Number("2"))
	o.Set("kind", "coordinator-session")
	o.Set("machine", c.Machine)
	o.Set("session_id", c.SessionID)
	return o
}

func (c *CoordinatorSession) Object() *ordjson.Object {
	o := c.Owner()
	o.Set("generation", json.Number(strconv.FormatUint(c.Generation, 10)))
	o.Set("claimed_at", c.ClaimedAt)
	o.Set("label", c.Label)
	if c.RetiredNative != nil {
		o.Set("retired_native", c.RetiredNative)
	}
	return o
}

// CoordinatorSession reads the logical owner without consulting HC or changing state.
func (s *Store) CoordinatorSession() (*CoordinatorSession, error) {
	path := filepath.Join(s.Home, "coordinator.json")
	value, err := ordjson.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	o, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("%s is not a coordinator session object", path)
	}
	if err = ValidateLogicalOwner(o); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	raw, _ := o.Get("generation")
	n, ok := raw.(json.Number)
	generation, err := strconv.ParseUint(string(n), 10, 64)
	if !ok || err != nil || generation == 0 {
		return nil, fmt.Errorf("%s: invalid coordinator claim generation", path)
	}
	if text(o, "claimed_at") == "" {
		return nil, fmt.Errorf("%s: missing claim timestamp", path)
	}
	retired, _ := o.Get("retired_native")
	native, _ := retired.(*ordjson.Object)
	return &CoordinatorSession{Schema: 2, Kind: "coordinator-session", Machine: text(o, "machine"), SessionID: text(o, "session_id"), Generation: generation, ClaimedAt: text(o, "claimed_at"), Label: text(o, "label"), RetiredNative: native}, nil
}

// ClaimCoordinator creates, resumes or explicitly transfers a claim under the state lock.
func (s *Store) ClaimCoordinator(token, label string, transfer bool) (*CoordinatorSession, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := s.CoordinatorSession()
	if err != nil {
		return nil, err
	}
	if current != nil {
		if !transfer {
			if token == current.Token() {
				return current, nil
			}
			if token != "" {
				return current, ErrStaleClaim
			}
			return current, fmt.Errorf("%w: %s (claimed %s)", ErrClaimConflict, current.Label, current.ClaimedAt)
		}
		if current.Generation == ^uint64(0) {
			return nil, fmt.Errorf("coordinator claim generation exhausted")
		}
		current.Generation++
		current.ClaimedAt, current.Label = Now(), label
	} else {
		if token != "" {
			return nil, ErrStaleClaim
		}
		host, err := s.Machine()
		if err != nil {
			return nil, err
		}
		var id [16]byte
		if _, err = rand.Read(id[:]); err != nil {
			return nil, err
		}
		current = &CoordinatorSession{Schema: 2, Kind: "coordinator-session", Machine: host.ID, SessionID: "c-" + hex.EncodeToString(id[:]), Generation: 1, ClaimedAt: Now(), Label: label}
	}
	if err = ordjson.WriteFile(filepath.Join(s.Home, "coordinator.json"), current.Object()); err != nil {
		return nil, err
	}
	return current, nil
}

// CheckClaim is repeated while holding Lock immediately before a coordinator mutation.
func (s *Store) CheckClaim(token string) (*CoordinatorSession, error) {
	if err := s.CheckWritable(); err != nil {
		return nil, err
	}
	if token == "" {
		return nil, ErrClaimRequired
	}
	current, err := s.CoordinatorSession()
	if err != nil {
		return nil, err
	}
	if current == nil || token != current.Token() {
		return nil, ErrStaleClaim
	}
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	if !host.Is(current.Machine) {
		return nil, fmt.Errorf("coordinator session belongs to another machine")
	}
	return current, nil
}

func ValidateLogicalOwner(owner *ordjson.Object) error {
	if owner == nil || number(owner, "schema") != 2 || text(owner, "kind") != "coordinator-session" || !machine.IsStable(text(owner, "machine")) || text(owner, "session_id") == "" {
		return fmt.Errorf("invalid logical coordinator owner")
	}
	return nil
}

func LogicalOwnersMatch(left, right *ordjson.Object) bool {
	return ValidateLogicalOwner(left) == nil && ValidateLogicalOwner(right) == nil && text(left, "machine") == text(right, "machine") && text(left, "session_id") == text(right, "session_id")
}
