package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/hookstatus"
	"github.com/douglasjarquin/sum/go/internal/metadata"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/statuscmd"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
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
	metadata.After(st, o.runtimeRoot, tasks, cmd.CommandPath())
}

// nativeOpen is the plugin id the inbox entrypoint can be opened through, from saved records only, or the reason it cannot.
func (o *rootOptions) nativeOpen(st *store.Store) (pluginID, reason string) {
	pluginID, reason = hookstatus.InboxOpenable(st, o.sumctlPath())
	if reason == "" && !metadata.Capability(st, "plugin_pane_open") {
		return "", "the saved capability probe does not show plugin pane opening; run `metadata enable` (or `metadata sync`) from the coordinator pane to probe the installed Herdr"
	}
	return pluginID, reason
}

var inboxPlacements = map[string]bool{"split": true, "overlay": true, "tab": true, "zoomed": true}

// openInbox opens the grouped view through the linked plugin's inbox entrypoint. Every precondition failure or Herdr
// error is a fallback: the grouped overview is the deliverable either way, and nothing syncs, pumps, or writes.
func (o *rootOptions) openInbox(st *store.Store, project, placement string) (*ordjson.Object, error) {
	view, err := statuscmd.Grouped(st, project)
	if err != nil {
		return nil, err
	}
	open := ordjson.NewObject()
	fallback := func(reason string) (*ordjson.Object, error) {
		open.Set("outcome", "fallback")
		open.Set("reason", reason)
		view.Set("open", open)
		return view, nil
	}
	pluginID, reason := o.nativeOpen(st)
	if reason != "" {
		return fallback(reason)
	}
	ctx, err := store.Context(o.installRoot)
	if err != nil {
		return fallback(err.Error())
	}
	herdrPath, err := toolpath.Find(o.runtimeRoot, "herdr")
	if err != nil {
		return fallback(err.Error())
	}
	args := []string{"plugin", "pane", "open", "--plugin", pluginID, "--entrypoint", hookstatus.InboxEntrypoint}
	if placement != "" {
		args = append(args, "--placement", placement, "--target-pane", ctxString(ctx, "pane"), "--no-focus")
	}
	opened, err := herdrclient.Call(herdrPath, ctxString(ctx, "session"), 15*time.Second, args...)
	if err != nil {
		return fallback(err.Error())
	}
	pluginPane, _ := opened.(*ordjson.Object)
	if pluginPane != nil {
		if inner, ok := pluginPane.Get("plugin_pane"); ok {
			pluginPane, _ = inner.(*ordjson.Object)
		}
	}
	var pane any
	if pluginPane != nil {
		if paneObj, ok := pluginPane.Get("pane"); ok {
			if obj, isObj := paneObj.(*ordjson.Object); isObj {
				pane, _ = obj.Get("pane_id")
			}
		}
	}
	open.Set("outcome", "opened")
	if placement == "" {
		open.Set("placement", "popup")
	} else {
		open.Set("placement", placement)
	}
	open.Set("entrypoint", hookstatus.InboxEntrypoint)
	open.Set("pane", pane)
	open.Set("note", "Read-only: the pane runs `inbox --grouped --view` (records only, no Herdr write); reading it answers, applies, and verifies nothing.")
	view.Set("open", open)
	return view, nil
}

func ctxString(ctx *ordjson.Object, key string) string {
	v, _ := ctx.Get(key)
	s, _ := v.(string)
	return s
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
			native := ordjson.NewObject()
			_, reason := o.nativeOpen(st)
			native.Set("available", reason == "")
			if reason == "" {
				native.Set("reason", nil)
			} else {
				native.Set("reason", reason)
			}
			view.Set("native_open", native)
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
	syncCmd.Flags().BoolVar(&force, "force", false, "Treat recorded tokens as unknown and rewrite every recorded endpoint (after a Herdr restart)")
	metadataCmd.AddCommand(syncCmd)

	var after, limit, maxChars int
	var grouped groupedFlags
	var open bool
	var placement string
	inboxCmd := &cobra.Command{
		Use:  "inbox",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := grouped.check(cmd); err != nil {
				return err
			}
			paged := pagingFlagsChanged(cmd)
			if open {
				if placement != "" && !inboxPlacements[placement] {
					return fmt.Errorf("--placement must be one of split, overlay, tab, zoomed")
				}
				if paged {
					return fmt.Errorf("--after, --limit, and --max-chars cannot combine with --open")
				}
				st, err := store.Open(o.home)
				if err != nil {
					return err
				}
				view, err := o.openInbox(st, grouped.project, placement)
				if err != nil {
					return err
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			}
			if placement != "" {
				return fmt.Errorf("--placement requires --open")
			}
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
	inboxCmd.Flags().BoolVar(&open, "open", false, "Open the grouped view natively through the linked plugin's inbox entrypoint; falls back to the grouped overview")
	inboxCmd.Flags().StringVar(&placement, "placement", "", "Native placement with --open: split, overlay, tab, or zoomed (default: the manifest's popup)")
	metadataCmd.AddCommand(inboxCmd)

	root.AddCommand(metadataCmd)
}
