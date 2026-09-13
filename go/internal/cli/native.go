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
		Use:                "help",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runHelp,
	})
	root.AddCommand(&cobra.Command{
		Use:                "quota",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runQuota,
	})
	root.AddCommand(&cobra.Command{
		Use:                "skills",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runSkills,
	})
	root.AddCommand(&cobra.Command{
		Use:                "notes",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runNotes,
	})
	root.AddCommand(&cobra.Command{
		Use:                "herdr",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runHerdr,
	})
	root.AddCommand(&cobra.Command{
		Use:                "archive",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runArchive,
	})
	root.AddCommand(&cobra.Command{
		Use:                "ask",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runAsk,
	})
	root.AddCommand(&cobra.Command{
		Use:                "answer",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runAnswer,
	})
	root.AddCommand(&cobra.Command{
		Use:                "resolve",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runResolve,
	})
	root.AddCommand(&cobra.Command{
		Use:                "notice",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runNotice,
	})
	root.AddCommand(&cobra.Command{
		Use:                "pump",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runPump,
	})
	root.AddCommand(&cobra.Command{
		Use:                "backup",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runBackup,
	})
	root.AddCommand(&cobra.Command{
		Use:                "attention",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runAttention,
	})
	root.AddCommand(&cobra.Command{
		Use:                "bind",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runBind,
	})
	root.AddCommand(&cobra.Command{
		Use:                "report",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runReport,
	})
	root.AddCommand(&cobra.Command{
		Use:                "dev",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runDev,
	})
}

func (o *rootOptions) runHelp(cmd *cobra.Command, args []string) error {
	if _, err := o.openStore("help"); err != nil {
		return err
	}
	topic := ""
	if len(args) == 1 && !strings.HasPrefix(args[0], "-") {
		topic = args[0]
	} else if len(args) > 1 {
		return usageError("help", args)
	}
	view, err := helpview.View(o.runtimeRoot, topic)
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func (o *rootOptions) runQuota(cmd *cobra.Command, args []string) error {
	if _, err := o.openStore("quota"); err != nil {
		return err
	}
	provider, format, ok := parseQuotaArgs(args)
	if !ok {
		return usageError("quota", args)
	}
	code, err := quota.Run(o.runtimeRoot, provider, format, cmd.OutOrStdout(), cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	if code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}

func parseQuotaArgs(tokens []string) (provider, format string, ok bool) {
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--provider":
			if provider != "" || i+1 >= len(tokens) {
				return "", "", false
			}
			i++
			provider = tokens[i]
		case strings.HasPrefix(token, "--provider="):
			if provider != "" {
				return "", "", false
			}
			provider = strings.TrimPrefix(token, "--provider=")
		case token == "--format":
			if i+1 >= len(tokens) {
				return "", "", false
			}
			i++
			format = tokens[i]
		case strings.HasPrefix(token, "--format="):
			format = strings.TrimPrefix(token, "--format=")
		default:
			return "", "", false
		}
	}
	if provider == "" {
		return "", "", false
	}
	if format != "" && format != "json" && format != "compact" && format != "toon" {
		return "", "", false
	}
	return provider, format, true
}

func (o *rootOptions) runSkills(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("command is required")
	}
	switch args[0] {
	case "check":
		rootPath, ok := parseSkillsCheckArgs(args[1:], o.runtimeRoot)
		if !ok {
			return usageError("skills", args)
		}
		if _, err := o.openStore("skills-check"); err != nil {
			return err
		}
		view, err := skills.Check(rootPath)
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
	case "install":
		target, source, skillNames, agentNames, ok := parseSkillsInstallArgs(args[1:])
		if !ok {
			return usageError("skills", args)
		}
		if _, err := o.openStore("skills-install"); err != nil {
			return err
		}
		view, err := skills.Install(o.runtimeRoot, target, source, skillNames, agentNames)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	default:
		return usageError("skills", args)
	}
}

func parseSkillsCheckArgs(tokens []string, defaultRoot string) (string, bool) {
	rootPath := defaultRoot
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--root":
			if i+1 >= len(tokens) {
				return "", false
			}
			i++
			rootPath = tokens[i]
		case strings.HasPrefix(token, "--root="):
			rootPath = strings.TrimPrefix(token, "--root=")
		default:
			return "", false
		}
	}
	return rootPath, true
}

