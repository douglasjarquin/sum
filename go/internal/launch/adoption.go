package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type SavedLaunch struct {
	Record      *ordjson.Object
	Incarnation any
	Template    any
	Intent      any
}

type Locate func(task *ordjson.Object) (SavedLaunch, error)

func decodeAny(value any, target any) error {
	if value == nil {
		return fmt.Errorf("missing")
	}
	return ordjson.DecodeValue(value, target)
}

func savedVisibility(record *ordjson.Object) (Visibility, bool) {
	if record == nil {
		return Visibility{}, false
	}
	value, ok := record.Get("visibility")
	if !ok || value == nil {
		return Visibility{}, false
	}
	var intent Visibility
	if decodeAny(value, &intent) != nil || len(intent.Labels) == 0 {
		return Visibility{}, false
	}
	return intent, true
}

type recordedProcess struct {
	PID        uint32               `json:"pid"`
	Foreground *hcclient.Foreground `json:"foreground"`
}

func Adoption(ctx context.Context, s *store.Store, ep store.Endpoint, client *hcclient.Client, locate Locate) func() error {
	return func() error {
		return PublishVisibility(ctx, client, ep.Address, func() (map[string]string, error) {
			unlock, err := s.Lock()
			if err != nil {
				return nil, err
			}
			defer unlock()
			current, err := s.ReadTask(ep.Task)
			if err != nil {
				return nil, err
			}
			saved, err := locate(current)
			if err != nil {
				return nil, err
			}
			intent, ok := savedVisibility(saved.Record)
			if !ok || intent.State != VisibilityPending {
				return nil, nil
			}
			var process recordedProcess
			if decodeAny(saved.Incarnation, &process) != nil || process.PID == 0 || process.Foreground == nil {
				return nil, nil
			}
			intent.State = VisibilityInFlight
			intent.At = store.Now()
			saved.Record.Set("visibility", intent.Object())
			return intent.Labels, s.SaveTask(current)
		}, func(outcome Visibility) error {
			return recordVisibility(s, ep, locate, outcome)
		})
	}
}

func terminalAbsent(err error) bool {
	var rpc *hcclient.RPCError
	if !errors.As(err, &rpc) {
		return false
	}
	var data struct {
		Kind string `json:"kind"`
	}
	return json.Unmarshal(rpc.Data, &data) == nil && data.Kind == "not_found"
}

func recordVisibility(s *store.Store, ep store.Endpoint, locate Locate, outcome Visibility) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	current, err := s.ReadTask(ep.Task)
	if err != nil {
		return err
	}
	saved, err := locate(current)
	if err != nil {
		return err
	}
	outcome.At = store.Now()
	saved.Record.Set("visibility", outcome.Object())
	return s.SaveTask(current)
}

func ReconcileVisibility(ctx context.Context, s *store.Store, ep store.Endpoint, locate Locate) error {
	task, err := s.ReadTask(ep.Task)
	if err != nil {
		return err
	}
	saved, err := locate(task)
	if err != nil {
		return err
	}
	intent, ok := savedVisibility(saved.Record)
	if !ok || intent.Settled() {
		return nil
	}
	var process recordedProcess
	if err := decodeAny(saved.Incarnation, &process); err != nil || process.PID == 0 {
		return fmt.Errorf("adoption needs the recorded original process; inspect the attempt without labeling")
	}
	var template struct {
		Spec HCTemplate `json:"spec"`
	}
	if err := decodeAny(saved.Template, &template); err != nil {
		return err
	}
	var params hcclient.RunParams
	if err := decodeAny(saved.Intent, &params); err != nil {
		return err
	}
	client := hcclient.Client{Socket: ep.Socket, CoreID: ep.CoreID}
	observed, err := client.Get(ctx, ep.Address)
	if terminalAbsent(err) {
		return recordVisibility(s, ep, locate, Visibility{Labels: intent.Labels, State: VisibilityRefused, Detail: "terminal is no longer known to the core; nothing was adopted"})
	}
	if err != nil {
		return err
	}
	if observed.State != "live" {
		return recordVisibility(s, ep, locate, Visibility{Labels: intent.Labels, State: VisibilityRefused, Observed: herdrLabels(observed.Labels), Detail: "program exited before adoption; nothing was adopted"})
	}
	initial := hcclient.Terminal{Address: ep.Address, CreatedMS: ep.CreatedMS, PID: &process.PID, Foreground: process.Foreground, Cwd: params.Cwd, Labels: params.Labels}
	if err := OriginalProgram(initial, observed, template.Spec); err != nil {
		return fmt.Errorf("adoption refused: %w", err)
	}
	if err := recordVisibility(s, ep, locate, ObservedVisibility(intent, observed)); err != nil {
		return err
	}
	return Adoption(ctx, s, ep, &client, locate)()
}
