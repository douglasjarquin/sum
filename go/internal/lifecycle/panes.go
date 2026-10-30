package lifecycle

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"encoding/json"
	"errors"
	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/bindcmd"
	"github.com/douglasjarquin/sum/go/internal/cleanup"
	"github.com/douglasjarquin/sum/go/internal/evidenceview"
	"github.com/douglasjarquin/sum/go/internal/execution"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/panes"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// hasRemovableTerminal is the records-only check sweep uses to queue a task: a worker
// or reviewer pane whose obligation is settled and not yet recorded closed.
func hasRemovableTerminal(task *ordjson.Object) bool {
	if reason, _ := workerCloseReason(task); reason != "" && !panes.WorkerIsClosed(task) {
		return true
	}
	if reason, _ := reviewerCloseReason(task); reason != "" && !panes.ReviewerIsClosed(task) {
		return true
	}
	return false
}

func terminalState(task *ordjson.Object) string {
	if asString(task, "status") == "archived" {
		return "archived"
	}
	pr := asObject(field(task, "pr"))
	if pr == nil {
		return ""
	}
	if merged, _ := pr.Get("merged_for_task"); merged == true {
		return "merged"
	}
	if state, _ := pr.Get("state"); state == "closed" {
		return "pr-closed"
	}
	return ""
}

// currentSHA is records-only so Pending and state-locked claims never run Git.
func currentSHA(task *ordjson.Object) string {
	var candidate string
	for _, raw := range asList(field(task, "evidence")) {
		row := asObject(raw)
		kind := asString(row, "kind")
		if (kind == "handoff" || kind == "report" || kind == "review") && asString(row, "candidate") != "" {
			candidate = asString(row, "candidate")
		}
	}
	if candidate == "" {
		candidate = asString(asObject(field(task, "report")), "candidate")
	}
	return candidate
}

func currentCandidateMatch(candidate, head string) bool {
	return candidate != "" && head != "" && candidate == head
}

func reportMatchesCurrent(task *ordjson.Object) bool {
	head := currentSHA(task)
	if currentCandidateMatch(asString(asObject(field(task, "report")), "candidate"), head) {
		return true
	}
	for _, raw := range asList(field(task, "evidence")) {
		row := asObject(raw)
		if asString(row, "kind") != "report" {
			continue
		}
		if currentCandidateMatch(asString(row, "candidate"), head) {
			return true
		}
	}
	return false
}

func reviewMatchesCurrent(task *ordjson.Object) bool {
	head := currentSHA(task)
	for _, raw := range asList(field(task, "evidence")) {
		row := asObject(raw)
		if asString(row, "kind") != "review" || asString(row, "verdict") == "" {
			continue
		}
		if currentCandidateMatch(asString(row, "candidate"), head) {
			return true
		}
	}
	return false
}

func evidenceAddress(row *ordjson.Object) string {
	return asString(asObject(field(row, "endpoint")), "address")
}

func currentReportAddress(task *ordjson.Object) string {
	head := currentSHA(task)
	var reportPane, handoffPane string
	for _, raw := range asList(field(task, "evidence")) {
		row := asObject(raw)
		kind := asString(row, "kind")
		if kind != "report" && kind != "handoff" {
			continue
		}
		if !currentCandidateMatch(asString(row, "candidate"), head) {
			continue
		}
		if !store.EndpointFromContext(asObject(field(row, "endpoint"))).Native().SameAuthority(roleEndpoint(task, "worker").Native()) {
			continue
		}
		if pane := evidenceAddress(row); pane != "" {
			if kind == "report" {
				reportPane = pane
			} else {
				handoffPane = pane
			}
		}
	}
	if reportPane != "" {
		return reportPane
	}
	return handoffPane
}

func currentReviewAddress(task *ordjson.Object) string {
	head := currentSHA(task)
	var pane string
	for _, raw := range asList(field(task, "evidence")) {
		row := asObject(raw)
		if asString(row, "kind") != "review" || asString(row, "verdict") == "" {
			continue
		}
		if !currentCandidateMatch(asString(row, "candidate"), head) {
			continue
		}
		if !store.EndpointFromContext(asObject(field(row, "endpoint"))).Native().SameAuthority(roleEndpoint(task, "reviewer").Native()) {
			continue
		}
		if p := evidenceAddress(row); p != "" {
			pane = p
		}
	}
	return pane
}

func workerCloseReason(task *ordjson.Object) (string, string) {
	current := roleEndpoint(task, "worker").Address
	if current == "" || !reportMatchesCurrent(task) || currentReportAddress(task) != current {
		return "", ""
	}
	if reason := terminalState(task); reason != "" {
		return reason, current
	}
	return "report-submitted", current
}
func reviewerCloseReason(task *ordjson.Object) (string, string) {
	current := roleEndpoint(task, "reviewer").Address
	if current == "" || !reviewMatchesCurrent(task) || currentReviewAddress(task) != current {
		return "", ""
	}
	if reason := terminalState(task); reason != "" {
		return reason, current
	}
	return "review-recorded", current
}

