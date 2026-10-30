package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func reviewCandidateFor(d *demoLab, taskID string) string {
	d.t.Helper()
	task := d.taskRecord(taskID)
	out, err := exec.Command("git", "-C", asString(task["worktree"]), "rev-parse", "HEAD").Output()
	if err != nil {
		d.t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// reviewLab dispatches one approved task under the receipt-bound native coordinator.
func reviewLab(t *testing.T) (*demoLab, string) {
	t.Helper()
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "reviewed", map[string]string{"README.md": "A project.\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	return d, asString(task["id"])
}

// reviewReturns lists the task's open review returns as the status view shows them.
func reviewReturns(d *demoLab, taskID string) []map[string]any {
	var out []map[string]any
	for _, raw := range asSlice(asMap(d.ctl(true, "context", taskID, "--role", "coordinator", "--section", "returns")["returns"])["open"]) {
		if row := asMap(raw); asString(row["kind"]) == "review" {
			out = append(out, row)
		}
	}
	return out
}

func TestReviewVerdictNotifiesAnIdleParent(t *testing.T) {
	for _, verdict := range []string{"approve", "changes-requested", "blocked"} {
		t.Run(verdict, func(t *testing.T) {
			d, taskID := reviewLab(t)
			settleNativeTerminal(t, d.base, "t1")

			out := d.independentReview(true, taskID, "--verdict", verdict, "--candidate", reviewCandidateFor(d, taskID), "--text", "findings")
			evidenceID := asString(asMap(out["evidence"])["id"])
			rows := asSlice(asMap(asMap(out["notice"])["returns"])["recipients"])
			if len(rows) != 1 {
				t.Fatalf("notice = %v, want one recipient row", out["notice"])
			}
			row := asMap(rows[0])
			if asString(row["state"]) != "submitted" || asString(row["via"]) != "prompt" || asString(asMap(row["recipient"])["address"]) != "t1" {
				t.Fatalf("recipient row = %v, want a prompt submitted to the coordinator pane", row)
			}
			prompt := strings.Join(d.native.Inputs("t1"), "\n")
			for _, want := range []string{taskID, "review " + evidenceID, "verdict " + verdict, reviewCandidateFor(d, taskID), "--kind review", "not approval"} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("parent prompt %q lacks %q", prompt, want)
				}
			}

			open := reviewReturns(d, taskID)
			if len(open) != 1 || asString(open[0]["id"]) != "review:"+evidenceID || asString(open[0]["verdict"]) != verdict ||
				asString(open[0]["candidate"]) != reviewCandidateFor(d, taskID) || asString(asMap(open[0]["notification"])["state"]) != "submitted" {
				t.Fatalf("open review returns = %v, want review:%s submitted and still open", open, evidenceID)
			}
		})
	}
}

func TestReviewVerdictWaitsForABusyParent(t *testing.T) {
	d, taskID := reviewLab(t)
	d.native.SetStatus("t1", "working")
	out := d.independentReview(true, taskID, "--verdict", "approve", "--candidate", reviewCandidateFor(d, taskID), "--text", "ok")
	rows := asSlice(asMap(asMap(out["notice"])["returns"])["recipients"])
	if len(rows) != 1 {
		t.Fatalf("busy review notice missing recipient: %v", out)
	}
	row := asMap(rows[0])
	if asString(row["state"]) != "not-delivered" || !strings.Contains(asString(row["reason"]), "working") {
		t.Fatalf("busy parent should defer native input: %v", row)
	}
	if len(d.native.Inputs("t1")) != 0 {
		t.Fatal("observed working parent received input")
	}

	if open := reviewReturns(d, taskID); len(open) != 1 {
		t.Fatalf("open review returns = %v, want the verdict still owed", open)
	}

	// The coordinator's next pass presents the still-owed verdict in its own output.
	pumped := d.ctl(true, "pump")
	for _, raw := range asSlice(pumped["recipients"]) {
		if row := asMap(raw); asString(row["via"]) == "inline" && strings.Contains(asString(row["message"]), "verdict approve") {
			return
		}
	}
	t.Fatalf("coordinator pump = %v, want the pending verdict listed inline", pumped)
}

func TestReviewWithoutAParentCreatesNoReturn(t *testing.T) {
	d, taskID := reviewLab(t)
	reviewer := launchNativeReviewer(t, d, taskID)
	address := workerAddress(reviewer)
	path := filepath.Join(d.home, "tasks", taskID, "task.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var task map[string]any
	if err := json.Unmarshal(raw, &task); err != nil {
		t.Fatal(err)
	}
	delete(task, "parent")
	if raw, err = json.Marshal(task); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	out := d.ctlPane(address, true, "review", taskID, "--verdict", "approve", "--candidate", reviewCandidateFor(d, taskID), "--text", "ok")
	if out["notice"] != nil || !strings.Contains(asString(out["note"]), "no parent") {
		t.Fatalf("review without a parent = %v, want no notice and a note saying why", out)
	}
	if open := reviewReturns(d, taskID); len(open) != 0 {
		t.Fatalf("open review returns = %v, want none without a parent", open)
	}
}

// Compact PR notes and the parent return are independent: notes are stored with the verdict, the return still opens,
// and the fixed notice carries neither the findings nor the notes.
func TestReviewWithNotesStillReturnsToTheParent(t *testing.T) {
	d, taskID := reviewLab(t)
	settleNativeTerminal(t, d.base, "t1")

	out := d.independentReview(true, taskID, "--verdict", "approve", "--candidate", reviewCandidateFor(d, taskID),
		"--text", "# Independent review\n\nlong durable report", "--focus", "hero CTA is scoped on purpose", "--limitation", "source inspected only")
	evidenceID := asString(asMap(out["evidence"])["id"])
	saved, err := json.Marshal(out["evidence"])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"kind":"review-focus"`, "hero CTA is scoped on purpose", `"kind":"limitation"`, "long durable report"} {
		if !strings.Contains(string(saved), want) {
			t.Fatalf("review evidence %s lacks %q", saved, want)
		}
	}

	open := reviewReturns(d, taskID)
	if len(open) != 1 || asString(open[0]["id"]) != "review:"+evidenceID || asString(asMap(open[0]["notification"])["state"]) != "submitted" {
		t.Fatalf("open review returns = %v, want review:%s submitted and still open", open, evidenceID)
	}
	prompt := strings.Join(d.native.Inputs("t1"), "\n")
	if !strings.Contains(prompt, "review "+evidenceID) {
		t.Fatalf("parent prompt %q does not name review %s", prompt, evidenceID)
	}
	for _, prose := range []string{"long durable report", "hero CTA", "source inspected only"} {
		if strings.Contains(prompt, prose) {
			t.Fatalf("parent prompt %q carries review prose %q", prompt, prose)
		}
	}
}
