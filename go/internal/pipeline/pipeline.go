// Package pipeline records the delivery gates one sum task passes through and renders them as one table.
// The gates are fixed and ordered; a record always carries exactly one row per gate, so no caller branches on a missing stage.
package pipeline

import "strings"

type Stage string

const (
	StageIntent   Stage = "intent"
	StageRebase   Stage = "rebase"
	StageReview   Stage = "review"
	StageTest     Stage = "test"
	StageDocument Stage = "document"
	StageLint     Stage = "lint"
	StagePush     Stage = "push"
	StagePR       Stage = "pr"
	StageCI       Stage = "ci"
)

type Status string

const (
	Pending     Status = "pending"
	Pass        Status = "pass"
	Fail        Status = "fail"
	Blocked     Status = "blocked"
	Skipped     Status = "skipped"
	NotDeclared Status = "not_declared"
)

type Definition struct {
	Stage   Stage
	Display string
}

// Stages is the pipeline, in order. Everything else reads this table instead of repeating the order.
var Stages = [...]Definition{
	{StageIntent, "Intent"},
	{StageRebase, "Rebase"},
	{StageReview, "Review"},
	{StageTest, "Test"},
	{StageDocument, "Document"},
	{StageLint, "Lint"},
	{StagePush, "Push"},
	{StagePR, "PR"},
	{StageCI, "CI"},
}

const Count = len(Stages)

var marks = map[Status]string{
	Pass:        "✅",
	Fail:        "❌",
	Blocked:     "⛔",
	Skipped:     "⏭️",
	NotDeclared: "➖",
	Pending:     "⏳",
}

var statuses = map[Status]bool{Pending: true, Pass: true, Fail: true, Blocked: true, Skipped: true, NotDeclared: true}

// Mark falls back to the pending emoji for a status this release does not know.
func Mark(status Status) string {
	if mark, known := marks[status]; known {
		return mark
	}
	return marks[Pending]
}

// Index reports -1 for a stage that is not a gate.
func Index(stage Stage) int {
	for i, definition := range Stages {
		if definition.Stage == stage {
			return i
		}
	}
	return -1
}

type Row struct {
	Stage    Stage
	Status   Status
	Result   string
	At       string
	Evidence []string
}

func (r Row) same(other Row) bool {
	if r.Stage != other.Stage || r.Status != other.Status || r.Result != other.Result {
		return false
	}
	if len(r.Evidence) != len(other.Evidence) {
		return false
	}
	for i := range r.Evidence {
		if r.Evidence[i] != other.Evidence[i] {
			return false
		}
	}
	return true
}

type Record struct {
	Schema    int
	Task      string
	Candidate string
	Rows      [Count]Row
	UpdatedAt string
}

// New is an all-pending record: what a task that has reached no gate yet looks like.
func New(taskID, candidate string) Record {
	record := Record{Schema: Schema, Task: taskID, Candidate: candidate}
	for i, definition := range Stages {
		record.Rows[i] = Row{Stage: definition.Stage, Status: Pending}
	}
	return record
}

// Set replaces one row in place. It keeps the row's stage, so a caller cannot write a row under the wrong gate.
func (r *Record) Set(row Row) {
	i := Index(row.Stage)
	if i < 0 {
		return
	}
	if !statuses[row.Status] {
		row.Status = Pending
	}
	r.Rows[i] = row
}

func (r Record) Get(stage Stage) Row {
	i := Index(stage)
	if i < 0 {
		return Row{Stage: stage, Status: Pending}
	}
	return r.Rows[i]
}

func (r Record) sameRows(other Record) bool {
	if r.Candidate != other.Candidate {
		return false
	}
	for i := range r.Rows {
		if !r.Rows[i].same(other.Rows[i]) {
			return false
		}
	}
	return true
}

func (r Record) Table() string {
	var out strings.Builder
	out.WriteString("| Stage | Status | Result |\n|---|:---:|---|\n")
	for i, definition := range Stages {
		row := r.Rows[i]
		out.WriteString("| " + definition.Display + " | " + Mark(row.Status) + " | " + cell(row.Result) + " |\n")
	}
	return out.String()
}

// cell keeps a result text inside one table cell: pipes are escaped and line breaks folded.
func cell(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "|", "\\|")
	text = strings.TrimSpace(text)
	if text == "" {
		return "&nbsp;"
	}
	return text
}
