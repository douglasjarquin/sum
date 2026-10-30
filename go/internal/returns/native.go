package returns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// NativeRecipient is a fresh public HC observation, never an application receipt.
// Callers serialize by recipient, revalidate authority and persist their claim
// before Send. No state lock may be held during either operation.
type NativeRecipient struct {
	Endpoint      store.Endpoint
	Terminal      hcclient.Terminal
	qualified     bool
	qualification []byte
}

type NativeTemplate struct {
	Spec       launch.HCTemplate        `json:"spec"`
	ConfigHome string                   `json:"config_home"`
	Options    launch.HCTemplateOptions `json:"options"`
}

func ObserveNative(ctx context.Context, endpoint store.Endpoint, expectedCwd string) (*NativeRecipient, error) {
	if err := endpoint.Validate(); err != nil {
		return nil, &unreachableError{state: "pending-unreachable", msg: err.Error()}
	}
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID, Timeout: ObserveTimeout}
	terminal, err := client.Get(ctx, endpoint.Address)
	if err != nil {
		return nil, &unreachableError{state: "pending-unreachable", msg: "Recipient cannot be observed: " + err.Error(), cause: err}
	}
	return checkNativeTerminal(endpoint, terminal, expectedCwd)
}

func checkNativeTerminal(endpoint store.Endpoint, terminal hcclient.Terminal, expectedCwd string) (*NativeRecipient, error) {
	if _, err := checkNativeIdentity(endpoint, terminal, expectedCwd); err != nil {
		return nil, err
	}
	status := "unknown"
	if terminal.Status != nil {
		status = terminal.Status.State
	}
	if status != "idle" && status != "done" {
		return nil, &unreachableError{state: "pending-busy", msg: fmt.Sprintf("Recipient is %s; notice remains pending.", status)}
	}
	return &NativeRecipient{Endpoint: endpoint, Terminal: terminal}, nil
}

// observeNativeIdentity permits passive reads regardless of input readiness.
// The binding and process receipt are still judged before presentation.
func observeNativeIdentity(ctx context.Context, endpoint store.Endpoint, expectedCwd string) (*NativeRecipient, error) {
	if err := endpoint.Validate(); err != nil {
		return nil, err
	}
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID, Timeout: ObserveTimeout}
	terminal, err := client.Get(ctx, endpoint.Address)
	if err != nil {
		return nil, err
	}
	return checkNativeIdentity(endpoint, terminal, expectedCwd)
}
func checkNativeIdentity(endpoint store.Endpoint, terminal hcclient.Terminal, expectedCwd string) (*NativeRecipient, error) {
	if terminal.Address != endpoint.Address || terminal.CreatedMS != endpoint.CreatedMS || terminal.Keep {
		return nil, &unreachableError{state: "recipient-replaced", msg: "Recipient native terminal creation or non-kept identity was replaced; explicit rebind is required."}
	}
	if terminal.State != "live" {
		return nil, &unreachableError{state: "pending-unreachable", msg: "Recipient non-kept HC terminal is absent, finished or replaced; explicit recovery is required."}
	}
	if expectedCwd != "" && (terminal.Cwd == "" || resolve(terminal.Cwd) != resolve(expectedCwd)) {
		return nil, &unreachableError{state: "pending-unreachable", msg: "Recipient cwd cannot be verified."}
	}
	return &NativeRecipient{Endpoint: endpoint, Terminal: terminal}, nil
}

// Qualify verifies saved command/keymap/template provenance without modifying it.
func (r *NativeRecipient) Qualify(record *ordjson.Object) error {
	return r.QualifyContext(context.Background(), record)
}

func (r *NativeRecipient) QualifyContext(ctx context.Context, record *ordjson.Object) error {
	r.qualified = false
	r.qualification = nil
	var raw any
	if record != nil {
		raw, _ = record.Get("native_template")
	}
	data, err := ordjson.MarshalCompact(raw)
	if err != nil {
		return err
	}
	var template NativeTemplate
	if err = json.Unmarshal(data, &template); err != nil {
		return err
	}
	if r.Terminal.Template == nil || *r.Terminal.Template != template.Spec.Name || r.Terminal.Submit == nil || *r.Terminal.Submit != "tab" {
		return fmt.Errorf("recipient has no matching qualified Tab template")
	}
	if err = launch.ValidateHCTemplate(template.ConfigHome, template.Spec, template.Options); err != nil {
		return err
	}
	client := hcclient.Client{Socket: r.Endpoint.Socket, CoreID: r.Endpoint.CoreID, Timeout: ObserveTimeout}
	templates, err := client.Templates(ctx)
	if err != nil {
		return err
	}
	expected := launch.TemplateCommand(template.Spec.Argv)
	matched := false
	for _, native := range templates.Templates {
		if native.Name == template.Spec.Name && native.Error == nil && native.Command == expected {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf("selected HC core does not advertise the qualified template command")
	}
	r.qualified = true
	r.qualification = append([]byte(nil), data...)
	return nil
}

func (r *NativeRecipient) Send(ctx context.Context, text string) (hcclient.SendResult, error) {
	params := hcclient.SendParams{Terminal: r.Endpoint.Address, Text: text}
	switch {
	case r.Terminal.Status == nil:
		return hcclient.SendResult{}, &hcclient.Error{Effect: hcclient.NotSubmitted, Method: "terminal.send", Cause: fmt.Errorf("unknown recipient status")}
	case r.Terminal.Status.State == "idle":
		params.If = "idle"
	case r.Terminal.Status.State == "done" && r.qualified:
	default:
		return hcclient.SendResult{}, &hcclient.Error{Effect: hcclient.NotSubmitted, Method: "terminal.send", Cause: fmt.Errorf("recipient is busy or lacks qualified Done input provenance")}
	}
	client := hcclient.Client{Socket: r.Endpoint.Socket, CoreID: r.Endpoint.CoreID, Timeout: PromptTimeout}
	return client.Send(ctx, params)
}

// DeliveryFailure is the common effect classification for every native send.
func DeliveryFailure(err error) (state, detail string) {
	if err == nil {
		return "submitted", "native input submitted; a matching application receipt is still required"
	}
	var effect *hcclient.Error
	if errors.As(err, &effect) && effect.Effect == hcclient.Uncertain {
		return "uncertain", "native input may have been submitted: " + err.Error() + "; left ambiguous, not retried by itself"
	}
	return "not-delivered", "native input was not accepted: " + err.Error()
}
