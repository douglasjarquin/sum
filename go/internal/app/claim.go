package app

import (
	"fmt"
	"slices"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func RequireClaim(s *store.Store, token string) (*ordjson.Object, error) {
	session, err := s.CheckClaim(token)
	if err != nil {
		return nil, err
	}
	if err := s.CaptureClaim(token); err != nil {
		return nil, err
	}
	ctx := session.Owner()
	ctx.Set("role", "coordinator")
	ctx.Set("claim", token)
	return ctx, nil
}

func logicalContext(ctx *ordjson.Object) bool {
	if ctx == nil {
		return false
	}
	kind, _ := ctx.Get("kind")
	return kind == "coordinator-session"
}

func observeLogicalActor(s *store.Store, ctx *ordjson.Object) (*TaskActor, error) {
	value, _ := ctx.Get("claim")
	token, _ := value.(string)
	session, err := s.CheckClaim(token)
	if err != nil {
		return nil, err
	}
	if !store.LogicalOwnersMatch(ctx, session.Owner()) {
		return nil, fmt.Errorf("logical actor does not match the claimed session")
	}
	return &TaskActor{claim: token}, nil
}

func checkLogicalTaskRole(s *store.Store, task *ordjson.Object, actor *TaskActor, roles ...string) error {
	if !slices.Contains(roles, "coordinator") {
		return fmt.Errorf("logical actor does not have the required task role")
	}
	session, err := s.CheckClaim(actor.claim)
	if err != nil {
		return err
	}
	ownerValue, _ := task.Get("owner")
	owner, _ := ownerValue.(*ordjson.Object)
	if store.LogicalOwnersMatch(owner, session.Owner()) {
		return nil
	}
	if owner == nil && session.RetiredNative != nil {
		parentValue, _ := task.Get("parent")
		parent, _ := parentValue.(*ordjson.Object)
		matches, err := s.Matches(parent, store.EndpointFromContext(session.RetiredNative))
		if err != nil {
			return err
		}
		if matches {
			return nil
		}
	}
	return fmt.Errorf("task belongs to another coordinator session")
}
