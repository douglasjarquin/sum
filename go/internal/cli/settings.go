package cli

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addSettingsCommands(root *cobra.Command) {
	settingsCmd := &cobra.Command{
		Use:                "settings",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	settingsCmd.AddCommand(&cobra.Command{
		Use:  "show",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view, err := settings.CapacityView(st)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	var (
		global, perRepository                                     int
		clearCapacity                                             bool
		workerHarness, workerModel, workerReasoning, workerPreset string
		clearWorker                                               bool
		reviewerPreset                                            string
		clearReviewer                                             bool
	)
	setCmd := &cobra.Command{
		Use:  "set",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			parsed := settings.WriteArgs{
				ClearCapacity:     clearCapacity,
				WorkerHarness:     workerHarness,
				WorkerModel:       workerModel,
				WorkerReasoning:   workerReasoning,
				WorkerPreset:      workerPreset,
				ClearWorker:       clearWorker,
				ReviewerPreset:    reviewerPreset,
				ClearReviewer:     clearReviewer,
				WorkerPresetSet:   cmd.Flags().Changed("worker-preset"),
				ReviewerPresetSet: cmd.Flags().Changed("reviewer-preset"),
			}
			if cmd.Flags().Changed("global") {
				parsed.Global = &global
			}
			if cmd.Flags().Changed("per-repository") {
				parsed.PerRepository = &perRepository
			}
			return o.runSettingsSet(cmd, parsed)
		},
	}
	setCmd.Flags().IntVar(&global, "global", 0, "")
	setCmd.Flags().IntVar(&perRepository, "per-repository", 0, "")
	setCmd.Flags().BoolVar(&clearCapacity, "clear-capacity", false, "")
	setCmd.Flags().StringVar(&workerHarness, "worker-harness", "", "")
	setCmd.Flags().StringVar(&workerModel, "worker-model", "", "")
	setCmd.Flags().StringVar(&workerReasoning, "worker-reasoning", "", "")
	setCmd.Flags().StringVar(&workerPreset, "worker-preset", "", "")
	setCmd.Flags().BoolVar(&clearWorker, "clear-worker", false, "")
	setCmd.Flags().StringVar(&reviewerPreset, "reviewer-preset", "", "")
	setCmd.Flags().BoolVar(&clearReviewer, "clear-reviewer", false, "")
	settingsCmd.AddCommand(setCmd)

	root.AddCommand(settingsCmd)
}

func (o *rootOptions) runSettingsSet(cmd *cobra.Command, parsed settings.WriteArgs) error {
	st, err := o.openStore("settings-set")
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
	if parsed.ClearCapacity && (parsed.Global != nil || parsed.PerRepository != nil) {
		return fmt.Errorf("--clear-capacity conflicts with --global/--per-repository.")
	}
	if parsed.ClearWorker && (parsed.WorkerHarness != "" || parsed.WorkerModel != "" || parsed.WorkerReasoning != "" || parsed.WorkerPresetSet) {
		return fmt.Errorf("--clear-worker conflicts with --worker-* values.")
	}
	if parsed.WorkerPresetSet && (parsed.WorkerHarness != "" || parsed.WorkerModel != "" || parsed.WorkerReasoning != "") {
		return fmt.Errorf("--worker-preset conflicts with --worker-harness/--worker-model/--worker-reasoning: a default is either a preset reference or a plain specification.")
	}
	if parsed.ClearReviewer && parsed.ReviewerPresetSet {
		return fmt.Errorf("--clear-reviewer conflicts with --reviewer-preset.")
	}
	if parsed.Global == nil && parsed.PerRepository == nil && !parsed.ClearCapacity && parsed.WorkerHarness == "" && parsed.WorkerModel == "" && parsed.WorkerReasoning == "" && !parsed.WorkerPresetSet && !parsed.ClearWorker && !parsed.ReviewerPresetSet && !parsed.ClearReviewer {
		return fmt.Errorf("Give --global, --per-repository, --clear-capacity, --worker-harness/--worker-model/--worker-reasoning, --worker-preset, --clear-worker, --reviewer-preset, or --clear-reviewer.")
	}
	view, err := settings.Write(st, parsed)
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}
