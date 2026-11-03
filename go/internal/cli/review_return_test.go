package cli

import (
	"bytes"
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

func requirePendingCollectedReview(t *testing.T, d *demoLab, taskID, evidenceID, verdict string) {
	t.Helper()
	open := reviewReturns(d, taskID)
	if len(open) != 1 || asString(open[0]["id"]) != "review:"+evidenceID || asString(open[0]["verdict"]) != verdict ||
		asString(open[0]["candidate"]) != reviewCandidateFor(d, taskID) || asString(asMap(open[0]["notification"])["state"]) != "pending" || open[0]["route"] != nil {
		t.Fatalf("open review returns = %v, want a pending unrouted verdict", open)
	}
	path := filepath.Join(d.home, "tasks", taskID, "task.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		collected := d.ctl(true, "collect", "--task", taskID, "--budget", "100ms")
		found := false
		for _, raw := range asSlice(collected["items"]) {
			item := asMap(raw)
			if item["kind"] == "review" && item["task"] == taskID && item["ref"] == evidenceID {
				found = true
			}
		}
		if !found || len(asSlice(collected["delivered"])) != 0 {
			t.Fatalf("collect omitted or delivered saved review: %v", collected)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("reading a review settled or rewrote it")
	}
	if len(d.native.Inputs("t1")) != 0 {
		t.Fatal("review collection prompted an unrelated terminal")
	}
}

func TestReviewVerdictRemainsPendingForCollection(t *testing.T) {
	for _, verdict := range []string{"approve", "changes-requested", "blocked"} {
		for _, historicalParent := range []bool{false, true} {
			name := verdict + "/parentless"
			if historicalParent {
				name = verdict + "/historical-parent"
			}
			t.Run(name, func(t *testing.T) {
				d, taskID := reviewLab(t)
				settleNativeTerminal(t, d.base, "t1")
				if historicalParent {
					task := d.taskRecord(taskID)
					parent := map[string]any{}
					for key, value := range asMap(task["worker_endpoint"]) {
						parent[key] = value
					}
					parent["role"], parent["address"], parent["task"] = "coordinator", "t1", ""
					parent["attempt"], parent["created_ms"] = "historical-coordinator", 1
					task["parent"] = parent
					writeJSONFile(t, filepath.Join(d.home, "tasks", taskID, "task.json"), task)
				}
				out := d.independentReview(true, taskID, "--verdict", verdict, "--candidate", reviewCandidateFor(d, taskID), "--text", "findings")
				if out["notice"] != nil {
					t.Fatalf("review created parent notification: %v", out["notice"])
				}
				requirePendingCollectedReview(t, d, taskID, asString(asMap(out["evidence"])["id"]), verdict)
			})
		}
	}
}

func TestReviewCollectionIgnoresUnrelatedBusyTerminal(t *testing.T) {
	d, taskID := reviewLab(t)
	d.native.SetStatus("t1", "working")
	out := d.independentReview(true, taskID, "--verdict", "approve", "--candidate", reviewCandidateFor(d, taskID), "--text", "ok")
	if out["notice"] != nil {
		t.Fatalf("review created native notice: %v", out["notice"])
	}
	requirePendingCollectedReview(t, d, taskID, asString(asMap(out["evidence"])["id"]), "approve")
}

func TestReviewWithoutAParentStillCreatesACollectibleReturn(t *testing.T) {
	d, taskID := reviewLab(t)
	reviewer := launchNativeReviewer(t, d, taskID)
	address := workerAddress(reviewer)
	path := filepath.Join(d.home, "tasks", taskID, "task.json")
	task := d.taskRecord(taskID)
	delete(task, "parent")
	writeJSONFile(t, path, task)
	out := d.ctlPane(address, true, "review", taskID, "--verdict", "approve", "--candidate", reviewCandidateFor(d, taskID), "--text", "ok")
	if out["notice"] != nil || !strings.Contains(asString(out["note"]), "collection") {
		t.Fatalf("parentless review must be saved for collection: %v", out)
	}
	requirePendingCollectedReview(t, d, taskID, asString(asMap(out["evidence"])["id"]), "approve")
}

func TestReviewWithNotesRemainsPendingForCollection(t *testing.T) {
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
	if out["notice"] != nil {
		t.Fatalf("review prose was prompted: %v", out["notice"])
	}
	requirePendingCollectedReview(t, d, taskID, evidenceID, "approve")
}
