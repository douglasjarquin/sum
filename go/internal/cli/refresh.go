package cli

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/refreshcmd"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addRefreshCommands(root *cobra.Command) {
	refreshCmd := &cobra.Command{
		Use:                "refresh",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	var statusTasks []string
	statusCmd := &cobra.Command{
		Use:  "status",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("refresh-status")
			if err != nil {
				return err
			}
			view, err := refreshcmd.Status(st, statusTasks)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	statusCmd.Flags().StringArrayVar(&statusTasks, "task", nil, "")
	refreshCmd.AddCommand(statusCmd)

	var requestTasks []string
	var coordinator bool
	requestCmd := &cobra.Command{
		Use:  "request",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("refresh-request")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := refreshcmd.Request(st, ctx, requestTasks, coordinator, o.runtimeRoot, o.sumctlPath())
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	requestCmd.Flags().StringArrayVar(&requestTasks, "task", nil, "")
	requestCmd.Flags().BoolVar(&coordinator, "coordinator", false, "")
	refreshCmd.AddCommand(requestCmd)

	var adoptCoordinator bool
	adoptCmd := &cobra.Command{
		Use:  "adopt REVISION",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !adoptCoordinator {
				return usageError("refresh adopt", args)
			}
			st, err := o.openStore("refresh-adopt")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := refreshcmd.AdoptCoordinator(st, ctx, args[0])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	adoptCmd.Flags().BoolVar(&adoptCoordinator, "coordinator", false, "")
	refreshCmd.AddCommand(adoptCmd)

	root.AddCommand(refreshCmd)
}
