package hookstatus

import (
	"context"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

// Observe is the single scoped non-agent HC observer. Its event connection is
// dedicated; reconciliation uses independent bounded native connections.
func Observe(ctx context.Context, s *store.Store, e store.Endpoint, runtimeRoot, sumctlPath string) error {
	if e.CoreID == "" || e.Session == "" || !filepath.IsAbs(e.Socket) {
		return fmt.Errorf("explicit HC observer scope required")
	}
	release, err := scopeLock(s, e, "running")
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	dirty := make(chan struct{}, 1)
	ready := make(chan struct{})
	watchErrors := make(chan error, 1)
	go func() { watchErrors <- watchDirty(ctx, filepath.Join(s.Home, HookDir, "dirty"), dirty, ready) }()
	select {
	case <-ready:
	case err := <-watchErrors:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
	client := hcclient.Client{Socket: e.Socket, CoreID: e.CoreID}
	// The helper attests its exact HC-created process before it can reconcile.
	address := e.Address
	if address == "" {
		address = os.Getenv("HC_TERMINAL_ID")
	}
	if address == "" {
		return fmt.Errorf("observer must run in its recorded HC terminal")
	}
	terminal, err := client.Get(ctx, address)
	if err != nil {
		return err
	}
	health, err := readHealth(s)
	if err != nil {
		return err
	}
	args := []string{sumctlPath, "--home", s.Root, "hook", "observe", "--core-id", e.CoreID, "--session", e.Session, "--socket", e.Socket}
	attempt, _ := value(health, "launch_attempt").(string)
	effect, _ := health.Get("launch_effect")
	core, _ := health.Get("core_id")
	session, _ := health.Get("session")
	socket, _ := health.Get("socket")
	expectedArgs := make([]string, 0, len(args))
	rawArgs, _ := health.Get("argv")
	for _, raw := range asList(rawArgs) {
		arg, ok := raw.(string)
		if !ok {
			return fmt.Errorf("observer launch argv is malformed")
		}
		expectedArgs = append(expectedArgs, arg)
	}
	if attempt == "" || terminal.Labels["sum.attempt"] != attempt || (effect != "in-flight" && effect != "submitted") || core != e.CoreID || session != e.Session || socket != e.Socket || !reflect.DeepEqual(expectedArgs, args) {
		return fmt.Errorf("observer has no matching owned launch intent for this scope")
	}
	if raw, _ := health.Get("observer"); raw != nil {
		recorded := store.EndpointFromContext(asObject(raw))
		if recorded.Address != "" && (recorded.CoreID != e.CoreID || recorded.Address != address || recorded.CreatedMS != terminal.CreatedMS) {
			return fmt.Errorf("observer launch endpoint differs from the current binding")
		}
	}

	if terminal.State != "live" || terminal.Keep || terminal.PID == nil || terminal.Foreground == nil || !reflect.DeepEqual(terminal.Argv, args) {
		return fmt.Errorf("observer terminal program cannot be verified")
	}
	e.Schema = 2
	e.Backend = "hc"
	host, err := s.Machine()
	if err != nil {
		return err
	}
	e.Machine = host.ID
	e.Role = "observer"
	e.Attempt = attempt
	e.BindingGeneration = 1
	e.Address = address
	e.CreatedMS = terminal.CreatedMS
	evidence := incarnation.NativeEvidence{Endpoint: e.Native(), PID: *terminal.PID, Foreground: terminal.Foreground}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: e.Native(), Kind: "observer-start", Attempt: e.Attempt})
	proof := ordjson.NewObject()
	proof.Set("observer", e.Object())
	proof.Set("observer_incarnation", evidence.Record(store.Now()))
	proof.Set("observer_receipt", receipt)
	if _, err = writeHealthForAttempt(s, attempt, proof); err != nil {
		return err
	}

	backoff := 100 * time.Millisecond
	for {
		reconnect, err, established := observeConnection(ctx, s, e, runtimeRoot, sumctlPath, dirty, watchErrors)
		if !reconnect {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		changes := ordjson.NewObject()
		if err != nil {
			changes.Set("degraded", err.Error())
		}
		if _, writeErr := writeHealthForAttempt(s, attempt, changes); writeErr != nil {
			return writeErr
		}
		if established {
			backoff = 100 * time.Millisecond
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		case <-dirty:
			timer.Stop()
		case err = <-watchErrors:
			timer.Stop()
			return err
		}
		backoff = min(5*time.Second, backoff*2)
	}
}

func observeConnection(ctx context.Context, s *store.Store, e store.Endpoint, runtimeRoot, sumctlPath string, dirty <-chan struct{}, watchErrors <-chan error) (bool, error, bool) {
	established := false
	enabled, present, err := settings.NativeEventsPreference(s)
	if err != nil {
		return false, err, established
	}
	if present && !enabled {
		return false, nil, established
	}
	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()
	client := hcclient.Client{Socket: e.Socket, CoreID: e.CoreID}
	instance, err := s.Instance()
	if err != nil {
		return false, err, established
	}
	sub, err := client.Subscribe(streamCtx, hcclient.SubscribeParams{Labels: map[string]string{"sum.installation": instance}})
	if err != nil {
		return retrySubscription(ctx, err), err, established
	}
	defer sub.Close()
	events := make(chan hcclient.Notification, 1)
	streamErrors := make(chan error, 1)
	go func() {
		for {
			event, err := sub.Next()
			if err != nil {
				select {
				case streamErrors <- err:
				case <-streamCtx.Done():
				}
				return
			}
			select {
			case events <- event:
			case <-streamCtx.Done():
				return
			}
		}
	}()
	if _, err = Reconcile(ctx, s, e, runtimeRoot, sumctlPath); err != nil {
		return retryNativeObservation(ctx, err), err, established
	}
	established = true
	changes := ordjson.NewObject()
	changes.Set("degraded", nil)
	changes.Set("connected_at", store.Now())
	changes.Set("snapshot_seq", jsonInt(int(sub.Snapshot.Seq)))
	if _, writeErr := writeHealthForAttempt(s, e.Attempt, changes); writeErr != nil {
		return false, writeErr, established
	}
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err(), established
		case err = <-watchErrors:
			return false, err, established
		case err = <-streamErrors:
			return retrySubscription(ctx, err), err, established
		case event := <-events:
			if event.Method == "heartbeat" {
				continue
			}
			if event.Method == "missed" {
				return true, nil, established
			}
			if _, err = Reconcile(ctx, s, e, runtimeRoot, sumctlPath); err != nil {
				return retryNativeObservation(ctx, err), err, established
			}
		case <-dirty:
			enabled, present, err = settings.NativeEventsPreference(s)
			if err != nil {
				return false, err, established
			}
			if present && !enabled {
				return false, nil, established
			}
			if _, err = Reconcile(ctx, s, e, runtimeRoot, sumctlPath); err != nil {
				return retryNativeObservation(ctx, err), err, established
			}
		}
	}
}
