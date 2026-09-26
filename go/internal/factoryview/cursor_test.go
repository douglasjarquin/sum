package factoryview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/factoryview/fixture"
	"github.com/douglasjarquin/sum/go/internal/returns"
)

func newOutcomes(d Digest) []Outcome {
	var out []Outcome
	for _, row := range d.Rows {
		for _, o := range row.Outcomes {
			if o.New {
				out = append(out, o)
			}
		}
	}
	return out
}

func allOutcomes(d Digest) []Outcome {
	var out []Outcome
	for _, row := range d.Rows {
		out = append(out, row.Outcomes...)
	}
	return out
}

func TestCursorRoundTripAndIdentityBasedNewness(t *testing.T) {
	home := standardHome(t)
	first := build(t, home, Options{})
	if first.Cursor == "" || first.Resync != nil || first.Since {
		t.Fatalf("first read: %+v", first)
	}
	if got := newOutcomes(first); got != nil {
		t.Fatalf("without a cursor nothing is labelled new: %+v", got)
	}
	parsed, err := Parse(first.Cursor)
	if err != nil || parsed.Kind != CursorKind || parsed.Installation != "inst-fixture" || len(parsed.Leaves) != 7 || len(parsed.Outcomes) != len(allOutcomes(first)) {
		t.Fatalf("parsed cursor: %+v %v", parsed, err)
	}
	again := build(t, home, Options{Since: first.Cursor})
	if again.Resync != nil || !again.Since || len(newOutcomes(again)) != 0 || len(again.Deltas) != 0 {
		t.Fatalf("unchanged records: resync=%+v new=%+v deltas=%+v", again.Resync, newOutcomes(again), again.Deltas)
	}
	if again.Cursor != first.Cursor {
		t.Fatal("an unchanged snapshot yields the same cursor")
	}
	// A late record with an old timestamp is still new: newness is identity, never time.
	late := fixture.StandardTasks()[4] // t-b2, PR #7 open, no report
	late.Report = true
	late.ReportAt = "2025-12-01T00:00:00+00:00"
	if err := fixture.WriteTask(home, late); err != nil {
		t.Fatal(err)
	}
	third := build(t, home, Options{Since: first.Cursor})
	got := newOutcomes(third)
	if third.Resync != nil || len(got) != 1 || got[0].Kind != "reported" || got[0].Source.Task != "t-b2bbbbbbbbbb" || got[0].Source.At != "2025-12-01T00:00:00+00:00" {
		t.Fatalf("late arrival: resync=%+v new=%+v", third.Resync, got)
	}
	if len(third.Deltas) != 1 || third.Deltas[0].Task != "t-b2bbbbbbbbbb" || third.Deltas[0].Reason != DeltaChanged {
		t.Fatalf("changed leaf: %+v", third.Deltas)
	}
	fourth := build(t, home, Options{Since: third.Cursor})
	if len(newOutcomes(fourth)) != 0 || len(fourth.Deltas) != 0 {
		t.Fatalf("the next cursor covers the late record: %+v %+v", newOutcomes(fourth), fourth.Deltas)
	}
}

func TestReopenedTaskOrNewCandidateIsANewDelta(t *testing.T) {
	home := standardHome(t)
	first := build(t, home, Options{})
	task := fixture.StandardTasks()[0] // t-a1
	task.Candidate = "c2a1"
	task.Evidence = append(task.Evidence, fixture.Evidence{ID: "e-a1-hand2", Kind: "handoff", Source: "worker", At: "2026-02-05T09:00:00+00:00", Candidate: "c2a1"})
	if err := fixture.WriteTask(home, task); err != nil {
		t.Fatal(err)
	}
	d := build(t, home, Options{Since: first.Cursor})
	if d.Resync != nil || len(d.Deltas) != 1 || d.Deltas[0].Task != "t-a1aaaaaaaaaa" || d.Deltas[0].Reason != DeltaChanged {
		t.Fatalf("new candidate: resync=%+v deltas=%+v", d.Resync, d.Deltas)
	}
	// The old candidate's verification and review no longer speak for the new candidate; the new report is new.
	var reported int
	for _, o := range newOutcomes(d) {
		if o.Kind == "reported" && o.Source.Candidate == "c2a1" {
			reported++
		}
	}
	if reported != 1 {
		t.Fatalf("new candidate report: %+v", newOutcomes(d))
	}
}

