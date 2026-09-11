package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/contextview"
	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/doctor"
	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/evidenceview"
	"github.com/douglasjarquin/sum/go/internal/graph"
	"github.com/douglasjarquin/sum/go/internal/hookstatus"
	"github.com/douglasjarquin/sum/go/internal/metadata"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/project"
	"github.com/douglasjarquin/sum/go/internal/release"
	"github.com/douglasjarquin/sum/go/internal/roleinit"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/statuscmd"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
	"github.com/spf13/cobra"
)

const Version = "sum 0.1.0"

type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("reference helper exited with status %d", e.Code)
}

type rootOptions struct {
	reference string
	home      string
	homeSet   bool
	out       io.Writer
	err       io.Writer
}

func NewRoot(reference string, out, errOut io.Writer) *cobra.Command {
	if out == nil {
		out = io.Discard
	}
	if errOut == nil {
		errOut = io.Discard
	}
	opts := &rootOptions{reference: reference, out: out, err: errOut}
	root := &cobra.Command{
		Use:                "sumctl",
		Short:              "Small, synchronous helpers for sum",
		Version:            Version,
		SilenceErrors:      true,
		SilenceUsage:       true,
		DisableSuggestions: true,
		TraverseChildren:   true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetVersionTemplate("{{.Version}}\n")
	root.CompletionOptions.DisableDefaultCmd = true
	root.Flags().StringVar(&opts.home, "home", "", "state home")
	root.Flags().Lookup("home").NoOptDefVal = ""
	root.PersistentPreRun = func(cmd *cobra.Command, _ []string) {
		opts.homeSet = root.Flags().Changed("home")
	}
	root.SetHelpCommand(nil)
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		if opts.reference != "" {
			if _, statErr := os.Stat(opts.reference); statErr == nil {
				if err := opts.compat(cmd.Context(), []string{"--help"}); err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), err)
				}
				return
			}
		}
		_, _ = io.WriteString(cmd.OutOrStdout(), cmd.UsageString())
	})

	root.AddCommand(&cobra.Command{
		Use:  "release-contract",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return emitJSON(cmd.OutOrStdout(), contract.BuildRelease())
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "settings",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && args[0] == "show" && opts.homeSet {
				st, err := store.Open(opts.home)
				if err != nil {
					return err
				}
				view, err := settings.CapacityView(st)
				if err != nil {
					return err
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			}
			return opts.compat(cmd.Context(), append([]string{"settings"}, args...))
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "preset",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.homeSet {
				st, err := store.Open(opts.home)
				if err != nil {
					return err
				}
				switch {
				case len(args) == 1 && args[0] == "list":
					view, err := settings.PresetList(st)
					if err != nil {
						return err
					}
					return emitOrdjson(cmd.OutOrStdout(), view)
				case len(args) == 2 && args[0] == "show":
					view, err := settings.PresetShow(st, args[1])
					if err != nil {
						return err
					}
					return emitOrdjson(cmd.OutOrStdout(), view)
				}
			}
			return opts.compat(cmd.Context(), append([]string{"preset"}, args...))
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "graph",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.homeSet && len(args) >= 1 && args[0] == "config" {
				if harness, raw, ok := parseGraphConfigArgs(args[1:]); ok && graph.IsValidHarness(harness) {
					if runtimeRoot := runtimeRootFromReference(opts.reference); runtimeRoot != "" {
						if _, err := store.Open(opts.home); err != nil {
							return err
						}
						view, err := graph.Config(runtimeRoot, harness)
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
					}
				}
			}
			return opts.compat(cmd.Context(), append([]string{"graph"}, args...))
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "metadata",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.homeSet && opts.reference != "" {
				raw := len(args) == 2 && args[0] == "snippet" && args[1] == "--raw"
				plain := len(args) == 1 && args[0] == "snippet"
				if raw || plain {
					if _, err := store.Open(opts.home); err != nil {
						return err
					}
					view := metadata.Snippet(opts.reference, opts.home)
					if raw {
						tomlValue, _ := view.Get("toml")
						toml, _ := tomlValue.(string)
						_, writeErr := io.WriteString(cmd.OutOrStdout(), toml)
						return writeErr
					}
					return emitOrdjson(cmd.OutOrStdout(), view)
				}
			}
			return opts.compat(cmd.Context(), append([]string{"metadata"}, args...))
		},
	})

	statusHandler := func(name string, inboxMode bool) func(cmd *cobra.Command, args []string) error {
		return func(cmd *cobra.Command, args []string) error {
			if opts.homeSet && len(args) == 0 {
				if st, err := store.Open(opts.home); err == nil {
					view, viewErr := statuscmd.Status(st, inboxMode)
					if viewErr == nil {
						return emitOrdjson(cmd.OutOrStdout(), view)
					}
					return viewErr
				}
			}
			return opts.compat(cmd.Context(), append([]string{name}, args...))
		}
	}
	root.AddCommand(&cobra.Command{
		Use:                "status",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               statusHandler("status", false),
	})
	root.AddCommand(&cobra.Command{
		Use:                "inbox",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               statusHandler("inbox", true),
	})

	root.AddCommand(&cobra.Command{
		Use:                "brief",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.homeSet && len(args) == 2 && args[0] == "list" {
				if st, err := store.Open(opts.home); err == nil {
					view, viewErr := versions.BriefList(st, args[1])
					if viewErr == nil {
						return emitOrdjson(cmd.OutOrStdout(), view)
					}
					return viewErr
				}
			}
			return opts.compat(cmd.Context(), append([]string{"brief"}, args...))
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "env",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.homeSet && opts.reference != "" && len(args) >= 2 && args[0] == "show" {
				if taskID, maxChars, ok := parseEnvShowArgs(args[1:]); ok {
					st, err := store.Open(opts.home)
					if err != nil {
						return err
					}
					view, viewErr := environment.Show(st, taskID, opts.reference, maxChars)
					if viewErr != nil {
						return viewErr
					}
					return emitOrdjson(cmd.OutOrStdout(), view)
				}
			}
			return opts.compat(cmd.Context(), append([]string{"env"}, args...))
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "release",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.homeSet {
				switch {
				case len(args) == 1 && args[0] == "list":
					st, err := store.Open(opts.home)
					if err != nil {
						return err
					}
					view, viewErr := release.List(st)
					if viewErr != nil {
						return viewErr
					}
					return emitOrdjson(cmd.OutOrStdout(), view)
				case len(args) == 2 && args[0] == "show":
					st, err := store.Open(opts.home)
					if err != nil {
						return err
					}
					view, viewErr := release.Show(st, args[1])
					if viewErr != nil {
						return viewErr
					}
					return emitOrdjson(cmd.OutOrStdout(), view)
				}
			}
			return opts.compat(cmd.Context(), append([]string{"release"}, args...))
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "project",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.homeSet {
				switch {
				case len(args) == 1 && args[0] == "list":
					st, err := store.Open(opts.home)
					if err != nil {
						return err
					}
					view, viewErr := project.List(st, runtimeRootFromReference(opts.reference))
					if viewErr != nil {
						return viewErr
					}
					return emitOrdjson(cmd.OutOrStdout(), view)
				case len(args) == 2 && args[0] == "show":
					st, err := store.Open(opts.home)
					if err != nil {
						return err
					}
					view, viewErr := project.Show(st, args[1])
					if viewErr != nil {
						return viewErr
					}
					return emitOrdjson(cmd.OutOrStdout(), view)
				}
			}
			return opts.compat(cmd.Context(), append([]string{"project"}, args...))
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "hook",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.homeSet && opts.reference != "" && len(args) == 1 && args[0] == "status" {
				st, err := store.Open(opts.home)
				if err != nil {
					return err
				}
				runtimeRoot := runtimeRootFromReference(opts.reference)
				ctx, ctxErr := store.Context(runtimeRoot)
				if ctxErr != nil {
					ctx = nil
				}
				view, viewErr := hookstatus.Status(st, ctx, runtimeRoot, opts.reference)
				if viewErr != nil {
					return viewErr
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			}
			return opts.compat(cmd.Context(), append([]string{"hook"}, args...))
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "show",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.homeSet && len(args) == 1 {
				st, err := store.Open(opts.home)
				if err != nil {
					return err
				}
				view, viewErr := evidenceview.Show(st, args[0])
				if viewErr != nil {
					return viewErr
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			}
			return opts.compat(cmd.Context(), append([]string{"show"}, args...))
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "context",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.homeSet && opts.reference != "" && len(args) >= 1 {
				taskID := args[0]
				sections, sectionsOK := []string(nil), true
				if len(args) > 1 {
					sections, sectionsOK = parseContextSectionArgs(args[1:])
				}
				if sectionsOK {
					st, err := store.Open(opts.home)
					if err != nil {
						return err
					}
					view, viewErr := contextview.View(st, taskID, opts.reference, sections)
					if viewErr != nil {
						return viewErr
					}
					return emitOrdjson(cmd.OutOrStdout(), view)
				}
			}
			return opts.compat(cmd.Context(), append([]string{"context"}, args...))
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "doctor",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.homeSet && opts.reference != "" && len(args) == 0 {
				if st, err := store.Open(opts.home); err == nil {
					view := doctor.Doctor(runtimeRootFromReference(opts.reference), st)
					if emitErr := emitOrdjson(cmd.OutOrStdout(), view); emitErr != nil {
						return emitErr
					}
					if okValue, _ := view.Get("ok"); okValue != true {
						return &ExitError{Code: 1}
					}
					return nil
				}
			}
			return opts.compat(cmd.Context(), append([]string{"doctor"}, args...))
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "init",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.homeSet && opts.reference != "" {
				if role, task, ok := parseInitArgs(args); ok {
					st, err := store.Open(opts.home)
					if err == nil && !st.Designated() {
						root := runtimeRootFromReference(opts.reference)
						ctx, ctxErr := store.Context(root)
						if ctxErr != nil {
							return ctxErr
						}
						view, initErr := roleinit.Init(root, st, ctx, role, task)
						if initErr != nil {
							return initErr
						}
						return emitOrdjson(cmd.OutOrStdout(), view)
					}
				}
			}
			return opts.compat(cmd.Context(), append([]string{"init"}, args...))
		},
	})

	for _, name := range compatibilityCommands {
		command := &cobra.Command{
			Use:                name,
			DisableFlagParsing: true,
			Args:               cobra.ArbitraryArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return opts.compat(cmd.Context(), append([]string{cmd.Name()}, args...))
			},
		}
		root.AddCommand(command)
	}
	return root
}

