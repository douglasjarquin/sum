package cli

import (
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
				view, err := statuscmd.Status(st, inboxMode)
				if err != nil {
					return err
				}
				if live {
					view.Set("live", true)
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
