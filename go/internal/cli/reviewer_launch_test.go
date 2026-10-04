package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReviewerLaunchUsesTaskTabAndRecordsEndpoint(t *testing.T) {
	tests := []struct {
		name      string
		preset    bool
		wantAgent string
		wantArgv  []string
	}{
		{name: "coordinator harness fallback", wantAgent: "claude"},
		{name: "saved reviewer preset", preset: true, wantAgent: "grok", wantArgv: []string{"-m", "review-model", "--reasoning-effort", "high"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newPolicyLab(t)
			repo := policyProject(t, d.base, "reviewer-launch", map[string]string{"README.md": "A reviewer launch fixture.\n"})
			if tc.preset {
				d.ctl(true, "preset", "set", "review", "--harness", "grok", "--model", "review-model", "--reasoning", "high")
				d.ctl(true, "settings", "set", "--reviewer-preset", "review")
			}
			task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
			taskID := asString(task["id"])
			if taskID == "" {
				t.Fatalf("dispatch result has no task id: %#v", task)
			}
			denied := d.ctlPane(asString(task["pane"]), false, "reviewer", "launch", taskID)
			if !strings.Contains(asString(denied["error"]), "registered coordinator") {
				t.Fatalf("worker pane reviewer launch error = %#v, want coordinator refusal", denied)
			}
			launched := d.ctl(true, "reviewer", "launch", taskID)
			if launched["placement"] != "tab" || launched["harness"] != tc.wantAgent {
				t.Fatalf("launch result = %#v, want tab with harness %q", launched, tc.wantAgent)
			}
			bound := asMap(launched["reviewer"])
			if bound["cwd"] != task["worktree"] {
				t.Fatalf("reviewer cwd = %v, want task checkout %v", bound["cwd"], task["worktree"])
			}
			pane := asString(bound["pane"])
			if pane == "" {
				t.Fatal("launch result has no reviewer pane")
			}
			wantArgv := append([]string(nil), tc.wantArgv...)
			if tc.wantAgent == "claude" {
				wantArgv = []string{"--add-dir", filepath.Join(d.home, "tasks", taskID)}
			}
			if !reflect.DeepEqual(asSlice(bound["argv"]), stringsToAny(wantArgv)) {
				t.Fatalf("recorded reviewer argv = %v, want %v", bound["argv"], wantArgv)
			}
			if tc.wantAgent == "claude" {
				pinned := asString(bound["skill_path"])
				if !strings.HasPrefix(pinned, filepath.Join(d.home, "tasks", taskID, "procedure")+string(filepath.Separator)) {
					t.Fatalf("reviewer delivery skill path = %q, want task-local procedure file", pinned)
				}
				context := d.ctl(true, "context", taskID, "--role", "reviewer")
				environment := asMap(context["environment"])
				files := asSlice(asMap(environment["skills"])["files"])
				if len(files) != 1 || asString(asMap(files[0])["path"]) != pinned {
					t.Fatalf("reviewer context skill reference = %v, want pinned task-local path %q", files, pinned)
				}
			}
			var tabCreate []string
			for _, call := range herdrCalls(t, d.base) {
				if len(call) >= 2 && call[0] == "tab" && call[1] == "create" {
					tabCreate = call
				}
				if len(call) >= 2 && call[0] == "pane" && call[1] == "split" {
					t.Fatalf("reviewer launch split the worker pane: %v", call)
				}
			}
			want := []string{"tab", "create", "--workspace", asString(task["workspace"]), "--cwd", asString(task["worktree"]), "--label", "reviewer", "--no-focus"}
			if !reflect.DeepEqual(tabCreate, want) {
				t.Fatalf("tab create = %v, want %v", tabCreate, want)
			}
			starts := agentStartCalls(t, d.base)
			if len(starts) != 2 || starts[1][2] != "reviewer" || starts[1][4] != tc.wantAgent || starts[1][6] != pane {
				t.Fatalf("agent starts = %v, want reviewer %s in pane %s", starts, tc.wantAgent, pane)
			}
			if tc.preset {
				if !reflect.DeepEqual(starts[1][len(starts[1])-len(tc.wantArgv):], tc.wantArgv) {
					t.Fatalf("reviewer start argv = %v, want suffix %v", starts[1], tc.wantArgv)
				}
			}
			if tc.wantAgent == "claude" && !reflect.DeepEqual(starts[1][len(starts[1])-len(wantArgv):], wantArgv) {
				t.Fatalf("reviewer start argv = %v, want task grant suffix %v", starts[1], wantArgv)
			}
			stateRaw, err := os.ReadFile(filepath.Join(d.base, "fake", "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			var state struct {
				Panes map[string]map[string]any `json:"panes"`
			}
			if err := json.Unmarshal(stateRaw, &state); err != nil {
				t.Fatal(err)
			}
			if state.Panes[pane]["label"] != "reviewer" || state.Panes[pane]["cwd"] != task["worktree"] {
				t.Fatalf("reviewer pane fixture = %#v, want label reviewer and task checkout", state.Panes[pane])
			}
			prompt := asString(state.Panes[pane]["last_prompt"])
			for _, want := range []string{"context " + taskID + " --role reviewer", "do not edit the checkout", "Review candidate " + asString(task["base_sha"]), "review " + taskID + " --verdict"} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("reviewer prompt = %q, want %q", prompt, want)
				}
			}
			if tc.wantAgent == "claude" && !strings.Contains(prompt, asString(bound["skill_path"])) {
				t.Fatalf("reviewer prompt = %q, want the pinned delivery skill path", prompt)
			}
			if strings.Contains(prompt, "--policy-reviewed") {
				t.Fatalf("reviewer prompt = %q, want no policy guidance for a candidate without a policy change", prompt)
			}
			delivery := asMap(launched["prompt"])
			if delivery["state"] != "delivered" || delivery["pane"] != pane || launched["action"] != "launched" {
				t.Fatalf("launch prompt outcome = %#v action = %v, want delivered to %s", delivery, launched["action"], pane)
			}
			persisted := d.ctl(true, "show", taskID)
			if asMap(persisted["reviewer"])["pane"] != pane || asMap(asMap(persisted["reviewer"])["prompt"])["state"] != "delivered" {
				t.Fatalf("saved task reviewer endpoint does not name %s with a delivered prompt: %#v", pane, persisted["reviewer"])
			}
			d.ctlPane(pane, true, "review", taskID, "--verdict", "comment", "--candidate", asString(task["base_sha"]), "--text", "Inspected the current candidate.")
			persisted = d.ctl(true, "show", taskID)
			evidence := asSlice(asMap(persisted["evidence_view"])["records"])
			if len(evidence) != 1 || asMap(evidence[0])["source"] != "reviewer" {
				t.Fatalf("review from launched pane was not attributed to reviewer: %#v", evidence)
			}
			state.Panes[pane]["last_prompt"] = ""
			writeFakeState(t, d.base, state.Panes)
			again := d.ctl(true, "reviewer", "launch", taskID)
			if again["action"] != "prompted" || again["placement"] != "recorded-pane" || asMap(again["reviewer"])["pane"] != pane {
				t.Fatalf("re-review result = %#v, want the live recorded pane prompted", again)
			}
			state.Panes = readFakeState(t, d.base)
			if !strings.Contains(asString(state.Panes[pane]["last_prompt"]), "context "+taskID+" --role reviewer") {
				t.Fatalf("re-review did not prompt recorded pane %s: %#v", pane, state.Panes[pane]["last_prompt"])
			}
			if n := countCalls(herdrCalls(t, d.base), "tab", "create"); n != 1 {
				t.Fatalf("tab create calls = %d after re-review, want the single original tab", n)
			}
			if n := len(agentStartCalls(t, d.base)); n != 2 {
				t.Fatalf("agent start calls = %d after re-review, want no new reviewer start", n)
			}
			flagged := d.ctl(false, "reviewer", "launch", taskID, "--harness", "grok")
			if !strings.Contains(asString(flagged["error"]), "apply only to a new reviewer") {
				t.Fatalf("explicit flags against a recorded reviewer = %v, want refusal", flagged)
			}
			state.Panes[pane]["agent"] = nil
			state.Panes[pane]["agent_status"] = "idle"
			writeFakeState(t, d.base, state.Panes)
			refused := d.ctl(false, "reviewer", "launch", taskID)
			if !strings.Contains(asString(refused["error"]), "sumctl bind "+taskID+" --reviewer-pane PANE") || !strings.Contains(asString(refused["error"]), "agent_not_found") {
				t.Fatalf("launch with a stopped reviewer agent = %v, want supported rebind route", refused)
			}
			if n := countCalls(herdrCalls(t, d.base), "tab", "create"); n != 1 {
				t.Fatalf("tab create calls = %d after a gone reviewer, want no second tab", n)
			}
			state.Panes[pane]["agent"] = nil
			state.Panes[pane]["agent_status"] = "idle"
			stateRaw, err = json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(d.base, "fake", "state.json"), stateRaw, 0o600); err != nil {
				t.Fatal(err)
			}
			cleanupResult := d.ctl(true, "cleanup", taskID, "--reviewer-only", "--apply")
			stateRaw, err = os.ReadFile(filepath.Join(d.base, "fake", "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			state = struct {
				Panes map[string]map[string]any `json:"panes"`
			}{}
			if err := json.Unmarshal(stateRaw, &state); err != nil {
				t.Fatal(err)
			}
			if _, exists := state.Panes[pane]; exists {
				t.Fatalf("reviewer pane %s remains after reviewer-only cleanup: result=%#v pane=%#v calls=%v", pane, cleanupResult, state.Panes[pane], herdrCalls(t, d.base))
			}
			if _, err := os.Stat(asString(task["worktree"])); err != nil {
				t.Fatalf("reviewer-only cleanup removed the task checkout: %v", err)
			}
			persisted = d.ctl(true, "show", taskID)
			closed := asSlice(persisted["closed_panes"])
			if len(closed) != 1 || asMap(closed[0])["pane"] != pane || asMap(closed[0])["role"] != "reviewer" {
				t.Fatalf("reviewer closure was not durably recorded: %#v", closed)
			}
		})
	}
}

