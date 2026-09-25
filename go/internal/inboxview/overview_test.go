package inboxview

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/presentation"
)

func record(t *testing.T, raw string) *ordjson.Object {
	t.Helper()
	parsed, err := ordjson.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return parsed.(*ordjson.Object)
}

func question(task, project, id, state, text string) presentation.Item {
	return presentation.Classify(presentation.Fact{Source: presentation.Source{TaskID: task, Kind: presentation.Question, ID: "question:" + id}, ProjectID: project, State: state, At: "2026-01-0" + id[len(id)-1:] + "T00:00:00Z", Text: text, Details: []presentation.Detail{{Section: "decisions", Ref: id, Path: "task.json"}}})
}

func pipelineItem(task, project, stage, status string) presentation.Item {
	return presentation.Classify(presentation.Fact{Source: presentation.Source{TaskID: task, Kind: presentation.Pipeline, ID: stage}, ProjectID: project, State: status, Details: []presentation.Detail{{Section: "pipeline", Ref: stage}}})
}

// countSnapshot mirrors Read's counting so pure fixtures carry the same global counts a real read would.
func countSnapshot(s *Snapshot) {
	s.Items = nil
	for _, task := range s.Tasks {
		s.Items = append(s.Items, task.Items...)
	}
	s.Counts = Counts{Tasks: len(s.Tasks), UnknownSources: len(s.Gaps)}
	s.Complete = len(s.Gaps) == 0
	for _, gap := range s.Gaps {
		s.Items = append(s.Items, presentation.Classify(presentation.Fact{Source: presentation.Source{TaskID: gap.TaskID, Kind: presentation.Gap, ID: gap.Path}, Text: gap.Reason}))
	}
	for _, item := range s.Items {
		switch item.Kind {
		case presentation.Decision:
			s.Counts.Decisions++
		case presentation.Inspection:
			s.Counts.Inspection++
		case presentation.Resolved:
			s.Counts.Resolved++
		}
		if item.Owner == presentation.Worker {
			s.Counts.Worker++
		}
		if item.Owner == presentation.Coordinator {
			s.Counts.Coordinator++
		}
	}
}

func groupedSnapshot(t *testing.T) Snapshot {
	t.Helper()
	s := Snapshot{}
	s.Tasks = []Task{
		{ID: "t-cccccccccccc", ProjectID: "b/repo", Record: record(t, `{"id":"t-cccccccccccc","status":"running","repository":"/w/b-repo","branch":"feat/c"}`), Items: []presentation.Item{pipelineItem("t-cccccccccccc", "b/repo", "test", "pass")}},
		{ID: "t-aaaaaaaaaaaa", ProjectID: "a/repo", Record: record(t, `{"id":"t-aaaaaaaaaaaa","status":"running","repository":"/w/a-repo","branch":"feat/a","questions":[{"id":"q-1","status":"open"},{"id":"q-2","status":"answered"},{"id":"q-5","status":"open"}],"launch":{"observed":{"status":"running","at":"2026-01-05T00:00:00Z"}}}`), Items: []presentation.Item{question("t-aaaaaaaaaaaa", "a/repo", "q-1", "open", "Which \x1b[31mpath\x1b[0m?"), question("t-aaaaaaaaaaaa", "a/repo", "q-2", "answered", "done")}},
		{ID: "t-bbbbbbbbbbbb", ProjectID: "ghe.example.com/b/repo", Record: record(t, `{"id":"t-bbbbbbbbbbbb","status":"running","repository":"/w/ghe-repo"}`), Items: []presentation.Item{pipelineItem("t-bbbbbbbbbbbb", "ghe.example.com/b/repo", "test", "fail")}},
		{ID: "t-dddddddddddd", ProjectID: "", Record: record(t, `{"id":"t-dddddddddddd","status":"waiting"}`), Items: []presentation.Item{question("t-dddddddddddd", "", "q-3", "open", "Standalone?")}},
		{ID: "t-eeeeeeeeeeee", ProjectID: "z/old", Record: record(t, `{"id":"t-eeeeeeeeeeee","status":"archived"}`), Items: []presentation.Item{question("t-eeeeeeeeeeee", "z/old", "q-4", "applied", "old")}},
	}
	s.Gaps = []Gap{{TaskID: "t-cccccccccccc", Path: "/h/tasks/t-cccccccccccc/versions.json", Reason: "broken"}, {TaskID: "", Path: "/h/tasks/t-ffffffffffff/task.json", Reason: "unreadable"}}
	s.Factory = record(t, `{"projects":{"a/repo":{"lanes_held":[{"task":"t-aaaaaaaaaaaa","issue":7,"state":"running","claimed_at":"2026-01-01T00:00:00Z"}]}}}`)
	countSnapshot(&s)
	return s
}

