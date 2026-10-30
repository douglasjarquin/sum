package cli

import (
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/roleinit"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addCoordinatorCommand(root *cobra.Command) {
	var options roleinit.CoordinatorStartOpts
	group := &cobra.Command{Use: "coordinator"}
	command := &cobra.Command{Use: "start", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		s, err := o.openStore("coordinator start")
		if err != nil {
			return err
		}
		options.Launch.PresetSet = cmd.Flags().Changed("preset")
		options.RuntimeRoot = o.runtimeRoot
		options.SumctlPath = o.sumctlPath()
		view, err := roleinit.StartCoordinator(s, options)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	}}
	command.Flags().StringVar(&options.Socket, "socket", "", "")
	command.Flags().StringVar(&options.Session, "session", "", "")
	command.Flags().StringVar(&options.CoreConfigHome, "hc-config-home", "", "")
	command.Flags().StringVar(&options.Launch.Harness, "harness", "codex", "")
	command.Flags().StringVar(&options.Launch.Model, "model", "", "")
	command.Flags().StringVar(&options.Launch.Reasoning, "reasoning", "", "")
	command.Flags().StringVar(&options.Launch.Preset, "preset", "", "")
	command.Flags().StringArrayVar(&options.Launch.Extra, "arg", nil, "")
	command.Flags().BoolVar(&options.Launch.PermissionOverride, launch.PermissionOverrideFlag[2:], false, "")
	group.AddCommand(command)
	root.AddCommand(group)
}
