// Package lifecycle is sum's explicit maintenance: observing recorded open PRs through GitHub and applying pending
// cleanup. Fast coordination (init, status, inbox, pump, bind, hook events) never runs it; those commands show
// Pending, the records-only view of what maintenance is outstanding, with the exact command that does it. Sweep is
// the one batch entry point, reached only through the coordinator's explicit `sumctl sweep`.
package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/cleanup"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/prcmd"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// DefaultSweepBudget bounds when a sweep may start another task. A task already started finishes under its own
// helpers' bounds (up to two 120-second gh calls for one PR observation), so the budget bounds admission, not wall time.
const DefaultSweepBudget = 60 * time.Second

// Pending is the records-only maintenance view over tasks (a snapshot the caller already read): recorded open PRs with
// the time they were last observed, and cleanup still pending, blocked, or interrupted. It reads and writes nothing.
func Pending(s *store.Store, sumctlPath string, tasks []*ordjson.Object) *ordjson.Object {
	host, err := s.Machine()
	view := ordjson.NewObject()
	if err != nil {
		view.Set("error", err.Error())
		return view
	}
	prs := []any{}
	cleanups := []any{}
	for _, task := range tasks {
		id := asString(task, "id")
		if !candidate(host.Is, task) {
			continue
		}
		if number := openPRNumber(task); number > 0 {
			pr := asObject(field(task, "pr"))
			row := ordjson.NewObject()
			row.Set("task", id)
			row.Set("pr", json.Number(fmt.Sprint(number)))
			row.Set("state", field(pr, "state"))
			row.Set("observed_at", field(pr, "observed_at"))
			row.Set("next", shquote.CommandFor(sumctlPath, s.Home, "pr", "reconcile", id))
			prs = append(prs, row)
		}
		if pending := cleanup.Pending(task); pending != nil {
			row := ordjson.NewObject()
			row.Set("task", id)
			for _, key := range pending.Keys() {
				v, _ := pending.Get(key)
				row.Set(key, v)
			}
			row.Set("next", shquote.CommandFor(sumctlPath, s.Home, "cleanup", id))
			cleanups = append(cleanups, row)
		}
	}
	view.Set("open_prs", prs)
	view.Set("cleanup", cleanups)
	if len(prs)+len(cleanups) > 0 {
		view.Set("next", shquote.CommandFor(sumctlPath, s.Home, "sweep"))
	} else {
		view.Set("next", nil)
	}
	view.Set("note", "From saved records only; nothing was re-observed. An open PR's state and CI are as of its observed_at; merges are seen only by `sweep`, `pr reconcile`, or `cleanup`.")
	return view
}

// SweepOpts limits one explicit sweep.
type SweepOpts struct {
	Tasks      []string
	Budget     time.Duration
	SumctlPath string
}

