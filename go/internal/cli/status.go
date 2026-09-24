package cli

import (
	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/statuscmd"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addStatusCommands(root *cobra.Command) {
	add := func(name string, inboxMode bool) {
		var live bool
		cmd := &cobra.Command{
			Use:  name,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				st, err := store.Open(o.home)
				if err != nil {
					return err
				}
				opts := statuscmd.Options{Inbox: inboxMode, Live: live, RuntimeRoot: o.runtimeRoot, SumctlPath: o.sumctlPath()}
				if live {
					opts.Ctx = app.OptionalContext(o.installRoot)
				}
				view, err := statuscmd.Status(st, opts)
				if err != nil {
					return err
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			},
		}
		cmd.Flags().BoolVar(&live, "live", false, "")
		root.AddCommand(cmd)
	}
	add("status", false)
	add("inbox", true)
}