var compatibilityCommands = []string{
	"prepare", "dispatch", "start", "help", "notes", "notice", "archive", "ask", "answer", "report", "resolve", "review", "verify", "pr", "cleanup", "pump", "attention", "bind", "backup", "herdr", "dev", "refresh", "update",
}

func parseInitArgs(tokens []string) (role, task string, ok bool) {
	roles := map[string]bool{"coordinator": true, "worker": true, "developer": true}
	reclaimSeen := false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--role":
			if role != "" || i+1 >= len(tokens) {
				return "", "", false
			}
			i++
			role = tokens[i]
		case strings.HasPrefix(token, "--role="):
			if role != "" {
				return "", "", false
			}
			role = strings.TrimPrefix(token, "--role=")
		case token == "--task":
			if task != "" || i+1 >= len(tokens) {
				return "", "", false
			}
			i++
			task = tokens[i]
		case strings.HasPrefix(token, "--task="):
			if task != "" {
				return "", "", false
			}
			task = strings.TrimPrefix(token, "--task=")
		case token == "--reclaim":
			if reclaimSeen {
				return "", "", false
			}
			reclaimSeen = true
		default:
			return "", "", false
		}
	}
	if role != "" && !roles[role] {
		return "", "", false
	}
	return role, task, true
}

func parseGraphConfigArgs(tokens []string) (harness string, raw bool, ok bool) {
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--harness":
			if harness != "" || i+1 >= len(tokens) {
				return "", false, false
			}
			i++
			harness = tokens[i]
		case strings.HasPrefix(token, "--harness="):
			if harness != "" {
				return "", false, false
			}
			harness = strings.TrimPrefix(token, "--harness=")
		case token == "--raw":
			if raw {
				return "", false, false
			}
			raw = true
		default:
			return "", false, false
		}
	}
	if harness == "" {
		return "", false, false
	}
	return harness, raw, true
}