func roleEndpoint(task *ordjson.Object, role string) store.Endpoint {
	key := "worker_endpoint"
	if role == "reviewer" {
		key = "reviewer"
	}
	return store.EndpointFromContext(asObject(field(task, key)))
}

// Only explicit maintenance calls this function. Durable evidence is retained
// before a finished native history is removed; no status value proves exit.
func removeSettledTerminals(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID string) []any {
	task, err := s.ReadTask(taskID)
	if err != nil {
		return []any{failureRow(taskID, err)}
	}
	var rows []any
	for _, role := range []string{"reviewer", "worker"} {
		var reason, address string
		if role == "worker" {
			reason, address = workerCloseReason(task)
		} else {
			reason, address = reviewerCloseReason(task)
		}
		endpoint := roleEndpoint(task, role)
		if reason == "" || panes.ClosedEndpoint(task, endpoint) {
			continue
		}
		row := removeOneTerminal(s, ctx, runtimeRoot, taskID, address, role, reason, endpoint.Cwd)
		rows = append(rows, row)
	}
	return rows
}
func failureRow(id string, err error) *ordjson.Object {
	row := ordjson.NewObject()
	row.Set("task", id)
	row.Set("action", "terminal-remove")
	row.Set("state", "error")
	row.Set("error", err.Error())
	return row
}
func removeOneTerminal(s *store.Store, caller *ordjson.Object, runtimeRoot, taskID, address, role, reason, expectedCwd string) *ordjson.Object {
	row := ordjson.NewObject()
	row.Set("task", taskID)
	row.Set("action", "terminal-remove")
	row.Set("address", address)
	row.Set("role", role)
	row.Set("reason", reason)
	fail := func(err error) *ordjson.Object {
		row.Set("state", "skipped")
		row.Set("error", err.Error())
		return row
	}
	if err := app.RequireCoordinator(s, caller); err != nil {
		return fail(err)
	}
	maintenanceUnlock, err := cleanup.MaintenanceLock(s, taskID)
	if err != nil {
		return fail(err)
	}
	defer maintenanceUnlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return fail(err)
	}
	if record := asObject(field(task, "cleanup")); record != nil {
		if intent := field(record, "intent"); intent != nil && intent != false {
			return fail(fmt.Errorf("full cleanup intent is active"))
		}
	}

	endpoint := roleEndpoint(task, role)
	if err = endpoint.Validate(); err != nil {
		return fail(err)
	}
	if endpoint.Address != address {
		return fail(fmt.Errorf("native endpoint changed"))
	}
	for _, other := range []store.Endpoint{store.EndpointFromContext(caller), store.EndpointFromContext(asObject(field(task, "parent")))} {
		if endpoint.Native().SameTerminal(other.Native()) {
			return fail(fmt.Errorf("refusing to remove coordinator or parent terminal"))
		}
	}
	if err = cleanup.RefusePendingEffects(s, taskID); err != nil {
		return fail(err)
	}
	obligations, err := returns.OpenObligations(s, task)
	if err != nil {
		return fail(err)
	}
	for _, obligation := range obligations {
		kind := asString(obligation, "kind")
		if kind != "report" && kind != "review" {
			return fail(fmt.Errorf("pending %s obligation retains terminal", kind))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID}
	terminal, err := client.Get(ctx, endpoint.Address)
	absent := nativeGone(err)
	if err != nil && !absent {
		return fail(err)
	}
	if !absent && (terminal.CreatedMS != endpoint.CreatedMS || terminal.Keep || terminal.State != "finished" || terminal.FinishedMS == nil || (terminal.ExitCode == nil && terminal.Signal == nil) || resolvePath(terminal.Cwd) != resolvePath(expectedCwd)) {
		return fail(fmt.Errorf("native terminal is live, replaced, kept, or lacks finished exit evidence"))
	}
	// Git observation stays outside the ledger lock; Pending and claim checks use saved candidates only.
	if terminalState(task) != "archived" {
		head := evidenceview.CurrentCandidate(task)
		if head == "" || head != currentSHA(task) {
			return fail(fmt.Errorf("saved candidate differs from current checkout HEAD"))
		}
	}
	var stoppedProof *ordjson.Object
	if role == "worker" {
		independent := execution.NativeProgramStopped(s, endpoint)
		if independent.Outcome != execution.OutcomeStopped {
			return fail(fmt.Errorf("%s", independent.Reason))
		}
		worker, err := reservations.Worker(task)
		if err != nil {
			return fail(err)
		}
		proof := execution.ObserveStop(s, runtimeRoot, task, worker)
		if proof.Outcome != execution.OutcomeStopped {
			return fail(fmt.Errorf("%s", proof.Reason))
		}
		stoppedProof = proof.Observation

	} else {
		if _, err = bindcmd.ReviewerNativeStopped(ctx, s, task, asObject(field(task, "reviewer"))); err != nil {
			return fail(err)
		}
	}
	// Commit an exact effect claim under the state lock, then release it
	// before HC I/O. Task writers refuse admission while this claim exists.
	if err = claimTerminalMaintenance(s, task, endpoint, role, reason, stoppedProof); err != nil {
		return fail(err)
	}
	if err = s.CheckWritable(); err != nil {
		return fail(err)
	}
	if !absent {
		if _, err = client.Remove(ctx, endpoint.Address); err != nil {
			_ = finishTerminalMaintenance(s, taskID, endpoint, reason, false)
			return fail(err)
		}
	}
	if err = finishTerminalMaintenance(s, taskID, endpoint, reason, true); err != nil {
		return fail(err)
	}
	row.Set("state", "closed")
	if absent {
		row.Set("state", "already-absent")
	}
	return row
}
func nativeGone(err error) bool {
	var rpc *hcclient.RPCError
	if !errors.As(err, &rpc) {
		return false
	}
	var d struct {
		Kind string `json:"kind"`
	}
	return json.Unmarshal(rpc.Data, &d) == nil && d.Kind == "not_found"
}
func resolvePath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func claimTerminalMaintenance(s *store.Store, observed *ordjson.Object, endpoint store.Endpoint, role, reason string, stoppedProof *ordjson.Object) error {
	taskID := asString(observed, "id")
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	fresh, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	if !endpoint.Native().SameAuthority(roleEndpoint(fresh, role).Native()) {
		return fmt.Errorf("native binding changed before removal")
	}
	var currentReason string
	if role == "worker" {
		currentReason, _ = workerCloseReason(fresh)
	} else {
		currentReason, _ = reviewerCloseReason(fresh)
	}
	if currentReason != reason {
		return fmt.Errorf("candidate evidence changed before removal")
	}
	if err = cleanup.RefusePendingEffects(s, taskID); err != nil {
		return err
	}
	obligations, err := returns.OpenObligations(s, fresh)
	if err != nil {
		return err
	}
	for _, obligation := range obligations {
		kind := asString(obligation, "kind")
		if kind != "report" && kind != "review" {
			return fmt.Errorf("new %s obligation retains terminal", kind)
		}
	}
	if prior := asObject(field(fresh, "terminal_maintenance")); prior != nil {
		old := store.EndpointFromContext(asObject(field(prior, "endpoint")))
		if !old.Native().SameAuthority(endpoint.Native()) || asString(prior, "candidate") != currentSHA(fresh) || asString(prior, "reason") != reason {
			return fmt.Errorf("another terminal maintenance claim remains unresolved")
		}
	}
	claim := ordjson.NewObject()
	claim.Set("state", "claimed")
	claim.Set("endpoint", endpoint.Object())
	claim.Set("candidate", currentSHA(fresh))
	claim.Set("role", role)
	claim.Set("reason", reason)
	claim.Set("at", store.Now())
	if role == "worker" {
		before, err := reservations.Worker(observed)
		if err != nil {
			return err
		}
		now, err := reservations.Worker(fresh)
		if err != nil {
			return err
		}
		if asString(before, "id") != asString(now, "id") || fmt.Sprint(field(before, "generation")) != fmt.Sprint(field(now, "generation")) {
			return fmt.Errorf("worker attempt changed before terminal maintenance")
		}
		claim.Set("attempt", field(now, "id"))
		claim.Set("attempt_generation", field(now, "generation"))
		claim.Set("stop_proof", stoppedProof)
	}

	fresh.Set("terminal_maintenance", claim)
	return s.SaveTask(fresh)
}
func finishTerminalMaintenance(s *store.Store, taskID string, endpoint store.Endpoint, reason string, success bool) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	fresh, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	claim := asObject(field(fresh, "terminal_maintenance"))
	if !store.EndpointFromContext(asObject(field(claim, "endpoint"))).Native().SameAuthority(endpoint.Native()) {
		return fmt.Errorf("terminal maintenance claim changed during native operation")
	}
	if !success {
		claim.Set("state", "uncertain")
		fresh.Set("terminal_maintenance", claim)
	} else {
		if endpoint.Role == "worker" {
			worker, err := reservations.Worker(fresh)
			if err != nil {
				return err
			}
			if asString(worker, "id") != asString(claim, "attempt") || fmt.Sprint(field(worker, "generation")) != fmt.Sprint(field(claim, "attempt_generation")) {
				return fmt.Errorf("worker attempt changed during native terminal removal")
			}
			if asString(worker, "state") != "released" {
				generationNumber, ok := field(worker, "generation").(json.Number)
				if !ok {
					return fmt.Errorf("worker generation missing")
				}
				generation, err := generationNumber.Int64()
				if err != nil {
					return err
				}
				proof := asObject(field(claim, "stop_proof"))
				if proof == nil {
					proof = ordjson.NewObject()
					proof.Set("at", store.Now())
					proof.Set("outcome", execution.OutcomeStopped)
				}
				if _, err = reservations.Transition(fresh, asString(worker, "id"), "released", store.Now(), proof, &generation); err != nil {
					return err
				}
			}
		}
		panes.AppendEndpoint(fresh, endpoint, reason)
		fresh.Set("terminal_maintenance", nil)
	}
	return s.SaveTask(fresh)
}