func parseSkillsInstallArgs(tokens []string) (target, source string, skillNames, agentNames []string, ok bool) {
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--target":
			if i+1 >= len(tokens) {
				return "", "", nil, nil, false
			}
			i++
			target = tokens[i]
		case strings.HasPrefix(token, "--target="):
			target = strings.TrimPrefix(token, "--target=")
		case token == "--source":
			if i+1 >= len(tokens) {
				return "", "", nil, nil, false
			}
			i++
			source = tokens[i]
		case strings.HasPrefix(token, "--source="):
			source = strings.TrimPrefix(token, "--source=")
		case token == "--skill":
			if i+1 >= len(tokens) {
				return "", "", nil, nil, false
			}
			i++
			skillNames = append(skillNames, tokens[i])
		case strings.HasPrefix(token, "--skill="):
			skillNames = append(skillNames, strings.TrimPrefix(token, "--skill="))
		case token == "--agent":
			if i+1 >= len(tokens) {
				return "", "", nil, nil, false
			}
			i++
			agentNames = append(agentNames, tokens[i])
		case strings.HasPrefix(token, "--agent="):
			agentNames = append(agentNames, strings.TrimPrefix(token, "--agent="))
		default:
			return "", "", nil, nil, false
		}
	}
	if target == "" || source == "" || len(skillNames) == 0 || len(agentNames) == 0 {
		return "", "", nil, nil, false
	}
	return target, source, skillNames, agentNames, true
}

func (o *rootOptions) runBackup(cmd *cobra.Command, args []string) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("backup destination is required")
	}
	st, err := o.openStore("backup")
	if err != nil {
		return err
	}
	view, err := backup.Run(st, args[0])
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func (o *rootOptions) runAttention(cmd *cobra.Command, args []string) error {
	taskID, attentionID, ok := parseAttentionArgs(args)
	if !ok {
		return fmt.Errorf("invalid attention arguments")
	}
	st, err := o.openStore("attention")
	if err != nil {
		return err
	}
	ctx, err := store.Context(o.installRoot)
	if err != nil {
		return err
	}
	view, err := attention.Seen(st, ctx, taskID, attentionID)
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func parseAttentionArgs(tokens []string) (taskID, attentionID string, ok bool) {
	seen := false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--seen":
			seen = true
		case strings.HasPrefix(token, "-"):
			return "", "", false
		case taskID == "":
			taskID = token
		case attentionID == "":
			attentionID = token
		default:
			return "", "", false
		}
	}
	if !seen || taskID == "" || attentionID == "" {
		return "", "", false
	}
	return taskID, attentionID, true
}

func (o *rootOptions) runBind(cmd *cobra.Command, args []string) error {
	taskID, workerPane, parentOnly, ok := parseBindArgs(args)
	if !ok {
		return fmt.Errorf("invalid bind arguments")
	}
	st, err := o.openStore("bind")
	if err != nil {
		return err
	}
	ctx, err := store.Context(o.installRoot)
	if err != nil {
		return err
	}
	view, err := bindcmd.Run(st, ctx, taskID, workerPane, parentOnly, o.pumpOpts())
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func parseBindArgs(tokens []string) (taskID, workerPane string, parentOnly, ok bool) {
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--parent-only":
			parentOnly = true
		case token == "--worker-pane":
			if i+1 >= len(tokens) {
				return "", "", false, false
			}
			i++
			workerPane = tokens[i]
		case strings.HasPrefix(token, "--worker-pane="):
			workerPane = strings.TrimPrefix(token, "--worker-pane=")
		case strings.HasPrefix(token, "-"):
			return "", "", false, false
		case taskID == "":
			taskID = token
		default:
			return "", "", false, false
		}
	}
	if taskID == "" {
		return "", "", false, false
	}
	return taskID, workerPane, parentOnly, true
}

