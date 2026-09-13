package cli

import (
	"fmt"
	"io"

	"github.com/douglasjarquin/sum/go/internal/lsp"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addLspCommand(root *cobra.Command) {
	lspCmd := &cobra.Command{
		Use:                "lsp",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	lspCmd.AddCommand(&cobra.Command{
		Use:  "ensure",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			payload, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return nil
			}
			root := o.installRoot
			if root == "" {
				root = o.runtimeRoot
			}
			_ = lsp.Ensure(root, payload)
			return nil
		},
	})

	root.AddCommand(lspCmd)
}
