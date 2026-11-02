package launch

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/proc"
)

func DiscoverOriginal(ctx context.Context, client *hcclient.Client, intent hcclient.RunParams, spec HCTemplate, claimedAt string) (hcclient.Terminal, error) {
	var empty hcclient.Terminal
	if spec.Schema != 2 || len(intent.Argv) != 1 || intent.Argv[0] != spec.Name || intent.Name == "" || intent.Keep || intent.Labels["sum.attempt"] == "" {
		return empty, fmt.Errorf("historical or incomplete creation intent cannot prove an unsent split opening; inspect without replay")
	}
	claimed, err := time.Parse(time.RFC3339Nano, claimedAt)
	if err != nil {
		return empty, err
	}
	result, err := client.List(ctx, hcclient.ListParams{All: true, Labels: intent.Labels})
	if err != nil {
		return empty, err
	}
	var matches []hcclient.Terminal
	for _, terminal := range result.Terminals {
		if terminal.Name != nil && *terminal.Name == intent.Name {
			matches = append(matches, terminal)
		}
	}
	if len(matches) != 1 {
		return empty, fmt.Errorf("creation remains ambiguous: found %d exact-name candidates; inspect without relaunch", len(matches))
	}
	terminal := matches[0]
	if terminal.Keep || terminal.CreatedMS < uint64(claimed.UnixMilli()) || terminal.CreatedMS > uint64(time.Now().Add(time.Second).UnixMilli()) {
		return empty, fmt.Errorf("candidate creation time or recovery policy cannot corroborate the original launch")
	}
	if terminal.Cwd != intent.Cwd {
		return empty, fmt.Errorf("candidate checkout differs from creation intent")
	}
	initial := terminal
	initial.Foreground = nil
	initial.Labels = intent.Labels
	if err = OriginalProgram(initial, terminal, spec); err != nil {
		return empty, err
	}
	local, err := proc.ProcessArgv(int(terminal.Foreground.PID))
	if err != nil {
		return empty, err
	}
	if !reflect.DeepEqual(local, terminal.Foreground.Argv) {
		return empty, fmt.Errorf("local original-process observation differs from HC; inspect and hand off without sending")
	}
	return terminal, nil
}
