// Package presentation classifies saved facts without reading or changing their sources.
package presentation

import (
	"encoding/json"
	"strings"
)

type SourceKind string

const (
	Question  SourceKind = "question"
	Answer    SourceKind = "answer"
	Report    SourceKind = "report"
	Review    SourceKind = "review"
	Attention SourceKind = "attention"
	Refresh   SourceKind = "refresh"
	Execution SourceKind = "execution"
	Pipeline  SourceKind = "pipeline"
	Factory   SourceKind = "factory"
	Finding   SourceKind = "finding"
	Gap       SourceKind = "gap"
)

type Owner string

const (
	Human       Owner = "human"
	Coordinator Owner = "coordinator"
	Worker      Owner = "worker"
	Nobody      Owner = "none"
)

type Kind string

const (
	Decision   Kind = "decision"
	Routine    Kind = "routine"
	Inspection Kind = "inspection"
	Resolved   Kind = "resolved"
)

const (
	AnswerUnapplied      = "answer-unapplied"
	RefreshUnapplied     = "refresh-unapplied"
	UnknownQuestionState = "unknown-question-state"
)

// Source keeps canonical identity separate from mutable prose and display state.
type Source struct {
	TaskID    string     `json:"task"`
	Kind      SourceKind `json:"kind"`
	ID        string     `json:"id"`
	Revision  string     `json:"revision,omitempty"`
	Candidate string     `json:"candidate,omitempty"`
}

type Detail struct {
	Section string `json:"section"`
	Ref     string `json:"ref,omitempty"`
	Path    string `json:"path,omitempty"`
}

type Choice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type HumanActionStep struct {
	Kind       string          `json:"kind"`
	Content    HumanActionText `json:"content"`
	Source     HumanActionText `json:"source,omitempty"`
	Redactions int             `json:"redactions,omitempty"`
}

type HumanActionText struct {
	Text      string `json:"text"`
	Chars     int    `json:"chars"`
	Truncated bool   `json:"truncated"`
}

type HumanAction struct {
	Why        HumanActionText   `json:"why"`
	Steps      []HumanActionStep `json:"steps"`
	Expected   HumanActionText   `json:"expected"`
	Report     HumanActionText   `json:"report"`
	Check      HumanActionText   `json:"check"`
	Redactions int               `json:"redactions,omitempty"`
}

type Action struct {
	Target    Source   `json:"target"`
	Kind      string   `json:"kind"`
	Label     string   `json:"label"`
	State     string   `json:"state"`
	ChoiceID  string   `json:"choice_id,omitempty"`
	Arguments []string `json:"arguments"`
}

func QuestionActions(source Source, choices []Choice, recipient string) []Action {
	if recipient == "" || source.TaskID == "" || source.ID == "" {
		return nil
	}
	questionID := strings.TrimPrefix(source.ID, "question:")
	base := []string{"answer", source.TaskID, questionID, "--expected-recipient", recipient}
	actions := make([]Action, 0, len(choices)+2)
	for _, choice := range choices {
		actions = append(actions, Action{Target: source, Kind: "answer-choice", Label: "Answer: " + choice.Label, State: "open", ChoiceID: choice.ID, Arguments: append(append([]string{}, base...), "--choice", choice.ID)})
	}
	actions = append(actions, Action{Target: source, Kind: "answer-custom", Label: "Submit a custom answer", State: "open", Arguments: append(append([]string{}, base...), "--text")})
	actions = append(actions, Action{Target: source, Kind: "explain", Label: "Ask for an explanation", State: "open", Arguments: []string{"explain", source.TaskID, questionID, "--expected-recipient", recipient, "--text"}})
	return actions
}

func FindingActions(source Source, state string) []Action {
	if source.TaskID == "" || source.ID == "" || source.Revision == "" || state == "" {
		return nil
	}
	identity := []string{"--expected-revision", source.Revision, "--expected-state", state}
	makeAction := func(kind, label, command string, args ...string) Action {
		return Action{Target: source, Kind: kind, Label: label, State: state, Arguments: append(append([]string{"finding", command, source.ID}, identity...), args...)}
	}
	actions := []Action{}
	if strings.HasPrefix(state, "open:") {
		actions = append(actions,
			makeAction("finding-follow-up", "Investigate / create follow-up", "promote", "APPROVED_TASK", "--reason", "approved follow-up"),
			makeAction("finding-attach", "Attach to existing work", "attach", "EXISTING_TASK", "--reason", "related work"),
			makeAction("finding-defer", "Defer", "defer", "--reason", "deferred"),
			makeAction("finding-dismiss", "Dismiss", "dismiss", "--reason", "dismissed"),
			makeAction("finding-resolve", "Resolve", "resolve", "--reason", "resolved"),
		)
	} else if strings.HasPrefix(state, "deferred:") || strings.HasPrefix(state, "dismissed:") || strings.HasPrefix(state, "resolved:") {
		actions = append(actions, makeAction("finding-reopen", "Reopen", "reopen", "--reason", "reopened"))
	}
	return actions
}

