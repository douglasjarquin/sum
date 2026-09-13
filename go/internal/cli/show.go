package cli

import (
	"github.com/douglasjarquin/sum/go/internal/evidenceview"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addShowCommand(root *cobra.Command) {
	root.AddCommand(&cobra.Command{
		Use:  "show TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view, viewErr := evidenceview.Show(st, args[0])
			if viewErr != nil {
				return viewErr
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})
}
