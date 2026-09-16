package pipeline

import (
	"fmt"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

// PushAdmission is the named exceptions a coordinator may pass. Each flag waives only the gate it names.
type PushAdmission struct {
	AllowBehind          bool
	AllowMissingEvidence bool
}

// RequirePush is the one publication decision for pipeline run, pipeline push, and pipeline pr.
// It reads Derive rows. It does not run a reviewer or invent a pass.
func RequirePush(task *ordjson.Object, opts PushAdmission) error {
	record := Derive(task)
	candidate := Candidate(task)
	stages := []Stage{StageIntent, StageRebase, StageReview, StageTest, StageDocument, StageLint}
	for _, stage := range stages {
		if err := refuseUnready(task, candidate, record.Get(stage), stage, opts); err != nil {
			return err
		}
	}
	if err := requirePolicyReviewed(task, candidate); err != nil {
		return err
	}
	return requireWorkerVerification(task, candidate)
}

// RequirePR adds the Push row: a PR may not point at a candidate that never reached origin.
func RequirePR(task *ordjson.Object, opts PushAdmission) error {
	row := Derive(task).Get(StagePush)
	if row.Status != Pass {
		return fmt.Errorf("the Push gate is %s (%s); the branch must be on origin at the candidate before a PR can point at it",
			row.Status, row.Result)
	}
	return RequirePush(task, opts)
}

func refuseUnready(task *ordjson.Object, candidate string, row Row, stage Stage, opts PushAdmission) error {
	if stage == StageRebase && opts.AllowBehind {
		return nil
	}
	if stage == StageTest && row.Status == Blocked && opts.AllowMissingEvidence && evidenceWaiverRecorded(task, candidate) {
		return nil
	}
	if stage == StageReview {
		if row.Status == Pass {
			return nil
		}
		return fmt.Errorf("the Review gate is %s (%s); launch a reviewer and record the verdict with `sumctl review TASK_ID --verdict ... --candidate %s`",
			row.Status, row.Result, candidate)
	}
	if settled(row.Status) {
		return nil
	}
	if stage == StageRebase {
		return fmt.Errorf("the Rebase gate is %s (%s); run `pipeline rebase`, let the worker rebase, or pass --allow-behind to push anyway",
			row.Status, row.Result)
	}
	if stage == StageTest {
		missing, waived := evidenceGap(latestFor(task, "verification", "coordinator", candidate))
		if len(missing) > 0 && !waived {
			names := strings.Join(missing, ", ")
			taskID := stringField(task, "id")
			if !opts.AllowMissingEvidence {
				return fmt.Errorf("the Test gate is blocked: no before/after evidence for %s. The worker captures it with `.agents/skills/evidence/`, or the user waives it with `verify %s --candidate %s --accept-missing-evidence`",
					names, taskID, candidate)
			}
			return fmt.Errorf("--allow-missing-evidence pushes work whose evidence for %s was never captured, so the waiver must be on the record first: run `verify %s --candidate %s --accept-missing-evidence`",
				names, taskID, candidate)
		}
	}
	next := row.Advice
	if next == "" {
		next = advice[stage]
	}
	return fmt.Errorf("the %s gate is %s (%s); %s", displayName(stage), row.Status, row.Result, next)
}

func displayName(stage Stage) string {
	for _, definition := range Stages {
		if definition.Stage == stage {
			return definition.Display
		}
	}
	return string(stage)
}

func requirePolicyReviewed(task *ordjson.Object, candidate string) error {
	latest := latestFor(task, "verification", "coordinator", candidate)
	requires, _ := field(latest, "requires_root_review").(bool)
	if !requires {
		return nil
	}
	for _, record := range Reviews(task, candidate) {
		reviewed, _ := field(record, "policy_reviewed").(bool)
		if stringField(record, "verdict") == "approve" && reviewed {
			return nil
		}
	}
	return fmt.Errorf("the Review gate is pending: explicit review of the changed verification policy (`review --policy-reviewed --verdict approve --candidate %s`); a candidate cannot certify its own new gate",
		candidate)
}

func requireWorkerVerification(task *ordjson.Object, candidate string) error {
	policy, _ := field(task, "verification_policy").(*ordjson.Object)
	if stringField(policy, "status") != "standardized" {
		return nil
	}
	coordinator := latestFor(task, "verification", "coordinator", candidate)
	coordinatorID := stringField(coordinator, "run_id")
	for _, record := range records(task) {
		if stringField(record, "kind") != "verification" || stringField(record, "source") != "worker" {
			continue
		}
		if stringField(record, "candidate") != candidate {
			continue
		}
		runID := stringField(record, "run_id")
		if runID == "" {
			continue
		}
		if coordinatorID != "" && runID == coordinatorID {
			return fmt.Errorf("the worker run id %s was reused as the coordinator result; run the contract again under a distinct id", runID)
		}
		return nil
	}
	return fmt.Errorf("no worker verification run of the current candidate; attach handoff.verification from the project's verify runner")
}
