package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const gateTaskID = "t-aaaaaaaaaaaa"
const gateBranch = "sum/t-aaaaaaaaaaaa"

// gateLab is a real local Git topology: a bare origin, a clone standing in for the task repository, and a task
// branch one commit ahead of main. The rebase and push gates read and write actual refs, never a stub.
type gateLab struct {
	home      string
	origin    string
	source    string
	clone     string
	candidate string
	ghRoot    string
}

func newGateLab(t *testing.T) gateLab {
	t.Helper()
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	if out, err := runCLI(t, home, "init"); err != nil {
		t.Fatalf("coordinator init: %v\n%s", err, out)
	}
	root := t.TempDir()
	lab := gateLab{
		home:   home,
		origin: filepath.Join(root, "origin.git"),
		source: filepath.Join(root, "source"),
		clone:  filepath.Join(root, "clone"),
	}
	gitIn(t, root, "init", "-q", "--bare", "-b", "main", lab.origin)

	gitIn(t, root, "clone", "-q", lab.origin, lab.source)
	identify(t, lab.source)
	writeFile(t, filepath.Join(lab.source, "app.txt"), "base\n")
	gitIn(t, lab.source, "add", "-A")
	gitIn(t, lab.source, "commit", "-q", "-m", "base")
	gitIn(t, lab.source, "push", "-q", "origin", "main")

	gitIn(t, root, "clone", "-q", lab.origin, lab.clone)
	identify(t, lab.clone)
	gitIn(t, lab.clone, "checkout", "-q", "-b", gateBranch)
	writeFile(t, filepath.Join(lab.clone, "app.txt"), "candidate\n")
	gitIn(t, lab.clone, "commit", "-q", "-am", "candidate")
	lab.candidate = strings.TrimSpace(gitIn(t, lab.clone, "rev-parse", "HEAD"))

	lab.writeTask(t, "reported")
	return lab
}

func (lab gateLab) writeTask(t *testing.T, status string, evidence ...string) {
	t.Helper()
	base := strings.TrimSpace(gitIn(t, lab.clone, "rev-parse", "HEAD~1"))
	writeTaskFixture(t, lab.home, gateTaskID, fmt.Sprintf(`{"schema": 1, "id": %q, "status": %q, "repository": %q, "worktree": %q,
"questions": [], "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "ship", "branch": %q,
"report": {"text": "done", "candidate": %q}, "evidence": [%s]}`,
		gateTaskID, status, lab.clone, lab.clone, base, gateBranch, lab.candidate, strings.Join(evidence, ",\n")))
}

// advanceOrigin lands one more commit on origin/main, which is what leaves the candidate behind its base.
func (lab gateLab) advanceOrigin(t *testing.T, name, content string) {
	t.Helper()
	writeFile(t, filepath.Join(lab.source, name), content)
	gitIn(t, lab.source, "add", "-A")
	gitIn(t, lab.source, "commit", "-q", "-m", "origin moves on")
	gitIn(t, lab.source, "push", "-q", "origin", "main")
}

func (lab gateLab) row(t *testing.T, stage string) map[string]any {
	t.Helper()
	stdout, stderr, err := runPRCLI(t, lab.home, "pipeline", "show", gateTaskID)
	if err != nil {
		t.Fatalf("pipeline show: %v stderr=%s", err, stderr)
	}
	return pipelineRow(t, decodeObject(t, stdout), stage)
}

