package factoryview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/factoryview/fixture"
	"github.com/douglasjarquin/sum/go/internal/inboxview"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func standardHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if _, err := fixture.WriteStandard(home); err != nil {
		t.Fatal(err)
	}
	return home
}

func snapshotOf(t *testing.T, home string) (inboxview.Snapshot, string) {
	t.Helper()
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := st.Instance()
	if err != nil {
		t.Fatal(err)
	}
	return inboxview.Read(st), instance
}

func build(t *testing.T, home string, opts Options) Digest {
	t.Helper()
	snapshot, instance := snapshotOf(t, home)
	return Build(snapshot, instance, opts)
}

func rowFor(t *testing.T, d Digest, project string) FactoryRow {
	t.Helper()
	for _, row := range d.Rows {
		if row.Project == project {
			return row
		}
	}
	t.Fatalf("no row for %s in %+v", project, d.Rows)
	return FactoryRow{}
}

func kinds(row FactoryRow) map[string][]Outcome {
	out := map[string][]Outcome{}
	for _, o := range row.Outcomes {
		out[o.Kind] = append(out[o.Kind], o)
	}
	return out
}

func TestStandardRowsShowCurrentIssueStageAndOwnerPerProject(t *testing.T) {
	home := standardHome(t)
	d := build(t, home, Options{})
	if d.Schema != Schema || d.Installation != "inst-fixture" || d.Counts.Decisions != 1 || !d.Complete {
		t.Fatalf("envelope: %+v", d)
	}
	a := rowFor(t, d, "a/repo")
	if a.Lane != "held" || a.CurrentIssue != "41" || a.CurrentTask != "t-a1aaaaaaaaaa" || a.Stage == "" || a.Stage == "settled" {
		t.Fatalf("a/repo row: %+v", a)
	}
	if a.Blocker != nil || a.ActionOwner == "human decision" {
		t.Fatalf("a/repo has no saved decision or gate: %+v", a)
	}
	if a.Observed.LastTickAt != "2026-02-01T09:00:00+00:00" || a.Observed.PRObservedAt != "2026-02-01T11:20:00+00:00" || a.Observed.NextTickAt != "" {
		t.Fatalf("a/repo observed: %+v", a.Observed)
	}
	if a.Next.Intake != IntakeUnknown || strings.Join(a.Next.Command, " ") != "context t-a1aaaaaaaaaa --role coordinator" {
		t.Fatalf("a/repo next: %+v", a.Next)
	}
	b := rowFor(t, d, "b/repo")
	if b.Lane != "held" || b.CurrentIssue != "43" || b.CurrentTask != "t-b1bbbbbbbbbb" || b.ActionOwner != "human decision" {
		t.Fatalf("b/repo row: %+v", b)
	}
	if b.Blocker == nil || b.Blocker.Kind != "question" || b.Blocker.Ref != "question:q-compat" || len(b.Blocker.Detail) == 0 || b.Blocker.Detail[0] != "context" {
		t.Fatalf("b/repo blocker: %+v", b.Blocker)
	}
	if b.Observed.NextTickAt != "2026-02-02T12:05:00+00:00" || !strings.Contains(b.Observed.Note, "recorded") {
		t.Fatalf("next_tick_at must be labelled recorded, not scheduled: %+v", b.Observed)
	}
	g := rowFor(t, d, "ghe.example.com/b/repo")
	if g.Lane != "none" || g.CurrentTask != "" || g.CurrentIssue != "" {
		t.Fatalf("non-factory project row guessed a lane: %+v", g)
	}
	for _, row := range d.Rows {
		if row.Project == "" {
			t.Fatal("standalone tasks are not a project row")
		}
		for _, o := range row.Outcomes {
			if o.Project != row.Project {
				t.Fatalf("cross-project outcome %+v under %s", o, row.Project)
			}
		}
	}
}

