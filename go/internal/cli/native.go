package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/archive"
	"github.com/douglasjarquin/sum/go/internal/ask"
	"github.com/douglasjarquin/sum/go/internal/attention"
	"github.com/douglasjarquin/sum/go/internal/backup"
	"github.com/douglasjarquin/sum/go/internal/bindcmd"
	"github.com/douglasjarquin/sum/go/internal/devcmd"
	"github.com/douglasjarquin/sum/go/internal/guard"
	"github.com/douglasjarquin/sum/go/internal/helpview"
	"github.com/douglasjarquin/sum/go/internal/herdrbridge"
	"github.com/douglasjarquin/sum/go/internal/notes"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/quota"
	"github.com/douglasjarquin/sum/go/internal/report"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/skills"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func (o *rootOptions) openStore(command string) (*store.Store, error) {
	st, err := store.Open(o.home)
	if err != nil {
		return nil, err
	}
	if err := guard.Candidate(o.installRoot, st, command); err != nil {
		return nil, err
	}
	return st, nil
}

func (o *rootOptions) addNativeCommands(root *cobra.Command) {
	root.AddCommand(&cobra.Command{
		Use:  "help [TOPIC]",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := o.openStore("help"); err != nil {
				return err
			}
			topic := ""
			if len(args) == 1 {
				topic = args[0]
			}
			view, err := helpview.View(o.runtimeRoot, topic)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	var quotaProvider, quotaFormat string
	quotaCmd := &cobra.Command{
		Use:  "quota",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if quotaFormat != "" && quotaFormat != "json" && quotaFormat != "compact" && quotaFormat != "toon" {
				return usageError("quota", []string{"--format", quotaFormat})
			}
			if _, err := o.openStore("quota"); err != nil {
				return err
			}
			code, err := quota.Run(o.runtimeRoot, quotaProvider, quotaFormat, cmd.OutOrStdout(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if code != 0 {
				return &ExitError{Code: code}
			}
			return nil
		},
	}
	quotaCmd.Flags().StringVar(&quotaProvider, "provider", "", "")
	quotaCmd.Flags().StringVar(&quotaFormat, "format", "", "")
	_ = quotaCmd.MarkFlagRequired("provider")
	root.AddCommand(quotaCmd)

	skillsCmd := &cobra.Command{
		Use:                "skills",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}
	var skillsRoot string
	skillsCheck := &cobra.Command{
		Use:  "check",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := o.openStore("skills-check"); err != nil {
				return err
			}
			view, err := skills.Check(skillsRoot)
			if err != nil {
				return err
			}
			if err := emitOrdjson(cmd.OutOrStdout(), view); err != nil {
				return err
			}
			if okValue, _ := view.Get("ok"); okValue != true {
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
	skillsCheck.Flags().StringVar(&skillsRoot, "root", o.runtimeRoot, "")
	skillsCmd.AddCommand(skillsCheck)

	var skillTarget, skillSource string
	var skillNames, agentNames []string
	skillsInstall := &cobra.Command{
		Use:  "install",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := o.openStore("skills-install"); err != nil {
				return err
			}
			view, err := skills.Install(o.runtimeRoot, skillTarget, skillSource, skillNames, agentNames)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	skillsInstall.Flags().StringVar(&skillTarget, "target", "", "")
	skillsInstall.Flags().StringVar(&skillSource, "source", "", "")
	skillsInstall.Flags().StringArrayVar(&skillNames, "skill", nil, "")
	skillsInstall.Flags().StringArrayVar(&agentNames, "agent", nil, "")
	_ = skillsInstall.MarkFlagRequired("target")
	_ = skillsInstall.MarkFlagRequired("source")
	_ = skillsInstall.MarkFlagRequired("skill")
	_ = skillsInstall.MarkFlagRequired("agent")
	skillsCmd.AddCommand(skillsInstall)
	root.AddCommand(skillsCmd)

	var notesText, notesFile string
	notesCmd := &cobra.Command{
		Use:  "notes TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("notes")
			if err != nil {
				return err
			}
			body, err := app.TextInput(notesText, notesFile)
			if err != nil {
				return err
			}
			view, err := notes.Add(st, args[0], body, app.OptionalContext(o.installRoot))
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	notesCmd.Flags().StringVar(&notesText, "text", "", "")
	notesCmd.Flags().StringVar(&notesFile, "file", "", "")
	notesCmd.MarkFlagsOneRequired("text", "file")
	notesCmd.MarkFlagsMutuallyExclusive("text", "file")
	root.AddCommand(notesCmd)

	root.AddCommand(&cobra.Command{
		Use:  "herdr",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("herdr")
			if err != nil {
				return err
			}
			ctx, ctxErr := store.Context(o.installRoot)
			if ctxErr != nil {
				return fmt.Errorf("%s The bridge never borrows a saved coordinator context.", capitalizeHerdrContext(ctxErr.Error()))
			}
			return herdrbridge.Run(o.runtimeRoot, st, ctx, args, cmd.OutOrStdout())
		},
	})

	var acknowledge bool
	archiveCmd := &cobra.Command{
		Use:  "archive TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("archive")
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
			view, err := archive.Run(st, args[0], acknowledge)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	archiveCmd.Flags().BoolVar(&acknowledge, "acknowledge", false, "")
	root.AddCommand(archiveCmd)

	var askKey, askText, askFile string
	askCmd := &cobra.Command{
		Use:  "ask TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("ask")
			if err != nil {
				return err
			}
			body, err := app.TextInput(askText, askFile)
			if err != nil {
				return err
			}
			view, err := ask.Ask(st, args[0], askKey, body, func() (*ordjson.Object, error) {
				return returns.Notify(st, o.pumpOpts(), args[0], "parent", "a decision is waiting", false)
			})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	askCmd.Flags().StringVar(&askKey, "key", "", "")
	askCmd.Flags().StringVar(&askText, "text", "", "")
	askCmd.Flags().StringVar(&askFile, "file", "", "")
	askCmd.MarkFlagsOneRequired("text", "file")
	askCmd.MarkFlagsMutuallyExclusive("text", "file")
	root.AddCommand(askCmd)

	var answerText, answerFile string
	answerCmd := &cobra.Command{
		Use:  "answer TASK QUESTION",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("answer")
			if err != nil {
				return err
			}
			body, err := app.TextInput(answerText, answerFile)
			if err != nil {
				return err
			}
			view, err := ask.Answer(st, args[0], args[1], body, app.OptionalContext(o.installRoot), func() (*ordjson.Object, error) {
				return returns.Notify(st, o.pumpOpts(), args[0], "worker", "an answer has been recorded", false)
			})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	answerCmd.Flags().StringVar(&answerText, "text", "", "")
	answerCmd.Flags().StringVar(&answerFile, "file", "", "")
	answerCmd.MarkFlagsOneRequired("text", "file")
	answerCmd.MarkFlagsMutuallyExclusive("text", "file")
	root.AddCommand(answerCmd)

	root.AddCommand(&cobra.Command{
		Use:  "resolve TASK QUESTION",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("resolve")
			if err != nil {
				return err
			}
			view, err := ask.Resolve(st, args[0], args[1])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	var noticeTo string
	noticeCmd := &cobra.Command{
		Use:  "notice TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if noticeTo != "parent" && noticeTo != "worker" {
				return usageError("notice", []string{"--to", noticeTo})
			}
			st, err := o.openStore("notice")
			if err != nil {
				return err
			}
			view, err := returns.Notify(st, o.pumpOpts(), args[0], noticeTo, "saved task state needs attention", true)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	noticeCmd.Flags().StringVar(&noticeTo, "to", "parent", "")
	root.AddCommand(noticeCmd)

	var pumpTasks []string
	var pumpForce bool
	pumpCmd := &cobra.Command{
		Use:  "pump",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("pump")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			reg, err := st.Registration(store.EndpointFromContext(ctx))
			if err != nil {
				return err
			}
			if reg == nil {
				return fmt.Errorf("Run `sumctl init` in this pane first; the pump delivers only for a pane registered in this instance.")
			}
			opts := o.pumpOpts()
			opts.Ctx = ctx
			opts.Tasks = pumpTasks
			opts.Force = pumpForce
			opts.Inline = true
			view, err := returns.Pump(st, opts)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	pumpCmd.Flags().StringArrayVar(&pumpTasks, "task", nil, "")
	pumpCmd.Flags().BoolVar(&pumpForce, "force", false, "")
	root.AddCommand(pumpCmd)

	root.AddCommand(&cobra.Command{
		Use:  "backup DESTINATION",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("backup")
			if err != nil {
				return err
			}
			view, err := backup.Run(st, args[0])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})

	var seen bool
	attentionCmd := &cobra.Command{
		Use:  "attention TASK ATTENTION",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !seen {
				return usageError("attention", []string{"--seen"})
			}
			st, err := o.openStore("attention")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := attention.Seen(st, ctx, args[0], args[1])
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	attentionCmd.Flags().BoolVar(&seen, "seen", false, "")
	_ = attentionCmd.MarkFlagRequired("seen")
	root.AddCommand(attentionCmd)

	var workerPane string
	var parentOnly bool
	bindCmd := &cobra.Command{
		Use:  "bind TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("bind")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := bindcmd.Run(st, ctx, args[0], workerPane, parentOnly, o.pumpOpts())
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	bindCmd.Flags().StringVar(&workerPane, "worker-pane", "", "")
	bindCmd.Flags().BoolVar(&parentOnly, "parent-only", false, "")
	root.AddCommand(bindCmd)

	var reportText, reportFile, handoffPath string
	reportCmd := &cobra.Command{
		Use:  "report TASK",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("report")
			if err != nil {
				return err
			}
			body, err := app.TextInput(reportText, reportFile)
			if err != nil {
				return err
			}
			var handoff *ordjson.Object
			if handoffPath != "" {
				raw, readErr := os.ReadFile(handoffPath)
				if readErr != nil {
					return readErr
				}
				decoded, decErr := ordjson.Decode(raw)
				if decErr != nil {
					return decErr
				}
				obj, isObj := decoded.(*ordjson.Object)
				if !isObj {
					return fmt.Errorf("handoff file is not a JSON object")
				}
				handoff = obj
			}
			view, err := report.Run(st, args[0], body, handoff, app.OptionalContext(o.installRoot), o.pumpOpts())
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	reportCmd.Flags().StringVar(&reportText, "text", "", "")
	reportCmd.Flags().StringVar(&reportFile, "file", "", "")
	reportCmd.Flags().StringVar(&handoffPath, "handoff", "", "")
	reportCmd.MarkFlagsOneRequired("text", "file")
	root.AddCommand(reportCmd)

	devCmd := &cobra.Command{
		Use:                "dev",
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("command is required")
		},
	}
	devCmd.AddCommand(&cobra.Command{
		Use:  "list",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("dev-list")
			if err != nil {
				return err
			}
			view, err := devcmd.List(st)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	})
	var prepareName, prepareBase string
	var preparePane bool
	devPrepare := &cobra.Command{
		Use:  "prepare",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("dev-prepare")
			if err != nil {
				return err
			}
			view, err := devcmd.Prepare(st, app.OptionalContext(o.installRoot), o.runtimeRoot, prepareName, prepareBase, preparePane)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	devPrepare.Flags().StringVar(&prepareName, "name", "", "")
	devPrepare.Flags().StringVar(&prepareBase, "base", "HEAD", "")
	devPrepare.Flags().BoolVar(&preparePane, "pane", false, "")
	_ = devPrepare.MarkFlagRequired("name")
	devCmd.AddCommand(devPrepare)

	var removeName string
	devRemove := &cobra.Command{
		Use:  "remove",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := o.openStore("dev-remove")
			if err != nil {
				return err
			}
			view, err := devcmd.Remove(st, removeName)
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	devRemove.Flags().StringVar(&removeName, "name", "", "")
	_ = devRemove.MarkFlagRequired("name")
	devCmd.AddCommand(devRemove)
	root.AddCommand(devCmd)
}

func (o *rootOptions) pumpOpts() returns.PumpOpts {
	return returns.PumpOpts{
		RuntimeRoot: o.runtimeRoot,
		SumctlPath:  o.sumctlPath(),
		Ctx:         app.OptionalContext(o.installRoot),
		Inline:      true,
	}
}

func capitalizeHerdrContext(msg string) string {
	if strings.HasPrefix(msg, "run this command") {
		return "Run" + msg[3:]
	}
	return msg
}
