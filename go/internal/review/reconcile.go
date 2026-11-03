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
	if err := reconcileCreation(s, coordinator, taskID, attempt); err != nil {
		return nil, err
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	ep := store.EndpointFromContext(objectField(task, "reviewer"))
	if ep.Validate() == nil && ep.Attempt == attempt {
		observe, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err = launch.ReconcileVisibility(observe, s, ep, locateReviewer(attempt)); err != nil {
			return nil, err
		}
		if task, err = s.ReadTask(taskID); err != nil {
			return nil, err
		}
	}
	out := ordjson.NewObject()
	out.Set("reviewer", objectField(task, "reviewer"))
	return out, nil
}

func reconcileCreation(s *store.Store, coordinator *ordjson.Object, taskID, attempt string) error {
	if err := app.RequireCoordinator(s, coordinator); err != nil {
		return err
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	if err = s.CheckMachine(task); err != nil {
		return err
	}
	if err = maintenance.Refuse(task, "Reviewer recovery"); err != nil {
		return err
	}
	actor, err := app.ObserveTaskActor(s, coordinator)
	if err != nil {
		return err
	}
	if err = app.CheckTaskRole(s, task, actor, "coordinator"); err != nil {
		return err
	}
	reviewer := objectField(task, "reviewer")
	ep := store.EndpointFromContext(reviewer)
	if attempt == "" || ep.Attempt != attempt {
		return fmt.Errorf("exact reviewer attempt required")
	}
	if ep.Validate() == nil {
		return nil
	}
	before, _ := ordjson.MarshalCompact(reviewer)
	intent := objectField(reviewer, "native_launch")
	var params hcclient.RunParams
	var template returns.NativeTemplate
	if err = decodeObject(objectField(intent, "params"), &params); err != nil {
		return err
	}
	if err = decodeObject(objectField(reviewer, "native_template"), &template); err != nil {
		return err
	}
	instance, err := s.Instance()
	if err != nil {
		return err
	}
	if params.Labels["sum.installation"] != instance || params.Labels["sum.task"] != taskID || params.Labels["sum.role"] != "reviewer" || params.Labels["sum.attempt"] != attempt || params.Cwd != asString(task, "worktree") || ep.Task != taskID || ep.Role != "reviewer" {
		return fmt.Errorf("creation intent does not belong to this reviewer attempt and checkout")
	}
	if params.Env["HC_SOCKET"] == "" || params.Env["SUM_CORE_ID"] == "" || params.Env["SUM_SESSION"] == "" || params.Env["SUM_TASK"] != taskID || params.Env["SUM_ATTEMPT"] != attempt || params.Env["SUM_ROLE"] != "reviewer" {
		return fmt.Errorf("saved reviewer creation route is incomplete or mismatched")
	}
	if ep.Socket != params.Env["HC_SOCKET"] || ep.CoreID != params.Env["SUM_CORE_ID"] || ep.Session != params.Env["SUM_SESSION"] {
		return fmt.Errorf("reviewer endpoint differs from saved creation route")
	}
	client := hcclient.Client{Socket: params.Env["HC_SOCKET"], CoreID: params.Env["SUM_CORE_ID"]}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	terminal, err := launch.DiscoverOriginal(ctx, &client, params, template.Spec, asString(intent, "at"))
	if err != nil {
		return err
	}
	ep.Address, ep.CreatedMS = terminal.Address, terminal.CreatedMS
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	current, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	saved := objectField(current, "reviewer")
	if asString(saved, "attempt") != attempt || asString(saved, "address") != "" || reviewerCandidateState(task) != reviewerCandidateState(current) {
		return fmt.Errorf("reviewer assignment changed during discovery")
	}
	after, _ := ordjson.MarshalCompact(saved)
	if !bytes.Equal(before, after) {
		return fmt.Errorf("reviewer intent changed during discovery")
	}
	if err = maintenance.Refuse(current, "Reviewer reconcile"); err != nil {
		return err
	}
	if err = app.CheckTaskRole(s, current, actor, "coordinator"); err != nil {
		return err
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
	return s.SaveTask(current)
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
	actor, err := app.ObserveTaskActor(s, coordinator)
	if err != nil {
		return nil, err
	}
	if err = app.CheckTaskRole(s, task, actor, "coordinator"); err != nil {
		return nil, err
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
	if err = app.CheckTaskRole(s, current, actor, "coordinator"); err != nil {
		return nil, err
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