func TestOutcomeChainIsDistinguishableFromOneTasksRecords(t *testing.T) {
	home := standardHome(t)
	a := rowFor(t, build(t, home, Options{}), "a/repo")
	got := map[string][]Outcome{}
	for _, o := range a.Outcomes {
		if o.Source.Task == "t-a1aaaaaaaaaa" {
			got[o.Kind] = append(got[o.Kind], o)
		}
	}
	for _, kind := range []string{"reported", "verified", "review-accepted", "pr-open"} {
		list := got[kind]
		if len(list) != 1 || list[0].Source.Candidate != "c1a1" || list[0].Source.At == "" || list[0].Attribution != "factory" {
			t.Fatalf("%s: %+v", kind, list)
		}
	}
	if got["reported"][0].Source.Kind != "report" || got["verified"][0].Source.ID != "e-a1-ver" || got["review-accepted"][0].Source.ID != "e-a1-rev" || got["pr-open"][0].Source.Kind != "pr" {
		t.Fatalf("sources: %+v", got)
	}
	if len(got) != 4 {
		t.Fatalf("an open PR is not merged, cleanup-pending or lane-free: %+v", got)
	}
	got = kinds(a)
	if len(got["observed-merged"]) != 1 || got["observed-merged"][0].Identity != "observed-merged:a/repo#5" || !strings.Contains(got["observed-merged"][0].Attribution, "factory linkage not recorded") {
		t.Fatalf("observed-merged: %+v", got["observed-merged"])
	}
	if len(got["lane-free"]) != 2 || got["cleanup-pending"] != nil {
		t.Fatalf("lane-free/cleanup: %+v", got)
	}
	for _, o := range a.Outcomes {
		if len(o.Detail) == 0 || o.Identity == "" || o.Source.At == "" {
			t.Fatalf("every outcome carries a source time and a detail route: %+v", o)
		}
	}
}

// AE5: a merge request result is not a merged observation; only a saved PR observation with a merge commit is.
func TestMergeRequestResultIsNotObservedMerge(t *testing.T) {
	home := standardHome(t)
	// factory.Merge returns merged:true without touching task records; the saved PR observation is still open.
	before := kinds(rowFor(t, build(t, home, Options{}), "a/repo"))
	if len(before["observed-merged"]) != 1 || before["observed-merged"][0].Identity != "observed-merged:a/repo#5" || len(before["pr-open"]) != 1 {
		t.Fatalf("before observation: %+v", before)
	}
	task := fixture.StandardTasks()[0]
	task.PR.State = "merged"
	task.PR.MergeCommit = "m7m7m7"
	task.PR.ObservedAt = "2026-02-01T12:00:00+00:00"
	task.Status = "archived"
	task.Cleanup = &fixture.Cleanup{State: "complete", At: "2026-02-01T12:30:00+00:00"}
	if err := fixture.WriteTask(home, task); err != nil {
		t.Fatal(err)
	}
	factories := fixture.StandardFactories()
	factories[0].Held = nil // lane released after the merge
	if err := fixture.WriteFactory(home, factories); err != nil {
		t.Fatal(err)
	}
	after := rowFor(t, build(t, home, Options{}), "a/repo")
	got := kinds(after)
	if after.Lane != "enabled" || after.CurrentTask != "" || after.CurrentIssue != "" || after.Stage != "" {
		t.Fatalf("released lane row: %+v", after)
	}
	if len(got["observed-merged"]) != 2 || len(got["pr-open"]) != 0 {
		t.Fatalf("after observation: %+v", got)
	}
	var merged, free *Outcome
	for i := range after.Outcomes {
		o := &after.Outcomes[i]
		if o.Source.Task == "t-a1aaaaaaaaaa" && o.Kind == "observed-merged" {
			merged = o
		}
		if o.Source.Task == "t-a1aaaaaaaaaa" && o.Kind == "lane-free" {
			free = o
		}
	}
	if merged == nil || free == nil || merged.Source.At != "2026-02-01T12:00:00+00:00" || merged.Attribution != "project (factory linkage not recorded)" || free.Attribution != "project (factory linkage not recorded)" {
		t.Fatalf("archived task outcomes: merged=%+v free=%+v", merged, free)
	}
	if strings.Join(after.Next.Command, " ") != "factory tick --project a/repo" {
		t.Fatalf("free enabled lane next: %+v", after.Next)
	}
}

