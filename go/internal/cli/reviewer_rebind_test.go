package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func reviewerRebindLab(t *testing.T) (*demoLab, string, string) {
	t.Helper()
	d, taskID := reviewLab(t)
	reviewer := launchNativeReviewer(t, d, taskID)
	address := workerAddress(reviewer)
	d.ctlPane(address, true, "review", taskID, "--verdict", "changes-requested", "--candidate", reviewCandidateFor(d, taskID), "--text", "first independent review")
	d.native.Exit(address, 0)
	return d, taskID, asString(d.taskRecord(taskID)["worktree"])
}

func successorReviewer(t *testing.T, d *demoLab, taskID string) store.Endpoint {
	t.Helper()
	old := asMap(d.taskRecord(taskID)["reviewer"])
	raw, _ := json.Marshal(old)
	var endpoint store.Endpoint
	if err := json.Unmarshal(raw, &endpoint); err != nil {
		t.Fatal(err)
	}
	endpoint.Address = "t700"
	endpoint.CreatedMS = 700
	endpoint.BindingGeneration++
	previous := nativeReviewerTerminal(t, d, asString(old["address"]))
	pid := uint32(390000)
	foreground := &hcclient.Foreground{PID: 390001, Name: "codex", Argv: []string{"codex", "--no-daemon"}, Cwd: endpoint.Cwd}
	previous.Address = endpoint.Address
	previous.CreatedMS = endpoint.CreatedMS
	previous.State = "live"
	previous.PID = &pid
	previous.Foreground = foreground
	previous.Status = &hcclient.Status{State: "idle"}
	previous.FinishedMS = nil
	previous.ExitCode = nil
	previous.Reason = nil
	d.native.SetTerminal(previous)
	s := nativeTestStore(t, d.home)
	binding := (incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: foreground}).Record(store.Now())
	registration, err := s.Register(endpoint, "reviewer", taskID, binding)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: "reviewer-init", Attempt: endpoint.Attempt})
	registration.Set("native_receipt", receipt)
	template, _ := ordjson.FromValue(old["native_template"])
	registration.Set("native_template", template)
	if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(endpoint)+".json"), registration); err != nil {
		t.Fatal(err)
	}
	return endpoint
}

func TestBindReviewerTerminalPreservesReviewIsolationAndAssignsFreshKickoff(t *testing.T) {
	d, taskID, worktree := reviewerRebindLab(t)
	old := asMap(d.taskRecord(taskID)["reviewer"])
	successor := successorReviewer(t, d, taskID)
	before := nativeRunCount(d)
	out := d.ctl(true, "bind", taskID, "--reviewer-terminal", successor.Address)
	reviewer := asMap(out["reviewer"])
	if reviewer["address"] != successor.Address || reviewer["binding_generation"] != float64(successor.BindingGeneration) || asMap(reviewer["incarnation"]) == nil {
		t.Fatalf("successor identity was not bound: %v", reviewer)
	}
	if reviewer["prompt"] != nil || reviewer["native_launch"] != nil || asMap(reviewer["kickoff"])["state"] != "pending" {
		t.Fatalf("replacement inherited application input or fake launch: %v", reviewer)
	}
	found := false
	for _, raw := range asSlice(out["evidence"]) {
		row := asMap(raw)
		if row["kind"] == "native-rebind" {
			found = true
			if asMap(row["from"])["address"] != old["address"] || asMap(row["to"])["address"] != successor.Address || row["source"] != "coordinator" {
				t.Fatalf("rebind evidence lost endpoints: %v", row)
			}
		}
	}
	if !found {
		t.Fatal("native rebind evidence missing")
	}
	d.ctlPane(successor.Address, true, "init", "--role", "reviewer", "--task", taskID)
	denied := d.ctlPane(successor.Address, false, "review", taskID, "--verdict", "approve", "--candidate", reviewCandidateFor(d, taskID), "--text", "cannot inherit old findings")
	if !strings.Contains(asString(denied["error"]), "candidate") {
		t.Fatalf("unassigned successor could approve old candidate: %v", denied)
	}
	preview := d.ctl(false, "cleanup", taskID, "--reviewer-only")
	previewBytes, _ := json.Marshal(preview)
	if asMap(preview["reviewer"])["closable"] == true || !strings.Contains(string(previewBytes), "no findings for current reviewer endpoint") {
		t.Fatalf("prior reviewer findings made successor closable or were not distinguished: %v", preview)
	}
	kicked := d.ctl(true, "reviewer", "launch", taskID)
	if asMap(kicked["prompt"])["state"] != "submitted" || nativeRunCount(d) != before || len(d.native.Inputs(successor.Address)) != 1 {
		t.Fatalf("bound successor kickoff not delivered exactly once without Run: %v", kicked)
	}
	for _, want := range []string{reviewCandidateFor(d, taskID), asString(reviewer["skill_path"]), "do not edit the checkout"} {
		if !strings.Contains(d.native.Inputs(successor.Address)[0], want) {
			t.Fatalf("successor pointer missing %q", want)
		}
	}
	fresh := d.ctlPane(successor.Address, true, "review", taskID, "--verdict", "approve", "--candidate", reviewCandidateFor(d, taskID), "--text", "successor's independent review")
	if asMap(asMap(fresh["evidence"])["endpoint"])["address"] != successor.Address || asMap(asMap(fresh["evidence"])["endpoint"])["binding_generation"] != float64(successor.BindingGeneration) {
		t.Fatalf("new findings attributed to retired reviewer: %v", fresh)
	}
	current := d.taskRecord(taskID)
	if asMap(asMap(current["reviewer"])["kickoff"])["state"] != "applied" {
		t.Fatal("matching successor review did not discharge kickoff")
	}
	d.native.Exit(successor.Address, 0)
	cleanup := d.ctl(true, "cleanup", taskID, "--reviewer-only", "--apply")
	if cleanup["state"] != "reviewer-closed" {
		t.Fatalf("settled reviewer-only cleanup: %v", cleanup)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatal("reviewer cleanup removed task checkout")
	}
	if workerAddress(d.taskRecord(taskID)) != workerAddress(out) {
		t.Fatal("reviewer cleanup replaced worker endpoint")
	}
	workerAlive := false
	for _, terminal := range d.native.Terminals() {
		if terminal.Address == successor.Address {
			t.Fatal("settled successor terminal remains after reviewer-only cleanup")
		}
		if terminal.Address == workerAddress(out) {
			workerAlive = terminal.State == "live"
		}
	}
	if !workerAlive {
		t.Fatal("reviewer-only cleanup stopped or removed worker")
	}
	if reviewCandidateFor(d, taskID) != asString(asMap(fresh["evidence"])["candidate"]) {
		t.Fatal("reviewer-only cleanup changed candidate checkout")
	}

}

