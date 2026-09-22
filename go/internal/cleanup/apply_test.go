package cleanup

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/archive"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// makeWorktreeCheckout turns the lab's plain directory into a real registered
// worktree of l.repo on the task branch and returns its HEAD sha, which doubles
// as the merged PR head (rev-list head..HEAD is then empty).
func (l *lab) makeWorktreeCheckout() string {
	l.t.Helper()
	if err := os.RemoveAll(l.checkout); err != nil {
		l.t.Fatal(err)
	}
	write := exec.Command("git", "-C", l.repo, "commit", "--allow-empty", "-m", "base")
	if out, err := write.CombinedOutput(); err != nil {
		l.t.Fatalf("git commit: %v\n%s", err, out)
	}
	add := exec.Command("git", "-C", l.repo, "worktree", "add", l.checkout, "-b", "sum/t-aaaaaaaaaaaa")
	if out, err := add.CombinedOutput(); err != nil {
		l.t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	head := exec.Command("git", "-C", l.checkout, "rev-parse", "HEAD")
	out, err := head.Output()
	if err != nil {
		l.t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// startDetachedSleep launches a sleep that is not a child of the test process,
// so the OS reaps it after the interrupt cleanup sends. This mirrors a real
// orphaned daemon (for example codegraph serve --mcp) left in the checkout.
func startDetachedSleep(t *testing.T, checkout string) int {
	t.Helper()
	cmd := exec.Command("sh", "-c", "sleep 120 </dev/null >/dev/null 2>&1 & echo $!")
	cmd.Dir = checkout
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("detached sleep pid: %v (%q)", err, out)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	})
	return pid
}

func (l *lab) mergedTaskJSON(headSHA, questions, workerState string) string {
	return l.mergedTaskExtra(headSHA, questions, workerState, "")
}

// mergedTaskExtra is mergedTaskJSON with extra top-level fields (for example a
// repairs ledger) spliced in before the execution record.
func (l *lab) mergedTaskExtra(headSHA, questions, workerState, extra string) string {
	return fmt.Sprintf(`{
"schema": 1, "id": %q, "status": "running", "repository": %q,
"machine": %q, "session": "sum-test", "pane": "w-worker:p1", "workspace": "w-worker",
"worktree": %q, "branch": "sum/t-aaaaaaaaaaaa",
"questions": %s,
"evidence": [{"kind": "handoff", "source": "worker", "candidate": %q, "at": "2026-01-01T00:00:00+00:00"}],
"report": {"text": "done", "candidate": %q}, "notice": null, "attention": [],
"brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship"%s,
"pr": {"complete": true, "merged_for_task": true, "state": "merged", "observed_at": "2026-01-02T00:00:00+00:00",
       "identity": {"number": 7, "url": "https://github.com/douglasjarquin/project/pull/7",
                    "head_sha": %q, "head_branch": "sum/t-aaaaaaaaaaaa", "base_branch": "main"},
       "merge_commit": %q, "findings": []},
"execution": {"schema": 1,
  "worker": {"id": %q, "kind": "worker", "state": %q, "generation": 1,
             "owner": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1"}, "checkout": %q,
             "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": [],
             "occupant": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1", "checkout": %q, "harness": "codex", "name": "done", "shell_pid": 4242, "pid": null, "argv": null}},
  "verifiers": []}
}`, taskID, l.repo, l.host, l.checkout, questions, headSHA, headSHA, extra, headSHA, headSHA, workerID, workerState, l.host, l.checkout, l.host, l.checkout)
}

func questionStatus(t *testing.T, s *store.Store, id string) string {
	t.Helper()
	task, err := s.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := task.Get("questions")
	for _, q := range raw.([]any) {
		row, _ := q.(*ordjson.Object)
		qid, _ := row.Get("id")
		if qid == id {
			status, _ := row.Get("status")
			v, _ := status.(string)
			return v
		}
	}
	return ""
}

// A merged task whose pane and workspace are already gone is the normal end
// state: cleanup adopts the verified clean checkout and removes it with git,
// without force, keeping the branch.
func TestCleanup_applyAdoptsClosedPaneCheckout(t *testing.T) {
	l := newLab(t)
	head := l.makeWorktreeCheckout()
	l.plantLsof(nil)
	l.saveTask(l.mergedTaskJSON(head, `[]`, "released"))
	result, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true})
	if err != nil {
		t.Fatalf("cleanup --apply: %v", err)
	}
	if state, _ := result.Get("state"); state != "complete" {
		t.Fatalf("cleanup state = %v, want complete", state)
	}
	if _, err := os.Stat(l.checkout); !os.IsNotExist(err) {
		t.Fatalf("checkout still exists: %v", err)
	}
	branch := exec.Command("git", "-C", l.repo, "show-ref", "--verify", "--quiet", "refs/heads/sum/t-aaaaaaaaaaaa")
	if branch.Run() != nil {
		t.Fatal("task branch was deleted; sum never deletes branches")
	}
	task, err := l.store.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	status, _ := task.Get("status")
	if status != "archived" {
		t.Fatalf("task status = %v, want archived", status)
	}
}