func (lab gateLab) remoteSHA(t *testing.T, branch string) string {
	t.Helper()
	out := gitIn(t, lab.clone, "ls-remote", "--heads", "origin", branch)
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func identify(t *testing.T, dir string) {
	t.Helper()
	gitIn(t, dir, "config", "user.email", "gate@example.com")
	gitIn(t, dir, "config", "user.name", "gate")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGate(t *testing.T, lab gateLab, args ...string) map[string]any {
	t.Helper()
	stdout, stderr, err := runPRCLI(t, lab.home, append([]string{"pipeline"}, args...)...)
	if err != nil {
		t.Fatalf("pipeline %v: %v stderr=%s", args, err, stderr)
	}
	return decodeObject(t, stdout)
}

// assertNoLeftovers proves the gate cleaned up after itself: no throwaway worktree and no half-finished rebase.
func assertNoLeftovers(t *testing.T, lab gateLab) {
	t.Helper()
	if entries, _ := os.ReadDir(filepath.Join(lab.clone, ".git", "worktrees")); len(entries) != 0 {
		t.Fatalf("the rebase checkout was left behind: %v", entries)
	}
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(lab.clone, ".git", name)); err == nil {
			t.Fatalf("a rebase is still in progress in the task repository (.git/%s)", name)
		}
	}
	if dirty := strings.TrimSpace(gitIn(t, lab.clone, "status", "--porcelain")); dirty != "" {
		t.Fatalf("the task repository was modified:\n%s", dirty)
	}
}

func TestPipelineRebase_candidateOnTopOfItsBasePasses(t *testing.T) {
	lab := newGateLab(t)

	result := runGate(t, lab, "rebase", gateTaskID)

	record, _ := result["evidence"].(map[string]any)
	if record == nil || record["kind"] != "rebase" || record["source"] != "coordinator" {
		t.Fatalf("pipeline rebase recorded %v, want a coordinator rebase record", result["evidence"])
	}
	if record["outcome"] != "up-to-date" || record["base_branch"] != "main" {
		t.Fatalf("rebase record = %v, want an up-to-date observation of main", record)
	}
	row := lab.row(t, "rebase")
	if row["status"] != "pass" || row["result"] != "Up to date with main" {
		t.Fatalf("rebase row = %v, want a pass", row)
	}
	assertNoLeftovers(t, lab)
}

func TestPipelineRebase_behindButCleanBlocksAndNamesTheCount(t *testing.T) {
	lab := newGateLab(t)
	lab.advanceOrigin(t, "other.txt", "elsewhere\n")

	runGate(t, lab, "rebase", gateTaskID)

	row := lab.row(t, "rebase")
	want := "Behind main by 1 commit; rebases cleanly. The worker rebases; send `repair send` with the rebase instruction"
	if row["status"] != "blocked" || row["result"] != want {
		t.Fatalf("rebase row = %v\nwant blocked with %q", row, want)
	}
	assertNoLeftovers(t, lab)
}

func TestPipelineRebase_conflictFailsAndNamesTheConflictingPath(t *testing.T) {
	lab := newGateLab(t)
	lab.advanceOrigin(t, "app.txt", "someone else's line\n")

	runGate(t, lab, "rebase", gateTaskID)

	row := lab.row(t, "rebase")
	if row["status"] != "fail" || row["result"] != "Conflicts with main in: app.txt" {
		t.Fatalf("rebase row = %v, want a fail naming app.txt", row)
	}
	assertNoLeftovers(t, lab)
}

// declareLint commits a mise task named lint into the candidate, which is how a project declares this gate's work.
func (lab *gateLab) declareLint(t *testing.T, exit int) {
	t.Helper()
	path := filepath.Join(lab.clone, "mise-tasks", "lint")
	writeFile(t, path, fmt.Sprintf("#!/bin/sh\necho \"lint says %d\"\nexit %d\n", exit, exit))
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, lab.clone, "add", "-A")
	gitIn(t, lab.clone, "commit", "-q", "-m", "declare a lint task")
	lab.candidate = strings.TrimSpace(gitIn(t, lab.clone, "rev-parse", "HEAD"))
	lab.writeTask(t, "reported")
}

