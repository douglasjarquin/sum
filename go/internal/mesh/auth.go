package mesh

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func (s Service) authorize(args []string) error {
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
	for _, candidate := range append([]string{host.ID}, host.Legacy()...) {
		key := store.RegistrationKey(store.Endpoint{Machine: candidate, Session: s.config.Session, Pane: s.config.Pane})
		if registration, err = readObject(filepath.Join(s.config.StateHome, "sessions", key+".json")); err == nil {
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
	return nil
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
