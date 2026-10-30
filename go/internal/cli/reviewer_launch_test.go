package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/launch"
)

func launchNativeReviewer(t *testing.T, d *demoLab, taskID string, flags ...string) map[string]any {
	t.Helper()
	sent := true
	d.native.QueueFault("terminal.run", hctest.Fault{FirstMessage: &hcclient.FirstMessage{Sent: true, Started: &sent}})
	out := d.ctl(true, append([]string{"reviewer", "launch", taskID}, flags...)...)
	reviewer := asMap(out["reviewer"])
	address := workerAddress(reviewer)
	if address == "" {
		t.Fatalf("reviewer endpoint missing: %v", out)
	}
	d.ctlPane(address, true, "init")
	return d.taskRecord(taskID)["reviewer"].(map[string]any)
}
func nativeReviewerTerminal(t *testing.T, d *demoLab, address string) hcclient.Terminal {
	t.Helper()
	for _, terminal := range d.native.Terminals() {
		if terminal.Address == address {
			return terminal
		}
	}
	t.Fatalf("native terminal %s missing", address)
	return hcclient.Terminal{}
}
func nativeRunCount(d *demoLab) int {
	count := 0
	for _, call := range d.native.Calls() {
		if call.Method == "terminal.run" {
			count++
		}
	}
	return count
}

func TestReviewerLaunchUsesTaskCheckoutAndRecordsNativeEndpoint(t *testing.T) {
	for _, preset := range []bool{false, true} {
		t.Run(map[bool]string{false: "coordinator harness", true: "saved reviewer preset"}[preset], func(t *testing.T) {
			d, taskID := reviewLab(t)
			task := d.taskRecord(taskID)
			if preset {
				d.ctl(true, "preset", "set", "review", "--harness", "codex", "--model", "review-model", "--reasoning", "high")
				d.ctl(true, "settings", "set", "--reviewer-preset", "review")
			}
			denied := d.ctlPane(workerAddress(task), false, "reviewer", "launch", taskID)
			if !strings.Contains(asString(denied["error"]), "coordinator") {
				t.Fatalf("worker could launch independent reviewer: %v", denied)
			}
			before := nativeRunCount(d)
			reviewer := launchNativeReviewer(t, d, taskID)
			address := workerAddress(reviewer)
			if reviewer["cwd"] != task["worktree"] || reviewer["role"] != "reviewer" || reviewer["backend"] != "hc" || reviewer["core_id"] != d.native.CoreID {
				t.Fatalf("native reviewer identity: %v", reviewer)
			}
			if workerAddress(task) == address || nativeRunCount(d) != before+1 {
				t.Fatal("reviewer reused worker or created duplicate terminal")
			}
			if asString(asMap(reviewer["native_receipt"])["kind"]) != "reviewer-init" {
				t.Fatal("reviewer has no init receipt")
			}
			pinned := asString(reviewer["skill_path"])
			if !strings.HasPrefix(pinned, filepath.Join(d.home, "tasks", taskID, "procedure")+string(filepath.Separator)) {
				t.Fatalf("procedure not task pinned: %q", pinned)
			}
			if preset {
				want := []any{"-m", "review-model", "-c", `model_reasoning_effort=high`}
				if !reflect.DeepEqual(asSlice(reviewer["argv"]), want) {
					t.Fatalf("reviewer argv %v want %v", reviewer["argv"], want)
				}
			}
			terminal := nativeReviewerTerminal(t, d, address)
			s := nativeTestStore(t, d.home)
			instance, err := s.Instance()
			if err != nil {
				t.Fatal(err)
			}
			if terminal.Keep || terminal.RM || terminal.Notify || terminal.DieWithParent || terminal.Labels["sum.installation"] != instance || terminal.Labels["sum.task"] != taskID || terminal.Labels["sum.role"] != "reviewer" || terminal.Labels["sum.attempt"] != reviewer["attempt"] {
				t.Fatalf("managed reviewer lifecycle/labels: %+v", terminal)
			}
			params := asMap(asMap(reviewer["native_launch"])["params"])
			pointer := asString(asSlice(params["argv"])[1])
			for _, want := range []string{"init --role reviewer", "context " + taskID + " --role reviewer", "do not edit the checkout", reviewCandidateFor(d, taskID), pinned, "review " + taskID + " --verdict"} {
				if !strings.Contains(pointer, want) {
					t.Fatalf("reviewer pointer lacks %q: %s", want, pointer)
				}
			}
			verdict := d.ctlPane(address, true, "review", taskID, "--verdict", "comment", "--candidate", reviewCandidateFor(d, taskID), "--text", "Read-only inspection")
			if asString(asMap(verdict["evidence"])["source"]) != "reviewer" || asString(asMap(asMap(verdict["evidence"])["endpoint"])["address"]) != address {
				t.Fatal("review attributed to another execution")
			}
			again := d.ctl(false, "reviewer", "launch", taskID)
			if !strings.Contains(asString(again["error"]), "no automatic replay") || nativeRunCount(d) != before+1 {
				t.Fatalf("applied candidate replayed: %v", again)
			}
		})
	}
}