func TestPipelineLint_projectWithNoLintTaskIsNotDeclared(t *testing.T) {
	lab := newGateLab(t)

	result := runGate(t, lab, "lint", gateTaskID)

	record, _ := result["evidence"].(map[string]any)
	if record == nil || record["kind"] != "lint" || record["outcome"] != "not-declared" {
		t.Fatalf("pipeline lint recorded %v, want a not-declared lint record", result["evidence"])
	}
	row := lab.row(t, "lint")
	if row["status"] != "not_declared" || row["result"] != "This project declares no lint task" {
		t.Fatalf("lint row = %v, want not_declared", row)
	}
	if row["mark"] != "➖" {
		t.Fatalf("lint mark = %v, want ➖", row["mark"])
	}
}

func TestPipelineLint_projectsOwnLintPasses(t *testing.T) {
	lab := newGateLab(t)
	lab.declareLint(t, 0)

	result := runGate(t, lab, "lint", gateTaskID)

	record, _ := result["evidence"].(map[string]any)
	if record["outcome"] != "pass" || record["command"] != "mise run lint" {
		t.Fatalf("lint record = %v, want a pass of the project's own task", record)
	}
	if record["log"] == nil {
		t.Fatalf("lint record keeps no log: %v", record)
	}
	row := lab.row(t, "lint")
	if row["status"] != "pass" || row["result"] != "Passed (`mise run lint`)" {
		t.Fatalf("lint row = %v, want a pass", row)
	}
}

func TestPipelineLint_projectsOwnLintFailsAndQuotesItsLastLine(t *testing.T) {
	lab := newGateLab(t)
	lab.declareLint(t, 1)

	runGate(t, lab, "lint", gateTaskID)

	row := lab.row(t, "lint")
	if row["status"] != "fail" || row["result"] != "Failed (`mise run lint`): lint says 1" {
		t.Fatalf("lint row = %v, want a fail quoting the lint's own last line", row)
	}
}

func TestPipelineLint_noDeclaredBootstrapLeavesBootstrapFieldsEmpty(t *testing.T) {
	lab := newGateLab(t)
	lab.declareLint(t, 0)

	result := runGate(t, lab, "lint", gateTaskID)

	record, _ := result["evidence"].(map[string]any)
	if record["bootstrap_command"] != nil || record["bootstrap_exit"] != nil ||
		record["bootstrap_seconds"] != nil || record["bootstrap_log"] != nil {
		t.Fatalf("lint record = %v, want empty bootstrap fields when the project declares none", record)
	}
}

func TestPipelineLint_miseDepsRunsBeforeLint(t *testing.T) {
	lab := newGateLab(t)
	lab.declareDepsAndLint(t, 0, `#!/bin/sh
if [ ! -f .bootstrapped ]; then echo "lint ran first"; exit 2; fi
echo "lint says 0"
exit 0
`)

	result := runGate(t, lab, "lint", gateTaskID)

	record, _ := result["evidence"].(map[string]any)
	if record["outcome"] != "pass" || record["bootstrap_command"] != "mise run deps" || record["command"] != "mise run lint" {
		t.Fatalf("lint record = %v, want deps then lint", record)
	}
	if fmt.Sprint(record["bootstrap_exit"]) != "0" {
		t.Fatalf("bootstrap_exit = %v (%T)", record["bootstrap_exit"], record["bootstrap_exit"])
	}
	if record["bootstrap_log"] == nil {
		t.Fatalf("bootstrap_log missing: %v", record)
	}
	row := lab.row(t, "lint")
	if row["status"] != "pass" || row["result"] != "Passed (`mise run lint`)" {
		t.Fatalf("lint row = %v, want a pass after bootstrap", row)
	}
	assertNoLeftovers(t, lab)
}

