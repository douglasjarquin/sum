package cli

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pipeline"
	"github.com/douglasjarquin/sum/go/internal/pipelinerun"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addPipelineCommands(root *cobra.Command) {
	pipelineCmd := &cobra.Command{
		Use:                "pipeline",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	showCmd := &cobra.Command{
		Use:  "show TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("pipeline-show")
			if err != nil {
				return err
			}
			view, err := pipeline.Show(st, args[0])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	pipelineCmd.AddCommand(showCmd)

	refreshCmd := &cobra.Command{
		Use:  "refresh TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, ctx, err := o.coordinatorContext("pipeline-refresh")
			if err != nil {
				return err
			}
			view, err := pipeline.RefreshCommand(st, ctx, args[0])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	pipelineCmd.AddCommand(refreshCmd)

	var rebaseCandidate string
	rebaseCmd := &cobra.Command{
		Use:  "rebase TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, ctx, err := o.coordinatorContext("pipeline-rebase")
			if err != nil {
				return err
			}
			view, err := pipeline.Rebase(st, ctx, pipeline.RebaseArgs{Task: args[0], Candidate: rebaseCandidate})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	rebaseCmd.Flags().StringVar(&rebaseCandidate, "candidate", "", "")
	pipelineCmd.AddCommand(rebaseCmd)

	var lintCandidate string
	lintCmd := &cobra.Command{
		Use:  "lint TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, ctx, err := o.coordinatorContext("pipeline-lint")
			if err != nil {
				return err
			}
			view, err := pipeline.Lint(st, ctx, o.runtimeRoot, pipeline.LintArgs{Task: args[0], Candidate: lintCandidate})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	lintCmd.Flags().StringVar(&lintCandidate, "candidate", "", "")
	pipelineCmd.AddCommand(lintCmd)

	var allowBehind bool
	pushCmd := &cobra.Command{
		Use:  "push TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, ctx, err := o.coordinatorContext("pipeline-push")
			if err != nil {
				return err
			}
			view, err := pipeline.Push(st, ctx, pipeline.PushArgs{Task: args[0], AllowBehind: allowBehind})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	pushCmd.Flags().BoolVar(&allowBehind, "allow-behind", false, "")
	pipelineCmd.AddCommand(pushCmd)

	var rerun, runAllowBehind bool
	runCmd := &cobra.Command{
		Use:  "run TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, ctx, err := o.coordinatorContext("pipeline-run")
			if err != nil {
				return err
			}
			view, err := pipelinerun.Run(st, ctx, pipelinerun.Args{
				Task: args[0], Rerun: rerun, AllowBehind: runAllowBehind, RuntimeRoot: o.runtimeRoot,
			})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	runCmd.Flags().BoolVar(&rerun, "rerun", false, "")
	runCmd.Flags().BoolVar(&runAllowBehind, "allow-behind", false, "")
	pipelineCmd.AddCommand(runCmd)

	var candidate string
	documentCmd := &cobra.Command{
		Use:  "document TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, ctx, err := o.coordinatorContext("pipeline-document")
			if err != nil {
				return err
			}
			view, err := pipeline.Document(st, ctx, o.runtimeRoot, pipeline.DocumentArgs{Task: args[0], Candidate: candidate})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	documentCmd.Flags().StringVar(&candidate, "candidate", "", "")
	pipelineCmd.AddCommand(documentCmd)

	var noPublish bool
	var ciTimeout int
	ciCmd := &cobra.Command{
		Use:  "ci TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, ctx, err := o.coordinatorContext("pipeline-ci")
			if err != nil {
				return err
			}
			view, err := pipeline.CI(st, ctx, o.runtimeRoot, pipeline.CIArgs{Task: args[0], NoPublish: noPublish, Timeout: ciTimeout})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	ciCmd.Flags().BoolVar(&noPublish, "no-publish", false, "")
	ciCmd.Flags().IntVar(&ciTimeout, "timeout", 0, "")
	pipelineCmd.AddCommand(ciCmd)

	var dryRun, replaceForeignBlock bool
	var timeout int
	publishCmd := &cobra.Command{
		Use:  "publish TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, ctx, err := o.coordinatorContext("pipeline-publish")
			if err != nil {
				return err
			}
			view, err := pipeline.PublishCommand(st, ctx, o.runtimeRoot, pipeline.PublishArgs{
				Task:                args[0],
				DryRun:              dryRun,
				ReplaceForeignBlock: replaceForeignBlock,
				Timeout:             timeout,
			})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	publishCmd.Flags().BoolVar(&dryRun, "dry-run", false, "")
	publishCmd.Flags().BoolVar(&replaceForeignBlock, "replace-foreign-block", false, "")
	publishCmd.Flags().IntVar(&timeout, "timeout", 0, "")
	pipelineCmd.AddCommand(publishCmd)

	root.AddCommand(pipelineCmd)
}

func (o *rootOptions) coordinatorContext(label string) (*store.Store, *ordjson.Object, error) {
	st, err := o.openStore(label)
	if err != nil {
		return nil, nil, err
	}
	ctx, err := store.Context(o.installRoot)
	if err != nil {
		return nil, nil, err
	}
	return st, ctx, nil
}
