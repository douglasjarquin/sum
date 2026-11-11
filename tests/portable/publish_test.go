package skilltest

// Evidence publication: validated media from comparison manifests lands in one PR's marked block through `gh pr edit --attach`,
// receipts per content hash and destination prevent duplicate uploads, prose outside the block survives, concurrent edits are rebased
// or refused, and every failure (hash mismatch, path escape, secret-shaped text, old gh, missing permission, edit failure, timeout)
// leaves local evidence and the previous PR body intact. GitHub is a strict fake (`tests/fixtures/gh_attach.py`) found as `gh` on PATH.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

const (
	publishCandidate = "cccccccccccccccccccccccccccccccccccccccc"
	publishBase      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	proseTop         = "## Summary\n\nHuman prose above the evidence block. It must survive every publish.\n"
	proseBottom      = "\n## Notes\n\nHuman prose below the block. Also kept byte for byte.\n"
	prose            = proseTop + proseBottom
	blockStart       = "<!-- before-and-after:start -->"
	blockEnd         = "<!-- before-and-after:end -->"
	attachmentPrefix = "https://github.com/user-attachments/assets/"
)

var attachmentURL = regexp.MustCompile(`https://github\.com/user-attachments/assets/[A-Za-z0-9._-]+`)

type publishLab struct {
	t                                            *testing.T
	root, ghRoot, evidence, publishDir, receipts string
	fakeGh                                       string
	env                                          []string
}

// mediaFile is one file of a fixture capture and what its manifest entry says about it.
type mediaFile struct {
	name string
	data []byte
	meta map[string]any
}

// captured is a fixture capture: its directory name under the scenario and its record.
type captured struct {
	dir    string
	record map[string]any
}

func newPublishLab(t *testing.T) *publishLab {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := repoRoot(t)
	l := &publishLab{t: t, root: root, ghRoot: filepath.Join(root, "fake-gh"), evidence: filepath.Join(root, "evidence"), publishDir: filepath.Join(root, "publish"),
		receipts: filepath.Join(root, "receipts.json"), fakeGh: filepath.Join(repo, "tests/fixtures/gh_attach.py")}
	l.env = append(labEnv(t, repo, root), "FAKE_GH_ROOT="+l.ghRoot)
	if err := os.Symlink(l.fakeGh, filepath.Join(root, "bin", "gh")); err != nil {
		t.Fatal(err)
	}
	l.github(prose, nil)
	return l
}

// github resets the fake to one open PR with this body; state overrides the fake's scenario knobs.
func (l *publishLab) github(body string, state map[string]any) {
	l.t.Helper()
	value := map[string]any{"version": "2.100.0", "repository": "douglasjarquin/project", "visibility": "PUBLIC", "viewer_permission": "WRITE",
		"pr": map[string]any{"number": 7, "state": "OPEN", "head_sha": publishCandidate, "body": body}}
	for k, v := range state {
		value[k] = v
	}
	l.writeJSON(filepath.Join(l.ghRoot, "github.json"), value)
}

func (l *publishLab) writeJSON(path string, value any) {
	l.t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		l.t.Fatal(err)
	}
	mustWrite(l.t, path, string(data))
}

func (l *publishLab) readJSON(path string) map[string]any {
	l.t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(readFile(l.t, path)), &value); err != nil {
		l.t.Fatalf("%s: %v", path, err)
	}
	return value
}

func (l *publishLab) state() map[string]any {
	return l.readJSON(filepath.Join(l.ghRoot, "github.json"))
}

func (l *publishLab) body() string {
	return asString(asMap(l.state()["pr"])["body"])
}

// calls is every invocation the fake gh recorded, as its argument list.
func (l *publishLab) calls() [][]string {
	l.t.Helper()
	data, err := os.ReadFile(filepath.Join(l.ghRoot, "calls.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		l.t.Fatal(err)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var call struct{ Args []string }
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			l.t.Fatal(err)
		}
		calls = append(calls, call.Args)
	}
	return calls
}

// edits is every `gh pr edit` that was not the capability probe's `--help`.
func (l *publishLab) edits() [][]string {
	var edits [][]string
	for _, args := range l.calls() {
		if len(args) >= 3 && args[0] == "pr" && args[1] == "edit" && args[2] != "--help" {
			edits = append(edits, args)
		}
	}
	return edits
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (l *publishLab) capture(run, scenario, role, sha string, media []mediaFile, outcome string, redaction int) captured {
	l.t.Helper()
	name := role + "-" + sha[:12]
	directory := filepath.Join(l.evidence, run, scenario, name)
	entries := []any{}
	hashes := map[string]any{}
	for _, m := range media {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			l.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, m.name), m.data, 0o644); err != nil {
			l.t.Fatal(err)
		}
		entry := map[string]any{"file": m.name, "bytes": len(m.data), "sha256": sha256Hex(m.data)}
		for k, v := range m.meta {
			entry[k] = v
		}
		entries = append(entries, entry)
		hashes[m.name] = entry["sha256"]
	}
	feature, _, _ := strings.Cut(scenario, ".")
	record := map[string]any{"schema": 1, "run": run, "scenario": scenario, "feature": feature, "role": role, "kind": "bugfix", "recipe": "browser", "outcome": outcome,
		"checkout": map[string]any{"sha": sha}, "assertions": []any{map[string]any{"expectation": "Count: 1", "met": outcome == "pass"}}, "limitations": []any{}, "media": entries,
		"redaction": map[string]any{"patterns": 4, "count": redaction, "labelled": redaction > 0}, "content_hashes": hashes}
	l.writeJSON(filepath.Join(directory, "capture.json"), record)
	return captured{name, record}
}