func TestReviewerLaunchFlagsOverrideSavedPreset(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "reviewer-flags", map[string]string{"README.md": "A reviewer flag fixture.\n"})
	d.ctl(true, "preset", "set", "review", "--harness", "grok", "--model", "review-model", "--reasoning", "high")
	d.ctl(true, "preset", "set", "alt", "--harness", "codex", "--model", "alt-model")
	d.ctl(true, "settings", "set", "--reviewer-preset", "review")
	cases := []struct {
		name     string
		flags    []string
		harness  string
		wantArgv []string
	}{
		{name: "harness model reasoning", flags: []string{"--harness", "grok", "--model", "flag-model", "--reasoning", "low"}, harness: "grok", wantArgv: []string{"-m", "flag-model", "--reasoning-effort", "low"}},
		{name: "preset", flags: []string{"--preset", "alt"}, harness: "codex", wantArgv: []string{"-m", "alt-model"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
			taskID := asString(task["id"])
			launched := d.ctl(true, append([]string{"reviewer", "launch", taskID}, tc.flags...)...)
			if launched["harness"] != tc.harness {
				t.Fatalf("launch harness = %v, want %s from explicit flags over the saved reviewer preset", launched["harness"], tc.harness)
			}
			argv := asSlice(asMap(launched["reviewer"])["argv"])
			if !reflect.DeepEqual(argv, stringsToAny(tc.wantArgv)) {
				t.Fatalf("recorded reviewer argv = %v, want %v", argv, tc.wantArgv)
			}
			starts := agentStartCalls(t, d.base)
			last := starts[len(starts)-1]
			if last[4] != tc.harness || !reflect.DeepEqual(last[len(last)-len(tc.wantArgv):], tc.wantArgv) {
				t.Fatalf("reviewer start = %v, want harness %s with argv %v", last, tc.harness, tc.wantArgv)
			}
		})
	}
}

