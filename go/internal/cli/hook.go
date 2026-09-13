package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/hookstatus"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addHookCommands(root *cobra.Command) {
	hookCmd := &cobra.Command{
		Use:                "hook",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	hookCmd.AddCommand(&cobra.Command{
		Use:  "status",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			ctx, ctxErr := store.Context(o.runtimeRoot)
			if ctxErr != nil {
				ctx = nil
			}
			view, err := hookstatus.Status(st, ctx, o.runtimeRoot, o.sumctlPath())
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	hookCmd.AddCommand(&cobra.Command{
		Use:  "enable",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("hook-enable")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := hookstatus.Enable(st, ctx, o.runtimeRoot, o.sumctlPath())
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	var unlink bool
	disableCmd := &cobra.Command{
		Use:  "disable",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("hook-disable")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := hookstatus.Disable(st, ctx, o.runtimeRoot, unlink)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	disableCmd.Flags().BoolVar(&unlink, "unlink", false, "")
	hookCmd.AddCommand(disableCmd)

	hookCmd.AddCommand(&cobra.Command{
		Use:  "event",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			environ := map[string]string{}
			for _, e := range os.Environ() {
				if i := strings.IndexByte(e, '='); i > 0 {
					environ[e[:i]] = e[i+1:]
				}
			}
			view, err := hookstatus.Event(st, environ, o.runtimeRoot, o.sumctlPath())
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	root.AddCommand(hookCmd)
}
