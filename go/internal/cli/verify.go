package cli

import (
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/verifycmd"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addVerifyCommand(root *cobra.Command) {
	var candidate, result, run, base, text, file string
	var execute bool
	cmd := &cobra.Command{
		Use:  "verify TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if candidate == "" {
				return usageError("verify", args)
			}
			st, err := o.openStore("verify")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := verifycmd.Run(st, ctx, verifycmd.Args{
				Task:        args[0],
				Candidate:   candidate,
				Result:      result,
				Run:         run,
				Execute:     execute,
				Base:        base,
				Text:        text,
				File:        file,
				RuntimeRoot: o.runtimeRoot,
			})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	cmd.Flags().StringVar(&candidate, "candidate", "", "")
	cmd.Flags().StringVar(&result, "result", "", "")
	cmd.Flags().StringVar(&run, "run", "", "")
	cmd.Flags().BoolVar(&execute, "execute", false, "")
	cmd.Flags().StringVar(&base, "base", "", "")
	cmd.Flags().StringVar(&text, "text", "", "")
	cmd.Flags().StringVar(&file, "file", "", "")
	root.AddCommand(cmd)
}
