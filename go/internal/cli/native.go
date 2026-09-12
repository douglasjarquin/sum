package cli

import (
	"fmt"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/archive"
	"github.com/douglasjarquin/sum/go/internal/execution"
	"github.com/douglasjarquin/sum/go/internal/guard"
	"github.com/douglasjarquin/sum/go/internal/helpview"
	"github.com/douglasjarquin/sum/go/internal/herdrbridge"
	"github.com/douglasjarquin/sum/go/internal/notes"
	"github.com/douglasjarquin/sum/go/internal/quota"
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
}

func (o *rootOptions) runHelp(cmd *cobra.Command, args []string) error {
	if _, err := o.openStore("help"); err != nil {
		return err
	}
	topic := ""
	if len(args) == 1 && !strings.HasPrefix(args[0], "-") {
		topic = args[0]
	} else if len(args) > 1 {
		return o.compat(cmd.Context(), append([]string{"help"}, args...))
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
		return o.compat(cmd.Context(), append([]string{"quota"}, args...))
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
	format = "compact"
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
	if provider == "" || (format != "json" && format != "compact") {
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
			return o.compat(cmd.Context(), append([]string{"skills"}, args...))
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
			return o.compat(cmd.Context(), append([]string{"skills"}, args...))
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
		return o.compat(cmd.Context(), append([]string{"skills"}, args...))
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
			return o.compat(cmd.Context(), append([]string{"execution"}, args...))
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
	default:
		return o.compat(cmd.Context(), append([]string{"execution"}, args...))
	}
}

func (o *rootOptions) runRepair(cmd *cobra.Command, args []string) error {
	return o.compat(cmd.Context(), append([]string{"repair"}, args...))
}

func (o *rootOptions) runArchive(cmd *cobra.Command, args []string) error {
	taskID, acknowledge, ok := parseArchiveArgs(args)
	if !ok {
		return o.compat(cmd.Context(), append([]string{"archive"}, args...))
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
		return o.compat(cmd.Context(), append([]string{"notes"}, args...))
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
