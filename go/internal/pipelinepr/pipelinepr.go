// Package pipelinepr is the PR delivery gate end to end: open the pull request (or adopt the one already there),
// then reconcile it. It is its own package because reconcile lives in prcmd, and prcmd already imports pipeline.
package pipelinepr

import (
	"encoding/json"
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pipeline"
	"github.com/douglasjarquin/sum/go/internal/prcmd"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// Run opens or adopts the PR and then reconciles it, which is what records the identity, reads the checks, and
// publishes the evidence and pipeline blocks. It never reviews and never merges.
func Run(s *store.Store, ctx *ordjson.Object, runtimeRoot string, args pipeline.PRArgs) (*ordjson.Object, error) {
	opened, err := pipeline.PR(s, ctx, runtimeRoot, args)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", args.Task)
	pr, _ := field(opened, "pr").(*ordjson.Object)
	result.Set("pr", pr)
	summary, _ := opened.Get("summary")
	result.Set("summary", summary)

	number := numberOf(pr)
	if number == 0 {
		result.Set("reconcile", nil)
		return result, nil
	}
	reconciled, err := prcmd.Reconcile(s, ctx, runtimeRoot, prcmd.ReconcileArgs{Task: args.Task, Number: number})
	if err != nil {
		return nil, fmt.Errorf("#%d is the PR for this task, but observing it failed: %w. Run `sumctl pr reconcile %s --number %d` once GitHub answers; nothing will be created again",
			number, err, args.Task, number)
	}
	result.Set("reconcile", reconciled)
	return result, nil
}

func field(o *ordjson.Object, key string) any {
	if o == nil {
		return nil
	}
	value, _ := o.Get(key)
	return value
}

func numberOf(pr *ordjson.Object) int {
	raw, isNumber := field(pr, "number").(json.Number)
	if !isNumber {
		return 0
	}
	value, err := raw.Int64()
	if err != nil {
		return 0
	}
	return int(value)
}
