package roleinit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func ReconcileCoordinator(s *store.Store, attempt string) (*ordjson.Object, error) {
	path := filepath.Join(s.Home, "coordinator-launch.json")
	before, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := ordjson.Decode(before)
	if err != nil {
		return nil, err
	}
	record, ok := raw.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("invalid coordinator intent")
	}
	ep := store.EndpointFromContext(asObjectCoordinator(record, "endpoint"))
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	if attempt == "" || ep.Attempt != attempt || !host.Is(ep.Machine) || ep.Role != "coordinator" {
		return nil, fmt.Errorf("exact local coordinator attempt required")
	}
	if ep.Validate() == nil {
		return record, nil
	}
	if value, _ := record.Get("opening_delivery"); value != nil {
		return nil, fmt.Errorf("opening may have been delivered; inspect without replay")
	}
	var params hcclient.RunParams
	var template nativeTemplate
	if value, _ := record.Get("intent"); decode(value, &params) != nil {
		return nil, fmt.Errorf("invalid creation intent")
	}
	if value, _ := record.Get("native_template"); decode(value, &template) != nil {
		return nil, fmt.Errorf("invalid template provenance")
	}
	client := hcclient.Client{Socket: ep.Socket, CoreID: ep.CoreID}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	terminal, err := launch.DiscoverOriginal(ctx, &client, params, template.Spec, asString(record, "claimed_at"))
	if err != nil {
		return nil, err
	}
	ep.Address, ep.CreatedMS = terminal.Address, terminal.CreatedMS
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(current, before) {
		return nil, fmt.Errorf("coordinator record changed during discovery; inspect again")
	}
	record.Set("endpoint", ep.Object())
	record.Set("effect", "created")
	first := &hcclient.FirstMessage{Sent: false}
	record.Set("first_message", mustObject(first))
	record.Set("result", mustObject(hcclient.RunResult{Terminal: terminal, FirstMessage: first}))
	if err = ordjson.WriteFile(path, record); err != nil {
		return nil, err
	}
	return record, nil
}
