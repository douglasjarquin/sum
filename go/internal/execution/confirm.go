package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/maintenance"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/prepare"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

func field(o *ordjson.Object, key string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	return v
}
func decodeNative(value any, target any) error {
	b, e := ordjson.MarshalCompact(value)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, target)
}
func workerEndpoint(task *ordjson.Object) store.Endpoint {
	return store.EndpointFromContext(asObject(field(task, "worker_endpoint")))
}
func sameRoute(a, b *ordjson.Object) bool {
	return store.EndpointFromContext(a).Native().SameAuthority(store.EndpointFromContext(b).Native())
}

func Confirm(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID, attemptID string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	if err = s.CheckMachine(task); err != nil {
		return nil, err
	}
	if err = repair.RefuseDuringCleanup(task, "Execution confirm"); err != nil {
		return nil, err
	}
	worker, err := reservations.Worker(task)
	if err != nil {
		return nil, err
	}
	if field(worker, "id") != attemptID {
		return nil, fmt.Errorf("worker attempt is stale")
	}
	state := asString(field(worker, "state"))
	if state != "uncertain" && state != "starting" && state != "running" {
		return nil, fmt.Errorf("attempt %s is not awaiting a launch receipt", attemptID)
	}
	endpoint := workerEndpoint(task)
	if err = endpoint.Validate(); err != nil {
		return nil, err
	}
	registration, err := s.Registration(endpoint)
	if err != nil {
		return nil, err
	}
	var receipt incarnation.NativeReceipt
	if err = decodeNative(field(registration, "native_receipt"), &receipt); err != nil {
		return nil, fmt.Errorf("no matching worker-init receipt: %w", err)
	}
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID}
	observe, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	terminal, err := client.Get(observe, endpoint.Address)
	if err != nil {
		return nil, err
	}
	verdict := incarnation.JudgeNative(field(registration, "incarnation"), endpoint.Native(), terminal, &receipt)
	if !verdict.Verified {
		return nil, fmt.Errorf("worker receipt/process is not current: %s", verdict.Reason)
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	w, err := reservations.Worker(current)
	if err != nil {
		return nil, err
	}
	if field(w, "id") != attemptID || int64From(w, "generation") != int64From(worker, "generation") || !workerEndpoint(current).Native().SameAuthority(endpoint.Native()) {
		return nil, fmt.Errorf("worker identity changed during confirmation")
	}
	obs := ordjson.NewObject()
	obs.Set("at", store.Now())
	obs.Set("outcome", "worker-init-receipt")
	if _, err = reservations.Transition(current, attemptID, "running", store.Now(), obs, nil); err != nil {
		return nil, err
	}
	current.Set("status", "running")
	current.Set("error", nil)
	body := ordjson.NewObject()
	body.Set("action", "confirm")
	body.Set("attempt", attemptID)
	body.Set("receipt", field(registration, "native_receipt"))
	if _, err = evidence.Append(current, "execution", "coordinator", body, nil, ctx); err != nil {
		return nil, err
	}
	if err = s.SaveTask(current); err != nil {
		return nil, err
	}
	current.Set("confirmed", true)
	return current, nil
}

