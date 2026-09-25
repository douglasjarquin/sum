package cli

import (
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/statuscmd"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addStatusCommands(root *cobra.Command) {
	add := func(name string, inboxMode bool) {
		var live, compact bool
		var after, limit, maxChars int
		cmd := &cobra.Command{
			Use:  name,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				if !compact && (cmd.Flags().Changed("after") || cmd.Flags().Changed("limit") || cmd.Flags().Changed("max-chars")) {
					return fmt.Errorf("--after, --limit, and --max-chars require --compact")
				}
				st, err := store.Open(o.home)
				if err != nil {
					return err
				}
				opts := statuscmd.Options{Compact: compact, After: after, Limit: limit, MaxChars: maxChars, Inbox: inboxMode, Live: live, RuntimeRoot: o.runtimeRoot, SumctlPath: o.sumctlPath()}
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
		cmd.Flags().BoolVar(&compact, "compact", false, "Bounded presentation with global known decision counts")
		compactFlags(cmd, &after, &limit, &maxChars)
		root.AddCommand(cmd)
	}
	add("status", false)
	add("inbox", true)
}

func compactFlags(cmd *cobra.Command, after, limit, maxChars *int) {
	cmd.Flags().IntVar(after, "after", 0, "Skip this many presentation items")
	cmd.Flags().IntVar(limit, "limit", statuscmd.CompactLimit, "Maximum presentation items (1..100)")
	cmd.Flags().IntVar(maxChars, "max-chars", statuscmd.CompactChars, "Maximum characters per text (1..2000); use detail for full text")
}