// An orphaned process left in the checkout (a daemon outliving its pane) is a
// named blocker at inspection, and apply interrupts it once, verifies exit,
// re-inspects, and completes.
func TestCleanup_applyStopsOrphanThenAdopts(t *testing.T) {
	l := newLab(t)
	head := l.makeWorktreeCheckout()
	orphan := startDetachedSleep(t, l.checkout)
	l.plantLsof([]map[string]any{{"pid": orphan, "cwd": l.checkout, "alive": true}})
	l.saveTask(l.mergedTaskJSON(head, `[]`, "running"))

	plan := l.inspect()
	if !hasCode(blockerCodes(plan), "occupant") && !hasCode(blockerCodes(plan), "execution") {
		t.Fatalf("inspect blockers = %v, want the live orphan named", blockerCodes(plan))
	}
	orphans := asList(func() any { v, _ := plan.Get("orphans"); return v }())
	if len(orphans) != 1 {
		t.Fatalf("plan orphans = %v, want the dead-owner process listed", orphans)
	}

	result, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true})
	if err != nil {
		t.Fatalf("cleanup --apply: %v", err)
	}
	if state, _ := result.Get("state"); state != "complete" {
		t.Fatalf("cleanup state = %v, want complete", state)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(orphan, 0); err != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(orphan, 0); err == nil {
		t.Fatalf("orphan pid %d still alive after apply", orphan)
	}
	if _, err := os.Stat(l.checkout); !os.IsNotExist(err) {
		t.Fatalf("checkout still exists: %v", err)
	}
}

// writeWorkspaceWithoutPane plants a Herdr world where the task workspace still
// exists — workspaces persist until cleanup removes them — but the recorded
// worker pane is already gone.
func (l *lab) writeWorkspaceWithoutPane() {
	l.t.Helper()
	state := map[string]any{
		"panes": map[string]any{
			"w-parent:p1": map[string]any{
				"pane_id": "w-parent:p1", "cwd": l.home, "workspace_id": "w-parent",
				"agent_status": "idle", "agent": "claude",
			},
		},
		"workspaces": map[string]any{
			"w-parent": map[string]any{"workspace_id": "w-parent", "label": "coordinator", "worktree": nil},
			"w-worker": map[string]any{
				"workspace_id": "w-worker", "label": "task",
				"worktree": map[string]any{"checkout_path": l.checkout, "repo_root": l.repo},
			},
		},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.herdr, "state.json"), raw, 0o600); err != nil {
		l.t.Fatal(err)
	}
}

// startArgvBoundDaemon launches a detached long-lived process whose argv names
// a path inside the checkout (the `serve --mcp --path <checkout>` binding a
// codegraph MCP daemon carries) but whose cwd is elsewhere, so only argv binds
// it. Detaching mirrors the real daemon: it reparents to PID 1 and the OS reaps
// it after the cleanup signal.
func startArgvBoundDaemon(t *testing.T, checkout, dir string) int {
	t.Helper()
	target := filepath.Join(checkout, ".git")
	cmd := exec.Command("sh", "-c", fmt.Sprintf("tail -f '%s' </dev/null >/dev/null 2>&1 & echo $!", target))
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("argv-bound daemon pid: %v (%q)", err, out)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	})
	return pid
}

