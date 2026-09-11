package evidenceview

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

var workerRunKeys = []string{"id", "run_id", "result", "outcome", "candidate", "current", "certifies", "requires_root_review"}
var rootRunKeys = []string{"id", "run_id", "result", "outcome", "candidate", "current", "certifies", "requires_root_review", "isolation"}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case []any:
		return len(t) > 0
	case *ordjson.Object:
		return t != nil && t.Len() > 0
	default:
		return v != nil
	}
}

func asObject(v any) *ordjson.Object {
	obj, _ := v.(*ordjson.Object)
	return obj
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func getField(o *ordjson.Object, key string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	return v
}

func pick(o *ordjson.Object, keys []string) *ordjson.Object {
	result := ordjson.NewObject()
	for _, k := range keys {
		result.Set(k, getField(o, k))
	}
	return result
}

func pyStr(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	default:
		return fmt.Sprint(t)
	}
}

func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}
	return "", fmt.Errorf("git: %s", strings.TrimSpace(stderr.String()))
}

// CurrentCandidate ports `current_candidate`: the worktree's live HEAD, or "" (Python's None) when the worktree
// is unset, missing, or git fails.
func CurrentCandidate(task *ordjson.Object) string {
	worktree := asString(getField(task, "worktree"))
	if worktree == "" {
		return ""
	}
	info, err := os.Stat(worktree)
	if err != nil || !info.IsDir() {
		return ""
	}
	out, err := runGit("-C", worktree, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func evidenceRowCopy(record *ordjson.Object, head string) *ordjson.Object {
	row := ordjson.NewObject()
	for _, k := range record.Keys() {
		v, _ := record.Get(k)
		row.Set(k, v)
	}
	candidateValue := getField(record, "candidate")
	var current any
	if !truthy(candidateValue) || head == "" {
		current = nil
	} else {
		current = asString(candidateValue) == head
	}
	row.Set("current", current)
	return row
}

// View ports `evidence_view`: scoped evidence with candidate currency, plus the closure prerequisites this task
// has or lacks. Computed; never stored.
func View(task *ordjson.Object) *ordjson.Object {
	head := CurrentCandidate(task)

	var records []*ordjson.Object
	evidenceValue, _ := task.Get("evidence")
	if list, ok := evidenceValue.([]any); ok {
		for _, ev := range list {
			record := asObject(ev)
			records = append(records, evidenceRowCopy(record, head))
		}
	}

	reportValue, hasReport := task.Get("report")
	report := asObject(reportValue)
	if hasReport && truthy(reportValue) {
		hasReportKind := false
		for _, r := range records {
			if asString(getField(r, "kind")) == "report" {
				hasReportKind = true
				break
			}
		}
		if !hasReportKind {
			legacy := ordjson.NewObject()
			legacy.Set("schema", nil)
			legacy.Set("id", nil)
			legacy.Set("kind", "report")
			legacy.Set("source", "worker")
			legacy.Set("legacy", true)
			legacy.Set("at", getField(report, "submitted_at"))
			legacy.Set("candidate", nil)
			legacy.Set("brief_revision", getField(report, "brief_revision"))
			legacy.Set("current", nil)
			legacy.Set("note", "Legacy prose report recorded before scoped evidence existed; unstructured worker claim.")
			records = append([]*ordjson.Object{legacy}, records...)
		}
	}

	var handoffs, reviews, workerRuns, root []*ordjson.Object
	for _, r := range records {
		kind := asString(getField(r, "kind"))
		source := asString(getField(r, "source"))
		if kind == "handoff" {
			handoffs = append(handoffs, r)
		}
		if kind == "review" {
			reviews = append(reviews, r)
		}
		if kind == "verification" && source == "worker" && truthy(getField(r, "run_id")) {
			workerRuns = append(workerRuns, r)
		}
		if kind == "verification" && source == "coordinator" {
			root = append(root, r)
		}
	}
	var rootCurrent []*ordjson.Object
	for _, r := range root {
		if truthy(getField(r, "current")) {
			rootCurrent = append(rootCurrent, r)
		}
	}
	var rootPass []*ordjson.Object
	for _, r := range rootCurrent {
		if asString(getField(r, "result")) == "pass" {
			rootPass = append(rootPass, r)
		}
	}

	policy := asObject(getField(task, "verification_policy"))
	standardized := policy != nil && asString(getField(policy, "status")) == "standardized"
	pr := asObject(getField(task, "pr"))

	var missing []string
	if len(handoffs) == 0 || !truthy(getField(handoffs[len(handoffs)-1], "current")) {
		missing = append(missing, "current structured handoff")
	}
	if pr == nil || !truthy(getField(pr, "complete")) {
		missing = append(missing, "complete PR identity from `pr reconcile`")
	} else if head != "" {
		identity := asObject(getField(pr, "identity"))
		if asString(getField(identity, "head_sha")) != head {
			missing = append(missing, "PR head SHA does not match the current candidate; reconcile again")
		}
	}
	if len(rootPass) == 0 {
		var latest *ordjson.Object
		if len(rootCurrent) > 0 {
			latest = rootCurrent[len(rootCurrent)-1]
		}
		if latest != nil && asString(getField(latest, "result")) != "pass" {
			runID := getField(latest, "run_id")
			if !truthy(runID) {
				runID = getField(latest, "id")
			}
			missing = append(missing, fmt.Sprintf(
				"coordinator verification of the current candidate passed (latest root run %s was %s); the task is parked with that evidence",
				pyStr(runID), pyStr(getField(latest, "result"))))
		} else {
			missing = append(missing, "coordinator verification of the current candidate")
		}
	}
	if truthy(getField(task, "reviewer")) && len(reviews) == 0 {
		missing = append(missing, "saved findings from the bound reviewer pane")
	}

	var reviewsCurrent []*ordjson.Object
	for _, r := range reviews {
		if truthy(getField(r, "current")) {
			reviewsCurrent = append(reviewsCurrent, r)
		}
	}
	var latestRoot *ordjson.Object
	if len(rootCurrent) > 0 {
		latestRoot = rootCurrent[len(rootCurrent)-1]
	}

	if standardized {
		anyWorkerCurrent := false
		for _, r := range workerRuns {
			if truthy(getField(r, "current")) {
				anyWorkerCurrent = true
				break
			}
		}
		if !anyWorkerCurrent {
			missing = append(missing, "worker verification run of the current candidate (handoff.verification from the project's verify runner)")
		}
		if latestRoot != nil && !truthy(getField(latestRoot, "run_id")) {
			missing = append(missing, "coordinator verification run record (`verify --run run.json` or `verify --execute`); a prose-only record shows no fresh execution of the contract")
		}
		if latestRoot != nil && truthy(getField(latestRoot, "run_id")) {
			latestRootRunID := asString(getField(latestRoot, "run_id"))
			sameAsWorker := false
			for _, r := range workerRuns {
				if asString(getField(r, "run_id")) == latestRootRunID {
					sameAsWorker = true
					break
				}
			}
			if sameAsWorker {
				missing = append(missing, "a coordinator run distinct from the worker run (the same run id was recorded twice)")
			}
		}
		if latestRoot != nil && asString(getField(latestRoot, "result")) == "pass" && truthy(getField(latestRoot, "run_id")) && truthy(getField(latestRoot, "requires_root_review")) {
			approved := false
			for _, r := range reviewsCurrent {
				if truthy(getField(r, "policy_reviewed")) && asString(getField(r, "verdict")) == "approve" {
					approved = true
					break
				}
			}
			if !approved {
				missing = append(missing, "explicit review of the changed verification policy (`review --policy-reviewed --verdict approve`); a candidate cannot certify its own new gate")
			}
		}
		if len(reviewsCurrent) == 0 {
			missing = append(missing, "independent review findings for the current candidate (reviewer pane or the configured MADE/No Mistakes record); until then the result is not reviewed")
		}
	}

	var latestWorker *ordjson.Object
	if len(workerRuns) > 0 {
		latestWorker = workerRuns[len(workerRuns)-1]
	}
	var latestReview *ordjson.Object
	if len(reviewsCurrent) > 0 {
		latestReview = reviewsCurrent[len(reviewsCurrent)-1]
	} else if len(reviews) > 0 {
		latestReview = reviews[len(reviews)-1]
	}

	verification := ordjson.NewObject()
	contract := "legacy"
	if policy != nil {
		contract = asString(getField(policy, "status"))
	}
	verification.Set("contract", contract)
	if latestWorker != nil {
		verification.Set("worker_run", pick(latestWorker, workerRunKeys))
	} else {
		verification.Set("worker_run", nil)
	}
	if latestRoot != nil {
		verification.Set("root_run", pick(latestRoot, rootRunKeys))
	} else {
		verification.Set("root_run", nil)
	}
	distinctRunIDs := latestWorker != nil && latestRoot != nil && truthy(getField(latestRoot, "run_id")) &&
		asString(getField(latestWorker, "run_id")) != asString(getField(latestRoot, "run_id"))
	verification.Set("distinct_run_ids", distinctRunIDs)

	review := ordjson.NewObject()
	if len(reviewsCurrent) > 0 {
		review.Set("status", "performed")
	} else {
		review.Set("status", "not-performed")
	}
	review.Set("current", len(reviewsCurrent) > 0)
	review.Set("verdict", getField(latestReview, "verdict"))
	review.Set("tool", getField(latestReview, "tool"))
	review.Set("policy_reviewed", latestReview != nil && truthy(getField(latestReview, "policy_reviewed")))
	review.Set("reviewer_pane", getField(asObject(getField(task, "reviewer")), "pane"))
	verification.Set("review", review)
	verification.Set("note", "Worker and coordinator runs are separate executions with their own run ids; neither the worker's run nor a review substitutes for the coordinator's. Records for another SHA are historical.")

	result := ordjson.NewObject()
	if head != "" {
		result.Set("current_candidate", head)
	} else {
		result.Set("current_candidate", nil)
	}
	recordsAny := make([]any, len(records))
	for i, r := range records {
		recordsAny[i] = r
	}
	result.Set("records", recordsAny)
	result.Set("reviewer", getField(task, "reviewer"))
	if pr != nil {
		result.Set("pr", pr)
	} else {
		result.Set("pr", nil)
	}
	result.Set("verification", verification)

	closure := ordjson.NewObject()
	closure.Set("prerequisites_met", len(missing) == 0)
	missingAny := make([]any, len(missing))
	for i, m := range missing {
		missingAny[i] = m
	}
	closure.Set("missing", missingAny)
	closure.Set("merged_for_task", pr != nil && truthy(getField(pr, "merged_for_task")))
	closure.Set("note", "Readiness only. Nothing here closes a pane or removes a checkout; an idle state or a report never counts as verified or merged.")
	result.Set("closure", closure)
	return result
}

// Show ports the `show TASK_ID` command body.
func Show(s *store.Store, taskID string) (*ordjson.Object, error) {
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	for _, k := range task.Keys() {
		v, _ := task.Get(k)
		result.Set(k, v)
	}

	versionsView, versionsErr := versions.View(s, task)
	if versionsErr != nil {
		result.Set("versions", nil)
		result.Set("versions_error", versionsErr.Error())
	} else {
		result.Set("versions", versionsView)
	}

	result.Set("evidence_view", View(task))

	returnsView, returnsErr := returns.View(s, task)
	if returnsErr != nil {
		errObj := ordjson.NewObject()
		errObj.Set("error", returnsErr.Error())
		result.Set("returns", errObj)
	} else {
		result.Set("returns", returnsView)
	}
	return result, nil
}
