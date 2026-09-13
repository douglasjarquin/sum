package cli

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/execution"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addExecutionCommands(root *cobra.Command) {
	executionCmd := &cobra.Command{
		Use:                "execution",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	executionCmd.AddCommand(&cobra.Command{
		Use:  "show TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("execution-show")
			if err != nil {
				return err
			}
			view, err := execution.Show(st, args[0])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	addAttempt := func(name string, run func(*store.Store, *ordjson.Object, string, string, string) (*ordjson.Object, error)) {
		var attempt string
		cmd := &cobra.Command{
			Use:  name + " TASK",
			Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				st, err := o.openStore("execution-" + name)
				if err != nil {
					return err
				}
				ctx, err := store.Context(o.installRoot)
				if err != nil {
					return err
				}
				view, err := run(st, ctx, o.runtimeRoot, args[0], attempt)
				if err != nil {
					return err
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			},
		}
		cmd.Flags().StringVar(&attempt, "attempt", "", "")
		_ = cmd.MarkFlagRequired("attempt")
		executionCmd.AddCommand(cmd)
	}
	addAttempt("park", execution.Park)
	addAttempt("resume", execution.Resume)

	root.AddCommand(executionCmd)
}