// startRecordedParent launches a live process that keeps a child running, and
// returns both pids: the parent is recorded as the attempt occupant, the child
// is planted as checkout-bound to prove descendants of a live recorded pid are
// named blockers, not orphans.
func startRecordedParent(t *testing.T) (parent, child int) {
	t.Helper()
	cmd := exec.Command("sh", "-c", "sleep 120 </dev/null >/dev/null 2>&1 & echo $!; exec sleep 120")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fscanf(bufio.NewReader(stdout), "%d\n", &child); err != nil {
		t.Fatalf("child pid: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid, child
}

// A repair-allowance question that is answered, granted, and then settled by
// worker-death cleanup must keep satisfying its grant: apply settles the
// question mid-flight, and every post-settle ledger check (the intent save
// inside apply itself, then park and archive) still accepts the recorded
// decision.
func TestCleanup_applySettlesGrantedQuestionThenArchives(t *testing.T) {
	l := newLab(t)
	head := l.makeWorktreeCheckout()
	l.plantLsof(nil)
	questions := `[{"id": "q-aaaaaaaaaa", "status": "answered", "text": "extend the allowance?",
		"answer": "add one", "decision": {"kind": "repair-allowance", "allowance": 2}}]`
	repairs := fmt.Sprintf(`, "repairs": {"schema": 1, "default_allowance": 2, "consumed": 0, "operations": [],
		"grants": [{"question": "q-aaaaaaaaaa", "additional": 1, "text": "add one",
		"at": "2026-01-01T00:00:00+00:00", "by": {"machine": %q, "session": "sum-test", "pane": "w-parent:p1"}}]}`, l.host)
	l.saveTask(l.mergedTaskExtra(head, questions, "running", repairs))

	result, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true})
	if err != nil {
		t.Fatalf("cleanup --apply refused after settling the granted question: %v", err)
	}
	if state, _ := result.Get("state"); state != "complete" {
		t.Fatalf("cleanup state = %v, want complete", state)
	}
	if got := questionStatus(t, l.store, "q-aaaaaaaaaa"); got != "settled" {
		t.Fatalf("question status = %q, want settled", got)
	}
	task, err := l.store.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repair.Ledger(task); err != nil {
		t.Fatalf("settled question broke the recorded grant: %v", err)
	}
	if _, err := archive.Run(l.store, taskID, true); err != nil {
		t.Fatalf("archive refused the settled grant task: %v", err)
	}
}

// The pane is dead but its Herdr workspace still exists — workspaces persist
// until cleanup removes them, which is exactly the state the wedged codegraph
// daemons left behind. A checkout-bound process is still a stoppable orphan:
// apply sends one SIGTERM, verifies exit, and removes the workspace.
func TestCleanup_applyStopsOrphanWhileWorkspacePresent(t *testing.T) {
	l := newLab(t)
	head := l.makeWorktreeCheckout()
	l.writeWorkspaceWithoutPane()
	orphan := startDetachedSleep(t, l.checkout)
	l.plantLsof([]map[string]any{{"pid": orphan, "cwd": l.checkout, "alive": true}})
	l.saveTask(l.mergedTaskJSON(head, `[]`, "running"))

	plan := l.inspect()
	orphans := asList(func() any { v, _ := plan.Get("orphans"); return v }())
	if len(orphans) != 1 {
		t.Fatalf("plan orphans = %v, want the bound daemon listed; blockers=%v", orphans, blockerDetails(plan))
	}
	resources := asObject(func() any { v, _ := plan.Get("resources"); return v }())
	if ws := asString(func() any { v, _ := resources.Get("workspace"); return v }()); ws != "present" {
		t.Fatalf("workspace resource = %v, want present", ws)
	}

	result, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true})
	if err != nil {
		t.Fatalf("cleanup --apply: %v", err)
	}
	if state, _ := result.Get("state"); state != "complete" {
		t.Fatalf("cleanup state = %v, want complete", state)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(orphan, 0); err != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(orphan, 0); err == nil {
		t.Fatalf("orphan pid %d still alive after apply", orphan)
	}
	if _, err := os.Stat(l.checkout); !os.IsNotExist(err) {
		t.Fatalf("checkout still exists: %v", err)
	}
	task, err := l.store.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := task.Get("status"); status != "archived" {
		t.Fatalf("task status = %v, want archived", status)
	}
}

