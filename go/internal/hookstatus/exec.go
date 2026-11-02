package hookstatus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func observerLauncher(foreground *hcclient.Foreground, argv []string) bool {
	return foreground != nil && len(foreground.Argv) == len(argv)+1 && filepath.Base(foreground.Argv[0]) == "bash" && reflect.DeepEqual(foreground.Argv[1:], argv)
}

func reconcileObserverExec(ctx context.Context, s *store.Store, health *ordjson.Object, endpoint store.Endpoint, terminal hcclient.Terminal) (*ordjson.Object, error) {
	var evidence incarnation.NativeEvidence
	raw, _ := ordjson.MarshalCompact(value(health, "observer_incarnation"))
	if err := json.Unmarshal(raw, &evidence); err != nil {
		return nil, err
	}
	prior := terminal
	prior.Foreground = evidence.Foreground
	if err := observerProof(health, endpoint, prior); err != nil {
		return nil, err
	}
	var argv []string
	raw, err := ordjson.MarshalCompact(value(health, "argv"))
	if err != nil || json.Unmarshal(raw, &argv) != nil || len(argv) == 0 {
		return nil, fmt.Errorf("observer launch argv is missing or malformed")
	}
	foreground := terminal.Foreground
	if !observerLauncher(evidence.Foreground, argv) || !reflect.DeepEqual(terminal.Argv, argv) || foreground == nil || foreground.PID != int32(evidence.PID) || evidence.Foreground.PID != foreground.PID || foreground.Cwd != evidence.Foreground.Cwd || foreground.Cwd != terminal.Cwd || terminal.Labels["sum.attempt"] != endpoint.Attempt || terminal.Labels["sum.scope"] != scopeKey(s, endpoint) {
		return nil, fmt.Errorf("observer is not the recorded launcher's exec transition")
	}
	instance, err := s.Instance()
	if err != nil || terminal.Labels["sum.installation"] != instance {
		return nil, fmt.Errorf("observer installation label changed")
	}
	expected := append([]string{filepath.Join(terminal.Cwd, ".local", "bin", "sumctl")}, argv[1:]...)
	if !filepath.IsAbs(terminal.Cwd) || !reflect.DeepEqual(foreground.Argv, expected) {
		return nil, fmt.Errorf("observer native command does not match its saved runtime and scope")
	}
	info, err := os.Lstat(expected[0])
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return nil, fmt.Errorf("observer native executable cannot be verified")
	}
	local, err := proc.ProcessArgv(int(evidence.PID))
	if err != nil || !reflect.DeepEqual(local, expected) {
		return nil, fmt.Errorf("observer local process does not match the native command")
	}
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID}
	latest, err := client.Get(ctx, endpoint.Address)
	if err != nil || !reflect.DeepEqual(latest, terminal) {
		return nil, fmt.Errorf("observer changed during exec reconciliation")
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := readHealth(s)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"observer", "observer_incarnation", "observer_receipt", "launch_attempt", "launch_effect", "argv", "core_id", "session", "socket"} {
		before, _ := ordjson.MarshalCompact(value(health, key))
		after, _ := ordjson.MarshalCompact(value(current, key))
		if !bytes.Equal(before, after) {
			return nil, fmt.Errorf("observer proof changed during exec reconciliation")
		}
	}
	if err = s.CheckWritable(); err != nil {
		return nil, err
	}
	current.Set("observer_launcher_incarnation", value(current, "observer_incarnation"))
	evidence.Foreground = foreground
	current.Set("observer_incarnation", evidence.Record(store.Now()))
	if err = ordjson.WriteFile(filepath.Join(s.Home, HookDir, HookHealth), current); err != nil {
		return nil, err
	}
	return current, nil
}
