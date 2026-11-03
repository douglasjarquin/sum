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

func unresolvedEffects(s *store.Store, tasks []*ordjson.Object) []any {
	var blocked []any
	for _, task := range tasks {
		id := effectField(task, "id")
		maintenance := asObject(func() any { v, _ := task.Get("terminal_maintenance"); return v }())
		cleanup := asObject(func() any { v, _ := task.Get("cleanup"); return v }())
		if unresolvedLaunch(maintenance) || effectField(cleanup, "state") == "removing" || (effectField(task, "status") == "preparing" && (effectField(task, "prepare_operation_id") != "" || func() bool { v, _ := task.Get("prepare_operation_pid"); return v != nil && fmt.Sprint(v) != "0" }())) {
			blocked = append(blocked, fmt.Sprintf("task %s has unresolved preparation or terminal maintenance", id))
		}
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
				redelivery := asObject(func() any {
					if launch == nil {
						return nil
					}
					v, _ := launch.Get("redelivery")
					return v
				}())
				if state == "running" || state == "starting" || state == "uncertain" || unresolvedLaunch(redelivery) || (unresolvedLaunch(launch) && !nativeReceiptResolvesLaunch(s, task, attempt)) {
					blocked = append(blocked, fmt.Sprintf("task %s attempt %s is active or has an unresolved launch or send; settle or reconcile it before activation", id, effectField(attempt, "id")))
				}
			}
		}
		reviewer := asObject(func() any { v, _ := task.Get("reviewer"); return v }())
		if reviewer != nil {
			for _, field := range []string{"native_launch", "prompt", "kickoff"} {
				record := asObject(func() any { v, _ := reviewer.Get(field); return v }())
				state := effectField(record, "state")
				if state == "in-flight" || state == "uncertain" || state == "claimed" || state == "submitted" || unresolvedLaunch(record) {
					blocked = append(blocked, fmt.Sprintf("task %s reviewer %s is unresolved; reconcile it from the serving runtime before activation", id, field))
				}
			}
		}
		if err := returns.PendingDeliveryEffects(s, task); err != nil {
			blocked = append(blocked, fmt.Sprintf("task %s notification effects: %v", id, err))
		}
	}
	return blocked
}

func unresolvedLaunch(launch *ordjson.Object) bool {
	if launch == nil {
		return false
	}
	effect := effectField(launch, "effect")
	if effect == "" {
		effect = effectField(launch, "state")
	}
	if effect == "claimed" || effect == "uncertain" || effect == "in-flight" || effect == "unknown" {
		return true
	}
	redelivery := asObject(func() any { v, _ := launch.Get("redelivery"); return v }())
	if redelivery == nil {
		return false
	}
	effect = effectField(redelivery, "effect")
	return effect == "claimed" || effect == "uncertain" || effect == "in-flight" || effect == "unknown"
}

func effectField(o *ordjson.Object, key string) string {
	if o == nil {
		return ""
	}
	v, _ := o.Get(key)
	return asString(v)
}

func nativeReceiptResolvesLaunch(s *store.Store, task, attempt *ordjson.Object) bool {
	if state := effectField(attempt, "state"); state != "running" && state != "released" {
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
