package roleinit

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/douglasjarquin/sum/go/internal/cleanup"
	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/hookstatus"
	"github.com/douglasjarquin/sum/go/internal/metadata"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/project"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

type DesignatedOpts struct {
	RuntimeRoot string
	SumctlPath  string
	Store       *store.Store
	Ctx         *ordjson.Object
	Role        string
	Task        string
	Reclaim     bool
}

func asString(o *ordjson.Object, key string) string {
	if o == nil {
		return ""
	}
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}

func unwrapPane(value any) *ordjson.Object {
	obj, _ := value.(*ordjson.Object)
	if obj == nil {
		return nil
	}
	if nested, ok := obj.Get("pane"); ok {
		if inner, is := nested.(*ordjson.Object); is {
			return inner
		}
	}
	return obj
}

func identityEquals(a, b *ordjson.Object) bool {
	if a == nil || b == nil {
		return a == b
	}
	return asString(a, "machine") == asString(b, "machine") &&
		asString(a, "session") == asString(b, "session") &&
		asString(a, "pane") == asString(b, "pane")
}

func newInstanceID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func observeOwner(runtimeRoot string, owner *ordjson.Object) (string, string) {
	host, err := os.Hostname()
	if err != nil {
		return "uncertain", err.Error()
	}
	if asString(owner, "machine") != host {
		return "other-machine", ""
	}
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		return "uncertain", err.Error()
	}
	_, code, err := herdrclient.Observe(herdrPath, asString(owner, "session"), 5*time.Second, "pane", "get", asString(owner, "pane"))
	if err != nil {
		detail := err.Error()
		if len(detail) > 300 {
			detail = detail[len(detail)-300:]
		}
		return "uncertain", detail
	}
	if code == "pane_not_found" {
		return "absent", code
	}
	if code != "" {
		return "uncertain", code
	}
	return "present", ""
}

func copyEndpoint(ctx *ordjson.Object) *ordjson.Object {
	owner := ordjson.NewObject()
	for _, key := range ctx.Keys() {
		v, _ := ctx.Get(key)
		owner.Set(key, v)
	}
	return owner
}