func (o *rootOptions) runReport(cmd *cobra.Command, args []string) error {
	taskID, text, file, handoffPath, ok := parseReportArgs(args)
	if !ok {
		return fmt.Errorf("invalid report arguments")
	}
	st, err := o.openStore("report")
	if err != nil {
		return err
	}
	body, err := app.TextInput(text, file)
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
	view, err := report.Run(st, taskID, body, handoff, app.OptionalContext(o.installRoot), o.pumpOpts())
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func parseReportArgs(tokens []string) (taskID, text, file, handoff string, ok bool) {
	textSet, fileSet := false, false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		take := func(dest *string) bool {
			if i+1 >= len(tokens) {
				return false
			}
			i++
			*dest = tokens[i]
			return true
		}
		switch {
		case token == "--text":
			if textSet || !take(&text) {
				return "", "", "", "", false
			}
			textSet = true
		case strings.HasPrefix(token, "--text="):
			if textSet {
				return "", "", "", "", false
			}
			text = strings.TrimPrefix(token, "--text=")
			textSet = true
		case token == "--file":
			if fileSet || !take(&file) {
				return "", "", "", "", false
			}
			fileSet = true
		case strings.HasPrefix(token, "--file="):
			if fileSet {
				return "", "", "", "", false
			}
			file = strings.TrimPrefix(token, "--file=")
			fileSet = true
		case token == "--handoff":
			if !take(&handoff) {
				return "", "", "", "", false
			}
		case strings.HasPrefix(token, "--handoff="):
			handoff = strings.TrimPrefix(token, "--handoff=")
		case strings.HasPrefix(token, "-"):
			return "", "", "", "", false
		case taskID == "":
			taskID = token
		default:
			return "", "", "", "", false
		}
	}
	if taskID == "" || (!textSet && !fileSet) {
		return "", "", "", "", false
	}
	return taskID, text, file, handoff, true
}

func (o *rootOptions) runArchive(cmd *cobra.Command, args []string) error {
	taskID, acknowledge, ok := parseArchiveArgs(args)
	if !ok {
		return usageError("archive", args)
	}
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
	view, err := archive.Run(st, taskID, acknowledge)
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func (o *rootOptions) pumpOpts() returns.PumpOpts {
	return returns.PumpOpts{
		RuntimeRoot: o.runtimeRoot,
		SumctlPath:  o.sumctlPath(),
		Ctx:         app.OptionalContext(o.installRoot),
		Inline:      true,
	}
}

func (o *rootOptions) runAsk(cmd *cobra.Command, args []string) error {
	taskID, key, text, file, ok := parseAskArgs(args)
	if !ok {
		return usageError("ask", args)
	}
	st, err := o.openStore("ask")
	if err != nil {
		return err
	}
	body, err := app.TextInput(text, file)
	if err != nil {
		return err
	}
	view, err := ask.Ask(st, taskID, key, body, func() (*ordjson.Object, error) {
		return returns.Notify(st, o.pumpOpts(), taskID, "parent", "a decision is waiting", false)
	})
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func parseAskArgs(tokens []string) (taskID, key, text, file string, ok bool) {
	textSet, fileSet := false, false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--key":
			if i+1 >= len(tokens) {
				return "", "", "", "", false
			}
			i++
			key = tokens[i]
		case strings.HasPrefix(token, "--key="):
			key = strings.TrimPrefix(token, "--key=")
		case token == "--text":
			if textSet || fileSet || i+1 >= len(tokens) {
				return "", "", "", "", false
			}
			i++
			text = tokens[i]
			textSet = true
		case strings.HasPrefix(token, "--text="):
			if textSet || fileSet {
				return "", "", "", "", false
			}
			text = strings.TrimPrefix(token, "--text=")
			textSet = true
		case token == "--file":
			if textSet || fileSet || i+1 >= len(tokens) {
				return "", "", "", "", false
			}
			i++
			file = tokens[i]
			fileSet = true
		case strings.HasPrefix(token, "--file="):
			if textSet || fileSet {
				return "", "", "", "", false
			}
			file = strings.TrimPrefix(token, "--file=")
			fileSet = true
		case strings.HasPrefix(token, "-"):
			return "", "", "", "", false
		case taskID == "":
			taskID = token
		default:
			return "", "", "", "", false
		}
	}
	if taskID == "" || (!textSet && !fileSet) {
		return "", "", "", "", false
	}
	return taskID, key, text, file, true
}

