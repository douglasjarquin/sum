package cli

import (
	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/review"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addReviewCommand(root *cobra.Command) {
	var verdict, candidate, toolName, text, file, runPath, notesPath string
	var policyReviewed bool
	var focus, findings, limitations []string
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
			var notes []review.Note
			if notesPath != "" {
				notes, err = review.ParseNotesFile(notesPath)
				if err != nil {
					return err
				}
			}
			flagNotes, err := review.NotesFromFlags(focus, findings, limitations)
			if err != nil {
				return err
			}
			notes = append(notes, flagNotes...)
			view, err := review.Run(st, review.Args{
				Task:           args[0],
				Verdict:        verdict,
				Candidate:      candidate,
				Tool:           toolName,
				Text:           body,
				RunPath:        runPath,
				PolicyReviewed: policyReviewed,
				Notes:          notes,
			}, app.OptionalContext(o.installRoot))
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
	cmd.Flags().StringVar(&notesPath, "notes", "", "")
	cmd.Flags().StringArrayVar(&focus, "focus", nil, "")
	cmd.Flags().StringArrayVar(&findings, "finding", nil, "")
	cmd.Flags().StringArrayVar(&limitations, "limitation", nil, "")
	_ = cmd.MarkFlagRequired("verdict")
	cmd.MarkFlagsOneRequired("text", "file", "run")
	cmd.MarkFlagsMutuallyExclusive("text", "file")
	root.AddCommand(cmd)
}
