package cli

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/app"
	findings "github.com/douglasjarquin/sum/go/internal/notes"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) addFindingCommands(root *cobra.Command) {
	cmd := &cobra.Command{
		Use:                "finding",
		Short:              "Record and follow optional findings",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("finding action is required")
		},
	}

	var in findings.Input
	record := &cobra.Command{
		Use:         "record TASK",
		Annotations: map[string]string{projectsAnnotation: "task"},
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("finding-record")
			if err != nil {
				return err
			}
			value, duplicate, err := findings.Capture(st, args[0], in, app.OptionalContext(o.installRoot))
			if err != nil {
				return err
			}
			findingObject, err := ordjson.FromValue(value)
			if err != nil {
				return err
			}
			result := ordjson.NewObject()
			result.Set("finding", findingObject)
			result.Set("duplicate", duplicate)
			return emitOrdjson(cmd.OutOrStdout(), result)
		},
	}
	record.Flags().StringVar(&in.Key, "key", "", "")
	record.Flags().StringVar(&in.Title, "title", "", "")
	record.Flags().StringVar(&in.Explanation, "explanation", "", "")
	record.Flags().StringVar(&in.NextStep, "next-step", "", "")
	record.Flags().StringArrayVar(&in.References, "reference", nil, "")
	record.Flags().StringVar(&in.Attempt, "attempt", "", "")
	record.Flags().StringVar(&in.Candidate, "candidate", "", "")
	for _, name := range []string{"key", "title", "explanation", "next-step"} {
		_ = record.MarkFlagRequired(name)
	}
	cmd.AddCommand(record)

	list := &cobra.Command{
		Use:  "list TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			records, gaps := findings.ReadTask(st, args[0])
			result := ordjson.NewObject()
			result.Set("task", args[0])
			findingRows := make([]any, 0, len(records))
			for _, record := range records {
				row, err := ordjson.FromValue(record)
				if err != nil {
					return err
				}
				findingRows = append(findingRows, row)
			}
			gapRows := make([]any, 0, len(gaps))
			for _, gap := range gaps {
				row, err := ordjson.FromValue(gap)
				if err != nil {
					return err
				}
				gapRows = append(gapRows, row)
			}
			result.Set("findings", findingRows)
			result.Set("gaps", gapRows)
			return emitOrdjson(cmd.OutOrStdout(), result)
		},
	}
	cmd.AddCommand(list)

	show := &cobra.Command{
		Use:  "show ID",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(o.home)
			if err != nil {
				return err
			}
			value, err := findings.Read(st, args[0])
			if err != nil {
				return err
			}
			row, err := ordjson.FromValue(value)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), row)
		},
	}
	cmd.AddCommand(show)

	addChange := func(action string) {
		var reason, target, expectedRevision, expectedState string
		use := action + " ID"
		args := cobra.ExactArgs(1)
		if action == "promote" || action == "attach" {
			use += " TASK"
			args = cobra.ExactArgs(2)
		}
		actionCmd := &cobra.Command{
			Use:         use,
			Annotations: map[string]string{projectsAnnotation: "finding"},
			Args:        args,
			RunE: func(cmd *cobra.Command, args []string) error {
				st, err := o.openStore("finding-" + action)
				if err != nil {
					return err
				}
				if action == "promote" || action == "attach" {
					target = args[1]
				}
				value, err := findings.ChangeExpected(st, args[0], action, reason, target, expectedRevision, expectedState, app.OptionalContext(o.installRoot))
				if err != nil {
					return err
				}
				row, err := ordjson.FromValue(value)
				if err != nil {
					return err
				}
				return emitOrdjson(cmd.OutOrStdout(), row)
			},
		}
		actionCmd.Flags().StringVar(&reason, "reason", "", "")
		actionCmd.Flags().StringVar(&expectedRevision, "expected-revision", "", "")
		actionCmd.Flags().StringVar(&expectedState, "expected-state", "", "")
		if action != "promote" && action != "attach" {
			_ = actionCmd.MarkFlagRequired("reason")
		}
		if action == "promote" || action == "attach" {
			_ = actionCmd.MarkFlagRequired("reason")
		}
		cmd.AddCommand(actionCmd)
	}
	for _, action := range []string{"defer", "dismiss", "reopen", "resolve", "attach", "promote", "settle"} {
		addChange(action)
	}
	root.AddCommand(cmd)
}
