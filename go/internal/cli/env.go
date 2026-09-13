package cli

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addEnvCommands(root *cobra.Command) {
	envCmd := &cobra.Command{
		Use:                "env",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	var maxChars int
	showCmd := &cobra.Command{
		Use:  "show TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("env-show")
			if err != nil {
				return err
			}
			view, err := environment.Show(st, args[0], o.sumctlPath(), maxChars)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	showCmd.Flags().IntVar(&maxChars, "max-chars", environment.DefaultMaxChars, "")
	envCmd.AddCommand(showCmd)

	envCmd.AddCommand(&cobra.Command{
		Use:  "discover TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("env-discover")
			if err != nil {
				return err
			}
			view, err := environment.Discover(st, args[0], app.OptionalContext(o.installRoot), o.sumctlPath())
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	envCmd.AddCommand(&cobra.Command{
		Use:  "inspect TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("env-inspect")
			if err != nil {
				return err
			}
			view, err := environment.Inspect(st, environment.InspectArgs{Task: args[0], RuntimeRoot: o.runtimeRoot}, app.OptionalContext(o.installRoot))
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	var url, log, pane, container, label, ownership string
	recordCmd := &cobra.Command{
		Use:  "record TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("env-record")
			if err != nil {
				return err
			}
			view, err := environment.Record(st, environment.RecordArgs{
				Task:        args[0],
				URL:         url,
				Log:         log,
				Pane:        pane,
				Container:   container,
				Label:       label,
				Ownership:   ownership,
				RuntimeRoot: o.runtimeRoot,
			}, app.OptionalContext(o.installRoot))
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	recordCmd.Flags().StringVar(&url, "url", "", "")
	recordCmd.Flags().StringVar(&log, "log", "", "")
	recordCmd.Flags().StringVar(&pane, "pane", "", "")
	recordCmd.Flags().StringVar(&container, "container", "", "")
	recordCmd.Flags().StringVar(&label, "label", "", "")
	recordCmd.Flags().StringVar(&ownership, "ownership", "", "")
	envCmd.AddCommand(recordCmd)

	var startCommand, startSource, startURL, startMatch, startLog, startLabel string
	var startTimeout int
	startCmd := &cobra.Command{
		Use:  "start TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("env-start")
			if err != nil {
				return err
			}
			view, err := environment.Start(st, environment.StartArgs{
				Task:        args[0],
				Command:     startCommand,
				Source:      startSource,
				URL:         startURL,
				Match:       startMatch,
				Log:         startLog,
				Label:       startLabel,
				Timeout:     startTimeout,
				TimeoutSet:  cmd.Flags().Changed("timeout"),
				RuntimeRoot: o.runtimeRoot,
			}, app.OptionalContext(o.installRoot))
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	startCmd.Flags().StringVar(&startCommand, "command", "", "")
	startCmd.Flags().StringVar(&startSource, "source", "", "")
	startCmd.Flags().StringVar(&startURL, "url", "", "")
	startCmd.Flags().StringVar(&startMatch, "match", "", "")
	startCmd.Flags().StringVar(&startLog, "log", "", "")
	startCmd.Flags().StringVar(&startLabel, "label", "", "")
	startCmd.Flags().IntVar(&startTimeout, "timeout", 0, "")
	_ = startCmd.MarkFlagRequired("command")
	envCmd.AddCommand(startCmd)

	var service string
	var timeout int
	stopCmd := &cobra.Command{
		Use:  "stop TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("env-stop")
			if err != nil {
				return err
			}
			view, err := environment.Stop(st, environment.StopArgs{
				Task:        args[0],
				Service:     service,
				Timeout:     timeout,
				TimeoutSet:  cmd.Flags().Changed("timeout"),
				RuntimeRoot: o.runtimeRoot,
			})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	stopCmd.Flags().StringVar(&service, "service", "", "")
	stopCmd.Flags().IntVar(&timeout, "timeout", 0, "")
	envCmd.AddCommand(stopCmd)

	root.AddCommand(envCmd)
}
