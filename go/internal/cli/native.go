package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/archive"
	"github.com/douglasjarquin/sum/go/internal/ask"
	"github.com/douglasjarquin/sum/go/internal/attention"
	"github.com/douglasjarquin/sum/go/internal/backup"
	"github.com/douglasjarquin/sum/go/internal/bindcmd"
	"github.com/douglasjarquin/sum/go/internal/cleanup"
	"github.com/douglasjarquin/sum/go/internal/devcmd"
	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/execution"
	"github.com/douglasjarquin/sum/go/internal/guard"
	"github.com/douglasjarquin/sum/go/internal/helpview"
	"github.com/douglasjarquin/sum/go/internal/herdrbridge"
	"github.com/douglasjarquin/sum/go/internal/hookstatus"
	"github.com/douglasjarquin/sum/go/internal/lsp"
	"github.com/douglasjarquin/sum/go/internal/notes"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/prcmd"
	"github.com/douglasjarquin/sum/go/internal/prepare"
	"github.com/douglasjarquin/sum/go/internal/refreshcmd"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/report"
	"github.com/douglasjarquin/sum/go/internal/review"
	"github.com/douglasjarquin/sum/go/internal/updatecmd"
	"github.com/douglasjarquin/sum/go/internal/verifycmd"
	"strconv"

	"github.com/douglasjarquin/sum/go/internal/quota"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/settings"
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
		Use:                "execution",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runExecution,
	})
	root.AddCommand(&cobra.Command{
		Use:                "repair",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runRepair,
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
		Use:                "prepare",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runPrepare,
	})
	root.AddCommand(&cobra.Command{
		Use:                "dispatch",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runDispatch,
	})
	root.AddCommand(&cobra.Command{
		Use:                "start",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runStart,
	})
	root.AddCommand(&cobra.Command{
		Use:                "review",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runReview,
	})
	root.AddCommand(&cobra.Command{
		Use:                "verify",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runVerify,
	})
	root.AddCommand(&cobra.Command{
		Use:                "pr",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runPR,
	})
	root.AddCommand(&cobra.Command{
		Use:                "cleanup",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runCleanup,
	})
	root.AddCommand(&cobra.Command{
		Use:                "dev",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runDev,
	})
	root.AddCommand(&cobra.Command{
		Use:                "refresh",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runRefresh,
	})
	root.AddCommand(&cobra.Command{
		Use:                "update",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runUpdate,
	})
	root.AddCommand(&cobra.Command{
		Use:                "lsp",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               o.runLsp,
	})
}

func (o *rootOptions) runLsp(cmd *cobra.Command, args []string) error {
	if len(args) == 1 && args[0] == "ensure" {
		payload, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return nil
		}
		root := o.installRoot
		if root == "" {
			root = o.runtimeRoot
		}
		_ = lsp.Ensure(root, payload)
		return nil
	}
	return usageError("lsp", args)
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

func (o *rootOptions) runExecution(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("command is required")
	}
	switch args[0] {
	case "show":
		if len(args) != 2 || strings.HasPrefix(args[1], "-") {
			return fmt.Errorf("invalid execution show arguments")
		}
		st, err := o.openStore("execution-show")
		if err != nil {
			return err
		}
		view, err := execution.Show(st, args[1])
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "park":
		taskID, attempt, ok := parseExecutionAttemptArgs(args[1:])
		if !ok {
			return fmt.Errorf("invalid execution park arguments")
		}
		st, err := o.openStore("execution-park")
		if err != nil {
			return err
		}
		ctx, err := store.Context(o.installRoot)
		if err != nil {
			return err
		}
		view, err := execution.Park(st, ctx, o.runtimeRoot, taskID, attempt)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "resume":
		taskID, attempt, ok := parseExecutionAttemptArgs(args[1:])
		if !ok {
			return fmt.Errorf("invalid execution resume arguments")
		}
		st, err := o.openStore("execution-resume")
		if err != nil {
			return err
		}
		ctx, err := store.Context(o.installRoot)
		if err != nil {
			return err
		}
		view, err := execution.Resume(st, ctx, o.runtimeRoot, taskID, attempt)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	default:
		return fmt.Errorf("unknown execution command")
	}
}

func (o *rootOptions) runRepair(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("command is required")
	}
	switch args[0] {
	case "send":
		taskID, attempt, key, text, file, ok := parseRepairSendArgs(args[1:])
		if !ok {
			return fmt.Errorf("invalid repair send arguments")
		}
		st, err := o.openStore("repair-send")
		if err != nil {
			return err
		}
		ctx, err := store.Context(o.installRoot)
		if err != nil {
			return err
		}
		body, err := app.TextInput(text, file)
		if err != nil {
			return err
		}
		view, err := repair.Send(st, ctx, repair.SendArgs{
			TaskID:      taskID,
			Attempt:     attempt,
			Key:         key,
			Text:        body,
			RuntimeRoot: o.runtimeRoot,
		})
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "extend":
		taskID, question, additional, approved, text, file, ok := parseRepairExtendArgs(args[1:])
		if !ok {
			return fmt.Errorf("invalid repair extend arguments")
		}
		st, err := o.openStore("repair-extend")
		if err != nil {
			return err
		}
		ctx, err := store.Context(o.installRoot)
		if err != nil {
			return err
		}
		body, err := app.TextInput(text, file)
		if err != nil {
			return err
		}
		view, err := repair.Extend(st, ctx, repair.ExtendArgs{
			TaskID:     taskID,
			Question:   question,
			Additional: additional,
			Approved:   approved,
			Text:       body,
		})
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	default:
		return fmt.Errorf("unknown repair command")
	}
}

func parseExecutionAttemptArgs(tokens []string) (taskID, attempt string, ok bool) {
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--attempt":
			if i+1 >= len(tokens) {
				return "", "", false
			}
			i++
			attempt = tokens[i]
		case strings.HasPrefix(token, "--attempt="):
			attempt = strings.TrimPrefix(token, "--attempt=")
		case strings.HasPrefix(token, "-"):
			return "", "", false
		case taskID == "":
			taskID = token
		default:
			return "", "", false
		}
	}
	if taskID == "" || attempt == "" {
		return "", "", false
	}
	return taskID, attempt, true
}

