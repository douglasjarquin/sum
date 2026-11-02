package roleinit

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func presentCoordinator(s *store.Store, record *ordjson.Object, space string) (*ordjson.Object, error) {
	ep := store.EndpointFromContext(asObjectCoordinator(record, "endpoint"))
	var progress launch.OpeningProgress
	value, _ := record.Get("opening_delivery")
	if decode(value, &progress) != nil || progress.Terminal.Foreground == nil {
		record.Set("presentation_error", "original foreground not observed; use inspect and native attach before desktop adoption")
		return record, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := hcclient.Client{Socket: ep.Socket, CoreID: ep.CoreID}
	observed, err := client.Get(ctx, ep.Address)
	if err != nil {
		return record, err
	}
	if observed.CreatedMS != ep.CreatedMS || observed.PID == nil || progress.Terminal.PID == nil || *observed.PID != *progress.Terminal.PID || !reflect.DeepEqual(observed.Foreground, progress.Terminal.Foreground) {
		return record, fmt.Errorf("coordinator program changed before desktop placement")
	}
	labels := map[string]string{}
	for key, value := range observed.Labels {
		labels[key] = value
	}
	labels["herdr.space"] = space
	updated, err := client.Label(ctx, ep.Address, labels)
	if err != nil {
		return record, err
	}
	if updated.Address != ep.Address || updated.CreatedMS != ep.CreatedMS || !reflect.DeepEqual(updated.PID, observed.PID) || !reflect.DeepEqual(updated.Foreground, observed.Foreground) || updated.Labels["herdr.space"] != space {
		return record, fmt.Errorf("desktop placement response changed original process identity")
	}
	unlock, err := s.Lock()
	if err != nil {
		return record, err
	}
	defer unlock()
	path := filepath.Join(s.Home, "coordinator-launch.json")
	raw, err := ordjson.ReadFile(path)
	if err != nil {
		return record, err
	}
	current, ok := raw.(*ordjson.Object)
	if !ok {
		return record, fmt.Errorf("coordinator launch record is unreadable")
	}
	current.Set("presentation", mustObject(map[string]any{"space": space, "core_id": ep.CoreID, "address": ep.Address, "placement_label_confirmed": true, "desktop_visibility": "requires observation in the associated running desktop", "inspect": "sumctl inspect --role coordinator --attempt " + ep.Attempt}))
	if err = ordjson.WriteFile(path, current); err != nil {
		return record, err
	}
	return current, nil
}
