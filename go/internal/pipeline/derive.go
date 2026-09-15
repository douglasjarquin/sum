package pipeline

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

// Derive computes every row from the task record alone, so the pipeline can always be rebuilt and never drifts from the evidence.
func Derive(task *ordjson.Object) Record {
	candidate := Candidate(task)
	record := New(stringField(task, "id"), candidate)
	record.Set(deriveIntent(task))
	record.Set(deriveTest(task, candidate))
	record.Set(deriveReview(task, candidate))
	record.Set(deriveDocument(task, candidate))
	record.Set(derivePR(task))
	record.Set(gateRow(task, StageRebase, rebaseGate, rebaseUnobserved, rebaseOutcomes))
	record.Set(gateRow(task, StageLint, lintGate, lintUnobserved, lintOutcomes))
	record.Set(derivePush(task, candidate))
	record.Set(deriveCI(task))
	return record
}

// Candidate is the SHA this task's delivery is about: the reported candidate, else the latest handoff's.
func Candidate(task *ordjson.Object) string {
	report, _ := field(task, "report").(*ordjson.Object)
	if candidate := stringField(report, "candidate"); candidate != "" {
		return candidate
	}
	candidate := ""
	for _, record := range records(task) {
		if stringField(record, "kind") == "handoff" {
			if value := stringField(record, "candidate"); value != "" {
				candidate = value
			}
		}
	}
	return candidate
}

func deriveIntent(task *ordjson.Object) Row {
	row := Row{Stage: StageIntent}
	if stringField(task, "brief") == "" {
		row.Status = Fail
		row.Result = "No approved brief"
		return row
	}
	fingerprint := versions.ApprovedFingerprint(task)
	if stringField(fingerprint, "sha256") == "" {
		row.Status = Fail
		row.Result = "No approved brief"
		return row
	}
	row.Status = Pass
	row.Result = "Approved brief recorded"
	return row
}

func deriveTest(task *ordjson.Object, candidate string) Row {
	row := Row{Stage: StageTest, Status: Pending}
	latest := latestFor(task, "verification", "coordinator", candidate)
	if latest == nil {
		row.Result = "No coordinator verification for this candidate"
		return row
	}
	row.At = stringField(latest, "at")
	row.Evidence = []string{stringField(latest, "id")}
	runID := stringField(latest, "run_id")
	suffix := ""
	if runID != "" {
		suffix = fmt.Sprintf(" (`mise run verify`, run %s)", runID)
	}
	missing, waived := evidenceGap(latest)
	result := stringField(latest, "result")
	switch {
	case len(missing) > 0 && !waived && (result == "pass" || result == "blocked"):
		row.Status = Blocked
		row.Result = "Passed checks; missing before/after evidence for " + strings.Join(missing, ", ") + " (the worker captures with `.agents/skills/evidence/`)" + suffix
		row.Advice = "The worker captures evidence for " + strings.Join(missing, ", ") + "; send `repair send` with the evidence instruction, or the user waives with `verify --accept-missing-evidence`"
	case result == "pass":
		row.Status = Pass
		row.Result = "Passed" + suffix
		if waived && len(missing) > 0 {
			row.Result = "Passed; evidence waived for " + strings.Join(missing, ", ") + suffix
		}
	case result == "fail":
		row.Status = Fail
		row.Result = "Failed" + suffix
	default:
		row.Status = Blocked
		reason := stringField(latest, "blocked_reason")
		if reason == "" {
			reason = result
		}
		row.Result = "Blocked" + suffix + ": " + reason
	}
	row.Result += verificationCaveats(latest, candidate)
	return row
}

// evidenceWaiverRecorded reports whether any verification of this candidate carries a waiver, not only the latest one.
// A rerun after the waiver leaves the decision recorded but no longer current, which is what `--allow-missing-evidence` reads.
func evidenceWaiverRecorded(task *ordjson.Object, candidate string) bool {
	for _, record := range records(task) {
		if stringField(record, "kind") != "verification" || stringField(record, "source") != "coordinator" {
			continue
		}
		if stringField(record, "candidate") != candidate {
			continue
		}
		if waived, _ := field(record, "evidence_waived").(bool); waived {
			return true
		}
	}
	return false
}

// evidenceGap reads one verification record: the scenarios it found no before/after comparison for, and its waiver.
func evidenceGap(record *ordjson.Object) ([]string, bool) {
	if record == nil {
		return nil, false
	}
	waived, _ := field(record, "evidence_waived").(bool)
	rows, _ := field(record, "evidence_missing").([]any)
	var missing []string
	for _, raw := range rows {
		row, _ := raw.(*ordjson.Object)
		if row == nil {
			continue
		}
		if scenario := stringField(row, "scenario"); scenario != "" {
			missing = append(missing, scenario)
		}
	}
	return missing, waived
}

// verificationCaveats names what the run itself says is unsettled, so a pass row never reads as more than the run proved.
func verificationCaveats(record *ordjson.Object, candidate string) string {
	var notes []string
	if certifies := stringField(record, "certifies"); certifies != candidate {
		notes = append(notes, "the run certifies no candidate")
	}
	if requires, _ := field(record, "requires_root_review").(bool); requires {
		notes = append(notes, "requires root review")
	}
	if len(notes) == 0 {
		return ""
	}
	return "; " + strings.Join(notes, ", ")
}

