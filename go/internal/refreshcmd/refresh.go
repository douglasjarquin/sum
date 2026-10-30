package refreshcmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/brief"
	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/maintenance"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/procedure"
	"github.com/douglasjarquin/sum/go/internal/returns"
	sumruntime "github.com/douglasjarquin/sum/go/internal/runtime"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

func sha256Text(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asObject(v any) *ordjson.Object {
	o, _ := v.(*ordjson.Object)
	return o
}

func jsonNumber(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func runtimeSHA(runtimeRoot string) string {
	return sumruntime.SourceSHA(runtimeRoot)
}

func deferredCapabilities(recorded *ordjson.Object) []any { return []any{} }

func refreshSummary(rows []any, excluded []any) *ordjson.Object {
	counts := ordjson.NewObject()
	for _, state := range []string{"confirmed", "submitted-unconfirmed", "pending-busy", "pending-unreachable", versions.RefreshUnreachable, "pane-closed", "not-requested", "capability-deferred"} {
		counts.Set(state, jsonNumber(0))
	}
	inc := func(state string) {
		v, _ := counts.Get(state)
		n := 0
		if num, ok := v.(json.Number); ok {
			if i, err := num.Int64(); err == nil {
				n = int(i)
			}
		}
		counts.Set(state, jsonNumber(n+1))
	}
	for _, raw := range rows {
		row := asObject(raw)
		state := asString(func() any { v, _ := row.Get("state"); return v }())
		if state == "" {
			state = "pending-unreachable"
			row.Set("state", state)
		}
		if _, has := row.Get("deferred"); !has {
			row.Set("deferred", []any{})
		}
		inc(state)
		if def, _ := row.Get("deferred"); state != "capability-deferred" {
			if list, ok := def.([]any); ok && len(list) > 0 {
				inc("capability-deferred")
			}
		}
	}
	result := ordjson.NewObject()
	result.Set("counts", counts)
	result.Set("targets", rows)
	result.Set("excluded", excluded)
	result.Set("note", "Bounded status from saved records and one delivery attempt per target. No fleet barrier, sleep, polling loop, or relaunch. confirmed = receipt recorded; submitted-unconfirmed = prompt accepted, nothing read yet; pending-* = old contract keeps serving and may be rechecked; unreachable = the worker pane or agent is gone, delivery for that revision is terminal; capability-deferred = a surface this client cannot reload until it restarts.")
	return result
}

// snapshotTimeout bounds each refresh observation and prompt.
const snapshotTimeout = 10 * time.Second

type snapshots struct{}

func newSnapshots(runtimeRoot string) *snapshots { return &snapshots{} }
func fanout(sn *snapshots) *ordjson.Object       { return ordjson.NewObject() }

func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	if errA != nil {
		ra, _ = filepath.Abs(a)
	}
	rb, errB := filepath.EvalSymlinks(b)
	if errB != nil {
		rb, _ = filepath.Abs(b)
	}
	return ra == rb
}

// occupantRecord is the incarnation a refresh target's pane was bound to, judged before the instruction is sent.
type occupantRecord struct {
	revision   string
	role       string
	value      any
	occupiedAt string
	ok         bool
	// current re-reads the record under the state lock right before the prompt; a record that changed since it was
	// judged (a rebind or a new registration) sends nothing. Nil skips the re-read.
	current func() (any, error)
}

// unchanged re-reads the record under the state lock and reports whether it is still the one judged.
func (r occupantRecord) unchanged(s *store.Store) bool {
	if r.current == nil {
		return true
	}
	unlock, err := s.Lock()
	if err != nil {
		return false
	}
	defer unlock()
	now, err := r.current()
	if err != nil {
		return false
	}
	a, _ := ordjson.MarshalCompact(now)
	b, _ := ordjson.MarshalCompact(r.value)
	return string(a) == string(b)
}

// judgeOccupant refuses a target whose pane is no longer the recorded occupant: the agent row the pass observed must be
// the recorded incarnation (a shell probe only when the terminal changed or the record is legacy).
func attemptDelivery(s *store.Store, sn *snapshots, endpoint *ordjson.Object, expectedCwd, message string, host machine.Identity, record occupantRecord, task *ordjson.Object) *ordjson.Object {
	row := ordjson.NewObject()
	fail := func(state string, err error) *ordjson.Object {
		row.Set("state", state)
		row.Set("reason", err.Error())
		return row
	}
	if !host.Is(func() any { v, _ := endpoint.Get("machine"); return v }()) {
		return fail("pending-unreachable", fmt.Errorf("recipient is on another machine"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), returns.DefaultPassBudget)
	defer cancel()
	release, err := returns.LockRecipient(s, ctx, endpoint)
	if err != nil {
		return fail("pending-busy", err)
	}
	defer release()
	recipient, err := returns.ObserveNative(ctx, store.EndpointFromContext(endpoint), expectedCwd)
	if err != nil {
		var native *hcclient.Error
		var diagnostic struct {
			Kind string `json:"kind"`
		}
		if errors.As(err, &native) && native.RPC != nil && json.Unmarshal(native.RPC.Data, &diagnostic) == nil && diagnostic.Kind == "not_found" {
			return fail(versions.RefreshUnreachable, err)
		}
		return fail("pending-busy", err)
	}
	var current *ordjson.Object
	if record.role == "coordinator" {
		current, err = s.Owner()
	} else {
		current, err = s.Registration(store.EndpointFromContext(endpoint))
	}
	if err != nil {
		return fail("pending-unreachable", err)
	}
	if err = recipient.QualifyContext(ctx, current); err != nil {
		return fail("pending-unreachable", err)
	}
	if !record.unchanged(s) {
		return fail("pending-unreachable", fmt.Errorf("recipient binding changed; nothing was sent"))
	}
	// This sidecar records only the effect claim, never another payload queue.
	key := sha256Text(fmt.Sprintf("%s:%s:%s", store.RegistrationKey(recipient.Endpoint), recipient.Endpoint.Attempt, record.revision))
	path := filepath.Join(s.Home, "deliver", "refresh-"+key+".json")
	unlock, err := s.Lock()
	if err != nil {
		return fail("pending-unreachable", err)
	}
	var currentVersions *ordjson.Object
	if record.role == "coordinator" {
		currentVersions, err = versions.ReadContractVersions(s)
	} else {
		currentTask, readErr := s.ReadTask(recipient.Endpoint.Task)
		err = readErr
		if err == nil {
			err = maintenance.Refuse(currentTask, "Procedure refresh")
		}
		if err == nil && !store.EndpointFromContext(returns.ReturnRoute(currentTask, "worker")).Native().SameAuthority(recipient.Endpoint.Native()) {
			err = fmt.Errorf("worker route changed before refresh claim")
		}
		if err == nil {
			currentVersions, err = versions.ReadVersions(s, currentTask)
		}
	}
	if err != nil {
		unlock()
		return fail("pending-unreachable", err)
	}
	requested, _ := currentVersions.Get("requested")
	if requested != record.revision {
		unlock()
		return fail("pending-unreachable", fmt.Errorf("requested revision changed before refresh claim"))
	}
	if err = recipient.Check(s); err != nil {
		unlock()
		return fail("pending-unreachable", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		prior, e := ordjson.ReadFile(path)
		if e != nil {
			unlock()
			return fail("pending-unreachable", e)
		}
		if obj, ok := prior.(*ordjson.Object); ok {
			state, _ := obj.Get("state")
			if state != "not-delivered" {
				unlock()
				row.Set("state", "submitted-unconfirmed")
				row.Set("reason", "an earlier native refresh is submitted or uncertain; matching adoption is required, no automatic replay")
				return row
			}
		}
	} else if !os.IsNotExist(statErr) {
		unlock()
		return fail("pending-unreachable", statErr)
	}
	claim := ordjson.NewObject()
	claim.Set("state", "in-flight")
	claim.Set("at", store.Now())
	claim.Set("endpoint", endpoint)
	err = ordjson.WriteFile(path, claim)
	unlock()
	if err != nil {
		return fail("pending-unreachable", err)
	}
	_, sendErr := recipient.Send(ctx, message)
	state, detail := returns.DeliveryFailure(sendErr)
	unlock, err = s.Lock()
	if err != nil {
		return fail("submitted-unconfirmed", err)
	}
	claim.Set("state", state)
	claim.Set("reason", detail)
	err = ordjson.WriteFile(path, claim)
	unlock()
	if err != nil {
		return fail("submitted-unconfirmed", err)
	}
	if state == "not-delivered" {
		return fail("pending-busy", sendErr)
	}
	row.Set("state", "submitted-unconfirmed")
	row.Set("delivery_effect", state)
	row.Set("reason", detail)
	return row
}

func recordDelivery(versionsObj *ordjson.Object, revisionID string, event *ordjson.Object, runtimeRoot string) {
	refresh, _ := versionsObj.Get("refresh")
	list, _ := refresh.([]any)
	row := ordjson.NewObject()
	row.Set("at", store.Now())
	row.Set("event", "delivery")
	row.Set("revision", revisionID)
	for _, k := range event.Keys() {
		v, _ := event.Get(k)
		row.Set(k, v)
	}
	rt := ordjson.NewObject()
	rt.Set("sum_version", contract.SumVersion)
	rt.Set("sha", runtimeSHA(runtimeRoot))
	row.Set("runtime", rt)
	list = append(list, row)
	if len(list) > 40 {
		list = list[len(list)-40:]
	}
	versionsObj.Set("refresh", list)
}

func refreshTask(s *store.Store, task, ctx *ordjson.Object, sn *snapshots, runtimeRoot, sumctlPath string, host machine.Identity) (*ordjson.Object, error) {
	id := asString(func() any { v, _ := task.Get("id"); return v }())
	row := ordjson.NewObject()
	row.Set("target", "task")
	row.Set("task", id)
	row.Set("harness", func() any { v, _ := task.Get("harness"); return v }())
	row.Set("deferred", []any{})
	workerEndpoint, _ := task.Get("worker_endpoint")
	briefPath, _ := task.Get("brief_path")
	if workerEndpoint == nil || briefPath == nil {
		row.Set("state", "pending-unreachable")
		row.Set("reason", "task has no worker pane or brief yet")
		return row, nil
	}
	staged, err := brief.Regenerate(s, runtimeRoot, sumctlPath, id)
	if err != nil {
		return nil, err
	}
	rev := asObject(func() any { v, _ := staged.Get("revision"); return v }())
	latest := asString(func() any { v, _ := rev.Get("id"); return v }())
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	task, err = s.ReadTask(id)
	if err != nil {
		unlock()
		return nil, err
	}
	versionsObj, err := versions.ReadVersions(s, task)
	if err != nil {
		unlock()
		return nil, err
	}
	var target *ordjson.Object
	list, _ := versionsObj.Get("revisions")
	for _, raw := range asList(list) {
		if item := asObject(raw); item != nil {
			if asString(func() any { v, _ := item.Get("id"); return v }()) == latest {
				target = item
			}
		}
	}
	runtimeRec := asObject(func() any { v, _ := versionsObj.Get("runtime"); return v }())
	row.Set("deferred", deferredCapabilities(runtimeRec))
	active, _ := versionsObj.Get("active")
	if active == latest {
		state := versions.RefreshState(versionsObj)
		row.Set("revision", latest)
		if st, ok := state.Get("state"); ok {
			row.Set("state", st)
		}
		if reason, ok := state.Get("reason"); ok {
			row.Set("reason", reason)
		}
		unlock()
		return row, nil
	}
	existing := versions.RefreshState(versionsObj)
	if asString(func() any { v, _ := versionsObj.Get("requested"); return v }()) == latest && versions.RefreshWorkerGone(existing) {
		row.Set("revision", latest)
		if st, ok := existing.Get("state"); ok {
			row.Set("state", st)
		}
		if reason, ok := existing.Get("reason"); ok {
			row.Set("reason", reason)
		}
		unlock()
		return row, nil
	}
	taskPath, err := s.TaskPath(id)
	if err != nil {
		unlock()
		return nil, err
	}
	state := versions.RevisionView(taskPath, target)
	if ok, _ := state.Get("ok"); ok != true {
		errText, _ := state.Get("error")
		unlock()
		return nil, fmt.Errorf("%v", errText)
	}
	decisionsOnly := versions.DecisionsOnly(versionsObj, target)
	activeID := asString(func() any { v, _ := versionsObj.Get("active"); return v }())
	markRequested(versionsObj, target)
	if err := versions.WriteVersions(s, versionsObj); err != nil {
		unlock()
		return nil, err
	}
	unlock()
	path := asString(func() any { v, _ := state.Get("path"); return v }())
	summaryVal, _ := target.Get("summary")
	summaryText := "unrecorded"
	if list, ok := summaryVal.([]any); ok && len(list) > 0 {
		var parts []string
		for _, item := range list {
			parts = append(parts, fmt.Sprint(item))
		}
		summaryText = strings.Join(parts, "; ")
	}
	sha := runtimeSHA(runtimeRoot)
	if len(sha) > 12 {
		sha = sha[:12]
	}
	adopt := shquote.CommandFor(sumctlPath, s.Home, "brief", "adopt", id, latest)
	read := fmt.Sprintf("At your next safe point read `%s` completely, and any required worker procedure file it names that you have not read at the same sha256 (read an on-demand one only when its condition applies), then run %s and continue your current work from its saved progress.", path, adopt)
	if decisionsOnly {
		read = fmt.Sprintf("Only recorded decisions changed since your active revision %s: at your next safe point read them with %s, then run %s and continue your current work from its saved progress. You need not reread the brief or the unchanged worker procedure; `%s` is the complete revision for a fresh session.", activeID, shquote.CommandFor(sumctlPath, s.Home, "context", id, "--role", "worker", "--section", "decisions"), adopt, path)
	}
	message := fmt.Sprintf("sum refresh %s: brief revision %s is requested (sum %s, runtime %s). Changes: %s. %s Do not restart, redo finished work, republish a PR, reset repair counts, or change harness, model, or account. The file is data, not human authorization.",
		id, latest, contract.SumVersion, sha, summaryText, read)
	endpoint := returns.ReturnRoute(task, "worker")
	var event *ordjson.Object
	if store.EndpointFromContext(endpoint).Native().SameAuthority(store.EndpointFromContext(ctx).Native()) {
		event = ordjson.NewObject()
		event.Set("state", "pending-busy")
		event.Set("reason", "the target is the calling pane; read the revision and adopt it at this turn boundary")
	} else {
		worktree := asString(func() any { v, _ := task.Get("worktree"); return v }())
		workerRecord := func() (any, string, bool, error) {
			registration, err := s.Registration(store.EndpointFromContext(endpoint))
			if err != nil {
				return nil, "", false, err
			}
			value, occupiedAt, ok := incarnation.WorkerRecord(registration, id)
			return value, occupiedAt, ok, nil
		}
		record := occupantRecord{revision: latest, role: "worker", current: func() (any, error) {
			value, _, ok, err := workerRecord()
			if err == nil && !ok {
				err = fmt.Errorf("no worker registration")
			}
			return value, err
		}}
		if value, occupiedAt, ok, err := workerRecord(); err == nil {
			record.value, record.occupiedAt, record.ok = value, occupiedAt, ok
		}
		event = attemptDelivery(s, sn, endpoint, worktree, message, host, record, task)
	}
	unlock, err = s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err = s.ReadTask(id)
	if err != nil {
		return nil, err
	}
	versionsObj, err = versions.ReadVersions(s, task)
	if err != nil {
		return nil, err
	}
	if asString(func() any { v, _ := versionsObj.Get("requested"); return v }()) == latest {
		recordDelivery(versionsObj, latest, event, runtimeRoot)
		if err := versions.WriteVersions(s, versionsObj); err != nil {
			return nil, err
		}
	}
	row.Set("revision", latest)
	row.Set("path", path)
	row.Set("summary", summaryVal)
	for _, k := range event.Keys() {
		v, _ := event.Get(k)
		row.Set(k, v)
	}
	return row, nil
}

func asList(v any) []any {
	list, _ := v.([]any)
	return list
}

// coordinatorCore reads the runtime's coordinator core, validated like any role procedure; a runtime
// without it stages no contract revision.
func coordinatorCore(runtimeRoot string) (string, error) {
	_, data, err := procedure.Load(runtimeRoot, procedure.Coordinator, "no coordinator contract revision was staged")
	return string(data), err
}

func contractPolicy(runtimeRoot, core string) (*ordjson.Object, error) {
	agents, err := os.ReadFile(filepath.Join(runtimeRoot, "AGENTS.md"))
	if err != nil {
		return nil, err
	}
	skillsDir := filepath.Join(runtimeRoot, "skills")
	entries, _ := filepath.Glob(filepath.Join(skillsDir, "*", "SKILL.md"))
	sort.Strings(entries)
	skillHashes := ordjson.NewObject()
	for _, path := range entries {
		text, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		skillHashes.Set(filepath.Base(filepath.Dir(path)), sha256Text(string(text)))
	}
	policy := ordjson.NewObject()
	policy.Set("sum_version", contract.SumVersion)
	policy.Set("runtime_sha", runtimeSHA(runtimeRoot))
	policy.Set("backend", "hc")
	policy.Set("agents_sha256", sha256Text(string(agents)))
	policy.Set("coordinator_sha256", sha256Text(core))
	policy.Set("skills_sha256", skillHashes)
	return policy, nil
}

func contractSummary(previous, policy *ordjson.Object) []any {
	if previous == nil {
		return []any{"initial contract snapshot"}
	}
	var changes []any
	if asString(func() any { v, _ := previous.Get("sum_version"); return v }()) != asString(func() any { v, _ := policy.Get("sum_version"); return v }()) {
		changes = append(changes, fmt.Sprintf("sum_version: %v -> %v", func() any { v, _ := previous.Get("sum_version"); return v }(), func() any { v, _ := policy.Get("sum_version"); return v }()))
	}
	if asString(func() any { v, _ := previous.Get("runtime_sha"); return v }()) != asString(func() any { v, _ := policy.Get("runtime_sha"); return v }()) {
		changes = append(changes, "runtime changed")
	}
	if asString(func() any { v, _ := previous.Get("agents_sha256"); return v }()) != asString(func() any { v, _ := policy.Get("agents_sha256"); return v }()) {
		changes = append(changes, "AGENTS.md changed")
	}
	if asString(func() any { v, _ := previous.Get("coordinator_sha256"); return v }()) != asString(func() any { v, _ := policy.Get("coordinator_sha256"); return v }()) {
		changes = append(changes, procedure.Coordinator.Path+" changed")
	}
	if len(changes) == 0 {
		return []any{"no recorded change"}
	}
	return changes
}

func writeOnce(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("Refusing to overwrite %s", path)
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

func regenerateContract(s *store.Store, runtimeRoot, sumctlPath string) (*ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	versionsObj, err := versions.ReadContractVersions(s)
	if err != nil {
		return nil, err
	}
	core, err := coordinatorCore(runtimeRoot)
	if err != nil {
		return nil, err
	}
	policy, err := contractPolicy(runtimeRoot, core)
	if err != nil {
		return nil, err
	}
	encoded, _ := ordjson.MarshalSortedCompact(policy)
	fingerprint := sha256Text(string(encoded))
	list, _ := versionsObj.Get("revisions")
	revs := asList(list)
	var previous *ordjson.Object
	if len(revs) > 0 {
		previous = asObject(revs[len(revs)-1])
	}
	base := filepath.Join(s.Home, versions.ContractDir)
	if previous != nil && asString(func() any { v, _ := previous.Get("fingerprint"); return v }()) == fingerprint {
		state := versions.RevisionView(base, previous)
		if ok, _ := state.Get("ok"); ok == true {
			result := ordjson.NewObject()
			result.Set("duplicate", true)
			result.Set("revision", state)
			result.Set("active", func() any { v, _ := versionsObj.Get("active"); return v }())
			result.Set("requested", func() any { v, _ := versionsObj.Get("requested"); return v }())
			return result, nil
		}
	}
	maxN := 0
	for _, raw := range revs {
		id := asString(func() any { v, _ := asObject(raw).Get("id"); return v }())
		if strings.HasPrefix(id, "r") {
			var n int
			if _, scanErr := fmt.Sscanf(id, "r%d", &n); scanErr == nil && n > maxN {
				maxN = n
			}
		}
	}
	rid := fmt.Sprintf("r%d", maxN+1)
	var prevPolicy *ordjson.Object
	if previous != nil {
		prevPolicy = asObject(func() any { v, _ := previous.Get("policy"); return v }())
	}
	summary := contractSummary(prevPolicy, policy)
	var summaryLines []string
	for _, item := range summary {
		summaryLines = append(summaryLines, "- "+fmt.Sprint(item))
	}
	agents, _ := os.ReadFile(filepath.Join(runtimeRoot, "AGENTS.md"))
	text := fmt.Sprintf("# sum coordinator contract — %s\n\nThis is the coordinator's operating contract as shipped by sum %s (runtime %s).\nYou remain the coordinator of this installation. This revision does not change your role, your registered pane, the recorded tasks, or their parent routes.\n\n## Refresh procedure\n\n- Read the contract below and the change summary. Then run `%s` to record the receipt.\n- Continue coordination from saved state: `%s` and the task records are the source of truth.\n- Do not restart yourself, re-dispatch running tasks, re-run setup, or re-answer recorded decisions.\n- Native HC status and submission remain observations; a matching adoption receipt is required.\n\n## Change summary\n\n%s\n\n## Operating contract (AGENTS.md at this revision)\n\n%s\n\n## Coordinator core (%s at this revision)\n\n%s\n",
		rid, contract.SumVersion, runtimeSHA(runtimeRoot),
		shquote.CommandFor(sumctlPath, s.Home, "refresh", "adopt", "--coordinator", rid),
		shquote.CommandFor(sumctlPath, s.Home, "inbox", "--live"),
		strings.Join(summaryLines, "\n"), string(agents), procedure.Coordinator.Path, core)
	relative := "contracts/" + rid + ".md"
	if err := writeOnce(filepath.Join(base, relative), text); err != nil {
		return nil, err
	}
	revision := ordjson.NewObject()
	revision.Set("id", rid)
	revision.Set("path", relative)
	revision.Set("status", "staged")
	revision.Set("created_at", store.Now())
	revision.Set("sha256", sha256Text(text))
	revision.Set("fingerprint", fingerprint)
	revision.Set("policy", policy)
	revision.Set("summary", summary)
	revision.Set("verification_affected", false)
	if previous != nil {
		revision.Set("previous", func() any { v, _ := previous.Get("id"); return v }())
	} else {
		revision.Set("previous", nil)
	}
	revs = append(revs, revision)
	versionsObj.Set("revisions", revs)
	if err := versions.WriteContractVersions(s, versionsObj); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("duplicate", false)
	result.Set("revision", versions.RevisionView(base, revision))
	result.Set("active", func() any { v, _ := versionsObj.Get("active"); return v }())
	result.Set("requested", func() any { v, _ := versionsObj.Get("requested"); return v }())
	return result, nil
}

func markRequested(versionsObj, target *ordjson.Object) {
	list, _ := versionsObj.Get("revisions")
	id, _ := target.Get("id")
	for _, raw := range asList(list) {
		rev := asObject(raw)
		if st, _ := rev.Get("status"); st == "requested" {
			rev.Set("status", "superseded")
		}
	}
	target.Set("status", "requested")
	versionsObj.Set("requested", id)
}

func refreshCoordinator(s *store.Store, ctx *ordjson.Object, sn *snapshots, runtimeRoot, sumctlPath string, host machine.Identity) (*ordjson.Object, error) {
	row := ordjson.NewObject()
	row.Set("target", "coordinator")
	row.Set("deferred", []any{})
	staged, err := regenerateContract(s, runtimeRoot, sumctlPath)
	if err != nil {
		return nil, err
	}
	rev := asObject(func() any { v, _ := staged.Get("revision"); return v }())
	latest := asString(func() any { v, _ := rev.Get("id"); return v }())
	owner, err := s.Owner()
	if err != nil {
		return nil, err
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	versionsObj, err := versions.ReadContractVersions(s)
	if err != nil {
		unlock()
		return nil, err
	}
	var target *ordjson.Object
	for _, raw := range asList(func() any { v, _ := versionsObj.Get("revisions"); return v }()) {
		if item := asObject(raw); item != nil && asString(func() any { v, _ := item.Get("id"); return v }()) == latest {
			target = item
		}
	}
	row.Set("deferred", []any{})
	active, _ := versionsObj.Get("active")
	if active == latest {
		state := versions.RefreshState(versionsObj)
		row.Set("revision", latest)
		if st, ok := state.Get("state"); ok {
			row.Set("state", st)
		}
		if reason, ok := state.Get("reason"); ok {
			row.Set("reason", reason)
		}
		unlock()
		return row, nil
	}
	markRequested(versionsObj, target)
	if err := versions.WriteContractVersions(s, versionsObj); err != nil {
		unlock()
		return nil, err
	}
	unlock()
	state := versions.RevisionView(filepath.Join(s.Home, versions.ContractDir), target)
	var event *ordjson.Object
	if owner == nil || store.EndpointFromContext(owner).Native().SameAuthority(store.EndpointFromContext(ctx).Native()) {
		event = ordjson.NewObject()
		event.Set("state", "pending-busy")
		event.Set("reason", "the coordinator is the calling pane; read the contract revision and run `refresh adopt --coordinator` at this turn boundary")
	} else {
		path := asString(func() any { v, _ := state.Get("path"); return v }())
		summaryVal, _ := target.Get("summary")
		var parts []string
		if list, ok := summaryVal.([]any); ok {
			for _, item := range list {
				parts = append(parts, fmt.Sprint(item))
			}
		}
		sha := runtimeSHA(runtimeRoot)
		if len(sha) > 12 {
			sha = sha[:12]
		}
		cwd := asString(func() any { v, _ := owner.Get("cwd"); return v }())
		session := asString(func() any { v, _ := owner.Get("session"); return v }())
		message := fmt.Sprintf("sum refresh coordinator: operating contract revision %s is requested (sum %s, runtime %s). Changes: %s. At your next safe point read %s, then run %s and continue coordination from saved state. Do not restart or re-dispatch.",
			latest, contract.SumVersion, sha, strings.Join(parts, "; "), path, shquote.CommandFor(sumctlPath, s.Home, "refresh", "adopt", "--coordinator", latest))
		record := occupantRecord{revision: latest, role: "coordinator", ok: true, current: func() (any, error) {
			current, err := s.Owner()
			if err != nil || current == nil {
				return nil, fmt.Errorf("no coordinator record")
			}
			value, _ := incarnation.CoordinatorRecord(current)
			return value, nil
		}}
		record.value, record.occupiedAt = incarnation.CoordinatorRecord(owner)
		event = attemptDelivery(s, sn, owner, cwd, message, host, record, nil)
		_ = session
	}
	unlock, err = s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	versionsObj, err = versions.ReadContractVersions(s)
	if err != nil {
		return nil, err
	}
	if asString(func() any { v, _ := versionsObj.Get("requested"); return v }()) == latest {
		recordDelivery(versionsObj, latest, event, runtimeRoot)
		if err := versions.WriteContractVersions(s, versionsObj); err != nil {
			return nil, err
		}
	}
	row.Set("revision", latest)
	row.Set("path", func() any { v, _ := state.Get("path"); return v }())
	row.Set("summary", func() any { v, _ := target.Get("summary"); return v }())
	for _, k := range event.Keys() {
		v, _ := event.Get(k)
		row.Set(k, v)
	}
	return row, nil
}

func Status(s *store.Store, taskIDs []string) (*ordjson.Object, error) {
	var rows []any
	coord := versions.ContractState(s)
	coord.Set("target", "coordinator")
	rows = append(rows, coord)
	tasks, err := s.AllTasks()
	if err != nil {
		return nil, err
	}
	filter := map[string]bool{}
	for _, id := range taskIDs {
		filter[id] = true
	}
	for _, task := range tasks {
		status, _ := task.Get("status")
		id := asString(func() any { v, _ := task.Get("id"); return v }())
		if status == "archived" || (len(filter) > 0 && !filter[id]) {
			continue
		}
		versionsObj, vErr := versions.ReadVersions(s, task)
		row := ordjson.NewObject()
		row.Set("target", "task")
		row.Set("task", id)
		row.Set("harness", func() any { v, _ := task.Get("harness"); return v }())
		if vErr != nil {
			row.Set("state", "pending-unreachable")
			row.Set("reason", "version sidecar unreadable: "+vErr.Error())
			row.Set("deferred", []any{})
		} else {
			state := versions.RefreshState(versionsObj)
			for _, k := range state.Keys() {
				v, _ := state.Get(k)
				row.Set(k, v)
			}
			row.Set("deferred", deferredCapabilities(asObject(func() any { v, _ := versionsObj.Get("runtime"); return v }())))
		}
		rows = append(rows, row)
	}
	result := refreshSummary(rows, []any{})
	rt := ordjson.NewObject()
	rt.Set("sum_version", contract.SumVersion)
	result.Set("runtime", rt)
	return result, nil
}

func AdoptCoordinator(s *store.Store, ctx *ordjson.Object, revision string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	versionsObj, err := versions.ReadContractVersions(s)
	if err != nil {
		return nil, err
	}
	requested, _ := versionsObj.Get("requested")
	if requested != revision {
		return nil, fmt.Errorf("%s is not the requested revision (%v). Adopt only what was requested.", revision, requested)
	}
	revisionsValue, _ := versionsObj.Get("revisions")
	list, _ := revisionsValue.([]any)
	var target *ordjson.Object
	for _, raw := range list {
		rev, _ := raw.(*ordjson.Object)
		id, _ := rev.Get("id")
		if id == revision {
			target = rev
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("Unknown revision %s.", revision)
	}
	for _, raw := range list {
		rev, _ := raw.(*ordjson.Object)
		if st, _ := rev.Get("status"); st == "active" {
			rev.Set("status", "superseded")
		}
	}
	target.Set("status", "active")
	versionsObj.Set("active", revision)
	versionsObj.Set("requested", nil)
	refreshValue, _ := versionsObj.Get("refresh")
	refresh, _ := refreshValue.([]any)
	row := ordjson.NewObject()
	row.Set("at", store.Now())
	row.Set("event", "adopted")
	row.Set("revision", revision)
	versionsObj.Set("refresh", append(refresh, row))
	if err := versions.WriteContractVersions(s, versionsObj); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("active", revision)
	result.Set("note", "Receipt recorded: this contract revision was read and adopted. A receipt is evidence of reading, not proof it is followed.")
	return result, nil
}

func Request(s *store.Store, ctx *ordjson.Object, taskIDs []string, coordinator bool, runtimeRoot, sumctlPath string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	sn := newSnapshots(runtimeRoot)
	everything := len(taskIDs) == 0 && !coordinator
	var rows []any
	var excluded []any
	if everything || coordinator {
		row, err := refreshCoordinator(s, ctx, sn, runtimeRoot, sumctlPath, host)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	tasks, err := s.AllTasks()
	if err != nil {
		return nil, err
	}
	filter := map[string]bool{}
	for _, id := range taskIDs {
		filter[id] = true
	}
	seen := map[string]bool{}
	for _, task := range tasks {
		status, _ := task.Get("status")
		id := asString(func() any { v, _ := task.Get("id"); return v }())
		if status == "archived" || (len(filter) > 0 && !filter[id]) {
			continue
		}
		seen[id] = true
		if !host.Is(asString(func() any { v, _ := task.Get("machine"); return v }())) {
			row := ordjson.NewObject()
			row.Set("target", "task")
			row.Set("task", id)
			row.Set("harness", func() any { v, _ := task.Get("harness"); return v }())
			row.Set("state", "pending-unreachable")
			row.Set("deferred", []any{})
			row.Set("reason", "task belongs to another machine; nothing was requested for it")
			rows = append(rows, row)
			continue
		}
		row, rErr := refreshTask(s, task, ctx, sn, runtimeRoot, sumctlPath, host)
		if rErr != nil {
			return nil, rErr
		}
		rows = append(rows, row)
	}
	for _, wanted := range taskIDs {
		if !seen[wanted] {
			return nil, fmt.Errorf("Unknown or archived task %s; nothing was requested for it.", wanted)
		}
	}
	if everything {
		regs, _ := s.Registrations()
		for _, reg := range regs {
			if asString(func() any { v, _ := reg.Get("role"); return v }()) == "developer" {
				row := ordjson.NewObject()
				row.Set("pane", func() any { v, _ := reg.Get("pane"); return v }())
				row.Set("session", func() any { v, _ := reg.Get("session"); return v }())
				row.Set("role", "developer")
				row.Set("reason", "developer sessions are outside production fan-out; a developer rereads its own checkout")
				excluded = append(excluded, row)
			}
		}
	}
	result := refreshSummary(rows, excluded)
	by := ordjson.NewObject()
	by.Set("session", func() any { v, _ := ctx.Get("session"); return v }())
	by.Set("pane", func() any { v, _ := ctx.Get("pane"); return v }())
	result.Set("requested_by", by)
	rt := ordjson.NewObject()
	rt.Set("sum_version", contract.SumVersion)
	rt.Set("sha", runtimeSHA(runtimeRoot))
	result.Set("runtime", rt)
	result.Set("fanout", fanout(sn))
	return result, nil
}

// ReconcileCoordinator delivers only an already-requested operating revision.
// It neither regenerates policy nor adopts it on the coordinator's behalf.
func ReconcileCoordinator(s *store.Store, scope store.Endpoint, runtimeRoot, sumctlPath string) (*ordjson.Object, error) {
	owner, err := s.Owner()
	if err != nil || owner == nil {
		return nil, err
	}
	endpoint := store.EndpointFromContext(owner)
	if endpoint.CoreID != scope.CoreID || endpoint.Session != scope.Session {
		return nil, nil
	}
	ledger, err := versions.ReadContractVersions(s)
	if err != nil {
		return nil, err
	}
	requested, _ := ledger.Get("requested")
	active, _ := ledger.Get("active")
	revision, _ := requested.(string)
	if revision == "" || requested == active {
		return nil, nil
	}
	var target *ordjson.Object
	revisions, _ := ledger.Get("revisions")
	for _, raw := range asList(revisions) {
		candidate := asObject(raw)
		if candidate != nil {
			if id, _ := candidate.Get("id"); id == revision {
				target = candidate
				break
			}
		}
	}
	if target == nil {
		return nil, fmt.Errorf("requested coordinator contract revision is missing")
	}
	view := versions.RevisionView(filepath.Join(s.Home, versions.ContractDir), target)
	if ok, _ := view.Get("ok"); ok != true {
		return nil, fmt.Errorf("requested coordinator contract revision is not readable")
	}
	path, _ := view.Get("path")
	message := fmt.Sprintf("sum refresh coordinator: operating contract revision %s is requested. Read %v completely, then run %s. Continue coordination from saved state; this notice is not adoption or approval.", revision, path, shquote.CommandFor(sumctlPath, s.Home, "refresh", "adopt", "--coordinator", revision))
	recorded, occupiedAt := incarnation.CoordinatorRecord(owner)
	record := occupantRecord{role: "coordinator", revision: revision, value: recorded, occupiedAt: occupiedAt, ok: true, current: func() (any, error) {
		current, err := s.Owner()
		if err != nil {
			return nil, err
		}
		value, _ := incarnation.CoordinatorRecord(current)
		return value, nil
	}}
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	event := attemptDelivery(s, newSnapshots(runtimeRoot), owner, endpoint.Cwd, message, host, record, nil)
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	ledger, err = versions.ReadContractVersions(s)
	if err != nil {
		return nil, err
	}
	if current, _ := ledger.Get("requested"); current == revision {
		recordDelivery(ledger, revision, event, runtimeRoot)
		err = versions.WriteContractVersions(s, ledger)
	}
	return event, err
}
