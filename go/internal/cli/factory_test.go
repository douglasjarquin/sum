package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/factory"
	"github.com/douglasjarquin/sum/go/internal/factoryview/fixture"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pipeline"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestFactoryStatus_pinsStdoutAcrossScenarios(t *testing.T) {
	t.Run("no factory registry", func(t *testing.T) {
		home := t.TempDir()
		assertStdoutGolden(t, home, []string{"factory", "status"}, scenarioGolden(t))
	})
}

func TestFactoryEnable_requiresCoordinator(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	t.Setenv("HERDR_PANE_ID", "w-other:p1")
	_, stderr, err := runFactory(t, home, "factory", "enable", "owner/repo")
	if err == nil {
		t.Fatalf("expected coordinator requirement, stderr=%s", stderr)
	}
	if !strings.Contains(err.Error(), "not the registered coordinator") {
		t.Fatalf("err = %v", err)
	}
}

func TestFactoryClaim_requiresCoordinator(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	t.Setenv("HERDR_PANE_ID", "w-other:p1")
	_, stderr, err := runFactory(t, home, "factory", "claim", "owner/repo", "--issue", "1")
	if err == nil {
		t.Fatalf("expected coordinator requirement, stderr=%s", stderr)
	}
	if !strings.Contains(err.Error(), "not the registered coordinator") {
		t.Fatalf("err = %v", err)
	}
}

func TestFactoryMerge_requiresCoordinator(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	t.Setenv("HERDR_PANE_ID", "w-other:p1")
	_, stderr, err := runFactory(t, home, "factory", "merge", "t-aaaaaaaaaaaa")
	if err == nil {
		t.Fatalf("expected coordinator requirement, stderr=%s", stderr)
	}
	if !strings.Contains(err.Error(), "not the registered coordinator") {
		t.Fatalf("err = %v", err)
	}
}