func deriveReview(task *ordjson.Object, candidate string) Row {
	row := Row{Stage: StageReview, Status: Pending}
	reviews := Reviews(task, candidate)
	if len(reviews) == 0 {
		row.Result = "No review recorded for this candidate"
		return row
	}
	for _, record := range reviews {
		row.Evidence = append(row.Evidence, stringField(record, "id"))
	}
	decisive := decisiveReview(reviews)
	if decisive == nil {
		row.At = stringField(reviews[len(reviews)-1], "at")
		row.Result = "Comments only; no verdict on this candidate"
		return row
	}
	row.At = stringField(decisive, "at")
	passes := RemediationPasses(task)
	switch stringField(decisive, "verdict") {
	case "approve":
		row.Status = Pass
		row.Result = "Passed"
		if passes > 0 {
			row.Result = fmt.Sprintf("Passed after %s", plural(passes, "remediation pass", "remediation passes"))
		}
	case "changes-requested":
		row.Status = Fail
		row.Result = "Changes requested"
		if passes > 0 {
			row.Result = fmt.Sprintf("Changes requested after %s", plural(passes, "remediation pass", "remediation passes"))
		}
	default:
		row.Status = Blocked
		row.Result = "Blocked by the reviewer"
	}
	return row
}

// Reviews are the review records that speak for this candidate, oldest first.
// A record that names no candidate is a verdict on the task as it stands, so it counts too.
func Reviews(task *ordjson.Object, candidate string) []*ordjson.Object {
	var out []*ordjson.Object
	for _, record := range records(task) {
		if stringField(record, "kind") != "review" {
			continue
		}
		named := stringField(record, "candidate")
		if named != "" && named != candidate {
			continue
		}
		out = append(out, record)
	}
	return out
}

func decisiveReview(reviews []*ordjson.Object) *ordjson.Object {
	for i := len(reviews) - 1; i >= 0; i-- {
		if verdict := stringField(reviews[i], "verdict"); verdict != "" && verdict != "comment" {
			return reviews[i]
		}
	}
	return nil
}

// RemediationPasses counts the candidates sent back before the approving one, plus the controlled repairs the coordinator spent.
func RemediationPasses(task *ordjson.Object) int {
	seen := map[string]bool{}
	for _, record := range records(task) {
		if stringField(record, "kind") != "review" || stringField(record, "verdict") != "changes-requested" {
			continue
		}
		seen[stringField(record, "candidate")] = true
	}
	repairs, _ := field(task, "repairs").(*ordjson.Object)
	consumed := 0
	if number, isNumber := field(repairs, "consumed").(json.Number); isNumber {
		if value, err := number.Int64(); err == nil && value > 0 {
			consumed = int(value)
		}
	}
	return len(seen) + consumed
}

func deriveDocument(task *ordjson.Object, candidate string) Row {
	row := Row{Stage: StageDocument, Status: Pending}
	latest := latestFor(task, "documentation", "coordinator", candidate)
	if latest == nil {
		row.Result = "No documentation audit for this candidate"
		return row
	}
	row.At = stringField(latest, "at")
	row.Evidence = []string{stringField(latest, "id")}
	row.Result = stringField(latest, "summary")
	switch stringField(latest, "result") {
	case "pass":
		row.Status = Pass
	case "skipped":
		row.Status = Skipped
	default:
		row.Status = Fail
	}
	return row
}

func derivePR(task *ordjson.Object) Row {
	row := Row{Stage: StagePR, Status: Pending}
	pr, _ := field(task, "pr").(*ordjson.Object)
	identity, _ := field(pr, "identity").(*ordjson.Object)
	if identity == nil {
		return unreconciledPR(task, row)
	}
	row.At = stringField(pr, "observed_at")
	findings, _ := field(pr, "findings").([]any)
	if len(findings) > 0 {
		row.Status = Blocked
		row.Result = fmt.Sprint(findings[0])
		return row
	}
	switch stringField(pr, "state") {
	case "open":
		row.Status = Pass
		row.Result = "Open: " + stringField(identity, "url")
	case "merged":
		row.Status = Pass
		row.Result = "Merged"
	case "closed":
		row.Status = Fail
		row.Result = "Closed without merging: " + stringField(identity, "url")
	default:
		row.Result = "PR state is not recorded"
	}
	return row
}

// unreconciledPR reads the PR stage's own record. A refusal is the one thing a coordinator must act on before this
// gate can move, so it blocks the row with its reason instead of reading as work nobody has started.
func unreconciledPR(task *ordjson.Object, row Row) Row {
	row.Result = "No PR reconciled for this task"
	latest := latestFor(task, prGate, "coordinator", Candidate(task))
	if stringField(latest, "action") != prRefused {
		return row
	}
	row.Status = Blocked
	row.Result = stringField(latest, "summary")
	row.At = stringField(latest, "at")
	row.Evidence = []string{stringField(latest, "id")}
	return row
}

func latestFor(task *ordjson.Object, kind, source, candidate string) *ordjson.Object {
	var latest *ordjson.Object
	for _, record := range records(task) {
		if stringField(record, "kind") != kind || stringField(record, "source") != source {
			continue
		}
		if stringField(record, "candidate") != candidate {
			continue
		}
		latest = record
	}
	return latest
}

func records(task *ordjson.Object) []*ordjson.Object {
	items, _ := field(task, "evidence").([]any)
	out := make([]*ordjson.Object, 0, len(items))
	for _, raw := range items {
		if record, isObject := raw.(*ordjson.Object); isObject {
			out = append(out, record)
		}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
