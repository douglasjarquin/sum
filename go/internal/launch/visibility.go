package launch

import (
	"context"
	"errors"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

type Visibility struct {
	Labels   map[string]string `json:"labels"`
	State    string            `json:"state"`
	At       string            `json:"at,omitempty"`
	Observed map[string]string `json:"observed,omitempty"`
	Detail   string            `json:"detail,omitempty"`
}

const (
	VisibilityPending   = "pending"
	VisibilityInFlight  = "in-flight"
	VisibilityApplied   = "applied"
	VisibilityUncertain = "uncertain"
	VisibilityRefused   = "refused"
)

func SpaceID(instance string) string {
	if len(instance) > 16 {
		instance = instance[:16]
	}
	return "sum-" + instance
}

func VisibilityLabels(instance, task string) map[string]string {
	space := SpaceID(instance)
	return map[string]string{"herdr.space": space, "herdr.tab": space + "-" + task}
}

func NewVisibility(instance, task string) Visibility {
	return Visibility{Labels: VisibilityLabels(instance, task), State: VisibilityPending}
}

func herdrLabels(labels map[string]string) map[string]string {
	observed := map[string]string{}
	for key, value := range labels {
		if strings.HasPrefix(key, "herdr.") {
			observed[key] = value
		}
	}
	return observed
}

func visibilityObserved(intent map[string]string, terminal hcclient.Terminal) bool {
	if len(intent) == 0 {
		return false
	}
	for key, value := range intent {
		if terminal.Labels[key] != value {
			return false
		}
	}
	return true
}

func (v Visibility) Object() *ordjson.Object {
	value, _ := ordjson.FromValue(v)
	return value
}

func (v Visibility) Settled() bool {
	return v.State == VisibilityApplied || v.State == VisibilityRefused
}

func PublishVisibility(ctx context.Context, client *hcclient.Client, address string, claim func() (map[string]string, error), record func(Visibility) error) error {
	labels, err := claim()
	if err != nil || labels == nil {
		return err
	}
	outcome := Visibility{Labels: labels, State: VisibilityUncertain}
	terminal, labelErr := client.Label(ctx, address, labels)
	switch {
	case labelErr == nil && visibilityObserved(labels, terminal):
		outcome.State = VisibilityApplied
		outcome.Observed = herdrLabels(terminal.Labels)
	case labelErr == nil:
		outcome.Observed = herdrLabels(terminal.Labels)
		outcome.Detail = "core reply did not show the intended labels"
	default:
		outcome.Detail = labelErr.Error()
		var native *hcclient.Error
		if errors.As(labelErr, &native) && (native.RPC != nil || native.Effect == hcclient.NotSubmitted) {
			outcome.State = VisibilityRefused
		}
	}
	return record(outcome)
}

func ObservedVisibility(intent Visibility, terminal hcclient.Terminal) Visibility {
	outcome := Visibility{Labels: intent.Labels, Observed: herdrLabels(terminal.Labels), State: VisibilityPending}
	if visibilityObserved(intent.Labels, terminal) {
		outcome.State = VisibilityApplied
	}
	return outcome
}
