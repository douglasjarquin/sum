package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/contextview"
	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/doctor"
	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/evidenceview"
	"github.com/douglasjarquin/sum/go/internal/guard"
	"github.com/douglasjarquin/sum/go/internal/helpview"
	"github.com/douglasjarquin/sum/go/internal/metadata"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/project"
	"github.com/douglasjarquin/sum/go/internal/release"
	"github.com/douglasjarquin/sum/go/internal/roleinit"
	sumruntime "github.com/douglasjarquin/sum/go/internal/runtime"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/statuscmd"
	"github.com/douglasjarquin/sum/go/internal/store"
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
	reference   string
	runtimeRoot string
	installRoot string
	home        string
	homeSet     bool
	format      string
	out         io.Writer
	err         io.Writer
}

func NewRoot(reference string, out, errOut io.Writer) *cobra.Command {
	if out == nil {
		out = io.Discard
	}
	if errOut == nil {
		errOut = io.Discard
	}
	runtimeRoot := sumruntime.RootFromHelper(reference)
	if runtimeRoot == "" {
		cwd, _ := os.Getwd()
		runtimeRoot = cwd
	}
	installRoot := sumruntime.ResolveInstallation(runtimeRoot, os.Getenv("SUM_INSTALL_ROOT"))
	opts := &rootOptions{reference: reference, runtimeRoot: runtimeRoot, installRoot: installRoot, out: out, err: errOut}
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
	root.PersistentFlags().StringVar(&opts.format, "format", "toon", "stdout encoding: toon or json")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		opts.homeSet = root.Flags().Changed("home")
		if !opts.homeSet {
			opts.home = sumruntime.DefaultHome(opts.installRoot)
		}
		if opts.format == "" {
			opts.format = "toon"
		}
		outputFormat = opts.format
		if _, err := os.Stat(filepath.Join(opts.installRoot, "release.json")); err == nil {
			return fmt.Errorf("%s is an immutable release tree. Run the installation's bin/sumctl, which selects a runtime and keeps state in its own .sum; a release never owns state.", opts.installRoot)
		}
		return nil
	}
	root.SetHelpCommand(nil)
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		view, err := helpview.View(opts.runtimeRoot, "")
		if err != nil {
			_, _ = io.WriteString(cmd.OutOrStdout(), cmd.UsageString())
			return
		}
		_ = emitOrdjson(cmd.OutOrStdout(), view)
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
			if len(args) == 1 && args[0] == "show" {
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
			if len(args) >= 1 && args[0] == "set" {
				return opts.runSettingsSet(cmd, args[1:])
			}
			return usageError("settings", args)
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "preset",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
			case len(args) >= 1 && args[0] == "set":
				return opts.runPresetSet(cmd, args[1:])
			case len(args) == 2 && args[0] == "delete":
				return opts.runPresetDelete(cmd, args[1])
			}
			return usageError("preset", args)
		},
	})

	opts.addGraphCommands(root)

	root.AddCommand(&cobra.Command{
		Use:                "metadata",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.sumctlPath() != "" {
				raw := len(args) == 2 && args[0] == "snippet" && args[1] == "--raw"
				plain := len(args) == 1 && args[0] == "snippet"
				if raw || plain {
					st, err := store.Open(opts.home)
					if err != nil {
						return err
					}
					view := metadata.Snippet(opts.sumctlPath(), st.Home)
					if raw {
						tomlValue, _ := view.Get("toml")
						toml, _ := tomlValue.(string)
						_, writeErr := io.WriteString(cmd.OutOrStdout(), toml)
						return writeErr
					}
					return emitOrdjson(cmd.OutOrStdout(), view)
				}
			}
			return opts.runMetadata(cmd, args)
		},
	})

	statusHandler := func(name string, inboxMode bool) func(cmd *cobra.Command, args []string) error {
		return func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				if st, err := store.Open(opts.home); err == nil {
					view, viewErr := statuscmd.Status(st, inboxMode)
					if viewErr == nil {
						return emitOrdjson(cmd.OutOrStdout(), view)
					}
					return viewErr
				}
			}
			if len(args) == 1 && args[0] == "--live" {
				if st, err := store.Open(opts.home); err == nil {
					view, viewErr := statuscmd.Status(st, inboxMode)
					if viewErr != nil {
						return viewErr
					}
					view.Set("live", true)
					return emitOrdjson(cmd.OutOrStdout(), view)
				}
			}
			return usageError(name, args)
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

	opts.addBriefCommands(root)

	root.AddCommand(&cobra.Command{
		Use:                "env",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.runEnv(cmd, args)
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "release",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(opts.home)
			if err != nil {
				return err
			}
			switch {
			case len(args) == 1 && args[0] == "list":
				view, viewErr := release.List(st)
				if viewErr != nil {
					return viewErr
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			case len(args) == 2 && args[0] == "show":
				view, viewErr := release.Show(st, args[1])
				if viewErr != nil {
					return viewErr
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			case len(args) >= 1 && args[0] == "stage":
				ref := "HEAD"
				if len(args) == 3 && args[1] == "--ref" {
					ref = args[2]
				} else if len(args) == 2 && strings.HasPrefix(args[1], "--ref=") {
					ref = strings.TrimPrefix(args[1], "--ref=")
				} else if len(args) == 2 && !strings.HasPrefix(args[1], "-") {
					ref = args[1]
				} else if len(args) != 1 {
					return usageError("release stage", args[1:])
				}
				if err := guard.Candidate(opts.installRoot, st, "release-stage"); err != nil {
					return err
				}
				view, err := release.Stage(st, ref)
				if err != nil {
					return err
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			default:
				return usageError("release", args)
			}
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "project",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(opts.home)
			if err != nil {
				return err
			}
			switch {
			case len(args) == 1 && args[0] == "list":
				view, viewErr := project.List(st, opts.runtimeRoot)
				if viewErr != nil {
					return viewErr
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			case len(args) == 2 && args[0] == "show":
				view, viewErr := project.Show(st, args[1])
				if viewErr != nil {
					return viewErr
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			case len(args) >= 1 && args[0] == "enroll":
				parsed, ok := parseProjectEnrollArgs(args[1:])
				if !ok {
					return usageError("project enroll", args[1:])
				}
				if err := guard.Candidate(opts.installRoot, st, "project-enroll"); err != nil {
					return err
				}
				ctx, err := store.Context(opts.installRoot)
				if err != nil {
					return err
				}
				view, err := project.Enroll(st, ctx, opts.runtimeRoot, parsed)
				if err != nil {
					return err
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			case len(args) >= 2 && args[0] == "migrate":
				name, apply, ok := parseProjectMigrateArgs(args[1:])
				if !ok {
					return usageError("project migrate", args[1:])
				}
				ctx := app.OptionalContext(opts.installRoot)
				view, err := project.Migrate(st, ctx, opts.runtimeRoot, name, apply)
				if err != nil {
					return err
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			default:
				return usageError("project", args)
			}
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "hook",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.runHook(cmd, args)
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "show",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
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
			return usageError("show", args)
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "context",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) >= 1 {
				taskID := args[0]
				contextOpts, ok := contextview.Options{
					After:    contextview.DefaultAfter,
					Limit:    contextview.DefaultLimit,
					MaxChars: contextview.DefaultMaxChars,
				}, true
				if len(args) > 1 {
					contextOpts, ok = parseContextArgs(args[1:])
				}
				if ok {
					st, err := store.Open(opts.home)
					if err != nil {
						return err
					}
					view, viewErr := contextview.View(st, taskID, opts.sumctlPath(), contextOpts)
					if viewErr != nil {
						return viewErr
					}
					return emitOrdjson(cmd.OutOrStdout(), view)
				}
			}
			return usageError("context", args)
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "doctor",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				if st, err := store.Open(opts.home); err == nil {
					view := doctor.Doctor(opts.runtimeRoot, opts.installRoot, st)
					if emitErr := emitOrdjson(cmd.OutOrStdout(), view); emitErr != nil {
						return emitErr
					}
					if okValue, _ := view.Get("ok"); okValue != true {
						return &ExitError{Code: 1}
					}
					return nil
				}
			}
			return usageError("doctor", args)
		},
	})

	root.AddCommand(&cobra.Command{
		Use:                "init",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			role, task, reclaim, ok := parseInitArgs(args)
			if !ok {
				return fmt.Errorf("invalid init arguments")
			}
			st, err := store.Open(opts.home)
			if err != nil {
				return err
			}
			ctx, ctxErr := store.Context(opts.runtimeRoot)
			if ctxErr != nil {
				return fmt.Errorf("%s", capitalizeHerdrContext(ctxErr.Error()))
			}
			if !st.Designated() {
				view, initErr := roleinit.Init(opts.runtimeRoot, st, ctx, role, task)
				if initErr != nil {
					return initErr
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			}
			view, initErr := roleinit.InitDesignated(roleinit.DesignatedOpts{
				RuntimeRoot: opts.runtimeRoot,
				SumctlPath:  opts.sumctlPath(),
				Store:       st,
				Ctx:         ctx,
				Role:        role,
				Task:        task,
				Reclaim:     reclaim,
			})
			if initErr != nil {
				return initErr
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	opts.addNativeCommands(root)
	opts.format = "toon"
	wrapOutputFormat(root, opts)
	return root
}

func parseInitArgs(tokens []string) (role, task string, reclaim, ok bool) {
	roles := map[string]bool{"coordinator": true, "worker": true, "developer": true}
	reclaimSeen := false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--role":
			if role != "" || i+1 >= len(tokens) {
				return "", "", false, false
			}
			i++
			role = tokens[i]
		case strings.HasPrefix(token, "--role="):
			if role != "" {
				return "", "", false, false
			}
			role = strings.TrimPrefix(token, "--role=")
		case token == "--task":
			if task != "" || i+1 >= len(tokens) {
				return "", "", false, false
			}
			i++
			task = tokens[i]
		case strings.HasPrefix(token, "--task="):
			if task != "" {
				return "", "", false, false
			}
			task = strings.TrimPrefix(token, "--task=")
		case token == "--reclaim":
			if reclaimSeen {
				return "", "", false, false
			}
			reclaimSeen = true
		default:
			return "", "", false, false
		}
	}
	if role != "" && !roles[role] {
		return "", "", false, false
	}
	return role, task, reclaimSeen, true
}

// validContextSections lists only the sections contextview.View actually implements.
var validContextSections = map[string]bool{
	"outline": true, "brief": true, "decisions": true, "handoff": true, "evidence": true,
	"execution": true, "returns": true, "notes": true, "environment": true, "update": true,
}

var validContextRoles = map[string]bool{"worker": true, "reviewer": true, "coordinator": true}

func parseContextArgs(tokens []string) (contextview.Options, bool) {
	result := contextview.Options{
		After:    contextview.DefaultAfter,
		Limit:    contextview.DefaultLimit,
		MaxChars: contextview.DefaultMaxChars,
	}
	var raw []string
	sinceSet, revisionSet := false, false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--section":
			if i+1 >= len(tokens) {
				return contextview.Options{}, false
			}
			i++
			raw = append(raw, tokens[i])
		case strings.HasPrefix(token, "--section="):
			raw = append(raw, strings.TrimPrefix(token, "--section="))
		case token == "--role":
			if result.Role != "" || i+1 >= len(tokens) {
				return contextview.Options{}, false
			}
			i++
			result.Role = tokens[i]
		case strings.HasPrefix(token, "--role="):
			if result.Role != "" {
				return contextview.Options{}, false
			}
			result.Role = strings.TrimPrefix(token, "--role=")
		case token == "--since":
			if sinceSet || i+1 >= len(tokens) {
				return contextview.Options{}, false
			}
			i++
			result.Since = tokens[i]
			sinceSet = true
		case strings.HasPrefix(token, "--since="):
			if sinceSet {
				return contextview.Options{}, false
			}
			result.Since = strings.TrimPrefix(token, "--since=")
			sinceSet = true
		case token == "--revision":
			if revisionSet || i+1 >= len(tokens) {
				return contextview.Options{}, false
			}
			i++
			result.Revision = tokens[i]
			revisionSet = true
		case strings.HasPrefix(token, "--revision="):
			if revisionSet {
				return contextview.Options{}, false
			}
			result.Revision = strings.TrimPrefix(token, "--revision=")
			revisionSet = true
		case token == "--kind":
			if i+1 >= len(tokens) {
				return contextview.Options{}, false
			}
			i++
			if tokens[i] != "" {
				result.Kinds = append(result.Kinds, tokens[i])
			}
		case strings.HasPrefix(token, "--kind="):
			if value := strings.TrimPrefix(token, "--kind="); value != "" {
				result.Kinds = append(result.Kinds, value)
			}
		case token == "--after":
			if i+1 >= len(tokens) {
				return contextview.Options{}, false
			}
			i++
			n, err := strconv.Atoi(tokens[i])
			if err != nil {
				return contextview.Options{}, false
			}
			result.After = n
		case strings.HasPrefix(token, "--after="):
			n, err := strconv.Atoi(strings.TrimPrefix(token, "--after="))
			if err != nil {
				return contextview.Options{}, false
			}
			result.After = n
		case token == "--limit":
			if i+1 >= len(tokens) {
				return contextview.Options{}, false
			}
			i++
			n, err := strconv.Atoi(tokens[i])
			if err != nil {
				return contextview.Options{}, false
			}
			result.Limit = n
		case strings.HasPrefix(token, "--limit="):
			n, err := strconv.Atoi(strings.TrimPrefix(token, "--limit="))
			if err != nil {
				return contextview.Options{}, false
			}
			result.Limit = n
		case token == "--max-chars":
			if i+1 >= len(tokens) {
				return contextview.Options{}, false
			}
			i++
			n, err := strconv.Atoi(tokens[i])
			if err != nil {
				return contextview.Options{}, false
			}
			result.MaxChars = n
		case strings.HasPrefix(token, "--max-chars="):
			n, err := strconv.Atoi(strings.TrimPrefix(token, "--max-chars="))
			if err != nil {
				return contextview.Options{}, false
			}
			result.MaxChars = n
		default:
			return contextview.Options{}, false
		}
	}
	if result.Role != "" && !validContextRoles[result.Role] {
		return contextview.Options{}, false
	}
	seen := map[string]bool{}
	for _, sec := range raw {
		if !validContextSections[sec] {
			return contextview.Options{}, false
		}
		if seen[sec] {
			continue
		}
		seen[sec] = true
		result.Sections = append(result.Sections, sec)
	}
	return result, true
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

func parseProjectEnrollArgs(tokens []string) (project.EnrollArgs, bool) {
	var parsed project.EnrollArgs
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--host":
			if i+1 >= len(tokens) {
				return project.EnrollArgs{}, false
			}
			i++
			parsed.Host = tokens[i]
		case strings.HasPrefix(token, "--host="):
			parsed.Host = strings.TrimPrefix(token, "--host=")
		case token == "--remote":
			if i+1 >= len(tokens) {
				return project.EnrollArgs{}, false
			}
			i++
			parsed.Remote = tokens[i]
		case strings.HasPrefix(token, "--remote="):
			parsed.Remote = strings.TrimPrefix(token, "--remote=")
		case token == "--path":
			if i+1 >= len(tokens) {
				return project.EnrollArgs{}, false
			}
			i++
			parsed.Path = tokens[i]
		case strings.HasPrefix(token, "--path="):
			parsed.Path = strings.TrimPrefix(token, "--path=")
		case strings.HasPrefix(token, "-"):
			return project.EnrollArgs{}, false
		case parsed.Spec == "":
			parsed.Spec = token
		default:
			return project.EnrollArgs{}, false
		}
	}
	if parsed.Spec == "" {
		return project.EnrollArgs{}, false
	}
	return parsed, true
}

func parseProjectMigrateArgs(tokens []string) (name string, apply, ok bool) {
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--apply":
			apply = true
		case strings.HasPrefix(token, "-"):
			return "", false, false
		case name == "":
			name = token
		default:
			return "", false, false
		}
	}
	return name, apply, name != ""
}

