package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/machine"
)

var devinWorkerArgv = []string{"--permission-mode", "dangerous", "--respect-workspace-trust", "false"}

func herdrCalls(t *testing.T, base string) [][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(base, "fake", "calls.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var row struct {
			Args []string `json:"args"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		out = append(out, row.Args)
	}
	return out
}

func agentStartCalls(t *testing.T, base string) [][]string {
	t.Helper()
	var out [][]string
	for _, call := range herdrCalls(t, base) {
		if len(call) >= 2 && call[0] == "agent" && call[1] == "start" {
			out = append(out, call)
		}
	}
	return out
}

func startedArgv(task map[string]any) []string {
	var out []string
	for _, a := range asSlice(asMap(task["launch"])["started_argv"]) {
		out = append(out, asString(a))
	}
	return out
}

func argsAfterSeparator(call []string) ([]string, bool) {
	for i := len(call) - 1; i >= 0; i-- {
		if call[i] == "--" {
			return call[i+1:], true
		}
	}
	return nil, false
}

func TestDispatchDevinWorkerCarriesTrustGateArgv(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "devin-worker", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "devin", "--arg=--permission-mode", "--arg=dangerous", "--arg=--respect-workspace-trust", "--arg=false", "--permission-override", "--approved")
	if argv := startedArgv(task); !reflect.DeepEqual(argv, devinWorkerArgv) {
		t.Fatalf("devin worker started_argv = %v, want %v", argv, devinWorkerArgv)
	}
	starts := agentStartCalls(t, d.base)
	if len(starts) != 1 {
		t.Fatalf("agent start calls = %v, want exactly one", starts)
	}
	passed, ok := argsAfterSeparator(starts[0])
	if !ok || !reflect.DeepEqual(passed, devinWorkerArgv) {
		t.Fatalf("agent start args = %v, want -- followed by %v", starts[0], devinWorkerArgv)
	}
}

func TestDispatchOtherHarnessHasNoDevinArgv(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "codex-worker", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if argv := startedArgv(task); len(argv) != 0 {
		t.Fatalf("codex worker started_argv = %v, want none", argv)
	}
	starts := agentStartCalls(t, d.base)
	if len(starts) != 1 {
		t.Fatalf("agent start calls = %v, want exactly one", starts)
	}
	if _, ok := argsAfterSeparator(starts[0]); ok {
		t.Fatalf("codex agent start args = %v, want no -- separator", starts[0])
	}
}

func TestClaudeDispatchGrantsOnlyItsTaskInstructions(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "claude-task-access", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "claude", "--arg", "existing", "--approved")
	home := d.home
	if resolved, err := filepath.EvalSymlinks(d.home); err == nil {
		home = resolved
	}
	taskDir := filepath.Join(home, "tasks", asString(task["id"]))
	want := []string{"existing", "--add-dir", taskDir}
	if argv := startedArgv(task); !reflect.DeepEqual(argv, want) {
		t.Fatalf("Claude started_argv = %v, want only the active task directory %v", argv, want)
	}
	launch := asMap(task["launch"])
	if got := asSlice(launch["argv"]); !reflect.DeepEqual(got, []any{"existing", "--add-dir", taskDir}) {
		t.Fatalf("recorded launch argv = %v, want %v", got, want)
	}
	if got := asSlice(launch["task_access_args"]); !reflect.DeepEqual(got, []any{"--add-dir", taskDir}) {
		t.Fatalf("recorded task access arguments = %v, want %v", got, want)
	}
	starts := agentStartCalls(t, d.base)
	if len(starts) != 1 {
		t.Fatalf("agent start calls = %v, want exactly one", starts)
	}
	passed, ok := argsAfterSeparator(starts[0])
	if !ok || !reflect.DeepEqual(passed, want) {
		t.Fatalf("Claude agent start args = %v, want -- followed by %v", starts[0], want)
	}
	for _, arg := range passed {
		if arg == home || arg == filepath.Join(home, "tasks") || arg == filepath.Join(home, "tasks", "t-other") {
			t.Fatalf("Claude received a state-wide or unrelated task grant: %v", passed)
		}
	}
}

func TestDispatchRecordsTheStableMachineIdentity(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "identity-worker", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	id, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	attempt, occupant := launchedWorker(task)
	for name, recorded := range map[string]any{"task": task["machine"], "attempt owner": asMap(attempt["owner"])["machine"], "occupant": occupant["machine"]} {
		if recorded != id {
			t.Fatalf("%s machine = %v, want the stable identity %s", name, recorded, id)
		}
	}
}

func launchedWorker(task map[string]any) (attempt, occupant map[string]any) {
	attempt = asMap(asMap(task["execution"])["worker"])
	return attempt, asMap(attempt["occupant"])
}

// settleFakeWorker returns the fake worker pane to idle, like a worker that
// finished its turn and waits for the next instruction.
func settleFakeWorker(t *testing.T, base, pane string) {
	t.Helper()
	path := filepath.Join(base, "fake", "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	asMap(asMap(state["panes"])[pane])["agent_status"] = "idle"
	out, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchBindsAgentThatStartsForegroundChildren(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "mcp-children", map[string]string{"README.md": "x\n"})
	d.setEnv("FAKE_AGENT_CHILDREN", "2")
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "claude", "--approved")
	attempt, occupant := launchedWorker(task)
	if state := asString(attempt["state"]); state != "running" {
		t.Fatalf("worker attempt state = %q, want running; observations %v", state, attempt["observations"])
	}
	if occupant["pid"] == nil || !reflect.DeepEqual(occupant["argv"], []any{"claude"}) || occupant["shell_pid"] == nil {
		t.Fatalf("occupant = %v, want the claude group leader bound with its shell", occupant)
	}
	settleFakeWorker(t, d.base, asString(task["pane"]))
	sent := d.ctl(false, "repair", "send", asString(task["id"]), "--attempt", asString(attempt["id"]), "--key", "rebase", "--text", "Rebase onto main.")
	if msg := asString(sent["error"]); msg != "" {
		t.Fatalf("repair send to the bound worker failed: %s", msg)
	}
}

func TestDispatchKeepsForegroundWithoutItsLeaderUncertain(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "orphaned-group", map[string]string{"README.md": "x\n"})
	d.setEnv("FAKE_AGENT_CHILDREN", "2")
	d.setEnv("FAKE_AGENT_LEADER_GONE", "1")
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "claude", "--approved")
	attempt, occupant := launchedWorker(task)
	if state := asString(attempt["state"]); state != "uncertain" {
		t.Fatalf("worker attempt state = %q, want uncertain when no group leader is observed", state)
	}
	if occupant["pid"] != nil || occupant["argv"] != nil {
		t.Fatalf("occupant = %v, want no guessed pid", occupant)
	}
	settleFakeWorker(t, d.base, asString(task["pane"]))
	sent := d.ctl(false, "repair", "send", asString(task["id"]), "--attempt", asString(attempt["id"]), "--key", "rebase", "--text", "Rebase onto main.")
	if msg := asString(sent["error"]); !strings.Contains(msg, "exact running worker attempt") {
		t.Fatalf("repair send to an uncertain attempt = %v, want refusal", sent)
	}
}

func flagAfter(call []string, name string) (string, bool) {
	for i := 0; i < len(call)-1; i++ {
		if call[i] == name {
			return call[i+1], true
		}
	}
	return "", false
}

func TestDispatchAgentStartTimeoutAllowsGrokColdStart(t *testing.T) {
	if herdrclient.AgentStartTimeout.Milliseconds() == 30000 {
		t.Fatalf("AgentStartTimeout = %s, want a bound above 30s", herdrclient.AgentStartTimeout)
	}
	if herdrclient.AgentStartCallTimeout <= herdrclient.AgentStartTimeout {
		t.Fatalf("AgentStartCallTimeout = %s, want above AgentStartTimeout %s", herdrclient.AgentStartCallTimeout, herdrclient.AgentStartTimeout)
	}
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "grok-cold-start", map[string]string{"README.md": "x\n"})
	d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "grok", "--approved")
	starts := agentStartCalls(t, d.base)
	if len(starts) != 1 {
		t.Fatalf("agent start calls = %v, want exactly one", starts)
	}
	got, ok := flagAfter(starts[0], "--timeout")
	want := fmt.Sprint(herdrclient.AgentStartTimeout.Milliseconds())
	if !ok || got != want || got == "30000" {
		t.Fatalf("agent start --timeout = %q, want %s in %v", got, want, starts[0])
	}
}

func TestDispatchRetriesAgentPaneBusy(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "busy-pane", map[string]string{"README.md": "x\n"})
	d.setEnv("FAKE_START_BUSY", "2")
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if status := asString(task["status"]); status != "running" {
		t.Fatalf("task status = %q, want running", status)
	}
	if starts := agentStartCalls(t, d.base); len(starts) != 3 {
		t.Fatalf("agent start calls = %d, want two busy refusals then one success", len(starts))
	}
}

func TestDispatchBranchesFromTheFetchedBase(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "moved-base", map[string]string{"README.md": "x\n"})
	local := strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD"))
	writeFile(t, filepath.Join(repo, "ADVANCED.md"), "y\n")
	gitIn(t, repo, "add", "ADVANCED.md")
	gitIn(t, repo, "commit", "-q", "-m", "advance")
	gitIn(t, repo, "push", "-q", "origin", "main")
	remote := strings.TrimSpace(gitIn(t, repo, "rev-parse", "origin/main"))
	if remote == local {
		t.Fatal("origin did not move")
	}
	gitIn(t, repo, "reset", "--hard", "-q", local)
	gitIn(t, repo, "update-ref", "refs/remotes/origin/main", local)

	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if asString(task["base_sha"]) != remote {
		t.Fatalf("base_sha = %v, want the fetched remote tip %s", task["base_sha"], remote)
	}
	if head := strings.TrimSpace(gitIn(t, asString(task["worktree"]), "rev-parse", "HEAD")); head != remote {
		t.Fatalf("worktree HEAD = %s, want the fetched remote tip %s", head, remote)
	}
}

func TestDispatchRefusesABaseTheRemoteDoesNotAnswer(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "dead-remote", map[string]string{"README.md": "x\n"})
	gitIn(t, repo, "remote", "set-url", "origin", filepath.Join(d.base, "gone.git"))

	refused := d.ctl(false, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if msg := asString(refused["error"]); !strings.Contains(msg, "Could not fetch") {
		t.Fatalf("dispatch error = %q, want a fetch refusal", msg)
	}
	entries, err := os.ReadDir(filepath.Join(d.home, "tasks"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a refused dispatch left task records: %v", entries)
	}
	for _, call := range herdrCalls(t, d.base) {
		if len(call) >= 2 && call[0] == "worktree" && call[1] == "create" {
			t.Fatalf("a refused dispatch still created a worktree: %v", call)
		}
	}
}

func TestDispatchHonorsAnExplicitBaseRef(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "stacked", map[string]string{"README.md": "x\n"})
	gitIn(t, repo, "checkout", "-q", "-b", "feature")
	writeFile(t, filepath.Join(repo, "FEATURE.md"), "f\n")
	gitIn(t, repo, "add", "FEATURE.md")
	gitIn(t, repo, "commit", "-q", "-m", "feature")
	gitIn(t, repo, "push", "-q", "origin", "feature")
	feature := strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD"))
	gitIn(t, repo, "checkout", "-q", "main")

	task := d.ctl(true, "dispatch", "--repo", repo, "--base", "feature", "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if asString(task["base_sha"]) != feature {
		t.Fatalf("base_sha = %v, want the fetched feature tip %s", task["base_sha"], feature)
	}
	if head := strings.TrimSpace(gitIn(t, asString(task["worktree"]), "rev-parse", "HEAD")); head != feature {
		t.Fatalf("worktree HEAD = %s, want %s", head, feature)
	}
	if asString(task["base_branch"]) != "feature" || asString(task["base_branch_source"]) != "explicit" {
		t.Fatalf("recorded base = %v/%v, want feature recorded as explicit", task["base_branch"], task["base_branch_source"])
	}
	again := d.ctl(true, "prepare", "--repo", repo, "--base", "origin/feature", "--brief", policyBrief(t, d.base), "--approved")
	if asString(again["base_sha"]) != feature {
		t.Fatalf("origin/feature base_sha = %v, want %s", again["base_sha"], feature)
	}
	if asString(again["base_branch"]) != "feature" || asString(again["base_branch_source"]) != "explicit" {
		t.Fatalf("origin/feature recorded base = %v/%v, want the bare name feature as explicit", again["base_branch"], again["base_branch_source"])
	}
}

func pushDivergentRelease(t *testing.T, repo string) string {
	t.Helper()
	gitIn(t, repo, "checkout", "-q", "-b", "release/1.2")
	writeFile(t, filepath.Join(repo, "RELEASE.md"), "1.2\n")
	gitIn(t, repo, "add", "RELEASE.md")
	gitIn(t, repo, "commit", "-q", "-m", "release work")
	gitIn(t, repo, "push", "-q", "origin", "release/1.2")
	release := strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD"))
	gitIn(t, repo, "checkout", "-q", "main")
	writeFile(t, filepath.Join(repo, "MAIN.md"), "main moves on\n")
	gitIn(t, repo, "add", "MAIN.md")
	gitIn(t, repo, "commit", "-q", "-m", "main work")
	gitIn(t, repo, "push", "-q", "origin", "main")
	return release
}

func assertNothingPrepared(t *testing.T, d *demoLab) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(d.home, "tasks"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a refused dispatch left task records: %v", entries)
	}
	for _, call := range herdrCalls(t, d.base) {
		if len(call) >= 2 && call[0] == "worktree" && call[1] == "create" {
			t.Fatalf("a refused dispatch still created a worktree: %v", call)
		}
	}
}

func TestDispatchRecordsAReleaseBranchAsTheDeliveryTarget(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "release-target", map[string]string{"README.md": "x\n"})
	release := pushDivergentRelease(t, repo)

	task := d.ctl(true, "dispatch", "--repo", repo, "--base", "release/1.2", "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if asString(task["base_sha"]) != release || asString(task["base_branch"]) != "release/1.2" || asString(task["base_branch_source"]) != "explicit" {
		t.Fatalf("task base = %v/%v/%v, want the release tip recorded as release/1.2 explicit", task["base_sha"], task["base_branch"], task["base_branch_source"])
	}
	if head := strings.TrimSpace(gitIn(t, asString(task["worktree"]), "rev-parse", "HEAD")); head != release {
		t.Fatalf("worktree HEAD = %s, want the release tip %s", head, release)
	}
}

func TestDispatchResolvesTheDefaultBranchOnceAtPrepare(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "default-once", map[string]string{"README.md": "x\n"})
	pushDivergentRelease(t, repo)

	first := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if asString(first["base_branch"]) != "main" || asString(first["base_branch_source"]) != "default" {
		t.Fatalf("default base = %v/%v, want main recorded as default", first["base_branch"], first["base_branch_source"])
	}

	origin := strings.TrimSpace(gitIn(t, repo, "remote", "get-url", "origin"))
	gitIn(t, origin, "symbolic-ref", "HEAD", "refs/heads/release/1.2")
	second := d.ctl(true, "prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	if asString(second["base_branch"]) != "release/1.2" || asString(second["base_branch_source"]) != "default" {
		t.Fatalf("base after the remote default moved = %v/%v, want release/1.2 as default", second["base_branch"], second["base_branch_source"])
	}
	recorded := readTaskJSON(t, filepath.Join(d.home, "tasks", asString(first["id"]), "task.json"))
	if asString(recorded["base_branch"]) != "main" {
		t.Fatalf("first task now records %v, want its original main", recorded["base_branch"])
	}
}

func TestDispatchRefusesATagAsBaseWithoutATargetBranch(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "tag-base", map[string]string{"README.md": "x\n"})
	gitIn(t, repo, "-c", "tag.gpgsign=false", "tag", "-a", "-m", "v1.0", "v1.0")
	gitIn(t, repo, "push", "-q", "origin", "v1.0")

	refused := d.ctl(false, "dispatch", "--repo", repo, "--base", "v1.0", "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if msg := asString(refused["error"]); !strings.Contains(msg, "names no branch on origin") || !strings.Contains(msg, "--target-branch") {
		t.Fatalf("dispatch error = %q, want a non-branch refusal naming --target-branch", msg)
	}
	assertNothingPrepared(t, d)
}

func TestDispatchAcceptsATagStartOnlyWithAnAncestorTargetBranch(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "tag-start", map[string]string{"README.md": "x\n"})
	start := strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD"))
	gitIn(t, repo, "-c", "tag.gpgsign=false", "tag", "-a", "-m", "v1.0", "v1.0")
	gitIn(t, repo, "push", "-q", "origin", "v1.0")
	release := pushDivergentRelease(t, repo)
	gitIn(t, repo, "checkout", "-q", "-b", "elsewhere", start)
	writeFile(t, filepath.Join(repo, "ELSEWHERE.md"), "not on the release line\n")
	gitIn(t, repo, "add", "ELSEWHERE.md")
	gitIn(t, repo, "commit", "-q", "-m", "elsewhere")
	gitIn(t, repo, "-c", "tag.gpgsign=false", "tag", "-a", "-m", "v2.0", "v2.0")
	gitIn(t, repo, "push", "-q", "origin", "v2.0")
	gitIn(t, repo, "checkout", "-q", "main")

	task := d.ctl(true, "dispatch", "--repo", repo, "--base", "v1.0", "--target-branch", "release/1.2", "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	if asString(task["base_sha"]) != start || asString(task["base_branch"]) != "release/1.2" || asString(task["base_branch_source"]) != "explicit" {
		t.Fatalf("tag-start task = %v/%v/%v, want start %s delivering to release/1.2 explicit", task["base_sha"], task["base_branch"], task["base_branch_source"], start)
	}
	if head := strings.TrimSpace(gitIn(t, asString(task["worktree"]), "rev-parse", "HEAD")); head != start {
		t.Fatalf("worktree HEAD = %s, want the tag's commit %s (not the release tip %s)", head, start, release)
	}
	herdrBefore := len(herdrCalls(t, d.base))

	refusals := map[string][]string{
		"not an ancestor":                      {"--base", "v2.0", "--target-branch", "release/1.2"},
		"already names a branch":               {"--base", "main", "--target-branch", "release/1.2"},
		"give that start with --base":          {"--target-branch", "release/1.2"},
		"--target-branch nope names no branch": {"--base", "v1.0", "--target-branch", "nope"},
	}
	for want, flags := range refusals {
		args := append([]string{"prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved"}, flags...)
		refused := d.ctl(false, args...)
		if msg := asString(refused["error"]); !strings.Contains(msg, want) {
			t.Fatalf("prepare %v error = %q, want %q", flags, msg, want)
		}
	}
	entries, err := os.ReadDir(filepath.Join(d.home, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("task records = %d, want only the accepted tag-start task", len(entries))
	}
	for _, call := range herdrCalls(t, d.base)[herdrBefore:] {
		if len(call) >= 2 && call[0] == "worktree" && call[1] == "create" {
			t.Fatalf("a refused prepare still created a worktree: %v", call)
		}
	}
}

func readTaskJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var task map[string]any
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}
	return task
}

// Dispatch binds the worker registration to the occupant Herdr reports for the task's pane (its terminal and shell),
// so the pane's own init and every later delivery are judged against it.
func TestDispatchRecordsTheWorkerPanesIncarnation(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "incarnation-worker", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	id, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	pane := asString(task["pane"])
	registration := readJSON(t, sessionPath(d.home, id, pane))
	inc := asMap(registration["incarnation"])
	if registration["role"] != "worker" || inc["terminal"] != "term-"+pane || asMap(inc["shell"])["pid"] == nil {
		t.Fatalf("worker registration = %v, want the pane's terminal and shell recorded", registration)
	}
	if worker := d.ctlPane(pane, true, "init"); asString(worker["role"]) != "worker" || asString(asMap(worker["incarnation"])["outcome"]) != "same" {
		t.Fatalf("worker init = %v, want worker judged same", worker)
	}
}
