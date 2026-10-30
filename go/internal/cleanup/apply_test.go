package cleanup

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

func TestCleanup_applyRemovesIgnoredOutputsAndRecordsPaths(t *testing.T) {
	l := newLab(t)
	_ = l.makeWorktreeCheckout()
	gitRun := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", l.checkout}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.WriteFile(filepath.Join(l.checkout, ".gitignore"), []byte(".env\nweb/.astro/\nweb/dist/\nbuild/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", ".gitignore")
	gitRun("commit", "-q", "-m", "ignore generated outputs")
	head := gitRun("rev-parse", "HEAD")
	for _, rel := range []string{".env", "web/.astro/cache", "web/dist/index.html", "build/output"} {
		path := filepath.Join(l.checkout, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("generated"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	l.saveTask(l.mergedTaskJSON(head, `[]`, "released"))
	plan, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID})
	if err != nil {
		t.Fatalf("cleanup inspection: %v", err)
	}
	t.Logf("inspection artifacts: %v", func() any { v, _ := plan.Get("artifacts"); return v }())
	if hasCode(blockerCodes(plan), "artifacts") {
		t.Fatalf("ignored outputs blocked cleanup: %v", blockerCodes(plan))
	}
	artifacts := asObject(func() any { v, _ := plan.Get("artifacts"); return v }())
	paths := asList(func() any { v, _ := artifacts.Get("ignored_paths"); return v }())
	for _, want := range []string{".env", "web/.astro/", "web/dist/", "build/"} {
		found := false
		for _, raw := range paths {
			if raw == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("inspection ignored_paths = %v, missing %s", paths, want)
		}
	}
	task, err := l.store.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := asObject(func() any { v, _ := task.Get("cleanup"); return v }())
	if got := asList(func() any { v, _ := cleanup.Get("ignored_paths"); return v }()); !reflect.DeepEqual(got, paths) {
		t.Fatalf("recorded inspection ignored_paths = %v, want %v", got, paths)
	}
	result, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true})
	if err != nil {
		t.Fatalf("cleanup --apply: %v", err)
	}
	t.Logf("cleanup removed: %v", func() any { v, _ := result.Get("removed"); return v }())
	removed := asObject(func() any { v, _ := result.Get("removed"); return v }())
	removedPaths := asList(func() any { v, _ := removed.Get("ignored_paths"); return v }())
	if !reflect.DeepEqual(removedPaths, paths) {
		t.Fatalf("removed ignored_paths = %v, want %v", removedPaths, paths)
	}
	task, err = l.store.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	cleanup = asObject(func() any { v, _ := task.Get("cleanup"); return v }())
	recorded := asObject(func() any { v, _ := cleanup.Get("removed"); return v }())
	if got := asList(func() any { v, _ := recorded.Get("ignored_paths"); return v }()); !reflect.DeepEqual(got, paths) {
		t.Fatalf("recorded ignored_paths = %v, want %v", got, paths)
	}
}

