package metadata

import (
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/shquote"
)

var TaskTokens = []string{"sum_state", "sum_task", "sum_repo", "sum_rev", "sum_pr"}

var RootTokens = []string{"sum_inbox", "sum_tasks"}

var SumStates = []string{
	"needs-attention", "needs-decision", "merged-cleanup-pending", "review-ready", "attention-blocked", "attention-exited",
	"attention-closed", "attention-idle", "instruction-refresh-pending", "answer-pending", "pr-open", "verified", "preparing", "running",
}

func snippetTOML() string {
	return strings.Join([]string{
		"# sum: optional sidebar rows that render sum's task tokens. Merge into ~/.config/herdr/config.toml, then run",
		"# `herdr server reload-config`. `rows` replaces the whole layout, so keep the built-in tokens you already use.",
		"[ui.sidebar.agents]",
		`rows = [["state_icon", "workspace", "tab"], ["agent", "$sum_state"], ["$sum_task", "$sum_inbox"]]`,
		"",
		"[ui.sidebar.spaces]",
		`rows = [["state_icon", "workspace"], ["branch", "git_status"], ["$sum_state", "$sum_task"]]`,
		"",
		"# Optional: pop-up notifications for sum transitions need a toast delivery; sum sends them only after `metadata enable --notify`.",
		"# [ui.toast]",
		`# delivery = "herdr"`,
		"",
	}, "\n")
}

func Snippet(sumctlPath, home string) *ordjson.Object {
	tokens := ordjson.NewObject()
	tokens.Set("task", toAny(TaskTokens))
	tokens.Set("coordinator", toAny(RootTokens))

	result := ordjson.NewObject()
	result.Set("toml", snippetTOML())
	result.Set("tokens", tokens)
	result.Set("states", toAny(SumStates))
	result.Set("enable", shquote.CommandFor(sumctlPath, home, "metadata", "enable"))
	result.Set("inbox", shquote.CommandFor(sumctlPath, home, "metadata", "inbox"))
	result.Set("note", "Nothing here is written by sum: the snippet is text for the user to merge. Without these rows the tokens exist but stay out of sight; every other setting (theme, keybindings, labels, toast delivery) is the user's.")
	return result
}

func toAny(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}
