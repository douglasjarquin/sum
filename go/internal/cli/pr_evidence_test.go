package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	evidenceBase      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	evidenceCandidate = "cccccccccccccccccccccccccccccccccccccccc"
	evidenceRun       = "run-1"
	evidenceScenario  = "counter.click"
	prProse           = "## Summary\n\nHuman prose that must survive publication.\n"
	blockMarker       = "<!-- before-and-after:start -->"
)

type publishLab struct {
	home     string
	repo     string
	worktree string
	ghRoot   string
	taskID   string
}

func newPublishLab(t *testing.T, ghState map[string]any) publishLab {
	t.Helper()
	lab := publishLab{home: writeDesignatedHome(t), repo: t.TempDir(), worktree: t.TempDir(), taskID: "t-aaaaaaaaaaaa"}
	lab.ghRoot = filepath.Join(lab.home, "fake-gh")
	herdrEnv(t, lab.home)
	t.Setenv("SUM_GH_BIN", filepath.Join(repoRoot(t), "tests", "fixtures", "gh_attach.py"))
	t.Setenv("FAKE_GH_ROOT", lab.ghRoot)
	lab.writeGitHub(t, ghState)
	if _, err := runCLI(t, lab.home, "init"); err != nil {
		t.Fatalf("coordinator init: %v", err)
	}
	comparison := writeEvidenceRun(t, filepath.Join(lab.worktree, "evidence"), evidenceBase, evidenceCandidate)
	writeTaskFixture(t, lab.home, lab.taskID, fmt.Sprintf(`{"schema": 1, "id": %q, "status": "reported", "repository": %q, "worktree": %q,
"questions": [], "notice": null, "attention": [], "brief": "do the thing", "base_sha": %q, "kind": "ship", "branch": "sum/t-aaaaaaaaaaaa",
"report": {"text": "done", "candidate": %q},
"evidence": [{"schema": 1, "id": "e-0000000001", "kind": "handoff", "source": "worker", "at": "2026-01-01T00:00:00+00:00", "candidate": %q,
"handoff": {"artifacts": [%q]}},
{"schema": 1, "id": "e-0000000002", "kind": "verification", "source": "worker", "at": "2026-01-01T00:00:00+00:00", "candidate": %q,
"run_id": "20260906T010203Z-abcd", "result": "pass"}]}`,
		lab.taskID, lab.repo, lab.worktree, evidenceBase, evidenceCandidate, evidenceCandidate, comparison, evidenceCandidate))
	return lab
}