func parseRepairSendArgs(tokens []string) (taskID, attempt, key, text, file string, ok bool) {
	textSet, fileSet := false, false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--attempt":
			if i+1 >= len(tokens) {
				return "", "", "", "", "", false
			}
			i++
			attempt = tokens[i]
		case strings.HasPrefix(token, "--attempt="):
			attempt = strings.TrimPrefix(token, "--attempt=")
		case token == "--key":
			if i+1 >= len(tokens) {
				return "", "", "", "", "", false
			}
			i++
			key = tokens[i]
		case strings.HasPrefix(token, "--key="):
			key = strings.TrimPrefix(token, "--key=")
		case token == "--text":
			if textSet || fileSet || i+1 >= len(tokens) {
				return "", "", "", "", "", false
			}
			i++
			text = tokens[i]
			textSet = true
		case strings.HasPrefix(token, "--text="):
			if textSet || fileSet {
				return "", "", "", "", "", false
			}
			text = strings.TrimPrefix(token, "--text=")
			textSet = true
		case token == "--file":
			if textSet || fileSet || i+1 >= len(tokens) {
				return "", "", "", "", "", false
			}
			i++
			file = tokens[i]
			fileSet = true
		case strings.HasPrefix(token, "--file="):
			if textSet || fileSet {
				return "", "", "", "", "", false
			}
			file = strings.TrimPrefix(token, "--file=")
			fileSet = true
		case strings.HasPrefix(token, "-"):
			return "", "", "", "", "", false
		case taskID == "":
			taskID = token
		default:
			return "", "", "", "", "", false
		}
	}
	if taskID == "" || attempt == "" || key == "" || (!textSet && !fileSet) {
		return "", "", "", "", "", false
	}
	return taskID, attempt, key, text, file, true
}

func parseRepairExtendArgs(tokens []string) (taskID, question string, additional int, approved bool, text, file string, ok bool) {
	textSet, fileSet, additionalSet := false, false, false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--question":
			if i+1 >= len(tokens) {
				return "", "", 0, false, "", "", false
			}
			i++
			question = tokens[i]
		case strings.HasPrefix(token, "--question="):
			question = strings.TrimPrefix(token, "--question=")
		case token == "--additional":
			if i+1 >= len(tokens) {
				return "", "", 0, false, "", "", false
			}
			i++
			n, err := strconv.Atoi(tokens[i])
			if err != nil {
				return "", "", 0, false, "", "", false
			}
			additional = n
			additionalSet = true
		case strings.HasPrefix(token, "--additional="):
			n, err := strconv.Atoi(strings.TrimPrefix(token, "--additional="))
			if err != nil {
				return "", "", 0, false, "", "", false
			}
			additional = n
			additionalSet = true
		case token == "--approved":
			approved = true
		case token == "--text":
			if textSet || fileSet || i+1 >= len(tokens) {
				return "", "", 0, false, "", "", false
			}
			i++
			text = tokens[i]
			textSet = true
		case strings.HasPrefix(token, "--text="):
			if textSet || fileSet {
				return "", "", 0, false, "", "", false
			}
			text = strings.TrimPrefix(token, "--text=")
			textSet = true
		case token == "--file":
			if textSet || fileSet || i+1 >= len(tokens) {
				return "", "", 0, false, "", "", false
			}
			i++
			file = tokens[i]
			fileSet = true
		case strings.HasPrefix(token, "--file="):
			if textSet || fileSet {
				return "", "", 0, false, "", "", false
			}
			file = strings.TrimPrefix(token, "--file=")
			fileSet = true
		case strings.HasPrefix(token, "-"):
			return "", "", 0, false, "", "", false
		case taskID == "":
			taskID = token
		default:
			return "", "", 0, false, "", "", false
		}
	}
	if taskID == "" || question == "" || !additionalSet || (!textSet && !fileSet) {
		return "", "", 0, false, "", "", false
	}
	return taskID, question, additional, approved, text, file, true
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