func (l *publishLab) comparison(run, scenario string, before, after *captured, verdict, label string) string {
	l.t.Helper()
	side := func(c *captured) any {
		if c == nil {
			return nil
		}
		media := []any{}
		for _, m := range asSlice(c.record["media"]) {
			projected := map[string]any{}
			for _, k := range []string{"file", "type", "sha256", "bytes", "width", "height", "frames", "duration_ms", "role", "derived"} {
				projected[k] = asMap(m)[k]
			}
			media = append(media, projected)
		}
		return map[string]any{"dir": c.dir, "outcome": c.record["outcome"], "sha": asMap(c.record["checkout"])["sha"], "recipe": "browser", "kind": "bugfix",
			"assertions": c.record["assertions"], "limitations": c.record["limitations"], "blocked_reason": nil, "media": media}
	}
	beforeOutcome := any("missing")
	if before != nil {
		beforeOutcome = before.record["outcome"]
	}
	value := map[string]any{"schema": 1, "run": run, "scenario": scenario, "base": map[string]any{"sha": publishBase}, "candidate": map[string]any{"sha": publishCandidate},
		"before": side(before), "after": side(after), "findings": []any{}, "verdict": verdict, "label": label, "kind": "bugfix", "visual_proof": "captured",
		"proves_claim": verdict == "red-green" || verdict == "before-after" || verdict == "after-only",
		"outcomes":     map[string]any{"before": beforeOutcome, "after": after.record["outcome"]}}
	directory := filepath.Join(l.evidence, run, scenario)
	l.writeJSON(filepath.Join(directory, "comparison.json"), value)
	return directory
}

func solidPNG(t *testing.T, width, height int, r, g, b uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{r, g, b, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func screenshot(t *testing.T, width, height int, r, g, b uint8) mediaFile {
	return mediaFile{"screenshot.png", solidPNG(t, width, height, r, g, b), map[string]any{"type": "image/png", "width": width, "height": height, "role": "screenshot", "derived": false}}
}

// tinyVideo is a real, probe-able clip when the tool under test can find ffmpeg (mp4); otherwise webm bytes the publisher accepts by declared type.
func (l *publishLab) tinyVideo(color string) mediaFile {
	l.t.Helper()
	meta := map[string]any{"role": "screencast-converted", "derived": true, "frames": 4, "duration_ms": 400}
	for _, entry := range l.env {
		path, isPath := strings.CutPrefix(entry, "PATH=")
		if !isPath {
			continue
		}
		for _, dir := range filepath.SplitList(path) {
			ffmpeg := filepath.Join(dir, "ffmpeg")
			if info, err := os.Stat(ffmpeg); err != nil || info.IsDir() {
				continue
			}
			out := filepath.Join(l.t.TempDir(), "clip.mp4")
			cmd := exec.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "color=c="+color+":s=32x32:d=0.4:r=10", "-pix_fmt", "yuv420p", "-movflags", "+faststart", out)
			if output, err := cmd.CombinedOutput(); err != nil {
				l.t.Fatalf("ffmpeg: %v\n%s", err, output)
			}
			meta["type"] = "video/mp4"
			return mediaFile{"screencast.mp4", []byte(readFile(l.t, out)), meta}
		}
	}
	meta["type"] = "video/webm"
	return mediaFile{"screencast.webm", append([]byte("\x1aE\xdf\xa3"), bytes.Repeat([]byte(color), 40)...), meta}
}

// scenarioPair writes a red-green screenshot pair for counter.click (with converted screencasts when video is set), or an after-only
// capture when before is false, and returns the scenario directory.
func (l *publishLab) scenarioPair(run string, video, before bool) string {
	l.t.Helper()
	const scenario = "counter.click"
	beforeMedia := []mediaFile{screenshot(l.t, 40, 60, 220, 40, 40)}
	afterMedia := []mediaFile{screenshot(l.t, 80, 60, 40, 80, 220)}
	if video {
		original := func(fill byte) mediaFile {
			return mediaFile{"screencast.avi", append([]byte("RIFF"), bytes.Repeat([]byte{fill}, 64)...),
				map[string]any{"type": "video/x-msvideo", "role": "screencast", "derived": false, "frames": 4, "duration_ms": 400}}
		}
		beforeMedia = append(beforeMedia, original(0), l.tinyVideo("red"))
		afterMedia = append(afterMedia, original(1), l.tinyVideo("blue"))
	}
	after := l.capture(run, scenario, "after", publishCandidate, afterMedia, "pass", 0)
	if !before {
		return l.comparison(run, scenario, nil, &after, "after-only", "before-unavailable: no baseline access; this shows the candidate only, not a red/green pair")
	}
	base := l.capture(run, scenario, "before", publishBase, beforeMedia, "fail", 0)
	return l.comparison(run, scenario, &base, &after, "red-green", "the base fails the user path and the candidate passes it")
}

// textPair writes a transcript pair for cli.output in run-1, or an after-only transcript when before is false.
func (l *publishLab) textPair(text string, before bool, redaction int) string {
	l.t.Helper()
	media := []mediaFile{{"transcript.txt", []byte(text), map[string]any{"type": "text/plain", "role": "transcript", "derived": false}}}
	after := l.capture("run-1", "cli.output", "after", publishCandidate, media, "pass", redaction)
	if !before {
		return l.comparison("run-1", "cli.output", nil, &after, "after-only", "before-unavailable: baseline cannot run")
	}
	base := l.capture("run-1", "cli.output", "before", publishBase, media, "fail", redaction)
	return l.comparison("run-1", "cli.output", &base, &after, "red-green", "base fails, candidate passes")
}

// realTextPair captures a failing base and a passing candidate of one text scenario with the real tool, compares them, and points the
// fake PR (on a gh too old to attach) at the candidate. It returns the base and candidate SHAs.
func (l *publishLab) realTextPair(recipe string) (base, candidate string) {
	t := l.t
	t.Helper()
	checkout := filepath.Join(l.root, "project")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, checkout, "init", "-q")
	git(t, checkout, "config", "user.email", "fixture@example.invalid")
	git(t, checkout, "config", "user.name", "Fixture")
	git(t, checkout, "config", "commit.gpgsign", "false")
	action := []string{"cli", "--expect-text", "ready", "--", "cat", "result.txt"}
	if recipe == "http" {
		server := httptest.NewServer(http.FileServer(http.Dir(checkout)))
		t.Cleanup(server.Close)
		action = []string{"http", "--expect-status", "200", "--expect-text", "ready", "GET", server.URL + "/result.txt"}
	}
	env := append(append([]string{}, l.env...), "VERIFY_EVIDENCE_ROOT="+l.evidence)
	var shas []string
	for _, step := range []struct {
		role, output string
		code         int
	}{{"before", "waiting", 1}, {"after", "ready", 0}} {
		mustWrite(t, filepath.Join(checkout, "result.txt"), step.output+"\ncontact=fixture@example.invalid\n")
		git(t, checkout, "add", "result.txt")
		git(t, checkout, "commit", "-qm", step.output)
		sha := git(t, checkout, "rev-parse", "HEAD")
		shas = append(shas, sha)
		args := append([]string{"evidence", "capture", "--scenario", recipe + ".output", "--run", "run-1", "--kind", "nonvisual", "--role", step.role, "--checkout", checkout, "--expect-sha", sha}, action...)
		if code, _, stdout, stderr := runTool(t, env, checkout, args...); code != step.code {
			t.Fatalf("%s capture exit %d, want %d\n%s%s", step.role, code, step.code, stdout, stderr)
		}
	}
	code, comparison, stdout, stderr := runTool(t, env, checkout, "evidence", "compare", "--scenario", recipe+".output", "--run", "run-1", "--base", shas[0], "--candidate", shas[1], "--json")
	if code != 0 || asString(comparison["verdict"]) != "red-green" {
		t.Fatalf("compare exit %d verdict %v\n%s%s", code, comparison["verdict"], stdout, stderr)
	}
	l.github(prose, map[string]any{"version": "2.78.0", "pr": map[string]any{"number": 7, "state": "OPEN", "head_sha": shas[1], "body": prose}})
	return shas[0], shas[1]
}

