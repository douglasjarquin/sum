package devcmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// A developer terminal is an ordinary shell, with no Sum task registration or
// role receipt. Its marker separates the durable launch from its native effect.
func developerTerminal(s *store.Store, caller *ordjson.Object, path, name string) (*ordjson.Object, error) {
	parent := store.EndpointFromContext(caller)
	if parent.Schema != 2 || parent.Backend != "hc" || parent.CoreID == "" || parent.Session == "" || !filepath.IsAbs(parent.Socket) {
		return nil, fmt.Errorf("verified current local HC context is required for --pane; no core was started")
	}
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	if !host.Is(parent.Machine) {
		return nil, fmt.Errorf("development terminals on another machine are unsupported")
	}
	client := hcclient.Client{Socket: parent.Socket, CoreID: parent.CoreID}
	markerPath := filepath.Join(path, ".sum", "dev.json")
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	marker, err := developmentMarker(path)
	if err != nil || marker == nil {
		unlock()
		return nil, fmt.Errorf("development marker cannot be read: %v", err)
	}
	if value, present := marker.Get("terminal_launch"); present && value != nil {
		record, ok := value.(*ordjson.Object)
		unlock()
		if !ok {
			return nil, fmt.Errorf("development terminal launch record is malformed")
		}
		return observeDeveloperTerminal(client, parent, record)
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	info, err := os.Stat(shell)
	if !filepath.IsAbs(shell) || err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		unlock()
		return nil, fmt.Errorf("SHELL must name an absolute executable file for an ordinary development terminal")
	}
	bytes := make([]byte, 12)
	if _, err = rand.Read(bytes); err != nil {
		unlock()
		return nil, err
	}
	attempt := hex.EncodeToString(bytes)
	params := hcclient.RunParams{Argv: []string{shell}, Cwd: path, NoParent: true, OutliveParent: true, NoNotify: true, Keep: false, RM: false, Name: "sum-dev-" + attempt, Labels: map[string]string{"sum.development": name, "sum.development_path": path, "sum.development_attempt": attempt}, Env: map[string]string{"SUM_HOME": "", "SUM_INSTALL_ROOT": "", "SUM_ROLE": "", "SUM_TASK": "", "SUM_ATTEMPT": "", "SUM_BINDING_GENERATION": "", "SUM_SESSION": parent.Session, "SUM_CORE_ID": parent.CoreID, "HC_SOCKET": parent.Socket}}
	record := ordjson.NewObject()
	record.Set("attempt", attempt)
	record.Set("effect", "claimed")
	record.Set("claimed_at", store.Now())
	intent, _ := ordjson.FromValue(params)
	record.Set("intent", intent)
	route := ordjson.NewObject()
	route.Set("core_id", parent.CoreID)
	route.Set("socket", parent.Socket)
	route.Set("session", parent.Session)
	route.Set("machine", host.ID)
	record.Set("scope", route)
	marker.Set("terminal_launch", record)
	if err = ordjson.WriteFile(markerPath, marker); err != nil {
		unlock()
		return nil, err
	}
	if err = unlock(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, runErr := client.Run(ctx, params)
	unlock, err = s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	marker, err = developmentMarker(path)
	if err != nil || marker == nil {
		return nil, fmt.Errorf("development marker changed during native launch")
	}
	value, _ := marker.Get("terminal_launch")
	current, _ := value.(*ordjson.Object)
	if current == nil || asString(current, "attempt") != attempt {
		return nil, fmt.Errorf("development terminal intent changed during native launch; preserve the returned terminal")
	}
	if runErr != nil {
		effect := hcclient.Uncertain
		var native *hcclient.Error
		if errors.As(runErr, &native) {
			effect = native.Effect
		}
		current.Set("effect", string(effect))
		current.Set("error", runErr.Error())
	} else {
		current.Set("effect", "created")
		terminal, _ := ordjson.FromValue(result.Terminal)
		current.Set("terminal", terminal)
	}
	current.Set("updated_at", store.Now())
	if err = ordjson.WriteFile(markerPath, marker); err != nil {
		return nil, err
	}
	current.Set("note", "Ordinary shell launch recorded; no Sum task or coordinator authority was granted. Repeated prepare observes this intent and never launches it again automatically.")
	return current, nil
}
func observeDeveloperTerminal(client hcclient.Client, parent store.Endpoint, record *ordjson.Object) (*ordjson.Object, error) {
	value, _ := record.Get("scope")
	scope, _ := value.(*ordjson.Object)
	if scope == nil || asString(scope, "core_id") != parent.CoreID || asString(scope, "socket") != parent.Socket || asString(scope, "session") != parent.Session || asString(scope, "machine") != parent.Machine {
		return nil, fmt.Errorf("development terminal intent belongs to another core/session/machine; no replacement was launched")
	}
	record.Set("reused", true)
	value, _ = record.Get("terminal")
	saved, _ := value.(*ordjson.Object)
	if saved == nil {
		record.Set("note", "Native launch has no confirmed terminal address. Inspect the persisted intent and exact core; no retry or replacement was launched.")
		return record, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	current, err := client.Get(ctx, asString(saved, "address"))
	if err != nil {
		record.Set("observation_error", err.Error())
		return record, nil
	}
	savedBytes, marshalErr := ordjson.MarshalCompact(saved)
	var original hcclient.Terminal
	if marshalErr != nil || json.Unmarshal(savedBytes, &original) != nil || current.Address != original.Address || current.CreatedMS != original.CreatedMS {
		record.Set("observation_error", "saved terminal creation no longer matches this address; no replacement was adopted or launched")
		return record, nil
	}
	observed, _ := ordjson.FromValue(current)
	record.Set("observed", observed)
	record.Set("note", "Existing terminal observed; a finished or uncertain launch is never replaced automatically.")
	return record, nil
}
