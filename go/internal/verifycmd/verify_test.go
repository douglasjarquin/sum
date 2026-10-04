package verifycmd

import (
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func TestRunEvidenceRejectsRecordWithoutCandidateObject(t *testing.T) {
	record := ordjson.NewObject()
	record.Set("run_id", "20261001T141644Z-01234567")
	record.Set("runner", func() *ordjson.Object {
		runner := ordjson.NewObject()
		runner.Set("mode", "run")
		return runner
	}())
	record.Set("outcome", "blocked")

	_, err := runEvidence(record, strings.Repeat("a", 40), nil, "run.json", "other-checkout", false, nil)
	if err == nil || !strings.Contains(err.Error(), "has no candidate object") {
		t.Fatalf("runEvidence error = %v, want missing candidate object", err)
	}
}
