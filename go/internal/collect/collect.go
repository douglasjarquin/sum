package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const DefaultBudget = 20 * time.Second
const MaximumBudget = 60 * time.Second

type Options struct {
	Claim       string
	Task        string
	Budget      time.Duration
	RuntimeRoot string
	SumctlPath  string
	home        string
}

type target struct {
	task     string
	role     string
	endpoint store.Endpoint
}

// Run collects ledger obligations and native execution facts without creating HC
// or acknowledging anything merely because it was read.
func Run(parent context.Context, s *store.Store, opts Options) (*ordjson.Object, error) {
	actor, err := app.RequireClaim(s, opts.Claim)
	if err != nil {
		return nil, err
	}
	opts.home = s.Home
	budget := opts.Budget
	if budget == 0 {
		budget = DefaultBudget
	}
	if budget < 0 || budget > MaximumBudget {
		return nil, fmt.Errorf("collect budget must be positive and at most %s", MaximumBudget)
	}
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	delivered := make([]any, 0)
	for {
		if _, err := s.CheckClaim(opts.Claim); err != nil {
			return nil, err
		}
		tasks, err := selectedTasks(s, opts.Task)
		if err != nil {
			return nil, err
		}
		pump := returns.PumpOpts{Ctx: actor, RuntimeRoot: opts.RuntimeRoot, SumctlPath: opts.SumctlPath, Recipient: "worker", RetryStalled: true, Parent: ctx, Budget: time.Until(deadline(ctx)), Snapshot: tasks, CallerVerifiedRole: "coordinator"}
		if opts.Task != "" {
			pump.Tasks = []string{opts.Task}
		}
		if ctx.Err() == nil {
			pass, err := returns.Pump(s, pump)
			if err != nil {
				return nil, err
			}
			rows, _ := field(pass, "recipients").([]any)
			for _, raw := range rows {
				row, _ := raw.(*ordjson.Object)
				if text(row, "via") == "prompt" && text(row, "state") == "submitted" {
					delivered = append(delivered, row)
				}
			}
		}
		// Delivery can change receipts; use a fresh ledger snapshot for the result.
		tasks, err = selectedTasks(s, opts.Task)
		if err != nil {
			return nil, err
		}
		items, err := ledgerItems(s, tasks, opts)
		if err != nil {
			return nil, err
		}
		if len(items) > 0 || len(delivered) > 0 {
			return result(items, delivered, false, opts), nil
		}
		if ctx.Err() != nil {
			return result(items, delivered, true, opts), nil
		}
		live := make([]target, 0)
		for _, task := range tasks {
			if text(task, "status") == "archived" {
				continue
			}
			for _, role := range []string{"worker", "reviewer"} {
				endpoint := object(task, "worker_endpoint")
				if role == "reviewer" {
					endpoint = object(task, "reviewer")
				}
				ep := store.EndpointFromContext(endpoint)
				if ep.Address == "" {
					continue
				}
				if err := ep.Validate(); err != nil {
					items = append(items, executionItem(task, role, "worker-failure", err.Error(), opts))
					continue
				}
				client := hcclient.Client{Socket: ep.Socket, CoreID: ep.CoreID, Timeout: min(2*time.Second, time.Until(deadline(ctx)))}
				terminal, err := client.Get(ctx, ep.Address)
				if err != nil {
					if ctx.Err() != nil {
						break
					}
					if terminalMissing(err) {
						items = append(items, executionItem(task, role, "worker-failure", "Recorded terminal no longer exists; inspect before recovery.", opts))
						continue
					}
					item := executionItem(task, role, "core-unreachable", err.Error(), opts)
					item.Set("next", stringsAny(command(opts, "start", ep.Task, "--claim", opts.Claim)))
					items = append(items, item)
					continue
				}
				if terminal.CreatedMS != ep.CreatedMS || terminal.Address != ep.Address {
					items = append(items, executionItem(task, role, "worker-failure", "Recorded terminal identity changed; inspect before recovery.", opts))
					continue
				}
				if terminal.State != "live" {
					if !hasReport(task, role, ep.Attempt) {
						items = append(items, executionItem(task, role, "worker-failure", "Recorded terminal finished without a task report.", opts))
					}
					continue
				}
				if terminal.Status != nil && terminal.Status.State == "blocked" {
					items = append(items, executionItem(task, role, "blocked", terminal.Status.Msg, opts))
					continue
				}
				live = append(live, target{task: text(task, "id"), role: role, endpoint: ep})
			}
		}
		if len(items) > 0 {
			return result(items, delivered, false, opts), nil
		}
		if ctx.Err() != nil {
			return result(items, delivered, true, opts), nil
		}
		if len(live) == 0 {
			return result(items, delivered, false, opts), nil
		}
		started := time.Now()
		for _, entry := range live {
			remaining := time.Until(deadline(ctx))
			if remaining <= 0 {
				break
			}
			wait := min(500*time.Millisecond/time.Duration(len(live)), remaining)
			seconds := wait.Seconds()
			client := hcclient.Client{Socket: entry.endpoint.Socket, CoreID: entry.endpoint.CoreID, Timeout: remaining}
			_, err := client.Wait(ctx, hcclient.WaitParams{Terminal: entry.endpoint.Address, Timeout: &seconds})
			if err != nil && ctx.Err() == nil {
				if terminalMissing(err) {
					// Re-read the ledger and terminal snapshot before reporting a failure.
					continue
				}
				item := newItem(entry.task, "core-unreachable", command(opts, "inspect", entry.task, "--role", entry.role), command(opts, "start", entry.task, "--claim", opts.Claim))
				item.Set("reason", err.Error())
				items = append(items, item)
			}
		}
		if len(items) > 0 {
			return result(items, delivered, false, opts), nil
		}
		// Some HC status waits return immediately for an idle terminal. Pace those
		// snapshots while preserving one shared deadline across every target.
		if pause := 100*time.Millisecond - time.Since(started); pause > 0 {
			timer := time.NewTimer(pause)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}
}

func terminalMissing(err error) bool {
	var native *hcclient.Error
	if !errors.As(err, &native) || native.RPC == nil {
		return false
	}
	var data struct {
		Kind string `json:"kind"`
	}
	return json.Unmarshal(native.RPC.Data, &data) == nil && (data.Kind == "not_found" || data.Kind == "finished")
}

func selectedTasks(s *store.Store, id string) ([]*ordjson.Object, error) {
	if id != "" {
		task, err := s.ReadTask(id)
		if err != nil {
			return nil, err
		}
		return []*ordjson.Object{task}, nil
	}
	return s.AllTasks()
}
func deadline(ctx context.Context) time.Time { value, _ := ctx.Deadline(); return value }

func ledgerItems(s *store.Store, tasks []*ordjson.Object, opts Options) ([]any, error) {
	items := make([]any, 0)
	for _, task := range tasks {
		id := text(task, "id")
		obligations, err := returns.OpenObligations(s, task)
		if err != nil {
			return nil, err
		}
		for _, obligation := range obligations {
			if text(obligation, "recipient") != "parent" {
				continue
			}
			kind := text(obligation, "kind")
			next := command(opts, "context", id, "--section", "evidence")
			switch kind {
			case "question":
				next = command(opts, "answer", id, text(obligation, "ref"), "--claim", opts.Claim)
			case "report", "review":
				candidate := text(obligation, "candidate")
				if kind == "report" {
					evidence, _ := field(task, "evidence").([]any)
					for _, value := range evidence {
						record, _ := value.(*ordjson.Object)
						if text(record, "kind") == "report" && text(record, "id") == text(obligation, "ref") {
							candidate = text(record, "candidate")
							break
						}
					}
				}
				if len(candidate) == 40 && strings.Trim(candidate, "0123456789abcdef") == "" {
					next = command(opts, "verify", id, "--candidate", candidate, "--execute", "--claim", opts.Claim)
				}
			case "attention":
				next = command(opts, "inspect", id, "--role", "worker")
			}
			item := newItem(id, kind, command(opts, "context", id, "--section", "returns"), next)
			item.Set("ref", field(obligation, "ref"))
			items = append(items, item)
		}
		if text(task, "status") == "archived" {
			continue
		}
		execution := object(task, "execution")
		worker := object(execution, "worker")
		for _, entry := range []struct {
			role   string
			record *ordjson.Object
		}{{"worker", worker}, {"reviewer", object(task, "reviewer")}} {
			launch := object(entry.record, "native_launch")
			state := text(entry.record, "state")
			effect := text(launch, "effect")
			if effect == "" {
				effect = text(launch, "state")
			}
			first := object(launch, "first_message")
			initialized := field(entry.record, "native_receipt") != nil
			openingPending := !initialized && first != nil && field(first, "sent") == false
			if !initialized && (state == "uncertain" || state == "starting" || effect == "uncertain" || effect == "claimed" || effect == "in-flight" || openingPending) {
				item := executionItem(task, entry.role, "launch-uncertain", "Creation or opening delivery needs explicit reconciliation.", opts)
				if openingPending {
					group := "execution"
					if entry.role == "reviewer" {
						group = "reviewer"
					}
					item.Set("next", stringsAny(command(opts, group, "redeliver", id, "--attempt", text(item, "attempt"), "--claim", opts.Claim)))
				}
				items = append(items, item)
			}
		}
		if text(task, "status") == "needs-attention" || text(task, "status") == "blocked" {
			items = append(items, newItem(id, "blocked", command(opts, "context", id), command(opts, "inspect", id, "--role", "worker")))
		}
	}
	return items, nil
}

func executionItem(task *ordjson.Object, role, kind, reason string, opts Options) *ordjson.Object {
	id := text(task, "id")
	group := "execution"
	if role == "reviewer" {
		group = "reviewer"
	}
	next := command(opts, group, "reconcile", id, "--claim", opts.Claim)
	if kind == "blocked" {
		next = command(opts, "inspect", id, "--role", role)
	}
	item := newItem(id, kind, command(opts, "inspect", id, "--role", role), next)
	item.Set("role", role)
	item.Set("reason", reason)
	record := object(object(task, "execution"), "worker")
	attempt := text(record, "id")
	if role == "reviewer" {
		record = object(task, "reviewer")
		attempt = text(record, "attempt")
	}
	item.Set("attempt", attempt)
	if kind != "blocked" {
		item.Set("next", stringsAny(command(opts, group, "reconcile", id, "--attempt", attempt, "--claim", opts.Claim)))
	}
	return item
}
func newItem(task, kind string, detail, next []string) *ordjson.Object {
	item := ordjson.NewObject()
	item.Set("task", task)
	item.Set("kind", kind)
	item.Set("detail", stringsAny(detail))
	item.Set("next", stringsAny(next))
	return item
}
func command(opts Options, args ...string) []string {
	helper := opts.SumctlPath
	if helper == "" {
		helper = "sumctl"
	}
	prefix := []string{helper}
	if opts.home != "" {
		prefix = append(prefix, "--home", opts.home)
	}
	return append(prefix, args...)
}
func stringsAny(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}
func result(items, delivered []any, hit bool, opts Options) *ordjson.Object {
	out := ordjson.NewObject()
	out.Set("items", items)
	out.Set("delivered", delivered)
	out.Set("deadline_hit", hit)
	next := command(opts, "collect", "--claim", opts.Claim)
	if opts.Task != "" {
		next = append(next, "--task", opts.Task)
	}
	if len(items) > 0 {
		item, _ := items[0].(*ordjson.Object)
		out.Set("next", field(item, "next"))
	} else {
		out.Set("next", stringsAny(next))
	}
	return out
}
func field(o *ordjson.Object, key string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	return v
}
func text(o *ordjson.Object, key string) string { v, _ := field(o, key).(string); return v }
func object(o *ordjson.Object, key string) *ordjson.Object {
	v, _ := field(o, key).(*ordjson.Object)
	return v
}
func hasReport(task *ordjson.Object, role, attempt string) bool {
	if role == "worker" && field(task, "report") != nil {
		return true
	}
	rows, _ := field(task, "evidence").([]any)
	for _, raw := range rows {
		row, _ := raw.(*ordjson.Object)
		kind := text(row, "kind")
		if (role == "worker" && kind == "report" || role == "reviewer" && kind == "review") && (text(row, "attempt") == "" || text(row, "attempt") == attempt) {
			return true
		}
	}
	return false
}