func (o *rootOptions) runSettingsSet(cmd *cobra.Command, args []string) error {
	parsed, ok := parseSettingsSetArgs(args)
	if !ok {
		return usageError("settings set", args)
	}
	st, err := o.openStore("settings-set")
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
	if parsed.ClearCapacity && (parsed.Global != nil || parsed.PerRepository != nil) {
		return fmt.Errorf("--clear-capacity conflicts with --global/--per-repository.")
	}
	if parsed.ClearWorker && (parsed.WorkerHarness != "" || parsed.WorkerModel != "" || parsed.WorkerReasoning != "" || parsed.WorkerPresetSet) {
		return fmt.Errorf("--clear-worker conflicts with --worker-* values.")
	}
	if parsed.WorkerPresetSet && (parsed.WorkerHarness != "" || parsed.WorkerModel != "" || parsed.WorkerReasoning != "") {
		return fmt.Errorf("--worker-preset conflicts with --worker-harness/--worker-model/--worker-reasoning: a default is either a preset reference or a plain specification.")
	}
	if parsed.ClearReviewer && parsed.ReviewerPresetSet {
		return fmt.Errorf("--clear-reviewer conflicts with --reviewer-preset.")
	}
	if parsed.Global == nil && parsed.PerRepository == nil && !parsed.ClearCapacity && parsed.WorkerHarness == "" && parsed.WorkerModel == "" && parsed.WorkerReasoning == "" && !parsed.WorkerPresetSet && !parsed.ClearWorker && !parsed.ReviewerPresetSet && !parsed.ClearReviewer {
		return fmt.Errorf("Give --global, --per-repository, --clear-capacity, --worker-harness/--worker-model/--worker-reasoning, --worker-preset, --clear-worker, --reviewer-preset, or --clear-reviewer.")
	}
	view, err := settings.Write(st, parsed)
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func parseSettingsSetArgs(tokens []string) (settings.WriteArgs, bool) {
	var parsed settings.WriteArgs
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		takeInt := func(dest **int) bool {
			if i+1 >= len(tokens) {
				return false
			}
			i++
			n, err := strconv.Atoi(tokens[i])
			if err != nil {
				return false
			}
			*dest = &n
			return true
		}
		takeString := func(dest *string) bool {
			if i+1 >= len(tokens) {
				return false
			}
			i++
			*dest = tokens[i]
			return true
		}
		switch {
		case token == "--global":
			if !takeInt(&parsed.Global) {
				return settings.WriteArgs{}, false
			}
		case strings.HasPrefix(token, "--global="):
			n, err := strconv.Atoi(strings.TrimPrefix(token, "--global="))
			if err != nil {
				return settings.WriteArgs{}, false
			}
			parsed.Global = &n
		case token == "--per-repository":
			if !takeInt(&parsed.PerRepository) {
				return settings.WriteArgs{}, false
			}
		case strings.HasPrefix(token, "--per-repository="):
			n, err := strconv.Atoi(strings.TrimPrefix(token, "--per-repository="))
			if err != nil {
				return settings.WriteArgs{}, false
			}
			parsed.PerRepository = &n
		case token == "--clear-capacity":
			parsed.ClearCapacity = true
		case token == "--worker-harness":
			if !takeString(&parsed.WorkerHarness) {
				return settings.WriteArgs{}, false
			}
		case strings.HasPrefix(token, "--worker-harness="):
			parsed.WorkerHarness = strings.TrimPrefix(token, "--worker-harness=")
		case token == "--worker-model":
			if !takeString(&parsed.WorkerModel) {
				return settings.WriteArgs{}, false
			}
		case strings.HasPrefix(token, "--worker-model="):
			parsed.WorkerModel = strings.TrimPrefix(token, "--worker-model=")
		case token == "--worker-reasoning":
			if !takeString(&parsed.WorkerReasoning) {
				return settings.WriteArgs{}, false
			}
		case strings.HasPrefix(token, "--worker-reasoning="):
			parsed.WorkerReasoning = strings.TrimPrefix(token, "--worker-reasoning=")
		case token == "--worker-preset":
			if !takeString(&parsed.WorkerPreset) {
				return settings.WriteArgs{}, false
			}
			parsed.WorkerPresetSet = true
		case strings.HasPrefix(token, "--worker-preset="):
			parsed.WorkerPreset = strings.TrimPrefix(token, "--worker-preset=")
			parsed.WorkerPresetSet = true
		case token == "--clear-worker":
			parsed.ClearWorker = true
		case token == "--reviewer-preset":
			if !takeString(&parsed.ReviewerPreset) {
				return settings.WriteArgs{}, false
			}
			parsed.ReviewerPresetSet = true
		case strings.HasPrefix(token, "--reviewer-preset="):
			parsed.ReviewerPreset = strings.TrimPrefix(token, "--reviewer-preset=")
			parsed.ReviewerPresetSet = true
		case token == "--clear-reviewer":
			parsed.ClearReviewer = true
		default:
			return settings.WriteArgs{}, false
		}
	}
	return parsed, true
}

func (o *rootOptions) runPresetSet(cmd *cobra.Command, args []string) error {
	parsed, ok := parsePresetSetArgs(args)
	if !ok {
		return usageError("preset set", args)
	}
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

func parsePresetSetArgs(tokens []string) (settings.PresetWriteArgs, bool) {
	var parsed settings.PresetWriteArgs
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--harness":
			if i+1 >= len(tokens) {
				return settings.PresetWriteArgs{}, false
			}
			i++
			parsed.Harness = tokens[i]
		case strings.HasPrefix(token, "--harness="):
			parsed.Harness = strings.TrimPrefix(token, "--harness=")
		case token == "--model":
			if i+1 >= len(tokens) {
				return settings.PresetWriteArgs{}, false
			}
			i++
			parsed.Model = tokens[i]
		case strings.HasPrefix(token, "--model="):
			parsed.Model = strings.TrimPrefix(token, "--model=")
		case token == "--reasoning":
			if i+1 >= len(tokens) {
				return settings.PresetWriteArgs{}, false
			}
			i++
			parsed.Reasoning = tokens[i]
		case strings.HasPrefix(token, "--reasoning="):
			parsed.Reasoning = strings.TrimPrefix(token, "--reasoning=")
		case token == "--arg":
			if i+1 >= len(tokens) {
				return settings.PresetWriteArgs{}, false
			}
			i++
			parsed.Args = append(parsed.Args, tokens[i])
			parsed.ArgsSet = true
		case strings.HasPrefix(token, "--arg="):
			parsed.Args = append(parsed.Args, strings.TrimPrefix(token, "--arg="))
			parsed.ArgsSet = true
		case token == "--clear-model":
			parsed.Clear = append(parsed.Clear, "model")
		case token == "--clear-reasoning":
			parsed.Clear = append(parsed.Clear, "reasoning")
		case token == "--clear-args":
			parsed.Clear = append(parsed.Clear, "args")
		case strings.HasPrefix(token, "-"):
			return settings.PresetWriteArgs{}, false
		case parsed.Name == "":
			parsed.Name = token
		default:
			return settings.PresetWriteArgs{}, false
		}
	}
	if parsed.Name == "" {
		return settings.PresetWriteArgs{}, false
	}
	return parsed, true
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

func capitalizeHerdrContext(msg string) string {
	if strings.HasPrefix(msg, "run this command") {
		return "Run" + msg[3:]
	}
	return msg
}

