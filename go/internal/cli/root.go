package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

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
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetVersionTemplate("{{.Version}}\n")
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().StringVar(&opts.home, "home", "", "state home")
	root.PersistentFlags().Lookup("home").NoOptDefVal = ""
	root.PersistentPreRun = func(cmd *cobra.Command, _ []string) {
		opts.homeSet = cmd.Flags().Changed("home") || cmd.InheritedFlags().Changed("home")
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
	"doctor", "init", "status", "inbox", "prepare", "dispatch", "start", "help", "context", "notes", "env", "show", "notice", "archive", "ask", "answer", "report", "resolve", "review", "verify", "pr", "cleanup", "pump", "hook", "metadata", "attention", "bind", "backup", "settings", "preset", "project", "herdr", "graph", "dev", "brief", "refresh", "release", "update",
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

func normalizeHome(args []string, home string, homeSet bool) []string {
	var prefix []string
	var body []string
	seenHome := false
	afterSeparator := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			afterSeparator = true
			body = append(body, arg)
			continue
		}
		if !afterSeparator && arg == "--home" && index+1 < len(args) {
			prefix = append(prefix, arg, args[index+1])
			seenHome = true
			index++
			continue
		}
		if !afterSeparator && strings.HasPrefix(arg, "--home=") {
			prefix = append(prefix, arg)
			seenHome = true
			continue
		}
		body = append(body, arg)
	}
	if homeSet && !seenHome {
		prefix = append([]string{"--home", home}, prefix...)
	}
	return append(prefix, body...)
}
