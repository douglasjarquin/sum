package roleinit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func RecoverCoordinator(s *store.Store, attempt string) (*ordjson.Object, error) {
	if !s.Designated() {
		return nil, fmt.Errorf("coordinator recovery requires the designated installation")
	}
	if attempt == "" || len(attempt) > 128 || strings.Trim(attempt, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
		return nil, fmt.Errorf("exact coordinator --attempt required")
	}
	owner, err := s.Owner()
	if err != nil {
		return nil, err
	}
	if owner != nil {
		return nil, fmt.Errorf("coordinator already registered; recovery cannot replace its authority")
	}
	path := filepath.Join(s.Home, "coordinator-launch.json")
	archive := filepath.Join(s.Home, "coordinator-launches", attempt+".json")
	before, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		value, readErr := ordjson.ReadFile(archive)
		if readErr != nil {
			return nil, fmt.Errorf("no pending or recovered coordinator attempt %s", attempt)
		}
		record, ok := value.(*ordjson.Object)
		if !ok || asString(record, "effect") != "not-created" {
			return nil, fmt.Errorf("invalid recovered coordinator attempt")
		}
		return record, nil
	}
	if err != nil {
		return nil, err
	}
	value, err := ordjson.Decode(before)
	if err != nil {
		return nil, err
	}
	record, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("invalid coordinator launch record")
	}
	endpointValue, _ := record.Get("endpoint")
	endpointObject, _ := endpointValue.(*ordjson.Object)
	endpoint := store.EndpointFromContext(endpointObject)
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	if endpoint.Attempt != attempt || endpoint.Role != "coordinator" || endpoint.Backend != "hc" || endpoint.Schema != 2 || !host.Is(endpoint.Machine) || endpoint.CoreID == "" || !filepath.IsAbs(endpoint.Socket) || endpoint.Address != "" || endpoint.CreatedMS != 0 {
		return nil, fmt.Errorf("recovery requires the exact local uncreated coordinator attempt; recorded identity differs")
	}
	if !uncreatedCoordinator(record) {
		return nil, fmt.Errorf("coordinator launch is ambiguous or created; absence alone cannot prove it was never started; no retry authorized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID}
	list, err := client.List(ctx, hcclient.ListParams{All: true})
	if err != nil {
		return nil, fmt.Errorf("observe recorded HC core before recovery: %w", err)
	}
	for _, terminal := range list.Terminals {
		if terminal.Labels["sum.attempt"] == attempt || (terminal.Name != nil && *terminal.Name == "sum-"+attempt[:min(28, len(attempt))]) {
			return nil, fmt.Errorf("coordinator attempt has native terminal %s (%s); inspect it without relaunching", terminal.Address, terminal.State)
		}
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	owner, err = s.Owner()
	if err != nil {
		return nil, err
	}
	if owner != nil {
		return nil, fmt.Errorf("coordinator already registered; recovery cannot replace its authority")
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(before, current) {
		return nil, fmt.Errorf("coordinator launch changed during observation; inspect again")
	}
	if _, err = os.Lstat(archive); !os.IsNotExist(err) {
		return nil, fmt.Errorf("coordinator recovery archive already exists; preserved")
	}
	record.Set("effect", "not-created")
	record.Set("recovery", mustObject(map[string]any{"attempt": attempt, "at": store.Now(), "core_id": endpoint.CoreID, "reason": "proven pre-creation refusal and no matching native terminal"}))
	if err = os.MkdirAll(filepath.Dir(archive), 0700); err != nil {
		return nil, err
	}
	if err = ordjson.WriteFile(path, record); err != nil {
		return nil, err
	}
	if err = os.Rename(path, archive); err != nil {
		return nil, err
	}
	return record, nil
}

func uncreatedCoordinator(record *ordjson.Object) bool {
	if receipt, ok := record.Get("native_receipt"); ok && receipt != nil {
		return false
	}
	effect := asString(record, "effect")
	if effect == "not-submitted" || effect == "not-created" {
		return true
	}
	if effect != "uncertain" && effect != "refused" {
		return false
	}
	if raw, ok := record.Get("native_error"); ok {
		data, err := ordjson.MarshalCompact(raw)
		var rpc hcclient.RPCError
		return err == nil && json.Unmarshal(data, &rpc) == nil && hcclient.RunRefusedBeforeCreation(&rpc)
	}
	text := asString(record, "error")
	prefix := "HC terminal.run (" + effect + "): RPC -32000: "
	if !strings.HasPrefix(text, prefix) || !strings.HasSuffix(text, ")") {
		return false
	}
	message, data, ok := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(text, prefix), ")"), " ({")
	if !ok {
		return false
	}
	return hcclient.RunRefusedBeforeCreation(&hcclient.RPCError{Code: -32000, Message: message, Data: json.RawMessage("{" + data)})
}
