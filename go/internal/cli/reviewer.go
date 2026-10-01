package cli

import (
	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/review"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addReviewerCommands(root *cobra.Command) {
	cmd := &cobra.Command{Use: "reviewer", Short: "Launch an independent reviewer"}
	launchCmd := &cobra.Command{
		Use:         "launch TASK",
		Short:       "Launch the task reviewer in its own labeled tab",
		Annotations: map[string]string{projectsAnnotation: "task"},
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("reviewer launch")
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
			view, err := review.Launch(st, ctx, o.runtimeRoot, o.sumctlPath(), args[0])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	cmd.AddCommand(launchCmd)
	root.AddCommand(cmd)
}