func (o *rootOptions) runPrepare(cmd *cobra.Command, args []string) error {
	parsed, ok := parsePrepareArgs(args)
	if !ok {
		return fmt.Errorf("invalid prepare arguments")
	}
	st, err := o.openStore("prepare")
	if err != nil {
		return err
	}
	ctx, err := store.Context(o.installRoot)
	if err != nil {
		return err
	}
	parsed.RuntimeRoot = o.runtimeRoot
	parsed.SumctlPath = o.sumctlPath()
	view, err := prepare.Prepare(st, ctx, parsed)
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func (o *rootOptions) runDispatch(cmd *cobra.Command, args []string) error {
	parsed, ok := parsePrepareArgs(args)
	if !ok {
		return fmt.Errorf("invalid dispatch arguments")
	}
	st, err := o.openStore("dispatch")
	if err != nil {
		return err
	}
	ctx, err := store.Context(o.installRoot)
	if err != nil {
		return err
	}
	parsed.RuntimeRoot = o.runtimeRoot
	parsed.SumctlPath = o.sumctlPath()
	view, err := prepare.Dispatch(st, ctx, parsed)
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func (o *rootOptions) runStart(cmd *cobra.Command, args []string) error {
	taskID, extra, ok := parseStartArgs(args)
	if !ok {
		return fmt.Errorf("invalid start arguments")
	}
	st, err := o.openStore("start")
	if err != nil {
		return err
	}
	ctx, err := store.Context(o.installRoot)
	if err != nil {
		return err
	}
	view, err := prepare.Start(st, ctx, o.runtimeRoot, taskID, extra)
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func parsePrepareArgs(tokens []string) (prepare.Args, bool) {
	var parsed prepare.Args
	parsed.Base = "HEAD"
	parsed.Kind = "ship"
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
		case token == "--repo":
			if !take(&parsed.Repo) {
				return prepare.Args{}, false
			}
		case strings.HasPrefix(token, "--repo="):
			parsed.Repo = strings.TrimPrefix(token, "--repo=")
		case token == "--project":
			if !take(&parsed.Project) {
				return prepare.Args{}, false
			}
		case strings.HasPrefix(token, "--project="):
			parsed.Project = strings.TrimPrefix(token, "--project=")
		case token == "--brief":
			if !take(&parsed.Brief) {
				return prepare.Args{}, false
			}
		case strings.HasPrefix(token, "--brief="):
			parsed.Brief = strings.TrimPrefix(token, "--brief=")
		case token == "--harness":
			if !take(&parsed.Harness) {
				return prepare.Args{}, false
			}
		case strings.HasPrefix(token, "--harness="):
			parsed.Harness = strings.TrimPrefix(token, "--harness=")
		case token == "--model":
			if !take(&parsed.Model) {
				return prepare.Args{}, false
			}
		case strings.HasPrefix(token, "--model="):
			parsed.Model = strings.TrimPrefix(token, "--model=")
		case token == "--reasoning":
			if !take(&parsed.Reasoning) {
				return prepare.Args{}, false
			}
		case strings.HasPrefix(token, "--reasoning="):
			parsed.Reasoning = strings.TrimPrefix(token, "--reasoning=")
		case token == "--same-as-you":
			parsed.SameAsYou = true
		case token == "--preset":
			if !take(&parsed.Preset) {
				return prepare.Args{}, false
			}
			parsed.PresetSet = true
		case strings.HasPrefix(token, "--preset="):
			parsed.Preset = strings.TrimPrefix(token, "--preset=")
			parsed.PresetSet = true
		case token == "--base":
			if !take(&parsed.Base) {
				return prepare.Args{}, false
			}
		case strings.HasPrefix(token, "--base="):
			parsed.Base = strings.TrimPrefix(token, "--base=")
		case token == "--kind":
			if !take(&parsed.Kind) {
				return prepare.Args{}, false
			}
		case strings.HasPrefix(token, "--kind="):
			parsed.Kind = strings.TrimPrefix(token, "--kind=")
		case token == "--approved":
			parsed.Approved = true
		case token == "--arg":
			if i+1 >= len(tokens) {
				return prepare.Args{}, false
			}
			i++
			parsed.Extra = append(parsed.Extra, tokens[i])
		case strings.HasPrefix(token, "--arg="):
			parsed.Extra = append(parsed.Extra, strings.TrimPrefix(token, "--arg="))
		default:
			return prepare.Args{}, false
		}
	}
	if parsed.Brief == "" {
		return prepare.Args{}, false
	}
	if parsed.Kind != "ship" && parsed.Kind != "scout" {
		return prepare.Args{}, false
	}
	return parsed, true
}

func (o *rootOptions) runReview(cmd *cobra.Command, args []string) error {
	taskID, verdict, candidate, toolName, text, file, policyReviewed, ok := parseReviewArgs(args)
	if !ok {
		return fmt.Errorf("invalid review arguments")
	}
	st, err := o.openStore("review")
	if err != nil {
		return err
	}
	body, err := app.TextInput(text, file)
	if err != nil {
		return err
	}
	view, err := review.Run(st, taskID, verdict, candidate, toolName, body, policyReviewed, app.OptionalContext(o.installRoot))
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func parseReviewArgs(tokens []string) (taskID, verdict, candidate, toolName, text, file string, policyReviewed, ok bool) {
	textSet, fileSet := false, false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--verdict":
			if i+1 >= len(tokens) {
				return "", "", "", "", "", "", false, false
			}
			i++
			verdict = tokens[i]
		case strings.HasPrefix(token, "--verdict="):
			verdict = strings.TrimPrefix(token, "--verdict=")
		case token == "--candidate":
			if i+1 >= len(tokens) {
				return "", "", "", "", "", "", false, false
			}
			i++
			candidate = tokens[i]
		case strings.HasPrefix(token, "--candidate="):
			candidate = strings.TrimPrefix(token, "--candidate=")
		case token == "--tool":
			if i+1 >= len(tokens) {
				return "", "", "", "", "", "", false, false
			}
			i++
			toolName = tokens[i]
		case strings.HasPrefix(token, "--tool="):
			toolName = strings.TrimPrefix(token, "--tool=")
		case token == "--policy-reviewed":
			policyReviewed = true
		case token == "--text":
			if textSet || fileSet || i+1 >= len(tokens) {
				return "", "", "", "", "", "", false, false
			}
			i++
			text = tokens[i]
			textSet = true
		case strings.HasPrefix(token, "--text="):
			if textSet || fileSet {
				return "", "", "", "", "", "", false, false
			}
			text = strings.TrimPrefix(token, "--text=")
			textSet = true
		case token == "--file":
			if textSet || fileSet || i+1 >= len(tokens) {
				return "", "", "", "", "", "", false, false
			}
			i++
			file = tokens[i]
			fileSet = true
		case strings.HasPrefix(token, "--file="):
			if textSet || fileSet {
				return "", "", "", "", "", "", false, false
			}
			file = strings.TrimPrefix(token, "--file=")
			fileSet = true
		case strings.HasPrefix(token, "-"):
			return "", "", "", "", "", "", false, false
		case taskID == "":
			taskID = token
		default:
			return "", "", "", "", "", "", false, false
		}
	}
	if taskID == "" || verdict == "" || (!textSet && !fileSet) {
		return "", "", "", "", "", "", false, false
	}
	return taskID, verdict, candidate, toolName, text, file, policyReviewed, true
}

