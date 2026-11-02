package roleinit

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func RedeliverCoordinator(s *store.Store, attempt string) (*ordjson.Object, error) {
	return deliverCoordinatorOpening(s, attempt, false)
}

func deliverCoordinatorOpening(s *store.Store, attempt string, initial bool) (*ordjson.Object, error) {
	path := filepath.Join(s.Home, "coordinator-launch.json")
	read := func() (*ordjson.Object, error) {
		v, err := ordjson.ReadFile(path)
		if err != nil {
			return nil, err
		}
		record, ok := v.(*ordjson.Object)
		if !ok {
			return nil, fmt.Errorf("invalid coordinator launch record")
		}
		return record, nil
	}
	record, err := read()
	if err != nil {
		return nil, err
	}
	ep := store.EndpointFromContext(asObjectCoordinator(record, "endpoint"))
	if attempt == "" || ep.Attempt != attempt || ep.Validate() != nil || ep.Role != "coordinator" {
		return nil, fmt.Errorf("exact created coordinator attempt required")
	}
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	if !host.Is(ep.Machine) {
		return nil, fmt.Errorf("coordinator belongs to another machine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	release, err := s.RecipientLock(ctx, [3]string{ep.Machine, ep.CoreID, ep.Address})
	if err != nil {
		return nil, err
	}
	defer release()
	record, err = read()
	if err != nil {
		return nil, err
	}
	if value, _ := record.Get("native_receipt"); value != nil {
		return record, nil
	}
	delivery := asObjectCoordinator(record, "opening_delivery")
	if state := asString(delivery, "state"); state == "in-flight" || state == "uncertain" || state == "submitted" {
		return nil, fmt.Errorf("coordinator opening already claimed/submitted or ambiguous; inspect without replay")
	}
	if !initial {
		first := asObjectCoordinator(record, "first_message")
		sent, _ := first.Get("sent")
		if asString(record, "effect") != "created" || sent != false {
			return nil, fmt.Errorf("coordinator opening is not proven unsent")
		}
	}
	var result hcclient.RunResult
	if value, _ := record.Get("result"); decode(value, &result) != nil {
		return nil, fmt.Errorf("coordinator creation evidence is missing")
	}
	var qualified nativeTemplate
	if value, _ := record.Get("native_template"); decode(value, &qualified) != nil {
		return nil, fmt.Errorf("coordinator template evidence is missing")
	}
	if err = launch.ValidateHCTemplate(qualified.ConfigHome, qualified.Spec, qualified.Options); err != nil {
		return nil, err
	}
	opening := asString(record, "opening")
	if opening == "" {
		return nil, fmt.Errorf("durable opening is unavailable; inspect this historical attempt")
	}
	if delivery != nil {
		if value, _ := delivery.Get("terminal"); value != nil {
			var observed hcclient.Terminal
			if decode(value, &observed) == nil && launch.OriginalProgram(result.Terminal, observed, qualified.Spec) == nil {
				result.Terminal = observed
			}
		}
	}
	client := hcclient.Client{Socket: ep.Socket, CoreID: ep.CoreID}
	launch.DeliverOpening(ctx, &client, result.Terminal, qualified.Spec, opening, func(progress launch.OpeningProgress) error {
		unlock, err := s.Lock()
		if err != nil {
			return err
		}
		defer unlock()
		current, err := read()
		if err != nil {
			return err
		}
		bound := store.EndpointFromContext(asObjectCoordinator(current, "endpoint"))
		if !bound.Native().SameAuthority(ep.Native()) {
			return fmt.Errorf("coordinator launch changed before opening")
		}
		if value, _ := current.Get("native_receipt"); value != nil {
			return fmt.Errorf("coordinator already initialized; opening was not sent")
		}
		current.Set("opening_delivery", mustObject(progress))
		current.Set("first_message", mustObject(progress.First))
		return ordjson.WriteFile(path, current)
	})
	return read()
}

func asObjectCoordinator(record *ordjson.Object, key string) *ordjson.Object {
	if record == nil {
		return ordjson.NewObject()
	}
	value, _ := record.Get(key)
	result, _ := value.(*ordjson.Object)
	if result == nil {
		return ordjson.NewObject()
	}
	return result
}
