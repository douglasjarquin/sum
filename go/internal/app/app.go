package app

import (
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const MaxText = 256 * 1024

func TextInput(text, file string) (string, error) {
	var value string
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		value = string(data)
	} else {
		value = text
	}
	if value == "" || isBlank(value) {
		return "", fmt.Errorf("Text must not be empty.")
	}
	if len([]byte(value)) > MaxText {
		return "", fmt.Errorf("Text exceeds %d bytes; use a concise report and reference artifacts.", MaxText)
	}
	return value, nil
}

func isBlank(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' && r != '\v' && r != '\f' {
			return false
		}
		_ = utf8.RuneLen(r)
	}
	return true
}

func RequireCoordinator(s *store.Store, ctx *ordjson.Object) error {
	endpoint := store.EndpointFromContext(ctx)
	registration, err := s.Registration(endpoint)
	if err != nil {
		return err
	}
	owner, err := s.Owner()
	if err != nil {
		return err
	}
	role, _ := registrationField(registration, "role")
	owns := false
	if owner != nil {
		if owns, err = s.Matches(owner, endpoint); err != nil {
			return err
		}
	}
	if registration == nil || role != "coordinator" || !owns {
		return fmt.Errorf("This pane is not the registered coordinator of this sum instance. Run ./bin/sumctl init in the coordinator pane; a developer session must not dispatch or rebind.")
	}
	// The address matches; the occupant must be the one that holds the role. A restored or reused pane ID with a new
	// occupant, or one Herdr cannot report, gets no coordinator authority.
	recorded, occupiedAt := incarnation.CoordinatorRecord(owner)
	if verdict := incarnation.Caller(endpoint.Session, endpoint.Pane, recorded, occupiedAt); !verdict.Verified {
		return fmt.Errorf("This pane is the recorded coordinator pane, but its occupant is not the recorded coordinator (%s: %s). %s", verdict.Outcome, verdict.Reason, incarnation.Recovery("coordinator", verdict.Outcome))
	}
	return nil
}

func registrationField(registration *ordjson.Object, key string) (string, bool) {
	if registration == nil {
		return "", false
	}
	value, ok := registration.Get(key)
	s, isString := value.(string)
	return s, ok && isString
}

func OptionalContext(root string) *ordjson.Object {
	ctx, err := store.Context(root)
	if err != nil {
		return nil
	}
	return ctx
}