func parseStartArgs(tokens []string) (taskID string, extra []string, ok bool) {
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--arg":
			if i+1 >= len(tokens) {
				return "", nil, false
			}
			i++
			extra = append(extra, tokens[i])
		case strings.HasPrefix(token, "--arg="):
			extra = append(extra, strings.TrimPrefix(token, "--arg="))
		case strings.HasPrefix(token, "-"):
			return "", nil, false
		case taskID == "":
			taskID = token
		default:
			return "", nil, false
		}
	}
	if taskID == "" {
		return "", nil, false
	}
	return taskID, extra, true
}

func (o *rootOptions) runEnv(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("command is required")
	}
	switch args[0] {
	case "show":
		taskID, maxChars, ok := parseEnvShowArgs(args[1:])
		if !ok {
			return usageError("env show", args[1:])
		}
		st, err := o.openStore("env-show")
		if err != nil {
			return err
		}
		view, err := environment.Show(st, taskID, o.sumctlPath(), maxChars)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "discover":
		if len(args) != 2 || strings.HasPrefix(args[1], "-") {
			return usageError("env discover", args[1:])
		}
		st, err := o.openStore("env-discover")
		if err != nil {
			return err
		}
		view, err := environment.Discover(st, args[1], app.OptionalContext(o.installRoot), o.sumctlPath())
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "inspect":
		if len(args) != 2 || strings.HasPrefix(args[1], "-") {
			return usageError("env inspect", args[1:])
		}
		st, err := o.openStore("env-inspect")
		if err != nil {
			return err
		}
		view, err := environment.Inspect(st, environment.InspectArgs{Task: args[1], RuntimeRoot: o.runtimeRoot}, app.OptionalContext(o.installRoot))
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "record":
		parsed, ok := parseEnvRecordArgs(args[1:])
		if !ok {
			return usageError("env record", args[1:])
		}
		st, err := o.openStore("env-record")
		if err != nil {
			return err
		}
		parsed.RuntimeRoot = o.runtimeRoot
		view, err := environment.Record(st, parsed, app.OptionalContext(o.installRoot))
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "start":
		parsed, ok := parseEnvStartArgs(args[1:])
		if !ok {
			return usageError("env start", args[1:])
		}
		st, err := o.openStore("env-start")
		if err != nil {
			return err
		}
		parsed.RuntimeRoot = o.runtimeRoot
		view, err := environment.Start(st, parsed, app.OptionalContext(o.installRoot))
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "stop":
		parsed, ok := parseEnvStopArgs(args[1:])
		if !ok {
			return usageError("env stop", args[1:])
		}
		st, err := o.openStore("env-stop")
		if err != nil {
			return err
		}
		parsed.RuntimeRoot = o.runtimeRoot
		view, err := environment.Stop(st, parsed)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	default:
		return usageError("env", args)
	}
}

func parseEnvRecordArgs(tokens []string) (environment.RecordArgs, bool) {
	var parsed environment.RecordArgs
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
		case token == "--url":
			if !take(&parsed.URL) {
				return environment.RecordArgs{}, false
			}
		case strings.HasPrefix(token, "--url="):
			parsed.URL = strings.TrimPrefix(token, "--url=")
		case token == "--log":
			if !take(&parsed.Log) {
				return environment.RecordArgs{}, false
			}
		case strings.HasPrefix(token, "--log="):
			parsed.Log = strings.TrimPrefix(token, "--log=")
		case token == "--pane":
			if !take(&parsed.Pane) {
				return environment.RecordArgs{}, false
			}
		case strings.HasPrefix(token, "--pane="):
			parsed.Pane = strings.TrimPrefix(token, "--pane=")
		case token == "--container":
			if !take(&parsed.Container) {
				return environment.RecordArgs{}, false
			}
		case strings.HasPrefix(token, "--container="):
			parsed.Container = strings.TrimPrefix(token, "--container=")
		case token == "--label":
			if !take(&parsed.Label) {
				return environment.RecordArgs{}, false
			}
		case strings.HasPrefix(token, "--label="):
			parsed.Label = strings.TrimPrefix(token, "--label=")
		case token == "--ownership":
			if !take(&parsed.Ownership) {
				return environment.RecordArgs{}, false
			}
		case strings.HasPrefix(token, "--ownership="):
			parsed.Ownership = strings.TrimPrefix(token, "--ownership=")
		case strings.HasPrefix(token, "-"):
			return environment.RecordArgs{}, false
		case parsed.Task == "":
			parsed.Task = token
		default:
			return environment.RecordArgs{}, false
		}
	}
	if parsed.Task == "" {
		return environment.RecordArgs{}, false
	}
	return parsed, true
}