func (l *publishLab) runPublish(wantCode int, args ...string) (map[string]any, string) {
	l.t.Helper()
	code, record, stdout, stderr := runTool(l.t, l.env, l.root, append([]string{"publish"}, args...)...)
	if code != wantCode {
		l.t.Fatalf("publish %v: exit %d, want %d\n%s%s", args, code, wantCode, stdout, stderr)
	}
	return record, stdout
}

// plan plans a run for the fake PR; extra arguments come last, so a repeated option overrides the lab's default.
func (l *publishLab) plan(wantCode int, run string, extra ...string) map[string]any {
	l.t.Helper()
	args := append([]string{"plan", "--run", run, "--repo", "douglasjarquin/project", "--pr", "7", "--candidate", publishCandidate, "--base", publishBase, "--evidence-root", l.evidence,
		"--publish-dir", l.publishDir, "--verification-run", "20260906T010203Z-abcd", "--json"}, extra...)
	record, _ := l.runPublish(wantCode, args...)
	return record
}

func (l *publishLab) planPath() string {
	return filepath.Join(l.publishDir, "plan.json")
}

func (l *publishLab) publishAs(wantCode int, visibility string, extra ...string) map[string]any {
	l.t.Helper()
	args := append([]string{"publish", "--plan", l.planPath(), "--receipts", l.receipts, "--visibility", visibility, "--timeout", "3", "--json"}, extra...)
	record, _ := l.runPublish(wantCode, args...)
	return record
}

func (l *publishLab) publish(wantCode int, extra ...string) map[string]any {
	l.t.Helper()
	return l.publishAs(wantCode, "public", extra...)
}

// block is the marked block of a PR body, markers included, or "" when the body has none.
func block(body string) string {
	start, end := strings.Index(body, blockStart), strings.Index(body, blockEnd)
	if start < 0 || end < start {
		return ""
	}
	return body[start : end+len(blockEnd)]
}

func (l *publishLab) wantOutcome(result map[string]any, outcome string) {
	l.t.Helper()
	if asString(result["outcome"]) != outcome {
		l.t.Fatalf("outcome %v, want %s: %v", result["outcome"], outcome, result)
	}
}

func (l *publishLab) wantContains(text string, parts ...string) {
	l.t.Helper()
	for _, part := range parts {
		if !strings.Contains(text, part) {
			l.t.Fatalf("missing %q in:\n%s", part, text)
		}
	}
}

func (l *publishLab) wantNoEdits() {
	l.t.Helper()
	if edits := l.edits(); len(edits) != 0 {
		l.t.Fatalf("expected no pr edit, got %v", edits)
	}
}

