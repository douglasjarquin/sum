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
			repo, project, brief, harness, model, reasoning, preset, base, kind string
			sameAsYou, approved                                                 bool
			extra                                                               []string
		)
		cmd := &cobra.Command{
			Use:  name,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				if brief == "" || (kind != "ship" && kind != "scout") {
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
				view, err := run(st, ctx, prepare.Args{
					Repo:        repo,
					Project:     project,
					Brief:       brief,
					Harness:     harness,
					Model:       model,
					Reasoning:   reasoning,
					SameAsYou:   sameAsYou,
					Preset:      preset,
					PresetSet:   cmd.Flags().Changed("preset"),
					Base:        base,
					Kind:        kind,
					Approved:    approved,
					Extra:       extra,
					RuntimeRoot: o.runtimeRoot,
					SumctlPath:  o.sumctlPath(),
				})
				if err != nil {
					return err
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			},
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
		cmd.Flags().StringVar(&kind, "kind", "ship", "")
		cmd.Flags().BoolVar(&approved, "approved", false, "")
		cmd.Flags().StringArrayVar(&extra, "arg", nil, "")
		root.AddCommand(cmd)
	}
	add("prepare", prepare.Prepare)
	add("dispatch", prepare.Dispatch)
}
