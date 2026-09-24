package review

import (
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func TestParseNotesJSON_requiresKindAndText(t *testing.T) {
	notes, err := ParseNotesJSON([]byte(`[
		{"kind": "review-focus", "text": "hero CTA is scoped on purpose"},
		{"kind": "open-finding", "severity": "blocking", "text": "nav still points at #", "link": "https://example.test/168"},
		{"kind": "limitation", "text": "source inspected only"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 3 {
		t.Fatalf("len(notes) = %d, want 3", len(notes))
	}
	if notes[1].Severity != "blocking" || notes[1].Link == "" {
		t.Fatalf("finding = %+v", notes[1])
	}
}

func TestParseNotesJSON_refusesUnknownKind(t *testing.T) {
	if _, err := ParseNotesJSON([]byte(`[{"kind": "nit", "text": "rename me"}]`)); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("err = %v, want a kind error", err)
	}
}

func TestNotesFromFlags_parsesFindingSeverityPrefix(t *testing.T) {
	notes, err := NotesFromFlags(
		[]string{"scope stays on the hero CTA"},
		[]string{"blocking: the counter is off by one, so totals drift; correct the increment"},
		[]string{"source inspected; checks were not rerun"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 3 {
		t.Fatalf("len(notes) = %d", len(notes))
	}
	if notes[1].Kind != NoteFinding || notes[1].Severity != "blocking" {
		t.Fatalf("finding = %+v", notes[1])
	}
	if notes[1].Text != "the counter is off by one, so totals drift; correct the increment" {
		t.Fatalf("text = %q", notes[1].Text)
	}
}

func TestNotesFromMade_mapsFindingsAndLimitations(t *testing.T) {
	made := ordjson.NewObject()
	finding := ordjson.NewObject()
	finding.Set("path", "go/x.go")
	finding.Set("invariant", "single writer")
	finding.Set("severity", "blocking")
	finding.Set("detail", "two writers")
	made.Set("findings", []any{finding})
	made.Set("limitations", "daemonless")

	notes := notesFromMade(made)
	if len(notes) != 2 {
		t.Fatalf("len(notes) = %d, want 2: %+v", len(notes), notes)
	}
	if notes[0].Kind != NoteFinding || notes[0].Severity != "blocking" || !strings.Contains(notes[0].Text, "two writers") {
		t.Fatalf("finding = %+v", notes[0])
	}
	if notes[1].Kind != NoteLimit || notes[1].Text != "daemonless" {
		t.Fatalf("limitation = %+v", notes[1])
	}
}

func TestNotesFromMade_emptyReportYieldsNoNotes(t *testing.T) {
	made := ordjson.NewObject()
	made.Set("findings", []any{})
	made.Set("limitations", nil)
	if notes := notesFromMade(made); len(notes) != 0 {
		t.Fatalf("notes = %+v, want none", notes)
	}
}
