package cli

import (
	"encoding/json"
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/factory"
	"github.com/douglasjarquin/sum/go/internal/factoryview"
	"github.com/douglasjarquin/sum/go/internal/inboxview"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addFactoryCommands(root *cobra.Command) {
	factoryCmd := &cobra.Command{
		Use:                "factory",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	var statusProject, statusSince string
	var statusLimit int
	statusCmd := &cobra.Command{
		Use:  "status",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if statusLimit < 1 || statusLimit > factoryview.MaxLimit {
				return fmt.Errorf("--limit accepts 1..%d", factoryview.MaxLimit)
			}
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view, err := factory.Status(st, statusProject)
			if err != nil {
				return err
			}
			installation, err := st.Instance()
			if err != nil {
				return err
			}
			// The digest is a projection of the same saved records; the lane summary above is unchanged.
			digest := factoryview.Build(inboxview.Read(st), installation, factoryview.Options{Project: statusProject, Since: statusSince, Limit: statusLimit, FactoryOnly: true})
			raw, err := json.Marshal(digest)
			if err != nil {
				return err
			}
			decoded, err := ordjson.Decode(raw)
			if err != nil {
				return err
			}
			view.Set("digest", decoded)
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	statusCmd.Flags().StringVar(&statusProject, "project", "", "")
	statusCmd.Flags().StringVar(&statusSince, "since", "", "Digest cursor from an earlier factory status read; labels new outcomes or resyncs")
	statusCmd.Flags().IntVar(&statusLimit, "limit", factoryview.DefaultLimit, "Maximum digest outcomes per response (1..100)")
	factoryCmd.AddCommand(statusCmd)

	var (
		lanes         int
		ready         string
		label         string
		roadmapIssue  int
		projectNumber int
		readyOption   string
		idleSeconds   int
		strictCleanup bool
		skip          []int
	)
	enableCmd := &cobra.Command{
		Use:  "enable PROJECT",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("factory-enable")
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
			view, err := factory.Enable(st, ctx, factory.EnableArgs{
				Project:       args[0],
				Lanes:         lanes,
				Ready:         ready,
				Label:         label,
				RoadmapIssue:  roadmapIssue,
				ProjectNumber: projectNumber,
				ReadyOption:   readyOption,
				IdleSeconds:   idleSeconds,
				StrictCleanup: strictCleanup,
				Skip:          skip,
			})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	enableCmd.Flags().IntVar(&lanes, "lanes", factory.DefaultLanes, "")
	enableCmd.Flags().StringVar(&ready, "ready", factory.ReadyLabel, "")
	enableCmd.Flags().StringVar(&label, "label", "ready", "")
	enableCmd.Flags().IntVar(&roadmapIssue, "roadmap-issue", 0, "")
	enableCmd.Flags().IntVar(&projectNumber, "project-number", 0, "")
	enableCmd.Flags().StringVar(&readyOption, "ready-option", "Ready", "")
	enableCmd.Flags().IntVar(&idleSeconds, "idle-seconds", factory.DefaultIdleSeconds, "")
	enableCmd.Flags().BoolVar(&strictCleanup, "strict-cleanup", false, "")
	enableCmd.Flags().IntSliceVar(&skip, "skip", nil, "")
	factoryCmd.AddCommand(enableCmd)

	factoryCmd.AddCommand(&cobra.Command{
		Use:  "disable PROJECT",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("factory-disable")
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
			view, err := factory.Disable(st, args[0])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	var tickProject string
	tickCmd := &cobra.Command{
		Use:  "tick",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("factory-tick")
			if err != nil {
				return err
			}
			view, err := factory.Tick(st, o.runtimeRoot, tickProject)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	tickCmd.Flags().StringVar(&tickProject, "project", "", "")
	factoryCmd.AddCommand(tickCmd)

	var claimIssue int
	var claimTask string
	claimCmd := &cobra.Command{
		Use:  "claim PROJECT",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("factory-claim")
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
			view, err := factory.Claim(st, ctx, o.runtimeRoot, factory.ClaimArgs{
				Project: args[0],
				Issue:   claimIssue,
				Task:    claimTask,
			})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	claimCmd.Flags().IntVar(&claimIssue, "issue", 0, "")
	claimCmd.Flags().StringVar(&claimTask, "task", "", "")
	factoryCmd.AddCommand(claimCmd)

	var releaseIssue int
	var releaseReason string
	var releaseContinue bool
	releaseCmd := &cobra.Command{
		Use:  "release PROJECT",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("factory-release")
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
			view, err := factory.Release(st, factory.ReleaseArgs{
				Project:  args[0],
				Issue:    releaseIssue,
				Reason:   releaseReason,
				Continue: releaseContinue,
			})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	releaseCmd.Flags().IntVar(&releaseIssue, "issue", 0, "")
	releaseCmd.Flags().StringVar(&releaseReason, "reason", "", "")
	releaseCmd.Flags().BoolVar(&releaseContinue, "continue", false, "")
	factoryCmd.AddCommand(releaseCmd)

	factoryCmd.AddCommand(&cobra.Command{
		Use:  "merge-check TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view, err := factory.MergeCheck(st, args[0])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	factoryCmd.AddCommand(&cobra.Command{
		Use:  "merge TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("factory-merge")
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
			view, err := factory.Merge(st, o.runtimeRoot, args[0])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	root.AddCommand(factoryCmd)
}
