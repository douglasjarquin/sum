package pipeline

import (
	"fmt"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	pushGate       = "push"
	pushUnobserved = "Not observed; run `pipeline push`"
)

var pushOutcomes = map[string]Status{
	"pushed":           Pass,
	"already":          Pass,
	"rejected":         Fail,
	"failed":           Fail,
	outcomeUnavailable: Blocked,
}

type PushArgs struct {
	Task                 string
	AllowBehind          bool
	AllowMissingEvidence bool
}

// Push is the one place sum writes to a remote. It is a plain fast-forward push of the branch the worker already owns:
// never --force, never --force-with-lease, never a delete, so nothing anyone else pushed can be lost here.
func Push(s *store.Store, ctx *ordjson.Object, args PushArgs) (*ordjson.Object, error) {
	task, candidate, err := gateTask(s, ctx, args.Task, "")
	if err != nil {
		return nil, err
	}
	if status := stringField(task, "status"); status != "reported" {
		return nil, fmt.Errorf("this task is %q; Push is the handoff step and runs only once the worker has reported", status)
	}
	branch := stringField(task, "branch")
	if branch == "" {
		return nil, fmt.Errorf("this task records no branch to push")
	}
	repo := stringField(task, "repository")
	if repo == "" || gitIn(repo, gitBound, "rev-parse", "--git-dir").Code != 0 {
		return nil, fmt.Errorf("the task repository %q is not a Git repository; nothing can be pushed from here", repo)
	}
	tip := gitIn(repo, gitBound, "rev-parse", "--verify", "refs/heads/"+branch)
	local := strings.TrimSpace(tip.Stdout)
	if tip.Code != 0 || local != candidate {
		return nil, fmt.Errorf("%s is at %q in the task repository, not at the candidate %s; nothing is pushed until the recorded branch and the candidate agree",
			branch, local, candidate)
	}
	if !args.AllowBehind {
		if rebase := Derive(task).Get(StageRebase); rebase.Status != Pass {
			return nil, fmt.Errorf("the Rebase gate is %s (%s); run `pipeline rebase`, let the worker rebase, or pass --allow-behind to push anyway",
				rebase.Status, rebase.Result)
		}
	}
	if missing, waived := EvidenceGap(task, candidate); len(missing) > 0 && !waived {
		names := strings.Join(missing, ", ")
		if !args.AllowMissingEvidence {
			return nil, fmt.Errorf("the Test gate is blocked: no before/after evidence for %s. The worker captures it with `.agents/skills/evidence/`, or the user waives it with `verify %s --candidate %s --accept-missing-evidence`",
				names, args.Task, candidate)
		}
		if !EvidenceWaiverRecorded(task, candidate) {
			return nil, fmt.Errorf("--allow-missing-evidence pushes work whose evidence for %s was never captured, so the waiver must be on the record first: run `verify %s --candidate %s --accept-missing-evidence`",
				names, args.Task, candidate)
		}
	}
	body, summary := pushBranch(repo, branch, candidate)
	return recordGate(s, ctx, args.Task, pushGate, candidate, body, summary)
}

func pushBranch(repo, branch, candidate string) (*ordjson.Object, string) {
	body := ordjson.NewObject()
	body.Set("branch", branch)

	before := remoteHead(repo, branch)
	if before == candidate {
		body.Set("outcome", "already")
		body.Set("remote_sha", before)
		return body, "Already on origin"
	}
	push := gitIn(repo, networkBound, "push", "origin", branch)
	if push.Code != 0 {
		body.Set("remote_sha", nilIfEmpty(before))
		detail := push.Stderr + "\n" + push.Stdout
		if strings.Contains(detail, "non-fast-forward") || strings.Contains(detail, "[rejected]") || strings.Contains(detail, "fetch first") {
			body.Set("outcome", "rejected")
			return body, fmt.Sprintf("Rejected: origin/%s has commits the candidate lacks; the worker rebases", branch)
		}
		body.Set("outcome", "failed")
		return body, "Failed: " + lastLineOf(push.Stderr)
	}
	after := remoteHead(repo, branch)
	body.Set("remote_sha", nilIfEmpty(after))
	if after != candidate {
		body.Set("outcome", outcomeUnavailable)
		return body, fmt.Sprintf("The push reported success but origin/%s reads %q; read the remote before pushing again", branch, after)
	}
	body.Set("outcome", "pushed")
	return body, fmt.Sprintf("Pushed %s to origin/%s", shortSHA(candidate), branch)
}

func remoteHead(repo, branch string) string {
	run := gitIn(repo, networkBound, "ls-remote", "--heads", "origin", branch)
	if run.Code != 0 {
		return ""
	}
	fields := strings.Fields(run.Stdout)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// A reconciled PR whose head is this candidate is proof the candidate reached origin, whoever pushed it.
func derivePush(task *ordjson.Object, candidate string) Row {
	row := gateRow(task, StagePush, pushGate, pushUnobserved, pushOutcomes)
	if row.Status != Pending {
		return row
	}
	pr, _ := field(task, "pr").(*ordjson.Object)
	identity, _ := field(pr, "identity").(*ordjson.Object)
	if identity != nil && stringField(identity, "head_sha") == candidate {
		return Row{Stage: StagePush, Status: Pass, Result: "On origin: the reconciled PR head is this candidate", At: stringField(pr, "observed_at")}
	}
	return row
}
