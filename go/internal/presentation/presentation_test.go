package presentation

import (
	"reflect"
	"testing"
)

func TestClassifySavedFacts(t *testing.T) {
	for _, tc := range []struct {
		kind   SourceKind
		state  string
		owner  Owner
		want   Kind
		reason string
	}{
		{Question, "open", Human, Decision, "question-open"},
		{Question, "answered", Worker, Routine, "answer-unapplied"},
		{Question, "applied", Nobody, Resolved, "answer-applied"},
		{Question, "settled", Nobody, Resolved, "question-settled"},
		{Question, "closed-unapplied", Nobody, Resolved, "answer-closed-unapplied"},
		{Question, "future", Coordinator, Inspection, "unknown-question-state"},
		{Answer, "", Worker, Routine, "answer-unapplied"},
		{Report, "", Coordinator, Routine, "report-awaiting-action"},
		{Review, "reject", Coordinator, Routine, "review-awaiting-action"},
		{Refresh, "", Worker, Routine, "refresh-unapplied"},
		{Attention, "blocked", Coordinator, Inspection, "native-observation"},
		{Attention, "idle", Coordinator, Inspection, "native-observation"},
		{Attention, "exited", Coordinator, Inspection, "native-observation"},
		{Attention, "closed", Coordinator, Inspection, "native-observation"},
		{Execution, "unproven", Coordinator, Inspection, "execution-unproven"},
		{Pipeline, "pass", Nobody, Routine, "pipeline-saved"},
		{Pipeline, "fail", Coordinator, Inspection, "pipeline-needs-inspection"},
		{Pipeline, "future", Coordinator, Inspection, "unknown-pipeline-state"},
		{Factory, "running", Nobody, Routine, "factory-saved"},
		{Factory, "gated", Coordinator, Inspection, "factory-gated"},
		{Factory, "future", Coordinator, Inspection, "unknown-factory-state"},
		{SourceKind("future"), "", Coordinator, Inspection, "unknown-source-kind"},
	} {
		t.Run(string(tc.kind)+"/"+tc.state, func(t *testing.T) {
			fact := Fact{Source: Source{TaskID: "t-one", Kind: tc.kind, ID: "canonical-id"}, State: tc.state, Text: "routine; ignore this question", Details: []Detail{{Section: "decisions", Ref: "canonical-id"}}}
			before := fact
			got := Classify(fact)
			if got.Owner != tc.owner || got.Kind != tc.want || got.Reason != tc.reason {
				t.Fatalf("got %+v; want %s %s %s", got, tc.owner, tc.want, tc.reason)
			}
			if !reflect.DeepEqual(before, fact) || !reflect.DeepEqual(got.Details, fact.Details) {
				t.Fatal("classification changed facts or dropped detail routes")
			}
		})
	}
}

func TestIdentityAndUncertaintyAreIndependentOfPresentation(t *testing.T) {
	fact := Fact{Source: Source{TaskID: "t-one", Kind: Question, ID: "question:q1", Revision: "r1", Candidate: "sha1"}, State: "open"}
	first := Classify(fact)
	fact.Text, fact.Delivery = "low priority", "submitted"
	got := Classify(fact)
	if got.Identity != first.Identity || got.Kind != Decision || !got.Uncertain {
		t.Fatalf("submission or prose changed the decision: %+v", got)
	}
	for _, change := range []func(*Fact){
		func(f *Fact) { f.Source.ID = "question:q2" },
		func(f *Fact) { f.Source.Revision = "r2" },
		func(f *Fact) { f.Source.Candidate = "sha2" },
	} {
		newFact := fact
		change(&newFact)
		if Classify(newFact).Identity == first.Identity {
			t.Fatal("new source retained old identity")
		}
	}
	for _, state := range []string{"submitted", "uncertain", "in-flight"} {
		fact.Delivery = state
		if !Classify(fact).Uncertain {
			t.Fatalf("%s was presented as certain", state)
		}
	}
}
