package cli

import (
	"time"

	"github.com/douglasjarquin/sum/go/internal/collect"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addCollectCommand(root *cobra.Command) {
	var claim, task string
	var budget time.Duration
	cmd := &cobra.Command{
		Use: "collect", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if budget <= 0 || budget > collect.MaximumBudget {
				return usageError("collect", []string{"--budget", budget.String()})
			}
			st, err := o.openStore("collect")
			if err != nil {
				return err
			}
			view, err := collect.Run(cmd.Context(), st, collect.Options{Claim: claim, Task: task, Budget: budget, RuntimeRoot: o.runtimeRoot, SumctlPath: o.sumctlPath()})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	cmd.Flags().StringVar(&claim, "claim", "", "Current logical session claim")
	cmd.Flags().StringVar(&task, "task", "", "Collect one recorded task")
	cmd.Flags().DurationVar(&budget, "budget", collect.DefaultBudget, "Shared collection deadline (maximum 60s)")
	root.AddCommand(cmd)
}
