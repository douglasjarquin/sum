package cli

import (
	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/review"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addReviewerCommands(root *cobra.Command) {
	cmd := &cobra.Command{Use: "reviewer", Short: "Launch an independent reviewer"}
	var harness, model, reasoning, preset string
	var permissionOverride bool
	launchCmd := &cobra.Command{
		Use:         "launch TASK",
		Short:       "Launch the independent task reviewer in HC",
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
			view, err := review.Launch(st, ctx, o.runtimeRoot, o.sumctlPath(), review.LaunchArgs{
				Task:      args[0],
				Harness:   harness,
				Model:     model,
				Reasoning: reasoning,
				Preset:    preset,
				PresetSet: cmd.Flags().Changed("preset"),

				PermissionOverride: permissionOverride,
			})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	launchCmd.Flags().StringVar(&harness, "harness", "", "")
	launchCmd.Flags().StringVar(&model, "model", "", "")
	launchCmd.Flags().StringVar(&reasoning, "reasoning", "", "")
	launchCmd.Flags().StringVar(&preset, "preset", "", "")
	launchCmd.Flags().BoolVar(&permissionOverride, "permission-override", false, "")
	cmd.AddCommand(launchCmd)
	var attempt string
	redeliver := &cobra.Command{Use: "redeliver TASK", Short: "Recover a proven unsent reviewer opening message", Annotations: map[string]string{projectsAnnotation: "task"}, Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		st, err := o.openStore("reviewer redeliver")
		if err != nil {
			return err
		}
		ctx, err := store.Context(o.installRoot)
		if err != nil {
			return err
		}
		if err = app.RequireCoordinator(st, ctx); err != nil {
			return err
		}
		view, err := review.Redeliver(st, ctx, args[0], attempt)
		if err != nil {
			return err
		}
		return emitOrdjson(command.OutOrStdout(), view)
	}}
	redeliver.Flags().StringVar(&attempt, "attempt", "", "")
	_ = redeliver.MarkFlagRequired("attempt")
	cmd.AddCommand(redeliver)
	root.AddCommand(cmd)
}
