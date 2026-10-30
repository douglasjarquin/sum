package hookstatus

import (
	"context"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
	"path/filepath"
	"time"
)

func AwaitReady(parent context.Context, s *store.Store) (*ordjson.Object, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	changes := make(chan struct{}, 1)
	ready := make(chan struct{})
	failures := make(chan error, 1)
	go func() { failures <- watchDirty(ctx, filepath.Join(s.Home, HookDir), changes, ready) }()
	select {
	case <-ready:
	case err := <-failures:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	enabled, present, err := settings.NativeEventsPreference(s)
	if err != nil {
		return nil, err
	}
	if present && !enabled {
		row := ordjson.NewObject()
		row.Set("enabled", false)
		row.Set("ready", true)
		row.Set("skipped", true)
		return row, nil
	}
	var last error
	for {
		health, err := readHealth(s)
		if err != nil {
			return nil, err
		}
		effect, _ := health.Get("launch_effect")
		if effect == "uncertain" || effect == "unknown" || effect == "claimed" {
			return nil, fmt.Errorf("observer launch/stop effect is unresolved; no replacement or active observer is inferred")
		}
		endpoint := store.EndpointFromContext(asObject(func() any { v, _ := health.Get("observer"); return v }()))
		if endpoint.CoreID != "" {
			if value, err := Compatibility(ctx, s); err == nil {
				if running, _ := value.Get("running"); running == true && healthReadyFor(health, asObject(func() any { v, _ := value.Get("observer"); return v }())) {
					value.Set("ready", true)
					for _, key := range []string{"launch_attempt", "connected_at", "snapshot_seq"} {
						v, _ := health.Get(key)
						value.Set(key, v)
					}
					return value, nil
				}
			} else {
				last = err
			}
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("selected but observer startup remains unconfirmed: %v (%v)", ctx.Err(), last)
		case err := <-failures:
			return nil, err
		case <-changes:
		}
	}
}

func healthReadyFor(health, endpointRecord *ordjson.Object) bool {
	endpoint := store.EndpointFromContext(endpointRecord)
	current := store.EndpointFromContext(asObject(value(health, "observer")))
	return endpoint.Validate() == nil && endpoint.Native().SameAuthority(current.Native()) && value(health, "launch_attempt") == endpoint.Attempt && asString(value(health, "connected_at")) != "" && value(health, "snapshot_seq") != nil && value(health, "observer_receipt") != nil && value(health, "observer_incarnation") != nil
}
func confirmedStartupHealth(s *store.Store, ready *ordjson.Object) (*ordjson.Object, error) {
	health, err := readHealth(s)
	if err != nil {
		return nil, err
	}
	if !healthReadyFor(health, asObject(value(ready, "observer"))) {
		return nil, fmt.Errorf("observer startup binding changed after readiness proof")
	}
	return health, nil
}
