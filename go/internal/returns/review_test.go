package returns

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	shaA     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB     = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	reviewAt = "2026-09-24T14:32:21+00:00"
)

func record(id, kind, source, candidate, at string) *ordjson.Object {
	r := ordjson.NewObject()
	r.Set("id", id)
	r.Set("kind", kind)
	r.Set("source", source)
	r.Set("at", at)
	if candidate == "" {
		r.Set("candidate", nil)
	} else {
		r.Set("candidate", candidate)
	}
	if kind == "review" {
		r.Set("verdict", "approve")
	}
	return r
}

func reviewTask(parentPane string, evidence []*ordjson.Object, repairSendsAt ...string) *ordjson.Object {
	task := ordjson.NewObject()
	task.Set("id", "t-000000000001")
	task.Set("status", "reported")
	if parentPane != "" {
		parent := ordjson.NewObject()
		parent.Set("machine", "m-lab")
		parent.Set("session", "lab")
		parent.Set("pane", parentPane)
		task.Set("parent", parent)
	}
	list := make([]any, 0, len(evidence))
	for _, e := range evidence {
		list = append(list, e)
	}
	task.Set("evidence", list)
	if len(repairSendsAt) > 0 {
		var ops []any
		for _, at := range repairSendsAt {
			op := ordjson.NewObject()
			op.Set("kind", "send")
			op.Set("created_at", at)
			ops = append(ops, op)
		}
		repairs := ordjson.NewObject()
		repairs.Set("operations", ops)
		task.Set("repairs", repairs)
	}
	return task
}

func openReviews(t *testing.T, s *store.Store, task *ordjson.Object) []string {
	t.Helper()
	items, err := OpenObligations(s, task)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, item := range items {
		if kind, _ := item.Get("kind"); kind == "review" {
			id, _ := item.Get("id")
			ids = append(ids, id.(string))
		}
	}
	return ids
}

func reviewStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestReviewReturnClosesOnlyWhenTheCoordinatorActs(t *testing.T) {
	s := reviewStore(t)
	before, after := "2026-09-24T14:30:00+00:00", "2026-09-24T14:40:00+00:00"
	review := func() *ordjson.Object { return record("e-review", "review", "reviewer", shaA, reviewAt) }
	cases := []struct {
		name    string
		task    *ordjson.Object
		wantIDs []string
	}{
		{"a recorded review is owed to the parent", reviewTask("w-root:p1", []*ordjson.Object{review()}), []string{"review:e-review"}},
		{"coordinator verification of the candidate closes it",
			reviewTask("w-root:p1", []*ordjson.Object{review(), record("e-2", "verification", "coordinator", shaA, after)}), nil},
		{"a push of the candidate closes it",
			reviewTask("w-root:p1", []*ordjson.Object{review(), record("e-2", "push", "coordinator", shaA, after)}), nil},
		{"opening the PR closes it",
			reviewTask("w-root:p1", []*ordjson.Object{review(), record("e-2", "pr", "coordinator", shaA, after)}), nil},
		{"a CI read of the candidate closes it",
			reviewTask("w-root:p1", []*ordjson.Object{review(), record("e-2", "ci", "coordinator", shaA, after)}), nil},
		{"a PR reconcile of the candidate closes it",
			reviewTask("w-root:p1", []*ordjson.Object{review(), record("e-2", "publication", "github", shaA, after)}), nil},
		{"a repair sent after the verdict closes it",
			reviewTask("w-root:p1", []*ordjson.Object{review()}, after), nil},
		{"a repair sent before the verdict leaves it open",
			reviewTask("w-root:p1", []*ordjson.Object{review()}, before), []string{"review:e-review"}},
		{"coordinator verification before the verdict leaves it open",
			reviewTask("w-root:p1", []*ordjson.Object{record("e-1", "verification", "coordinator", shaA, before), review()}), []string{"review:e-review"}},
		{"the worker's own verification leaves it open",
			reviewTask("w-root:p1", []*ordjson.Object{review(), record("e-2", "verification", "worker", shaA, after)}), []string{"review:e-review"}},
		{"a push of another candidate leaves it open",
			reviewTask("w-root:p1", []*ordjson.Object{review(), record("e-2", "push", "coordinator", shaB, after)}), []string{"review:e-review"}},
		{"a pipeline lint run is not acting on the verdict",
			reviewTask("w-root:p1", []*ordjson.Object{review(), record("e-2", "lint", "coordinator", shaA, after)}), []string{"review:e-review"}},
		{"a review naming no candidate closes on any coordinator verification",
			reviewTask("w-root:p1", []*ordjson.Object{record("e-review", "review", "reviewer", "", reviewAt), record("e-2", "verification", "coordinator", shaB, after)}), nil},
		{"no parent pane owes no return", reviewTask("", []*ordjson.Object{review()}), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := openReviews(t, s, tc.task); !reflect.DeepEqual(got, tc.wantIDs) {
				t.Fatalf("open review returns = %v, want %v", got, tc.wantIDs)
			}
		})
	}
}