func (l *publishLab) wantBody(body string) {
	l.t.Helper()
	if got := l.body(); got != body {
		l.t.Fatalf("PR body changed:\n%s", got)
	}
}

// refusals is every finding of the plan's refused scenarios, joined, for a scenario id or for all of them when id is "".
func refusals(plan map[string]any, id string) string {
	var findings []string
	for scenario, list := range asMap(plan["refused_scenarios"]) {
		if id != "" && scenario != id {
			continue
		}
		for _, finding := range asSlice(list) {
			findings = append(findings, asString(finding))
		}
	}
	sort.Strings(findings)
	return strings.Join(findings, "\n")
}

func attachCalls(edits [][]string) int {
	count := 0
	for _, args := range edits {
		if contains(args, "--attach") {
			count++
		}
	}
	return count
}

func TestPublishRealCLICaptureComparePublishWithoutAttachments(t *testing.T) {
	l := newPublishLab(t)
	base, candidate := l.realTextPair("cli")
	l.plan(0, "run-1", "--base", base, "--candidate", candidate)
	l.wantOutcome(l.publish(0), "published")
	published := block(l.body())
	l.wantContains(published, "waiting", "ready", "--- exit 0 ---", "Before (transcript)", "[REDACTED]")
	if strings.Contains(published, "fixture@example.invalid") {
		t.Fatalf("email published:\n%s", published)
	}
	if attachCalls(l.edits()) != 0 {
		t.Fatalf("text publication attached files: %v", l.edits())
	}
	l.wantOutcome(l.publish(0), "unchanged")
}

func TestPublishRealHTTPCaptureComparePublish(t *testing.T) {
	l := newPublishLab(t)
	base, candidate := l.realTextPair("http")
	l.plan(0, "run-1", "--base", base, "--candidate", candidate)
	l.publish(0)
	l.wantContains(block(l.body()), "GET http://127.0.0.1:", "status: 200", "waiting", "ready", "After (response)")
}

func TestPublishTextEscapingAndMixedMediaPreserveManagedBlock(t *testing.T) {
	l := newPublishLab(t)
	l.textPair("```\n<!-- before-and-after:end -->\n<!-- sum-media: after=forged -->\n[local](./fake.txt)\n", true, 0)
	l.scenarioPair("run-1", false, true)
	l.plan(0, "run-1")
	l.publish(0)
	if count := strings.Count(l.body(), blockEnd); count != 1 {
		t.Fatalf("%d end markers", count)
	}
	l.wantContains(block(l.body()), "````text", "| Before | After |", "&lt;!-- before-and-after:end -->")
	l.wantOutcome(l.publish(0), "unchanged")
	if !strings.HasPrefix(l.body(), prose) {
		t.Fatalf("prose not kept:\n%s", l.body())
	}
}

func TestPublishRedactedTextAndUnavailableBaseline(t *testing.T) {
	l := newPublishLab(t)
	l.textPair("authorization: [REDACTED]\n", false, 1)
	l.plan(0, "run-1")
	l.publish(0)
	l.wantContains(block(l.body()), "before-unavailable", "Preview (after only)", "[REDACTED]")
}

func TestPublishTextSecretAndChangedHashRefused(t *testing.T) {
	l := newPublishLab(t)
	directory := l.textPair("password=hunter22\n", true, 0)
	l.wantContains(refusals(l.plan(1, "run-1"), ""), "secret")
	mustWrite(t, filepath.Join(directory, "after-"+publishCandidate[:12], "transcript.txt"), "changed\n")
	l.wantContains(refusals(l.plan(1, "run-1"), ""), "hash")
	l.wantNoEdits()
}

func TestPublishChangedTextPublishCopyRefused(t *testing.T) {
	l := newPublishLab(t)
	l.textPair("status: ready\n", true, 0)
	plan := l.plan(0, "run-1")
	media := asSlice(asMap(asSlice(plan["scenarios"])[0])["media"])
	copy := filepath.Join(l.publishDir, "media", asString(asMap(media[len(media)-1])["publish_copy"]))
	mustWrite(t, copy, "changed")
	l.wantOutcome(l.publish(1), "refused")
	if err := os.Rename(copy, filepath.Join(l.root, "saved-copy.txt")); err != nil {
		t.Fatal(err)
	}
	missing := l.publish(1)
	l.wantOutcome(missing, "refused")
	l.wantContains(asString(missing["reason"]), "not a regular file")
	l.wantNoEdits()
}

func TestPublishReadbackRejectsChangedTextBlock(t *testing.T) {
	l := newPublishLab(t)
	l.textPair("status: ready\n", true, 0)
	l.plan(0, "run-1")
	l.github(prose, map[string]any{"mutation": map[string]any{"after_views": 3, "where": "inside", "text": "changed evidence"}})
	result := l.publish(4)
	l.wantOutcome(result, "uncertain")
	l.wantContains(asString(result["reason"]), "differs from the planned content")
	l.wantContains(l.body(), "changed evidence")
	if !strings.HasPrefix(l.body(), prose) {
		t.Fatalf("prose not kept:\n%s", l.body())
	}
	if len(l.edits()) != 1 {
		t.Fatalf("edits %v", l.edits())
	}
	if observed := readFile(t, filepath.Join(l.publishDir, "body-observed.md")); observed != l.body() {
		t.Fatalf("body-observed.md is not the observed body:\n%s", observed)
	}
}