func (lab publishLab) writeGitHub(t *testing.T, overrides map[string]any) {
	t.Helper()
	if err := os.MkdirAll(lab.ghRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{
		"version": "2.100.0", "repository": "douglasjarquin/project", "visibility": "PUBLIC", "viewer_permission": "WRITE",
		"pr": map[string]any{"number": 7, "state": "OPEN", "head_sha": evidenceCandidate, "body": prProse},
	}
	for k, v := range overrides {
		state[k] = v
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lab.ghRoot, "github.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (lab publishLab) prBody(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(lab.ghRoot, "github.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		PR struct {
			Body string `json:"body"`
		} `json:"pr"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state.PR.Body
}

func (lab publishLab) attachCalls(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(lab.ghRoot, "calls.jsonl"))
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.Contains(line, `"--attach"`) {
			count++
		}
	}
	return count
}

func (lab publishLab) publications(t *testing.T) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(lab.home, "tasks", lab.taskID, "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	var task struct {
		Evidence []map[string]any `json:"evidence"`
	}
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	for _, record := range task.Evidence {
		if record["kind"] == "publication" && record["source"] == "coordinator" && record["block"] == nil {
			rows = append(rows, record)
		}
	}
	return rows
}

// pipelinePublications are the delivery-pipeline block's records, which share the publication kind with the evidence block's.
func (lab publishLab) pipelinePublications(t *testing.T) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(lab.home, "tasks", lab.taskID, "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	var task struct {
		Evidence []map[string]any `json:"evidence"`
	}
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	for _, record := range task.Evidence {
		if record["kind"] == "publication" && record["block"] == "pipeline" {
			rows = append(rows, record)
		}
	}
	return rows
}

func writeEvidenceRun(t *testing.T, root, base, candidate string) string {
	t.Helper()
	scenarioDir := filepath.Join(root, evidenceRun, evidenceScenario)
	before := writeCapture(t, scenarioDir, "before", base, "fail", color.RGBA{220, 40, 40, 255}, 40, 60)
	after := writeCapture(t, scenarioDir, "after", candidate, "pass", color.RGBA{40, 80, 220, 255}, 80, 60)
	comparison := map[string]any{
		"schema": 1, "run": evidenceRun, "scenario": evidenceScenario,
		"base": map[string]any{"sha": base}, "candidate": map[string]any{"sha": candidate},
		"before": before, "after": after, "findings": []any{}, "verdict": "red-green",
		"label": "the base fails the user path and the candidate passes it", "kind": "bugfix",
		"visual_proof": "captured", "proves_claim": true,
		"outcomes": map[string]any{"before": "fail", "after": "pass"},
	}
	path := filepath.Join(scenarioDir, "comparison.json")
	writeJSONFile(t, path, comparison)
	return path
}

func writeCapture(t *testing.T, scenarioDir, role, sha, outcome string, fill color.RGBA, width, height int) map[string]any {
	t.Helper()
	dir := filepath.Join(scenarioDir, role+"-"+sha[:12])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shot := pngBytes(t, width, height, fill)
	if err := os.WriteFile(filepath.Join(dir, "screenshot.png"), shot, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(shot)
	hash := hex.EncodeToString(digest[:])
	media := map[string]any{"file": "screenshot.png", "bytes": len(shot), "sha256": hash, "type": "image/png",
		"width": width, "height": height, "role": "screenshot", "derived": false}
	writeJSONFile(t, filepath.Join(dir, "capture.json"), map[string]any{
		"schema": 1, "run": evidenceRun, "scenario": evidenceScenario, "feature": "counter", "role": role, "kind": "bugfix",
		"recipe": "browser", "outcome": outcome, "checkout": map[string]any{"sha": sha},
		"assertions":  []any{map[string]any{"expectation": "Count: 1", "met": outcome == "pass"}},
		"limitations": []any{}, "media": []any{media},
		"redaction":      map[string]any{"patterns": 4, "count": 0, "labelled": false},
		"content_hashes": map[string]any{"screenshot.png": hash},
	})
	return map[string]any{"dir": filepath.Base(dir), "outcome": outcome, "sha": sha, "recipe": "browser", "kind": "bugfix",
		"assertions":  []any{map[string]any{"expectation": "Count: 1", "met": outcome == "pass"}},
		"limitations": []any{}, "blocked_reason": nil, "media": []any{media}}
}

func pngBytes(t *testing.T, width, height int, fill color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.Set(x, y, fill)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func reconcile(t *testing.T, lab publishLab) map[string]any {
	t.Helper()
	stdout, stderr, err := runPRCLI(t, lab.home, "pr", "reconcile", lab.taskID, "--number", "7")
	if err != nil {
		t.Fatalf("pr reconcile: %v stderr=%s", err, stderr)
	}
	return decodeObject(t, stdout)
}

func TestPREvidencePublishesAndThenReportsUnchanged(t *testing.T) {
	requirePython(t)
	lab := newPublishLab(t, map[string]any{})
	lab.writeSettings(t, `{"schema": 1, "evidence": {"auto_publish": false}}`)
	reconcile(t, lab)

	stdout, stderr, err := runPRCLI(t, lab.home, "pr", "evidence", lab.taskID)
	if err != nil {
		t.Fatalf("pr evidence: %v stderr=%s", err, stderr)
	}
	if got := publishedOutcomes(t, stdout); len(got) != 1 || got[0] != "published" {
		t.Fatalf("pr evidence =\n%s\nwant one published outcome, got %v", stdout, got)
	}
	body := lab.prBody(t)
	if strings.Count(body, blockMarker) != 1 {
		t.Fatalf("PR body carries %d marked blocks:\n%s", strings.Count(body, blockMarker), body)
	}
	if !strings.Contains(body, prProse) {
		t.Fatalf("PR body lost the human prose:\n%s", body)
	}
	rows := lab.publications(t)
	if len(rows) != 1 {
		t.Fatalf("publication records = %d, want 1", len(rows))
	}
	if rows[0]["outcome"] != "published" || rows[0]["run"] != evidenceRun || rows[0]["trigger"] != "manual" {
		t.Fatalf("publication record = %v", rows[0])
	}
	uploads := lab.attachCalls(t)
	if uploads == 0 {
		t.Fatal("expected at least one gh --attach upload")
	}

	stdout, stderr, err = runPRCLI(t, lab.home, "pr", "evidence", lab.taskID)
	if err != nil {
		t.Fatalf("second pr evidence: %v stderr=%s", err, stderr)
	}
	if got := publishedOutcomes(t, stdout); len(got) != 1 || got[0] != "unchanged" {
		t.Fatalf("second pr evidence =\n%s\nwant one unchanged outcome, got %v", stdout, got)
	}
	if got := lab.attachCalls(t); got != uploads {
		t.Fatalf("second publish uploaded again: %d attach calls, first run made %d", got, uploads)
	}
	if rows = lab.publications(t); len(rows) != 2 || rows[1]["outcome"] != "unchanged" {
		t.Fatalf("publication records = %v, want a second unchanged record", rows)
	}
	if strings.Count(lab.prBody(t), blockMarker) != 1 {
		t.Fatalf("PR body carries more than one marked block:\n%s", lab.prBody(t))
	}
}

func TestPRReconcilePublishesUnlessTheEvidenceBlockOptsOut(t *testing.T) {
	requirePython(t)
	t.Run("no settings block publishes", func(t *testing.T) {
		lab := newPublishLab(t, map[string]any{})
		result := reconcile(t, lab)
		rows, _ := result["evidence_publication"].([]any)
		if len(rows) != 1 {
			t.Fatalf("evidence_publication = %v, want one run", result["evidence_publication"])
		}
		row, _ := rows[0].(map[string]any)
		if row["outcome"] != "published" || row["run"] != evidenceRun {
			t.Fatalf("evidence_publication row = %v", row)
		}
		if strings.Count(lab.prBody(t), blockMarker) != 1 {
			t.Fatalf("PR body =\n%s\nwant exactly one marked block", lab.prBody(t))
		}
		records := lab.publications(t)
		if len(records) != 1 || records[0]["trigger"] != "reconcile" {
			t.Fatalf("publication records = %v, want one reconcile record", records)
		}
	})

	t.Run("auto_publish false publishes nothing", func(t *testing.T) {
		lab := newPublishLab(t, map[string]any{})
		lab.writeSettings(t, `{"schema": 1, "evidence": {"auto_publish": false}}`)
		result := reconcile(t, lab)
		rows, _ := result["evidence_publication"].([]any)
		if len(rows) != 0 {
			t.Fatalf("evidence_publication = %v, want nothing published", result["evidence_publication"])
		}
		if strings.Contains(lab.prBody(t), blockMarker) {
			t.Fatalf("PR body was edited:\n%s", lab.prBody(t))
		}
		if rows := lab.publications(t); len(rows) != 0 {
			t.Fatalf("publication records = %v, want none", rows)
		}
	})
}

func TestPREvidenceSkipsAClosedPRAndDefersAnOldGh(t *testing.T) {
	requirePython(t)
	cases := []struct {
		name    string
		gh      map[string]any
		outcome string
		reason  string
	}{
		{name: "merged PR", gh: map[string]any{"pr": map[string]any{"number": 7, "state": "MERGED", "head_sha": evidenceCandidate, "body": prProse, "merge_commit": evidenceCandidate}}, outcome: "skipped", reason: "open PR"},
		{name: "closed PR", gh: map[string]any{"pr": map[string]any{"number": 7, "state": "CLOSED", "head_sha": evidenceCandidate, "body": prProse}}, outcome: "skipped", reason: "open PR"},
		{name: "gh without attach", gh: map[string]any{"version": "2.78.0"}, outcome: "deferred", reason: "2.99.0+"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lab := newPublishLab(t, tc.gh)
			lab.writeSettings(t, `{"schema": 1, "evidence": {"auto_publish": false}}`)
			reconcile(t, lab)
			stdout, stderr, err := runPRCLI(t, lab.home, "pr", "evidence", lab.taskID)
			if err != nil {
				t.Fatalf("pr evidence: %v stderr=%s", err, stderr)
			}
			if got := publishedOutcomes(t, stdout); len(got) != 1 || got[0] != tc.outcome {
				t.Fatalf("pr evidence =\n%s\nwant outcome %s, got %v", stdout, tc.outcome, got)
			}
			rows := lab.publications(t)
			if len(rows) != 1 || rows[0]["outcome"] != tc.outcome {
				t.Fatalf("publication records = %v, want one %s record", rows, tc.outcome)
			}
			reason, _ := rows[0]["reason"].(string)
			if !strings.Contains(reason, tc.reason) {
				t.Fatalf("reason = %q, want it to mention %q", reason, tc.reason)
			}
			if strings.Contains(lab.prBody(t), blockMarker) {
				t.Fatalf("PR body was edited:\n%s", lab.prBody(t))
			}
		})
	}
}

func (lab publishLab) writeSettings(t *testing.T, content string) {
	t.Helper()
	writeSettingsFile(t, lab.home, content)
}

func publishedOutcomes(t *testing.T, stdout string) []string {
	t.Helper()
	rows, _ := decodeObject(t, stdout)["publications"].([]any)
	outcomes := make([]string, 0, len(rows))
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		outcome, _ := row["outcome"].(string)
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}

func requirePython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
}
