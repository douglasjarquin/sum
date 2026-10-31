package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/metadata"
	"github.com/douglasjarquin/sum/go/internal/notes"
	"github.com/douglasjarquin/sum/go/internal/statuscmd"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

// projectsAnnotation marks a command whose successful RunE is a domain write or native event boundary: the root
// PersistentPostRunE then runs one bounded metadata projection. "task" projects args[0]; "all" projects every task.
const projectsAnnotation = "sum.projects"

// projectAfter runs after a command's RunE succeeded. It is silent: none of the annotated commands emit a metadata
// view, and a read never reaches here because reads carry no annotation. Failures are recorded in metadata state.
func (o *rootOptions) projectAfter(cmd *cobra.Command, args []string) {
	scope := cmd.Annotations[projectsAnnotation]
	if scope == "" {
		return
	}
	var tasks []string
	if scope == "task" {
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			return
		}
		tasks = []string{args[0]}
	}
	st, err := store.Open(o.home)
	if err != nil {
		return
	}
	if scope == "finding" {
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			return
		}
		record, err := notes.Read(st, args[0])
		if err != nil {
			return
		}
		tasks = []string{record.TaskID}
	}
	metadata.After(st, o.runtimeRoot, tasks, cmd.CommandPath())
}

func (o *rootOptions) addMetadataCommands(root *cobra.Command) {
	metadataCmd := &cobra.Command{
		Use:                "metadata",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	var raw bool
	snippetCmd := &cobra.Command{
		Use:  "snippet",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view := metadata.Snippet(o.sumctlPath(), st.Home)
			if raw {
				tomlValue, _ := view.Get("toml")
				toml, _ := tomlValue.(string)
				_, writeErr := io.WriteString(cmd.OutOrStdout(), toml)
				return writeErr
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	snippetCmd.Flags().BoolVar(&raw, "raw", false, "Print only the snippet text")
	metadataCmd.AddCommand(snippetCmd)

	metadataCmd.AddCommand(&cobra.Command{
		Use:  "status",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view := metadata.Status(st)
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	var notify bool
	enableCmd := &cobra.Command{
		Use:  "enable",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("metadata-enable")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := metadata.Enable(st, ctx, o.runtimeRoot, notify)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	enableCmd.Flags().BoolVar(&notify, "notify", false, "")
	metadataCmd.AddCommand(enableCmd)

	metadataCmd.AddCommand(&cobra.Command{
		Use:  "disable",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("metadata-disable")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := metadata.Disable(st, ctx, o.runtimeRoot)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	var force bool
	syncCmd := &cobra.Command{
		Use:  "sync",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("metadata-sync")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := metadata.Sync(st, ctx, o.runtimeRoot, force)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	syncCmd.Flags().BoolVar(&force, "force", false, "Rewrite the native labels for every recorded endpoint")
	metadataCmd.AddCommand(syncCmd)

	var after, limit, maxChars int
	var grouped groupedFlags
	inboxCmd := &cobra.Command{
		Use:  "inbox",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := grouped.check(cmd); err != nil {
				return err
			}
			paged := pagingFlagsChanged(cmd)
			if grouped.grouped {
				if paged {
					return fmt.Errorf("--after, --limit, and --max-chars cannot combine with --grouped")
				}
				st, err := store.Open(o.home)
				if err != nil {
					return err
				}
				view, err := statuscmd.Grouped(st, grouped.project)
				if err != nil {
					return err
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			}
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view, err := statuscmd.Compact(st, statuscmd.Options{Inbox: true, After: after, Limit: limit, MaxChars: maxChars})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	compactFlags(inboxCmd, &after, &limit, &maxChars)
	grouped.add(inboxCmd, false)
	metadataCmd.AddCommand(inboxCmd)

	root.AddCommand(metadataCmd)
}