func TestReviewerLaunchRelaunchesOnlyAfterRecordedNativeReviewerStops(t *testing.T) {
	d, taskID := reviewLab(t)
	old := launchNativeReviewer(t, d, taskID)
	address := workerAddress(old)
	before := nativeRunCount(d)
	live := d.ctl(false, "reviewer", "launch", taskID, "--model", "different")
	if !strings.Contains(asString(live["error"]), "new reviewer") {
		t.Fatalf("live reviewer replaced: %v", live)
	}
	d.native.Exit(address, 0)
	fresh := launchNativeReviewer(t, d, taskID)
	if workerAddress(fresh) == address || fresh["attempt"] == old["attempt"] || nativeRunCount(d) != before+1 {
		t.Fatal("stopped reviewer not replaced with fresh attempt")
	}
	found := false
	for _, raw := range asSlice(d.taskRecord(taskID)["evidence"]) {
		row := asMap(raw)
		if row["kind"] == "reviewer-rebind" {
			found = true
			if asString(asMap(row["from"])["address"]) != address || asString(asMap(row["to"])["address"]) != workerAddress(fresh) || asMap(row["stop_evidence"])["outcome"] != "stopped" {
				t.Fatalf("replacement omitted stop proof: %v", row)
			}
		}
	}
	if !found {
		t.Fatal("replacement did not preserve reviewer-rebind evidence")
	}
}

func TestReviewerLaunchRefusesReplacementWithSurvivingCheckoutWriter(t *testing.T) {
	d, taskID := reviewLab(t)
	reviewer := launchNativeReviewer(t, d, taskID)
	address := workerAddress(reviewer)
	d.native.Exit(address, 0)
	startReviewerCheckoutWriter(t, d, taskID)
	before := nativeRunCount(d)
	out := d.ctl(false, "reviewer", "launch", taskID)
	if !strings.Contains(asString(out["error"]), "writers") || nativeRunCount(d) != before {
		t.Fatalf("surviving checkout writer did not prevent replacement: %v", out)
	}
}

func TestReviewerLaunchFlagsOverrideSavedPreset(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		model string
	}{{"explicit", []string{"--harness", "codex", "--model", "flag-model", "--reasoning", "low"}, "flag-model"}, {"preset", []string{"--preset", "alt"}, "alt-model"}} {
		t.Run(tc.name, func(t *testing.T) {
			d, taskID := reviewLab(t)
			d.ctl(true, "preset", "set", "review", "--harness", "grok", "--model", "unqualified-model")
			d.ctl(true, "preset", "set", "alt", "--harness", "codex", "--model", "alt-model")
			d.ctl(true, "settings", "set", "--reviewer-preset", "review")
			reviewer := launchNativeReviewer(t, d, taskID, tc.flags...)
			if reviewer["harness"] != "codex" || !strings.Contains(strings.Join(anyStrings(asSlice(reviewer["argv"])), " "), tc.model) {
				t.Fatalf("explicit selection lost: %v", reviewer)
			}
		})
	}
}

func TestReviewerLaunchRefusesUnqualifiedHarnessAndChangedCheckout(t *testing.T) {
	for _, harness := range []string{"claude", "grok"} {
		t.Run(harness, func(t *testing.T) {
			d, taskID := reviewLab(t)
			before := nativeRunCount(d)
			out := d.ctl(false, "reviewer", "launch", taskID, "--harness", harness)
			if !strings.Contains(asString(out["error"]), "no qualified native") || nativeRunCount(d) != before {
				t.Fatalf("unqualified harness auto launched: %v", out)
			}
		})
	}
	d, taskID := reviewLab(t)
	task := d.taskRecord(taskID)
	if out, err := exec.Command("git", "-C", asString(task["worktree"]), "switch", "--detach").CombinedOutput(); err != nil {
		t.Fatalf("detach: %v %s", err, out)
	}
	before := nativeRunCount(d)
	refused := d.ctl(false, "reviewer", "launch", taskID)
	if asString(refused["error"]) == "" || nativeRunCount(d) != before {
		t.Fatal("changed task checkout identity launched reviewer")
	}
}

