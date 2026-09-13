package cli

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addRepairCommands(root *cobra.Command) {
	repairCmd := &cobra.Command{
		Use:                "repair",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}

	var sendAttempt, sendKey, sendText, sendFile string
	sendCmd := &cobra.Command{
		Use:  "send TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("repair-send")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			body, err := app.TextInput(sendText, sendFile)
			if err != nil {
				return err
			}
			view, err := repair.Send(st, ctx, repair.SendArgs{
				TaskID:      args[0],
				Attempt:     sendAttempt,
				Key:         sendKey,
				Text:        body,
				RuntimeRoot: o.runtimeRoot,
			})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	sendCmd.Flags().StringVar(&sendAttempt, "attempt", "", "")
	sendCmd.Flags().StringVar(&sendKey, "key", "", "")
	sendCmd.Flags().StringVar(&sendText, "text", "", "")
	sendCmd.Flags().StringVar(&sendFile, "file", "", "")
	_ = sendCmd.MarkFlagRequired("attempt")
	_ = sendCmd.MarkFlagRequired("key")
	sendCmd.MarkFlagsOneRequired("text", "file")
	sendCmd.MarkFlagsMutuallyExclusive("text", "file")
	repairCmd.AddCommand(sendCmd)

	var question, extendText, extendFile string
	var additional int
	var approved bool
	extendCmd := &cobra.Command{
		Use:  "extend TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("repair-extend")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			body, err := app.TextInput(extendText, extendFile)
			if err != nil {
				return err
			}
			view, err := repair.Extend(st, ctx, repair.ExtendArgs{
				TaskID:     args[0],
				Question:   question,
				Additional: additional,
				Approved:   approved,
				Text:       body,
			})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	extendCmd.Flags().StringVar(&question, "question", "", "")
	extendCmd.Flags().IntVar(&additional, "additional", 0, "")
	extendCmd.Flags().BoolVar(&approved, "approved", false, "")
	extendCmd.Flags().StringVar(&extendText, "text", "", "")
	extendCmd.Flags().StringVar(&extendFile, "file", "", "")
	_ = extendCmd.MarkFlagRequired("question")
	_ = extendCmd.MarkFlagRequired("additional")
	extendCmd.MarkFlagsOneRequired("text", "file")
	extendCmd.MarkFlagsMutuallyExclusive("text", "file")
	repairCmd.AddCommand(extendCmd)

	root.AddCommand(repairCmd)
}
