package cli

import (
	"github.com/douglasjarquin/sum/go/internal/backup"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addRestoreCommand(root *cobra.Command) {
	root.AddCommand(&cobra.Command{Use: "restore ARCHIVE NEW_HOME", Short: "Import a records backup into a fresh historical state home", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		view, err := backup.Restore(args[0], args[1])
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	}})
}
