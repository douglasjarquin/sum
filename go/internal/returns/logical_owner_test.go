package returns

import (
	"testing"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestParentlessQuestionRemainsPendingWithoutDeliveryAttempt(t *testing.T) {
	l := newNativeLab(t)
	task, _ := l.task("open")
	session, err := l.s.RetireNativeCoordinator("", "conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	task.Set("owner", session.Owner())
	task.Set("parent", nil)
	if err := l.s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	calls := len(l.f.Calls())
	if _, err := Pump(l.s, PumpOpts{Snapshot: []*ordjson.Object{task}}); err != nil {
		t.Fatal(err)
	}
	if len(l.f.Calls()) != calls {
		t.Fatal("logical coordinator obligation contacted HC")
	}
	history, err := ReadReturns(l.s, taskIDOf(task))
	if err != nil {
		t.Fatal(err)
	}
	deliveries, _ := routeValue(history, "deliveries").([]any)
	if len(deliveries) != 0 {
		t.Fatalf("parent question created delivery history: %v", deliveries)
	}
	view, err := View(l.s, task)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := routeValue(view, "open").([]any)
	if len(items) != 1 {
		t.Fatalf("question is not actionable: %v", items)
	}
	item := items[0].(*ordjson.Object)
	notification, _ := routeValue(item, "notification").(*ordjson.Object)
	if routeValue(notification, "state") != "pending" || routeValue(item, "route") != nil {
		t.Fatalf("logical obligation retained a native route: %v", item)
	}
}

func TestLogicalCoordinatorReconcilesNativeWorkerDelivery(t *testing.T) {
	l, task, _, delivery := uncertainAppliedLab(t)
	session, err := l.s.RetireNativeCoordinator("", "conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	task.Set("owner", session.Owner())
	task.Set("parent", nil)
	if err := l.s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	actor, err := app.RequireClaim(l.s, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ReconcileDelivery(l.s, actor, taskIDOf(task), delivery); err != nil {
		t.Fatal(err)
	}
	ledger, err := readResolutions(l.s, taskIDOf(task))
	if err != nil {
		t.Fatal(err)
	}
	value, err := ordjson.Decode(ledger.Resolutions[0].Coordinator)
	if err != nil {
		t.Fatal(err)
	}
	recorded, _ := value.(*ordjson.Object)
	if !store.LogicalOwnersMatch(recorded, session.Owner()) {
		t.Fatal("reconciliation lost its logical actor")
	}
	if _, leaked := recorded.Get("claim"); leaked {
		t.Fatal("reconciliation retained a conversation claim")
	}
	history, err := ReadReturns(l.s, taskIDOf(task))
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := routeValue(history, "deliveries").([]any)
	resolved, err := DeliveryResolved(l.s, task, rows[len(rows)-1].(*ordjson.Object))
	if err != nil || !resolved {
		t.Fatalf("logical actor reconciliation did not round-trip: resolved=%t err=%v", resolved, err)
	}
}
