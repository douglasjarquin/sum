package cli

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addPresetCommands(root *cobra.Command) {
	presetCmd := &cobra.Command{
		Use:                "preset",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	presetCmd.AddCommand(&cobra.Command{
		Use:  "list",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view, err := settings.PresetList(st)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	presetCmd.AddCommand(&cobra.Command{
		Use:  "show NAME",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			view, err := settings.PresetShow(st, args[0])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	var harness, model, reasoning string
	var argValues []string
	var clearModel, clearReasoning, clearArgs bool
	setCmd := &cobra.Command{
		Use:  "set NAME",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			parsed := settings.PresetWriteArgs{
				Name:      args[0],
				Harness:   harness,
				Model:     model,
				Reasoning: reasoning,
				Args:      argValues,
				ArgsSet:   cmd.Flags().Changed("arg"),
			}
			if clearModel {
				parsed.Clear = append(parsed.Clear, "model")
			}
			if clearReasoning {
				parsed.Clear = append(parsed.Clear, "reasoning")
			}
			if clearArgs {
				parsed.Clear = append(parsed.Clear, "args")
			}
			return o.runPresetSet(cmd, parsed)
		},
	}
	setCmd.Flags().StringVar(&harness, "harness", "", "")
	setCmd.Flags().StringVar(&model, "model", "", "")
	setCmd.Flags().StringVar(&reasoning, "reasoning", "", "")
	setCmd.Flags().StringArrayVar(&argValues, "arg", nil, "")
	setCmd.Flags().BoolVar(&clearModel, "clear-model", false, "")
	setCmd.Flags().BoolVar(&clearReasoning, "clear-reasoning", false, "")
	setCmd.Flags().BoolVar(&clearArgs, "clear-args", false, "")
	presetCmd.AddCommand(setCmd)

	presetCmd.AddCommand(&cobra.Command{
		Use:  "delete NAME",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.runPresetDelete(cmd, args[0])
		},
	})

	root.AddCommand(presetCmd)
}

func (o *rootOptions) runPresetSet(cmd *cobra.Command, parsed settings.PresetWriteArgs) error {
	st, err := o.openStore("preset-set")
	if err != nil {
		return err
	}
	ctx, err := store.Context(o.installRoot)
	if err != nil {
		return err
	}
	if err := app.RequireCoordinator(st, ctx); err != nil {
		return err
	}
	clear := map[string]bool{}
	for _, c := range parsed.Clear {
		clear[c] = true
	}
	if (clear["model"] && parsed.Model != "") || (clear["reasoning"] && parsed.Reasoning != "") || (clear["args"] && parsed.ArgsSet) {
		return fmt.Errorf("--clear-* conflicts with a value for the same field.")
	}
	view, err := settings.WritePreset(st, parsed)
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func (o *rootOptions) runPresetDelete(cmd *cobra.Command, name string) error {
	st, err := o.openStore("preset-delete")
	if err != nil {
		return err
	}
	ctx, err := store.Context(o.installRoot)
	if err != nil {
		return err
	}
	if err := app.RequireCoordinator(st, ctx); err != nil {
		return err
	}
	view, err := settings.DeletePreset(st, name)
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}
