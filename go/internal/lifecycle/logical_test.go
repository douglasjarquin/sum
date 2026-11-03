package lifecycle

import (
	"github.com/douglasjarquin/sum/go/internal/app"
	"testing"
)

func TestSweepLogicalClaimIgnoresHistoricalParentTerminal(t *testing.T) {
	l := newSweepLab(t)
	l.saveTask(l.openPR())
	task := l.task()
	endpoint := roleEndpoint(task, "worker")
	task.Set("parent", endpoint.Object())
	l.save(task)
	l.terminal(endpoint, "finished")
	session, err := l.store.CoordinatorSession()
	if err != nil {
		t.Fatal(err)
	}
	l.ctx, err = app.RequireClaim(l.store, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	row := l.closeWorker()
	if asString(row, "state") != "closed" {
		t.Fatalf("historical parent blocked worker cleanup: %v", row)
	}
	l.assertTerminal(endpoint.Address, false)
}
