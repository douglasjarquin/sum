package pipeline

import (
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

// The PR body sum generates is a table of sections rather than one long formatter, so a section that has nothing
// to say drops out on its own and a new one is added in one place. Every section reads the task record only.
type bodySection struct {
	heading string
	text    func(task *ordjson.Object) string
}

var bodySections = []bodySection{
	{"Summary", summarySection},
	{"Changes", changesSection},
	{"Verification", verificationSection},
	{"Limitations", limitationsSection},
}

// RenderBody writes the default PR body from the task record alone. It carries neither the pipeline table nor the
// evidence block: reconcile publishes both into their own marked blocks straight after the PR is created.
func RenderBody(task *ordjson.Object) string {
	var parts []string
	for _, section := range bodySections {
		if text := strings.TrimSpace(section.text(task)); text != "" {
			parts = append(parts, "## "+section.heading+"\n\n"+text)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n") + "\n"
}

// Title is the PR title sum proposes: the approved brief's first line, else the branch it is pushing.
func Title(task *ordjson.Object) string {
	line := firstLine(stringField(task, "brief"))
	line = strings.TrimSpace(strings.TrimLeft(line, "#"))
	if line == "" {
		return stringField(task, "branch")
	}
	return boundText(line, 120)
}

func summarySection(task *ordjson.Object) string {
	return firstParagraph(stringField(task, "brief"))
}

// changesSection prefers the worker's own `changes`, and falls back to the handoff `summary` it wrote instead.
func changesSection(task *ordjson.Object) string {
	return handoffText(task, "changes", "summary")
}

func limitationsSection(task *ordjson.Object) string {
	return handoffText(task, "limitations", "gaps")
}

// verificationSection states what the coordinator's own gates recorded, in the words the rows already use, so the
// body cannot claim more than the table published beside it.
func verificationSection(task *ordjson.Object) string {
	record := Derive(task)
	lines := []string{"- Test: " + rowText(record.Get(StageTest))}
	if unexercised := notExercised(task); unexercised != "" {
		lines = append(lines, "- Not exercised: "+unexercised)
	}
	lines = append(lines,
		"- Lint: "+rowText(record.Get(StageLint)),
		"- Document: "+rowText(record.Get(StageDocument)),
	)
	return strings.Join(lines, "\n")
}

func rowText(row Row) string {
	if text := strings.TrimSpace(row.Result); text != "" {
		return text
	}
	return "Nothing recorded (" + string(row.Status) + ")"
}

func notExercised(task *ordjson.Object) string {
	latest := latestFor(task, "verification", "coordinator", Candidate(task))
	items, _ := field(latest, "not_exercised").([]any)
	var names []string
	for _, item := range items {
		if text, isText := item.(string); isText && strings.TrimSpace(text) != "" {
			names = append(names, strings.TrimSpace(text))
		}
	}
	return strings.Join(names, ", ")
}

// handoffText reads the latest worker handoff, which is free-form JSON, so only a string or a list of strings is
// rendered; anything else the worker put there is left out rather than printed as a Go value.
func handoffText(task *ordjson.Object, keys ...string) string {
	handoff := latestHandoff(task)
	for _, key := range keys {
		switch value := field(handoff, key).(type) {
		case string:
			if text := strings.TrimSpace(value); text != "" {
				return text
			}
		case []any:
			var lines []string
			for _, item := range value {
				if text, isText := item.(string); isText && strings.TrimSpace(text) != "" {
					lines = append(lines, "- "+strings.TrimSpace(text))
				}
			}
			if len(lines) > 0 {
				return strings.Join(lines, "\n")
			}
		}
	}
	return ""
}

func latestHandoff(task *ordjson.Object) *ordjson.Object {
	var latest *ordjson.Object
	for _, record := range records(task) {
		if stringField(record, "kind") != "handoff" {
			continue
		}
		if body, isObject := field(record, "handoff").(*ordjson.Object); isObject {
			latest = body
		}
	}
	return latest
}

func firstParagraph(text string) string {
	for _, block := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		if trimmed := strings.TrimSpace(block); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func boundText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}