func TestCleanup_dirtyNonIgnoredFilesAndCommitsStillBlock(t *testing.T) {
	for _, scenario := range []string{"untracked", "modified", "local commit"} {
		t.Run(scenario, func(t *testing.T) {
			l := newLab(t)
			head := l.makeWorktreeCheckout()
			gitRun := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-C", l.checkout}, args...)...)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			if scenario == "modified" || scenario == "local commit" {
				if err := os.WriteFile(filepath.Join(l.checkout, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				gitRun("add", "tracked.txt")
				gitRun("commit", "-q", "-m", "add tracked file")
				head = gitRun("rev-parse", "HEAD")
			}
			l.saveTask(l.mergedTaskJSON(head, `[]`, "released"))
			switch scenario {
			case "untracked":
				if err := os.WriteFile(filepath.Join(l.checkout, "notes.txt"), []byte("keep me\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "modified":
				if err := os.WriteFile(filepath.Join(l.checkout, "tracked.txt"), []byte("changed\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "local commit":
				gitRun("commit", "--allow-empty", "-q", "-m", "local work")
			}
			plan := l.inspect()
			if !hasCode(blockerCodes(plan), map[string]string{"untracked": "artifacts", "modified": "artifacts", "local commit": "commits"}[scenario]) {
				t.Fatalf("blockers = %v, want blocker for %s", blockerCodes(plan), scenario)
			}
			if _, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true}); err == nil {
				t.Fatal("cleanup --apply succeeded with a non-ignored change")
			}
			if _, err := os.Stat(l.checkout); err != nil {
				t.Fatalf("cleanup removed checkout with %s change: %v", scenario, err)
			}
		})
	}
}

func (l *lab) makeSquashMergedCheckout() (prHead, squash string) {
	l.t.Helper()
	l.makeWorktreeCheckout()
	git := func(dir string, args ...string) string {
		l.t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			l.t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(l.checkout, "commit", "--allow-empty", "-q", "-m", "worker change")
	prHead = git(l.checkout, "rev-parse", "HEAD")
	git(l.repo, "commit", "--allow-empty", "-q", "-m", "squash merge of the task PR")
	squash = git(l.repo, "rev-parse", "HEAD")
	git(l.repo, "update-ref", "refs/remotes/origin/main", squash)
	git(l.checkout, "reset", "--hard", "-q", squash)
	return prHead, squash
}

func (l *lab) saveSquashMergedTask(prHead, squash string) {
	l.t.Helper()
	l.saveTask(l.mergedTaskExtra(prHead, `[]`, "released", `, "base_branch": "main", "base_branch_source": "explicit"`))
	task, err := l.store.ReadTask(taskID)
	if err != nil {
		l.t.Fatal(err)
	}
	pr := asObject(func() any { v, _ := task.Get("pr"); return v }())
	pr.Set("merge_commit", squash)
	if err := l.store.SaveTask(task); err != nil {
		l.t.Fatal(err)
	}
}

func TestCleanup_squashMergeOnBaseCountsAsDelivered(t *testing.T) {
	l := newLab(t)
	prHead, squash := l.makeSquashMergedCheckout()
	l.saveSquashMergedTask(prHead, squash)

	plan := l.inspect()
	if hasCode(blockerCodes(plan), "commits") {
		t.Fatalf("base-branch commits blocked cleanup: %v", blockerDetails(plan))
	}
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
}

func TestCleanup_unmergedCommitAfterSquashResetStillBlocks(t *testing.T) {
	l := newLab(t)
	prHead, squash := l.makeSquashMergedCheckout()
	l.saveSquashMergedTask(prHead, squash)
	cmd := exec.Command("git", "-C", l.checkout, "commit", "--allow-empty", "-q", "-m", "unmerged local work")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	plan := l.inspect()
	if !hasCode(blockerCodes(plan), "commits") {
		t.Fatalf("unmerged commit did not block: %v", blockerDetails(plan))
	}
	if _, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true}); err == nil {
		t.Fatal("cleanup --apply succeeded with an unmerged commit")
	}
	if _, err := os.Stat(l.checkout); err != nil {
		t.Fatalf("checkout removed despite the unmerged commit: %v", err)
	}
}

func TestCleanup_mergedHeadNotLocalObjectCoveredByRemoteBase(t *testing.T) {
	l := newLab(t)
	_, squash := l.makeSquashMergedCheckout()
	l.saveSquashMergedTask("ffffffffffffffffffffffffffffffffffffffff", squash)

	plan := l.inspect()
	if hasCode(blockerCodes(plan), "commits") {
		t.Fatalf("coverage from the remote base alone blocked cleanup: %v", blockerDetails(plan))
	}
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
}

func TestCleanup_mergedHeadNotLocalObjectExtraCommitBlocks(t *testing.T) {
	l := newLab(t)
	_, squash := l.makeSquashMergedCheckout()
	l.saveSquashMergedTask("ffffffffffffffffffffffffffffffffffffffff", squash)
	cmd := exec.Command("git", "-C", l.checkout, "commit", "--allow-empty", "-q", "-m", "unmerged local work")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	plan := l.inspect()
	if !hasCode(blockerCodes(plan), "commits") {
		t.Fatalf("a commit past the remote base did not block: %v", blockerDetails(plan))
	}
	if _, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true}); err == nil {
		t.Fatal("cleanup --apply succeeded with a commit past the remote base")
	}
	if _, err := os.Stat(l.checkout); err != nil {
		t.Fatalf("checkout removed despite the uncovered commit: %v", err)
	}
}

func TestCleanup_localBranchNamedLikeRemoteBaseDoesNotCover(t *testing.T) {
	l := newLab(t)
	prHead, squash := l.makeSquashMergedCheckout()
	l.saveSquashMergedTask(prHead, squash)
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(l.checkout, "commit", "--allow-empty", "-q", "-m", "unmerged local work")
	extra := git(l.checkout, "rev-parse", "HEAD")
	git(l.repo, "update-ref", "refs/heads/origin/main", extra)

	plan := l.inspect()
	if !hasCode(blockerCodes(plan), "commits") {
		t.Fatalf("a local refs/heads/origin/main covered the extra commit: %v", blockerDetails(plan))
	}
	if _, err := Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true}); err == nil {
		t.Fatal("cleanup --apply succeeded on coverage from a local branch named like the remote base")
	}
	if _, err := os.Stat(l.checkout); err != nil {
		t.Fatalf("checkout removed on local-branch coverage: %v", err)
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

// The old workspace fixture maps to native terminal absence with Git registration
// retained independently. No runtime workspace is synthesized.
func (l *lab) writeWorkspaceWithoutPane() { l.t.Helper() }

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

// The native terminal is gone but Git registration still exists.
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
	if _, ok := resources.Get("workspace"); ok {
		t.Fatal("HC resource view invented a Git workspace")
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
	l.workerTerminal(true)
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
	intent.Set("worker_endpoint", l.endpoint("t2", "worker", workerID, 2).Object())
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
	if err := saveCleanupIntent(l.store, taskID, intent, attempts, asObject(func() any { v, _ := plan.Get("resources"); return v }()), task); err != nil {
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
	if err := saveCleanupIntent(l.store, taskID, intent, []any{}, ordjson.NewObject(), func() *ordjson.Object {
		task, err := l.store.ReadTask(taskID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	}()); err != nil {
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
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: "host", CoreID: "core", Socket: "/tmp/sum-test.sock", Session: "lab", Address: "t2", Role: "worker", Task: "t-aaaaaaaaaaaa", Attempt: "attempt", BindingGeneration: 1, CreatedMS: 2}
	task.Set("worker_endpoint", endpoint.Object())
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
	saved.Set("worker_endpoint", endpoint.Object())
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
	other := endpoint
	other.Address = "t3"
	saved.Set("worker_endpoint", other.Object())
	if intentMatches(saved, task, plan, "cccc", attempts) {
		t.Fatal("intent for another pane accepted")
	}
	saved.Set("worker_endpoint", endpoint.Object())
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
