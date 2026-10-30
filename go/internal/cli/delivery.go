package cli

import (
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addDeliveryCommands(root *cobra.Command) {
	delivery := &cobra.Command{Use: "delivery", Args: cobra.NoArgs}
	var id string
	reconcile := &cobra.Command{Use: "reconcile TASK", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		st, ctx, err := o.coordinatorContext("delivery-reconcile")
		if err != nil {
			return err
		}
		view, err := returns.ReconcileDelivery(st, ctx, args[0], id)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	}}
	reconcile.Flags().StringVar(&id, "delivery", "", "exact recorded delivery ID")
	_ = reconcile.MarkFlagRequired("delivery")
	delivery.AddCommand(reconcile)
	root.AddCommand(delivery)
}