func TestPublishTextWithoutGhRetainsDeferredResult(t *testing.T) {
	l := newPublishLab(t)
	l.textPair("status: ready\n", true, 0)
	l.plan(0, "run-1")
	l.env = append(l.env, "PATH=")
	outcome, _ := l.runPublish(2, "publish", "--plan", l.planPath(), "--visibility", "public", "--json")
	l.wantOutcome(outcome, "deferred")
	if _, err := os.Stat(asString(outcome["record"])); err != nil {
		t.Fatalf("deferred result not retained: %v", err)
	}
	l.wantNoEdits()
}

func TestPublishOversizedFullBodyRefusedBeforeEdit(t *testing.T) {
	l := newPublishLab(t)
	l.textPair(strings.Repeat("x", 20000), true, 0)
	l.plan(0, "run-1")
	original := strings.Repeat("h", 50000)
	l.github(original, nil)
	l.wantOutcome(l.publish(1), "refused")
	l.wantNoEdits()
	l.wantBody(original)
}

func TestPublishCapabilitiesReadAttachSupportFromTheBinary(t *testing.T) {
	l := newPublishLab(t)
	info, _ := l.runPublish(0, "capabilities", "--gh", l.fakeGh, "--json")
	if asString(info["version"]) != "2.100.0" || info["attach"] != true || info["authenticated"] != true {
		t.Fatalf("capabilities %v", info)
	}
	l.github("", map[string]any{"version": "2.78.0"})
	info, _ = l.runPublish(2, "capabilities", "--gh", l.fakeGh, "--json")
	if asString(info["version"]) != "2.78.0" || info["attach"] != false {
		t.Fatalf("capabilities %v", info)
	}
	l.wantContains(asString(info["reason"]), "2.99.0+")
}

func TestPublishPlanValidatesMediaAndStagesPublishCopiesWithoutTouchingGitHub(t *testing.T) {
	l := newPublishLab(t)
	l.scenarioPair("run-1", true, true)
	plan := l.plan(0, "run-1")
	if got := asSlice(plan["publishable_scenarios"]); len(got) != 1 || got[0] != "counter.click" {
		t.Fatalf("publishable %v", got)
	}
	var roles []string
	for _, m := range asSlice(asMap(asSlice(plan["scenarios"])[0])["media"]) {
		entry := asMap(m)
		roles = append(roles, asString(entry["role"])+"/"+asString(entry["kind"]))
		name := asString(entry["publish_copy"])
		data, err := os.ReadFile(filepath.Join(l.publishDir, "media", name))
		if err != nil || sha256Hex(data) != asString(entry["sha256"]) {
			t.Fatalf("publish copy %s: %v", name, err)
		}
		if strings.Contains(name, " ") {
			t.Fatalf("publish copy name has a space: %q", name)
		}
	}
	sort.Strings(roles)
	if got := strings.Join(roles, " "); got != "after/image after/video before/image before/video" {
		t.Fatalf("media %s", got)
	}
	if _, err := os.Stat(filepath.Join(l.evidence, "run-1/counter.click", "before-"+publishBase[:12], "screenshot.png")); err != nil {
		t.Fatalf("originals must stay in place: %v", err)
	}
	l.wantContains(asString(plan["block_preview"]), blockStart, "| Before | After |", "not independent verification", "20260906T010203Z-abcd")
	if calls := l.calls(); len(calls) != 0 {
		t.Fatalf("plan called gh: %v", calls)
	}
}

func TestPublishFirstPublishUploadsReplacesNothingElseAndRecordsReceipts(t *testing.T) {
	l := newPublishLab(t)
	l.scenarioPair("run-1", true, true)
	l.plan(0, "run-1")
	result := l.publish(0)
	l.wantOutcome(result, "published")
	if len(asSlice(result["uploaded"])) != 4 || len(asSlice(result["reused"])) != 0 || asString(result["video_table"]) != "applied" {
		t.Fatalf("result %v", result)
	}
	body := l.body()
	if !strings.HasPrefix(body, prose) {
		t.Fatalf("a first publish appends after every byte of the existing prose:\n%s", body)
	}
	if strings.Count(body, blockStart) != 1 || strings.Contains(body, "](./") {
		t.Fatalf("body:\n%s", body)
	}
	published := block(body)
	l.wantContains(published, "| Before | After |", `<video src="`+attachmentPrefix, "<th>Before (screencast)</th>", "counter.click", "candidate `"+publishCandidate[:12]+"`")
	if count := strings.Count(published, attachmentPrefix); count != 4 {
		t.Fatalf("%d attachment URLs in:\n%s", count, published)
	}
	receipts := l.readJSON(l.receipts)
	attachments := asMap(receipts["attachments"])
	if len(attachments) != 4 {
		t.Fatalf("attachments %v", attachments)
	}
	for key, entry := range attachments {
		if !strings.HasSuffix(key, "@douglasjarquin/project#7") || !strings.HasPrefix(asString(asMap(entry)["url"]), attachmentPrefix) {
			t.Fatalf("receipt %s: %v", key, entry)
		}
	}
	if blocks := asSlice(receipts["blocks"]); len(blocks) != 2 {
		t.Fatalf("blocks %v", blocks)
	}
	if _, err := os.Stat(asString(result["record"])); err != nil {
		t.Fatalf("result record: %v", err)
	}
}

