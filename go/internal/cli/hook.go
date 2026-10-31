package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/hookstatus"
	"github.com/douglasjarquin/sum/go/internal/settings"
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
		Use:         "enable",
		Annotations: map[string]string{projectsAnnotation: "all"},
		Args:        cobra.NoArgs,
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
			if err := settings.SetNativeEventsEnabled(st, true); err != nil {
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
			if err := app.RequireCoordinator(st, ctx); err != nil {
				return err
			}
			if err := settings.SetNativeEventsEnabled(st, false); err != nil {
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

	var coreID, session, socket string
	observeCmd := &cobra.Command{Use: "observe", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		st, err := store.Open(o.home)
		if err != nil {
			return err
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		endpoint := store.Endpoint{CoreID: coreID, Session: session, Socket: socket}
		return hookstatus.Observe(ctx, st, endpoint, o.runtimeRoot, o.sumctlPath())
	}}
	observeCmd.Flags().StringVar(&coreID, "core-id", "", "")
	observeCmd.Flags().StringVar(&session, "session", "", "")
	observeCmd.Flags().StringVar(&socket, "socket", "", "")
	hookCmd.AddCommand(observeCmd)

	root.AddCommand(hookCmd)
}
