package pipeline

import (
	"encoding/json"
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

// GitHub computes mergeability lazily and answers UNKNOWN until it has, so only these two answers are a finding.
const (
	mergeableConflicting = "CONFLICTING"
	mergeStateDirty      = "DIRTY"
	mergeFields          = "mergeable,mergeStateStatus"
)

// MergeAdvice is the one remedy for a pushed branch GitHub cannot merge. Push never forces, so the branch is brought
// up to date by a merge commit on top of what is already on origin, never by rewriting it.
const MergeAdvice = "the worker merges the base branch into the task branch, resolves the conflict, and reports the new candidate (a pushed branch is never rebased or force-pushed); send `repair send` with that instruction, and the new candidate goes through the gates again"

// SetMergeability records GitHub's own answer on the task's PR object at the instant it was read. A field GitHub did
// not return stays absent rather than reading as mergeable.
func SetMergeability(pr *ordjson.Object, mergeable, mergeState any, at string) {
	recorded := false
	if text, isText := mergeable.(string); isText && text != "" {
		pr.Set("mergeable", text)
		recorded = true
	}
	if text, isText := mergeState.(string); isText && text != "" {
		pr.Set("merge_state_status", text)
		recorded = true
	}
	if recorded {
		pr.Set("mergeability_observed_at", at)
	}
}

// MergeConflict is the sentence for an open PR GitHub last reported as conflicting with its base, or "".
func MergeConflict(pr *ordjson.Object) string {
	if stringField(pr, "state") != "open" {
		return ""
	}
	mergeable := stringField(pr, "mergeable")
	mergeState := stringField(pr, "merge_state_status")
	if mergeable != mergeableConflicting && mergeState != mergeStateDirty {
		return ""
	}
	identity, _ := field(pr, "identity").(*ordjson.Object)
	return fmt.Sprintf("Conflicts with %s per GitHub (mergeable %s, mergeStateStatus %s, observed %s): %s",
		stringField(identity, "base_branch"), orUnknown(mergeable), orUnknown(mergeState),
		observedStamp(stringField(pr, "mergeability_observed_at")), stringField(identity, "url"))
}

func orUnknown(text string) string {
	if text == "" {
		return "UNKNOWN"
	}
	return text
}

// ObserveMergeability reads mergeability once for the reconciled PR and records it. It is the `pipeline ci` path;
// `pr reconcile` reads the same fields in its own PR observation.
func ObserveMergeability(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID string, timeout int) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	dest, err := destination(task)
	if err != nil {
		return nil, err
	}
	gh, err := toolpath.Find(runtimeRoot, "gh")
	if err != nil {
		return nil, err
	}
	stdout, err := runGH(gh, stringField(task, "repository"), timeout, "pr", "view", fmt.Sprint(dest.Number), "--repo", dest.Repository, "--json", mergeFields)
	if err != nil {
		return nil, fmt.Errorf("could not read the mergeability of #%d: %w", dest.Number, err)
	}
	var payload struct {
		Mergeable        string `json:"mergeable"`
		MergeStateStatus string `json:"mergeStateStatus"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		return nil, fmt.Errorf("gh did not return the mergeability of #%d: %s", dest.Number, boundText(stdout, 300))
	}

	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err = s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	pr, _ := field(task, "pr").(*ordjson.Object)
	if pr == nil {
		return nil, fmt.Errorf("the task lost its PR record while mergeability was read")
	}
	SetMergeability(pr, payload.Mergeable, payload.MergeStateStatus, store.Now())
	if err := s.SaveTask(task); err != nil {
		return nil, err
	}
	row := ordjson.NewObject()
	row.Set("mergeable", field(pr, "mergeable"))
	row.Set("merge_state_status", field(pr, "merge_state_status"))
	row.Set("observed_at", field(pr, "mergeability_observed_at"))
	row.Set("conflict", nilIfEmpty(MergeConflict(pr)))
	row.Set("pipeline_note", RefreshNote(s, task))
	return row, nil
}
