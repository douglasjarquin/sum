package hookstatus

import (
	"context"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/refreshcmd"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// Event refuses obsolete environment callbacks. HC subscriptions are the only
// runtime event source; a forged environment callback cannot trigger a send.
func Event(s *store.Store, environ map[string]string, runtimeRoot, sumctlPath string) (*ordjson.Object, error) {
	return nil, fmt.Errorf("legacy plugin callbacks are retired; run hook observe in its recorded HC observer terminal")
}

// Reconcile drains budget-deferred recipients fairly, without waiting for
// another native event. Submitted and uncertain attempts are never replayed.
func Reconcile(ctx context.Context, s *store.Store, endpoint store.Endpoint, runtimeRoot, sumctlPath string) (*ordjson.Object, error) {
	if err := reconcileAttention(ctx, s, endpoint); err != nil {
		return nil, err
	}
	if _, err := refreshcmd.ReconcileCoordinator(s, endpoint, runtimeRoot, sumctlPath); err != nil {
		return nil, err
	}
	var row *ordjson.Object
	visited := map[string]bool{}
	for {
		var err error
		row, err = returns.Pump(s, returns.PumpOpts{RuntimeRoot: runtimeRoot, SumctlPath: sumctlPath, CoreID: endpoint.CoreID, Session: endpoint.Session, Parent: ctx, RetryStalled: true})
		if err != nil {
			return nil, err
		}
		deferred, _ := row.Get("deferred")
		if fmt.Sprint(deferred) == "0" || ctx.Err() != nil {
			return row, ctx.Err()
		}
		recipients, _ := row.Get("recipients")
		progress := false
		for _, raw := range asList(recipients) {
			if item := asObject(raw); item != nil {
				state, _ := item.Get("state")
				if state != "deferred" {
					recipient, _ := item.Get("recipient")
					encoded, _ := ordjson.MarshalCompact(recipient)
					key := string(encoded)
					if !visited[key] {
						visited[key] = true
						progress = true
					}
				}
			}
		}
		if !progress {
			return row, nil
		}
	}
}