func parseEnvStartArgs(tokens []string) (environment.StartArgs, bool) {
	var parsed environment.StartArgs
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
		case token == "--command":
			if !take(&parsed.Command) {
				return environment.StartArgs{}, false
			}
		case strings.HasPrefix(token, "--command="):
			parsed.Command = strings.TrimPrefix(token, "--command=")
		case token == "--source":
			if !take(&parsed.Source) {
				return environment.StartArgs{}, false
			}
		case strings.HasPrefix(token, "--source="):
			parsed.Source = strings.TrimPrefix(token, "--source=")
		case token == "--url":
			if !take(&parsed.URL) {
				return environment.StartArgs{}, false
			}
		case strings.HasPrefix(token, "--url="):
			parsed.URL = strings.TrimPrefix(token, "--url=")
		case token == "--match":
			if !take(&parsed.Match) {
				return environment.StartArgs{}, false
			}
		case strings.HasPrefix(token, "--match="):
			parsed.Match = strings.TrimPrefix(token, "--match=")
		case token == "--log":
			if !take(&parsed.Log) {
				return environment.StartArgs{}, false
			}
		case strings.HasPrefix(token, "--log="):
			parsed.Log = strings.TrimPrefix(token, "--log=")
		case token == "--label":
			if !take(&parsed.Label) {
				return environment.StartArgs{}, false
			}
		case strings.HasPrefix(token, "--label="):
			parsed.Label = strings.TrimPrefix(token, "--label=")
		case token == "--timeout":
			if i+1 >= len(tokens) {
				return environment.StartArgs{}, false
			}
			i++
			n, err := strconv.Atoi(tokens[i])
			if err != nil {
				return environment.StartArgs{}, false
			}
			parsed.Timeout = n
			parsed.TimeoutSet = true
		case strings.HasPrefix(token, "--timeout="):
			n, err := strconv.Atoi(strings.TrimPrefix(token, "--timeout="))
			if err != nil {
				return environment.StartArgs{}, false
			}
			parsed.Timeout = n
			parsed.TimeoutSet = true
		case strings.HasPrefix(token, "-"):
			return environment.StartArgs{}, false
		case parsed.Task == "":
			parsed.Task = token
		default:
			return environment.StartArgs{}, false
		}
	}
	if parsed.Task == "" || parsed.Command == "" {
		return environment.StartArgs{}, false
	}
	return parsed, true
}

func parseEnvStopArgs(tokens []string) (environment.StopArgs, bool) {
	var parsed environment.StopArgs
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--service":
			if i+1 >= len(tokens) {
				return environment.StopArgs{}, false
			}
			i++
			parsed.Service = tokens[i]
		case strings.HasPrefix(token, "--service="):
			parsed.Service = strings.TrimPrefix(token, "--service=")
		case token == "--timeout":
			if i+1 >= len(tokens) {
				return environment.StopArgs{}, false
			}
			i++
			n, err := strconv.Atoi(tokens[i])
			if err != nil {
				return environment.StopArgs{}, false
			}
			parsed.Timeout = n
			parsed.TimeoutSet = true
		case strings.HasPrefix(token, "--timeout="):
			n, err := strconv.Atoi(strings.TrimPrefix(token, "--timeout="))
			if err != nil {
				return environment.StopArgs{}, false
			}
			parsed.Timeout = n
			parsed.TimeoutSet = true
		case strings.HasPrefix(token, "-"):
			return environment.StopArgs{}, false
		case parsed.Task == "":
			parsed.Task = token
		default:
			return environment.StopArgs{}, false
		}
	}
	if parsed.Task == "" {
		return environment.StopArgs{}, false
	}
	return parsed, true
}

func (o *rootOptions) runHook(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("command is required")
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return usageError("hook status", args[1:])
		}
		st, err := store.Open(o.home)
		if err != nil {
			return err
		}
		ctx, ctxErr := store.Context(o.runtimeRoot)
		if ctxErr != nil {
			ctx = nil
		}
		view, err := hookstatus.Status(st, ctx, o.runtimeRoot, o.sumctlPath())
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "enable":
		if len(args) != 1 {
			return usageError("hook enable", args[1:])
		}
		st, err := o.openStore("hook-enable")
		if err != nil {
			return err
		}
		ctx, err := store.Context(o.installRoot)
		if err != nil {
			return err
		}
		view, err := hookstatus.Enable(st, ctx, o.runtimeRoot, o.sumctlPath())
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "disable":
		unlink := false
		if len(args) == 2 && args[1] == "--unlink" {
			unlink = true
		} else if len(args) != 1 {
			return usageError("hook disable", args[1:])
		}
		st, err := o.openStore("hook-disable")
		if err != nil {
			return err
		}
		ctx, err := store.Context(o.installRoot)
		if err != nil {
			return err
		}
		view, err := hookstatus.Disable(st, ctx, o.runtimeRoot, unlink)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "event":
		if len(args) != 1 {
			return usageError("hook event", args[1:])
		}
		st, err := store.Open(o.home)
		if err != nil {
			return err
		}
		environ := map[string]string{}
		for _, e := range os.Environ() {
			if i := strings.IndexByte(e, '='); i > 0 {
				environ[e[:i]] = e[i+1:]
			}
		}
		view, err := hookstatus.Event(st, environ, o.runtimeRoot, o.sumctlPath())
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	default:
		return usageError("hook", args)
	}
}