func TestPublishSecondPublishReusesEveryAttachmentAndReplacesTheSameBlockInPlace(t *testing.T) {
	l := newPublishLab(t)
	l.scenarioPair("run-1", true, true)
	l.plan(0, "run-1")
	first := l.publish(0)
	bodyAfterFirst := l.body()
	editsAfterFirst := len(l.edits())
	l.wantOutcome(l.publish(0), "unchanged")
	if len(l.edits()) != editsAfterFirst {
		t.Fatalf("an unchanged publish edited the PR: %v", l.edits())
	}
	l.wantBody(bodyAfterFirst)
	// A second run with the same media plus a reviewer's edit elsewhere: receipts reuse the URLs, the block is replaced where it stands.
	edited := strings.Replace(bodyAfterFirst, "Human prose above", "Human prose (edited by a reviewer) above", 1)
	state := l.state()
	asMap(state["pr"])["body"] = edited
	l.writeJSON(filepath.Join(l.ghRoot, "github.json"), state)
	l.scenarioPair("run-2", true, true)
	l.plan(0, "run-2")
	result := l.publish(0)
	l.wantOutcome(result, "published")
	if len(asSlice(result["uploaded"])) != 0 || len(asSlice(result["reused"])) != 4 {
		t.Fatalf("result %v", result)
	}
	if count := attachCalls(l.edits()); count != 1 {
		t.Fatalf("the second run must not upload anything; %d attach calls", count)
	}
	body := l.body()
	l.wantContains(body, "Human prose (edited by a reviewer) above")
	if strings.Count(body, blockStart) != 1 || strings.Index(body, blockStart) != strings.Index(edited, blockStart) {
		t.Fatalf("block moved:\n%s", body)
	}
	l.wantContains(block(body), "run=run-2")
	urls := func(text string) string {
		found := attachmentURL.FindAllString(text, -1)
		sort.Strings(found)
		return strings.Join(slices.Compact(found), "\n")
	}
	if urls(block(body)) != urls(block(bodyAfterFirst)) {
		t.Fatalf("attachment URLs changed:\n%s\n--\n%s", urls(block(body)), urls(block(bodyAfterFirst)))
	}
	if len(asSlice(first["uploaded"])) != 4 {
		t.Fatalf("first %v", first)
	}
}

func TestPublishConcurrentOutsideEditIsRebasedAndKept(t *testing.T) {
	l := newPublishLab(t)
	l.scenarioPair("run-1", true, true)
	l.plan(0, "run-1")
	l.github(prose, map[string]any{"mutation": map[string]any{"after_views": 1, "where": "outside", "text": "A reviewer added this paragraph meanwhile."}})
	l.wantOutcome(l.publish(0), "published")
	body := l.body()
	l.wantContains(body, "A reviewer added this paragraph meanwhile.")
	if !strings.HasPrefix(body, proseTop) || strings.Count(body, blockStart) != 1 {
		t.Fatalf("body:\n%s", body)
	}
}

func TestPublishPreExistingForeignBlockIsRefusedUntilExplicitlyReplaced(t *testing.T) {
	l := newPublishLab(t)
	foreign := blockStart + "\n| Before | After |\n|:---:|:---:|\n| ![b](" + attachmentPrefix + "aaa) | ![a](" + attachmentPrefix + "bbb) |\n" + blockEnd + "\n"
	original := proseTop + "\n" + foreign + proseBottom
	l.github(original, nil)
	l.scenarioPair("run-1", true, true)
	l.plan(0, "run-1")
	result := l.publish(1)
	l.wantOutcome(result, "refused")
	l.wantContains(asString(result["reason"]), "did not write")
	l.wantNoEdits()
	l.wantBody(original)
	l.wantOutcome(l.publish(0, "--replace-foreign-block"), "published")
	body := l.body()
	if !strings.HasPrefix(body, proseTop+"\n"+blockStart) || !strings.HasSuffix(body, proseBottom) || strings.Count(body, blockStart) != 1 || strings.Contains(body, "assets/aaa") {
		t.Fatalf("body:\n%s", body)
	}
}

func TestPublishConcurrentInsideBlockEditIsRefusedNotOverwritten(t *testing.T) {
	l := newPublishLab(t)
	l.scenarioPair("run-1", true, true)
	l.plan(0, "run-1")
	l.publish(0)
	l.github(l.body(), map[string]any{"mutation": map[string]any{"after_views": 1, "where": "inside", "text": "Reviewer: this screenshot looks wrong."}})
	l.scenarioPair("run-2", true, true)
	l.plan(0, "run-2")
	result := l.publish(1)
	l.wantOutcome(result, "refused")
	l.wantContains(asString(result["reason"]), "did not write")
	l.wantContains(l.body(), "Reviewer: this screenshot looks wrong.")
	if len(l.edits()) != 2 {
		t.Fatalf("edits %v", l.edits())
	}
}

func TestPublishAbsentBaselinePublishesALabelledPreviewOnly(t *testing.T) {
	l := newPublishLab(t)
	l.scenarioPair("run-1", true, false)
	l.plan(0, "run-1")
	l.wantOutcome(l.publish(0), "published")
	published := block(l.body())
	l.wantContains(published, "| Preview (after only) |", "after-only", "before-unavailable", "<th>Preview (screencast)</th>")
	if strings.Contains(published, "| Before | After |") {
		t.Fatalf("after-only block has a before column:\n%s", published)
	}
}

