package review

import (
	"context"
	"fmt"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
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
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	reviewer := objectField(task, "reviewer")
	ep := store.EndpointFromContext(reviewer)
	if attempt == "" || ep.Attempt != attempt || ep.Address != "" || objectField(reviewer, "native_receipt") != nil {
		return nil, fmt.Errorf("exact uncreated reviewer attempt required")
	}
	state := asString(objectField(reviewer, "native_launch"), "state")
	if state != "not-submitted" && state != "refused" {
		return nil, fmt.Errorf("creation remains ambiguous; inspect or reconcile without relaunching")
	}
	prior, _ := task.Get("reviewer_launches")
	history, _ := prior.([]any)
	task.Set("reviewer_launches", append(history, reviewer))
	task.Delete("reviewer")
	if err = s.SaveTask(task); err != nil {
		return nil, err
	}
	out := ordjson.NewObject()
	out.Set("recovered_attempt", attempt)
	out.Set("note", "Proven no-effect attempt retained in reviewer_launches; explicitly launch after correcting its prerequisite.")
	return out, nil
}
