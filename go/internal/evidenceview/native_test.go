package evidenceview

import (
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"testing"
)

func TestHistoricalEvidenceProjectionDoesNotRewriteSealedRecord(t *testing.T) {
	record := ordjson.NewObject()
	endpoint := ordjson.NewObject()
	endpoint.Set("machine", "old")
	endpoint.Set("session", "old")
	endpoint.Set("pane", "old-pane")
	record.Set("endpoint", endpoint)
	record.Set("candidate", "base")
	before, _ := ordjson.MarshalCompact(record)
	view := evidenceRowCopy(record, "base")
	if authority, _ := view.Get("endpoint_authority"); authority != "historical" {
		t.Fatal("old endpoint presented without historical label")
	}
	after, _ := ordjson.MarshalCompact(record)
	if string(before) != string(after) {
		t.Fatal("historical evidence rewritten")
	}
}
