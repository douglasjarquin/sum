package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/usage"
)

func (o *rootOptions) addUsageCommands(root *cobra.Command) {
	usageCmd := &cobra.Command{
		Use:  "usage",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}
	usageCmd.AddCommand(&cobra.Command{
		Use:  "show TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view, err := usage.View(st, args[0])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})
	usageCmd.AddCommand(&cobra.Command{
		Use:  "collect TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("usage-collect")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			if err := app.RequireCoordinator(st, ctx); err != nil {
				return err
			}
			result, err := usage.Collect(st, args[0])
			if err != nil {
				return err
			}
			if enabled, _ := result.Get("enabled"); enabled == false {
				return fmt.Errorf("Usage accounting is off; nothing was read. Enable it with `settings set --usage-accounting true`.")
			}
			return emitOrdjson(cmd.OutOrStdout(), result)
		},
	})
	root.AddCommand(usageCmd)
}
