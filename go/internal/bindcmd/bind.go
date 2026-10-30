package bindcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hookstatus"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/taskcheckout"
)

func field(o *ordjson.Object, k string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(k)
	return v
}
func object(o *ordjson.Object, k string) *ordjson.Object {
	v, _ := field(o, k).(*ordjson.Object)
	return v
}
func text(o *ordjson.Object, k string) string { v, _ := field(o, k).(string); return v }
func decode(v any, to any) error {
	b, e := ordjson.MarshalCompact(v)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, to)
}
func SameEndpoint(a, b *ordjson.Object) bool {
	return store.EndpointFromContext(a).Native().SameAuthority(store.EndpointFromContext(b).Native())
}
func EndpointRecord(endpoint *ordjson.Object) *ordjson.Object {
	return store.EndpointFromContext(endpoint).Object()
}
func absent(err error) bool {
	var rpc *hcclient.RPCError
	if !errors.As(err, &rpc) {
		return false
	}
	var data struct {
		Kind string `json:"kind"`
	}
	return json.Unmarshal(rpc.Data, &data) == nil && data.Kind == "not_found"
}

// ReviewerNativeStopped proves the exact old native terminal and process ended.
// Surviving writers block replacement; only the current receipt-bound worker
// process and its observed descendants can be excluded.
func ReviewerNativeStopped(ctx context.Context, s *store.Store, task, old *ordjson.Object, successors ...store.Endpoint) (*ordjson.Object, error) {
	endpoint := store.EndpointFromContext(old)
	if err := endpoint.Validate(); err != nil {
		return nil, err
	}
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID}
	terminal, err := client.Get(ctx, endpoint.Address)
	if err != nil && !absent(err) {
		return nil, err
	}
	if err == nil && (terminal.State == "live" || terminal.CreatedMS != endpoint.CreatedMS) {
		return nil, fmt.Errorf("recorded reviewer is live or replaced")
	}
	registration, err := s.Registration(endpoint)
	if err != nil {
		return nil, err
	}
	var bound incarnation.NativeEvidence
	if err = decode(field(registration, "incarnation"), &bound); err != nil || !bound.Endpoint.SameAuthority(endpoint.Native()) || bound.PID == 0 || bound.Foreground == nil {
		return nil, fmt.Errorf("reviewer process identity missing")
	}
	for _, pid := range []int{int(bound.PID), int(bound.Foreground.PID)} {
		alive, e := proc.PIDRunning(pid)
		if e != nil || alive {
			return nil, fmt.Errorf("recorded reviewer process remains live or unobservable")
		}
		kids, e := proc.Descendants(pid)
		if e != nil || len(kids) > 0 {
			return nil, fmt.Errorf("reviewer descendants remain live or unobservable")
		}
	}
	exclude := map[int]bool{}
	worker := store.EndpointFromContext(object(task, "worker_endpoint"))
	for _, worker := range append([]store.Endpoint{worker}, successors...) {
		if worker.Validate() == nil {
			reg, e := s.Registration(worker)
			if e != nil {
				return nil, e
			}
			var rec incarnation.NativeEvidence
			var receipt incarnation.NativeReceipt
			c := hcclient.Client{Socket: worker.Socket, CoreID: worker.CoreID}
			live, e := c.Get(ctx, worker.Address)
			if e == nil && decode(field(reg, "incarnation"), &rec) == nil && decode(field(reg, "native_receipt"), &receipt) == nil && incarnation.JudgeNative(field(reg, "incarnation"), worker.Native(), live, &receipt).Verified {
				for _, pid := range []int{int(rec.PID), int(rec.Foreground.PID)} {
					exclude[pid] = true
					kids, e := proc.Descendants(pid)
					if e != nil {
						return nil, e
					}
					for _, p := range kids {
						exclude[p] = true
					}
				}
			}
		}
	}
	inside, err := proc.ProcessesBoundTo(text(task, "worktree"), exclude)
	if err != nil {
		return nil, err
	}
	if len(inside) > 0 {
		return nil, fmt.Errorf("reviewer checkout still has writers; replacement refused")
	}
	result := ordjson.NewObject()
	result.Set("at", store.Now())
	result.Set("outcome", "stopped")
	result.Set("endpoint", endpoint.Object())
	return result, nil
}

