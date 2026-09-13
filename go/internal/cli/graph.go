package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/graph"
	"github.com/douglasjarquin/sum/go/internal/guard"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addGraphCommands(root *cobra.Command) {
	graphCmd := &cobra.Command{
		Use:                "graph",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	graphCmd.AddCommand(&cobra.Command{
		Use:  "init TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			if err := guard.Candidate(o.installRoot, st, "graph-init"); err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			if err := app.RequireCoordinator(st, ctx); err != nil {
				return err
			}
			view, err := graph.InitTask(st, o.runtimeRoot, args[0])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	graphCmd.AddCommand(&cobra.Command{
		Use:  "status TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view, err := graph.StatusTask(st, o.runtimeRoot, args[0])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	var harness string
	var raw bool
	configCmd := &cobra.Command{
		Use:  "config",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !graph.IsValidHarness(harness) {
				return usageError("graph config", []string{"--harness", harness})
			}
			if _, err := store.Open(o.home); err != nil {
				return err
			}
			view, err := graph.Config(o.runtimeRoot, harness)
			if err != nil {
				return err
			}
			if raw {
				snippetValue, _ := view.Get("snippet")
				snippet, _ := snippetValue.(string)
				if !strings.HasSuffix(snippet, "\n") {
					snippet += "\n"
				}
				_, writeErr := io.WriteString(cmd.OutOrStdout(), snippet)
				return writeErr
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	configCmd.Flags().StringVar(&harness, "harness", "", "")
	configCmd.Flags().BoolVar(&raw, "raw", false, "Print only the snippet text")
	_ = configCmd.MarkFlagRequired("harness")
	graphCmd.AddCommand(configCmd)

	root.AddCommand(graphCmd)
}
