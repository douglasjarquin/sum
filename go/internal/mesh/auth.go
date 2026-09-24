package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func (s Service) authorize(ctx context.Context, args []string) error {
	if s.config.Session == "" || s.config.Pane == "" {
		return fmt.Errorf("Herdr scope is incomplete")
	}
	statePath := filepath.Join(s.config.StateHome, "state.json")
	state, err := readObject(statePath)
	if err != nil {
		return fmt.Errorf("read Sum state: %w", err)
	}
	if _, ok := state["instance"].(string); !ok {
		return fmt.Errorf("Sum state has no registered instance")
	}
	host, err := machine.Local(s.config.StateHome)
	if err != nil {
		return err
	}
	var registration map[string]any
	var registrationPath string
	for _, candidate := range append([]string{host.ID}, host.Legacy()...) {
		key := store.RegistrationKey(store.Endpoint{Machine: candidate, Session: s.config.Session, Pane: s.config.Pane})
		registrationPath = filepath.Join(s.config.StateHome, "sessions", key+".json")
		if registration, err = readObject(registrationPath); err == nil {
			break
		}
	}
	if registration == nil {
		return fmt.Errorf("pane %s in session %s is not registered with %s; run ./bin/sumctl init there first", s.config.Pane, s.config.Session, s.config.StateHome)
	}
	if !host.Is(registration["machine"]) || registration["session"] != s.config.Session || registration["pane"] != s.config.Pane {
		return fmt.Errorf("Sum session registration identity mismatch")
	}
	if registration["instance"] != state["instance"] {
		return fmt.Errorf("Sum session registration belongs to another instance")
	}
	if registration["role"] == "developer" && !readOnly(args) {
		return fmt.Errorf("developer sessions may only observe through the bridge")
	}
	if !readOnly(args) {
		// Only the occupant the registration's role was granted to may act; observing needs nothing.
		recorded, occupiedAt, err := s.roleRecord(host, registrationPath, registration["role"])
		if err != nil {
			return err
		}
		if verdict := incarnation.Pane(s.call(ctx), s.config.Pane, recorded, occupiedAt); !verdict.Verified {
			return fmt.Errorf("pane %s is registered as %v, but its occupant is not the one that role was granted to (%s: %s); run ./bin/sumctl init there. %s", s.config.Pane, registration["role"], verdict.Outcome, verdict.Reason, incarnation.Recovery(fmt.Sprint(registration["role"]), verdict.Outcome))
		}
	}
	return nil
}

// roleRecord is the incarnation this pane's role is held by: the owner's for the coordinator while the owner names this
// pane, the registration's own otherwise.
func (s Service) roleRecord(host machine.Identity, registrationPath string, role any) (any, string, error) {
	if role == "coordinator" {
		ownerValue, err := ordjson.ReadFile(filepath.Join(s.config.StateHome, "context.json"))
		if err != nil {
			return nil, "", nil
		}
		owner, _ := ownerValue.(*ordjson.Object)
		if owner == nil || !host.SameEndpoint(owner, endpointObject(s.config.Session, s.config.Pane, host.ID)) {
			return nil, "", nil
		}
		recorded, occupiedAt := incarnation.CoordinatorRecord(owner)
		return recorded, occupiedAt, nil
	}
	value, err := ordjson.ReadFile(registrationPath)
	if err != nil {
		return nil, "", err
	}
	registration, _ := value.(*ordjson.Object)
	recorded, occupiedAt := incarnation.RegistrationRecord(registration)
	return recorded, occupiedAt, nil
}

func endpointObject(session, pane, machineID string) *ordjson.Object {
	obj := ordjson.NewObject()
	obj.Set("machine", machineID)
	obj.Set("session", session)
	obj.Set("pane", pane)
	return obj
}

// call observes this server's own Herdr session through its runner, for the occupant check.
func (s Service) call(ctx context.Context) incarnation.Call {
	return func(timeout time.Duration, args ...string) (any, string, error) {
		result, err := s.runner.Run(ctx, args, timeout, false)
		if err != nil {
			return nil, "", err
		}
		value, err := ordjson.Decode([]byte(result.stdout))
		if err != nil {
			return nil, "", fmt.Errorf("Herdr did not return JSON: %s", trim(result.stdout))
		}
		if obj, ok := value.(*ordjson.Object); ok {
			if inner, has := obj.Get("result"); has {
				return inner, "", nil
			}
		}
		return value, "", nil
	}
}

func readOnly(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[0] + " " + args[1] {
	case "agent list", "agent get", "agent read", "agent wait", "pane get", "pane read", "pane list", "workspace list", "integration status", "session list":
		return true
	default:
		return false
	}
}

func readObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return value, nil
}