func TestSourceShrinkAndReplacedHistoryResync(t *testing.T) {
	home := standardHome(t)
	first := build(t, home, Options{})
	if err := os.RemoveAll(filepath.Join(home, "tasks", "t-d1dddddddddd")); err != nil {
		t.Fatal(err)
	}
	shrunk := build(t, home, Options{Since: first.Cursor})
	if shrunk.Resync == nil || !strings.Contains(shrunk.Resync.Reason, "t-d1dddddddddd") || len(newOutcomes(shrunk)) != 0 || len(shrunk.Rows) == 0 {
		t.Fatalf("source shrink: %+v new=%+v", shrunk.Resync, newOutcomes(shrunk))
	}
	if shrunk.Cursor == "" {
		t.Fatal("a resync still returns a fresh cursor for the full summary")
	}
	home2 := standardHome(t)
	first2 := build(t, home2, Options{})
	task := fixture.StandardTasks()[0]
	task.Evidence = task.Evidence[:1] // history replaced: fewer records than the cursor's leaf recorded
	if err := fixture.WriteTask(home2, task); err != nil {
		t.Fatal(err)
	}
	rotated := build(t, home2, Options{Since: first2.Cursor})
	if rotated.Resync == nil || !strings.Contains(rotated.Resync.Reason, "history") || len(newOutcomes(rotated)) != 0 {
		t.Fatalf("history rotation: %+v", rotated.Resync)
	}
}

