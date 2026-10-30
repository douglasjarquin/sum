package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/archive"
	"github.com/douglasjarquin/sum/go/internal/ask"
	"github.com/douglasjarquin/sum/go/internal/attention"
	"github.com/douglasjarquin/sum/go/internal/backup"
	"github.com/douglasjarquin/sum/go/internal/bindcmd"
	"github.com/douglasjarquin/sum/go/internal/devcmd"
	"github.com/douglasjarquin/sum/go/internal/guard"
	"github.com/douglasjarquin/sum/go/internal/helpview"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/lifecycle"
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

func observeExpectedWorker(st *store.Store, runtimeRoot, taskID, expectedRecipient string) (ask.RecipientCheck, error) {
	task, err := st.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	currentRecipient, err := ask.ExpectedWorkerRecipient(st, task)
	if err != nil {
		return nil, err
	}
	if currentRecipient == "" || currentRecipient != expectedRecipient {
		return nil, fmt.Errorf("The worker recipient changed since this action was presented. Refresh the inbox and choose again.")
	}
	route := returns.ReturnRoute(task, "worker")
	cwd, _ := route.Get("cwd")
	expectedCwd, _ := cwd.(string)
	occupant, err := returns.ObserveWorker(st, runtimeRoot, task, route, expectedCwd)
	if err != nil {
		return nil, err
	}
	return func(s *store.Store, currentTask *ordjson.Object) error {
		if message := occupant.Check(s, returns.ReturnRoute(currentTask, "worker")); message != "" {
			return fmt.Errorf("%s", message)
		}
		return nil
	}, nil
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

	var acknowledge bool
	archiveCmd := &cobra.Command{
		Use:         "archive TASK",
		Annotations: map[string]string{projectsAnnotation: "task"},
		Args:        cobra.ExactArgs(1),
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

	var askKey, askText, askFile, askRecommended, askRationale string
	var askActionWhy, askActionExpected, askActionReport, askActionCheck string
	var askChoices, askSources, askActionSteps, askActionSources []string
	askCmd := &cobra.Command{
		Use:         "ask TASK",
		Annotations: map[string]string{projectsAnnotation: "task"},
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("ask")
			if err != nil {
				return err
			}
			body, err := app.TextInput(askText, askFile)
			if err != nil {
				return err
			}
			var decision *ask.StructuredDecision
			if len(askChoices)+len(askSources)+len(askActionSteps)+len(askActionSources) > 0 || askRecommended != "" || askRationale != "" || askActionWhy != "" || askActionExpected != "" || askActionReport != "" || askActionCheck != "" {
				decision = &ask.StructuredDecision{Recommended: askRecommended, Rationale: askRationale, Sources: askSources}
				for _, value := range askChoices {
					id, label, ok := strings.Cut(value, "=")
					if !ok {
						return fmt.Errorf("--choice must use ID=LABEL form.")
					}
					decision.Choices = append(decision.Choices, ask.Choice{ID: id, Label: label})
				}
				if askActionWhy != "" || askActionExpected != "" || askActionReport != "" || askActionCheck != "" || len(askActionSteps)+len(askActionSources) > 0 {
					action := &ask.HumanAction{Why: askActionWhy, Expected: askActionExpected, Report: askActionReport, Check: askActionCheck}
					sources := map[int]string{}
					for _, value := range askActionSources {
						indexText, source, ok := strings.Cut(value, "=")
						index, parseErr := strconv.Atoi(indexText)
						if !ok || parseErr != nil || index < 1 || index > len(askActionSteps) || source == "" || sources[index] != "" {
							return fmt.Errorf("--action-source must use STEP=REFERENCE once for a matching 1-based step.")
						}
						sources[index] = source
					}
					for i, value := range askActionSteps {
						kind, content, ok := strings.Cut(value, "=")
						if !ok {
							return fmt.Errorf("--action-step must use TYPE=CONTENT form.")
						}
						action.Steps = append(action.Steps, ask.HumanActionStep{Kind: kind, Content: content, Source: sources[i+1]})
					}
					decision.HumanAction = action
				}
			}
			view, err := ask.AskStructured(st, args[0], askKey, body, decision, func() (*ordjson.Object, error) {
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
	askCmd.Flags().StringArrayVar(&askChoices, "choice", nil, "")
	askCmd.Flags().StringVar(&askRecommended, "recommended", "", "")
	askCmd.Flags().StringVar(&askRationale, "rationale", "", "")
	askCmd.Flags().StringArrayVar(&askSources, "source", nil, "")
	askCmd.Flags().StringVar(&askActionWhy, "action-why", "", "")
	askCmd.Flags().StringArrayVar(&askActionSteps, "action-step", nil, "")
	askCmd.Flags().StringArrayVar(&askActionSources, "action-source", nil, "")
	askCmd.Flags().StringVar(&askActionExpected, "action-expected", "", "")
	askCmd.Flags().StringVar(&askActionReport, "action-report", "", "")
	askCmd.Flags().StringVar(&askActionCheck, "action-check", "", "")
	askCmd.MarkFlagsOneRequired("text", "file")
	askCmd.MarkFlagsMutuallyExclusive("text", "file")
	root.AddCommand(askCmd)

	var answerText, answerFile, answerReason, answerChoice, answerExpectedRecipient string
	var answerClose bool
	answerCmd := &cobra.Command{
		Use:         "answer TASK QUESTION",
		Annotations: map[string]string{projectsAnnotation: "task"},
		Args:        cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("answer")
			if err != nil {
				return err
			}
			if answerClose {
				ctx, err := store.Context(o.installRoot)
				if err != nil {
					return err
				}
				if err := app.RequireCoordinator(st, ctx); err != nil {
					return err
				}
				view, err := ask.Close(st, ctx, args[0], args[1], answerReason)
				if err != nil {
					return err
				}
				return emitOrdjson(cmd.OutOrStdout(), view)
			}
			ctx := app.OptionalContext(o.installRoot)
			if answerChoice != "" || answerExpectedRecipient != "" {
				ctx, err = store.Context(o.installRoot)
				if err != nil {
					return err
				}
				if err := app.RequireCoordinator(st, ctx); err != nil {
					return err
				}
			}
			body := ""
			if answerChoice == "" {
				body, err = app.TextInput(answerText, answerFile)
				if err != nil {
					return err
				}
			}
			var checkRecipient ask.RecipientCheck
			if answerExpectedRecipient != "" {
				checkRecipient, err = observeExpectedWorker(st, o.runtimeRoot, args[0], answerExpectedRecipient)
				if err != nil {
					return err
				}
			}
			pump := func() (*ordjson.Object, error) {
				return returns.Notify(st, o.pumpOpts(), args[0], "worker", "an answer has been recorded", false)
			}
			var view *ordjson.Object
			if answerChoice != "" || answerExpectedRecipient != "" {
				view, err = ask.AnswerExpectedWithRecipientCheck(st, args[0], args[1], body, answerChoice, answerExpectedRecipient, ctx, checkRecipient, pump)
			} else {
				view, err = ask.Answer(st, args[0], args[1], body, ctx, pump)
			}
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	answerCmd.Flags().StringVar(&answerText, "text", "", "")
	answerCmd.Flags().StringVar(&answerFile, "file", "", "")
	answerCmd.Flags().StringVar(&answerChoice, "choice", "", "")
	answerCmd.Flags().StringVar(&answerExpectedRecipient, "expected-recipient", "", "")
	answerCmd.Flags().BoolVar(&answerClose, "close", false, "")
	answerCmd.Flags().StringVar(&answerReason, "reason", "", "")
	answerCmd.MarkFlagsOneRequired("text", "file", "choice", "close")
	answerCmd.MarkFlagsMutuallyExclusive("text", "file", "choice", "close")
	answerCmd.MarkFlagsRequiredTogether("close", "reason")
	answerCmd.MarkFlagsRequiredTogether("choice", "expected-recipient")
	root.AddCommand(answerCmd)

	var explainText, explainFile, explainExpectedRecipient string
	explainCmd := &cobra.Command{
		Use:         "explain TASK QUESTION",
		Annotations: map[string]string{projectsAnnotation: "task"},
		Args:        cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("explain")
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
			body, err := app.TextInput(explainText, explainFile)
			if err != nil {
				return err
			}
			checkRecipient, err := observeExpectedWorker(st, o.runtimeRoot, args[0], explainExpectedRecipient)
			if err != nil {
				return err
			}
			view, err := ask.ExplainWithRecipientCheck(st, args[0], args[1], body, explainExpectedRecipient, checkRecipient, func() (*ordjson.Object, error) {
				return returns.Notify(st, o.pumpOpts(), args[0], "worker", "a clarification was requested", true)
			})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	explainCmd.Flags().StringVar(&explainText, "text", "", "")
	explainCmd.Flags().StringVar(&explainFile, "file", "", "")
	explainCmd.Flags().StringVar(&explainExpectedRecipient, "expected-recipient", "", "")
	explainCmd.MarkFlagsOneRequired("text", "file")
	explainCmd.MarkFlagsMutuallyExclusive("text", "file")
	_ = explainCmd.MarkFlagRequired("expected-recipient")
	root.AddCommand(explainCmd)

	root.AddCommand(&cobra.Command{
		Use:         "resolve TASK QUESTION",
		Annotations: map[string]string{projectsAnnotation: "task"},
		Args:        cobra.ExactArgs(2),
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
	var pumpBudget float64
	pumpCmd := &cobra.Command{
		Use:  "pump",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if pumpBudget < 0 {
				return usageError("pump", []string{"--budget", fmt.Sprint(pumpBudget)})
			}
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
			tasks, err := st.AllTasks()
			if err != nil {
				return err
			}
			opts := o.pumpOpts()
			opts.Ctx = ctx
			opts.Tasks = pumpTasks
			opts.Force = pumpForce
			opts.Inline = true
			if callerVerified(st, ctx, reg) {
				opts.CallerVerifiedRole, _ = registrationString(reg, "role")
				opts.CallerVerifiedTask, _ = reg.Get("task")
			}
			opts.Snapshot = tasks
			opts.Budget = time.Duration(pumpBudget * float64(time.Second))
			view, err := returns.Pump(st, opts)
			if err != nil {
				return err
			}
			if app.RequireCoordinator(st, ctx) == nil {
				view.Set("maintenance", lifecycle.Pending(st, o.sumctlPath(), tasks))
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	pumpCmd.Flags().StringArrayVar(&pumpTasks, "task", nil, "")
	pumpCmd.Flags().BoolVar(&pumpForce, "force", false, "")
	pumpCmd.Flags().Float64Var(&pumpBudget, "budget", 0, "")
	root.AddCommand(pumpCmd)

	var sweepTasks []string
	var sweepBudget float64
	sweepCmd := &cobra.Command{
		Use:  "sweep",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sweepBudget < 0 {
				return usageError("sweep", []string{"--budget", fmt.Sprint(sweepBudget)})
			}
			st, err := o.openStore("sweep")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			budget := lifecycle.DefaultSweepBudget
			if cmd.Flags().Changed("budget") {
				// An explicit zero starts nothing: every task is reported deferred with its command.
				budget = time.Duration(sweepBudget * float64(time.Second))
				if budget == 0 {
					budget = -1
				}
			}
			view, err := lifecycle.Sweep(st, ctx, o.runtimeRoot, lifecycle.SweepOpts{Tasks: sweepTasks, Budget: budget, SumctlPath: o.sumctlPath()})
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	sweepCmd.Flags().StringArrayVar(&sweepTasks, "task", nil, "")
	sweepCmd.Flags().Float64Var(&sweepBudget, "budget", 0, "")
	root.AddCommand(sweepCmd)

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
		Use:         "attention TASK ATTENTION",
		Annotations: map[string]string{projectsAnnotation: "task"},
		Args:        cobra.ExactArgs(2),
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

	var workerPane, reviewerPane string
	var parentOnly bool
	bindCmd := &cobra.Command{
		Use:         "bind TASK",
		Annotations: map[string]string{projectsAnnotation: "task"},
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := o.openStore("bind")
			if err != nil {
				return err
			}
			ctx, err := store.Context(o.installRoot)
			if err != nil {
				return err
			}
			view, err := bindcmd.Run(st, ctx, args[0], workerPane, reviewerPane, parentOnly, o.pumpOpts())
			if err != nil {
				return err
			}
			return emitOrdjson(cmd.OutOrStdout(), view)
		},
	}
	bindCmd.Flags().StringVar(&workerPane, "worker-terminal", "", "")
	bindCmd.Flags().StringVar(&reviewerPane, "reviewer-terminal", "", "")
	bindCmd.Flags().BoolVar(&parentOnly, "parent-only", false, "")
	root.AddCommand(bindCmd)

	var reportText, reportFile, handoffPath string
	reportCmd := &cobra.Command{
		Use:         "report TASK",
		Annotations: map[string]string{projectsAnnotation: "task"},
		Args:        cobra.ExactArgs(1),
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

// callerVerified judges the calling pane against the incarnation its registration's role records. Anything else is not
// verified, and the delivery pass then checks the pane itself before presenting its inline listing.
func callerVerified(st *store.Store, ctx *ordjson.Object, reg *ordjson.Object) bool {
	endpoint := store.EndpointFromContext(ctx)
	_, terminal, err := app.ObserveNative(endpoint)
	if err != nil {
		return false
	}
	receipt, err := app.NativeReceipt(reg)
	if err != nil {
		return false
	}
	recorded, _ := reg.Get("incarnation")
	return incarnation.JudgeNative(recorded, endpoint.Native(), terminal, receipt).Verified
}

func registrationString(reg *ordjson.Object, key string) (string, bool) {
	v, _ := reg.Get(key)
	s, ok := v.(string)
	return s, ok
}