func TestPublishHashMismatchAndPathEscapeAreRefusedBeforeAnyUpload(t *testing.T) {
	l := newPublishLab(t)
	directory := l.scenarioPair("run-1", true, true)
	afterDir := filepath.Join(directory, "after-"+publishCandidate[:12])
	if err := os.WriteFile(filepath.Join(afterDir, "screenshot.png"), solidPNG(t, 80, 60, 0, 0, 0), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := l.plan(1, "run-1")
	if len(asSlice(plan["publishable_scenarios"])) != 0 {
		t.Fatalf("publishable %v", plan["publishable_scenarios"])
	}
	altered := false
	for _, finding := range strings.Split(refusals(plan, "counter.click"), "\n") {
		altered = altered || (strings.Contains(finding, "hash") && strings.Contains(finding, "altered"))
	}
	if !altered {
		t.Fatalf("refusals %v", plan["refused_scenarios"])
	}
	if staged, _ := filepath.Glob(filepath.Join(l.publishDir, "media", "*after*")); len(staged) != 0 {
		t.Fatalf("after media staged: %v", staged)
	}
	l.wantOutcome(l.publish(1), "refused")
	l.wantNoEdits()
	// A manifest pointing outside the run through a symlink or a parent component is refused, and the file is never read into a copy.
	secret := filepath.Join(l.root, "outside.png")
	outside := solidPNG(t, 8, 8, 1, 2, 3)
	if err := os.WriteFile(secret, outside, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(directory, "comparison.json")
	comparison := l.readJSON(manifest)
	shot := asMap(asSlice(asMap(comparison["after"])["media"])[0])
	shot["file"] = "../../../outside.png"
	l.writeJSON(manifest, comparison)
	l.wantContains(refusals(l.plan(1, "run-1"), "counter.click"), "unsafe path")
	if err := os.Symlink(secret, filepath.Join(afterDir, "linked.png")); err != nil {
		t.Fatal(err)
	}
	shot["file"] = "linked.png"
	shot["sha256"] = sha256Hex(outside)
	l.writeJSON(manifest, comparison)
	l.wantContains(refusals(l.plan(1, "run-1"), "counter.click"), "escapes")
	l.wantBody(prose)
}

func TestPublishSecretShapedTextOrRedactedCaptureRefusesPublication(t *testing.T) {
	l := newPublishLab(t)
	directory := l.scenarioPair("run-1", true, true)
	manifest := filepath.Join(directory, "comparison.json")
	comparison := l.readJSON(manifest)
	comparison["label"] = "passes with token=ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	l.writeJSON(manifest, comparison)
	l.wantContains(refusals(l.plan(1, "run-1"), "counter.click"), "secret-shaped")
	if staged, _ := filepath.Glob(filepath.Join(l.publishDir, "media", "*")); len(staged) != 0 {
		t.Fatalf("media staged: %v", staged)
	}
	l.scenarioPair("run-2", true, true)
	capture := filepath.Join(l.evidence, "run-2/counter.click", "after-"+publishCandidate[:12], "capture.json")
	record := l.readJSON(capture)
	record["redaction"] = map[string]any{"patterns": 4, "count": 1, "labelled": true}
	l.writeJSON(capture, record)
	l.wantContains(refusals(l.plan(1, "run-2"), "counter.click"), "cannot be redacted")
	l.wantNoEdits()
}

func TestPublishOldGhDefersPublicationAndTouchesNothing(t *testing.T) {
	l := newPublishLab(t)
	l.scenarioPair("run-1", true, true)
	l.plan(0, "run-1")
	l.github(prose, map[string]any{"version": "2.78.0"})
	result := l.publish(2)
	l.wantOutcome(result, "deferred")
	l.wantContains(asString(result["reason"]), "2.99.0+")
	l.wantNoEdits()
	l.wantBody(prose)
	if _, err := os.Stat(l.receipts); err == nil && len(asMap(l.readJSON(l.receipts)["attachments"])) != 0 {
		t.Fatalf("receipts recorded attachments: %v", l.readJSON(l.receipts))
	}
	if info, err := os.Stat(filepath.Join(l.publishDir, "media")); err != nil || !info.IsDir() {
		t.Fatalf("local publish copies must stay available for a later publication: %v", err)
	}
}

func TestPublishPrivateRepositoryWithoutWritePermissionOrWrongVisibilityIsRefused(t *testing.T) {
	l := newPublishLab(t)
	l.scenarioPair("run-1", true, true)
	l.plan(0, "run-1")
	l.github(prose, map[string]any{"visibility": "PRIVATE", "viewer_permission": "READ"})
	l.wantContains(asString(l.publishAs(1, "public")["reason"]), "is private; --visibility public was declared")
	l.wantContains(asString(l.publishAs(1, "private")["reason"]), "READ permission")
	l.wantNoEdits()
	l.github(prose, map[string]any{"visibility": "PRIVATE", "viewer_permission": "WRITE"})
	l.wantOutcome(l.publishAs(0, "private"), "published")
}

func TestPublishEditFailureLeavesBodyIntactAndReportsUnpublished(t *testing.T) {
	l := newPublishLab(t)
	l.scenarioPair("run-1", true, true)
	l.plan(0, "run-1")
	l.github(prose, map[string]any{"edit": "exit"})
	result := l.publish(1)
	l.wantOutcome(result, "failed")
	l.wantContains(asString(result["reason"]), "intact")
	l.wantBody(prose)
	if len(asSlice(result["uploaded"])) != 0 {
		t.Fatalf("uploaded %v", result["uploaded"])
	}
	for _, scenario := range asSlice(l.readJSON(l.planPath())["scenarios"]) {
		for _, m := range asSlice(asMap(scenario)["media"]) {
			if _, err := os.Stat(filepath.Join(l.publishDir, "media", asString(asMap(m)["publish_copy"]))); err != nil {
				t.Fatalf("publish copy gone: %v", err)
			}
		}
	}
}

func TestPublishPartialUploadRestoresThePreviousBodyAndKeepsReceiptsForReuse(t *testing.T) {
	l := newPublishLab(t)
	l.scenarioPair("run-1", true, true)
	l.plan(0, "run-1")
	l.github(prose, map[string]any{"edit": "partial"})
	result := l.publish(1)
	l.wantOutcome(result, "failed")
	if result["restored"] != true || len(asSlice(result["uploaded"])) != 1 {
		t.Fatalf("result %v", result)
	}
	l.wantBody(prose)
	if attachments := asMap(l.readJSON(l.receipts)["attachments"]); len(attachments) != 1 {
		t.Fatalf("attachments %v", attachments)
	}
	l.github(prose, map[string]any{"edit": "ok"})
	result = l.publish(0)
	l.wantOutcome(result, "published")
	if len(asSlice(result["uploaded"])) != 3 || len(asSlice(result["reused"])) != 1 {
		t.Fatalf("result %v", result)
	}
}

func TestPublishTimeoutBeforeAnyWriteIsReportedFailedWithBodyIntact(t *testing.T) {
	l := newPublishLab(t)
	l.scenarioPair("run-1", true, true)
	l.plan(0, "run-1")
	l.github(prose, map[string]any{"edit": "hang"})
	result := l.publish(1)
	l.wantOutcome(result, "failed")
	l.wantContains(asString(result["reason"]), "did not finish")
	l.wantBody(prose)
}

func TestPublishTimeoutAfterAWriteIsReconciledFromRemoteState(t *testing.T) {
	l := newPublishLab(t)
	l.scenarioPair("run-1", true, true)
	l.plan(0, "run-1")
	l.github(prose, map[string]any{"edit": "hang-after-write"})
	result := l.publish(0)
	l.wantOutcome(result, "published")
	if len(asSlice(result["uploaded"])) != 4 {
		t.Fatalf("uploaded %v", result["uploaded"])
	}
	var kinds []string
	for _, event := range asSlice(l.readJSON(l.receipts)["events"]) {
		kinds = append(kinds, asString(asMap(event)["kind"]))
	}
	if len(kinds) == 0 || kinds[0] != "edit-attempt" || !contains(kinds, "published") {
		t.Fatalf("events %v", kinds)
	}
	if !strings.HasPrefix(l.body(), proseTop) {
		t.Fatalf("body:\n%s", l.body())
	}
}

func TestPublishHeadMovedPastCandidateIsRefusedUnlessLabelled(t *testing.T) {
	l := newPublishLab(t)
	l.scenarioPair("run-1", true, true)
	l.plan(0, "run-1")
	l.github(prose, map[string]any{"pr": map[string]any{"number": 7, "state": "OPEN", "head_sha": strings.Repeat("d", 40), "body": prose}})
	l.wantContains(asString(l.publish(1)["reason"]), "is not the candidate")
	l.wantNoEdits()
	l.wantOutcome(l.publish(0, "--allow-head-mismatch"), "published")
}

func TestPublishAVIOnlyScreencastPublishesScreenshotsAndNamesTheVideoAsUnsupported(t *testing.T) {
	l := newPublishLab(t)
	const run, scenario = "run-1", "counter.click"
	avi := func(fill byte) mediaFile {
		return mediaFile{"screencast.avi", append([]byte("RIFF"), bytes.Repeat([]byte{fill}, 20)...), map[string]any{"type": "video/x-msvideo", "role": "screencast", "derived": false, "frames": 3}}
	}
	before := l.capture(run, scenario, "before", publishBase, []mediaFile{screenshot(t, 4, 4, 1, 1, 1), avi(0)}, "fail", 0)
	after := l.capture(run, scenario, "after", publishCandidate, []mediaFile{screenshot(t, 4, 4, 2, 2, 2), avi(1)}, "pass", 0)
	l.comparison(run, scenario, &before, &after, "red-green", "the base fails the user path and the candidate passes it")
	plan := l.plan(1, run)
	if len(asSlice(plan["publishable_scenarios"])) != 0 {
		t.Fatalf("publishable %v", plan["publishable_scenarios"])
	}
	l.wantContains(refusals(plan, "counter.click"), "GitHub renders mp4, mov, and webm only")
}

func TestPublishDryRunComputesEverythingAndEditsNothing(t *testing.T) {
	l := newPublishLab(t)
	l.scenarioPair("run-1", true, true)
	l.plan(0, "run-1")
	result := l.publish(0, "--dry-run")
	l.wantOutcome(result, "planned")
	if len(asSlice(result["to_upload"])) != 4 {
		t.Fatalf("to_upload %v", result["to_upload"])
	}
	l.wantContains(asString(result["body_preview"]), blockStart)
	l.wantNoEdits()
	l.wantBody(prose)
}

func TestPublishBlockCommandRendersWithoutGitHub(t *testing.T) {
	l := newPublishLab(t)
	l.scenarioPair("run-1", true, true)
	l.plan(0, "run-1")
	_, rendered := l.runPublish(0, "block", "--plan", l.planPath())
	if !strings.HasPrefix(rendered, blockStart) {
		t.Fatalf("block:\n%s", rendered)
	}
	l.wantContains(rendered, "](./counter.click--after--")
	_, listing := l.runPublish(0, "block", "--plan", l.planPath(), "--attach-list")
	if len(strings.Fields(listing)) != 4 {
		t.Fatalf("attach list:\n%s", listing)
	}
	if calls := l.calls(); len(calls) != 0 {
		t.Fatalf("block called gh: %v", calls)
	}
}