func TestFactoryMergeCommand_rechecksLiveGatesAfterUnknown(t *testing.T) {
	const taskID = "t-bbbbbbbbbbbb"
	const repo = "cofactorworks/nicebaas"
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	if _, err := runCLI(t, home, "init"); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := store.Context(home)
	if err != nil {
		t.Fatal(err)
	}
	projectRegistry := map[string]any{"schema": 1, "projects": map[string]any{repo: map[string]any{
		"name": repo, "host": "github.com", "owner": "cofactorworks", "repo": "nicebaas", "kind": "managed",
		"path": home, "remote": "https://github.com/cofactorworks/nicebaas.git", "canonical_path": home,
	}}}
	registryRaw, err := json.Marshal(projectRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "projects.json"), registryRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := factory.Enable(st, ctx, factory.EnableArgs{Project: repo}); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(home, "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", worktree}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init", "--quiet")
	if err := os.WriteFile(filepath.Join(worktree, "README"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README")
	runGit("commit", "--quiet", "-m", "fixture")
	candidate := runGit("rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Join(worktree, ".artifacts", "evidence", "r1", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	comparison := map[string]any{"schema": 1, "scenario": "demo", "verdict": "red-green", "candidate": map[string]any{"sha": candidate}}
	comparisonRaw, err := json.Marshal(comparison)
	if err != nil {
		t.Fatal(err)
	}
	comparisonRel := filepath.Join(".artifacts", "evidence", "r1", "demo", "comparison.json")
	if err := os.WriteFile(filepath.Join(worktree, comparisonRel), comparisonRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", json.Number("1"))
	task.Set("id", taskID)
	task.Set("status", "running")
	task.Set("brief", "approved task brief")
	task.Set("worktree", worktree)
	task.Set("candidate", candidate)
	task.Set("repository", worktree)
	policy := ordjson.NewObject()
	projectIdentity := ordjson.NewObject()
	projectIdentity.Set("owner", "cofactorworks")
	projectIdentity.Set("repo", "nicebaas")
	projectIdentity.Set("name", repo)
	policy.Set("project_identity", projectIdentity)
	policy.Set("status", "not-yet-standardized")
	task.Set("verification_policy", policy)
	prIdentity := ordjson.NewObject()
	prIdentity.Set("number", json.Number("12"))
	prIdentity.Set("url", "https://github.com/cofactorworks/nicebaas/pull/12")
	prIdentity.Set("head_sha", candidate)
	prIdentity.Set("head_branch", "sum/"+taskID)
	prIdentity.Set("base_branch", "main")
	pr := ordjson.NewObject()
	pr.Set("identity", prIdentity)
	pr.Set("state", "open")
	pr.Set("complete", false)
	pr.Set("merged_for_task", false)
	pr.Set("findings", []any{})
	task.Set("pr", pr)
	appendEvidence := func(kind, source string) *ordjson.Object {
		row := ordjson.NewObject()
		row.Set("kind", kind)
		row.Set("source", source)
		row.Set("candidate", candidate)
		return row
	}
	handoff := appendEvidence("handoff", "worker")
	handoff.Set("current", true)
	handoff.Set("artifacts", []any{filepath.ToSlash(comparisonRel)})
	verification := appendEvidence("verification", "coordinator")
	verification.Set("current", true)
	verification.Set("result", "pass")
	verification.Set("outcome", "pass")
	verification.Set("certifies", candidate)
	review := appendEvidence("review", "reviewer")
	review.Set("current", true)
	review.Set("verdict", "approve")
	documentation := appendEvidence("documentation", "coordinator")
	documentation.Set("result", "pass")
	documentation.Set("summary", "Passed")
	rebase := appendEvidence("rebase", "coordinator")
	rebase.Set("outcome", "up-to-date")
	lint := appendEvidence("lint", "coordinator")
	lint.Set("outcome", "pass")
	ci := appendEvidence("ci", "coordinator")
	ci.Set("head_sha", candidate)
	ci.Set("outcome", "pass")
	ci.Set("summary", "Passed 1/1 required checks")
	task.Set("evidence", []any{handoff, verification, review, documentation, rebase, lint, ci})
	if err := os.MkdirAll(filepath.Join(home, "tasks", taskID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.Refresh(st, taskID); err != nil {
		t.Fatal(err)
	}
	fakeRoot := t.TempDir()
	t.Setenv("FAKE_GH_ROOT", fakeRoot)
	fakeGh := filepath.Join(fakeRoot, "gh")
	fakeScript := `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_GH_ROOT/calls.jsonl"
case "$1 $2" in
  "issue edit"|"issue comment") exit 0 ;;
  "pr view")
    count=0
    [ ! -f "$FAKE_GH_ROOT/views" ] || count=$(cat "$FAKE_GH_ROOT/views")
    printf '%s' "$((count + 1))" > "$FAKE_GH_ROOT/views"
    mergeable=MERGEABLE
    [ "$count" -ne 0 ] || mergeable=UNKNOWN
    state=CLEAN
    [ "$mergeable" != UNKNOWN ] || state=UNKNOWN
    printf '{"number":12,"url":"https://github.com/cofactorworks/nicebaas/pull/12","state":"OPEN","headRefName":"sum/t-bbbbbbbbbbbb","headRefOid":"%s","baseRefName":"main","isDraft":false,"mergeable":"%s","mergeStateStatus":"%s"}\n' "$CANDIDATE" "$mergeable" "$state"
    ;;
  "pr checks") printf '[{"name":"unit","state":"SUCCESS","bucket":"pass"}]\n' ;;
  "pr ready") printf 'ready\n' ;;
  "pr merge") printf 'merged\n' ;;
  *) printf 'unexpected fake gh call: %s\n' "$*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(fakeGh, []byte(fakeScript), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_GH_BIN", fakeGh)
	t.Setenv("CANDIDATE", candidate)
	if _, err := factory.Claim(st, ctx, home, factory.ClaimArgs{Project: repo, Issue: 12, Task: taskID}); err != nil {
		t.Fatal(err)
	}
	mergeCheck, err := factory.MergeCheck(st, taskID)
	if err != nil {
		t.Fatalf("merge check setup: %v", err)
	}
	confidence, _ := mergeCheck.Get("confidence")
	if confidence != "high" {
		t.Fatalf("test setup did not authorize merge: check=%v err=%v", mergeCheck, err)
	}
	stdout, err := runCLI(t, home, "--format", "json", "factory", "merge", taskID)
	if err != nil {
		t.Fatalf("factory merge: %v\n%s", err, stdout)
	}
	view := decodeCLIMap(t, stdout)
	if view["merged"] != true {
		t.Fatalf("factory merge result = %v", view)
	}
	calls, err := os.ReadFile(filepath.Join(fakeRoot, "calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(calls)
	if strings.Count(text, "pr view") != 3 || strings.Count(text, "pr checks") != 3 || strings.Count(text, "pr merge") != 1 {
		t.Fatalf("factory merge did not retry with fresh PR and check observations before one merge: %s", text)
	}
	if !strings.Contains(text, "pr merge 12 --repo cofactorworks/nicebaas --squash --match-head-commit "+candidate) || strings.Index(text, "pr ready") > strings.LastIndex(text, "pr view") {
		t.Fatalf("factory merge lost ready ordering or candidate pin: %s", text)
	}
}

func runFactory(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	out, runErr := runCLIForGolden(t, home, args)
	return out, "", runErr
}

func digestHome(t *testing.T) string {
	t.Helper()
	clearHerdrEnv(t)
	home := t.TempDir()
	if _, err := fixture.WriteStandard(home); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_GH_ROOT", filepath.Join(home, "fake-gh"))
	t.Setenv("FAKE_HERDR_ROOT", filepath.Join(home, "fake-herdr"))
	return home
}

func digestOf(t *testing.T, view map[string]any) map[string]any {
	t.Helper()
	digest, ok := view["digest"].(map[string]any)
	if !ok {
		t.Fatalf("no digest section: %v", view)
	}
	return digest
}

func TestFactoryStatusDigest_readsSavedFactsOnly(t *testing.T) {
	home := digestHome(t)
	before := snapshotPresentationFiles(t, home)
	view := compactRead(t, home, "factory", "status")
	projects := view["projects"].([]any)
	if len(projects) != 2 || projects[0].(map[string]any)["held"] != float64(1) || view["note"] == nil {
		t.Fatalf("existing projects contract changed: %v", view)
	}
	digest := digestOf(t, view)
	rows := digest["rows"].([]any)
	byKey := map[string]map[string]any{}
	for _, raw := range rows {
		row := raw.(map[string]any)
		byKey[row["project"].(string)] = row
	}
	if byKey["a/repo"]["current_issue"] != "41" || byKey["a/repo"]["lane"] != "held" || byKey["b/repo"]["action_owner"] != "human decision" {
		t.Fatalf("rows: %v", rows)
	}
	if byKey["b/repo"]["blocker"].(map[string]any)["ref"] != "question:q-compat" {
		t.Fatalf("blocker: %v", byKey["b/repo"]["blocker"])
	}
	if _, has := byKey["ghe.example.com/b/repo"]; has {
		t.Fatal("factory status lists factory projects only")
	}
	cursor, _ := digest["cursor"].(string)
	if cursor == "" || digest["since"] != false || digest["resync"] != nil || digest["counts"].(map[string]any)["decisions"] != float64(1) {
		t.Fatalf("envelope: %v", digest)
	}
	again := digestOf(t, compactRead(t, home, "factory", "status", "--since", cursor))
	if again["since"] != true || again["resync"] != nil || len(again["deltas"].([]any)) != 0 {
		t.Fatalf("unchanged read: %v", again)
	}
	for _, raw := range again["rows"].([]any) {
		for _, o := range raw.(map[string]any)["outcomes"].([]any) {
			if o.(map[string]any)["new"] == true {
				t.Fatalf("unchanged records labelled new: %v", o)
			}
		}
	}
	scoped := digestOf(t, compactRead(t, home, "factory", "status", "--project", "a/repo", "--limit", "3"))
	if scoped["project"] != "a/repo" || len(scoped["rows"].([]any)) != 1 || scoped["page"].(map[string]any)["limit"] != float64(3) || scoped["page"].(map[string]any)["continuation"] != true {
		t.Fatalf("scoped page: %v", scoped)
	}
	if foreign := digestOf(t, compactRead(t, home, "factory", "status", "--since", scoped["cursor"].(string))); foreign["resync"] == nil {
		t.Fatalf("a project-bound cursor on the global read resyncs: %v", foreign)
	}
	if after := snapshotPresentationFiles(t, home); !reflect.DeepEqual(before, after) {
		t.Fatal("digest reads changed the home")
	}
	for _, log := range []string{filepath.Join(home, "fake-gh", "calls.jsonl"), filepath.Join(home, "fake-herdr", "calls.jsonl")} {
		if _, err := os.Stat(log); !os.IsNotExist(err) {
			t.Fatalf("a read called a helper: %s", log)
		}
	}
	for _, args := range [][]string{{"factory", "status", "--limit", "0"}, {"factory", "status", "--limit", "101"}} {
		if _, _, err := runStatus(t, home, args...); err == nil || !strings.Contains(err.Error(), "--limit") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

// AE4: a project focus limits routine detail; the global decision count, its route and the other project's
// obligations stay exactly as they are.
func TestGroupedProjectFocusAttachesDigestAndKeepsGlobalDecisions(t *testing.T) {
	home := digestHome(t)
	before := snapshotPresentationFiles(t, home)
	for _, command := range [][]string{{"status", "--grouped"}, {"inbox", "--grouped"}} {
		view := compactRead(t, home, append(command, "--project", "a/repo")...)
		counts := view["counts"].(map[string]any)
		needs := view["needs_you"].([]any)
		if counts["decisions"] != float64(1) || len(needs) != 1 || needs[0].(map[string]any)["project"] != "b/repo" || needs[0].(map[string]any)["detail"].([]any)[0] != "context" {
			t.Fatalf("%v: global decision and route: %v", command, view)
		}
		groups := view["groups"].([]any)
		if len(groups) != 1 || groups[0].(map[string]any)["key"] != "a/repo" {
			t.Fatalf("%v: focus: %v", command, groups)
		}
		row := groups[0].(map[string]any)["digest"].(map[string]any)
		if row["current_issue"] != "41" || row["stage"] == "" || row["action_owner"] == nil {
			t.Fatalf("%v: group digest: %v", command, row)
		}
		digest := digestOf(t, view)
		if digest["project"] != "a/repo" || len(digest["rows"].([]any)) != 1 || digest["counts"].(map[string]any)["decisions"] != float64(1) {
			t.Fatalf("%v: digest: %v", command, digest)
		}
		cursor := digest["cursor"].(string)
		next := compactRead(t, home, append(command, "--project", "a/repo", "--since", cursor, "--limit", "5")...)
		if d := digestOf(t, next); d["since"] != true || d["resync"] != nil || d["page"].(map[string]any)["limit"] != float64(5) {
			t.Fatalf("%v: since: %v", command, d)
		}
		all := compactRead(t, home, command...)
		if len(all["groups"].([]any)) != 3 || all["groups"].([]any)[0].(map[string]any)["digest"] == nil {
			t.Fatalf("%v: every project row carries its digest without --project: %v", command, all)
		}
	}
	if after := snapshotPresentationFiles(t, home); !reflect.DeepEqual(before, after) {
		t.Fatal("grouped digest reads changed the home")
	}
	// Other projects' obligations remain open for delivery: the pump still lists b/repo's question.
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	task, err := st.ReadTask("t-b1bbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	obligations, err := returns.OpenObligations(st, task)
	if err != nil {
		t.Fatal(err)
	}
	open := false
	for _, obligation := range obligations {
		if kind, _ := obligation.Get("kind"); kind == "question" {
			open = true
		}
	}
	if !open {
		t.Fatal("b/repo's decision must stay deliverable after an a/repo focus")
	}
	for _, args := range [][]string{{"status", "--since", "x"}, {"inbox", "--since", "x"}, {"status", "--grouped", "--after", "1"}, {"status", "--compact", "--since", "x"}} {
		if _, stdout, err := runStatus(t, home, args...); err == nil || stdout != "" {
			t.Fatalf("%v must be a usage error: %v", args, err)
		}
	}
	compact := compactRead(t, home, "status", "--compact")
	if _, has := compact["digest"]; has {
		t.Fatal("compact output is unchanged")
	}
}

// TestFactoryMergeCheck_emitsHumanGateForAnUnknownTask pins the CLI emit of merge-check: its result carries the
// authorized repository list, which must encode. Before this test the command always exited 1 after evaluating the
// gates, and `factory merge` would have mutated GitHub before failing to report.
func TestFactoryMergeCheck_emitsHumanGateForAnUnknownTask(t *testing.T) {
	clearHerdrEnv(t)
	home := presentationHome(t)
	out, stderr, err := runFactory(t, home, "--format", "json", "factory", "merge-check", "t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("merge-check must emit its result: %v %s", err, stderr)
	}
	view := decodeCLIMap(t, out)
	if view["confidence"] != "human-gate" {
		t.Fatalf("confidence = %v, want human-gate for a task without evidence", view["confidence"])
	}
	if list, ok := view["authorized_merge"].([]any); !ok || len(list) == 0 {
		t.Fatalf("authorized_merge must encode as a list: %v", view["authorized_merge"])
	}
}
