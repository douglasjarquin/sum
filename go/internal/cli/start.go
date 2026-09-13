package cli

import (
	"github.com/douglasjarquin/sum/go/internal/prepare"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addStartCommand(root *cobra.Command) {
	var extra []string
	cmd := &cobra.Command{
		Use:  "start TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("start")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := prepare.Start(st, ctx, o.runtimeRoot, args[0], extra)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	cmd.Flags().StringArrayVar(&extra, "arg", nil, "")
	root.AddCommand(cmd)
}
