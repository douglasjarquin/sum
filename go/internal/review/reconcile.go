package review

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/bindcmd"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/maintenance"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func Reconcile(s *store.Store, coordinator *ordjson.Object, taskID, attempt string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, coordinator); err != nil {
		return nil, err
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	if err = s.CheckMachine(task); err != nil {
		return nil, err
	}
	if err = maintenance.Refuse(task, "Reviewer recovery"); err != nil {
		return nil, err
	}
	parent := store.EndpointFromContext(objectField(task, "parent"))
	if !parent.Native().SameAuthority(store.EndpointFromContext(coordinator).Native()) {
		return nil, fmt.Errorf("task belongs to another coordinator")
	}
	reviewer := objectField(task, "reviewer")
	ep := store.EndpointFromContext(reviewer)
	if attempt == "" || ep.Attempt != attempt {
		return nil, fmt.Errorf("exact reviewer attempt required")
	}
	if ep.Validate() == nil {
		out := ordjson.NewObject()
		out.Set("reviewer", reviewer)
		return out, nil
	}
	before, _ := ordjson.MarshalCompact(reviewer)
	intent := objectField(reviewer, "native_launch")
	var params hcclient.RunParams
	var template returns.NativeTemplate
	if err = decodeObject(objectField(intent, "params"), &params); err != nil {
		return nil, err
	}
	if err = decodeObject(objectField(reviewer, "native_template"), &template); err != nil {
		return nil, err
	}
	client := hcclient.Client{Socket: ep.Socket, CoreID: ep.CoreID}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	terminal, err := launch.DiscoverOriginal(ctx, &client, params, template.Spec, asString(intent, "at"))
	if err != nil {
		return nil, err
	}
	ep.Address, ep.CreatedMS = terminal.Address, terminal.CreatedMS
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	saved := objectField(current, "reviewer")
	if asString(saved, "attempt") != attempt || asString(saved, "address") != "" || reviewerCandidateState(task) != reviewerCandidateState(current) {
		return nil, fmt.Errorf("reviewer assignment changed during discovery")
	}
	after, _ := ordjson.MarshalCompact(saved)
	if !bytes.Equal(before, after) {
		return nil, fmt.Errorf("reviewer intent changed during discovery")
	}
	if err = maintenance.Refuse(current, "Reviewer reconcile"); err != nil {
		return nil, err
	}
	if !parent.Native().SameAuthority(store.EndpointFromContext(objectField(current, "parent")).Native()) {
		return nil, fmt.Errorf("task parent changed during discovery")
	}
	for _, key := range ep.Object().Keys() {
		value, _ := ep.Object().Get(key)
		saved.Set(key, value)
	}
	native := objectField(saved, "native_launch")
	first := &hcclient.FirstMessage{Sent: false}
	native.Set("state", "created")
	result, _ := ordjson.FromValue(hcclient.RunResult{Terminal: terminal, FirstMessage: first})
	native.Set("result", result)
	firstValue, _ := ordjson.FromValue(first)
	native.Set("first_message", firstValue)
	prompt := ordjson.NewObject()
	prompt.Set("state", "not-delivered")
	prompt.Set("candidate", asString(saved, "candidate"))
	saved.Set("prompt", prompt)
	saved.Set("incarnation", (incarnation.NativeEvidence{Endpoint: ep.Native(), PID: *terminal.PID, Foreground: terminal.Foreground}).Record(store.Now()))
	if err = s.SaveTask(current); err != nil {
		return nil, err
	}
	out := ordjson.NewObject()
	out.Set("reviewer", saved)
	return out, nil
}

func Recover(s *store.Store, coordinator *ordjson.Object, taskID, attempt string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, coordinator); err != nil {
		return nil, err
	}

	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	if err = s.CheckMachine(task); err != nil {
		return nil, err
	}
	if err = maintenance.Refuse(task, "Reviewer recover"); err != nil {
		return nil, err
	}
	parent := store.EndpointFromContext(objectField(task, "parent"))
	if !parent.Native().SameAuthority(store.EndpointFromContext(coordinator).Native()) {
		return nil, fmt.Errorf("task belongs to another coordinator")
	}
	reviewer := objectField(task, "reviewer")
	ep := store.EndpointFromContext(reviewer)
	if attempt == "" || ep.Attempt != attempt || objectField(reviewer, "native_receipt") != nil {
		return nil, fmt.Errorf("exact uninitialized reviewer attempt required")
	}
	snapshot, _ := ordjson.MarshalCompact(reviewer)
	if ep.Address != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		proof, proofErr := bindcmd.ReviewerNativeStopped(ctx, s, task, reviewer)
		if proofErr != nil {
			return nil, proofErr
		}
		reviewer.Set("stop_proof", proof)
	} else {
		state := asString(objectField(reviewer, "native_launch"), "state")
		if state != "not-submitted" && state != "refused" {
			return nil, fmt.Errorf("creation remains ambiguous; inspect or reconcile without relaunching")
		}
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
	saved, _ := ordjson.MarshalCompact(objectField(current, "reviewer"))
	if string(saved) != string(snapshot) || reviewerCandidateState(current) != reviewerCandidateState(task) {
		return nil, fmt.Errorf("reviewer changed during recovery")
	}
	if err = maintenance.Refuse(current, "Reviewer recover"); err != nil {
		return nil, err
	}
	if !parent.Native().SameAuthority(store.EndpointFromContext(objectField(current, "parent")).Native()) {
		return nil, fmt.Errorf("task parent changed during recovery")
	}
	owner, err := s.Owner()
	if err != nil {
		return nil, err
	}
	if !parent.Native().SameAuthority(store.EndpointFromContext(owner).Native()) {
		return nil, fmt.Errorf("coordinator authority changed during recovery")
	}
	task = current
	prior, _ := task.Get("reviewer_launches")
	history, _ := prior.([]any)
	task.Set("reviewer_launches", append(history, reviewer))
	task.Delete("reviewer")
	if err = s.SaveTask(task); err != nil {
		return nil, err
	}
	out := ordjson.NewObject()
	out.Set("recovered_attempt", attempt)
	out.Set("note", "Proven stopped or no-effect attempt retained in reviewer_launches; explicitly launch after correcting its prerequisite.")
	return out, nil
}
