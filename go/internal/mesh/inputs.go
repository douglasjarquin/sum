package mesh

import (
	"fmt"
	"time"
	"unicode/utf8"
)

type emptyInput struct{}

type targetInput struct {
	Target string `json:"target"`
}

func (i targetInput) validate() error {
	if !validTarget(i.Target) {
		return fmt.Errorf("target must be a non-empty agent name or pane ID without leading dashes or whitespace and at most 200 characters")
	}
	return nil
}

type readInput struct {
	Target string `json:"target"`
	Lines  *int   `json:"lines,omitempty"`
}

func (i readInput) validate() error {
	return validateTargetAndLines(i.Target, i.Lines)
}

func (i readInput) target() string { return i.Target }

func (i readInput) readLines() (int, error) {
	return linesValue(i.Lines)
}

type paneReadInput struct {
	PaneID string `json:"pane_id"`
	Lines  *int   `json:"lines,omitempty"`
}

func (i paneReadInput) validate() error {
	return validateTargetAndLines(i.PaneID, i.Lines)
}

func (i paneReadInput) target() string { return i.PaneID }

func (i paneReadInput) readLines() (int, error) {
	return linesValue(i.Lines)
}

type messageInput struct {
	Target  string `json:"target"`
	Message string `json:"message"`
}

func (i messageInput) validate() error {
	if !validTarget(i.Target) {
		return fmt.Errorf("target must be a non-empty agent name or pane ID without leading dashes or whitespace and at most 200 characters")
	}
	if !validMessage(i.Message) {
		return fmt.Errorf("message must be between 1 and 20000 characters")
	}
	return nil
}

func (i messageInput) target() string { return i.Target }

type handoffInput struct {
	Target  string `json:"target"`
	Message string `json:"message"`
	Timeout *int   `json:"timeout_ms,omitempty"`
	Lines   *int   `json:"lines,omitempty"`
}

func (i handoffInput) validate() error {
	if err := (messageInput{Target: i.Target, Message: i.Message}).validate(); err != nil {
		return err
	}
	i.timeout()
	_, err := i.readLines()
	return err
}

func (i handoffInput) target() string { return i.Target }

func (i handoffInput) timeout() time.Duration {
	value := 5000
	if i.Timeout != nil {
		value = *i.Timeout
	}
	if value < 100 {
		value = 100
	}
	if value > 60000 {
		value = 60000
	}
	return time.Duration(value) * time.Millisecond
}

func (i handoffInput) readLines() (int, error) {
	return linesValue(i.Lines)
}

type waitInput struct {
	Target  string  `json:"target"`
	Status  *string `json:"status,omitempty"`
	Timeout *int    `json:"timeout_ms,omitempty"`
}

func (i waitInput) validate() error {
	if !validTarget(i.Target) {
		return fmt.Errorf("target must be a non-empty agent name or pane ID without leading dashes or whitespace and at most 200 characters")
	}
	status := i.statusValue()
	if status != "idle" && status != "done" && status != "blocked" && status != "working" && status != "unknown" {
		return fmt.Errorf("status must be one of idle, done, blocked, working, unknown")
	}
	i.timeout()
	return nil
}

func (i waitInput) statusValue() string {
	if i.Status == nil {
		return "idle"
	}
	return *i.Status
}

func (i waitInput) timeout() time.Duration {
	value := 5000
	if i.Timeout != nil {
		value = *i.Timeout
	}
	if value < 100 {
		value = 100
	}
	if value > 60000 {
		value = 60000
	}
	return time.Duration(value) * time.Millisecond
}

type startInput struct {
	Name   string   `json:"name"`
	Kind   string   `json:"kind"`
	PaneID string   `json:"pane_id"`
	Args   []string `json:"args,omitempty"`
}

func (i startInput) validate() error {
	if !validKind(i.Name) || !validKind(i.Kind) {
		return fmt.Errorf("name and kind must match lowercase letters, digits, underscores, or dashes and be at most 32 characters")
	}
	if !validTarget(i.PaneID) {
		return fmt.Errorf("pane_id must be a non-empty pane ID without leading dashes or whitespace and at most 200 characters")
	}
	if len(i.Args) > 30 {
		return fmt.Errorf("args must contain at most 30 values")
	}
	return nil
}

func validateTargetAndLines(target string, lines *int) error {
	if !validTarget(target) {
		return fmt.Errorf("target must be a non-empty agent name or pane ID without leading dashes or whitespace and at most 200 characters")
	}
	_, err := linesValue(lines)
	return err
}

func linesValue(lines *int) (int, error) {
	value := 80
	if lines != nil {
		value = *lines
	}
	if value < 1 || value > 200 {
		return 0, fmt.Errorf("lines must be between 1 and 200")
	}
	return value, nil
}

func validMessage(value string) bool {
	return utf8.RuneCountInString(value) >= 1 && utf8.RuneCountInString(value) <= 20000
}

func validKind(value string) bool {
	if value == "" || utf8.RuneCountInString(value) > 32 {
		return false
	}
	for index, runeValue := range value {
		if !((runeValue >= 'a' && runeValue <= 'z') || (runeValue >= '0' && runeValue <= '9') || runeValue == '_' || runeValue == '-') || (index == 0 && runeValue == '-') {
			return false
		}
	}
	return true
}
