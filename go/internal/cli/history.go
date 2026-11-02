package cli

import (
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addHistoryCommand(root *cobra.Command) {
	root.AddCommand(&cobra.Command{Use: "history", Short: "Read preserved task records without granting live authority", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		s, err := store.OpenHistorical(o.home)
		if err != nil {
			return err
		}
		tasks, err := s.AllTasks()
		if err != nil {
			return err
		}
		return emitJSON(cmd.OutOrStdout(), map[string]any{"home": s.Home, "read_only": true, "tasks": tasks})
	}})
}
