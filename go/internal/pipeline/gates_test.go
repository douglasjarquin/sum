package pipeline

import (
	"strings"
	"testing"
)

func TestGatesSettled_allGreenAndAllowedNotDeclaredPass(t *testing.T) {
	record := New("t-x", strings.Repeat("a", 40))
	for i := range record.Rows {
		record.Rows[i].Status = Pass
	}
	record.Set(Row{Stage: StageLint, Status: NotDeclared})
	record.Set(Row{Stage: StageCI, Status: NotDeclared})
	ok, detail := GatesSettled(record)
	if !ok {
		t.Fatalf("settled = false (%s), want pass with lint/CI not_declared", detail)
	}
}

func TestGatesSettled_missingReviewDoesNotSettle(t *testing.T) {
	record := New("t-x", strings.Repeat("a", 40))
	for i := range record.Rows {
		record.Rows[i].Status = Pass
	}
	record.Set(Row{Stage: StageReview, Status: Pending})
	ok, detail := GatesSettled(record)
	if ok {
		t.Fatal("settled = true, want missing Review to block promotion")
	}
	if !strings.Contains(detail, "Review") {
		t.Fatalf("detail = %q, want it to name Review", detail)
	}
}
