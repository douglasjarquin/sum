package cli

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/guard"
	"github.com/douglasjarquin/sum/go/internal/project"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addProjectCommands(root *cobra.Command) {
	projectCmd := &cobra.Command{
		Use:                "project",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	projectCmd.AddCommand(&cobra.Command{
		Use:  "list",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view, err := project.List(st, o.runtimeRoot)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	projectCmd.AddCommand(&cobra.Command{
		Use:  "show NAME",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view, err := project.Show(st, args[0])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	var host, remote, path string
	enrollCmd := &cobra.Command{
		Use:  "enroll SPEC",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			if err := guard.Candidate(o.installRoot, st, "project-enroll"); err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := project.Enroll(st, ctx, o.runtimeRoot, project.EnrollArgs{
				Spec:   args[0],
				Host:   host,
				Remote: remote,
				Path:   path,
			})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	enrollCmd.Flags().StringVar(&host, "host", "", "")
	enrollCmd.Flags().StringVar(&remote, "remote", "", "")
	enrollCmd.Flags().StringVar(&path, "path", "", "")
	projectCmd.AddCommand(enrollCmd)

	var apply bool
	migrateCmd := &cobra.Command{
		Use:  "migrate NAME",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			ctx := app.OptionalContext(o.installRoot)
			view, err := project.Migrate(st, ctx, o.runtimeRoot, args[0], apply)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	migrateCmd.Flags().BoolVar(&apply, "apply", false, "")
	projectCmd.AddCommand(migrateCmd)

	root.AddCommand(projectCmd)
}