func (o *rootOptions) runMetadata(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("command is required")
	}
	switch args[0] {
	case "snippet":
		raw := len(args) == 2 && args[1] == "--raw"
		if len(args) > 2 || (len(args) == 2 && !raw) {
			return usageError("metadata snippet", args[1:])
		}
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
	case "status":
		if len(args) != 1 {
			return usageError("metadata status", args[1:])
		}
		st, err := store.Open(o.home)
		if err != nil {
			return err
		}
		view := metadata.Summary(st)
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "enable", "disable", "sync", "inbox":
		st, err := o.openStore("metadata-" + args[0])
		if err != nil {
			return err
		}
		ctx, err := store.Context(o.installRoot)
		if err != nil {
			return err
		}
		notify := false
		for _, a := range args[1:] {
			if a == "--notify" {
				notify = true
			}
		}
		var view *ordjson.Object
		switch args[0] {
		case "enable":
			view, err = metadata.Enable(st, ctx, o.runtimeRoot, notify)
		case "disable":
			view, err = metadata.Disable(st, ctx)
		case "sync", "inbox":
			view, err = metadata.Sync(st, ctx, o.runtimeRoot)
		}
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	default:
		return usageError("metadata", args)
	}
}

func usageError(command string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("invalid %s arguments", command)
	}
	return fmt.Errorf("unrecognized arguments: %s", strings.Join(args, " "))
}