func TestReviewerLaunchPolicyPointerAndKnownUnsentOpeningAreDurable(t *testing.T) {
	d, taskID := reviewLab(t)
	task := d.taskRecord(taskID)
	candidate := reviewCandidateFor(d, taskID)
	handoff := filepath.Join(d.base, "handoff.json")
	body, err := json.Marshal(map[string]any{"outcome": "completed", "candidate": candidate, "next_action": "review", "verification": map[string]any{"run_id": "policy-test", "outcome": "pass", "record": "run.json", "candidate": candidate, "certifies": nil, "requires_root_review": true}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(handoff, body, 0600); err != nil {
		t.Fatal(err)
	}
	d.workerReport(true, taskID, "--text", "policy candidate ready", "--handoff", handoff)
	no := false
	d.native.QueueFault("terminal.run", hctest.Fault{FirstMessage: &hcclient.FirstMessage{Sent: false, Started: &no}})
	out := d.ctl(true, "reviewer", "launch", taskID)
	reviewer := asMap(out["reviewer"])
	address := workerAddress(reviewer)
	if asMap(reviewer["prompt"])["state"] != "not-delivered" || address == "" {
		t.Fatalf("unsent kickoff lost created terminal: %v", out)
	}
	pointer := asString(asSlice(asMap(asMap(reviewer["native_launch"])["params"])["argv"])[1])
	for _, want := range []string{"--policy-reviewed", "line by line", candidate} {
		if !strings.Contains(pointer, want) {
			t.Fatalf("policy pointer lacks %q", want)
		}
	}
	before := nativeRunCount(d)
	again := d.ctl(false, "reviewer", "launch", taskID)
	if asString(again["error"]) == "" || nativeRunCount(d) != before {
		t.Fatal("unsent opening triggered automatic duplicate")
	}
	// Model the already created harness before explicit recovery; init follows delivery.
	terminal := nativeReviewerTerminal(t, d, address)
	var spec launch.HCTemplate
	raw, _ := json.Marshal(asMap(asMap(reviewer["native_template"])["spec"]))
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	pid := uint32(310000)
	terminal.PID = &pid
	terminal.Foreground = &hcclient.Foreground{PID: 310001, Name: "codex", Argv: spec.Argv, Cwd: asString(task["worktree"])}
	terminal.Status = &hcclient.Status{State: "idle"}
	d.native.SetTerminal(terminal)
	redelivered := d.ctl(true, "reviewer", "redeliver", taskID, "--attempt", asString(reviewer["attempt"]))
	if asMap(redelivered["prompt"])["state"] != "submitted" || nativeRunCount(d) != before {
		t.Fatal("explicit recovery failed or created duplicate")
	}
	d.ctlPane(address, true, "init")
}

func TestReviewerLaunchLostResponseAndUnknownFirstMessageAreNotReplayed(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown first message", true: "lost run response"}[lost], func(t *testing.T) {
			d, taskID := reviewLab(t)
			d.native.QueueFault("terminal.run", hctest.Fault{LoseResponse: lost})
			out := d.ctl(!lost, "reviewer", "launch", taskID)
			if lost && asString(out["error"]) == "" {
				t.Fatal("lost response accepted")
			}
			before := nativeRunCount(d)
			again := d.ctl(false, "reviewer", "launch", taskID)
			if asString(again["error"]) == "" || nativeRunCount(d) != before {
				t.Fatalf("ambiguous launch replayed: %v", again)
			}
		})
	}
}
func anyStrings(values []any) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = asString(v)
	}
	return out
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

func startReviewerCheckoutWriter(t *testing.T, d *demoLab, taskID string) {
	t.Helper()
	cwd := asString(d.taskRecord(taskID)["worktree"])
	writer := exec.Command("sleep", "30")
	writer.Dir = cwd
	if err := writer.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Process.Kill(); _ = writer.Wait() })
	data, err := json.Marshal(map[string]any{"processes": []map[string]any{{"pid": writer.Process.Pid, "cwd": cwd}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(d.base, "fake-lsof", "cwds.json")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