func InitDesignated(opts DesignatedOpts) (*ordjson.Object, error) {
	if opts.Role == "worker" && opts.Task == "" {
		return nil, fmt.Errorf("--role worker needs --task TASK_ID.")
	}
	s := opts.Store
	ctx := opts.Ctx
	herdrPath, err := toolpath.Find(opts.RuntimeRoot, "herdr")
	if err != nil {
		return nil, err
	}
	if _, err := herdrclient.EnsureVersion(herdrPath, contract.HerdrCLI); err != nil {
		return nil, err
	}
	session := asString(ctx, "session")
	paneID := asString(ctx, "pane")
	paneValue, err := herdrclient.Call(herdrPath, session, 5*time.Second, "pane", "get", paneID)
	if err != nil {
		return nil, err
	}
	pane := unwrapPane(paneValue)
	installRoot := project.InstallationOf(s, opts.RuntimeRoot)
	if pane != nil {
		var nested *ordjson.Object
		for _, key := range []string{"foreground_cwd", "cwd", "working_directory"} {
			cwd, _ := pane.Get(key)
			cwdStr, _ := cwd.(string)
			if found := project.PaneInside(s, installRoot, cwdStr); found != nil {
				nested = found
				break
			}
		}
		if nested != nil {
			matched, matchErr := matchingTask(s, store.EndpointFromContext(ctx))
			if matchErr != nil {
				return nil, matchErr
			}
			if matched == nil {
				name, _ := nested.Get("name")
				path, _ := nested.Get("path")
				why, _ := nested.Get("why")
				return nil, fmt.Errorf("This pane works inside managed project %v (%v) (%v). A project session is not a sum session: no role was registered and nothing was claimed. Coordinate from the installation directory; a parent directory's instructions grant a project pane nothing.", name, path, why)
			}
		}
	}

	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()

	statePath := filepath.Join(s.Home, "state.json")
	stateValue, err := ordjson.ReadFile(statePath)
	if err != nil {
		return nil, err
	}
	state, ok := stateValue.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("state.json is not a JSON object")
	}
	instance, hasInstance := state.Get("instance")
	instanceStr, _ := instance.(string)
	if !hasInstance || instanceStr == "" {
		id, err := newInstanceID()
		if err != nil {
			return nil, err
		}
		state.Set("instance", id)
		if err := ordjson.WriteFile(statePath, state); err != nil {
			return nil, err
		}
		instanceStr = id
	}

	owner, err := s.Owner()
	if err != nil {
		return nil, err
	}
	previous, err := s.Registration(store.EndpointFromContext(ctx))
	if err != nil {
		return nil, err
	}
	task, err := matchingTask(s, store.EndpointFromContext(ctx))
	if err != nil {
		return nil, err
	}
	if opts.Task != "" {
		recorded, readErr := s.ReadTask(opts.Task)
		if readErr != nil {
			return nil, readErr
		}
		taskID := any(nil)
		if task != nil {
			taskID, _ = task.Get("id")
		}
		if task == nil || taskID != opts.Task {
			pane, _ := recorded.Get("pane")
			return nil, fmt.Errorf("This pane is not the recorded worker pane of %s (%v). Worker identity comes from dispatch records, not from the brief text.", opts.Task, pane)
		}
	}

	result := ordjson.NewObject()
	result.Set("home", s.Home)
	result.Set("installation", true)
	result.Set("instance", instanceStr)
	result.Set("endpoint", ctx)
	result.Set("upgraded", false)

	var role string
	var taskID any
	prevRole := asString(previous, "role")
	switch {
	case task != nil || (previous != nil && prevRole == "worker"):
		if opts.Role == "coordinator" {
			return nil, fmt.Errorf("This pane is a dispatched worker; it cannot become the coordinator.")
		}
		role = "worker"
		if task != nil {
			taskID, _ = task.Get("id")
		} else {
			taskID, _ = previous.Get("task")
		}
	case owner != nil && identityEquals(owner, ctx):
		if opts.Role == "developer" {
			return nil, fmt.Errorf("This pane owns the coordinator role. Initialize a developer session from another pane; ownership is not released implicitly.")
		}
		role = "coordinator"
		taskID = nil
		if _, hasRole := owner.Get("role"); !hasRole {
			owner.Set("role", "coordinator")
			owner.Set("instance", instanceStr)
			owner.Set("sum_version", contract.SumVersion)
			owner.Set("upgraded_at", store.Now())
			if err := ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
				return nil, err
			}
			result.Set("upgraded", true)
		}
	case owner == nil && (opts.Role == "" || opts.Role == "coordinator"):
		owner = copyEndpoint(ctx)
		owner.Set("role", "coordinator")
		owner.Set("instance", instanceStr)
		owner.Set("sum_version", contract.SumVersion)
		owner.Set("claimed_at", store.Now())
		if err := ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
			return nil, err
		}
		role = "coordinator"
		taskID = nil
	case opts.Role == "coordinator":
		if !opts.Reclaim {
			return nil, fmt.Errorf("Coordinator is owned by pane %s in session %s on %s. Inspect it; use --reclaim only for a deliberate, verified takeover. Task parent routes stay unchanged either way.", asString(owner, "pane"), asString(owner, "session"), asString(owner, "machine"))
		}
		observed, detail := observeOwner(opts.RuntimeRoot, owner)
		if observed != "absent" {
			return nil, fmt.Errorf("Refusing reclaim: recorded coordinator pane is %s (%s). Only a pane Herdr reports as pane_not_found can be reclaimed; an existing, unreachable, or uncertain coordinator pane is not permission to take over.", observed, detail)
		}
		from := ordjson.NewObject()
		for _, key := range []string{"machine", "session", "pane", "at", "claimed_at"} {
			v, _ := owner.Get(key)
			from.Set(key, v)
		}
		owner = copyEndpoint(ctx)
		owner.Set("role", "coordinator")
		owner.Set("instance", instanceStr)
		owner.Set("sum_version", contract.SumVersion)
		owner.Set("claimed_at", store.Now())
		owner.Set("reclaimed_from", from)
		owner.Set("previous_observed", observed)
		if err := ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
			return nil, err
		}
		result.Set("reclaimed", true)
		role = "coordinator"
		taskID = nil
	default:
		role = "developer"
		taskID = nil
	}

	registration, err := s.Register(store.EndpointFromContext(ctx), role, taskID)
	if err != nil {
		return nil, err
	}

	if role == "coordinator" {
		result.Set("contract", versions.ContractState(s))
		tasks, taskErr := s.AllTasks()
		if taskErr != nil {
			return nil, taskErr
		}
		pending := make([]any, 0)
		for _, t := range tasks {
			if row := cleanup.Pending(t); row != nil {
				item := ordjson.NewObject()
				id, _ := t.Get("id")
				item.Set("task", id)
				for _, key := range row.Keys() {
					v, _ := row.Get(key)
					item.Set(key, v)
				}
				pending = append(pending, item)
			}
		}
		result.Set("cleanup_pending", pending)
		pumped, pumpErr := returns.Pump(s, returns.PumpOpts{
			RuntimeRoot: opts.RuntimeRoot,
			SumctlPath:  opts.SumctlPath,
			Ctx:         ctx,
			Reason:      "saved task state needs attention",
			Inline:      true,
		})
		if pumpErr != nil {
			return nil, pumpErr
		}
		result.Set("returns", pumped)
		hook, hookErr := hookstatus.Summary(s)
		if hookErr != nil {
			return nil, hookErr
		}
		result.Set("hook", hook)
		result.Set("metadata", metadata.Summary(s))
	}

	coordinator, err := s.Owner()
	if err != nil {
		return nil, err
	}
	result.Set("role", role)
	result.Set("task", taskID)
	result.Set("registration", registration)
	result.Set("coordinator", coordinator)
	note := map[string]string{
		"coordinator": "You are the coordinator for this instance. Continue the coordinator startup steps.",
		"worker":      "You are a dispatched worker. Follow your brief; do not run coordinator startup.",
		"developer":   "Another session owns coordination. Do not run coordinator startup, dispatch, or setup here; develop sum only in a development checkout. Role bookkeeping is not an OS-level sandbox.",
	}[role]
	if role == "coordinator" {
		contractValue, _ := result.Get("contract")
		if contractObj, ok := contractValue.(*ordjson.Object); ok {
			if requested, has := contractObj.Get("requested"); has && requested != nil && requested != "" {
				note += fmt.Sprintf(" Operating contract revision %v is requested: read it and run `sumctl refresh adopt --coordinator %v` before other work.", requested, requested)
			}
		}
	}
	result.Set("note", note)
	return result, nil
}