func Run(s *store.Store, ctx *ordjson.Object, taskID, workerTerminal, reviewerTerminal string, parentOnly bool, pump returns.PumpOpts) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	if (parentOnly && workerTerminal != "") || (parentOnly && reviewerTerminal != "") || (workerTerminal != "" && reviewerTerminal != "") || (!parentOnly && workerTerminal == "" && reviewerTerminal == "") {
		return nil, fmt.Errorf("choose exactly one of --parent-only, --worker-terminal, or --reviewer-terminal")
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	if err = s.CheckMachine(task); err != nil {
		return nil, err
	}
	if err = repair.RefuseDuringCleanup(task, "Native rebind"); err != nil {
		return nil, err
	}
	var intent taskcheckout.Intent
	if err = decode(field(task, "checkout_intent"), &intent); err != nil {
		return nil, err
	}
	if _, err = taskcheckout.Verify(context.Background(), intent, false); err != nil {
		return nil, err
	}
	var replacement, old *ordjson.Object
	key, role, address := "worker_endpoint", "worker", workerTerminal
	if reviewerTerminal != "" {
		key, role, address = "reviewer", "reviewer", reviewerTerminal
	}
	if !parentOnly {
		old = object(task, key)
		previous := store.EndpointFromContext(old)
		if previous.Validate() != nil {
			return nil, fmt.Errorf("recorded native endpoint missing")
		}
		if previous.Address == address {
			return nil, fmt.Errorf("replacement terminal must differ")
		}
		current := store.EndpointFromContext(ctx)
		candidate := previous
		candidate.CoreID = current.CoreID
		candidate.Socket = current.Socket
		candidate.Session = current.Session
		candidate.Address = address
		candidate.BindingGeneration++
		lookupClient := hcclient.Client{Socket: candidate.Socket, CoreID: candidate.CoreID}
		lookup, cancelLookup := context.WithTimeout(context.Background(), 5*time.Second)
		observed, lookupErr := lookupClient.Get(lookup, candidate.Address)
		cancelLookup()
		if lookupErr != nil {
			return nil, lookupErr
		}
		candidate.CreatedMS = observed.CreatedMS
		registration, err := s.Registration(candidate)
		if err != nil {
			return nil, fmt.Errorf("replacement needs a matching bootstrap registration: %w", err)
		}
		if registration == nil {
			return nil, fmt.Errorf("replacement needs a matching bootstrap registration")
		}
		candidate = store.EndpointFromContext(registration)
		if candidate.Task != taskID || candidate.Role != role || candidate.Attempt != previous.Attempt || candidate.BindingGeneration <= previous.BindingGeneration {
			return nil, fmt.Errorf("replacement receipt has no matching task/role/attempt and successor binding generation")
		}
		observe, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client := hcclient.Client{Socket: candidate.Socket, CoreID: candidate.CoreID}
		terminal, err := client.Get(observe, candidate.Address)
		if err != nil {
			return nil, err
		}
		var receipt incarnation.NativeReceipt
		if err = decode(field(registration, "native_receipt"), &receipt); err != nil {
			return nil, err
		}
		verdict := incarnation.JudgeNative(field(registration, "incarnation"), candidate.Native(), terminal, &receipt)
		if !verdict.Verified {
			return nil, fmt.Errorf("replacement has no current process/receipt authority: %s", verdict.Reason)
		}
		if filepath.Clean(terminal.Cwd) != filepath.Clean(intent.Path) {
			return nil, fmt.Errorf("replacement cwd differs from task checkout")
		}
		if _, err = ReviewerNativeStopped(observe, s, task, old, candidate); err != nil {
			return nil, err
		}
		replacement = candidate.Object()
		if role == "reviewer" {
			for _, key := range []string{"harness", "argv", "permissions", "skill_path", "skill_sha256", "candidate"} {
				if value, ok := old.Get(key); ok {
					replacement.Set(key, value)
				}
			}
		}
		for _, k := range []string{"native_template", "native_receipt", "incarnation"} {
			replacement.Set(k, field(registration, k))
		}
	}
	scopeTarget := store.EndpointFromContext(ctx)
	if replacement != nil {
		scopeTarget = store.EndpointFromContext(replacement)
	}
	coverageCtx, cancelCoverage := context.WithTimeout(context.Background(), 10*time.Second)
	_, err = hookstatus.EnsureScope(coverageCtx, s, ctx, scopeTarget, pump.RuntimeRoot, pump.SumctlPath)
	cancelCoverage()
	if err != nil {
		return nil, fmt.Errorf("binding unchanged; native observer coverage: %w", err)
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	current, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	if !parentOnly {
		if !SameEndpoint(object(current, key), old) {
			return nil, fmt.Errorf("endpoint changed during rebind")
		}
		current.Set(key, replacement)
	}
	current.Set("parent", ctx)
	body := ordjson.NewObject()
	body.Set("from", old)
	body.Set("to", replacement)
	body.Set("parent_only", parentOnly)
	if _, err = evidence.Append(current, "native-rebind", "coordinator", body, nil, ctx); err != nil {
		return nil, err
	}
	if err = s.SaveTask(current); err != nil {
		return nil, err
	}
	if err = unlock(); err != nil {
		return nil, err
	}
	unlock = nil
	if err = returns.MarkDirty(s); err != nil {
		return nil, fmt.Errorf("binding saved; reconciliation signal failed: %w", err)
	}
	return current, nil
}
