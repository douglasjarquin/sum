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

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

var (
	ErrClaimRequired            = errors.New("claim_required: run init and carry its claim in --claim")
	ErrClaimConflict            = errors.New("conflict: another conversation holds the coordinator claim")
	ErrStaleClaim               = errors.New("stale-claim: coordinator claim changed; run init to inspect the current owner")
	ErrNativeRetirementRequired = errors.New("native-retirement-required: confirm retirement with init --retire-native")
)

const LogicalStateSchema = contract.LogicalStateSchema

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

func (s *Store) ClaimCoordinator(token, label string, transfer bool) (*CoordinatorSession, error) {
	return s.claimCoordinator(token, label, transfer, false)
}

func (s *Store) RetireNativeCoordinator(token, label string, transfer bool) (*CoordinatorSession, error) {
	return s.claimCoordinator(token, label, transfer, true)
}

func (s *Store) claimCoordinator(token, label string, transfer, retire bool) (*CoordinatorSession, error) {
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
	current, err := s.CoordinatorSession()
	if err != nil {
		return nil, err
	}
	if current != nil {
		host, err := s.Machine()
		if err != nil {
			return nil, err
		}
		if !host.Is(current.Machine) {
			return nil, fmt.Errorf("coordinator session belongs to another machine")
		}
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
		owner, err := s.Owner()
		if err != nil {
			return nil, err
		}
		if owner != nil {
			if !retire {
				return nil, ErrNativeRetirementRequired
			}
			endpoint := EndpointFromContext(owner)
			if err := endpoint.Validate(); err != nil || endpoint.Role != "coordinator" || !host.Is(endpoint.Machine) {
				return nil, fmt.Errorf("native owner cannot be safely identified for retirement")
			}
			if err := s.checkRetirementLaunches(); err != nil {
				return nil, err
			}
			current.RetiredNative = owner
		}
	}
	if err = s.fenceNativeWriters(); err != nil {
		return nil, err
	}
	if err = ordjson.WriteFile(filepath.Join(s.Home, "coordinator.json"), current.Object()); err != nil {
		return nil, err
	}
	return current, nil
}

func (s *Store) fenceNativeWriters() error {
	path := filepath.Join(s.Root, "state.json")
	value, err := ordjson.ReadFile(path)
	if err != nil {
		return err
	}
	state, ok := value.(*ordjson.Object)
	if !ok {
		return fmt.Errorf("state.json is not an object")
	}
	if number(state, "schema") == LogicalStateSchema {
		return nil
	}
	state.Set("schema", json.Number(strconv.Itoa(LogicalStateSchema)))
	return ordjson.WriteFile(path, state)
}

func (s *Store) checkRetirementLaunches() error {
	tasks, err := s.AllTasks()
	if err != nil {
		return err
	}
	for _, task := range tasks {
		blocked := func() error {
			return fmt.Errorf("task %s has an unresolved native effect; reconcile it before native retirement", text(task, "id"))
		}
		if text(task, "status") == "preparing" && (text(task, "prepare_operation_id") != "" || number(task, "prepare_operation_pid") > 0) {
			return blocked()
		}
		maintenance := valueObject(task, "terminal_maintenance")
		cleanup := valueObject(task, "cleanup")
		if unsettledEffect(maintenance) || (cleanup != nil && text(cleanup, "state") == "removing") {
			return blocked()
		}
		if text(task, "status") == "archived" {
			continue
		}
		var records []*ordjson.Object
		execution := valueObject(task, "execution")
		if execution != nil {
			worker := valueObject(execution, "worker")
			records = append(records, worker)
			raw, _ := execution.Get("verifiers")
			list, _ := raw.([]any)
			for _, v := range list {
				if row, ok := v.(*ordjson.Object); ok {
					records = append(records, row)
				}
			}
		}
		reviewer, _ := RecordedReviewer(task)
		for _, field := range []string{"prompt", "kickoff"} {
			pending := valueObject(reviewer, field)
			if unsettledEffect(pending) {
				return blocked()
			}
		}
		records = append(records, reviewer)
		for _, record := range records {
			if record == nil {
				continue
			}
			state := text(record, "state")
			launch := valueObject(record, "native_launch")
			redelivery := valueObject(launch, "redelivery")
			if unsettledEffect(redelivery) {
				return blocked()
			}
			effect := ""
			if launch != nil {
				effect = text(launch, "effect")
				if effect == "" {
					effect = text(launch, "state")
				}
			}
			unresolved := effect == "claimed" || effect == "in-flight" || effect == "uncertain" || effect == "unknown"
			if state == "starting" || state == "uncertain" || (unresolved && !(state == "running" && s.receiptConfirmsWorker(task, record))) {
				return fmt.Errorf("task %s has an unresolved launch; reconcile it before native retirement", text(task, "id"))
			}
		}
	}
	return nil
}

func (s *Store) receiptConfirmsWorker(task, attempt *ordjson.Object) bool {
	value := valueObject(task, "worker_endpoint")
	endpoint := EndpointFromContext(value)
	if endpoint.Validate() != nil || endpoint.Role != "worker" || endpoint.Task != text(task, "id") || endpoint.Attempt != text(attempt, "id") {
		return false
	}
	registration, err := s.Registration(endpoint)
	if err != nil || registration == nil {
		return false
	}
	raw, _ := registration.Get("native_receipt")
	encoded, err := ordjson.MarshalCompact(raw)
	if err != nil {
		return false
	}
	var receipt struct {
		Endpoint hcclient.Endpoint `json:"endpoint"`
		Kind     string            `json:"kind"`
		Attempt  string            `json:"attempt"`
	}
	if json.Unmarshal(encoded, &receipt) != nil || receipt.Kind != "worker-init" || receipt.Attempt != endpoint.Attempt || !receipt.Endpoint.SameAuthority(endpoint.Native()) {
		return false
	}
	raw, _ = registration.Get("incarnation")
	encoded, err = ordjson.MarshalCompact(raw)
	if err != nil {
		return false
	}
	var observed struct {
		Endpoint hcclient.Endpoint `json:"endpoint"`
	}
	return json.Unmarshal(encoded, &observed) == nil && observed.Endpoint.SameAuthority(endpoint.Native())
}

func unsettledEffect(record *ordjson.Object) bool {
	if record == nil {
		return false
	}
	for _, key := range []string{"state", "effect"} {
		switch text(record, key) {
		case "claimed", "in-flight", "uncertain", "unknown":
			return true
		}
	}
	return false
}

func valueObject(o *ordjson.Object, key string) *ordjson.Object {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	obj, _ := v.(*ordjson.Object)
	return obj
}

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
