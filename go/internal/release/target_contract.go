package release

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/proc"
)

// ReadTargetContract executes the staged artifact's read-only report. Source
// markers and the stager's own contract cannot prove what that artifact supports.
func ReadTargetContract(ctx context.Context, target string) (contract.Release, error) {
	var offered contract.Release
	helper := filepath.Join(target, ".local", "bin", "sumctl")
	info, err := os.Lstat(helper)
	if err != nil {
		return offered, fmt.Errorf("inspect HC target helper: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return offered, fmt.Errorf("HC target helper must be its own executable artifact")
	}
	result, err := proc.RunContext(ctx, proc.Cmd{Argv: []string{helper, "--format", "json", "release-contract"}, Dir: target, Timeout: 20 * time.Second, Env: append(proc.ScrubbedEnv(), "SUM_INSTALL_ROOT="+target)})
	if err != nil {
		return offered, fmt.Errorf("inspect HC target contract: %w", err)
	}
	if result.Code != 0 {
		return offered, fmt.Errorf("HC target capability report failed: %s", result.Detail())
	}
	if err = json.Unmarshal([]byte(result.Stdout), &offered); err != nil {
		return offered, fmt.Errorf("HC target capability report is not JSON: %w", err)
	}
	if err = ValidateNativeContract(offered); err != nil {
		return offered, err
	}
	return offered, nil
}

func ValidateNativeContract(offered contract.Release) error {
	c, s := offered.Contracts, offered.Supports
	if c.HCCore != contract.HCVersion || c.HCGeneration != contract.HCGeneration || c.EndpointSchema != contract.EndpointSchema || c.NotificationProtocol != contract.NotificationProtocol {
		return fmt.Errorf("target lacks the selected HC runtime, endpoint or notification contract")
	}
	for _, required := range []struct {
		name    string
		values  []int
		version int
	}{
		{"state", s.StateSchema, contract.StateSchema}, {"brief", s.BriefSchema, contract.BriefSchema}, {"endpoint", s.EndpointSchema, contract.EndpointSchema}, {"notification", s.NotificationProtocol, contract.NotificationProtocol}, {"observer", s.ObserverProtocol, contract.ObserverProtocol}, {"machine identity", s.MachineIdentity, contract.MachineIdentity}, {"wake", s.WakeProtocol, contract.WakeProtocol},
	} {
		if !slices.Contains(required.values, required.version) {
			return fmt.Errorf("target lacks HC %s capability %d", required.name, required.version)
		}
	}
	return nil
}
