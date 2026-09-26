package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/factoryview/fixture"
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
