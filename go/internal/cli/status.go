package cli

import (
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/factoryview"
	"github.com/douglasjarquin/sum/go/internal/statuscmd"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addStatusCommands(root *cobra.Command) {
	add := func(name string, inboxMode bool) {
		var live, compact bool
		var after, limit, maxChars int
		var grouped groupedFlags
		cmd := &cobra.Command{
			Use:  name,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				if err := grouped.check(cmd); err != nil {
					return err
				}
				if grouped.grouped {
					if compact || live {
						return fmt.Errorf("--grouped cannot combine with --compact or --live")
					}
					if cmd.Flags().Changed("after") || cmd.Flags().Changed("max-chars") {
						return fmt.Errorf("--after and --max-chars require --compact; --grouped takes --limit for the digest page")
					}
					if err := boundedLimit(limit, factoryview.MaxLimit); err != nil {
						return err
					}
					return grouped.run(cmd, o.home, limit)
				}
				if !compact && pagingFlagsChanged(cmd) {
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
		grouped.add(cmd, true)
		root.AddCommand(cmd)
	}
	add("status", false)
	add("inbox", true)
}

// boundedLimit is the shared --limit check for the digest page bound.
func boundedLimit(limit, max int) error {
	if limit < 1 || limit > max {
		return fmt.Errorf("--limit accepts 1..%d", max)
	}
	return nil
}

func pagingFlagsChanged(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("after") || cmd.Flags().Changed("limit") || cmd.Flags().Changed("max-chars")
}

func compactFlags(cmd *cobra.Command, after, limit, maxChars *int) {
	cmd.Flags().IntVar(after, "after", 0, "Skip this many presentation items")
	cmd.Flags().IntVar(limit, "limit", statuscmd.CompactLimit, "Maximum presentation items, or digest outcomes with --grouped (1..100)")
	cmd.Flags().IntVar(maxChars, "max-chars", statuscmd.CompactChars, "Maximum characters per text (1..2000); use detail for full text")
}
