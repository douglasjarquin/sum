package cli

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

// The coordinator wake commands (#240a): `wake show` reads every wake sidecar and writes nothing; `wake consume`
// records the coordinator's exact consume receipt for one boundary; `wake reconcile` settles a prepared,
// interrupted, or replaced-occupant episode conservatively. Consume and reconcile are coordinator-only, judged from
// the caller's Herdr context exactly as `attention --seen` is.
func (o *rootOptions) addWakeCommands(root *cobra.Command) {
	wakeCmd := &cobra.Command{
		Use:                "wake",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	var showRecipient string
	showCmd := &cobra.Command{
		Use:  "show",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view, err := returns.Show(st, showRecipient)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	showCmd.Flags().StringVar(&showRecipient, "recipient", "", "")
	wakeCmd.AddCommand(showCmd)

	var boundary string
	consumeCmd := &cobra.Command{
		Use:         "consume",
		Annotations: map[string]string{projectsAnnotation: "all"},
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if boundary == "" {
				return usageError("wake consume", []string{"--boundary"})
			}
			st, ctx, err := o.coordinatorContext("wake-consume")
			if err != nil {
				return err
			}
			view, err := returns.Consume(st, ctx, boundary)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	consumeCmd.Flags().StringVar(&boundary, "boundary", "", "")
	_ = consumeCmd.MarkFlagRequired("boundary")
	wakeCmd.AddCommand(consumeCmd)

	var reconcileRecipient string
	reconcileCmd := &cobra.Command{
		Use:         "reconcile",
		Annotations: map[string]string{projectsAnnotation: "all"},
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, ctx, err := o.coordinatorContext("wake-reconcile")
			if err != nil {
				return err
			}
			view, err := returns.Reconcile(st, ctx, reconcileRecipient)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	reconcileCmd.Flags().StringVar(&reconcileRecipient, "recipient", "", "")
	wakeCmd.AddCommand(reconcileCmd)

	root.AddCommand(wakeCmd)
}