// A daemon bound to the checkout only through its argv (`serve --mcp --path
// <checkout>` while its cwd lies elsewhere) is still provably bound: it is a
// named orphan once the pane is proven absent and apply stops it.
func TestCleanup_applyStopsArgvBoundDaemon(t *testing.T) {
	l := newLab(t)
	head := l.makeWorktreeCheckout()
	daemon := startArgvBoundDaemon(t, l.checkout, l.home)
	l.plantLsof(nil)
	l.saveTask(l.mergedTaskJSON(head, `[]`, "running"))

	plan := l.inspect()
	orphans := asList(func() any { v, _ := plan.Get("orphans"); return v }())
	if len(orphans) != 1 {
		t.Fatalf("plan orphans = %v, want the argv-bound daemon listed; blockers=%v", orphans, blockerDetails(plan))
	}
	row := asObject(orphans[0])
	if bound := asString(func() any { v, _ := row.Get("bound"); return v }()); bound != "argv" {
		t.Fatalf("orphan binding = %q, want argv", bound)
	}

	result, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true})
	if err != nil {
		t.Fatalf("cleanup --apply: %v", err)
	}
	if state, _ := result.Get("state"); state != "complete" {
		t.Fatalf("cleanup state = %v, want complete", state)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(daemon, 0); err != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(daemon, 0); err == nil {
		t.Fatalf("argv-bound daemon pid %d still alive after apply", daemon)
	}
	if _, err := os.Stat(l.checkout); !os.IsNotExist(err) {
		t.Fatalf("checkout still exists: %v", err)
	}
}

// A checkout-bound process that is still a descendant of a live recorded pane
// or shell pid keeps a recorded owner: it stays a named blocker, never an
// orphan, and apply refuses to stop it.
func TestCleanup_boundDescendantOfRecordedPidStaysBlocker(t *testing.T) {
	l := newLab(t)
	head := l.makeWorktreeCheckout()
	parent, child := startRecordedParent(t)
	l.plantLsof([]map[string]any{{"pid": child, "cwd": l.checkout, "alive": true}})

	value, err := ordjson.Decode([]byte(l.mergedTaskJSON(head, `[]`, "running")))
	if err != nil {
		t.Fatal(err)
	}
	task, _ := value.(*ordjson.Object)
	execObj := asObject(func() any { v, _ := task.Get("execution"); return v }())
	worker := asObject(func() any { v, _ := execObj.Get("worker"); return v }())
	occupant := asObject(func() any { v, _ := worker.Get("occupant"); return v }())
	occupant.Set("pid", json.Number(fmt.Sprint(parent)))
	raw, err := ordjson.MarshalCompact(task)
	if err != nil {
		t.Fatal(err)
	}
	l.saveTask(string(raw))

	plan := l.inspect()
	if orphans := asList(func() any { v, _ := plan.Get("orphans"); return v }()); len(orphans) != 0 {
		t.Fatalf("descendant of a live recorded pid was named an orphan: %v", orphans)
	}
	if !hasCode(blockerCodes(plan), "occupant") && !hasCode(blockerCodes(plan), "execution") {
		t.Fatalf("descendant was not named as a blocker: %v", blockerCodes(plan))
	}
	if _, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true}); err == nil {
		t.Fatal("apply stopped or ignored a descendant of a live recorded pid")
	}
	if err := syscall.Kill(child, 0); err != nil {
		t.Fatalf("descendant pid %d was terminated", child)
	}
}

