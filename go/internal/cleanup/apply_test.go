package cleanup

import (
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

	"github.com/douglasjarquin/sum/go/internal/ordjson"
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
	return fmt.Sprintf(`{
"schema": 1, "id": %q, "status": "running", "repository": %q,
"machine": %q, "session": "sum-test", "pane": "w-worker:p1", "workspace": "w-worker",
"worktree": %q, "branch": "sum/t-aaaaaaaaaaaa",
"questions": %s,
"evidence": [{"kind": "handoff", "source": "worker", "candidate": %q, "at": "2026-01-01T00:00:00+00:00"}],
"report": {"text": "done", "candidate": %q}, "notice": null, "attention": [],
"brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship",
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
}`, taskID, l.repo, l.host, l.checkout, questions, headSHA, headSHA, headSHA, headSHA, workerID, workerState, l.host, l.checkout, l.host, l.checkout)
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