func (o *rootOptions) runVerify(cmd *cobra.Command, args []string) error {
	parsed, ok := parseVerifyArgs(args)
	if !ok {
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
	view, err := verifycmd.Run(st, ctx, parsed)
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func parseVerifyArgs(tokens []string) (verifycmd.Args, bool) {
	var parsed verifycmd.Args
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
		case token == "--candidate":
			if !take(&parsed.Candidate) {
				return verifycmd.Args{}, false
			}
		case strings.HasPrefix(token, "--candidate="):
			parsed.Candidate = strings.TrimPrefix(token, "--candidate=")
		case token == "--result":
			if !take(&parsed.Result) {
				return verifycmd.Args{}, false
			}
		case strings.HasPrefix(token, "--result="):
			parsed.Result = strings.TrimPrefix(token, "--result=")
		case token == "--run":
			if !take(&parsed.Run) {
				return verifycmd.Args{}, false
			}
		case strings.HasPrefix(token, "--run="):
			parsed.Run = strings.TrimPrefix(token, "--run=")
		case token == "--execute":
			parsed.Execute = true
		case token == "--base":
			if !take(&parsed.Base) {
				return verifycmd.Args{}, false
			}
		case strings.HasPrefix(token, "--base="):
			parsed.Base = strings.TrimPrefix(token, "--base=")
		case token == "--text":
			if !take(&parsed.Text) {
				return verifycmd.Args{}, false
			}
		case strings.HasPrefix(token, "--text="):
			parsed.Text = strings.TrimPrefix(token, "--text=")
		case token == "--file":
			if !take(&parsed.File) {
				return verifycmd.Args{}, false
			}
		case strings.HasPrefix(token, "--file="):
			parsed.File = strings.TrimPrefix(token, "--file=")
		case strings.HasPrefix(token, "-"):
			return verifycmd.Args{}, false
		case parsed.Task == "":
			parsed.Task = token
		default:
			return verifycmd.Args{}, false
		}
	}
	if parsed.Task == "" || parsed.Candidate == "" {
		return verifycmd.Args{}, false
	}
	return parsed, true
}

func (o *rootOptions) runPR(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("command is required")
	}
	st, err := o.openStore("pr-" + args[0])
	if err != nil {
		return err
	}
	ctx, err := store.Context(o.installRoot)
	if err != nil {
		return err
	}
	switch args[0] {
	case "reconcile":
		parsed, ok := parsePRReconcileArgs(args[1:])
		if !ok {
			return usageError("pr reconcile", args[1:])
		}
		view, err := prcmd.Reconcile(st, ctx, o.runtimeRoot, parsed)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "evidence":
		taskID, run, visibility, ok := parsePREvidenceArgs(args[1:])
		if !ok {
			return usageError("pr evidence", args[1:])
		}
		view, err := prcmd.Evidence(st, ctx, taskID, run, visibility)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	default:
		return usageError("pr", args)
	}
}

func parsePRReconcileArgs(tokens []string) (prcmd.ReconcileArgs, bool) {
	var parsed prcmd.ReconcileArgs
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--number":
			if i+1 >= len(tokens) {
				return prcmd.ReconcileArgs{}, false
			}
			i++
			n, err := strconv.Atoi(tokens[i])
			if err != nil {
				return prcmd.ReconcileArgs{}, false
			}
			parsed.Number = n
		case strings.HasPrefix(token, "--number="):
			n, err := strconv.Atoi(strings.TrimPrefix(token, "--number="))
			if err != nil {
				return prcmd.ReconcileArgs{}, false
			}
			parsed.Number = n
		case token == "--repo":
			if i+1 >= len(tokens) {
				return prcmd.ReconcileArgs{}, false
			}
			i++
			parsed.Repo = tokens[i]
		case strings.HasPrefix(token, "--repo="):
			parsed.Repo = strings.TrimPrefix(token, "--repo=")
		case token == "--replace":
			parsed.Replace = true
		case strings.HasPrefix(token, "-"):
			return prcmd.ReconcileArgs{}, false
		case parsed.Task == "":
			parsed.Task = token
		default:
			return prcmd.ReconcileArgs{}, false
		}
	}
	if parsed.Task == "" || parsed.Number == 0 {
		return prcmd.ReconcileArgs{}, false
	}
	return parsed, true
}

func parsePREvidenceArgs(tokens []string) (taskID, run, visibility string, ok bool) {
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--run":
			if i+1 >= len(tokens) {
				return "", "", "", false
			}
			i++
			run = tokens[i]
		case strings.HasPrefix(token, "--run="):
			run = strings.TrimPrefix(token, "--run=")
		case token == "--visibility":
			if i+1 >= len(tokens) {
				return "", "", "", false
			}
			i++
			visibility = tokens[i]
		case strings.HasPrefix(token, "--visibility="):
			visibility = strings.TrimPrefix(token, "--visibility=")
		case token == "--scenario" || strings.HasPrefix(token, "--scenario="):
			if token == "--scenario" {
				i++
			}
		case token == "--evidence-root" || strings.HasPrefix(token, "--evidence-root="):
			if token == "--evidence-root" {
				i++
			}
		case token == "--verification-run" || strings.HasPrefix(token, "--verification-run="):
			if token == "--verification-run" {
				i++
			}
		case token == "--timeout" || strings.HasPrefix(token, "--timeout="):
			if token == "--timeout" {
				i++
			}
		case token == "--dry-run" || token == "--allow-head-mismatch" || token == "--replace-foreign-block":
		case strings.HasPrefix(token, "-"):
			return "", "", "", false
		case taskID == "":
			taskID = token
		default:
			return "", "", "", false
		}
	}
	if taskID == "" || run == "" || visibility == "" {
		return "", "", "", false
	}
	return taskID, run, visibility, true
}

func (o *rootOptions) runCleanup(cmd *cobra.Command, args []string) error {
	parsed, ok := parseCleanupArgs(args)
	if !ok {
		return usageError("cleanup", args)
	}
	st, err := o.openStore("cleanup")
	if err != nil {
		return err
	}
	ctx, err := store.Context(o.installRoot)
	if err != nil {
		return err
	}
	view, err := cleanup.Run(st, ctx, o.runtimeRoot, parsed)
	if err != nil {
		return err
	}
	return emitOrdjson(cmd.OutOrStdout(), view)
}