// validContextSections lists only the sections contextview.View actually implements. A name from
// CONTEXT_SECTIONS MUST NOT be added here until contextview.View grows a matching case in the SAME commit —
// otherwise the native path would silently omit that key instead of falling back to Python (a real bug this
// port hit once already).
var validContextSections = map[string]bool{
	"outline": true, "brief": true, "decisions": true, "handoff": true, "evidence": true,
	"execution": true, "returns": true, "notes": true, "environment": true, "update": true,
}

// parseContextSectionArgs recognizes only repeated `--section NAME`/`--section=NAME` flags (no other flag), one
// or more of the recognized CONTEXT_SECTIONS names, deduplicated in first-occurrence order — mirroring
// `list(dict.fromkeys(args.section or []))`. Any other shape (an unrecognized flag, an unknown section, or zero
// --section flags) falls back to the Python reference.
func parseContextSectionArgs(tokens []string) (sections []string, ok bool) {
	var raw []string
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--section":
			if i+1 >= len(tokens) {
				return nil, false
			}
			i++
			raw = append(raw, tokens[i])
		case strings.HasPrefix(token, "--section="):
			raw = append(raw, strings.TrimPrefix(token, "--section="))
		default:
			return nil, false
		}
	}
	if len(raw) == 0 {
		return nil, false
	}
	seen := map[string]bool{}
	for _, sec := range raw {
		if !validContextSections[sec] {
			return nil, false
		}
		if seen[sec] {
			continue
		}
		seen[sec] = true
		sections = append(sections, sec)
	}
	return sections, true
}

