package cli

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

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
		projectLimits, clearProjectLimits                         []string
		clearCapacity                                             bool
		workerHarness, workerModel, workerReasoning, workerPreset string
		clearWorker                                               bool
		reviewerPreset                                            string
		clearReviewer                                             bool
		autoPublishEvidence                                       string
		clearEvidence                                             bool
		usageAccounting                                           string
		clearUsage                                                bool
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
				ClearEvidence:     clearEvidence,
				ClearUsage:        clearUsage,
			}
			if cmd.Flags().Changed("usage-accounting") {
				enabled, boolErr := strconv.ParseBool(usageAccounting)
				if boolErr != nil {
					return fmt.Errorf("--usage-accounting takes true or false, got %q", usageAccounting)
				}
				parsed.UsageEnabled = &enabled
			}
			if cmd.Flags().Changed("auto-publish-evidence") {
				enabled, boolErr := strconv.ParseBool(autoPublishEvidence)
				if boolErr != nil {
					return fmt.Errorf("--auto-publish-evidence takes true or false, got %q", autoPublishEvidence)
				}
				parsed.AutoPublish = &enabled
			}
			if cmd.Flags().Changed("global") {
				parsed.Global = &global
			}
			if cmd.Flags().Changed("per-repository") {
				parsed.PerRepository = &perRepository
			}
			if len(projectLimits) > 0 {
				parsed.ProjectLimits = map[string]int{}
				for _, spec := range projectLimits {
					name, value, ok := strings.Cut(spec, "=")
					n, numErr := strconv.Atoi(value)
					if !ok || name == "" || numErr != nil {
						return fmt.Errorf("--project-limit takes NAME=N with an enrolled project name and an integer, got %q", spec)
					}
					parsed.ProjectLimits[name] = n
				}
			}
			parsed.ClearProjectLimits = clearProjectLimits
			return o.runSettingsSet(cmd, parsed)
		},
	}
	setCmd.Flags().IntVar(&global, "global", 0, "")
	setCmd.Flags().IntVar(&perRepository, "per-repository", 0, "")
	setCmd.Flags().StringArrayVar(&projectLimits, "project-limit", nil, "")
	setCmd.Flags().StringArrayVar(&clearProjectLimits, "clear-project-limit", nil, "")
	setCmd.Flags().BoolVar(&clearCapacity, "clear-capacity", false, "")
	setCmd.Flags().StringVar(&workerHarness, "worker-harness", "", "")
	setCmd.Flags().StringVar(&workerModel, "worker-model", "", "")
	setCmd.Flags().StringVar(&workerReasoning, "worker-reasoning", "", "")
	setCmd.Flags().StringVar(&workerPreset, "worker-preset", "", "")
	setCmd.Flags().BoolVar(&clearWorker, "clear-worker", false, "")
	setCmd.Flags().StringVar(&reviewerPreset, "reviewer-preset", "", "")
	setCmd.Flags().BoolVar(&clearReviewer, "clear-reviewer", false, "")
	setCmd.Flags().StringVar(&autoPublishEvidence, "auto-publish-evidence", "", "")
	setCmd.Flags().BoolVar(&clearEvidence, "clear-evidence", false, "")
	setCmd.Flags().StringVar(&usageAccounting, "usage-accounting", "", "")
	setCmd.Flags().BoolVar(&clearUsage, "clear-usage", false, "")
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
	if parsed.ClearCapacity && (parsed.Global != nil || parsed.PerRepository != nil || len(parsed.ProjectLimits) > 0 || len(parsed.ClearProjectLimits) > 0) {
		return fmt.Errorf("--clear-capacity conflicts with --global/--per-repository/--project-limit/--clear-project-limit.")
	}
	for name := range parsed.ProjectLimits {
		if slices.Contains(parsed.ClearProjectLimits, name) {
			return fmt.Errorf("--project-limit and --clear-project-limit both name %q; give one.", name)
		}
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
	if parsed.ClearEvidence && parsed.AutoPublish != nil {
		return fmt.Errorf("--clear-evidence conflicts with --auto-publish-evidence.")
	}
	if parsed.ClearUsage && parsed.UsageEnabled != nil {
		return fmt.Errorf("--clear-usage conflicts with --usage-accounting.")
	}
	if parsed.Global == nil && parsed.PerRepository == nil && len(parsed.ProjectLimits) == 0 && len(parsed.ClearProjectLimits) == 0 && !parsed.ClearCapacity && parsed.WorkerHarness == "" && parsed.WorkerModel == "" && parsed.WorkerReasoning == "" && !parsed.WorkerPresetSet && !parsed.ClearWorker && !parsed.ReviewerPresetSet && !parsed.ClearReviewer && parsed.AutoPublish == nil && !parsed.ClearEvidence && parsed.UsageEnabled == nil && !parsed.ClearUsage {
		return fmt.Errorf("Give --global, --per-repository, --project-limit, --clear-project-limit, --clear-capacity, --worker-harness/--worker-model/--worker-reasoning, --worker-preset, --clear-worker, --reviewer-preset, --clear-reviewer, --auto-publish-evidence, --clear-evidence, --usage-accounting, or --clear-usage.")
	}
	view, err := settings.Write(st, parsed)
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}