func TestReviewerLaunchRefusesWhenWorkerWorkspaceIsGone(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "reviewer-workspace", map[string]string{"README.md": "A reviewer workspace fixture.\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	taskID := asString(task["id"])
	workspace := asString(task["workspace"])
	statePath := filepath.Join(d.base, "fake", "state.json")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	delete(asMap(state["workspaces"]), workspace)
	for pane, row := range asMap(state["panes"]) {
		if asMap(row)["workspace_id"] == workspace {
			delete(asMap(state["panes"]), pane)
		}
	}
	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	refused := d.ctl(false, "reviewer", "launch", taskID)
	message := asString(refused["error"])
	for _, want := range []string{"workspace " + workspace + " is gone", "workspace_not_found", "herdr worktree open --path " + asString(task["worktree"]) + " --no-focus", "sumctl bind " + taskID + " --worker-pane PANE"} {
		if !strings.Contains(message, want) {
			t.Fatalf("workspace-gone launch error = %q, want %q", message, want)
		}
	}
	if n := countCalls(herdrCalls(t, d.base), "tab", "create"); n != 0 {
		t.Fatalf("tab create calls = %d, want none when the worker workspace is gone", n)
	}
	if _, has := d.ctl(true, "show", taskID)["reviewer"].(map[string]any); has {
		t.Fatal("a reviewer endpoint was recorded although launch was refused")
	}
}

func TestReviewerLaunchPromptNamesPolicyReviewAndRecordsDeliveryFailure(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "reviewer-policy", map[string]string{"README.md": "A reviewer policy fixture.\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--harness", "codex", "--approved")
	taskID := asString(task["id"])
	candidate := asString(task["base_sha"])
	handoff := filepath.Join(d.base, "handoff.json")
	body, err := json.Marshal(map[string]any{
		"outcome": "completed", "candidate": candidate, "next_action": "review",
		"verification": map[string]any{"run_id": "20261004T000000Z-policy", "outcome": "pass", "record": "run.json", "candidate": candidate, "certifies": nil, "requires_root_review": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(handoff, body, 0o644); err != nil {
		t.Fatal(err)
	}
	d.ctlPane(asString(task["pane"]), true, "report", taskID, "--text", "policy candidate ready", "--handoff", handoff)
	d.setEnv("FAKE_FAIL_PROMPT", "1")
	launched := d.ctl(true, "reviewer", "launch", taskID)
	delivery := asMap(launched["prompt"])
	if delivery["state"] != "not-delivered" || !strings.Contains(asString(delivery["detail"]), "simulated uncertain prompt") {
		t.Fatalf("prompt outcome = %#v, want not-delivered with the Herdr detail", delivery)
	}
	pane := asString(asMap(launched["reviewer"])["pane"])
	persisted := asMap(d.ctl(true, "show", taskID)["reviewer"])
	if persisted["pane"] != pane || asMap(persisted["prompt"])["state"] != "not-delivered" {
		t.Fatalf("saved reviewer endpoint = %#v, want pane %s with a not-delivered prompt", persisted, pane)
	}
	if n := countCalls(herdrCalls(t, d.base), "agent", "prompt"); n != 2 {
		t.Fatalf("agent prompt calls = %d (worker plus reviewer), want exactly one reviewer attempt and no retry", n)
	}
	d.setEnv("FAKE_FAIL_PROMPT", "")
	again := d.ctl(true, "reviewer", "launch", taskID)
	if again["action"] != "prompted" || asMap(again["prompt"])["state"] != "delivered" {
		t.Fatalf("second launch = %#v, want the recorded pane prompted", again)
	}
	prompt := asString(readFakeState(t, d.base)[pane]["last_prompt"])
	for _, want := range []string{"review " + taskID + " --policy-reviewed", "line by line", "Review candidate " + candidate} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("policy reviewer prompt = %q, want %q", prompt, want)
		}
	}
}

func countCalls(calls [][]string, first, second string) int {
	n := 0
	for _, call := range calls {
		if len(call) >= 2 && call[0] == first && call[1] == second {
			n++
		}
	}
	return n
}

func readFakeState(t *testing.T, base string) map[string]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(base, "fake", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Panes map[string]map[string]any `json:"panes"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state.Panes
}

func writeFakeState(t *testing.T, base string, panes map[string]map[string]any) {
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
	state["panes"] = panes
	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}
