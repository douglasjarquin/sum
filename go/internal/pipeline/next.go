package pipeline

import (
	"fmt"
	"strings"
)

// advice is what a coordinator does about a gate that is not settled. It is a table so the sentence for a stage
// is written once and cannot drift between the commands that print it.
var advice = map[Stage]string{
	StageIntent:   "record the approved brief with `prepare` or `dispatch`",
	StageRebase:   "run `sumctl pipeline rebase TASK_ID`; a branch that is behind or conflicting is the worker's to rebase",
	StageReview:   "launch a reviewer and record the verdict with `sumctl review TASK_ID --verdict ... --candidate SHA`",
	StageTest:     "run `sumctl verify TASK_ID --candidate SHA --execute`",
	StageDocument: "run `sumctl pipeline document TASK_ID`",
	StageLint:     "run `sumctl pipeline lint TASK_ID`",
	StagePush:     "run `sumctl pipeline push TASK_ID`",
	StagePR:       "open the PR with `gh pr create`, then `sumctl pr reconcile TASK_ID --number N`",
	StageCI:       "run `sumctl pipeline ci TASK_ID` to read the checks again; sum observes them, it never watches them",
}

// A gate a project does not declare, or one deliberately skipped, needs nothing further; only these three do.
func settled(status Status) bool {
	return status == Pass || status == Skipped || status == NotDeclared
}

func Next(record Record) string {
	for i, definition := range Stages {
		row := record.Rows[i]
		if settled(row.Status) {
			continue
		}
		detail := strings.TrimRight(strings.TrimSpace(row.Result), ".")
		if detail == "" {
			detail = "nothing recorded"
		}
		return fmt.Sprintf("%s is %s: %s. Do: %s.", definition.Display, row.Status, detail, advice[definition.Stage])
	}
	return "Every gate is settled; the merge decision is the user's."
}
