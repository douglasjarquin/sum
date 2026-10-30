package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"os"
	"time"
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
	if err := s.CheckWritable(); err != nil {
		return err
	}
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
		return fmt.Errorf("This terminal is not the registered coordinator of this sum instance. Run the installed helper init in its coordinator terminal; a developer session must not dispatch or rebind.")
	}
	_, terminal, err := ObserveNative(endpoint)
	if err != nil {
		return err
	}
	recorded, _ := owner.Get("incarnation")
	receipt, err := NativeReceipt(owner)
	if err != nil {
		return err
	}
	verdict := incarnation.JudgeNative(recorded, endpoint.Native(), terminal, receipt)
	if !verdict.Verified {
		return fmt.Errorf("recorded coordinator authority is not current (%s: %s)", verdict.Outcome, verdict.Reason)
	}
	return nil
}

// ObserveNative reads public runtime facts from an existing identity-checked core.
// It never starts a core or treats runtime status as application authority.
func ObserveNative(endpoint store.Endpoint) (incarnation.NativeEvidence, hcclient.Terminal, error) {
	var evidence incarnation.NativeEvidence
	if err := endpoint.Validate(); err != nil {
		return evidence, hcclient.Terminal{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID}
	terminal, err := client.Get(ctx, endpoint.Address)
	if err != nil {
		return evidence, terminal, err
	}
	if terminal.Address != endpoint.Address || terminal.CreatedMS != endpoint.CreatedMS || terminal.State != "live" || terminal.Keep || terminal.PID == nil || *terminal.PID == 0 || terminal.Foreground == nil || terminal.Foreground.PID <= 0 {
		return evidence, terminal, fmt.Errorf("current non-kept HC terminal program and foreground cannot be verified")
	}
	evidence = incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: *terminal.PID, Foreground: terminal.Foreground}
	return evidence, terminal, nil
}

func NativeReceipt(record *ordjson.Object) (*incarnation.NativeReceipt, error) {
	if record == nil {
		return nil, fmt.Errorf("matching native bootstrap receipt required")
	}
	value, _ := record.Get("native_receipt")
	raw, err := ordjson.MarshalCompact(value)
	if err != nil {
		return nil, err
	}
	var receipt incarnation.NativeReceipt
	if err = json.Unmarshal(raw, &receipt); err != nil || receipt.Endpoint.Validate() != nil {
		return nil, fmt.Errorf("matching native bootstrap receipt required")
	}
	return &receipt, nil
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
