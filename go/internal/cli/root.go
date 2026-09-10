package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/settings"
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
	"doctor", "init", "status", "inbox", "prepare", "dispatch", "start", "help", "context", "notes", "env", "show", "notice", "archive", "ask", "answer", "report", "resolve", "review", "verify", "pr", "cleanup", "pump", "hook", "metadata", "attention", "bind", "backup", "project", "herdr", "graph", "dev", "brief", "refresh", "release", "update",
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
