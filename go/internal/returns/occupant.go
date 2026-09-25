package returns

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/panes"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

// occupant is what one fresh observation shows in a recipient pane: the terminal and native session from the agent
// or pane object, and the shell when judging the record needed it. It is taken outside the state lock and judged
// inside it, so a record that changed in between is judged afresh and a probe it needs but lacks refuses the prompt.
type occupant struct {
	evidence incarnation.Evidence
	probed   bool
	shell    *incarnation.Shell
	shellErr error
}

var errNotProbed = errors.New("the pane shell was not observed before the claim")

func (o *occupant) probe() (*incarnation.Shell, error) {
	if !o.probed {
		return nil, errNotProbed
	}
	return o.shell, o.shellErr
}

// call adapts the pass snapshot to one incarnation observation call in session, bounded like every pass observation.
func (p *pass) call(session string) incarnation.Call {
	return func(_ time.Duration, args ...string) (any, string, error) {
		value, err := p.sn.Call(session, ObserveTimeout, args...)
		return value, "", err
	}
}

// recordFor is the incarnation recorded for route's recipient role, read now: the owner's for the coordinator, the
// task's worker registration for a worker. ok is false when there is no such record to judge against.
func recordFor(s *store.Store, route *ordjson.Object, items [][2]*ordjson.Object) (recorded any, occupiedAt string, ok bool, err error) {
	if fmt.Sprint(routeValue(route, "role")) == "coordinator" {
		owner, err := s.Owner()
		if err != nil || owner == nil {
			return nil, "", false, err
		}
		recorded, occupiedAt = incarnation.CoordinatorRecord(owner)
		return recorded, occupiedAt, true, nil
	}
	registration, err := s.Registration(store.EndpointFromContext(route))
	if err != nil {
		return nil, "", false, err
	}
	for _, pair := range items {
		id, _ := pair[0].Get("id")
		if recorded, occupiedAt, ok := incarnation.WorkerRecord(registration, id); ok {
			return recorded, occupiedAt, true, nil
		}
	}
	return nil, "", false, nil
}

// observeOccupant builds the occupant from the fresh agent observation and, when judging the current record needs the
// shell (the terminal changed, or the record is legacy), probes it now within the pass budget. A non-empty deferReason
// means the probe did not fit: nothing was sent or recorded.
func (p *pass) observeOccupant(route *ordjson.Object, items [][2]*ordjson.Object, session, pane string, agent *ordjson.Object) (*occupant, string) {
	deferReason := ""
	occ := observeOccupant(p.s, route, items, agent, func() (*incarnation.Shell, error) {
		if !p.fits(ObserveTimeout) {
			deferReason = "the pass budget ran out before this recipient's pane shell could be observed; nothing was sent or recorded"
			return nil, errNotProbed
		}
		return incarnation.ProbeShell(p.call(session), pane)
	})
	return occ, deferReason
}

func observeOccupant(s *store.Store, route *ordjson.Object, items [][2]*ordjson.Object, agent *ordjson.Object, probe incarnation.ShellProbe) *occupant {
	occ := &occupant{evidence: incarnation.FromInfo(agent)}
	recorded, occupiedAt, ok, err := recordFor(s, route, items)
	if err != nil || !ok {
		return occ
	}
	incarnation.Judge(recorded, occupiedAt, occ.evidence, func() (*incarnation.Shell, error) {
		shell, err := probe()
		if err != errNotProbed {
			occ.probed, occ.shell, occ.shellErr = true, shell, err
		}
		return shell, err
	})
	return occ
}

// checkOccupant runs under the state lock right before the in-flight stamp: the occupant observed for this prompt must
// be the incarnation recorded for the recipient now. A verified worker whose record is out of date (a native session
// reported since launch, a terminal replaced by a handoff or restore) has its registration refreshed from the same
// observation, so its next restore can still be recognized. A refusal sends nothing and records nothing: the returns
// stay pending for the recorded recipient, and an earlier submitted or uncertain delivery keeps its state.
func (p *pass) checkOccupant(route *ordjson.Object, items [][2]*ordjson.Object, occ *occupant) string {
	return checkOccupant(p.s, route, items, occ)
}

