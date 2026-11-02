package review

import (
	"context"
	"fmt"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func deliverReviewerOpening(s *store.Store, taskID, attempt string, initial hcclient.Terminal) (*ordjson.Object, error) {
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	reviewer := objectField(task, "reviewer")
	ep := store.EndpointFromContext(reviewer)
	if ep.Attempt != attempt || ep.Validate() != nil {
		return nil, fmt.Errorf("reviewer attempt changed")
	}
	var template returns.NativeTemplate
	if err = decodeObject(objectField(reviewer, "native_template"), &template); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	release, err := s.RecipientLock(ctx, [3]string{ep.Machine, ep.CoreID, ep.Address})
	if err != nil {
		return nil, err
	}
	defer release()
	task, err = s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	reviewer = objectField(task, "reviewer")
	intent := objectField(reviewer, "native_launch")
	if objectField(reviewer, "native_receipt") != nil || objectField(intent, "opening_delivery") != nil || asString(objectField(reviewer, "prompt"), "redeliver_attempt") != "" {
		out := ordjson.NewObject()
		out.Set("task", taskID)
		out.Set("reviewer", reviewer)
		return out, nil
	}
	client := hcclient.Client{Socket: ep.Socket, CoreID: ep.CoreID}
	launch.DeliverOpening(ctx, &client, initial, template.Spec, asString(reviewer, "opening"), func(progress launch.OpeningProgress) error {
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
		if !ep.Native().SameAuthority(store.EndpointFromContext(saved).Native()) || reviewerCandidateState(current) != reviewerCandidateState(task) {
			return fmt.Errorf("reviewer assignment changed before opening")
		}
		if objectField(saved, "native_receipt") != nil {
			return fmt.Errorf("reviewer already initialized; no opening sent")
		}
		intent := objectField(saved, "native_launch")
		v, _ := ordjson.FromValue(progress)
		intent.Set("opening_delivery", v)
		v, _ = ordjson.FromValue(progress.First)
		intent.Set("first_message", v)
		result := objectField(intent, "result")
		if result != nil {
			result.Set("first_message", v)
		}
		prompt := objectField(saved, "prompt")
		prompt.Set("state", progress.State)
		prompt.Set("detail", progress.Detail)
		if progress.Terminal.PID != nil && launch.OriginalProgram(initial, progress.Terminal, template.Spec) == nil {
			saved.Set("incarnation", (incarnation.NativeEvidence{Endpoint: ep.Native(), PID: *progress.Terminal.PID, Foreground: progress.Terminal.Foreground}).Record(store.Now()))
		}
		return s.SaveTask(current)
	})
	task, err = s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	out := ordjson.NewObject()
	out.Set("task", taskID)
	out.Set("reviewer", objectField(task, "reviewer"))
	return out, nil
}
