package mesh

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	machine, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("identify machine: %w", err)
	}
	key := registrationKey(machine, s.config.Session, s.config.Pane)
	registration, err := readObject(filepath.Join(s.config.StateHome, "sessions", key+".json"))
	if err != nil {
		return fmt.Errorf("pane %s in session %s is not registered with %s; run ./bin/sumctl init there first", s.config.Pane, s.config.Session, s.config.StateHome)
	}
	if registration["machine"] != machine || registration["session"] != s.config.Session || registration["pane"] != s.config.Pane {
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

func registrationKey(machine, session, pane string) string {
	hash := sha256.Sum256([]byte(machine + "\n" + session + "\n" + pane))
	return hex.EncodeToString(hash[:])[:16]
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