func TestReviewReturnIsSupersededByANewerCandidate(t *testing.T) {
	s := reviewStore(t)
	after := "2026-09-24T14:40:00+00:00"
	review := func() *ordjson.Object { return record("e-review", "review", "reviewer", shaA, reviewAt) }
	cases := []struct {
		name    string
		later   *ordjson.Object
		wantIDs []string
	}{
		{"a worker report of a newer candidate", record("e-2", "report", "worker", shaB, after), nil},
		{"a worker handoff of a newer candidate", record("e-2", "handoff", "worker", shaB, after), nil},
		{"a review of a newer candidate", record("e-2", "review", "reviewer", shaB, after), []string{"review:e-2"}},
		{"a second review of the same candidate keeps both", record("e-2", "review", "reviewer", shaA, after), []string{"review:e-review", "review:e-2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := openReviews(t, s, reviewTask("w-root:p1", []*ordjson.Object{review(), tc.later}))
			if !reflect.DeepEqual(got, tc.wantIDs) {
				t.Fatalf("open review returns = %v, want %v", got, tc.wantIDs)
			}
		})
	}
}

func TestReviewReturnNeverClosesOnANoticeOrOnArchive(t *testing.T) {
	s := reviewStore(t)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	task := reviewTask("w-root:p1", []*ordjson.Object{record("e-review", "review", "reviewer", shaA, reviewAt)})
	delivery := ordjson.NewObject()
	delivery.Set("id", "d-1")
	delivery.Set("at", reviewAt)
	delivery.Set("obligations", []any{"review:e-review"})
	delivery.Set("state", "submitted")
	delivery.Set("via", "prompt")
	recipient := ordjson.NewObject()
	recipient.Set("key", "anything")
	delivery.Set("recipient", recipient)
	sidecar := ordjson.NewObject()
	sidecar.Set("schema", json.Number("1"))
	sidecar.Set("task", "t-000000000001")
	sidecar.Set("deliveries", []any{delivery})
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if err := Write(s, sidecar); err != nil {
		t.Fatal(err)
	}
	if got := openReviews(t, s, task); !reflect.DeepEqual(got, []string{"review:e-review"}) {
		t.Fatalf("after a submitted notice open review returns = %v, want it still open", got)
	}
	task.Set("status", "archived")
	if got := openReviews(t, s, task); len(got) != 0 {
		t.Fatalf("archived task still owes %v", got)
	}
}

func TestNoticeViewNamesAReviewDelivery(t *testing.T) {
	delivery := ordjson.NewObject()
	delivery.Set("id", "d-1")
	delivery.Set("at", reviewAt)
	delivery.Set("obligations", []any{"review:e-review"})
	delivery.Set("state", "submitted")
	recipient := ordjson.NewObject()
	recipient.Set("recipient", "parent")
	delivery.Set("recipient", recipient)
	sidecar := ordjson.NewObject()
	sidecar.Set("deliveries", []any{delivery})
	notice, _ := Notice(reviewTask("w-root:p1", nil), sidecar).(*ordjson.Object)
	if reason, _ := notice.Get("reason"); reason != "a review verdict is recorded" {
		t.Fatalf("notice view = %v, want the review reason", notice)
	}
}