func (o *rootOptions) sumctlPath() string {
	if o.installRoot != "" {
		return filepath.Join(o.installRoot, "bin", "sumctl")
	}
	if o.reference != "" {
		return o.reference
	}
	return filepath.Join(o.runtimeRoot, "bin", "sumctl")
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
	return emitOrdjsonFormat(out, outputFormat, value)
}

var outputFormat = "toon"

func emitOrdjsonFormat(out io.Writer, format string, value any) error {
	var encoded []byte
	var err error
	if format == "json" {
		encoded, err = ordjson.MarshalIndent(value)
	} else {
		encoded, err = ordjson.MarshalTOON(value)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(encoded))
	return err
}

func peelFormat(args []string) (rest []string, format string, ok bool) {
	for i := 0; i < len(args); i++ {
		token := args[i]
		switch {
		case token == "--format":
			if i+1 >= len(args) {
				return nil, "", false
			}
			i++
			format = args[i]
		case strings.HasPrefix(token, "--format="):
			format = strings.TrimPrefix(token, "--format=")
		default:
			rest = append(rest, token)
		}
	}
	if format != "" && format != "json" && format != "toon" {
		return nil, "", false
	}
	return rest, format, true
}

func wrapOutputFormat(cmd *cobra.Command, opts *rootOptions) {
	if cmd.Name() != "quota" && cmd.RunE != nil {
		next := cmd.RunE
		cmd.RunE = func(c *cobra.Command, args []string) error {
			rest, format, ok := peelFormat(args)
			if !ok {
				return fmt.Errorf("invalid --format")
			}
			if format != "" {
				opts.format = format
			}
			outputFormat = opts.format
			return next(c, rest)
		}
	}
	for _, child := range cmd.Commands() {
		wrapOutputFormat(child, opts)
	}
}