func (o *rootOptions) runAnswer(cmd *cobra.Command, args []string) error {
	taskID, questionID, text, file, ok := parseAnswerArgs(args)
	if !ok {
		return usageError("answer", args)
	}
	st, err := o.openStore("answer")
	if err != nil {
		return err
	}
	body, err := app.TextInput(text, file)
	if err != nil {
		return err
	}
	endpoint := app.OptionalContext(o.installRoot)
	view, err := ask.Answer(st, taskID, questionID, body, endpoint, func() (*ordjson.Object, error) {
		return returns.Notify(st, o.pumpOpts(), taskID, "worker", "an answer has been recorded", false)
	})
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func parseAnswerArgs(tokens []string) (taskID, questionID, text, file string, ok bool) {
	textSet, fileSet := false, false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--text":
			if textSet || fileSet || i+1 >= len(tokens) {
				return "", "", "", "", false
			}
			i++
			text = tokens[i]
			textSet = true
		case strings.HasPrefix(token, "--text="):
			if textSet || fileSet {
				return "", "", "", "", false
			}
			text = strings.TrimPrefix(token, "--text=")
			textSet = true
		case token == "--file":
			if textSet || fileSet || i+1 >= len(tokens) {
				return "", "", "", "", false
			}
			i++
			file = tokens[i]
			fileSet = true
		case strings.HasPrefix(token, "--file="):
			if textSet || fileSet {
				return "", "", "", "", false
			}
			file = strings.TrimPrefix(token, "--file=")
			fileSet = true
		case strings.HasPrefix(token, "-"):
			return "", "", "", "", false
		case taskID == "":
			taskID = token
		case questionID == "":
			questionID = token
		default:
			return "", "", "", "", false
		}
	}
	if taskID == "" || questionID == "" || (!textSet && !fileSet) {
		return "", "", "", "", false
	}
	return taskID, questionID, text, file, true
}

func (o *rootOptions) runResolve(cmd *cobra.Command, args []string) error {
	if len(args) != 2 || strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[1], "-") {
		return usageError("resolve", args)
	}
	st, err := o.openStore("resolve")
	if err != nil {
		return err
	}
	view, err := ask.Resolve(st, args[0], args[1])
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func (o *rootOptions) runNotice(cmd *cobra.Command, args []string) error {
	taskID, recipient, ok := parseNoticeArgs(args)
	if !ok {
		return usageError("notice", args)
	}
	st, err := o.openStore("notice")
	if err != nil {
		return err
	}
	view, err := returns.Notify(st, o.pumpOpts(), taskID, recipient, "saved task state needs attention", true)
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func parseNoticeArgs(tokens []string) (taskID, recipient string, ok bool) {
	recipient = "parent"
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--to":
			if i+1 >= len(tokens) {
				return "", "", false
			}
			i++
			recipient = tokens[i]
		case strings.HasPrefix(token, "--to="):
			recipient = strings.TrimPrefix(token, "--to=")
		case strings.HasPrefix(token, "-"):
			return "", "", false
		case taskID == "":
			taskID = token
		default:
			return "", "", false
		}
	}
	if taskID == "" || (recipient != "parent" && recipient != "worker") {
		return "", "", false
	}
	return taskID, recipient, true
}

func (o *rootOptions) runPump(cmd *cobra.Command, args []string) error {
	tasks, force, ok := parsePumpArgs(args)
	if !ok {
		return usageError("pump", args)
	}
	st, err := o.openStore("pump")
	if err != nil {
		return err
	}
	ctx, err := store.Context(o.installRoot)
	if err != nil {
		return err
	}
	if _, err := st.Registration(store.EndpointFromContext(ctx)); err != nil {
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
	opts.Tasks = tasks
	opts.Force = force
	opts.Inline = true
	view, err := returns.Pump(st, opts)
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func parsePumpArgs(tokens []string) (tasks []string, force bool, ok bool) {
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--force":
			force = true
		case token == "--task":
			if i+1 >= len(tokens) {
				return nil, false, false
			}
			i++
			tasks = append(tasks, tokens[i])
		case strings.HasPrefix(token, "--task="):
			tasks = append(tasks, strings.TrimPrefix(token, "--task="))
		default:
			return nil, false, false
		}
	}
	return tasks, force, true
}

func parseArchiveArgs(tokens []string) (taskID string, acknowledge bool, ok bool) {
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--acknowledge":
			if acknowledge {
				return "", false, false
			}
			acknowledge = true
		case strings.HasPrefix(token, "-"):
			return "", false, false
		case taskID == "":
			taskID = token
		default:
			return "", false, false
		}
	}
	if taskID == "" {
		return "", false, false
	}
	return taskID, acknowledge, true
}

