package app

import (
	"bytes"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"slices"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type TaskActor struct {
	endpoint     store.Endpoint
	terminal     hcclient.Terminal
	registration []byte
	owner        []byte
	candidate    string
	checkout     string
}

func ObserveTaskActor(s *store.Store, ctx *ordjson.Object) (*TaskActor, error) {
	endpoint := store.EndpointFromContext(ctx)
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	if !host.Is(endpoint.Machine) {
		return nil, fmt.Errorf("native Sum execution on another machine is unsupported")
	}
	registration, err := s.Registration(endpoint)
	if err != nil {
		return nil, err
	}
	if registration == nil {
		return nil, fmt.Errorf("current native role registration required")
	}
	_, terminal, err := ObserveNative(endpoint)
	if err != nil {
		return nil, err
	}
	receipt, err := NativeReceipt(registration)
	if err != nil {
		return nil, err
	}
	recorded, _ := registration.Get("incarnation")
	verdict := incarnation.JudgeNative(recorded, endpoint.Native(), terminal, receipt)
	if !verdict.Verified {
		return nil, fmt.Errorf("native actor authority refused: %s", verdict.Reason)
	}
	owner, err := s.Owner()
	if err != nil {
		return nil, err
	}
	registrationBytes, err := ordjson.MarshalSortedCompact(registration)
	if err != nil {
		return nil, err
	}
	ownerBytes, err := ordjson.MarshalSortedCompact(owner)
	if err != nil {
		return nil, err
	}
	actor := &TaskActor{endpoint: endpoint, terminal: terminal, registration: registrationBytes, owner: ownerBytes}
	if endpoint.Role == "reviewer" {
		task, err := s.ReadTask(endpoint.Task)
		if err != nil {
			return nil, err
		}
		checkout, _ := task.Get("worktree")
		actor.checkout, _ = checkout.(string)
		actor.candidate, err = nativeCandidate(actor.checkout)
		if err != nil {
			return nil, err
		}
		reviewerValue, _ := task.Get("reviewer")
		reviewer, _ := reviewerValue.(*ordjson.Object)
		if reviewer == nil {
			return nil, fmt.Errorf("current reviewer binding required")
		}
		promptValue, _ := reviewer.Get("prompt")
		prompt, _ := promptValue.(*ordjson.Object)
		if prompt == nil {
			return nil, fmt.Errorf("current reviewer candidate assignment required")
		}
		assigned, _ := prompt.Get("candidate")
		if assigned != actor.candidate {
			return nil, fmt.Errorf("reviewer candidate assignment is stale")
		}
	}
	return actor, nil
}

func CheckTaskRole(s *store.Store, task *ordjson.Object, actor *TaskActor, roles ...string) error {
	if actor == nil || task == nil {
		return fmt.Errorf("current native task actor required")
	}
	endpoint := actor.endpoint
	if !slices.Contains(roles, endpoint.Role) {
		return fmt.Errorf("native actor does not have the required task role")
	}
	registration, err := s.Registration(endpoint)
	if err != nil {
		return err
	}
	current, err := ordjson.MarshalSortedCompact(registration)
	if err != nil || !bytes.Equal(current, actor.registration) {
		return fmt.Errorf("native role registration changed after observation")
	}
	var record *ordjson.Object
	switch endpoint.Role {
	case "coordinator":
		parent, _ := task.Get("parent")
		record, _ = parent.(*ordjson.Object)
		owner, err := s.Owner()
		if err != nil {
			return err
		}
		current, err := ordjson.MarshalSortedCompact(owner)
		if err != nil || !bytes.Equal(current, actor.owner) {
			return fmt.Errorf("coordinator owner changed after observation")
		}
		if matches, err := s.Matches(owner, endpoint); err != nil || !matches {
			return fmt.Errorf("current coordinator owner required")
		}
		if err := checkTaskRecord(owner, actor); err != nil {
			return err
		}
	case "worker":
		id, _ := task.Get("id")
		if endpoint.Task != id {
			return fmt.Errorf("native worker belongs to another task")
		}
		binding, _ := task.Get("worker_endpoint")
		bound, _ := binding.(*ordjson.Object)
		if matches, err := s.Matches(bound, endpoint); err != nil || !matches {
			return fmt.Errorf("current native worker endpoint required")
		}
		record, err = reservations.Worker(task)
		if err != nil {
			return err
		}
		attempt, _ := record.Get("id")
		state, _ := record.Get("state")
		if attempt != endpoint.Attempt || (state != "running" && state != "starting" && state != "uncertain") {
			return fmt.Errorf("current unreleased worker attempt required")
		}
	case "reviewer":
		id, _ := task.Get("id")
		if endpoint.Task != id {
			return fmt.Errorf("native reviewer belongs to another task")
		}
		value, _ := task.Get("reviewer")
		record, _ = value.(*ordjson.Object)
		checkout, _ := task.Get("worktree")
		if checkout != actor.checkout {
			return fmt.Errorf("reviewer checkout changed after observation")
		}
		current, err := nativeCandidate(actor.checkout)
		if err != nil {
			return err
		}
		if current != actor.candidate {
			return fmt.Errorf("reviewer candidate changed after observation")
		}
		if record == nil {
			return fmt.Errorf("current reviewer binding required")
		}
		promptValue, _ := record.Get("prompt")
		prompt, _ := promptValue.(*ordjson.Object)
		if prompt == nil {
			return fmt.Errorf("current reviewer candidate assignment required")
		}
		if candidate, _ := prompt.Get("candidate"); candidate != actor.candidate {
			return fmt.Errorf("reviewer candidate assignment changed after observation")
		}
	default:
		return fmt.Errorf("unsupported native task role")
	}
	binding := record
	if endpoint.Role == "worker" {
		value, _ := task.Get("worker_endpoint")
		binding, _ = value.(*ordjson.Object)
	}
	if matches, err := s.Matches(binding, endpoint); err != nil || !matches {
		return fmt.Errorf("task binding changed after observation")
	}
	if endpoint.Role != "coordinator" {
		return checkTaskRecord(record, actor)
	}
	return nil
}

func checkTaskRecord(record *ordjson.Object, actor *TaskActor) error {
	receipt, err := NativeReceipt(record)
	if err != nil {
		return err
	}
	value, _ := record.Get("incarnation")
	verdict := incarnation.JudgeNative(value, actor.endpoint.Native(), actor.terminal, receipt)
	if !verdict.Verified {
		return fmt.Errorf("task native authority refused: %s", verdict.Reason)
	}
	return nil
}

func nativeCandidate(checkout string) (string, error) {
	if checkout == "" {
		return "", fmt.Errorf("reviewer requires an existing Git checkout")
	}
	result, err := proc.Run([]string{"git", "-C", checkout, "rev-parse", "HEAD"}, "", 0, true, proc.ScrubbedEnv())
	if err != nil {
		return "", err
	}
	candidate := strings.TrimSpace(result.Stdout)
	if candidate == "" {
		return "", fmt.Errorf("reviewer candidate unavailable")
	}
	return candidate, nil
}