func checkOccupant(s *store.Store, route *ordjson.Object, items [][2]*ordjson.Object, occ *occupant) string {
	role := fmt.Sprint(routeValue(route, "role"))
	recorded, occupiedAt, ok, err := recordFor(s, route, items)
	if err != nil {
		return "prompt was not accepted: " + err.Error()
	}
	if !ok {
		return fmt.Sprintf("Recipient pane %v records no %s incarnation to judge its occupant against; nothing was sent. %s", routeValue(route, "pane"), role, incarnation.Recovery(role, incarnation.Unrecorded))
	}
	verdict := incarnation.Judge(recorded, occupiedAt, occ.evidence, occ.probe)
	if !verdict.Verified {
		return fmt.Sprintf("Recipient pane %v is not the recorded %s's occupant (%s: %s); nothing was sent. %s", routeValue(route, "pane"), role, verdict.Outcome, verdict.Reason, incarnation.Recovery(role, verdict.Outcome))
	}
	if role != "coordinator" {
		evidence := occ.evidence
		if occ.probed && occ.shellErr == nil {
			evidence.Shell = occ.shell
		}
		if refreshed, changed := incarnation.Refresh(recorded, evidence, store.Now()); changed {
			if err := s.SetIncarnation(store.EndpointFromContext(route), refreshed); err != nil {
				return "prompt was not accepted: " + err.Error()
			}
		}
	}
	return ""
}

// checkInline judges the calling pane before its own listing is presented and marked submitted. A non-empty
// deferReason means the observation did not fit the pass.
func (p *pass) checkInline(route *ordjson.Object, items [][2]*ordjson.Object) (msg, deferReason string) {
	recorded, occupiedAt, ok, err := recordFor(p.s, route, items)
	if err != nil {
		return "inline listing was not presented: " + err.Error(), ""
	}
	role := fmt.Sprint(routeValue(route, "role"))
	if !ok {
		return fmt.Sprintf("This pane records no %s incarnation, so its returns were not presented here. %s", role, incarnation.Recovery(role, incarnation.Unrecorded)), ""
	}
	if !p.fits(2 * ObserveTimeout) {
		return "", "the pass budget ran out before this pane's occupant could be verified; nothing was presented or recorded"
	}
	session := fmt.Sprint(routeValue(route, "session"))
	verdict := incarnation.Pane(p.call(session), fmt.Sprint(routeValue(route, "pane")), recorded, occupiedAt)
	if !verdict.Verified {
		return fmt.Sprintf("This pane is not the recorded %s's occupant (%s: %s); its returns were not presented or marked submitted. %s", role, verdict.Outcome, verdict.Reason, incarnation.Recovery(role, verdict.Outcome)), ""
	}
	return "", ""
}

// Occupant is one fresh observation of a worker pane taken outside the state lock by a caller other than the delivery
// pass (repair send), to be judged under the lock with Check.
type Occupant struct {
	occ   *occupant
	items [][2]*ordjson.Object
}

// ObserveWorker is ObserveRecipient for task's worker route plus the occupant judgment's inputs: the agent get's
// terminal and native session, and the pane shell when the worker record needs it.
func ObserveWorker(s *store.Store, runtimeRoot string, task, route *ordjson.Object, expectedCwd string) (*Occupant, error) {
	agent, err := observeAgent(s, runtimeRoot, route, expectedCwd)
	if err != nil {
		if u, ok := err.(*unreachableError); ok && u.state == versions.RefreshUnreachable && panes.WorkerIsClosed(task) {
			return nil, &unreachableError{state: "pane-closed", msg: "Recipient pane is closed; resume via execution resume. Delivery is pane-closed, not unreachable."}
		}
		return nil, err
	}
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*ObserveTimeout)
	defer cancel()
	items := [][2]*ordjson.Object{{task, nil}}
	session, pane := fmt.Sprint(routeValue(route, "session")), fmt.Sprint(routeValue(route, "pane"))
	occ := observeOccupant(s, route, items, agent, func() (*incarnation.Shell, error) {
		return incarnation.ProbeShell(incarnation.SessionCall(ctx, herdrPath, session), pane)
	})
	return &Occupant{occ: occ, items: items}, nil
}

// Check judges the observed occupant against route's record as it is now; the caller holds the state lock. A non-empty
// result refuses the side effect and names the recovery.
func (o *Occupant) Check(s *store.Store, route *ordjson.Object) string {
	return checkOccupant(s, route, o.items, o.occ)
}
