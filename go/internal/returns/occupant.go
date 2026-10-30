package returns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type occupant struct{ native *NativeRecipient }

func nativeRecord(s *store.Store, route *ordjson.Object) (*ordjson.Object, error) {
	if routeValue(route, "role") == "coordinator" {
		return s.Owner()
	}
	return s.Registration(store.EndpointFromContext(route))
}
func recordFor(s *store.Store, route *ordjson.Object, items [][2]*ordjson.Object) (any, string, bool, error) {
	record, err := nativeRecord(s, route)
	if err != nil || record == nil {
		return nil, "", false, err
	}
	value, _ := record.Get("incarnation")
	return value, "", value != nil, nil
}
func checkOccupant(s *store.Store, route *ordjson.Object, items [][2]*ordjson.Object, occ *occupant) string {
	if occ == nil || occ.native == nil {
		return "No fresh HC process observation; nothing was sent."
	}
	record, err := nativeRecord(s, route)
	if err != nil {
		return err.Error()
	}
	if record == nil {
		return "No current native recipient binding; nothing was sent."
	}
	current := store.EndpointFromContext(record)
	expected := store.EndpointFromContext(route)
	host, hostErr := s.Machine()
	if hostErr != nil {
		return hostErr.Error()
	}
	current.Machine = host.Canonical(current.Machine)
	expected.Machine = host.Canonical(expected.Machine)
	if !current.Native().SameAuthority(expected.Native()) {
		return "Recipient binding changed during observation; nothing was sent."
	}
	if len(occ.native.qualification) > 0 {
		raw, _ := record.Get("native_template")
		now, err := ordjson.MarshalCompact(raw)
		if err != nil || !bytes.Equal(now, occ.native.qualification) {
			return "Recipient qualification changed during observation; nothing was sent."
		}
	}
	evidence, _ := record.Get("incarnation")
	raw, _ := record.Get("native_receipt")
	data, err := ordjson.MarshalCompact(raw)
	if err != nil {
		return err.Error()
	}
	var receipt incarnation.NativeReceipt
	if err = json.Unmarshal(data, &receipt); err != nil {
		return "No matching native application bootstrap receipt; nothing was sent."
	}
	verdict := incarnation.JudgeNative(evidence, current.Native(), occ.native.Terminal, &receipt)
	if !verdict.Verified {
		return fmt.Sprintf("Recipient is not the recorded %s occupant (%s: %s); explicit recovery is required.", current.Role, verdict.Outcome, verdict.Reason)
	}
	return ""
}
func (p *pass) checkOccupant(route *ordjson.Object, items [][2]*ordjson.Object, occ *occupant) string {
	return checkOccupant(p.s, route, items, occ)
}
func (p *pass) checkInline(route *ordjson.Object, items [][2]*ordjson.Object) (string, string) {
	if !p.fits(ObserveTimeout) {
		return "", "the pass budget ran out before native recipient observation"
	}
	recipient, err := ObserveNative(p.ctx, store.EndpointFromContext(route), fmt.Sprint(routeValue(route, "cwd")))
	if err != nil {
		return err.Error(), ""
	}
	return checkOccupant(p.s, route, items, &occupant{native: recipient}), ""
}

type Occupant struct {
	occ   *occupant
	items [][2]*ordjson.Object
	agent *ordjson.Object
}

func ObserveWorker(s *store.Store, runtimeRoot string, task, route *ordjson.Object, expectedCwd string) (*Occupant, error) {
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	if !host.Is(routeValue(route, "machine")) {
		return nil, fmt.Errorf("recipient is on another machine")
	}
	recipient, err := ObserveNative(context.Background(), store.EndpointFromContext(route), expectedCwd)
	if err != nil {
		return nil, err
	}
	record, err := nativeRecord(s, route)
	if err != nil {
		return nil, err
	}
	if err = recipient.Qualify(record); err != nil {
		return nil, err
	}
	agent, _ := ordjson.FromValue(recipient.Terminal)
	return &Occupant{occ: &occupant{native: recipient}, items: [][2]*ordjson.Object{{task, nil}}, agent: agent}, nil
}
func (o *Occupant) Agent() *ordjson.Object { return o.agent }
func (o *Occupant) Check(s *store.Store, route *ordjson.Object) string {
	return checkOccupant(s, route, o.items, o.occ)
}
func (o *Occupant) Send(ctx context.Context, text string) (string, error) {
	_, err := o.occ.native.Send(ctx, text)
	state, _ := DeliveryFailure(err)
	return state, err
}

// Check revalidates the observed native recipient against the current ledger.
// The caller holds the state lock; this method performs no runtime I/O.
func (r *NativeRecipient) Check(s *store.Store) error {
	if msg := checkOccupant(s, r.Endpoint.Object(), nil, &occupant{native: r}); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	return nil
}