func (o *rootOptions) runNotes(cmd *cobra.Command, args []string) error {
	taskID, text, file, ok := parseTextTaskArgs(args)
	if !ok {
		return usageError("notes", args)
	}
	st, err := o.openStore("notes")
	if err != nil {
		return err
	}
	body, err := app.TextInput(text, file)
	if err != nil {
		return err
	}
	endpoint := app.OptionalContext(o.installRoot)
	view, err := notes.Add(st, taskID, body, endpoint)
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func parseTextTaskArgs(tokens []string) (taskID, text, file string, ok bool) {
	textSet, fileSet := false, false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--text":
			if textSet || fileSet || i+1 >= len(tokens) {
				return "", "", "", false
			}
			i++
			text = tokens[i]
			textSet = true
		case strings.HasPrefix(token, "--text="):
			if textSet || fileSet {
				return "", "", "", false
			}
			text = strings.TrimPrefix(token, "--text=")
			textSet = true
		case token == "--file":
			if textSet || fileSet || i+1 >= len(tokens) {
				return "", "", "", false
			}
			i++
			file = tokens[i]
			fileSet = true
		case strings.HasPrefix(token, "--file="):
			if textSet || fileSet {
				return "", "", "", false
			}
			file = strings.TrimPrefix(token, "--file=")
			fileSet = true
		case strings.HasPrefix(token, "-"):
			return "", "", "", false
		case taskID == "":
			taskID = token
		default:
			return "", "", "", false
		}
	}
	if taskID == "" || (!textSet && !fileSet) || (textSet && fileSet) {
		return "", "", "", false
	}
	return taskID, text, file, true
}

func (o *rootOptions) runHerdr(cmd *cobra.Command, args []string) error {
	st, err := o.openStore("herdr")
	if err != nil {
		return err
	}
	ctx, ctxErr := store.Context(o.installRoot)
	if ctxErr != nil {
		return fmt.Errorf("%s The bridge never borrows a saved coordinator context.", capitalizeHerdrContext(ctxErr.Error()))
	}
	return herdrbridge.Run(o.runtimeRoot, st, ctx, args, cmd.OutOrStdout())
}

func capitalizeHerdrContext(msg string) string {
	if strings.HasPrefix(msg, "run this command") {
		return "Run" + msg[3:]
	}
	return msg
}

func (o *rootOptions) runDev(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("command is required")
	}
	st, err := o.openStore("dev-" + args[0])
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return usageError("dev list", args[1:])
		}
		view, err := devcmd.List(st)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "prepare":
		name, base, pane, ok := parseDevPrepareArgs(args[1:])
		if !ok {
			return usageError("dev prepare", args[1:])
		}
		ctx := app.OptionalContext(o.installRoot)
		view, err := devcmd.Prepare(st, ctx, o.runtimeRoot, name, base, pane)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "remove":
		name, ok := parseDevRemoveArgs(args[1:])
		if !ok {
			return usageError("dev remove", args[1:])
		}
		view, err := devcmd.Remove(st, name)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	default:
		return usageError("dev", args)
	}
}

func parseDevPrepareArgs(tokens []string) (name, base string, pane, ok bool) {
	base = "HEAD"
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--name":
			if i+1 >= len(tokens) {
				return "", "", false, false
			}
			i++
			name = tokens[i]
		case strings.HasPrefix(token, "--name="):
			name = strings.TrimPrefix(token, "--name=")
		case token == "--base":
			if i+1 >= len(tokens) {
				return "", "", false, false
			}
			i++
			base = tokens[i]
		case strings.HasPrefix(token, "--base="):
			base = strings.TrimPrefix(token, "--base=")
		case token == "--pane":
			pane = true
		default:
			return "", "", false, false
		}
	}
	if name == "" {
		return "", "", false, false
	}
	return name, base, pane, true
}

func parseDevRemoveArgs(tokens []string) (string, bool) {
	name := ""
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--name":
			if i+1 >= len(tokens) {
				return "", false
			}
			i++
			name = tokens[i]
		case strings.HasPrefix(token, "--name="):
			name = strings.TrimPrefix(token, "--name=")
		default:
			return "", false
		}
	}
	return name, name != ""
}
