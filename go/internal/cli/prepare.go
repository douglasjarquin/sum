package cli

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/prepare"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addPrepareCommands(root *cobra.Command) {
	add := func(name string, run func(*store.Store, *ordjson.Object, prepare.Args) (*ordjson.Object, error)) {
		var (
			repo, project, brief, harness, model, reasoning, preset, base, targetBranch, kind string
			reconcile                                                                         string
			sameAsYou, approved, permissionOverride                                           bool
			extra                                                                             []string
		)
		cmd := &cobra.Command{
			Use:         name,
			Annotations: map[string]string{projectsAnnotation: "all"},
			Args:        cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				if reconcile == "" && (brief == "" || (kind != "ship" && kind != "scout")) {
					return fmt.Errorf("invalid %s arguments", name)
				}
				st, err := o.openStore(name)
				if err != nil {
					return err
				}
				ctx, err := store.Context(o.installRoot)
				if err != nil {
					return err
				}
				if reconcile != "" {
					if name != "prepare" {
						return fmt.Errorf("only prepare supports --reconcile")
					}
					for _, flag := range []string{"repo", "project", "brief", "harness", "model", "reasoning", "same-as-you", "preset", "base", "target-branch", "kind", "approved", "arg", "permission-override"} {
						if cmd.Flags().Changed(flag) {
							return fmt.Errorf("--reconcile uses only the saved approved task; it conflicts with --%s", flag)
						}
					}
					view, err := prepare.Reconcile(st, ctx, o.runtimeRoot, o.sumctlPath(), reconcile)
					if err != nil {
						return err
					}
					return emitOrdjson(cmd.OutOrStdout(), view)
				}
				view, err := run(st, ctx, prepare.Args{
					Repo:         repo,
					Project:      project,
					Brief:        brief,
					Harness:      harness,
					Model:        model,
					Reasoning:    reasoning,
					SameAsYou:    sameAsYou,
					Preset:       preset,
					PresetSet:    cmd.Flags().Changed("preset"),
					Base:         base,
					TargetBranch: targetBranch,
					Kind:         kind,
					Approved:     approved,
					Extra:        extra,
					RuntimeRoot:  o.runtimeRoot,
					SumctlPath:   o.sumctlPath(),

					PermissionOverride: permissionOverride,
				})
				if err != nil {
					return err
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			},
		}
		if name == "prepare" {
			cmd.Flags().StringVar(&reconcile, "reconcile", "", "")
		}
		cmd.Flags().StringVar(&repo, "repo", "", "")
		cmd.Flags().StringVar(&project, "project", "", "")
		cmd.Flags().StringVar(&brief, "brief", "", "")
		cmd.Flags().StringVar(&harness, "harness", "", "")
		cmd.Flags().StringVar(&model, "model", "", "")
		cmd.Flags().StringVar(&reasoning, "reasoning", "", "")
		cmd.Flags().BoolVar(&sameAsYou, "same-as-you", false, "")
		cmd.Flags().StringVar(&preset, "preset", "", "")
		cmd.Flags().StringVar(&base, "base", "HEAD", "")
		cmd.Flags().StringVar(&targetBranch, "target-branch", "", "")
		cmd.Flags().StringVar(&kind, "kind", "ship", "")
		cmd.Flags().BoolVar(&approved, "approved", false, "")
		cmd.Flags().StringArrayVar(&extra, "arg", nil, "")
		cmd.Flags().BoolVar(&permissionOverride, "permission-override", false, "")
		root.AddCommand(cmd)
	}
	add("prepare", prepare.Prepare)
	add("dispatch", prepare.Dispatch)
}
