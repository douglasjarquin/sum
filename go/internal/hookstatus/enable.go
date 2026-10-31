package hookstatus

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
)

func scopeKey(s *store.Store, e store.Endpoint) string {
	hash := sha256.Sum256([]byte(s.Root + "\x00" + e.CoreID + "\x00" + e.Session))
	return hex.EncodeToString(hash[:12])
}
func scopeLock(s *store.Store, e store.Endpoint, kind string) (func(), error) {
	if err := os.MkdirAll(filepath.Join(s.Home, HookDir), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(s.Root, "observer-"+scopeKey(s, e)+"-"+kind+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another observer owns this installation/core/session scope: %w", err)
	}
	return func() { syscall.Flock(int(file.Fd()), syscall.LOCK_UN); file.Close() }, nil
}
func Enable(s *store.Store, ctx *ordjson.Object, runtimeRoot, sumctlPath string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	if err := settings.SetNativeEventsEnabled(s, true); err != nil {
		return nil, err
	}
	changes := ordjson.NewObject()
	changes.Set("disabled_at", nil)
	if _, err := writeHealth(s, nil, changes); err != nil {
		return nil, err
	}
	return ensureObserver(s, store.EndpointFromContext(ctx), runtimeRoot, sumctlPath, true)
}
func ensureObserver(s *store.Store, e store.Endpoint, runtimeRoot, sumctlPath string, reconcile bool) (*ordjson.Object, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	release, err := scopeLock(s, e, "launch")
	if err != nil {
		return nil, err
	}
	defer release()
	leaseCtx, cancelLease := context.WithTimeout(context.Background(), returns.DefaultPassBudget)
	defer cancelLease()
	releaseLease, err := s.DeliveryShared(leaseCtx)
	if err != nil {
		return nil, err
	}
	defer releaseLease()
	if err = s.CheckWritable(); err != nil {
		return nil, err
	}
	health, err := readHealth(s)
	if err != nil {
		return nil, err
	}
	client := hcclient.Client{Socket: e.Socket, CoreID: e.CoreID}
	args := []string{sumctlPath, "--home", s.Root, "hook", "observe", "--core-id", e.CoreID, "--session", e.Session, "--socket", e.Socket}
	prior, _ := health.Get("observer")
	old := store.EndpointFromContext(asObject(prior))
	if old.CoreID != "" {
		if old.CoreID != e.CoreID || old.Session != e.Session {
			return nil, fmt.Errorf("this installation observes one HC core/session (%s/%s); bind the recipient in that scope, or explicitly disable automatic native events and use manual pump before binding another scope", old.CoreID, old.Session)
		}
		terminal, getErr := client.Get(context.Background(), old.Address)
		if getErr == nil && observerProof(health, old, terminal) == nil && reflect.DeepEqual(terminal.Argv, args) {
			changes := ordjson.NewObject()
			changes.Set("enabled", true)
			changes.Set("degraded", nil)
			return writeHealthForAttempt(s, old.Attempt, changes)
		}
		if getErr == nil && terminal.State == "live" {
			return nil, fmt.Errorf("recorded observer process or receipt changed; explicit recovery is required")
		}
		if getErr != nil {
			return nil, fmt.Errorf("recorded observer cannot be observed; no replacement launched: %w", getErr)
		}
	}
	effect, _ := health.Get("launch_effect")
	if effect == "in-flight" || effect == "uncertain" || effect == "claimed" || effect == "unknown" {
		return nil, fmt.Errorf("observer launch is uncertain; inspect exact native scope before replacement")
	}
	dirty, err := os.OpenFile(filepath.Join(s.Home, HookDir, "dirty"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	dirty.Close()
	attempt, err := beginObserverLaunch(s, health, e, args)
	if err != nil {
		return nil, err
	}
	instance, err := s.Instance()
	if err != nil {
		return nil, err
	}
	result, runErr := client.Run(context.Background(), hcclient.RunParams{Argv: args, Cwd: runtimeRoot, NoParent: true, OutliveParent: true, NoNotify: true, Keep: false, RM: false, Labels: map[string]string{"sum.installation": instance, "sum.kind": "observer", "sum.scope": scopeKey(s, e), "sum.attempt": attempt}})
	if runErr != nil {
		effect := "uncertain"
		if native, ok := runErr.(*hcclient.Error); ok && native.Effect != hcclient.Uncertain {
			effect = "not-submitted"
		}
		changes := ordjson.NewObject()
		changes.Set("launch_effect", effect)
		changes.Set("degraded", runErr.Error())
		_, _ = writeHealthForAttempt(s, attempt, changes)
		return nil, runErr
	}
	observer := e
	observer.Role = "observer"
	observer.Task = ""
	observer.Attempt = attempt
	observer.BindingGeneration = 1
	observer.Address = result.Address
	observer.CreatedMS = result.CreatedMS
	changes := ordjson.NewObject()
	changes.Set("observer", observer.Object())
	changes.Set("launch_effect", "submitted")
	changes.Set("degraded", "observer startup is unconfirmed; native helper creation alone is not readiness")
	out, err := writeHealthForAttempt(s, attempt, changes)
	if err != nil {
		return nil, err
	}
	if reconcile {
		_, _ = Reconcile(context.Background(), s, e, runtimeRoot, sumctlPath)
	}
	return out, nil
}
func EnableByDefault(s *store.Store, ctx *ordjson.Object, runtimeRoot, sumctlPath string, reconcile bool) *ordjson.Object {
	enabled, present, err := settings.NativeEventsPreference(s)
	if err == nil && present && !enabled {
		row := ordjson.NewObject()
		row.Set("enabled", false)
		row.Set("skipped", true)
		return row
	}
	if err == nil && !present {
		if health, e := readHealth(s); e == nil {
			if disabled, _ := health.Get("disabled_at"); disabled != nil {
				_ = settings.SetNativeEventsEnabled(s, false)
				row := ordjson.NewObject()
				row.Set("enabled", false)
				row.Set("skipped", true)
				return row
			}
		}
	}
	var row *ordjson.Object
	if err == nil {
		row, err = ensureObserver(s, store.EndpointFromContext(ctx), runtimeRoot, sumctlPath, reconcile)
	}
	if err != nil {
		row = ordjson.NewObject()
		row.Set("enabled", false)
		row.Set("degraded", true)
		row.Set("reason", err.Error())
	}
	return row
}
func Disable(s *store.Store, ctx *ordjson.Object, runtimeRoot string, unlink bool) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	if err := settings.SetNativeEventsEnabled(s, false); err != nil {
		return nil, err
	}
	changes := ordjson.NewObject()
	changes.Set("enabled", false)
	changes.Set("disabled_at", store.Now())
	changes.Set("note", "delivery disabled; the observer exits on this ledger signal; explicit pump remains available")
	health, err := writeHealth(s, nil, changes)
	if err != nil {
		return nil, err
	}
	_ = markDirty(s)
	return health, nil
}
func markDirty(s *store.Store) error {
	file, err := os.OpenFile(filepath.Join(s.Home, HookDir, "dirty"), os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = file.WriteAt([]byte{1}, 0)
	file.Close()
	return err
}

func observerProof(record *ordjson.Object, current store.Endpoint, terminal hcclient.Terminal) error {
	if value(record, "launch_attempt") != current.Attempt {
		return fmt.Errorf("observer proof does not belong to the current launch intent")
	}
	var evidence incarnation.NativeEvidence
	raw, _ := record.Get("observer_incarnation")
	data, err := ordjson.MarshalCompact(raw)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &evidence); err != nil {
		return err
	}
	var receipt incarnation.NativeReceipt
	raw, _ = record.Get("observer_receipt")
	data, err = ordjson.MarshalCompact(raw)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &receipt); err != nil {
		return err
	}
	if !evidence.Endpoint.SameAuthority(current.Native()) || !receipt.Endpoint.SameAuthority(current.Native()) || receipt.Kind != "observer-start" || receipt.Attempt != current.Attempt || terminal.Address != current.Address || terminal.CreatedMS != current.CreatedMS || terminal.State != "live" || terminal.Keep || terminal.PID == nil || *terminal.PID != evidence.PID || terminal.Foreground == nil || evidence.Foreground == nil || !reflect.DeepEqual(terminal.Foreground, evidence.Foreground) {
		return fmt.Errorf("observer process and matching scope receipt cannot be verified")
	}
	return nil
}

func beginObserverLaunch(s *store.Store, observed *ordjson.Object, e store.Endpoint, args []string) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	attempt := "observer-" + hex.EncodeToString(nonce)
	unlock, err := s.Lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	if err = s.CheckWritable(); err != nil {
		return "", err
	}
	current, err := readHealth(s)
	if err != nil {
		return "", err
	}
	for _, key := range []string{"observer", "observer_incarnation", "observer_receipt", "launch_attempt", "launch_effect"} {
		a, _ := ordjson.MarshalCompact(value(observed, key))
		b, _ := ordjson.MarshalCompact(value(current, key))
		if string(a) != string(b) {
			return "", fmt.Errorf("observer binding changed before launch claim; no helper launched")
		}
	}
	if prior := value(current, "observer"); prior != nil {
		retired := ordjson.NewObject()
		for _, key := range []string{"observer", "observer_incarnation", "observer_receipt", "launch_attempt"} {
			retired.Set(key, value(current, key))
		}
		retired.Set("retired_at", store.Now())
		current.Set("retired_observers", append(asList(value(current, "retired_observers")), retired))
	}
	for _, key := range []string{"observer", "observer_incarnation", "observer_receipt"} {
		current.Set(key, nil)
	}
	current.Set("connected_at", nil)
	current.Set("snapshot_seq", nil)
	current.Set("degraded", "observer launch claimed; startup is unconfirmed")
	current.Set("launch_attempt", attempt)
	current.Set("launch_effect", "in-flight")
	current.Set("enabled", true)
	current.Set("core_id", e.CoreID)
	current.Set("session", e.Session)
	current.Set("socket", e.Socket)
	argv := make([]any, len(args))
	for i, arg := range args {
		argv[i] = arg
	}
	current.Set("argv", argv)
	current.Set("updated_at", store.Now())
	current.Set("schema", jsonInt(HookSchema))
	if err = ordjson.WriteFile(filepath.Join(s.Home, HookDir, HookHealth), current); err != nil {
		return "", err
	}
	return attempt, nil
}
