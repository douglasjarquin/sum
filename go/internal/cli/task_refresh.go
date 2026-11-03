package cli

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/refreshcmd"
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
	requestCmd := &cobra.Command{
		Use:         "request",
		Annotations: map[string]string{projectsAnnotation: "all"},
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("refresh-request")
			if err != nil {
				return err
			}
			ctx, err := o.context(st)
			if err != nil {
				return err
			}
			view, err := refreshcmd.Request(st, ctx, requestTasks, o.runtimeRoot, o.sumctlPath())
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	requestCmd.Flags().StringArrayVar(&requestTasks, "task", nil, "")
	refreshCmd.AddCommand(requestCmd)

	root.AddCommand(refreshCmd)
}