func TestPipelineLint_bootstrapFailureSkipsLint(t *testing.T) {
	lab := newGateLab(t)
	lab.declareDepsAndLint(t, 1, `#!/bin/sh
echo "lint ran"
exit 0
`)

	result := runGate(t, lab, "lint", gateTaskID)

	record, _ := result["evidence"].(map[string]any)
	if record["outcome"] != "fail" || record["bootstrap_command"] != "mise run deps" {
		t.Fatalf("lint record = %v, want a failed bootstrap", record)
	}
	if record["exit"] != nil || record["log"] != nil {
		t.Fatalf("lint ran after bootstrap failed: %v", record)
	}
	row := lab.row(t, "lint")
	if row["status"] != "fail" || row["result"] != "Bootstrap failed (`mise run deps`): deps says 1" {
		t.Fatalf("lint row = %v, want the bootstrap failure in the summary", row)
	}
	assertNoLeftovers(t, lab)
}

func TestPipelineLint_lockfileInstallPicksPnpm(t *testing.T) {
	lab := newGateLab(t)
	lab.declareLint(t, 0)
	writeFile(t, filepath.Join(lab.clone, "package.json"), `{"name":"app","private":true}`+"\n")
	writeFile(t, filepath.Join(lab.clone, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n")
	gitIn(t, lab.clone, "add", "-A")
	gitIn(t, lab.clone, "commit", "-q", "-m", "declare a pnpm lockfile")
	lab.candidate = strings.TrimSpace(gitIn(t, lab.clone, "rev-parse", "HEAD"))
	lab.writeTask(t, "reported")
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "pnpm"), "#!/bin/sh\necho pnpm-ok\nexit 0\n")
	if err := os.Chmod(filepath.Join(bin, "pnpm"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	result := runGate(t, lab, "lint", gateTaskID)

	record, _ := result["evidence"].(map[string]any)
	if record["outcome"] != "pass" || record["bootstrap_command"] != "pnpm install --frozen-lockfile" {
		t.Fatalf("lint record = %v, want a pnpm frozen install before lint", record)
	}
}

func (lab *gateLab) declareDepsAndLint(t *testing.T, depsExit int, lintBody string) {
	t.Helper()
	writeFile(t, filepath.Join(lab.clone, "mise-tasks", "deps"), fmt.Sprintf("#!/bin/sh\necho \"deps says %d\"\ntouch .bootstrapped\nexit %d\n", depsExit, depsExit))
	if err := os.Chmod(filepath.Join(lab.clone, "mise-tasks", "deps"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(lab.clone, "mise-tasks", "lint"), lintBody)
	if err := os.Chmod(filepath.Join(lab.clone, "mise-tasks", "lint"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, lab.clone, "add", "-A")
	gitIn(t, lab.clone, "commit", "-q", "-m", "declare deps and lint")
	lab.candidate = strings.TrimSpace(gitIn(t, lab.clone, "rev-parse", "HEAD"))
	lab.writeTask(t, "reported")
}

func publicationRecords(candidate string) []string {
	return []string{
		fmt.Sprintf(`{"schema": 1, "id": "e-rev", "kind": "review", "source": "reviewer", "at": "2026-01-01T00:30:00+00:00",
"candidate": %q, "verdict": "approve", "text": "looks good", "policy_reviewed": false}`, candidate),
		fmt.Sprintf(`{"schema": 1, "id": "e-1", "kind": "verification", "source": "coordinator",
"at": "2026-01-01T01:00:00+00:00", "candidate": %q, "result": "pass", "run_id": "20260906T010203Z-abcd",
"certifies": %q, "requires_root_review": false}`, candidate, candidate),
		fmt.Sprintf(`{"schema": 1, "id": "e-doc", "kind": "documentation", "source": "coordinator",
"at": "2026-01-01T01:30:00+00:00", "candidate": %q, "result": "skipped", "summary": "No VERIFY.md"}`, candidate),
		fmt.Sprintf(`{"schema": 1, "id": "e-2", "kind": "lint", "source": "coordinator",
"at": "2026-01-01T02:00:00+00:00", "candidate": %q, "outcome": "not-declared",
"summary": "This project declares no lint task"}`, candidate),
	}
}

func TestPipelinePush_refusedUntilReviewPasses(t *testing.T) {
	lab := newGateLab(t)
	runGate(t, lab, "rebase", gateTaskID)

	_, _, err := runPRCLI(t, lab.home, "pipeline", "push", gateTaskID)

	if err == nil || !strings.Contains(err.Error(), "Review") {
		t.Fatalf("error = %v, want a Review refusal before any remote write", err)
	}
	if got := lab.remoteSHA(t, gateBranch); got != "" {
		t.Fatalf("origin/%s = %s, want nothing pushed", gateBranch, got)
	}
}

func TestPipelinePush_landsTheCandidateOnOrigin(t *testing.T) {
	lab := newGateLab(t)
	lab.writeTask(t, "reported", publicationRecords(lab.candidate)...)
	runGate(t, lab, "rebase", gateTaskID)

	result := runGate(t, lab, "push", gateTaskID)

	record, _ := result["evidence"].(map[string]any)
	if record == nil || record["kind"] != "push" || record["outcome"] != "pushed" {
		t.Fatalf("pipeline push recorded %v, want a pushed record", result["evidence"])
	}
	if record["remote_sha"] != lab.candidate {
		t.Fatalf("push record remote_sha = %v, want the candidate %s", record["remote_sha"], lab.candidate)
	}
	if got := lab.remoteSHA(t, gateBranch); got != lab.candidate {
		t.Fatalf("origin/%s = %s, want the candidate %s", gateBranch, got, lab.candidate)
	}
	row := lab.row(t, "push")
	want := fmt.Sprintf("Pushed %s to origin/%s", lab.candidate[:7], gateBranch)
	if row["status"] != "pass" || row["result"] != want {
		t.Fatalf("push row = %v\nwant a pass with %q", row, want)
	}

	repeated := runGate(t, lab, "push", gateTaskID)
	if again, _ := repeated["evidence"].(map[string]any); again["outcome"] != "already" {
		t.Fatalf("a second push = %v, want already", again)
	}
}

func TestPipelinePush_refusedUntilTheWorkerHasReported(t *testing.T) {
	lab := newGateLab(t)
	runGate(t, lab, "rebase", gateTaskID)
	lab.writeTask(t, "running")

	_, _, err := runPRCLI(t, lab.home, "pipeline", "push", gateTaskID)

	if err == nil {
		t.Fatal("pushing a task the worker has not reported should fail")
	}
	if !strings.Contains(err.Error(), "reported") {
		t.Fatalf("error = %v, want it to name the reported handoff", err)
	}
	if got := lab.remoteSHA(t, gateBranch); got != "" {
		t.Fatalf("origin/%s = %s, want nothing pushed", gateBranch, got)
	}
}

func TestPipelinePush_refusedWhileTheRebaseGateIsNotPassing(t *testing.T) {
	lab := newGateLab(t)
	lab.advanceOrigin(t, "other.txt", "elsewhere\n")
	runGate(t, lab, "rebase", gateTaskID)

	_, _, err := runPRCLI(t, lab.home, "pipeline", "push", gateTaskID)

	if err == nil || !strings.Contains(err.Error(), "--allow-behind") {
		t.Fatalf("error = %v, want a refusal naming --allow-behind", err)
	}
	if got := lab.remoteSHA(t, gateBranch); got != "" {
		t.Fatalf("origin/%s = %s, want nothing pushed", gateBranch, got)
	}
}

func TestPipelinePush_nonFastForwardIsRejectedAndOriginIsUnchanged(t *testing.T) {
	lab := newGateLab(t)
	lab.writeTask(t, "reported", publicationRecords(lab.candidate)...)
	runGate(t, lab, "rebase", gateTaskID)
	gitIn(t, lab.source, "checkout", "-q", "-b", gateBranch)
	writeFile(t, filepath.Join(lab.source, "app.txt"), "someone else pushed first\n")
	gitIn(t, lab.source, "commit", "-q", "-am", "someone else")
	gitIn(t, lab.source, "push", "-q", "origin", gateBranch)
	foreign := lab.remoteSHA(t, gateBranch)

	result := runGate(t, lab, "push", gateTaskID)

	record, _ := result["evidence"].(map[string]any)
	if record["outcome"] != "rejected" {
		t.Fatalf("push record = %v, want rejected", record)
	}
	row := lab.row(t, "push")
	want := fmt.Sprintf("Rejected: origin/%s has commits the candidate lacks; the worker rebases", gateBranch)
	if row["status"] != "fail" || row["result"] != want {
		t.Fatalf("push row = %v\nwant a fail with %q", row, want)
	}
	if got := lab.remoteSHA(t, gateBranch); got != foreign {
		t.Fatalf("origin/%s = %s, want the other commit %s left alone", gateBranch, got, foreign)
	}
}

func TestPipelineRun_stopsAtAFailingRebaseAndPushesNothing(t *testing.T) {
	lab := newGateLab(t)
	lab.advanceOrigin(t, "app.txt", "someone else's line\n")

	result := runGate(t, lab, "run", gateTaskID)

	steps, _ := result["steps"].([]any)
	if len(steps) != 6 {
		t.Fatalf("pipeline run reported %d steps, want one per coordinator gate:\n%v", len(steps), result["steps"])
	}
	first, _ := steps[0].(map[string]any)
	if first["stage"] != "rebase" || first["outcome"] != "ran" {
		t.Fatalf("first step = %v, want the rebase gate to have run", first)
	}
	for _, raw := range steps[1:] {
		step, _ := raw.(map[string]any)
		if step["outcome"] != "not-run" {
			t.Fatalf("step %v ran after the rebase failed, want not-run", step)
		}
		if step["detail"] != "the rebase gate failed" {
			t.Fatalf("step %v does not name the failing gate", step)
		}
	}
	if got := lab.remoteSHA(t, gateBranch); got != "" {
		t.Fatalf("origin/%s = %s, want nothing pushed after a failing rebase", gateBranch, got)
	}
	want := "Rebase is fail: Conflicts with main in: app.txt. Do: run `sumctl pipeline rebase TASK_ID`; a branch that is behind or conflicting is the worker's to rebase."
	if result["next"] != want {
		t.Fatalf("next = %v\nwant %q", result["next"], want)
	}
}

// --no-pr is the pre-PR-stage behaviour: the run stops after Push and the coordinator opens the PR itself.
func TestPipelineRun_skipsWhatIsRecordedAndCarriesOnToThePush(t *testing.T) {
	requirePython(t)
	lab := newGateLab(t)
	lab.writeTask(t, "reported", publicationRecords(lab.candidate)...)

	result := runGate(t, lab, "run", gateTaskID, "--no-pr")

	want := map[string]string{
		"rebase":   "ran",
		"test":     "skipped",
		"lint":     "skipped",
		"document": "ran",
		"push":     "ran",
		"pr":       "skipped",
	}
	steps, _ := result["steps"].([]any)
	for _, raw := range steps {
		step, _ := raw.(map[string]any)
		stage, _ := step["stage"].(string)
		if step["outcome"] != want[stage] {
			t.Fatalf("step %v, want the %s gate to be %s", step, stage, want[stage])
		}
	}
	if got := lab.remoteSHA(t, gateBranch); got != lab.candidate {
		t.Fatalf("origin/%s = %s, want the run to have pushed the candidate %s", gateBranch, got, lab.candidate)
	}
	if got := lab.row(t, "push")["status"]; got != "pass" {
		t.Fatalf("push row = %v after a full run, want pass", got)
	}
	next, _ := result["next"].(string)
	if !strings.HasPrefix(next, "PR is pending") {
		t.Fatalf("next = %q, want the PR gate after a successful push", next)
	}
}