// Sweep is one explicit maintenance pass as the coordinator: one PR observation per recorded open PR, then one cleanup
// apply per task still pending cleanup. Tasks are visited least recently maintained first; no task starts after the
// budget, and after a gh timeout or unknown effect no further PR is observed in this pass. Each task is re-read
// before anything acts on it; per-task failures are named in their row and never abort the rest.
func Sweep(s *store.Store, ctx *ordjson.Object, runtimeRoot string, opts SweepOpts) (*ordjson.Object, error) {
	if ctx == nil {
		return nil, fmt.Errorf("sweep needs this pane's Herdr context; run it from the coordinator pane.")
	}
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	budget := opts.Budget
	if budget < 0 {
		budget = 0
	} else if opts.Budget == 0 {
		budget = DefaultSweepBudget
	}
	started := time.Now()
	filter := map[string]bool{}
	for _, id := range opts.Tasks {
		filter[id] = true
	}
	tasks, err := s.AllTasks()
	if err != nil {
		return nil, err
	}
	type work struct {
		id   string
		last string
	}
	var queue []work
	for _, task := range tasks {
		id := asString(task, "id")
		if !candidate(host.Is, task) || (len(filter) > 0 && !filter[id]) {
			continue
		}
		if openPRNumber(task) == 0 && cleanup.Pending(task) == nil {
			continue
		}
		queue = append(queue, work{id: id, last: lastMaintained(task)})
	}
	sort.SliceStable(queue, func(i, j int) bool {
		if queue[i].last != queue[j].last {
			return queue[i].last < queue[j].last
		}
		return queue[i].id < queue[j].id
	})
	rows := []any{}
	deferred := []any{}
	ghTripped := ""
	defer1 := func(id, action, reason string, args ...string) {
		row := ordjson.NewObject()
		row.Set("task", id)
		row.Set("action", action)
		row.Set("state", "deferred")
		row.Set("reason", reason)
		row.Set("next", shquote.CommandFor(opts.SumctlPath, s.Home, args...))
		deferred = append(deferred, row)
	}
	for _, item := range queue {
		if time.Since(started) >= budget {
			defer1(item.id, "maintenance", fmt.Sprintf("the sweep budget (%s) was spent before this task started; nothing was observed or applied", budget), "sweep", "--task", item.id)
			continue
		}
		beforeTask(item.id)
		task, err := s.ReadTask(item.id)
		if err != nil {
			row := ordjson.NewObject()
			row.Set("task", item.id)
			row.Set("action", "read")
			row.Set("state", "error")
			row.Set("error", err.Error())
			rows = append(rows, row)
			continue
		}
		if !candidate(host.Is, task) {
			continue
		}
		if number := openPRNumber(task); number > 0 {
			if ghTripped != "" {
				defer1(item.id, "pr-observe", "GitHub did not answer an earlier observation in this sweep ("+ghTripped+"); not contacted again this pass", "pr", "reconcile", item.id)
			} else {
				row := ordjson.NewObject()
				row.Set("task", item.id)
				row.Set("action", "pr-observe")
				obs, obsErr := prcmd.Reconcile(s, ctx, runtimeRoot, prcmd.ReconcileArgs{Task: item.id, Number: number})
				if obsErr != nil {
					row.Set("state", "error")
					row.Set("error", obsErr.Error())
					if errors.Is(obsErr, proc.ErrUncertain) || errors.Is(obsErr, proc.ErrNotStarted) || strings.Contains(obsErr.Error(), "timed out") {
						ghTripped = "#" + fmt.Sprint(number)
					}
				} else {
					row.Set("state", field(asObject(field(obs, "pr")), "state"))
				}
				rows = append(rows, row)
				if fresh, err := s.ReadTask(item.id); err == nil {
					task = fresh
				}
			}
		}
		if cleanup.Pending(task) == nil {
			continue
		}
		row := ordjson.NewObject()
		row.Set("task", item.id)
		row.Set("action", "cleanup")
		applied, runErr := cleanup.Run(s, ctx, runtimeRoot, cleanup.Args{Task: item.id, Apply: true})
		if runErr != nil {
			row.Set("state", "blocked")
			row.Set("error", runErr.Error())
		} else {
			row.Set("state", field(applied, "state"))
			row.Set("archived", field(applied, "archived"))
		}
		rows = append(rows, row)
	}
	after, err := s.AllTasks()
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("rows", rows)
	result.Set("deferred", deferred)
	result.Set("budget_ms", json.Number(fmt.Sprint(budget.Milliseconds())))
	result.Set("elapsed_ms", json.Number(fmt.Sprint(time.Since(started).Milliseconds())))
	result.Set("maintenance", Pending(s, opts.SumctlPath, after))
	result.Set("note", "Explicit maintenance as the coordinator: each started task finished under its own helpers' bounds; deferred tasks were not touched and keep their exact next command. Nothing here merges, and a merged PR is cleaned up only through cleanup's guarded checks.")
	return result, nil
}

// beforeTask runs between the sweep's snapshot and its fresh read of one task (a seam for tests).
var beforeTask = func(string) {}

// candidate is a local, unarchived task.
func candidate(local func(any) bool, task *ordjson.Object) bool {
	return asString(task, "status") != "archived" && local(field(task, "machine"))
}

// lastMaintained is the older of the task's last PR observation and cleanup record ("" when never).
func lastMaintained(task *ordjson.Object) string {
	times := []string{}
	if pr := asObject(field(task, "pr")); pr != nil {
		times = append(times, asString(pr, "observed_at"))
	}
	if record := asObject(field(task, "cleanup")); record != nil {
		times = append(times, asString(record, "at"))
	}
	if len(times) == 0 {
		return ""
	}
	sort.Strings(times)
	return times[0]
}

// openPRNumber returns the recorded PR number when the task's PR record shows a
// still-open pull request with a verified identity. Only recorded structure is
// consulted; prose, reports, and notices never trigger observation.
func openPRNumber(task *ordjson.Object) int {
	pr := asObject(field(task, "pr"))
	if pr == nil {
		return 0
	}
	if merged, _ := pr.Get("merged_for_task"); merged == true {
		return 0
	}
	if state, _ := pr.Get("state"); state != "open" {
		return 0
	}
	identity := asObject(field(pr, "identity"))
	if identity == nil {
		return 0
	}
	n, _ := field(identity, "number").(json.Number)
	i, _ := n.Int64()
	return int(i)
}

func field(obj *ordjson.Object, key string) any {
	if obj == nil {
		return nil
	}
	v, _ := obj.Get(key)
	return v
}

func asString(obj *ordjson.Object, key string) string {
	s, _ := field(obj, key).(string)
	return s
}

func asObject(v any) *ordjson.Object {
	obj, _ := v.(*ordjson.Object)
	return obj
}
