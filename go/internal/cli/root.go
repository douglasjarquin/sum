package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/helpview"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	sumruntime "github.com/douglasjarquin/sum/go/internal/runtime"
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

	opts.addSettingsCommands(root)
	opts.addPresetCommands(root)
	opts.addGraphCommands(root)
	opts.addMetadataCommands(root)

	opts.addStatusCommands(root)
	opts.addBriefCommands(root)
	opts.addEnvCommands(root)
	opts.addReleaseCommands(root)
	opts.addProjectCommands(root)
	opts.addHookCommands(root)

	opts.addShowCommand(root)
	opts.addContextCommand(root)
	opts.addDoctorCommand(root)
	opts.addInitCommand(root)
	opts.addNativeCommands(root)
	opts.format = "toon"
	wrapOutputFormat(root, opts)
	return root
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
