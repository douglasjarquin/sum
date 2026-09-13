package cli

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/brief"
	"github.com/douglasjarquin/sum/go/internal/guard"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addBriefCommands(root *cobra.Command) {
	briefCmd := &cobra.Command{
		Use:                "brief",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	briefCmd.AddCommand(&cobra.Command{
		Use:  "list TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view, err := versions.BriefList(st, args[0])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	briefCmd.AddCommand(&cobra.Command{
		Use:  "request TASK REVISION",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			if err := guard.Candidate(o.installRoot, st, "brief-request"); err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			if err := app.RequireCoordinator(st, ctx); err != nil {
				return err
			}
			view, err := versions.Request(st, args[0], args[1])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	briefCmd.AddCommand(&cobra.Command{
		Use:  "adopt TASK REVISION",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			if err := guard.Candidate(o.installRoot, st, "brief-adopt"); err != nil {
				return err
			}
			view, err := versions.Adopt(st, args[0], args[1])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	briefCmd.AddCommand(&cobra.Command{
		Use:  "regenerate TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			if err := guard.Candidate(o.installRoot, st, "brief-regenerate"); err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			if err := app.RequireCoordinator(st, ctx); err != nil {
				return err
			}
			view, err := brief.Regenerate(st, o.runtimeRoot, o.sumctlPath(), args[0])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	root.AddCommand(briefCmd)
}