func Redeliver(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID, attemptID string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	if err = s.CheckMachine(task); err != nil {
		return nil, err
	}
	if err = repair.RefuseDuringCleanup(task, "Execution redeliver"); err != nil {
		return nil, err
	}
	worker, err := reservations.Worker(task)
	if err != nil {
		return nil, err
	}
	if field(worker, "id") != attemptID {
		return nil, fmt.Errorf("worker attempt is stale")
	}
	state := asString(field(worker, "state"))
	if state != "uncertain" && state != "starting" {
		return nil, fmt.Errorf("attempt is not awaiting its initial receipt")
	}
	if err = launch.CheckRecorded(launch.TaskLaunch(task)); err != nil {
		return nil, err
	}
	brief, err := versions.ActiveBrief(s, task)
	if err != nil {
		return nil, err
	}
	endpoint := workerEndpoint(task)
	if err = endpoint.Validate(); err != nil {
		return nil, fmt.Errorf("launch has no returned terminal identity; inspect its saved intent without relaunching: %w", err)
	}
	deliveryCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	release, err := s.RecipientLock(deliveryCtx, [3]string{endpoint.Machine, endpoint.CoreID, endpoint.Address})
	if err != nil {
		return nil, err
	}
	defer release()
	recipient, err := returns.ObserveNative(deliveryCtx, endpoint, asString(field(task, "worktree")))
	if err != nil {
		return nil, err
	}
	registration, err := s.Registration(endpoint)
	if err != nil {
		return nil, err
	}
	if field(registration, "native_receipt") != nil {
		return nil, fmt.Errorf("worker already recorded an init receipt; use execution confirm")
	}
	var recorded incarnation.NativeEvidence
	if err = decodeNative(field(registration, "incarnation"), &recorded); err != nil {
		return nil, err
	}
	if !recorded.Endpoint.SameAuthority(endpoint.Native()) || recipient.Terminal.PID == nil || *recipient.Terminal.PID != recorded.PID || !reflect.DeepEqual(recipient.Terminal.Foreground, recorded.Foreground) {
		return nil, fmt.Errorf("recorded launch process changed; redelivery refused")
	}
	if err = recipient.Qualify(registration); err != nil {
		return nil, err
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	current, err := s.ReadTask(taskID)
	if err != nil {
		unlock()
		return nil, err
	}
	if err = maintenance.Refuse(current, "Execution redeliver"); err != nil {
		unlock()
		return nil, err
	}
	w, err := reservations.Worker(current)
	if err != nil {
		unlock()
		return nil, err
	}
	if field(w, "id") != attemptID || int64From(w, "generation") != int64From(worker, "generation") || !workerEndpoint(current).Native().SameAuthority(endpoint.Native()) {
		unlock()
		return nil, fmt.Errorf("worker identity changed during redelivery")
	}
	native := asObject(field(w, "native_launch"))
	if native == nil {
		unlock()
		return nil, fmt.Errorf("no saved native launch intent")
	}
	first := asObject(field(native, "first_message"))
	sent, explicit := field(first, "sent").(bool)
	if field(native, "effect") != "created" || !explicit || sent {
		unlock()
		return nil, fmt.Errorf("the opening message is not proven unsent for this created attempt; preserve uncertainty and use execution confirm after a matching worker-init receipt")
	}
	currentRegistration, err := s.Registration(endpoint)
	if err != nil {
		unlock()
		return nil, err
	}
	if field(currentRegistration, "native_receipt") != nil {
		unlock()
		return nil, fmt.Errorf("worker already recorded an init receipt; use execution confirm")
	}
	for _, key := range []string{"incarnation", "native_template"} {
		observed, _ := ordjson.MarshalCompact(field(registration, key))
		current, _ := ordjson.MarshalCompact(field(currentRegistration, key))
		if !bytes.Equal(observed, current) {
			unlock()
			return nil, fmt.Errorf("worker registration changed during redelivery observation; inspect the current attempt")
		}
	}
	if field(native, "redelivery") != nil {
		unlock()
		return nil, fmt.Errorf("redelivery already claimed; inspect its outcome and record a receipt before further recovery")
	}
	claim := ordjson.NewObject()
	claim.Set("effect", "claimed")
	claim.Set("at", store.Now())
	native.Set("redelivery", claim)
	if err = s.SaveTask(current); err != nil {
		unlock()
		return nil, err
	}
	unlock()
	result, sendErr := recipient.Send(deliveryCtx, prepare.WorkerPrompt(s, runtimeRoot, taskID, brief, current))
	effect, detail := returns.DeliveryFailure(sendErr)
	unlock, err = s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err = s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	w, err = reservations.Worker(current)
	if err != nil {
		return nil, err
	}
	if field(w, "id") != attemptID || !workerEndpoint(current).Native().SameAuthority(endpoint.Native()) {
		return nil, fmt.Errorf("worker changed after delivery; inspect original endpoint")
	}
	native = asObject(field(w, "native_launch"))
	claim = asObject(field(native, "redelivery"))
	claim.Set("effect", effect)
	claim.Set("detail", detail)
	resultObject, _ := ordjson.FromValue(result)
	claim.Set("result", resultObject)
	if err = s.SaveTask(current); err != nil {
		return nil, err
	}
	if sendErr != nil {
		return nil, sendErr
	}
	return current, nil
}
