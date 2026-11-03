package cleanup

import (
	"github.com/douglasjarquin/sum/go/internal/app"
	"testing"
)

func TestCleanupLogicalClaimUsesRecordedWorkerRoute(t *testing.T) {
	l := newLab(t)
	head := l.makeWorktreeCheckout()
	l.saveTask(l.mergedTaskJSON(head, `[]`, "released"))
	l.workerTerminal(false)
	session, err := l.store.CoordinatorSession()
	if err != nil {
		t.Fatal(err)
	}
	task, err := l.store.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	task.Set("owner", session.Owner())
	task.Set("parent", nil)
	if err = l.store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	l.ctx, err = app.RequireClaim(l.store, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Run(l.store, l.ctx, l.runtime, Args{Task: taskID, Apply: true}); err != nil {
		t.Fatal(err)
	}
}
