package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Fast coordination (init, status, inbox, pump, bind, hook enable, hook events) never reaches GitHub or applies
// cleanup, while outstanding maintenance stays listed with its exact command; only the explicit `sweep` observes the
// PR. The fake gh logs every call it receives.
func TestFastPathsNeverReconcileOrCleanUp(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "boundary", map[string]string{"README.md": "A project.\n"})
	open := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	merged := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	openID, mergedID := asString(open["id"]), asString(merged["id"])
	for _, task := range []map[string]any{open, merged} {
		d.ctlPane(asString(task["pane"]), true, "init")
		d.ctlPane(asString(task["pane"]), true, "ask", asString(task["id"]), "--key", "k", "--text", "Which way?")
	}
	boundaryPR(t, d, openID, "open")
	boundaryPR(t, d, mergedID, "merged")
	ghRoot := filepath.Join(d.base, "fake-gh")
	if err := os.MkdirAll(ghRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	scenario := `{"number": 7, "repository": "douglasjarquin/project", "state": "MERGED", "head_branch": "sum/x", "head_sha": "` + strings.Repeat("a", 40) + `", "merge_commit": "` + strings.Repeat("b", 40) + `"}`
	if err := os.WriteFile(filepath.Join(ghRoot, "pr.json"), []byte(scenario), 0o600); err != nil {
		t.Fatal(err)
	}
	ghCalls := func() int {
		raw, err := os.ReadFile(filepath.Join(ghRoot, "calls.jsonl"))
		if err != nil {
			return 0
		}
		return strings.Count(string(raw), "\n")
	}
	mergedWorktree := asString(merged["worktree"])

	// Read-only views change nothing under the state home, --live included.
	before := homeDigest(t, d.home)
	for _, args := range [][]string{{"status"}, {"inbox"}, {"status", "--live"}, {"inbox", "--live"}} {
		view := d.ctl(true, args...)
		assertMaintenance(t, args, view, openID, mergedID)
		if len(args) == 2 {
			if view["live"] != true || asInt(asMap(view["fanout"])["herdr_calls"]) != 1 {
				t.Fatalf("%v live=%v fanout=%v, want one Herdr call", args, view["live"], view["fanout"])
			}
			for _, row := range asSlice(view["tasks"]) {
				if asMap(row)["observed"] == nil {
					t.Fatalf("%v row without an observation: %v", args, row)
				}
			}
		}
	}
	if after := homeDigest(t, d.home); after != before {
		t.Fatal("a read-only view wrote under the state home")
	}

	initView := d.ctl(true, "init")
	assertMaintenance(t, []string{"init"}, initView, openID, mergedID)
	pumped := d.ctl(true, "pump")
	assertMaintenance(t, []string{"pump"}, pumped, openID, mergedID)
	if pumped["lifecycle"] != nil || initView["lifecycle"] != nil {
		t.Fatal("a fast path still reports a lifecycle sweep")
	}
	d.ctl(true, "bind", openID, "--parent-only")
	pluginID := asString(d.ctl(true, "hook", "enable")["plugin_id"])
	d.setEnv("FAKE_PARENT_STATUS", "idle")
	d.herdrEvent(pluginID, "w-parent:p1", "idle", "sum-test")
	for _, task := range []map[string]any{open, merged} {
		settleFakePane(t, d.base, asString(task["pane"]))
		d.herdrEvent(pluginID, asString(task["pane"]), "idle", "sum-test")
	}
	d.setEnv("FAKE_PARENT_STATUS", "working")

	if n := ghCalls(); n != 0 {
		raw, _ := os.ReadFile(filepath.Join(ghRoot, "calls.jsonl"))
		t.Fatalf("fast paths made %d gh call(s):\n%s", n, raw)
	}
	mergedTask := boundaryTask(t, d, mergedID)
	if mergedTask["status"] == "archived" || mergedTask["cleanup"] != nil {
		t.Fatalf("a fast path applied cleanup: status=%v cleanup=%v", mergedTask["status"], mergedTask["cleanup"])
	}
	if _, err := os.Stat(mergedWorktree); err != nil {
		t.Fatalf("merged task's checkout was removed by a fast path: %v", err)
	}
	if at := asMap(boundaryTask(t, d, openID)["pr"])["observed_at"]; at != "2026-01-02T00:00:00+00:00" {
		t.Fatalf("open PR observation was refreshed by a fast path: %v", at)
	}

	// The explicit maintenance command is the one that reaches GitHub.
	swept := d.ctl(true, "sweep")
	if ghCalls() == 0 {
		t.Fatalf("sweep made no gh call: %v", swept)
	}
	var actions []string
	for _, row := range asSlice(swept["rows"]) {
		actions = append(actions, asString(asMap(row)["task"])+":"+asString(asMap(row)["action"]))
	}
	sort.Strings(actions)
	if !containsString(actions, openID+":pr-observe") || !containsString(actions, mergedID+":cleanup") {
		t.Fatalf("sweep rows = %v", actions)
	}
}

func assertMaintenance(t *testing.T, args []string, view map[string]any, openID, mergedID string) {
	t.Helper()
	m := asMap(view["maintenance"])
	if m == nil {
		t.Fatalf("%v has no maintenance view: %v", args, view)
	}
	prs := asSlice(m["open_prs"])
	if len(prs) != 1 || asString(asMap(prs[0])["task"]) != openID || !strings.Contains(asString(asMap(prs[0])["next"]), "pr reconcile "+openID) {
		t.Fatalf("%v open_prs = %v", args, prs)
	}
	if asString(asMap(prs[0])["observed_at"]) != "2026-01-02T00:00:00+00:00" {
		t.Fatalf("%v open PR observed_at = %v", args, asMap(prs[0])["observed_at"])
	}
	cleanups := asSlice(m["cleanup"])
	if len(cleanups) != 1 || asString(asMap(cleanups[0])["task"]) != mergedID || !strings.Contains(asString(asMap(cleanups[0])["next"]), "cleanup "+mergedID) {
		t.Fatalf("%v cleanup = %v", args, cleanups)
	}
	if !strings.Contains(asString(m["next"]), "sweep") {
		t.Fatalf("%v next = %v", args, m["next"])
	}
}

func boundaryTask(t *testing.T, d *demoLab, id string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(d.home, "tasks", id, "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	var task map[string]any
	if err := json.Unmarshal(raw, &task); err != nil {
		t.Fatal(err)
	}
	return task
}

// boundaryPR records a verified PR identity for id: open, or merged for this task.
func boundaryPR(t *testing.T, d *demoLab, id, state string) {
	t.Helper()
	task := boundaryTask(t, d, id)
	pr := map[string]any{
		"complete": true, "merged_for_task": state == "merged", "state": state, "observed_at": "2026-01-02T00:00:00+00:00",
		"identity": map[string]any{"number": 7, "url": "https://github.com/douglasjarquin/project/pull/7",
			"head_sha": strings.Repeat("a", 40), "head_branch": asString(task["branch"]), "base_branch": "main"},
		"findings": []any{},
	}
	if state == "merged" {
		pr["merge_commit"] = strings.Repeat("b", 40)
	}
	task["pr"] = pr
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.home, "tasks", id, "task.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// homeDigest hashes every regular file under home (path and content).
func homeDigest(t *testing.T, home string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !entry.Type().IsRegular() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%x\x00", strings.TrimPrefix(path, home), sha256.Sum256(raw))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
