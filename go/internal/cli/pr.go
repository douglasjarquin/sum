package cli

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/prcmd"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addPRCommands(root *cobra.Command) {
	prCmd := &cobra.Command{
		Use:                "pr",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	var number int
	var repo string
	var replace bool
	reconcileCmd := &cobra.Command{
		Use:  "reconcile TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if number == 0 {
				return usageError("pr reconcile", args)
			}
			st, err := o.openStore("pr-reconcile")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := prcmd.Reconcile(st, ctx, o.runtimeRoot, prcmd.ReconcileArgs{
				Task:    args[0],
				Number:  number,
				Repo:    repo,
				Replace: replace,
			})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	reconcileCmd.Flags().IntVar(&number, "number", 0, "")
	reconcileCmd.Flags().StringVar(&repo, "repo", "", "")
	reconcileCmd.Flags().BoolVar(&replace, "replace", false, "")
	_ = reconcileCmd.MarkFlagRequired("number")
	prCmd.AddCommand(reconcileCmd)

	var run, visibility, evidenceRoot string
	var scenarios, verificationRuns []string
	var timeout int
	var dryRun, allowHeadMismatch, replaceForeignBlock bool
	evidenceCmd := &cobra.Command{
		Use:  "evidence TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("pr-evidence")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := prcmd.Evidence(st, ctx, o.runtimeRoot, prcmd.PublishArgs{
				Task:                args[0],
				Run:                 run,
				Visibility:          visibility,
				Scenarios:           scenarios,
				EvidenceRoot:        evidenceRoot,
				VerificationRuns:    verificationRuns,
				Timeout:             timeout,
				DryRun:              dryRun,
				AllowHeadMismatch:   allowHeadMismatch,
				ReplaceForeignBlock: replaceForeignBlock,
			})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	evidenceCmd.Flags().StringVar(&run, "run", "", "")
	evidenceCmd.Flags().StringVar(&visibility, "visibility", "", "")
	evidenceCmd.Flags().StringArrayVar(&scenarios, "scenario", nil, "")
	evidenceCmd.Flags().StringVar(&evidenceRoot, "evidence-root", "", "")
	evidenceCmd.Flags().StringArrayVar(&verificationRuns, "verification-run", nil, "")
	evidenceCmd.Flags().IntVar(&timeout, "timeout", 0, "")
	evidenceCmd.Flags().BoolVar(&dryRun, "dry-run", false, "")
	evidenceCmd.Flags().BoolVar(&allowHeadMismatch, "allow-head-mismatch", false, "")
	evidenceCmd.Flags().BoolVar(&replaceForeignBlock, "replace-foreign-block", false, "")
	prCmd.AddCommand(evidenceCmd)

	root.AddCommand(prCmd)
}
