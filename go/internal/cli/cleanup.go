package cli

import (
	"github.com/douglasjarquin/sum/go/internal/cleanup"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addCleanupCommand(root *cobra.Command) {
	var apply, reviewerOnly bool
	var number int
	cmd := &cobra.Command{
		Use:  "cleanup TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("cleanup")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := cleanup.Run(st, ctx, o.runtimeRoot, cleanup.Args{
				Task:         args[0],
				Apply:        apply,
				ReviewerOnly: reviewerOnly,
				Number:       number,
			})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "")
	cmd.Flags().BoolVar(&reviewerOnly, "reviewer-only", false, "")
	cmd.Flags().IntVar(&number, "number", 0, "")
	root.AddCommand(cmd)
}