// An answered question whose worker is verifiably dead settles during apply;
// archive then accepts it like an applied one.
func TestCleanup_applySettlesAnsweredQuestionAfterVerifiedWorkerDeath(t *testing.T) {
	l := newLab(t)
	head := l.makeWorktreeCheckout()
	l.plantLsof(nil)
	questions := `[{"id": "q-aaaaaaaaaa", "status": "answered", "text": "keep it?", "answer": {"text": "yes"}}]`
	l.saveTask(l.mergedTaskJSON(head, questions, "running"))

	plan := l.inspect()
	settleable := asList(func() any { v, _ := plan.Get("settled_questions"); return v }())
	if len(settleable) != 1 || settleable[0] != "q-aaaaaaaaaa" {
		t.Fatalf("settled_questions = %v blockers=%v", settleable, blockerDetails(plan))
	}
	if hasCode(blockerCodes(plan), "obligations") {
		t.Fatalf("answered question with dead worker still blocks: %v", blockerDetails(plan))
	}

	result, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true})
	if err != nil {
		t.Fatalf("cleanup --apply: %v", err)
	}
	if state, _ := result.Get("state"); state != "complete" {
		t.Fatalf("cleanup state = %v", state)
	}
	if got := questionStatus(t, l.store, "q-aaaaaaaaaa"); got != "settled" {
		t.Fatalf("question status = %q, want settled", got)
	}
}

// An answered question while the worker is still live stays a blocker and is
// never settled.
func TestCleanup_answeredQuestionBlocksWhileWorkerAlive(t *testing.T) {
	l := newLab(t)
	head := l.makeWorktreeCheckout()
	l.writeWorkerPane()
	// Give the recorded worker pane a live agent so the attempt is genuinely
	// running; an agent-less pane is already conclusive death.
	raw, err := os.ReadFile(filepath.Join(l.herdr, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	panes := state["panes"].(map[string]any)
	worker := panes["w-worker:p1"].(map[string]any)
	worker["agent"] = "claude"
	worker["agent_status"] = "running"
	out, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.herdr, "state.json"), out, 0o600); err != nil {
		t.Fatal(err)
	}
	l.plantLsof(nil)
	questions := `[{"id": "q-aaaaaaaaaa", "status": "answered", "text": "keep it?", "answer": {"text": "yes"}}]`
	l.saveTask(l.mergedTaskJSON(head, questions, "running"))
	plan := l.inspect()
	if !hasCode(blockerCodes(plan), "obligations") {
		t.Fatalf("live worker's answered question must block: %v", blockerDetails(plan))
	}
	if got := questionStatus(t, l.store, "q-aaaaaaaaaa"); got != "answered" {
		t.Fatalf("question status = %q, want still answered", got)
	}
}

// A saved cleanup intent reconciles instead of wedging: apply continues when it
// still matches the fresh plan.
func TestCleanup_applyContinuesMatchingSavedIntent(t *testing.T) {
	l := newLab(t)
	head := l.makeWorktreeCheckout()
	l.plantLsof(nil)
	l.saveTask(l.mergedTaskJSON(head, `[]`, "released"))
	task, err := l.store.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := inspectTask(l.store, task, l.ctx, l.runtime, 0, "task")
	if err != nil {
		t.Fatal(err)
	}
	intent := ordjson.NewObject()
	intent.Set("workspace", "w-worker")
	intent.Set("pane", "w-worker:p1")
	intent.Set("worktree", l.checkout)
	intent.Set("branch", "sum/t-aaaaaaaaaaaa")
	intent.Set("repository", l.repo)
	intent.Set("head", head)
	intent.Set("merged_head", head)
	intent.Set("merge_commit", head)
	intent.Set("pr", 7)
	intent.Set("at", "2026-01-02T00:00:00+00:00")
	execRow := asObject(func() any { v, _ := plan.Get("execution"); return v }())
	attempts, _ := execRow.Get("attempts")
	if err := saveCleanupIntent(l.store, taskID, intent, attempts, asObject(func() any { v, _ := plan.Get("resources"); return v }())); err != nil {
		t.Fatal(err)
	}
	result, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true})
	if err != nil {
		t.Fatalf("cleanup --apply with saved intent: %v", err)
	}
	if state, _ := result.Get("state"); state != "complete" {
		t.Fatalf("cleanup state = %v, want complete", state)
	}
	if kind, _ := result.Get("intent"); kind != "continued" {
		t.Fatalf("intent = %v, want continued", kind)
	}
}

