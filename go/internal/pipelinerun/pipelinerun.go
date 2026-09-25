// Package pipelinerun drives the coordinator-side delivery gates in order. It is its own package because it calls
// both pipeline and verifycmd, and verifycmd already re-derives the pipeline after it saves.
package pipelinerun

import (
	"fmt"
	"regexp"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pipeline"
	"github.com/douglasjarquin/sum/go/internal/pipelinepr"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/verifycmd"
)

var sha40 = regexp.MustCompile(`^[0-9a-f]{40}$`)

type Args struct {
	Task                 string
	Rerun                bool
	AllowBehind          bool
	AllowMissingEvidence bool
	RuntimeRoot          string
	NoPR                 bool
	Title                string
	BodyFile             string
	AllowNewAfterClosed  bool
}

// A blocked gate normally leaves the run going, because most blocks are something sum could not observe and a later gate
// may still be worth recording. Test is the exception: a blocked Test means the candidate changed something a user sees
// and nobody proved it, and the run stops rather than push that to origin.
//
// Review is absent on purpose: it belongs to a separately launched reviewer pane, and no runner may stand in for it.
var order = []pipeline.Stage{pipeline.StageRebase, pipeline.StageTest, pipeline.StageLint, pipeline.StageDocument, pipeline.StagePush, pipeline.StagePR}

const reviewNote = "Review is the reviewer pane's; only the user merges."

func Run(s *store.Store, ctx *ordjson.Object, args Args) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	candidate := pipeline.Candidate(task)
	if !sha40.MatchString(candidate) {
		return nil, fmt.Errorf("this task records no candidate; the gates run against the SHA the worker reported")
	}

	steps := make([]any, 0, len(order))
	stopped := ""
	for _, stage := range order {
		if stopped != "" {
			steps = append(steps, step(stage, "not-run", stopped))
			continue
		}
		outcome, detail := runStage(s, ctx, args, stage, candidate)
		steps = append(steps, step(stage, outcome, detail))
		if outcome == "refused" {
			stopped = fmt.Sprintf("the %s gate was refused", stage)
			continue
		}
		current, readErr := s.ReadTask(args.Task)
		if readErr != nil {
			return nil, readErr
		}
		if status := pipeline.Derive(current).Get(stage).Status; status == pipeline.Fail {
			stopped = fmt.Sprintf("the %s gate failed", stage)
		} else if status == pipeline.Blocked && stage == pipeline.StageTest {
			stopped = fmt.Sprintf("the %s gate is blocked", stage)
		}
	}

	record, err := pipeline.Load(s, args.Task)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", args.Task)
	result.Set("candidate", candidate)
	result.Set("steps", steps)
	result.Set("pr", prIdentity(s, args.Task))
	result.Set("pipeline", pipeline.View(record))
	result.Set("next", pipeline.Next(record))
	result.Set("note", reviewNote)
	return result, nil
}

// prIdentity is the PR this run left behind, so the coordinator reads the URL without a second command.
func prIdentity(s *store.Store, taskID string) any {
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil
	}
	pr, _ := value(task, "pr").(*ordjson.Object)
	identity, _ := value(pr, "identity").(*ordjson.Object)
	if identity == nil {
		return nil
	}
	row := ordjson.NewObject()
	for _, key := range []string{"number", "url", "base_branch"} {
		row.Set(key, value(identity, key))
	}
	row.Set("state", value(pr, "state"))
	row.Set("draft", value(pr, "draft"))
	return row
}

func value(o *ordjson.Object, key string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	return v
}

func runStage(s *store.Store, ctx *ordjson.Object, args Args, stage pipeline.Stage, candidate string) (string, string) {
	switch stage {
	case pipeline.StageRebase:
		return attempt(pipeline.Rebase(s, ctx, pipeline.RebaseArgs{Task: args.Task}))
	case pipeline.StageTest:
		if !args.Rerun && settledFor(s, args.Task, pipeline.StageTest) == pipeline.Pass {
			return "skipped", "already verified for this candidate; pass --rerun to execute the contract again"
		}
		return attempt(verifycmd.Run(s, ctx, verifycmd.Args{
			Task: args.Task, Candidate: candidate, Execute: true, RuntimeRoot: args.RuntimeRoot,
		}))
	case pipeline.StageLint:
		if settledFor(s, args.Task, pipeline.StageLint) != pipeline.Pending {
			return "skipped", "recorded by `verify --execute` in the checkout it already made"
		}
		return attempt(pipeline.Lint(s, ctx, args.RuntimeRoot, pipeline.LintArgs{Task: args.Task}))
	case pipeline.StageDocument:
		return attempt(pipeline.Document(s, ctx, args.RuntimeRoot, pipeline.DocumentArgs{Task: args.Task}))
	case pipeline.StagePush:
		return attempt(pipeline.Push(s, ctx, pipeline.PushArgs{Task: args.Task, AllowBehind: args.AllowBehind, AllowMissingEvidence: args.AllowMissingEvidence}))
	case pipeline.StagePR:
		if args.NoPR {
			return "skipped", "--no-pr; opening and reconciling the PR is left to you"
		}
		return attempt(pipelinepr.Run(s, ctx, args.RuntimeRoot, pipeline.PRArgs{
			Task: args.Task, Title: args.Title, BodyFile: args.BodyFile,
			AllowNewAfterClosed: args.AllowNewAfterClosed, AllowBehind: args.AllowBehind,
		}))
	}
	return "not-run", "unknown gate"
}

// A command that refuses (a precondition, a missing checkout) is not a failing gate: it recorded nothing, and the
// run stops so the coordinator reads the reason rather than pushing past it.
func attempt(_ *ordjson.Object, err error) (string, string) {
	if err != nil {
		return "refused", err.Error()
	}
	return "ran", ""
}

func settledFor(s *store.Store, taskID string, stage pipeline.Stage) pipeline.Status {
	task, err := s.ReadTask(taskID)
	if err != nil {
		return pipeline.Pending
	}
	return pipeline.Derive(task).Get(stage).Status
}

func step(stage pipeline.Stage, outcome, detail string) *ordjson.Object {
	row := ordjson.NewObject()
	row.Set("stage", string(stage))
	row.Set("outcome", outcome)
	if detail == "" {
		row.Set("detail", nil)
	} else {
		row.Set("detail", detail)
	}
	return row
}
