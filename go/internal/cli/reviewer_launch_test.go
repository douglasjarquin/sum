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
			if !strings.Contains(prompt, "context "+taskID+" --role reviewer") || !strings.Contains(prompt, "Do not edit the checkout") {
				t.Fatalf("reviewer prompt = %q, want bounded reviewer context and read-only instructions", prompt)
			}
			persisted := d.ctl(true, "show", taskID)
			if asMap(persisted["reviewer"])["pane"] != pane {
				t.Fatalf("saved task reviewer endpoint does not name %s: %#v", pane, persisted)
			}
			refused := d.ctl(false, "reviewer", "launch", taskID)
			if !strings.Contains(asString(refused["error"]), "sumctl bind TASK --reviewer-pane PANE") {
				t.Fatalf("second reviewer launch error = %v, want supported rebind route", refused)
			}
			d.ctlPane(pane, true, "review", taskID, "--verdict", "comment", "--candidate", asString(task["base_sha"]), "--text", "Inspected the current candidate.")
			persisted = d.ctl(true, "show", taskID)
			evidence := asSlice(asMap(persisted["evidence_view"])["records"])
			if len(evidence) != 1 || asMap(evidence[0])["source"] != "reviewer" {
				t.Fatalf("review from launched pane was not attributed to reviewer: %#v", evidence)
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

func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}
