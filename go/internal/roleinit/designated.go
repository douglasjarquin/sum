package roleinit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/douglasjarquin/sum/go/internal/cleanup"
	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/hookstatus"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/lifecycle"
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

func newInstanceID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// observeOwner judges the recorded coordinator pane's current occupant against the owner record. Only absent
// (pane_not_found) and replaced (a different terminal and shell, no matching native session) prove it gone.
func observeOwner(s *store.Store, herdrPath string, owner *ordjson.Object) incarnation.Verdict {
	host, err := s.Machine()
	if err != nil {
		return incarnation.Verdict{Outcome: incarnation.Unobservable, Reason: err.Error()}
	}
	if !host.Is(asString(owner, "machine")) {
		return incarnation.Verdict{Outcome: "other-machine", Reason: "the recorded coordinator is on another machine"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*incarnation.ObserveTimeout)
	defer cancel()
	recorded, occupiedAt := incarnation.CoordinatorRecord(owner)
	return incarnation.Pane(incarnation.SessionCall(ctx, herdrPath, asString(owner, "session")), asString(owner, "pane"), recorded, occupiedAt)
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
	// The occupant of this address now: terminal and native session from the pane get above, shell from one
	// process-info read. Every role below is judged against it, and it is what this init records.
	self := incarnation.FromInfo(paneValue)
	observeCtx, cancelObserve := context.WithTimeout(context.Background(), 2*incarnation.ObserveTimeout)
	selfShell, selfShellErr := incarnation.ProbeShell(incarnation.SessionCall(observeCtx, herdrPath, session), paneID)
	cancelObserve()
	self.Shell = selfShell
	selfProbe := func() (*incarnation.Shell, error) { return selfShell, selfShellErr }
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

	// The coordinator core is validated before the lock: a runtime without it grants or keeps no coordinator role.
	core, coreErr := coordinatorCore(opts.RuntimeRoot)

	// A reclaim observes the recorded coordinator before the state lock (no Herdr call runs under it) and proceeds
	// only if the owner record is unchanged when the lock is held.
	var reclaimVerdict incarnation.Verdict
	var reclaimSeen []byte
	if opts.Reclaim {
		if seen, ownerErr := s.Owner(); ownerErr != nil {
			return nil, ownerErr
		} else if seen != nil {
			reclaimVerdict = observeOwner(s, herdrPath, seen)
			reclaimSeen, _ = ordjson.MarshalCompact(seen)
		}
	}

	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	released := false
	defer func() {
		if !released {
			_ = unlock()
		}
	}()

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
	// One task read for this operation, taken under the lock: it decides the role here and selects work for the
	// delivery pass below, which re-reads every task it writes.
	tasks, err := s.AllTasks()
	if err != nil {
		return nil, err
	}
	task, err := matchingTaskIn(s, tasks, store.EndpointFromContext(ctx))
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
	now := store.Now()
	observed := self.Record(now)
	// judged is the verdict on the authority recorded for this address, if any; it is reported and, when it could not
	// be established either way, leaves the registration as it was.
	var judged *incarnation.Verdict
	var judgedRole string
	workerVerified := false
	prevRole := asString(previous, "role")
	if task != nil || prevRole == "worker" {
		workerTask := any(nil)
		if task != nil {
			workerTask, _ = task.Get("id")
		} else {
			workerTask, _ = previous.Get("task")
		}
		v := incarnation.Verdict{Outcome: incarnation.Unrecorded, Reason: "no worker registration for this task records this pane; missing metadata is not proof of a match"}
		if recordedValue, occupiedAt, ok := incarnation.WorkerRecord(previous, workerTask); ok {
			v = incarnation.Judge(recordedValue, occupiedAt, self, selfProbe)
		}
		judged, judgedRole, workerVerified = &v, "worker", v.Verified
	}

	result := ordjson.NewObject()
	result.Set("home", s.Home)
	result.Set("installation", true)
	result.Set("instance", instanceStr)
	result.Set("endpoint", ctx)
	result.Set("upgraded", false)

	ownsCoordinator := false
	if owner != nil {
		ownsAddress, matchErr := s.Matches(owner, store.EndpointFromContext(ctx))
		if matchErr != nil {
			return nil, matchErr
		}
		if ownsAddress && !workerVerified {
			recordedValue, occupiedAt := incarnation.CoordinatorRecord(owner)
			v := incarnation.Judge(recordedValue, occupiedAt, self, selfProbe)
			judged, judgedRole, ownsCoordinator = &v, "coordinator", v.Verified
		}
	}
	var role string
	var taskID any
	switch {
	case workerVerified:
		if opts.Role == "coordinator" {
			return nil, fmt.Errorf("This pane is a dispatched worker; it cannot become the coordinator.")
		}
		role = "worker"
		if task != nil {
			taskID, _ = task.Get("id")
		} else {
			taskID, _ = previous.Get("task")
		}
	case ownsCoordinator:
		if opts.Role == "developer" {
			return nil, fmt.Errorf("This pane owns the coordinator role. Initialize a developer session from another pane; ownership is not released implicitly.")
		}
		if coreErr != nil {
			return nil, coreErr
		}
		role = "coordinator"
		taskID = nil
		changed := false
		if recorded := asString(owner, "machine"); recorded != asString(ctx, "machine") {
			owner.Set("machine", asString(ctx, "machine"))
			changed = true
		}
		if _, hasRole := owner.Get("role"); !hasRole {
			owner.Set("role", "coordinator")
			owner.Set("instance", instanceStr)
			owner.Set("sum_version", contract.SumVersion)
			owner.Set("upgraded_at", now)
			changed = true
			result.Set("upgraded", true)
		}
		// A verified occupant adopts the wake protocol this helper speaks; an owner record an older helper wrote
		// stays legacy (uncoalesced) until it does.
		if _, adopted := owner.Get("wake_protocol"); !adopted {
			owner.Set("wake_protocol", json.Number(fmt.Sprint(contract.WakeProtocol)))
			changed = true
		}
		// A verified occupant refreshes the evidence it is judged by next time: a legacy record is adopted here, and
		// a handoff, restore, or new conversation records the terminal and session it now has.
		recordedValue, _ := owner.Get("incarnation")
		if refreshed, differs := incarnation.Refresh(recordedValue, self, now); differs {
			owner.Set("incarnation", refreshed)
			changed = true
		}
		if changed {
			if err := ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
				return nil, err
			}
		}
	case judged != nil && judgedRole == "worker" && !judged.Verified:
		// A pane a task records as its worker, whose occupant is not the recorded one, claims nothing.
		if opts.Role == "coordinator" {
			return nil, fmt.Errorf("This pane is a task's recorded worker pane, but its occupant is not the recorded worker (%s: %s); it cannot claim the coordinator role. %s", judged.Outcome, judged.Reason, incarnation.Recovery("worker", judged.Outcome))
		}
		role = "developer"
		taskID = nil
	case owner == nil && (opts.Role == "" || opts.Role == "coordinator"):
		if coreErr != nil {
			return nil, coreErr
		}
		owner = copyEndpoint(ctx)
		owner.Set("role", "coordinator")
		owner.Set("instance", instanceStr)
		owner.Set("sum_version", contract.SumVersion)
		owner.Set("claimed_at", now)
		owner.Set("incarnation", observed)
		owner.Set("wake_protocol", json.Number(fmt.Sprint(contract.WakeProtocol)))
		if err := ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
			return nil, err
		}
		role = "coordinator"
		taskID = nil
	case opts.Role == "coordinator":
		if !opts.Reclaim {
			if judged != nil && judgedRole == "coordinator" {
				return nil, fmt.Errorf("This pane is the recorded coordinator pane, but its occupant is not the recorded one (%s: %s). %s", judged.Outcome, judged.Reason, incarnation.Recovery("coordinator", judged.Outcome))
			}
			return nil, fmt.Errorf("Coordinator is owned by pane %s in session %s on %s. Inspect it; use --reclaim only for a deliberate, verified takeover. Task parent routes stay unchanged either way.", asString(owner, "pane"), asString(owner, "session"), asString(owner, "machine"))
		}
		if current, _ := ordjson.MarshalCompact(owner); string(current) != string(reclaimSeen) {
			return nil, fmt.Errorf("Refusing reclaim: the coordinator record changed while its pane was observed. Nothing was claimed; inspect it and retry.")
		}
		if reclaimVerdict.Outcome != incarnation.Absent && reclaimVerdict.Outcome != incarnation.Replaced {
			return nil, fmt.Errorf("Refusing reclaim: recorded coordinator pane is %s (%s). Only a pane Herdr reports as pane_not_found, or one a different occupant now holds (a different terminal and shell, no matching native session), can be reclaimed; an existing, unreachable, uncertain, or unprovable coordinator pane is not permission to take over.", reclaimVerdict.Outcome, reclaimVerdict.Reason)
		}
		if coreErr != nil {
			return nil, coreErr
		}
		from := ordjson.NewObject()
		for _, key := range []string{"machine", "session", "pane", "at", "claimed_at", "incarnation"} {
			v, _ := owner.Get(key)
			from.Set(key, v)
		}
		owner = copyEndpoint(ctx)
		owner.Set("role", "coordinator")
		owner.Set("instance", instanceStr)
		owner.Set("sum_version", contract.SumVersion)
		owner.Set("claimed_at", now)
		owner.Set("incarnation", observed)
		owner.Set("reclaimed_from", from)
		owner.Set("previous_observed", reclaimVerdict.Outcome)
		owner.Set("wake_protocol", json.Number(fmt.Sprint(contract.WakeProtocol)))
		if err := ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
			return nil, err
		}
		result.Set("reclaimed", true)
		role = "coordinator"
		taskID = nil
		judged = nil
	default:
		role = "developer"
		taskID = nil
	}
	result.Set("incarnation", incarnationView(judged, judgedRole, role, observed))

	// An occupant that could be neither proven nor disproven leaves the registration it was judged by untouched, so a
	// transient Herdr failure never costs a worker or coordinator its record.
	if judged != nil && !judged.Verified && (judged.Outcome == incarnation.Unobservable || judged.Outcome == incarnation.Unrecorded) && role == "developer" {
		if err := unlock(); err != nil {
			return nil, err
		}
		released = true
		result.Set("role", role)
		result.Set("task", nil)
		result.Set("registered", false)
		result.Set("registration", previous)
		coordinator, err := s.Owner()
		if err != nil {
			return nil, err
		}
		result.Set("coordinator", coordinator)
		result.Set("procedure", roleProcedure(opts.RuntimeRoot, s, role, nil, nil))
		result.Set("note", "This pane's recorded role could not be verified, so it runs as a developer and its registration was left unchanged. "+incarnation.Recovery(judgedRole, judged.Outcome)+" Role bookkeeping is not an OS-level sandbox.")
		return result, nil
	}

	// A verified occupant keeps the recorded shell when this init could not read it; any other occupant is recorded as
	// observed.
	registrationRecord := any(observed)
	if judged != nil && judged.Verified && previous != nil {
		previousValue, _ := previous.Get("incarnation")
		if refreshed, differs := incarnation.Refresh(previousValue, self, now); differs {
			registrationRecord = refreshed
		} else {
			registrationRecord = previousValue
		}
	}
	registration, err := s.Register(store.EndpointFromContext(ctx), role, taskID, registrationRecord)
	if err != nil {
		return nil, err
	}
	if err := unlock(); err != nil {
		return nil, err
	}
	released = true

	if role == "coordinator" {
		result.Set("contract", versions.ContractState(s))
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
		// Registration is done; delivery is one budgeted pass. PR observation, evidence publication, and cleanup are
		// explicit maintenance (`sweep`, `pr reconcile`, `cleanup`), listed below with their commands.
		pumped, pumpErr := returns.Pump(s, returns.PumpOpts{
			RuntimeRoot: opts.RuntimeRoot,
			SumctlPath:  opts.SumctlPath,
			Ctx:         ctx,
			Inline:      true,
			Snapshot:    tasks,
			// The coordinator role was granted to this pane's verified occupant above.
			CallerVerifiedRole: "coordinator",
		})
		if pumpErr != nil {
			return nil, pumpErr
		}
		result.Set("returns", pumped)
		result.Set("maintenance", lifecycle.Pending(s, opts.SumctlPath, tasks))
		hook, hookErr := hookstatus.SummaryOf(s, tasks)
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
	procedureView := any(nil)
	if workerTask := task; role == "worker" && workerTask == nil {
		// A verified worker whose task no longer matches this pane (archived, say) still gets its procedure.
		if id, ok := taskID.(string); ok && id != "" {
			if recorded, readErr := s.ReadTask(id); readErr != nil {
				procedureView = []any{unreadableProcedure(readErr)}
			} else {
				procedureView = roleProcedure(opts.RuntimeRoot, s, role, recorded, core)
			}
		}
	} else {
		procedureView = roleProcedure(opts.RuntimeRoot, s, role, task, core)
	}
	result.Set("procedure", procedureView)
	note := map[string]string{
		"coordinator": "You are the coordinator for this instance. Read the coordinator core at the path under `procedure` before any other step, then follow its startup steps.",
		"worker":      "You are a dispatched worker. Follow your brief and the required files under `procedure`; do not run coordinator startup.",
		"developer":   "Another session owns coordination. Read the developer procedure under `procedure`. Do not run coordinator startup, dispatch, or setup here; develop sum only in a development checkout. Role bookkeeping is not an OS-level sandbox.",
	}[role]
	if role == "coordinator" {
		contractValue, _ := result.Get("contract")
		if contractObj, ok := contractValue.(*ordjson.Object); ok {
			if requested, has := contractObj.Get("requested"); has && requested != nil && requested != "" {
				note += fmt.Sprintf(" Operating contract revision %v is requested: read it and run `sumctl refresh adopt --coordinator %v` before other work.", requested, requested)
			}
		}
	}
	if role == "developer" && judged != nil && !judged.Verified {
		note = earlierOccupantNote(judgedRole, *judged)
	}
	result.Set("note", note)
	return result, nil
}

// incarnationView is the init output's account of which recorded incarnation this pane was judged against.
func incarnationView(judged *incarnation.Verdict, judgedRole, role string, observed *ordjson.Object) *ordjson.Object {
	view := ordjson.NewObject()
	if judged == nil {
		view.Set("judged", nil)
		view.Set("outcome", nil)
		view.Set("reason", "no earlier coordinator or worker record names this pane; this init records its occupant")
	} else {
		view.Set("judged", judgedRole)
		verdict := judged.Object()
		for _, key := range verdict.Keys() {
			v, _ := verdict.Get(key)
			view.Set(key, v)
		}
		if !judged.Verified {
			view.Set("recovery", incarnation.Recovery(judgedRole, judged.Outcome))
		}
	}
	view.Set("observed", observed)
	return view
}