func parseCleanupArgs(tokens []string) (cleanup.Args, bool) {
	var parsed cleanup.Args
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--apply":
			parsed.Apply = true
		case token == "--reviewer-only":
			parsed.ReviewerOnly = true
		case token == "--number":
			if i+1 >= len(tokens) {
				return cleanup.Args{}, false
			}
			i++
			n, err := strconv.Atoi(tokens[i])
			if err != nil {
				return cleanup.Args{}, false
			}
			parsed.Number = n
		case strings.HasPrefix(token, "--number="):
			n, err := strconv.Atoi(strings.TrimPrefix(token, "--number="))
			if err != nil {
				return cleanup.Args{}, false
			}
			parsed.Number = n
		case strings.HasPrefix(token, "-"):
			return cleanup.Args{}, false
		case parsed.Task == "":
			parsed.Task = token
		default:
			return cleanup.Args{}, false
		}
	}
	if parsed.Task == "" {
		return cleanup.Args{}, false
	}
	return parsed, true
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

func (o *rootOptions) runRefresh(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("command is required")
	}
	st, err := o.openStore("refresh-" + args[0])
	if err != nil {
		return err
	}
	switch args[0] {
	case "status":
		tasks, ok := parseRepeatableTask(args[1:])
		if !ok {
			return usageError("refresh status", args[1:])
		}
		view, err := refreshcmd.Status(st, tasks)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "request":
		tasks, coordinator, ok := parseRefreshRequest(args[1:])
		if !ok {
			return usageError("refresh request", args[1:])
		}
		ctx, err := store.Context(o.installRoot)
		if err != nil {
			return err
		}
		view, err := refreshcmd.Request(st, ctx, tasks, coordinator, o.runtimeRoot, o.sumctlPath())
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "adopt":
		revision, ok := parseRefreshAdopt(args[1:])
		if !ok {
			return usageError("refresh adopt", args[1:])
		}
		ctx, err := store.Context(o.installRoot)
		if err != nil {
			return err
		}
		view, err := refreshcmd.AdoptCoordinator(st, ctx, revision)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	default:
		return usageError("refresh", args)
	}
}

func parseRepeatableTask(tokens []string) ([]string, bool) {
	var tasks []string
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--task":
			if i+1 >= len(tokens) {
				return nil, false
			}
			i++
			tasks = append(tasks, tokens[i])
		case strings.HasPrefix(token, "--task="):
			tasks = append(tasks, strings.TrimPrefix(token, "--task="))
		default:
			return nil, false
		}
	}
	return tasks, true
}

func parseRefreshRequest(tokens []string) ([]string, bool, bool) {
	var tasks []string
	coordinator := false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--task":
			if i+1 >= len(tokens) {
				return nil, false, false
			}
			i++
			tasks = append(tasks, tokens[i])
		case strings.HasPrefix(token, "--task="):
			tasks = append(tasks, strings.TrimPrefix(token, "--task="))
		case token == "--coordinator":
			coordinator = true
		default:
			return nil, false, false
		}
	}
	return tasks, coordinator, true
}

func parseRefreshAdopt(tokens []string) (string, bool) {
	coordinator := false
	revision := ""
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--coordinator":
			coordinator = true
		case strings.HasPrefix(token, "-"):
			return "", false
		case revision == "":
			revision = token
		default:
			return "", false
		}
	}
	if !coordinator || revision == "" {
		return "", false
	}
	return revision, true
}

func (o *rootOptions) runUpdate(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("command is required")
	}
	st, err := o.openStore("update-" + args[0])
	if err != nil {
		return err
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return usageError("update status", args[1:])
		}
		view, err := updatecmd.Status(st)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "check", "stage", "apply":
		ref, noFetch, ok := parseUpdateRef(args[1:])
		if !ok {
			return usageError("update "+args[0], args[1:])
		}
		ctx := app.OptionalContext(o.installRoot)
		var view *ordjson.Object
		var err error
		switch args[0] {
		case "check":
			view, err = updatecmd.Check(st, o.runtimeRoot, ref, noFetch)
		case "stage":
			view, err = updatecmd.Stage(st, ctx, ref, noFetch)
		case "apply":
			view, err = updatecmd.Apply(st, ctx, ref, noFetch)
		}
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "rollback":
		to, ok := parseUpdateTo(args[1:])
		if !ok {
			return usageError("update rollback", args[1:])
		}
		ctx, err := store.Context(o.installRoot)
		if err != nil {
			return err
		}
		view, err := updatecmd.Rollback(st, ctx, to)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	case "recover":
		generation, ok := parseUpdateRecover(args[1:])
		if !ok {
			return usageError("update recover", args[1:])
		}
		ctx, err := store.Context(o.installRoot)
		if err != nil {
			return err
		}
		view, err := updatecmd.Recover(st, ctx, generation)
		if err != nil {
			return err
		}
		return emitOrdjson(cmd.OutOrStdout(), view)
	default:
		return usageError("update", args)
	}
}

func parseUpdateRef(tokens []string) (ref string, noFetch, ok bool) {
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--ref":
			if i+1 >= len(tokens) {
				return "", false, false
			}
			i++
			ref = tokens[i]
		case strings.HasPrefix(token, "--ref="):
			ref = strings.TrimPrefix(token, "--ref=")
		case token == "--no-fetch":
			noFetch = true
		default:
			return "", false, false
		}
	}
	return ref, noFetch, true
}

func parseUpdateTo(tokens []string) (string, bool) {
	to := ""
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--to":
			if i+1 >= len(tokens) {
				return "", false
			}
			i++
			to = tokens[i]
		case strings.HasPrefix(token, "--to="):
			to = strings.TrimPrefix(token, "--to=")
		default:
			return "", false
		}
	}
	return to, true
}

func parseUpdateRecover(tokens []string) (string, bool) {
	generation := ""
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch {
		case token == "--generation":
			if i+1 >= len(tokens) {
				return "", false
			}
			i++
			generation = tokens[i]
		case strings.HasPrefix(token, "--generation="):
			generation = strings.TrimPrefix(token, "--generation=")
		default:
			return "", false
		}
	}
	return generation, generation != ""
}
