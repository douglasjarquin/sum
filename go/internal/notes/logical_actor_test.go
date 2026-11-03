package notes

import (
	"testing"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestFindingCaptureAndDispositionRetainLogicalActorWithoutClaim(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "findings conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := app.RequireClaim(s, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", 2)
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("owner", session.Owner())
	task.Set("parent", nil)
	task.Set("status", "running")
	project := ordjson.NewObject()
	project.Set("host", "github.com")
	project.Set("owner", "example")
	project.Set("repo", "project")
	task.Set("project", project)
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	record, _, err := Capture(s, "t-aaaaaaaaaaaa", Input{Key: "followup", Title: "Followup", Explanation: "A separate improvement", NextStep: "Review the improvement"}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !store.LogicalOwnersMatch(record.Author, session.Owner()) || len(record.Author.Keys()) != 4 {
		t.Fatalf("invalid logical finding author: %v", record.Author)
	}
	record, err = Change(s, record.ID, "defer", "Handle separately", "", ctx)
	if err != nil {
		t.Fatal(err)
	}
	actor := record.History[len(record.History)-1].Actor
	if !store.LogicalOwnersMatch(actor, session.Owner()) || len(actor.Keys()) != 4 {
		t.Fatalf("invalid logical finding disposition actor: %v", actor)
	}
	foreign := session.Owner()
	foreign.Set("session_id", "another-session")
	task.Set("owner", foreign)
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if _, _, err = Capture(s, "t-aaaaaaaaaaaa", Input{Key: "foreign", Title: "Other", Explanation: "Another improvement", NextStep: "Review"}, ctx); err == nil {
		t.Fatal("accepted another session's task")
	}
}
