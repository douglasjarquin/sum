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
	sumruntime "github.com/douglasjarquin/sum/go/internal/runtime"
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
	if logicalContext(ctx) {
		value, _ := ctx.Get("claim")
		token, _ := value.(string)
		_, err := RequireClaim(s, token)
		return err
	}
	if err := s.CheckWritable(); err != nil {
		return err
	}
	return store.ErrClaimRequired
}

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
	if terminal.Address != endpoint.Address || terminal.CreatedMS != endpoint.CreatedMS || terminal.State != "live" || terminal.PID == nil || *terminal.PID == 0 || terminal.Foreground == nil || terminal.Foreground.PID <= 0 {
		return evidence, terminal, fmt.Errorf("current HC terminal program and foreground cannot be verified")
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

func Context(s *store.Store, root, claim string) (*ordjson.Object, error) {
	if claim != "" {
		return RequireClaim(s, claim)
	}
	role := os.Getenv("SUM_ROLE")
	taskIdentity := os.Getenv("SUM_TASK") != "" || role == "worker" || role == "reviewer"
	if !taskIdentity || !sumruntime.InheritedHomeBelongsToInstallation(root) {
		return nil, store.ErrClaimRequired
	}
	return store.Context(root)
}

func OptionalContext(s *store.Store, root, claim string) *ordjson.Object {
	ctx, err := Context(s, root, claim)
	if err != nil {
		return nil
	}
	return ctx
}