func TestUnreadableSourceIsExcludedFromCoverageAndReturns(t *testing.T) {
	home := standardHome(t)
	first := build(t, home, Options{})
	path := filepath.Join(home, "tasks", "t-a2aaaaaaaaaa", "task.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	partial := build(t, home, Options{Since: first.Cursor})
	if partial.Resync != nil || partial.Complete || len(partial.Gaps) != 1 && len(rowFor(t, partial, "a/repo").Gaps) != 1 {
		t.Fatalf("partial read: resync=%+v complete=%v gaps=%+v", partial.Resync, partial.Complete, partial.Gaps)
	}
	parsed, err := Parse(partial.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaf := range parsed.Leaves {
		if leaf.Task == "t-a2aaaaaaaaaa" {
			t.Fatal("an unreadable source must not enter coverage")
		}
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	restored := build(t, home, Options{Since: partial.Cursor})
	if restored.Resync != nil || len(restored.Deltas) != 1 || restored.Deltas[0].Task != "t-a2aaaaaaaaaa" || restored.Deltas[0].Reason != DeltaNewSource {
		t.Fatalf("the source appears again once readable: resync=%+v deltas=%+v", restored.Resync, restored.Deltas)
	}
	// Its outcomes were rendered before the outage, so they are covered and not new; the leaf is what returns.
	for _, o := range newOutcomes(restored) {
		if o.Source.Task == "t-a2aaaaaaaaaa" {
			t.Fatalf("previously rendered outcome claimed new: %+v", o)
		}
	}
}

func TestForeignOrUnsupportedTokensResync(t *testing.T) {
	home := standardHome(t)
	first := build(t, home, Options{})
	scoped := build(t, home, Options{Project: "a/repo"})
	cases := map[string]string{}
	cases["foreign project"] = scoped.Cursor
	parsed, _ := Parse(first.Cursor)
	parsed.Installation = "another-installation"
	cases["foreign installation"] = mustToken(t, parsed)
	parsed, _ = Parse(first.Cursor)
	parsed.Schema = Schema + 1
	cases["unsupported schema"] = mustToken(t, parsed)
	parsed, _ = Parse(first.Cursor)
	parsed.Page.Truncated = true
	cases["truncated coverage"] = mustToken(t, parsed)
	wake := &returns.Boundary{Schema: returns.WakeSchema, Installation: "inst-fixture", Incarnation: json.RawMessage("null"), Included: []returns.WakeCovered{}, Omitted: []returns.WakeCovered{}}
	wakeToken, err := wake.Token()
	if err != nil {
		t.Fatal(err)
	}
	cases["wake receipt"] = wakeToken
	cases["garbage"] = "not-a-token"
	for name, token := range cases {
		d := build(t, home, Options{Since: token})
		if d.Resync == nil || d.Resync.Reason == "" || len(newOutcomes(d)) != 0 || len(d.Rows) != 3 || d.Cursor == "" {
			t.Fatalf("%s: resync=%+v rows=%d new=%d", name, d.Resync, len(d.Rows), len(newOutcomes(d)))
		}
	}
	if _, err := returns.ParseBoundary(first.Cursor); err == nil {
		t.Fatal("a digest cursor must never verify as a wake boundary")
	}
	if d := build(t, home, Options{Project: "a/repo", Since: scoped.Cursor}); d.Resync != nil {
		t.Fatalf("matching project scope: %+v", d.Resync)
	}
}

func mustToken(t *testing.T, c *Cursor) string {
	t.Helper()
	token, _, err := c.Token()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestPagingIsExplicitAndNeverClaimsTheExcess(t *testing.T) {
	home := standardHome(t)
	first := build(t, home, Options{Limit: 2})
	total := len(allOutcomes(build(t, home, Options{})))
	if first.Page.Limit != 2 || first.Page.Rendered != 2 || first.Page.Total != total || !first.Page.Continuation || len(allOutcomes(first)) != 2 {
		t.Fatalf("page: %+v rendered=%d", first.Page, len(allOutcomes(first)))
	}
	if first.Counts.Decisions != 1 || first.Counts.Tasks != 7 {
		t.Fatalf("global counts come from the full snapshot before paging: %+v", first.Counts)
	}
	parsed, _ := Parse(first.Cursor)
	if len(parsed.Outcomes) != 2 {
		t.Fatalf("the cursor covers only what was rendered: %+v", parsed.Outcomes)
	}
	// Meanwhile: one source becomes unreadable and a new task arrives behind the page boundary.
	if err := os.WriteFile(filepath.Join(home, "tasks", "t-d1dddddddddd", "task.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	arrival := fixture.Task{ID: "t-b3bbbbbbbbbb", Project: fixture.B, Status: "running", Candidate: "c1b3", Report: true, ReportAt: "2026-02-06T00:00:00+00:00"}
	if err := fixture.WriteTask(home, arrival); err != nil {
		t.Fatal(err)
	}
	second := build(t, home, Options{Limit: 2, Since: first.Cursor})
	if second.Resync != nil || second.Complete || !second.Page.Continuation || second.Page.Rendered != 2 {
		t.Fatalf("second page: resync=%+v complete=%v page=%+v", second.Resync, second.Complete, second.Page)
	}
	for _, o := range allOutcomes(second) {
		if !o.New {
			t.Fatalf("a page after a partial cursor renders uncovered outcomes first: %+v", o)
		}
	}
	// Walk the continuation until it ends; the arrival must surface as new exactly once and never be claimed covered early.
	seen := map[string]int{}
	cursor := first.Cursor
	for i := 0; i < 20; i++ {
		page := build(t, home, Options{Limit: 2, Since: cursor})
		if page.Resync != nil {
			t.Fatalf("page %d resync: %+v", i, page.Resync)
		}
		for _, o := range newOutcomes(page) {
			seen[o.Identity]++
		}
		cursor = page.Cursor
		if !page.Page.Continuation {
			break
		}
	}
	arrivalID := ""
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("outcome %s labelled new %d times", id, n)
		}
		if strings.Contains(id, "t-b3bbbbbbbbbb") {
			arrivalID = id
		}
	}
	if arrivalID == "" {
		t.Fatalf("the arrival never surfaced: %v", seen)
	}
	final := build(t, home, Options{Limit: 2, Since: cursor})
	if len(newOutcomes(final)) != 0 || final.Page.Continuation {
		t.Fatalf("settled: new=%+v page=%+v", newOutcomes(final), final.Page)
	}
	parsed, _ = Parse(cursor)
	for _, leaf := range parsed.Leaves {
		if leaf.Task == "t-d1dddddddddd" {
			t.Fatal("unreadable source claimed covered")
		}
	}
}

func TestTokenBoundLabelsTruncationAndParsesAsResync(t *testing.T) {
	c := &Cursor{Schema: Schema, Kind: CursorKind, Installation: "x", Page: Page{Limit: 20}}
	for i := 0; i < 2000; i++ {
		c.Outcomes = append(c.Outcomes, fmt.Sprintf("observed-merged:a/repo#%d", i))
		c.Leaves = append(c.Leaves, Leaf{Task: fmt.Sprintf("t-%012d", i), Digest: "abcdef012345", Evidence: 3})
	}
	token, truncated, err := c.Token()
	if err != nil {
		t.Fatal(err)
	}
	if len(token) > MaxTokenBytes || !truncated {
		t.Fatalf("token exceeds the bound or is not labelled truncated: %d %v", len(token), truncated)
	}
	parsed, err := Parse(token)
	if err != nil || !parsed.Page.Truncated {
		t.Fatalf("a bounded token that cannot retain coverage is labelled truncated: %+v %v", parsed, err)
	}
	raw, _ := json.Marshal(parsed)
	if strings.Contains(string(raw), "observed-merged:a/repo#1999") {
		t.Fatal("truncated coverage must not pretend to retain every outcome")
	}
}

func TestLimitBounds(t *testing.T) {
	home := standardHome(t)
	if d := build(t, home, Options{}); d.Page.Limit != DefaultLimit {
		t.Fatalf("default limit: %+v", d.Page)
	}
	if d := build(t, home, Options{Limit: 500}); d.Page.Limit != MaxLimit {
		t.Fatalf("max limit: %+v", d.Page)
	}
}

func TestOnlyAGapOnItsOwnTaskRetainsAVanishedOutcome(t *testing.T) {
	const reported = "reported:t-d1dddddddddd:c1g1"
	home := standardHome(t)
	first := build(t, home, Options{})
	parsed, err := Parse(first.Cursor)
	if err != nil || !contains(parsed.Outcomes, reported) {
		t.Fatalf("initial coverage: %+v %v", parsed, err)
	}
	// The report vanishes from t-d1 while an unrelated task (t-b2) becomes unreadable.
	unrelated := filepath.Join(home, "tasks", "t-b2bbbbbbbbbb", "task.json")
	original, err := os.ReadFile(unrelated)
	if err != nil {
		t.Fatal(err)
	}
	d1 := fixture.StandardTasks()[5]
	d1.Report = false
	if err := fixture.WriteTask(home, d1); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelated, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	pruned := build(t, home, Options{Since: first.Cursor})
	if pruned.Resync != nil || len(pruned.Gaps)+len(rowFor(t, pruned, "b/repo").Gaps) != 1 {
		t.Fatalf("unrelated gap: resync=%+v gaps=%+v", pruned.Resync, pruned.Gaps)
	}
	if parsed, err = Parse(pruned.Cursor); err != nil || contains(parsed.Outcomes, reported) {
		t.Fatalf("a gap on another task must not pin a vanished outcome: %v %v", parsed.Outcomes, err)
	}
	// A gap on the outcome's own task keeps it covered, so it is not relabelled new when the source returns.
	if err := os.WriteFile(unrelated, original, 0o600); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(home, "tasks", "t-d1dddddddddd", "task.json")
	if err := os.WriteFile(own, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	pinned := build(t, home, Options{Since: first.Cursor})
	if pinned.Resync != nil {
		t.Fatalf("own gap: resync=%+v", pinned.Resync)
	}
	if parsed, err = Parse(pinned.Cursor); err != nil || !contains(parsed.Outcomes, reported) {
		t.Fatalf("a gap on the outcome's own task retains its coverage: %v %v", parsed.Outcomes, err)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