func parseEnvShowArgs(tokens []string) (task string, maxChars int, ok bool) {
	maxChars = environment.DefaultMaxChars
	maxCharsSet := false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--max-chars":
			if maxCharsSet || i+1 >= len(tokens) {
				return "", 0, false
			}
			i++
			n, err := strconv.Atoi(tokens[i])
			if err != nil {
				return "", 0, false
			}
			maxChars = n
			maxCharsSet = true
		case strings.HasPrefix(token, "--max-chars="):
			if maxCharsSet {
				return "", 0, false
			}
			n, err := strconv.Atoi(strings.TrimPrefix(token, "--max-chars="))
			if err != nil {
				return "", 0, false
			}
			maxChars = n
			maxCharsSet = true
		case strings.HasPrefix(token, "-"):
			return "", 0, false
		case task == "":
			task = token
		default:
			return "", 0, false
		}
	}
	if task == "" {
		return "", 0, false
	}
	return task, maxChars, true
}

func runtimeRootFromReference(reference string) string {
	if reference == "" {
		return ""
	}
	return filepath.Dir(filepath.Dir(reference))
}

func (o *rootOptions) compat(ctx context.Context, args []string) error {
	if o.reference == "" {
		return fmt.Errorf("sumctl reference helper is not configured")
	}
	argv := append([]string(nil), args...)
	argv = normalizeHome(argv, o.home, o.homeSet)
	command := exec.CommandContext(ctx, o.reference, argv...)
	command.Stdin = os.Stdin
	command.Stdout = o.out
	command.Stderr = o.err
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if exit, ok := err.(*exec.ExitError); ok {
			return &ExitError{Code: exit.ExitCode()}
		}
		return err
	}
	return nil
}

func emitJSON(out io.Writer, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(encoded))
	return err
}

func emitOrdjson(out io.Writer, value any) error {
	encoded, err := ordjson.MarshalIndent(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(encoded))
	return err
}

func normalizeHome(args []string, home string, homeSet bool) []string {
	var prefix []string
	if homeSet {
		prefix = append([]string{"--home", home}, prefix...)
	}
	return append(prefix, args...)
}
