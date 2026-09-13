package cli

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/updatecmd"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addUpdateCommands(root *cobra.Command) {
	updateCmd := &cobra.Command{
		Use:                "update",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	updateCmd.AddCommand(&cobra.Command{
		Use:  "status",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("update-status")
			if err != nil {
				return err
			}
			view, err := updatecmd.Status(st)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	addRef := func(name string) {
		var ref string
		var noFetch bool
		cmd := &cobra.Command{
			Use:  name,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				st, err := o.openStore("update-" + name)
				if err != nil {
					return err
				}
				var view *ordjson.Object
				switch name {
				case "check":
					view, err = updatecmd.Check(st, o.runtimeRoot, ref, noFetch)
				case "stage":
					view, err = updatecmd.Stage(st, app.OptionalContext(o.installRoot), ref, noFetch)
				case "apply":
					view, err = updatecmd.Apply(st, app.OptionalContext(o.installRoot), ref, noFetch)
				}
				if err != nil {
					return err
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			},
		}
		cmd.Flags().StringVar(&ref, "ref", "", "")
		cmd.Flags().BoolVar(&noFetch, "no-fetch", false, "")
		updateCmd.AddCommand(cmd)
	}
	addRef("check")
	addRef("stage")
	addRef("apply")

	var to string
	rollbackCmd := &cobra.Command{
		Use:  "rollback",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("update-rollback")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := updatecmd.Rollback(st, ctx, to)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	rollbackCmd.Flags().StringVar(&to, "to", "", "")
	updateCmd.AddCommand(rollbackCmd)

	var generation string
	recoverCmd := &cobra.Command{
		Use:  "recover",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if generation == "" {
				return usageError("update recover", args)
			}
			st, err := o.openStore("update-recover")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := updatecmd.Recover(st, ctx, generation)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	recoverCmd.Flags().StringVar(&generation, "generation", "", "")
	updateCmd.AddCommand(recoverCmd)

	root.AddCommand(updateCmd)
}
