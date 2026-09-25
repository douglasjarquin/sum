package cli

import (
	"fmt"
	"os"
	"strconv"

	"github.com/douglasjarquin/sum/go/internal/inboxview"
	"github.com/douglasjarquin/sum/go/internal/statuscmd"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

type groupedFlags struct {
	grouped       bool
	project       string
	view          bool
	width, height int
}

func (g *groupedFlags) add(cmd *cobra.Command, withView bool) {
	cmd.Flags().BoolVar(&g.grouped, "grouped", false, "Grouped read-only overview by recorded project identity")
	cmd.Flags().StringVar(&g.project, "project", "", "Focus one project key (requires --grouped); global counts stay")
	if withView {
		cmd.Flags().BoolVar(&g.view, "view", false, "Explicit-refresh terminal view of the grouped overview (requires --grouped)")
		cmd.Flags().IntVar(&g.width, "width", 0, "View width in cells (default COLUMNS or 80)")
		cmd.Flags().IntVar(&g.height, "height", 0, "View height in lines (default LINES or 24)")
	}
}

// check rejects grouped-only flags without --grouped, mirroring the compact prerequisite check.
func (g *groupedFlags) check(cmd *cobra.Command) error {
	if g.grouped {
		return nil
	}
	for _, name := range []string{"project", "view", "width", "height"} {
		if cmd.Flags().Lookup(name) != nil && cmd.Flags().Changed(name) {
			return fmt.Errorf("--project, --view, --width, and --height require --grouped")
		}
	}
	return nil
}

func (g *groupedFlags) run(cmd *cobra.Command, home string) error {
	st, err := store.Open(home)
	if err != nil {
		return err
	}
	if !g.view {
		view, err := statuscmd.Grouped(st, g.project)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	}
	width, height := g.width, g.height
	if width <= 0 {
		width = viewDimension("COLUMNS", 80)
	}
	if height <= 0 {
		height = viewDimension("LINES", 24)
	}
	load := func() (inboxview.Overview, error) { return statuscmd.GroupedOverview(st, g.project) }
	return inboxview.Run(cmd.InOrStdin(), cmd.OutOrStdout(), width, height, inboxview.Scope{Home: st.Home, Project: g.project}, load)
}

func viewDimension(name string, fallback int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n > 0 {
		return n
	}
	return fallback
}
