package returns

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// Recipient-scoped delivery: the lock a pass holds for one recipient, the claim that records an attempt in-flight
// right before its prompt, and the recording of its outcome. The lock order is the one store documents:
// compatibility lock (shared), then one recipient lock, then the state lock, which is never held across Herdr I/O.

// LockRecipient takes the delivery compatibility lock shared and then route's recipient lock, keyed by the canonical
// endpoint so every machine spelling of one pane serializes together. Both waits end with ctx.
func LockRecipient(s *store.Store, ctx context.Context, route *ordjson.Object) (func(), error) {
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	return lockRecipient(s, ctx, ctx, identity(host, route))
}

func lockRecipient(s *store.Store, sharedCtx, recipientCtx context.Context, endpoint [3]string) (func(), error) {
	unlockShared, err := s.DeliveryShared(sharedCtx)
	if err != nil {
		return nil, err
	}
	unlockRecipient, err := s.RecipientLock(recipientCtx, endpoint)
	if err != nil {
		unlockShared()
		return nil, err
	}
	return func() {
		unlockRecipient()
		unlockShared()
	}, nil
}

// lock takes b's recipient lock for this pass. A first visit tries the recipient lock once and reports busy; a revisit
// waits only as long as an observation and a prompt could still follow within the pass budget.
func (p *pass) lock(b *bucket, wait bool) (unlock func(), busy bool, deferReason string, err error) {
	started := time.Now()
	defer func() { p.lockWait += time.Since(started) }()
	recipientCtx, cancel := context.WithCancel(p.ctx)
	defer cancel()
	if !wait {
		cancel()
	} else if deadline, ok := p.ctx.Deadline(); ok {
		bound := time.Until(deadline) - (ObserveTimeout + PromptTimeout + 2*proc.PipeGrace)
		if bound <= 0 {
			return nil, false, "another operation was delivering to this recipient and too little of the pass budget remained to wait for it; nothing was sent or recorded", nil
		}
		recipientCtx, cancel = context.WithTimeout(p.ctx, bound)
		defer cancel()
	}
	unlock, err = lockRecipient(p.s, p.ctx, recipientCtx, identity(p.host, b.route))
	switch {
	case errors.Is(err, store.ErrDeliveryLockBusy):
		return nil, false, "an older release's delivery pass held the delivery lock until this pass's budget ran out; nothing was sent or recorded", nil
	case errors.Is(err, store.ErrRecipientBusy) && !wait:
		return nil, true, "", nil
	case errors.Is(err, store.ErrRecipientBusy):
		return nil, false, "another operation was delivering to this recipient for the rest of this pass's budget; nothing was sent or recorded", nil
	}
	return unlock, false, "", err
}

// claimResult is the outcome of the pre-prompt claim: the notice to send, or why nothing is sent.
type claimResult struct {
	message     string
	quiet       string // nothing is still owed here; nothing was recorded
	refused     string // the recipient is no longer registered for these returns
	stale       string // the recipient pane's occupant is not the recorded one; nothing was sent or recorded
	deferReason string // the prompt no longer fits the pass; nothing was recorded
	// beforePrompt persists the wake episode's submission intent immediately before the prompt call; an error sends
	// nothing.
	beforePrompt func() error
}

// record writes an attempt's outcome under one state-lock hold. A claimed attempt already has its in-flight entry and
// is always finalized; an outcome with no possible effect is recorded only for returns still open and still routed to
// route. The returns sidecar is the only record written; no task record is rewritten for an attempt.
func (p *pass) record(route *ordjson.Object, items [][2]*ordjson.Object, claimed bool, delivery *ordjson.Object, changes map[string]any) error {
	unlock, err := p.s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	// A claimed attempt is finalized before anything is re-read, so a read error cannot leave a known outcome
	// in-flight.
	if claimed {
		return stampDeliveryLocked(p.s, items, delivery, changes)
	}
	current, _, err := p.stillRouted(route, items)
	if err != nil {
		return err
	}
	return stampDeliveryLocked(p.s, current, delivery, changes)
}

func pairKey(pair [2]*ordjson.Object) [2]string {
	taskID, _ := pair[0].Get("id")
	oid, _ := pair[1].Get("id")
	return [2]string{fmt.Sprint(taskID), fmt.Sprint(oid)}
}

// stampDeliveryLocked adds or updates delivery in each item's returns sidecar; the caller holds the state lock.
func stampDeliveryLocked(s *store.Store, items [][2]*ordjson.Object, delivery *ordjson.Object, changes map[string]any) error {
	seen := map[string]bool{}
	deliveryID, _ := delivery.Get("id")
	for _, pair := range items {
		taskID, _ := pair[0].Get("id")
		idStr, _ := taskID.(string)
		if seen[idStr] {
			continue
		}
		seen[idStr] = true
		returnsObj, err := ReadReturns(s, idStr)
		if err != nil {
			return err
		}
		ids := []any{}
		idSet := map[string]bool{}
		for _, p := range items {
			tid, _ := p[0].Get("id")
			if fmt.Sprint(tid) != idStr {
				continue
			}
			oid, _ := p[1].Get("id")
			key := fmt.Sprint(oid)
			if !idSet[key] {
				idSet[key] = true
				ids = append(ids, oid)
			}
		}
		sortIDs(ids)
		deliveriesValue, _ := returnsObj.Get("deliveries")
		list, _ := deliveriesValue.([]any)
		var existing *ordjson.Object
		for _, d := range list {
			obj, _ := d.(*ordjson.Object)
			id, _ := obj.Get("id")
			if id == deliveryID {
				existing = obj
				break
			}
		}
		if existing != nil {
			for k, v := range changes {
				existing.Set(k, v)
			}
		} else {
			entry := cloneObject(delivery)
			entry.Set("obligations", ids)
			for k, v := range changes {
				entry.Set(k, v)
			}
			returnsObj.Set("deliveries", append(list, entry))
		}
		if err := Write(s, returnsObj); err != nil {
			return err
		}
	}
	return nil
}
