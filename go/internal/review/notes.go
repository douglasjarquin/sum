package review

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

const (
	NoteFocus    = "review-focus"
	NoteFinding  = "open-finding"
	NoteLimit    = "limitation"
	maxNoteRunes = 2000
)

var (
	noteKinds     = map[string]bool{NoteFocus: true, NoteFinding: true, NoteLimit: true}
	severities    = map[string]bool{"blocking": true, "important": true, "advisory": true}
	findingPrefix = regexp.MustCompile(`(?i)^(blocking|important|advisory)\s*:\s*(.+)$`)
)

// Note is one reviewer-facing item for the PR summary. The full findings text stays on the review record.
type Note struct {
	Kind     string
	Text     string
	Severity string
	Link     string
}

func ParseNotesFile(path string) ([]Note, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--notes %s is unreadable: %w", path, err)
	}
	return ParseNotesJSON(data)
}

func ParseNotesJSON(data []byte) ([]Note, error) {
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("--notes must be a JSON array of objects with kind and text")
	}
	notes := make([]Note, 0, len(raw))
	for i, row := range raw {
		kind, _ := row["kind"].(string)
		text, _ := row["text"].(string)
		severity, _ := row["severity"].(string)
		link, _ := row["link"].(string)
		note := Note{Kind: kind, Text: text, Severity: severity, Link: link}
		if err := validateNote(note, i); err != nil {
			return nil, err
		}
		notes = append(notes, normalizeNote(note))
	}
	return notes, nil
}

func NotesFromFlags(focus, findings, limitations []string) ([]Note, error) {
	var notes []Note
	for _, text := range focus {
		notes = append(notes, Note{Kind: NoteFocus, Text: text})
	}
	for _, text := range findings {
		note := Note{Kind: NoteFinding, Text: text}
		if match := findingPrefix.FindStringSubmatch(strings.TrimSpace(text)); match != nil {
			note.Severity = strings.ToLower(match[1])
			note.Text = match[2]
		}
		notes = append(notes, note)
	}
	for _, text := range limitations {
		notes = append(notes, Note{Kind: NoteLimit, Text: text})
	}
	for i, note := range notes {
		if err := validateNote(note, i); err != nil {
			return nil, err
		}
		notes[i] = normalizeNote(note)
	}
	return notes, nil
}

func notesFromMade(made *ordjson.Object) []Note {
	if made == nil {
		return nil
	}
	var notes []Note
	raw, _ := made.Get("findings")
	items, _ := raw.([]any)
	for _, item := range items {
		row, _ := item.(*ordjson.Object)
		if row == nil {
			continue
		}
		severity, _ := row.Get("severity")
		path, _ := row.Get("path")
		invariant, _ := row.Get("invariant")
		detail, _ := row.Get("detail")
		parts := []string{}
		if p, _ := path.(string); strings.TrimSpace(p) != "" {
			parts = append(parts, strings.TrimSpace(p))
		}
		if inv, _ := invariant.(string); strings.TrimSpace(inv) != "" {
			parts = append(parts, strings.TrimSpace(inv))
		}
		text := strings.Join(parts, ": ")
		if d, _ := detail.(string); strings.TrimSpace(d) != "" {
			if text != "" {
				text += ". " + strings.TrimSpace(d)
			} else {
				text = strings.TrimSpace(d)
			}
		}
		if text == "" {
			continue
		}
		note := Note{Kind: NoteFinding, Text: text}
		if s, _ := severity.(string); severities[s] {
			note.Severity = s
		}
		notes = append(notes, normalizeNote(note))
	}
	if limitations, _ := made.Get("limitations"); limitations != nil {
		if text, _ := limitations.(string); strings.TrimSpace(text) != "" {
			notes = append(notes, normalizeNote(Note{Kind: NoteLimit, Text: text}))
		}
	}
	return notes
}

func encodeNotes(notes []Note) []any {
	out := make([]any, 0, len(notes))
	for _, note := range notes {
		row := ordjson.NewObject()
		row.Set("kind", note.Kind)
		row.Set("text", note.Text)
		if note.Severity != "" {
			row.Set("severity", note.Severity)
		}
		if note.Link != "" {
			row.Set("link", note.Link)
		}
		out = append(out, row)
	}
	return out
}

func validateNote(note Note, index int) error {
	if !noteKinds[note.Kind] {
		return fmt.Errorf("notes[%d]: kind must be one of review-focus, open-finding, limitation", index)
	}
	if strings.TrimSpace(note.Text) == "" {
		return fmt.Errorf("notes[%d]: text must not be empty", index)
	}
	if note.Severity != "" && !severities[note.Severity] {
		return fmt.Errorf("notes[%d]: severity must be blocking, important, or advisory", index)
	}
	if note.Kind != NoteFinding && note.Severity != "" {
		return fmt.Errorf("notes[%d]: severity applies only to open-finding", index)
	}
	if n := len([]rune(strings.TrimSpace(note.Text))); n > maxNoteRunes {
		return fmt.Errorf("notes[%d]: text exceeds %d characters; keep the PR note compact and leave the full findings in --file/--text", index, maxNoteRunes)
	}
	return nil
}

func normalizeNote(note Note) Note {
	note.Kind = strings.TrimSpace(note.Kind)
	note.Text = strings.TrimSpace(note.Text)
	note.Severity = strings.ToLower(strings.TrimSpace(note.Severity))
	note.Link = strings.TrimSpace(note.Link)
	return note
}
