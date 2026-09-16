package cli

import (
	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/review"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addReviewCommand(root *cobra.Command) {
	var verdict, candidate, toolName, text, file, runPath string
	var policyReviewed bool
	cmd := &cobra.Command{
		Use:  "review TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("review")
			if err != nil {
				return err
			}
			body := text
			if file != "" || (text == "" && runPath == "") {
				body, err = app.TextInput(text, file)
				if err != nil {
					return err
				}
			}
			view, err := review.Run(st, args[0], verdict, candidate, toolName, body, runPath, policyReviewed, app.OptionalContext(o.installRoot))
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	cmd.Flags().StringVar(&verdict, "verdict", "", "")
	cmd.Flags().StringVar(&candidate, "candidate", "", "")
	cmd.Flags().StringVar(&toolName, "tool", "", "")
	cmd.Flags().BoolVar(&policyReviewed, "policy-reviewed", false, "")
	cmd.Flags().StringVar(&text, "text", "", "")
	cmd.Flags().StringVar(&file, "file", "", "")
	cmd.Flags().StringVar(&runPath, "run", "", "")
	_ = cmd.MarkFlagRequired("verdict")
	cmd.MarkFlagsOneRequired("text", "file", "run")
	cmd.MarkFlagsMutuallyExclusive("text", "file")
	root.AddCommand(cmd)
}