// A stale saved intent that no longer matches the fresh plan is re-derived
// instead of wedging the task or driving removal from old data.
func TestCleanup_applyRederivesStaleIntent(t *testing.T) {
	l := newLab(t)
	head := l.makeWorktreeCheckout()
	l.plantLsof(nil)
	l.saveTask(l.mergedTaskJSON(head, `[]`, "released"))
	intent := ordjson.NewObject()
	intent.Set("workspace", "w-stale")
	intent.Set("pane", "w-stale:p9")
	intent.Set("worktree", "/definitely/not/the/checkout")
	intent.Set("branch", "sum/other")
	intent.Set("repository", l.repo)
	intent.Set("head", "9999999999999999999999999999999999999999")
	intent.Set("merged_head", "9999999999999999999999999999999999999999")
	intent.Set("merge_commit", "9999999999999999999999999999999999999999")
	intent.Set("pr", 99)
	intent.Set("at", "2026-01-02T00:00:00+00:00")
	if err := saveCleanupIntent(l.store, taskID, intent, []any{}, ordjson.NewObject()); err != nil {
		t.Fatal(err)
	}
	result, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true})
	if err != nil {
		t.Fatalf("cleanup --apply with stale intent: %v", err)
	}
	if state, _ := result.Get("state"); state != "complete" {
		t.Fatalf("cleanup state = %v, want complete", state)
	}
	if kind, _ := result.Get("intent"); kind != "re-derived" {
		t.Fatalf("intent = %v, want re-derived", kind)
	}
	if _, err := os.Stat("/definitely/not/the/checkout"); !os.IsNotExist(err) {
		t.Fatal("stale intent path was touched")
	}
}

func TestIntentMatches(t *testing.T) {
	task := ordjson.NewObject()
	task.Set("workspace", "w-worker")
	task.Set("pane", "w-worker:p1")
	task.Set("worktree", "/tmp/checkout")
	task.Set("branch", "sum/t-x")
	plan := ordjson.NewObject()
	plan.Set("head", "aaaa")
	pr := ordjson.NewObject()
	pr.Set("number", 7)
	pr.Set("merge_commit", "bbbb")
	plan.Set("pr", pr)
	attempts := []any{func() any {
		row := ordjson.NewObject()
		row.Set("id", "x-1")
		row.Set("generation", 1)
		row.Set("outcome", "stopped")
		return row
	}()}

	saved := ordjson.NewObject()
	saved.Set("workspace", "w-worker")
	saved.Set("pane", "w-worker:p1")
	saved.Set("worktree", "/tmp/checkout")
	saved.Set("branch", "sum/t-x")
	saved.Set("head", "aaaa")
	saved.Set("merged_head", "cccc")
	saved.Set("merge_commit", "bbbb")
	saved.Set("pr", 7)
	saved.Set("attempts", attempts)

	if !intentMatches(saved, task, plan, "cccc", attempts) {
		t.Fatal("identical intent rejected")
	}
	saved.Set("pane", "w-other:p2")
	if intentMatches(saved, task, plan, "cccc", attempts) {
		t.Fatal("intent for another pane accepted")
	}
	saved.Set("pane", "w-worker:p1")
	saved.Set("merged_head", "dddd")
	if intentMatches(saved, task, plan, "cccc", attempts) {
		t.Fatal("intent with different merge evidence accepted")
	}
	saved.Set("merged_head", "cccc")
	saved.Set("attempts", []any{})
	if intentMatches(saved, task, plan, "cccc", attempts) {
		t.Fatal("intent with different attempts accepted")
	}
}
