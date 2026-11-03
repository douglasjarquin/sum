package launch

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
)

type OpeningProgress struct {
	Terminal hcclient.Terminal      `json:"terminal"`
	State    string                 `json:"state"`
	First    *hcclient.FirstMessage `json:"first_message,omitempty"`
	Detail   string                 `json:"detail,omitempty"`
}

func OriginalProgram(initial, observed hcclient.Terminal, spec HCTemplate) error {
	if initial.Address != observed.Address || initial.CreatedMS != observed.CreatedMS || initial.PID == nil || observed.PID == nil || *initial.PID == 0 || *initial.PID != *observed.PID || observed.State != "live" || !sameDirectory(initial.Cwd, observed.Cwd) {
		return fmt.Errorf("original terminal creation, PID or checkout cannot be corroborated")
	}
	if observed.Template == nil || *observed.Template != spec.Name || observed.Submit == nil || *observed.Submit != spec.Submit || !reflect.DeepEqual(observed.Argv, []string{"sh", "-c", spec.Command()}) {
		return fmt.Errorf("original template command and submit key cannot be corroborated")
	}
	for _, key := range []string{"sum.installation", "sum.task", "sum.role", "sum.attempt"} {
		if initial.Labels[key] != observed.Labels[key] {
			return fmt.Errorf("original ownership label changed: %s", key)
		}
	}
	if observed.Foreground == nil || observed.Foreground.PID <= 0 {
		return fmt.Errorf("original foreground has not been observed")
	}
	if initial.Foreground != nil {
		if !reflect.DeepEqual(initial.Foreground, observed.Foreground) {
			return fmt.Errorf("original foreground was replaced")
		}
	} else if observed.Keep || !LaunchProgramMatches(spec, observed.Foreground.Argv) || !sameDirectory(observed.Foreground.Cwd, observed.Cwd) {
		return fmt.Errorf("first foreground observation cannot prove the original program; inspect without sending")
	}
	return nil
}

func DeliverOpening(ctx context.Context, client *hcclient.Client, initial hcclient.Terminal, spec HCTemplate, text string, persist func(OpeningProgress) error, adopt func() error) OpeningProgress {
	progress := OpeningProgress{Terminal: initial, State: "not-delivered", First: &hcclient.FirstMessage{Sent: false}}
	adopted := adopt == nil
	ready, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		observed, err := client.Get(ready, initial.Address)
		if err != nil {
			progress.Detail = err.Error()
			break
		}
		progress.Terminal = observed
		if initial.PID == nil || observed.PID == nil || *initial.PID != *observed.PID || initial.Address != observed.Address || initial.CreatedMS != observed.CreatedMS || (initial.Foreground != nil && !reflect.DeepEqual(initial.Foreground, observed.Foreground)) {
			progress.Detail = "original program identity changed; inspect without sending"
			break
		}
		if observed.State != "live" {
			progress.Detail = "program exited before opening; inspect its output, then use exact-attempt recovery with stop evidence"
			break
		}
		originalErr := OriginalProgram(initial, observed, spec)
		if originalErr == nil && initial.Foreground == nil {
			initial = observed
			progress.State = "readiness-pending"
			if err = persist(progress); err != nil {
				progress.Detail = err.Error()
				return progress
			}
		}
		if originalErr == nil && !adopted {
			adopted = true
			if err = adopt(); err != nil {
				progress.Detail = err.Error()
				return progress
			}
		}
		if err = originalErr; err == nil && observed.Status != nil && (observed.Status.State == "idle" || observed.Status.State == "done") {
			progress.State, progress.First = "in-flight", nil
			if err = persist(progress); err != nil {
				progress.State = "not-delivered"
				progress.First = &hcclient.FirstMessage{Sent: false}
				progress.Detail = err.Error()
				return progress
			}
			params := hcclient.SendParams{Terminal: observed.Address, Text: text}
			if observed.Status.State == "idle" {
				params.If = "idle"
			}
			result, sendErr := client.Send(ctx, params)
			progress.State, progress.Detail = "submitted", "input submitted; application init receipt pending"
			progress.First = &hcclient.FirstMessage{Sent: true, Started: result.Started, Turn: &result.Turn}
			if sendErr != nil {
				progress.State, progress.Detail, progress.First = "uncertain", sendErr.Error(), nil
				var native *hcclient.Error
				if errors.As(sendErr, &native) && native.Effect != hcclient.Uncertain {
					progress.State, progress.First = "not-delivered", &hcclient.FirstMessage{Sent: false}
				}
			}
			break
		}
		if err != nil {
			progress.Detail = err.Error()
		} else {
			progress.Detail = "native readiness pending; inspect the saved exact terminal"
		}
		select {
		case <-ready.Done():
			progress.Detail += ": " + ready.Err().Error()
			goto complete
		case <-ticker.C:
		}
	}
complete:
	if progress.State == "readiness-pending" {
		progress.State = "not-delivered"
	}
	if err := persist(progress); err != nil {
		progress.State, progress.Detail = "uncertain", err.Error()
	}
	return progress
}
