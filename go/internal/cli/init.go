package cli

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/roleinit"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

var validInitRoles = map[string]bool{"coordinator": true, "worker": true, "developer": true}

func (o *rootOptions) addInitCommand(root *cobra.Command) {
	var role, task string
	var reclaim bool
	cmd := &cobra.Command{
		Use:  "init",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if role != "" && !validInitRoles[role] {
				return fmt.Errorf("invalid init arguments")
			}
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			ctx, ctxErr := store.Context(o.runtimeRoot)
			if ctxErr != nil {
				return fmt.Errorf("%s", capitalizeHerdrContext(ctxErr.Error()))
			}
			if !st.Designated() {
				view, initErr := roleinit.Init(o.runtimeRoot, st, ctx, role, task)
				if initErr != nil {
					return initErr
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			}
			view, initErr := roleinit.InitDesignated(roleinit.DesignatedOpts{
				RuntimeRoot: o.runtimeRoot,
				SumctlPath:  o.sumctlPath(),
				Store:       st,
				Ctx:         ctx,
				Role:        role,
				Task:        task,
				Reclaim:     reclaim,
			})
			if initErr != nil {
				return initErr
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "")
	cmd.Flags().StringVar(&task, "task", "", "")
	cmd.Flags().BoolVar(&reclaim, "reclaim", false, "")
	root.AddCommand(cmd)
}
