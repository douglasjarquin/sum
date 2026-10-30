package roleinit

import (
	"context"
	"fmt"
	"reflect"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func labelCoordinator(ctx context.Context, s *store.Store, client hcclient.Client, e store.Endpoint, observed incarnation.NativeEvidence, t hcclient.Terminal) (incarnation.NativeEvidence, hcclient.Terminal, error) {
	instance, err := s.Instance()
	if err != nil {
		return observed, t, err
	}
	if instance == "" {
		return observed, t, fmt.Errorf("installation identity required before coordinator event subscription")
	}
	labels := make(map[string]string, len(t.Labels)+3)
	for key, value := range t.Labels {
		labels[key] = value
	}
	changed := false
	for key, want := range map[string]string{"sum.installation": instance, "sum.role": "coordinator", "sum.attempt": e.Attempt} {
		if labels[key] != want {
			labels[key] = want
			changed = true
		}
	}
	if !changed {
		return observed, t, nil
	}
	if err = s.CheckWritable(); err != nil {
		return observed, t, err
	}
	updated, err := client.Label(ctx, e.Address, labels)
	if err != nil {
		return observed, t, fmt.Errorf("coordinator discovery labels not confirmed; no role recorded: %w", err)
	}
	if updated.Address != e.Address || updated.CreatedMS != e.CreatedMS || updated.State != "live" || updated.Keep || updated.PID == nil || *updated.PID != observed.PID || !reflect.DeepEqual(updated.Foreground, observed.Foreground) || updated.Cwd != t.Cwd || !reflect.DeepEqual(updated.Argv, t.Argv) {
		return observed, t, fmt.Errorf("coordinator terminal process changed while discovery labels were applied; no role recorded")
	}
	for key, want := range labels {
		if updated.Labels[key] != want {
			return observed, t, fmt.Errorf("coordinator discovery labels did not preserve %s; no role recorded", key)
		}
	}
	return observed, updated, nil
}
