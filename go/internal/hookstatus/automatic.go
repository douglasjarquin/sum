package hookstatus

import (
	"context"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func AutoEnableAfterInit(ctx context.Context, s *store.Store, caller *ordjson.Object, runtimeRoot, sumctlPath string) *ordjson.Object {
	if err := app.RequireCoordinator(s, caller); err != nil {
		row := ordjson.NewObject()
		row.Set("enabled", false)
		row.Set("degraded", true)
		row.Set("reason", err.Error())
		return row
	}
	row := EnableByDefault(s, caller, runtimeRoot, sumctlPath, false)
	enabled, _ := row.Get("enabled")
	if enabled != true {
		return row
	}
	ready, err := AwaitReady(ctx, s)
	if err != nil {
		row.Set("degraded", true)
		row.Set("readiness", "submitted-unconfirmed")
		row.Set("reason", err.Error())
		return row
	}
	fresh, err := confirmedStartupHealth(s, ready)
	if err != nil {
		row.Set("degraded", true)
		row.Set("readiness", "submitted-unconfirmed")
		row.Set("reason", err.Error())
		return row
	}
	row = fresh
	row.Set("readiness", "ready")
	row.Set("startup", ready)
	return row
}

func EnsureScope(ctx context.Context, s *store.Store, caller *ordjson.Object, target store.Endpoint, runtimeRoot, sumctlPath string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, caller); err != nil {
		return nil, err
	}
	if err := target.Validate(); err != nil {
		return nil, err
	}
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	if !host.Is(target.Machine) {
		return nil, fmt.Errorf("remote observer scopes are unsupported")
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
	return startAndAwait(ctx, s, target, runtimeRoot, sumctlPath)
}