func TestDuplicatePRNumbersStayDistinctAndAliasesCountOnce(t *testing.T) {
	home := standardHome(t)
	d := build(t, home, Options{})
	a, b := kinds(rowFor(t, d, "a/repo")), kinds(rowFor(t, d, "b/repo"))
	if len(a["observed-merged"]) != 1 {
		t.Fatalf("PR #5 observed under two spellings must count once: %+v", a["observed-merged"])
	}
	if len(a["pr-open"]) != 1 || len(b["pr-open"]) != 2 || a["pr-open"][0].Identity == b["pr-open"][0].Identity {
		t.Fatalf("PR #7 on a/repo and b/repo are distinct: a=%+v b=%+v", a["pr-open"], b["pr-open"])
	}
	seen := map[string]bool{}
	for _, row := range d.Rows {
		for _, o := range row.Outcomes {
			if seen[o.Identity] {
				t.Fatalf("duplicate identity %s", o.Identity)
			}
			seen[o.Identity] = true
		}
	}
	var factoryPR, projectPR int
	for _, o := range b["pr-open"] {
		switch o.Attribution {
		case "factory":
			factoryPR++
		case "project (factory linkage not recorded)":
			projectPR++
		}
	}
	if factoryPR != 1 || projectPR != 1 {
		t.Fatalf("lane record proves t-b1 only: %+v", b["pr-open"])
	}
}

func TestCleanupPendingAndStaleCIAreExplicit(t *testing.T) {
	home := standardHome(t)
	task := fixture.StandardTasks()[3] // t-b1: open PR #9
	task.PR.State = "merged"
	task.PR.MergeCommit = "m9m9m9"
	task.PR.ObservedAt = "2026-02-02T13:00:00+00:00"
	task.Evidence = append(task.Evidence, fixture.Evidence{ID: "e-b1-ci", Kind: "ci", Source: "coordinator", At: "2026-02-02T12:00:00+00:00", Candidate: "c1b1", Fields: map[string]any{"head_sha": "c1b1", "outcome": "pass", "checks": []any{}}})
	if err := fixture.WriteTask(home, task); err != nil {
		t.Fatal(err)
	}
	b := rowFor(t, build(t, home, Options{}), "b/repo")
	got := kinds(b)
	if len(got["cleanup-pending"]) != 1 || got["cleanup-pending"][0].Source.Task != "t-b1bbbbbbbbbb" || got["cleanup-pending"][0].Attribution != "factory" {
		t.Fatalf("cleanup-pending: %+v", got)
	}
	if len(got["lane-free"]) != 0 {
		t.Fatalf("a gated lane still names the task; it is not free: %+v", got["lane-free"])
	}
	if b.Observed.CIObservedAt != "2026-02-02T12:00:00+00:00" || !b.Observed.CIStale {
		t.Fatalf("CI observed before the latest record write is stale: %+v", b.Observed)
	}
}

func TestProjectFocusKeepsGlobalCountsAndOtherProjectsDecisions(t *testing.T) {
	home := standardHome(t)
	d := build(t, home, Options{Project: "a/repo"})
	if len(d.Rows) != 1 || d.Rows[0].Project != "a/repo" || d.Project != "a/repo" {
		t.Fatalf("focus rows: %+v", d.Rows)
	}
	if d.Counts.Decisions != 1 || d.Counts.Tasks != 7 {
		t.Fatalf("global counts precede focus: %+v", d.Counts)
	}
	if len(d.NeedsYou) != 1 || d.NeedsYou[0].Project != "b/repo" || d.NeedsYou[0].Detail[0] != "context" {
		t.Fatalf("other project's decision keeps its route: %+v", d.NeedsYou)
	}
}

func TestUnreadableSourceIsAGapNotAHealthyRow(t *testing.T) {
	home := standardHome(t)
	if err := os.WriteFile(filepath.Join(home, "tasks", "t-a1aaaaaaaaaa", "task.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := build(t, home, Options{})
	a := rowFor(t, d, "a/repo")
	if d.Complete || len(a.Gaps) != 1 || a.Gaps[0].TaskID != "t-a1aaaaaaaaaa" {
		t.Fatalf("gap: complete=%v row=%+v", d.Complete, a)
	}
	if a.CurrentTask != "t-a1aaaaaaaaaa" || a.Stage != "" || a.ActionOwner != "" {
		t.Fatalf("held lane with an unreadable task: %+v", a)
	}
	for _, o := range a.Outcomes {
		if o.Source.Task == "t-a1aaaaaaaaaa" {
			t.Fatalf("no outcomes from an unreadable source: %+v", o)
		}
	}
}