func TestBindReviewerTerminalRefusesLiveRecordedReviewer(t *testing.T) {
	d, taskID := reviewLab(t)
	launchNativeReviewer(t, d, taskID)
	successor := successorReviewer(t, d, taskID)
	before := d.taskRecord(taskID)["reviewer"]
	out := d.ctl(false, "bind", taskID, "--reviewer-terminal", successor.Address)
	if !strings.Contains(asString(out["error"]), "live or replaced") {
		t.Fatalf("live recorded reviewer replaced: %v", out)
	}
	if asMap(d.taskRecord(taskID)["reviewer"])["address"] != asMap(before)["address"] {
		t.Fatal("failed bind mutated reviewer")
	}
}

func TestReviewRefusalRequiresExplicitNativeReviewerBinding(t *testing.T) {
	d, taskID, _ := reviewerRebindLab(t)
	successor := successorReviewer(t, d, taskID)

	env := endpointEnv(successor)
	for key, value := range env {
		d.setEnv(key, value)
	}
	out := d.ctl(false, "review", taskID, "--verdict", "approve", "--candidate", reviewCandidateFor(d, taskID), "--text", "not bound")
	if !strings.Contains(asString(out["error"]), "explicit reviewer binding") {
		t.Fatalf("unbound successor adopted authority: %v", out)
	}
}

func TestBindReviewerTerminalRefusesWorkerTerminal(t *testing.T) {
	d, taskID, _ := reviewerRebindLab(t)
	worker := workerAddress(d.taskRecord(taskID))
	d.ctlPane(worker, true, "init")
	out := d.ctl(false, "bind", taskID, "--reviewer-terminal", worker)
	if !strings.Contains(asString(out["error"]), "registration identity mismatch") {
		t.Fatalf("worker adopted independent reviewer role: %v", out)
	}
}

func TestBindReviewerTerminalRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testing.T, *demoLab, string, store.Endpoint)
		want   string
	}{
		{"same terminal", nil, "replacement terminal must differ"},
		{"missing receipt", func(t *testing.T, d *demoLab, id string, e store.Endpoint) {
			s := nativeTestStore(t, d.home)
			path := filepath.Join(s.Sessions, store.RegistrationKey(e)+".json")
			raw, err := ordjson.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			raw.(*ordjson.Object).Delete("native_receipt")
			if err = ordjson.WriteFile(path, raw); err != nil {
				t.Fatal(err)
			}
		}, "process/receipt authority"},
		{"another core", func(t *testing.T, d *demoLab, id string, e store.Endpoint) {
			editTaskObject(t, d.home, id, func(task map[string]any) { asMap(task["reviewer"])["core_id"] = "another-core" })
		}, "core identity"},
		{"recorded terminal unknown", func(t *testing.T, d *demoLab, id string, e store.Endpoint) {
			old := asMap(d.taskRecord(id)["reviewer"])
			terminal := nativeReviewerTerminal(t, d, asString(old["address"]))
			terminal.State = "unknown"
			d.native.SetTerminal(terminal)
		}, "live or replaced"},
		{"replacement process changed", func(t *testing.T, d *demoLab, id string, e store.Endpoint) {
			terminal := nativeReviewerTerminal(t, d, e.Address)
			terminal.Foreground.PID++
			d.native.SetTerminal(terminal)
		}, "foreground process changed"},
		{"replacement cwd differs", func(t *testing.T, d *demoLab, id string, e store.Endpoint) {
			terminal := nativeReviewerTerminal(t, d, e.Address)
			terminal.Cwd = "/tmp/not-the-checkout"
			d.native.SetTerminal(terminal)
		}, "cwd differs"},
		{"surviving checkout writer", func(t *testing.T, d *demoLab, id string, e store.Endpoint) { startReviewerCheckoutWriter(t, d, id) }, "writers"},
		{"native observation unavailable", func(t *testing.T, d *demoLab, id string, e store.Endpoint) {
			d.native.QueueFault("terminal.get", hctest.Fault{RefuseKind: "server_busy"})
		}, "server_busy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, id, _ := reviewerRebindLab(t)
			e := successorReviewer(t, d, id)
			address := e.Address
			if tc.name == "same terminal" {
				address = asString(asMap(d.taskRecord(id)["reviewer"])["address"])
			}
			if tc.mutate != nil {
				tc.mutate(t, d, id, e)
			}
			before := nativeRunCount(d)
			out := d.ctl(false, "bind", id, "--reviewer-terminal", address)
			if !strings.Contains(asString(out["error"]), tc.want) {
				t.Fatalf("bind refusal=%v want %q", out, tc.want)
			}
			if nativeRunCount(d) != before {
				t.Fatal("failed bind created terminal")
			}
		})
	}
}

func readTaskObject(t *testing.T, home, taskID string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "tasks", taskID, "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	var task map[string]any
	if err := json.Unmarshal(raw, &task); err != nil {
		t.Fatal(err)
	}
	return task
}

func editTaskObject(t *testing.T, home, taskID string, edit func(map[string]any)) {
	t.Helper()
	task := readTaskObject(t, home, taskID)
	edit(task)
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "tasks", taskID, "task.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func editFakePane(t *testing.T, d *demoLab, pane string, edit func(map[string]any)) {
	t.Helper()
	path := filepath.Join(d.base, "fake", "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	edit(asMap(asMap(state["panes"])[pane]))
	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func setReviewerPane(t *testing.T, base, pane, cwd string, agent map[string]any, shell int) {
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
	panes := asMap(state["panes"])
	row := asMap(panes[pane])
	if row == nil {
		row = map[string]any{"pane_id": pane, "workspace_id": strings.Split(pane, ":")[0], "terminal_id": "term-" + pane}
		panes[pane] = row
	}
	row["cwd"] = cwd
	row["shell_pid"] = shell
	row["processes"] = []any{}
	if agent == nil {
		row["agent"] = nil
		row["agent_status"] = "unknown"
		delete(row, "agent_pid")
	} else {
		for key, value := range agent {
			row[key] = value
		}
	}
	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReviewerRebindKickoffUncertainInputIsNotReplayed(t *testing.T) {
	d, id, _ := reviewerRebindLab(t)
	e := successorReviewer(t, d, id)
	d.ctl(true, "bind", id, "--reviewer-terminal", e.Address)
	before := nativeRunCount(d)
	d.native.QueueFault("terminal.send", hctest.Fault{LoseResponse: true})
	out := d.ctl(true, "reviewer", "launch", id)
	if asMap(out["prompt"])["state"] != "uncertain" || asMap(asMap(d.taskRecord(id)["reviewer"])["kickoff"])["state"] != "uncertain" {
		t.Fatalf("successor kickoff lost send ambiguity: %v", out)
	}
	again := d.ctl(false, "reviewer", "launch", id)
	if asString(again["error"]) == "" || len(d.native.Inputs(e.Address)) != 1 || nativeRunCount(d) != before {
		t.Fatalf("uncertain successor kickoff replayed or launched: %v", again)
	}
}

func TestReviewerRebindKickoffCannotTransferToChangedBinding(t *testing.T) {
	d, id, _ := reviewerRebindLab(t)
	e := successorReviewer(t, d, id)
	d.ctl(true, "bind", id, "--reviewer-terminal", e.Address)
	editTaskObject(t, d.home, id, func(task map[string]any) {
		endpoint := asMap(asMap(asMap(task["reviewer"])["kickoff"])["endpoint"])
		endpoint["binding_generation"] = float64(e.BindingGeneration + 1)
	})
	before := nativeRunCount(d)
	out := d.ctl(false, "reviewer", "launch", id)
	if !strings.Contains(asString(out["error"]), "successor authority") || len(d.native.Inputs(e.Address)) != 0 || nativeRunCount(d) != before {
		t.Fatalf("kickoff authority transferred by edited binding: %v", out)
	}
}
