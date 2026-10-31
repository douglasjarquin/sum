package maintenance

import (
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"testing"
)

func TestRefuseOnlyUnresolvedResourceOwnership(t *testing.T) {
	for _, tc := range []struct {
		state   string
		refused bool
	}{{"claimed", true}, {"uncertain", true}, {"finished", false}, {"not-submitted", false}} {
		t.Run(tc.state, func(t *testing.T) {
			task := ordjson.NewObject()
			task.Set("id", "t1")
			record := ordjson.NewObject()
			record.Set("state", tc.state)
			task.Set("terminal_maintenance", record)
			if got := Refuse(task, "Answer") != nil; got != tc.refused {
				t.Fatalf("refused %v", got)
			}
		})
	}
	task := ordjson.NewObject()
	record := ordjson.NewObject()
	record.Set("intent", ordjson.NewObject())
	task.Set("cleanup", record)
	if Refuse(task, "Notification") == nil {
		t.Fatal("cleanup intent admitted another effect")
	}
	record.Set("intent", false)
	if err := Refuse(task, "Notification"); err != nil {
		t.Fatal(err)
	}
}
