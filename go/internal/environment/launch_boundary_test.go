package environment

import (
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestServiceLaunchExcludesTransferThroughCreationAccounting(t *testing.T) {
	lab := newServiceLab(t)
	entered, release := lab.hc.HoldReplyNext("terminal.run", "")
	defer release()
	lab.hc.QueueFault("terminal.run", hctest.Fault{LoseResponse: true})
	launched := make(chan error, 1)
	go func() { _, err := lab.start(); launched <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("launch never reached native creation")
	}
	other, err := store.Open(lab.s.Root)
	if err != nil {
		t.Fatal(err)
	}
	transferred := make(chan error, 1)
	go func() { _, err := other.ClaimCoordinator("", "replacement", true); transferred <- err }()
	select {
	case err := <-transferred:
		release()
		<-launched
		t.Fatalf("transfer completed before service result accounting: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case err := <-launched:
		if err == nil || !strings.Contains(err.Error(), "launch outcome must be reconciled") {
			t.Fatalf("lost outcome: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("launch did not finish")
	}
	select {
	case err := <-transferred:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("transfer stayed blocked after result accounting")
	}
	record, err := Read(lab.s, stringField(lab.task, "id"))
	if err != nil {
		t.Fatal(err)
	}
	services := listField(record, "services")
	if len(services) != 1 {
		t.Fatalf("service intents=%d", len(services))
	}
	service, _ := services[0].(*ordjson.Object)
	if stringField(service, "state") != "unknown" {
		t.Fatal("lost creation was not durably retained as uncertain")
	}
	if len(lab.hc.Terminals()) != 2 {
		t.Fatal("lost creation changed native terminal count")
	}
}
