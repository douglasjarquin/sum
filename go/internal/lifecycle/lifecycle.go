// Package lifecycle runs the bounded cleanup-and-observe pass that rides along
// with the returns pump. It sits above returns in the dependency graph (returns
// cannot import cleanup without a cycle), so every coordinator-context pump
// entry point calls PumpAndSweep instead of returns.Pump directly.
package lifecycle

import (
	"encoding/json"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/cleanup"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/prcmd"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// PumpAndSweep is returns.Pump plus one bounded lifecycle pass: a recorded open
// PR gets a single structured observation, then each task still pending cleanup
// gets one apply attempt. Only a coordinator context sweeps; worker pumps
// deliver returns exactly as before.
func PumpAndSweep(s *store.Store, opts returns.PumpOpts) (*ordjson.Object, error) {
	result, err := returns.Pump(s, opts)
	if err != nil {
		return nil, err
	}
	if rows := Sweep(s, opts.Ctx, opts.RuntimeRoot, opts.Tasks); rows != nil {
		result.Set("lifecycle", rows)
	}
	return result, nil
}

// Sweep performs the bounded pass itself: every matching task is touched once,
// per-task failures are named in their row and never abort the rest of the
// pass, and nothing retries within one sweep. A saved cleanup intent re-plans
// and reconciles instead of wedging. Returns nil for non-coordinator contexts
// so callers can attach the field unconditionally.
func Sweep(s *store.Store, ctx *ordjson.Object, runtimeRoot string, only []string) []any {
	if ctx == nil || app.RequireCoordinator(s, ctx) != nil {
		return nil
	}
	host, err := s.Machine()
	if err != nil {
		return nil
	}
	taskFilter := map[string]bool{}
	for _, id := range only {
		taskFilter[id] = true
	}
	tasks, err := s.AllTasks()
	if err != nil {
		row := ordjson.NewObject()
		row.Set("state", "unavailable")
		row.Set("error", err.Error())
		return []any{row}
	}
	rows := []any{}
	for _, task := range tasks {
		id, _ := task.Get("id")
		idStr, _ := id.(string)
		status, _ := task.Get("status")
		recorded, _ := task.Get("machine")
		if status == "archived" || !host.Is(recorded) || (len(taskFilter) > 0 && !taskFilter[idStr]) {
			continue
		}
		if number := openPRNumber(task); number > 0 {
			row := ordjson.NewObject()
			row.Set("task", idStr)
			row.Set("action", "pr-observe")
			obs, obsErr := prcmd.Reconcile(s, ctx, runtimeRoot, prcmd.ReconcileArgs{Task: idStr, Number: number})
			if obsErr != nil {
				row.Set("state", "error")
				row.Set("error", obsErr.Error())
			} else {
				pr := asObject(func() any { v, _ := obs.Get("pr"); return v }())
				row.Set("state", func() any { v, _ := pr.Get("state"); return v }())
			}
			rows = append(rows, row)
			if fresh, err := s.ReadTask(idStr); err == nil {
				task = fresh
			}
		}
		if cleanup.Pending(task) == nil {
			continue
		}
		row := ordjson.NewObject()
		row.Set("task", idStr)
		row.Set("action", "cleanup")
		applied, runErr := cleanup.Run(s, ctx, runtimeRoot, cleanup.Args{Task: idStr, Apply: true})
		if runErr != nil {
			row.Set("state", "blocked")
			row.Set("error", runErr.Error())
		} else {
			row.Set("state", func() any { v, _ := applied.Get("state"); return v }())
			row.Set("archived", func() any { v, _ := applied.Get("archived"); return v }())
		}
		rows = append(rows, row)
	}
	return rows
}

// openPRNumber returns the recorded PR number when the task's PR record shows a
// still-open pull request with a verified identity. Only recorded structure is
// consulted; prose, reports, and notices never trigger observation.
func openPRNumber(task *ordjson.Object) int {
	pr := asObject(func() any { v, _ := task.Get("pr"); return v }())
	if pr == nil {
		return 0
	}
	if merged, _ := pr.Get("merged_for_task"); merged == true {
		return 0
	}
	if state, _ := pr.Get("state"); state != "open" {
		return 0
	}
	identity := asObject(func() any { v, _ := pr.Get("identity"); return v }())
	if identity == nil {
		return 0
	}
	number, _ := identity.Get("number")
	n, _ := number.(json.Number)
	i, _ := n.Int64()
	return int(i)
}

func asObject(v any) *ordjson.Object {
	obj, _ := v.(*ordjson.Object)
	return obj
}
