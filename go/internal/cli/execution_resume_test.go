package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResumeLaunchesAtCurrentHEADAfterReportedCommit(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "resume-head", map[string]string{"README.md": "x\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "claude", "--arg", "existing", "--approved")
	id := asString(task["id"])
	worktree := asString(task["worktree"])
	pane := asString(task["pane"])
	attempt := asString(asMap(asMap(task["execution"])["worker"])["id"])
	if id == "" || worktree == "" || pane == "" || attempt == "" {
		t.Fatalf("dispatch = %v", task)
	}
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", worktree}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("config", "user.email", "test@example.invalid")
	git("config", "user.name", "sum test")
	git("commit", "--allow-empty", "-m", "worker candidate")
	head, err := exec.Command("git", "-C", worktree, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	base := asString(task["base_sha"])
	if strings.TrimSpace(string(head)) == base {
		t.Fatal("HEAD did not move past the dispatch base")
	}
	raw, err := os.ReadFile(filepath.Join(d.base, "fake", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	delete(state["panes"].(map[string]any), pane)
	out, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.base, "fake", "state.json"), out, 0o600); err != nil {
		t.Fatal(err)
	}
	parked := d.ctl(true, "execution", "park", id, "--attempt", attempt)
	if asString(parked["released"]) == "false" {
		t.Fatalf("park = %v", parked)
	}
	resumed := d.ctl(true, "execution", "resume", id, "--attempt", attempt)
	if errText := asString(resumed["error"]); strings.Contains(errText, "saved Git identity") {
		t.Fatalf("resume refused a reported checkout: %v", resumed)
	}
	if asString(resumed["status"]) != "running" && asString(resumed["id"]) == "" {
		t.Fatalf("resume = %v", resumed)
	}
	starts := agentStartCalls(t, d.base)
	if len(starts) < 2 {
		t.Fatalf("agent start calls = %d, want dispatch plus resume", len(starts))
	}
	want := []string{"existing", "--add-dir", filepath.Join(d.home, "tasks", id)}
	for i, start := range starts {
		passed, ok := argsAfterSeparator(start)
		if !ok || !reflect.DeepEqual(passed, want) {
			t.Fatalf("Claude start %d args = %v, want -- followed by %v", i+1, start, want)
		}
	}
	if got := startedArgv(resumed); !reflect.DeepEqual(got, want) {
		t.Fatalf("resumed launch started_argv = %v, want %v", got, want)
	}
}
