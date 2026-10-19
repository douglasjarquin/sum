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
	var replace, acceptBase bool
	reconcileCmd := &cobra.Command{
		Use:         "reconcile TASK",
		Annotations: map[string]string{projectsAnnotation: "task"},
		Args:        cobra.ExactArgs(1),
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
				Task:             args[0],
				Number:           number,
				Repo:             repo,
				Replace:          replace,
				AcceptBaseBranch: acceptBase,
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
	reconcileCmd.Flags().BoolVar(&acceptBase, "accept-base-branch", false, "")
	_ = reconcileCmd.MarkFlagRequired("number")
	prCmd.AddCommand(reconcileCmd)

	threadCmd := &cobra.Command{
		Use:                "thread",
		Short:              "Inspect saved GitHub review threads and choose local follow-up",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("thread action is required")
		},
	}
	var threadAction, threadHead, threadPRURL, threadText, threadAttempt, threadClass, threadReason string
	var threadIDs, threadRevisions []string
	var threadAll bool
	threadActionCmd := &cobra.Command{
		Use:                "action TASK",
		Short:              "Revalidate and record a GitHub review-thread follow-up",
		Annotations:        map[string]string{projectsAnnotation: "task"},
		DisableSuggestions: true,
		Args:               cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(threadIDs) != len(threadRevisions) {
				return fmt.Errorf("each --thread requires one matching --revision")
			}
			selected := make([]prcmd.ThreadRevision, 0, len(threadIDs))
			for i := range threadIDs {
				selected = append(selected, prcmd.ThreadRevision{ID: threadIDs[i], Revision: threadRevisions[i]})
			}
			st, err := o.openStore("pr-thread-action")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := prcmd.ThreadAction(st, ctx, prcmd.ThreadActionArgs{Task: args[0], Action: threadAction, Head: threadHead, PRURL: threadPRURL, Threads: selected, All: threadAll, Text: threadText, Attempt: threadAttempt, Class: threadClass, Reason: threadReason, RuntimeRoot: o.runtimeRoot})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	threadActionCmd.Flags().StringVar(&threadAction, "action", "", "address, discuss, draft-reply, or open-comment")
	threadActionCmd.Flags().StringVar(&threadHead, "head", "", "observed PR head SHA shown on the saved row")
	threadActionCmd.Flags().StringVar(&threadPRURL, "pr-url", "", "recorded GitHub PR URL shown on the saved row")
	threadActionCmd.Flags().StringArrayVar(&threadIDs, "thread", nil, "exact GitHub thread ID; repeat with matching --revision to address selected threads")
	threadActionCmd.Flags().StringArrayVar(&threadRevisions, "revision", nil, "exact latest GitHub comment ID paired with each --thread")
	threadActionCmd.Flags().BoolVar(&threadAll, "all", false, "address every currently actionable thread from a complete saved observation")
	threadActionCmd.Flags().StringVar(&threadText, "text", "", "corrective instruction, discussion note, or draft reply text")
	threadActionCmd.Flags().StringVar(&threadAttempt, "attempt", "", "exact running worker attempt required for Address")
	threadActionCmd.Flags().StringVar(&threadClass, "class", "", "repair scope: in-scope or expansion")
	threadActionCmd.Flags().StringVar(&threadReason, "reason", "", "required explanation for expansion work")
	_ = threadActionCmd.MarkFlagRequired("action")
	_ = threadActionCmd.MarkFlagRequired("head")
	_ = threadActionCmd.MarkFlagRequired("pr-url")
	threadCmd.AddCommand(threadActionCmd)
	prCmd.AddCommand(threadCmd)

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