func keys(groups []Group) []string {
	out := []string{}
	for _, group := range groups {
		out = append(out, group.Key)
	}
	return out
}

func TestBuildOverviewGroupsByCanonicalProjectIdentity(t *testing.T) {
	snapshot := groupedSnapshot(t)
	overview := BuildOverview(snapshot)
	// Same short repo name under two hosts/owners stays apart; decisions outrank inspection, which outranks health.
	if got := keys(overview.Groups); !reflect.DeepEqual(got, []string{"a/repo", "b/repo", "ghe.example.com/b/repo", "z/old"}) {
		t.Fatalf("groups = %v", got)
	}
	if overview.Standalone == nil || len(overview.Standalone.Tasks) != 1 || overview.Standalone.Tasks[0].ID != "t-dddddddddddd" {
		t.Fatalf("standalone = %+v", overview.Standalone)
	}
	if overview.Counts != snapshot.Counts || overview.Complete {
		t.Fatalf("global counts must be the snapshot's: %+v", overview.Counts)
	}
	if len(overview.Gaps) != 1 || overview.Gaps[0].TaskID != "" {
		t.Fatalf("global gaps = %+v", overview.Gaps)
	}
	byKey := map[string]Group{}
	for _, group := range overview.Groups {
		byKey[group.Key] = group
	}
	if g := byKey["b/repo"]; len(g.Gaps) != 1 || g.Healthy || g.Counts.Inspection != 1 || g.Tasks[0].State != "needs-attention" {
		t.Fatalf("task-scoped gap must land in its group: %+v", g)
	}
	if g := byKey["a/repo"]; g.Counts.Decisions != 1 || g.Counts.Worker != 1 || g.Factory == nil || len(g.Factory.Lanes) != 1 || g.Factory.Lanes[0].Issue != "7" || g.Factory.Lanes[0].State != "running" || g.Tasks[0].State != "needs-decision" || g.Tasks[0].Observed == nil || g.Tasks[0].Observed.Status != "running" || g.Tasks[0].Stage == "" {
		t.Fatalf("a/repo = %+v", g)
	}
	if g := byKey["ghe.example.com/b/repo"]; g.Counts.Inspection != 1 || g.Factory != nil || g.Tasks[0].Detail == nil {
		t.Fatalf("ghe group = %+v", g)
	}
	if g := byKey["z/old"]; !g.Healthy || !g.Tasks[0].Archived || g.Counts.Resolved != 1 {
		t.Fatalf("archived-only group = %+v", g)
	}
	// Group counts partition the global ones: nothing duplicated, nothing lost except the global gap.
	sum := Counts{}
	for _, group := range append(overview.Groups, *overview.Standalone) {
		sum.Decisions += group.Counts.Decisions
		sum.Inspection += group.Counts.Inspection
		sum.Coordinator += group.Counts.Coordinator
		sum.Worker += group.Counts.Worker
		sum.Resolved += group.Counts.Resolved
		sum.Tasks += group.Counts.Tasks
	}
	globalGapItems := len(overview.Gaps)
	if sum.Decisions != overview.Counts.Decisions || sum.Inspection+globalGapItems != overview.Counts.Inspection || sum.Coordinator+globalGapItems != overview.Counts.Coordinator || sum.Worker != overview.Counts.Worker || sum.Resolved != overview.Counts.Resolved || sum.Tasks != overview.Counts.Tasks {
		t.Fatalf("group counts %+v do not partition global %+v", sum, overview.Counts)
	}
	if overview.Coordinator.Items != overview.Counts.Coordinator {
		t.Fatalf("coordinator summary = %+v", overview.Coordinator)
	}
	if len(overview.NeedsYou) != 2 || overview.NeedsYou[0].Task != "t-aaaaaaaaaaaa" || overview.NeedsYou[1].Task != "t-dddddddddddd" {
		t.Fatalf("needs you = %+v", overview.NeedsYou)
	}
	row := overview.NeedsYou[0]
	if !reflect.DeepEqual(row.Answer, []string{"answer", "t-aaaaaaaaaaaa", "q-1", "--text"}) || !reflect.DeepEqual(row.Detail, []string{"context", "t-aaaaaaaaaaaa", "--section", "decisions", "--after", "0", "--limit", "1", "--max-chars", "0"}) || row.Project != "a/repo" || row.Text.Text != "Which \x1b[31mpath\x1b[0m?" {
		t.Fatalf("needs-you row = %+v", row)
	}
	raw, err := json.Marshal(overview)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"counts"`, `"complete"`, `"gaps"`, `"needs_you"`, `"groups"`, `"standalone"`, `"coordinator"`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("json lacks %s: %s", key, raw)
		}
	}
}

func TestArchivedTasksNeverOutrankLiveWork(t *testing.T) {
	snapshot := groupedSnapshot(t)
	snapshot.Tasks = append(snapshot.Tasks, Task{ID: "t-000000000000", ProjectID: "a/repo", Record: record(t, `{"id":"t-000000000000","status":"archived"}`), Items: []presentation.Item{question("t-000000000000", "a/repo", "q-9", "settled", "")}})
	countSnapshot(&snapshot)
	overview := BuildOverview(snapshot)
	group := overview.Groups[0]
	if group.Key != "a/repo" || group.Tasks[0].ID != "t-aaaaaaaaaaaa" || !group.Tasks[len(group.Tasks)-1].Archived {
		t.Fatalf("archived task above live work: %+v", group.Tasks)
	}
	if last := overview.Groups[len(overview.Groups)-1]; last.Key != "z/old" {
		t.Fatalf("archived-only group must sort last: %v", keys(overview.Groups))
	}
}

func TestFocusKeepsGlobalCountsGapsAndNeedsYou(t *testing.T) {
	snapshot := groupedSnapshot(t)
	overview := Focus(BuildOverview(snapshot), "b/repo")
	if overview.Project != "b/repo" || len(overview.Groups) != 1 || overview.Groups[0].Key != "b/repo" || overview.Standalone != nil {
		t.Fatalf("focus = %+v", overview)
	}
	if overview.Counts.Decisions != 2 || overview.Complete || overview.Counts.UnknownSources != 2 || len(overview.Gaps) != 1 || len(overview.NeedsYou) != 2 {
		t.Fatalf("focus dropped global facts: %+v", overview)
	}
	missing := Focus(BuildOverview(snapshot), "nobody/nothing")
	if len(missing.Groups) != 0 || missing.Counts.Decisions != 2 {
		t.Fatalf("unknown focus = %+v", missing)
	}
}

func TestTaskStateAndStageAgreeWithMetadataDerivation(t *testing.T) {
	task := Task{ID: "t-aaaaaaaaaaaa", Record: record(t, `{"id":"t-aaaaaaaaaaaa","status":"running"}`)}
	for _, tc := range []struct {
		items  []presentation.Item
		gapped bool
		want   string
	}{
		{nil, false, "running"},
		{nil, true, "needs-attention"},
		{[]presentation.Item{question("t", "", "q-1", "open", "")}, true, "needs-decision"},
		{[]presentation.Item{pipelineItem("t", "", "test", "fail")}, false, "needs-attention"},
		{[]presentation.Item{presentation.Classify(presentation.Fact{Source: presentation.Source{TaskID: "t", Kind: presentation.Report, ID: "e1"}})}, false, "review-ready"},
		{[]presentation.Item{question("t", "", "q-1", "answered", "")}, false, "answer-pending"},
		{[]presentation.Item{presentation.Classify(presentation.Fact{Source: presentation.Source{TaskID: "t", Kind: presentation.Refresh, ID: "r1"}})}, false, "instruction-refresh-pending"},
	} {
		task.Items = tc.items
		if got := TaskState(task, tc.gapped); got != tc.want {
			t.Fatalf("state(%v, %v) = %q, want %q", tc.items, tc.gapped, got, tc.want)
		}
	}
	if got := Stage(task.Record); !strings.HasPrefix(got, "intent-") {
		t.Fatalf("stage = %q", got)
	}
	if got := Stage(record(t, `{"id":"t-aaaaaaaaaaaa","status":"archived"}`)); got == "" {
		t.Fatal("stage must never be empty")
	}
}
