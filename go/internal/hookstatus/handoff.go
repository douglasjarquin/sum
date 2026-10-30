package hookstatus

import (
	"context"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// Compatibility observes saved observer ownership without starting a core or
// helper. Absence and explicit disablement need no running observer to preserve.
func Compatibility(ctx context.Context, s *store.Store) (*ordjson.Object, error) {
	if err := s.CheckWritable(); err != nil {
		return nil, err
	}
	health, err := readHealth(s)
	if err != nil {
		return nil, err
	}
	effect, _ := health.Get("launch_effect")
	if effect == "in-flight" || effect == "claimed" || effect == "uncertain" || effect == "unknown" {
		return nil, fmt.Errorf("observer launch/stop effect is unresolved; inspect the exact scope before activation or replacement")
	}
	enabled, present, err := settings.NativeEventsPreference(s)
	if err != nil {
		return nil, err
	}
	row := ordjson.NewObject()
	row.Set("enabled", enabled)
	if present && !enabled {
		row.Set("compatible", true)
		row.Set("reason", "native events explicitly disabled")
		return row, nil
	}
	endpoint := store.EndpointFromContext(asObject(func() any { v, _ := health.Get("observer"); return v }()))
	if endpoint.CoreID == "" {
		row.Set("compatible", true)
		row.Set("running", false)
		return row, nil
	}
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID, Timeout: returns.ObserveTimeout}
	terminal, err := client.Get(ctx, endpoint.Address)
	if err != nil {
		return nil, err
	}
	if err = observerProof(health, endpoint, terminal); err != nil {
		return nil, err
	}
	row.Set("compatible", true)
	row.Set("running", true)
	row.Set("observer", endpoint.Object())
	return row, nil
}

// Handoff replaces only the proven-owned helper. A lost stop response leaves
// uncertainty and blocks a replacement. It never changes delivery preferences.
func Handoff(ctx context.Context, s *store.Store, caller *ordjson.Object, runtimeRoot, sumctlPath string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, caller); err != nil {
		return nil, err
	}
	health, err := readHealth(s)
	if err != nil {
		return nil, err
	}
	effect, _ := health.Get("launch_effect")
	if effect == "in-flight" || effect == "claimed" || effect == "uncertain" || effect == "unknown" {
		return nil, fmt.Errorf("observer launch/stop effect is unresolved; inspect the exact scope before activation or replacement")
	}
	enabled, present, err := settings.NativeEventsPreference(s)
	if err != nil {
		return nil, err
	}
	if present && !enabled {
		row := ordjson.NewObject()
		row.Set("enabled", false)
		row.Set("skipped", true)
		return row, nil
	}
	endpoint := store.EndpointFromContext(asObject(func() any { v, _ := health.Get("observer"); return v }()))
	if endpoint.CoreID == "" {
		return startAndAwait(ctx, s, store.EndpointFromContext(caller), runtimeRoot, sumctlPath)
	}
	release, err := scopeLock(s, endpoint, "launch")
	if err != nil {
		return nil, err
	}
	releaseLease, err := s.DeliveryShared(ctx)
	if err != nil {
		release()
		return nil, err
	}
	releasedLease := false
	defer func() {
		if !releasedLease {
			releaseLease()
		}
	}()
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID, Timeout: returns.ObserveTimeout}
	terminal, err := client.Get(ctx, endpoint.Address)
	if err == nil {
		err = observerProof(health, endpoint, terminal)
	}
	if err != nil {
		release()
		return nil, err
	}
	if err = s.CheckWritable(); err != nil {
		release()
		return nil, err
	}
	intent := ordjson.NewObject()
	intent.Set("launch_effect", "uncertain")
	intent.Set("degraded", "observer handoff stop in progress; replacement requires proven exit")
	if _, err = writeHealth(s, nil, intent); err != nil {
		release()
		return nil, err
	}
	if _, err = client.Kill(ctx, endpoint.Address); err != nil {
		release()
		return nil, fmt.Errorf("observer stop effect is unconfirmed; no replacement launched: %w", err)
	}
	terminal, err = client.Get(ctx, endpoint.Address)
	if err != nil || terminal.State == "live" {
		release()
		return nil, fmt.Errorf("observer exit is not proven; no replacement launched")
	}
	stopLock, err := scopeLock(s, endpoint, "running")
	if err != nil {
		release()
		return nil, fmt.Errorf("observer process still owns its scope; no replacement launched: %w", err)
	}
	stopLock()
	changes := ordjson.NewObject()
	changes.Set("observer", nil)
	changes.Set("observer_incarnation", nil)
	changes.Set("observer_receipt", nil)
	changes.Set("launch_effect", "not-submitted")
	if _, err = writeHealth(s, nil, changes); err != nil {
		release()
		return nil, err
	}
	release()
	releaseLease()
	releasedLease = true
	return startAndAwait(ctx, s, store.EndpointFromContext(caller), runtimeRoot, sumctlPath)
}

func startAndAwait(ctx context.Context, s *store.Store, endpoint store.Endpoint, runtimeRoot, sumctlPath string) (*ordjson.Object, error) {
	created, err := ensureObserver(s, endpoint, runtimeRoot, sumctlPath, true)
	if err != nil {
		return nil, err
	}
	ready, err := AwaitReady(ctx, s)
	if err != nil {
		created.Set("readiness", "submitted-unconfirmed")
		created.Set("reason", err.Error())
		return created, err
	}
	created.Set("readiness", "ready")
	created.Set("startup", ready)
	return created, nil
}
