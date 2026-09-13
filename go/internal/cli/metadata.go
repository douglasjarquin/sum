package cli

import (
	"fmt"
	"io"

	"github.com/douglasjarquin/sum/go/internal/metadata"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addMetadataCommands(root *cobra.Command) {
	metadataCmd := &cobra.Command{
		Use:                "metadata",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	var raw bool
	snippetCmd := &cobra.Command{
		Use:  "snippet",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view := metadata.Snippet(o.sumctlPath(), st.Home)
			if raw {
				tomlValue, _ := view.Get("toml")
				toml, _ := tomlValue.(string)
				_, writeErr := io.WriteString(cmd.OutOrStdout(), toml)
				return writeErr
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	snippetCmd.Flags().BoolVar(&raw, "raw", false, "Print only the snippet text")
	metadataCmd.AddCommand(snippetCmd)

	metadataCmd.AddCommand(&cobra.Command{
		Use:  "status",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), metadata.Summary(st))
		},
	})

	var notify bool
	enableCmd := &cobra.Command{
		Use:  "enable",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("metadata-enable")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := metadata.Enable(st, ctx, o.runtimeRoot, notify)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	enableCmd.Flags().BoolVar(&notify, "notify", false, "")
	metadataCmd.AddCommand(enableCmd)

	metadataCmd.AddCommand(&cobra.Command{
		Use:  "disable",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("metadata-disable")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := metadata.Disable(st, ctx)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	metadataCmd.AddCommand(&cobra.Command{
		Use:  "sync",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("metadata-sync")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := metadata.Sync(st, ctx, o.runtimeRoot)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	metadataCmd.AddCommand(&cobra.Command{
		Use:  "inbox",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("metadata-inbox")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := metadata.Sync(st, ctx, o.runtimeRoot)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	root.AddCommand(metadataCmd)
}
