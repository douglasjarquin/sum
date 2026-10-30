package updatecmd

import (
	"encoding/json"
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// unresolvedEffects never rewrites or retries effects when selecting code. A
// receipt or explicit reconciliation must resolve them from the serving runtime.
func unresolvedEffects(s *store.Store, tasks []*ordjson.Object) []any {
	var blocked []any
	for _, task := range tasks {
		id := effectField(task, "id")
		execution, err := reservations.GetExecution(task)
		if err != nil {
			blocked = append(blocked, fmt.Sprintf("task %s execution cannot be validated: %v", id, err))
			continue
		}
		if execution != nil {
			attempts := append([]*ordjson.Object{execution.Worker}, execution.Verifiers...)
			for _, attempt := range attempts {
				state := effectField(attempt, "state")
				launch := asObject(func() any { v, _ := attempt.Get("native_launch"); return v }())
				if state == "starting" || state == "uncertain" || (unresolvedLaunch(launch) && !nativeReceiptResolvesLaunch(s, task, attempt)) {
					blocked = append(blocked, fmt.Sprintf("task %s attempt %s has an unresolved launch or send; reconcile it from the serving runtime before activation", id, effectField(attempt, "id")))
				}
			}
		}
		reviewer := asObject(func() any { v, _ := task.Get("reviewer"); return v }())
		if reviewer != nil {
			for _, field := range []string{"native_launch", "prompt"} {
				record := asObject(func() any { v, _ := reviewer.Get(field); return v }())
				state := effectField(record, "state")
				if state == "in-flight" || state == "uncertain" {
					blocked = append(blocked, fmt.Sprintf("task %s reviewer %s is unresolved; reconcile it from the serving runtime before activation", id, field))
				}
			}
		}
		sidecar, err := returns.ReadReturns(s, id)
		if err != nil {
			blocked = append(blocked, fmt.Sprintf("task %s notification attempts cannot be validated: %v", id, err))
			continue
		}
		raw, _ := sidecar.Get("deliveries")
		rows, _ := raw.([]any)
		for _, raw := range rows {
			row := asObject(raw)
			if row == nil {
				blocked = append(blocked, fmt.Sprintf("task %s has an unreadable notification attempt", id))
				continue
			}
			state := effectField(row, "state")
			if state == "in-flight" || state == "uncertain" {
				blocked = append(blocked, fmt.Sprintf("task %s has an unresolved %s notification attempt; reconcile it from the serving runtime before activation", id, state))
			}
		}
	}
	return blocked
}

func unresolvedLaunch(launch *ordjson.Object) bool {
	if launch == nil {
		return false
	}
	effect := effectField(launch, "effect")
	if effect == "claimed" || effect == "uncertain" {
		return true
	}
	redelivery := asObject(func() any { v, _ := launch.Get("redelivery"); return v }())
	if redelivery == nil {
		return false
	}
	effect = effectField(redelivery, "effect")
	return effect == "claimed" || effect == "uncertain"
}

func effectField(o *ordjson.Object, key string) string {
	if o == nil {
		return ""
	}
	v, _ := o.Get(key)
	return asString(v)
}

// A saved matching application init can resolve an uncertain transport effect.
// Keep the original launch/send diagnosis intact; status and native Started
// fields cannot stand in for this exact attempt and binding receipt.
func nativeReceiptResolvesLaunch(s *store.Store, task, attempt *ordjson.Object) bool {
	if effectField(attempt, "state") != "running" {
		return false
	}
	endpoint := store.EndpointFromContext(asObject(func() any { v, _ := task.Get("worker_endpoint"); return v }()))
	if endpoint.Validate() != nil || endpoint.Role != "worker" || endpoint.Task != effectField(task, "id") || endpoint.Attempt != effectField(attempt, "id") {
		return false
	}
	registration, err := s.Registration(endpoint)
	if err != nil || registration == nil {
		return false
	}
	receipt, err := app.NativeReceipt(registration)
	if err != nil || receipt.Kind != "worker-init" || receipt.Attempt != endpoint.Attempt || !receipt.Endpoint.SameAuthority(endpoint.Native()) {
		return false
	}
	raw, _ := registration.Get("incarnation")
	encoded, err := ordjson.MarshalCompact(raw)
	if err != nil {
		return false
	}
	var evidence incarnation.NativeEvidence
	if json.Unmarshal(encoded, &evidence) != nil {
		return false
	}
	return evidence.Endpoint.SameAuthority(endpoint.Native())
}