type Fact struct {
	Source      Source       `json:"source"`
	ProjectID   string       `json:"project,omitempty"`
	State       string       `json:"state,omitempty"`
	At          string       `json:"at,omitempty"`
	Text        string       `json:"text,omitempty"`
	Answer      string       `json:"answer,omitempty"`
	CloseReason string       `json:"close_reason,omitempty"`
	OutcomeAt   string       `json:"outcome_at,omitempty"`
	Feedback    string       `json:"feedback,omitempty"`
	Delivery    string       `json:"delivery,omitempty"`
	Details     []Detail     `json:"details"`
	Choices     []Choice     `json:"choices,omitempty"`
	Recommended string       `json:"recommended,omitempty"`
	Rationale   string       `json:"rationale,omitempty"`
	Sources     []string     `json:"sources,omitempty"`
	HumanAction *HumanAction `json:"human_action,omitempty"`
	Actions     []Action     `json:"actions,omitempty"`
}

type Item struct {
	Fact
	Identity  string `json:"identity"`
	Owner     Owner  `json:"owner"`
	Kind      Kind   `json:"presentation"`
	Reason    string `json:"reason"`
	Uncertain bool   `json:"uncertain"`
}

// Classify never treats a delivery receipt or worker prose as domain resolution.
func Classify(f Fact) Item {
	if f.Source.Kind == Question {
		f.Feedback = questionFeedback(f)
	}
	identity, _ := json.Marshal(f.Source)
	item := Item{Fact: f, Identity: string(identity), Owner: Coordinator, Kind: Inspection, Reason: "unknown-source-kind"}
	item.Uncertain = f.Delivery == "submitted" || f.Delivery == "uncertain" || f.Delivery == "in-flight" || f.Delivery == "unknown"
	switch f.Source.Kind {
	case Question:
		switch f.State {
		case "open":
			item.Owner, item.Kind, item.Reason = Human, Decision, "question-open"
		case "answered":
			item.Owner, item.Kind, item.Reason = Worker, Routine, AnswerUnapplied
		case "applied":
			item.Owner, item.Kind, item.Reason = Nobody, Resolved, "answer-applied"
		case "settled":
			item.Owner, item.Kind, item.Reason = Nobody, Resolved, "question-settled"
		case "closed-unapplied":
			item.Owner, item.Kind, item.Reason = Nobody, Resolved, "answer-closed-unapplied"
		default:
			item.Reason, item.Uncertain = UnknownQuestionState, true
		}
	case Answer:
		item.Owner, item.Kind, item.Reason = Worker, Routine, AnswerUnapplied
	case Report:
		item.Kind, item.Reason = Routine, "report-awaiting-action"
	case Review:
		item.Kind, item.Reason = Routine, "review-awaiting-action"
	case Refresh:
		item.Owner, item.Kind, item.Reason = Worker, Routine, RefreshUnapplied
	case Attention:
		item.Reason = "native-observation"
	case Execution:
		item.Reason, item.Uncertain = "execution-unproven", true
	case Pipeline:
		item.Kind, item.Reason = Routine, "pipeline-saved"
		switch f.State {
		case "blocked", "fail":
			item.Kind, item.Reason = Inspection, "pipeline-needs-inspection"
		case "pending", "pass", "skipped", "not_declared":
			item.Owner = Nobody
		default:
			item.Kind, item.Reason, item.Uncertain = Inspection, "unknown-pipeline-state", true
		}
	case Factory:
		item.Kind, item.Reason = Routine, "factory-saved"
		switch f.State {
		case "gated":
			item.Kind, item.Reason = Inspection, "factory-gated"
		case "claimed", "running":
			item.Owner = Nobody
		default:
			item.Kind, item.Reason, item.Uncertain = Inspection, "unknown-factory-state", true
		}
	case Finding:
		item.Owner, item.Kind, item.Reason = Nobody, Routine, "incidental-finding"
	case Gap:
		item.Reason, item.Uncertain = "source-unreadable", true
	default:
		item.Uncertain = true
	}
	return item
}

func questionFeedback(f Fact) string {
	switch f.State {
	case "open":
		return "Needs your decision"
	case "answered":
		switch f.Delivery {
		case "pending", "deferred":
			return "Answer saved; waiting to notify worker"
		case "uncertain", "in-flight", "unknown":
			return "Answer saved; delivery unconfirmed"
		case "not-delivered", "stalled":
			return "Answer saved; notification " + f.Delivery
		case "submitted":
			return "Waiting for worker to apply answer"
		default:
			return "Answer saved; waiting for worker"
		}
	case "applied":
		return "Applied by worker"
	case "settled":
		return "Settled by cleanup"
	case "closed-unapplied":
		if f.CloseReason != "" {
			return "Closed without application: " + f.CloseReason
		}
		return "Closed without application"
	}
	return ""
}
