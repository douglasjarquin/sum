package cli

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/guard"
	"github.com/douglasjarquin/sum/go/internal/release"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addReleaseCommands(root *cobra.Command) {
	releaseCmd := &cobra.Command{
		Use:                "release",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	releaseCmd.AddCommand(&cobra.Command{
		Use:  "list",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view, err := release.List(st)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	releaseCmd.AddCommand(&cobra.Command{
		Use:  "show SHA",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view, err := release.Show(st, args[0])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	var ref string
	stageCmd := &cobra.Command{
		Use: "stage [REF]",
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("ref") {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.MaximumNArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved := ref
			if !cmd.Flags().Changed("ref") && len(args) == 1 {
				resolved = args[0]
			}
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			if err := guard.Candidate(o.installRoot, st, "release-stage"); err != nil {
				return err
			}
			view, err := release.Stage(st, resolved)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	stageCmd.Flags().StringVar(&ref, "ref", "", "")
	releaseCmd.AddCommand(stageCmd)

	root.AddCommand(releaseCmd)
}
