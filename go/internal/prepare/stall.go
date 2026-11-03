package prepare

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const stallSendBound = 3

var errNotOneOpening = errors.New("not one live opening")

func stalledOpening(task *ordjson.Object) (prompt, attemptID, address string, ok bool) {
	worker, err := reservations.Worker(task)
	if err != nil {
		return "", "", "", false
	}
	outcome, _, uncertain := reservations.UncertainLaunch(worker)
	if !uncertain || outcome != "launch-uncertain" {
		return "", "", "", false
	}
	reason := asString(policyField(lastObservation(worker), "reason"))
	if !hcclient.AgentPromptStalledReason(&reason) {
		return "", "", "", false
	}
	prompt = asString(policyField(asObject(policyField(worker, "native_launch")), "opening"))
	if prompt == "" {
		return "", "", "", false
	}
	return prompt, asString(policyField(worker, "id")), asString(policyField(asObject(policyField(task, "worker_endpoint")), "address")), true
}

func lastObservation(worker *ordjson.Object) *ordjson.Object {
	list, _ := policyField(worker, "observations").([]any)
	if len(list) == 0 {
		return nil
	}
	last, _ := list[len(list)-1].(*ordjson.Object)
	return last
}

func workerLabels(instance, taskID, attemptID string) map[string]string {
	return map[string]string{"sum.installation": instance, "sum.task": taskID, "sum.role": "worker", "sum.attempt": attemptID}
}

func liveOpenings(ctx context.Context, client *hcclient.Client, labels map[string]string, name, cwd string) ([]hcclient.Terminal, error) {
	listed, err := client.List(ctx, hcclient.ListParams{Labels: labels})
	if err != nil {
		return nil, err
	}
	var matches []hcclient.Terminal
	for _, term := range listed.Terminals {
		if term.State != "live" || term.Cwd != cwd || term.Name == nil || *term.Name != name {
			continue
		}
		matches = append(matches, term)
	}
	return matches, nil
}

func landPrompt(ctx context.Context, client *hcclient.Client, address, prompt string) (bool, error) {
	sent := 0
	for {
		term, err := client.Get(ctx, address)
		if err != nil {
			return false, err
		}
		if term.State != "live" {
			return false, nil
		}
		if term.Status != nil {
			switch term.Status.State {
			case "working", "blocked":
				return true, nil
			case "idle":
			default:
				return false, nil
			}
		}
		if sent >= stallSendBound {
			return false, nil
		}
		params := hcclient.SendParams{Terminal: address, Text: prompt}
		if term.Status != nil && term.Status.State == "idle" {
			params.If = "idle"
		}
		if _, err = client.Send(ctx, params); err != nil {
			return false, err
		}
		sent++
	}
}

func stallMessage(cause error) string {
	text := LaunchUncertainPrefix + ": " + hcclient.AgentPromptStalled + ". sum retried the recorded brief on the live pane. A later start retries that same pane and does not allocate another terminal. Trust/auth prompts are for the user. Do not ask for permission to resend this brief."
	if cause != nil {
		text += " " + cause.Error()
	}
	return text
}

func openingEndpoint(route store.Endpoint, taskID, attemptID, checkout string, term hcclient.Terminal) store.Endpoint {
	return store.Endpoint{Schema: 2, Backend: "hc", Machine: route.Machine, CoreID: route.CoreID, Session: route.Session, Socket: route.Socket, Address: term.Address, CreatedMS: term.CreatedMS, Task: taskID, Role: "worker", Attempt: attemptID, BindingGeneration: 1, Cwd: checkout}
}

func matchedOpening(ctx context.Context, client *hcclient.Client, route store.Endpoint, instance, taskID, attemptID, checkout, requiredAddress string) (hcclient.Terminal, error) {
	matches, err := liveOpenings(ctx, client, workerLabels(instance, taskID, attemptID), NativeName(instance, taskID, "worker", attemptID), checkout)
	if err != nil {
		return hcclient.Terminal{}, err
	}
	if len(matches) != 1 || (requiredAddress != "" && matches[0].Address != requiredAddress) || openingEndpoint(route, taskID, attemptID, checkout, matches[0]).Validate() != nil {
		return hcclient.Terminal{}, errNotOneOpening
	}
	return matches[0], nil
}

func settleStalled(s *store.Store, client *hcclient.Client, route store.Endpoint, instance, taskID, attemptID, checkout, prompt, requiredAddress, effect string, first *hcclient.FirstMessage) (*ordjson.Object, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	term, err := matchedOpening(ctx, client, route, instance, taskID, attemptID, checkout, requiredAddress)
	if err != nil {
		return nil, false, nil
	}
	landed, sendErr := landPrompt(ctx, client, term.Address, prompt)
	task, err := persistStalled(s, taskID, attemptID, prompt, route, checkout, term, landed, effect, first, sendErr)
	return task, true, err
}

func resumeStalledOpening(s *store.Store, route store.Endpoint, taskID, attemptID, prompt, savedAddress, checkout string) (*ordjson.Object, error) {
	instance, err := s.Instance()
	if err != nil {
		return nil, err
	}
	client := hcclient.Client{Socket: route.Socket, CoreID: route.CoreID}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	term, err := matchedOpening(ctx, &client, route, instance, taskID, attemptID, checkout, savedAddress)
	if err != nil {
		if errors.Is(err, errNotOneOpening) {
			return nil, fmt.Errorf("%s: the stalled worker pane is not the one live terminal. start does not allocate another one.", taskID)
		}
		return nil, err
	}
	landed, sendErr := landPrompt(ctx, &client, term.Address, prompt)
	return persistStalled(s, taskID, attemptID, prompt, route, checkout, term, landed, "uncertain", nil, sendErr)
}

func persistStalled(s *store.Store, taskID, attemptID, prompt string, route store.Endpoint, checkout string, term hcclient.Terminal, landed bool, effect string, first *hcclient.FirstMessage, cause error) (*ordjson.Object, error) {
	endpoint := openingEndpoint(route, taskID, attemptID, checkout, term)
	if err := endpoint.Validate(); err != nil {
		return nil, err
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	currentWorker, err := reservations.Worker(current)
	if err != nil {
		return nil, err
	}
	state := asString(policyField(currentWorker, "state"))
	if asString(policyField(currentWorker, "id")) != attemptID || (state != "starting" && state != "running" && state != "uncertain") {
		return nil, fmt.Errorf("worker attempt changed during creation; inspect terminal %s", term.Address)
	}
	native := asObject(policyField(currentWorker, "native_launch"))
	if native == nil {
		return nil, fmt.Errorf("worker attempt changed during creation; inspect terminal %s", term.Address)
	}
	native.Set("opening", prompt)
	native.Set("endpoint", endpoint.Object())
	if asString(policyField(native, "effect")) != "created" {
		native.Set("effect", effect)
	}
	if first != nil {
		encoded, err := ordjson.FromValue(first)
		if err != nil {
			return nil, err
		}
		native.Set("first_message", encoded)
	}
	current.Set("worker_endpoint", endpoint.Object())
	evidence := incarnation.NativeEvidence{Endpoint: endpoint.Native(), Foreground: term.Foreground}
	if term.PID != nil {
		evidence.PID = *term.PID
	}
	recorded := evidence.Record(store.Now())
	native.Set("incarnation", recorded)
	var saved NativeTemplate
	attested := decode(policyField(native, "native_template"), &saved) == nil && term.Template != nil && *term.Template == saved.Spec.Name && term.Submit != nil && *term.Submit == saved.Spec.Submit
	if attested {
		registration, err := s.Registration(endpoint)
		if err != nil {
			return nil, err
		}
		if registration == nil {
			registration, err = s.Register(endpoint, "worker", taskID, recorded)
			if err != nil {
				return nil, err
			}
		}
		registration.Set("native_template", policyField(native, "native_template"))
		if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(endpoint)+".json"), registration); err != nil {
			return nil, err
		}
	}
	if state == "running" {
		if err = s.SaveTask(current); err != nil {
			return nil, err
		}
		return current, nil
	}
	if landed {
		current.Set("status", "starting")
		current.Delete("error")
		if state == "uncertain" {
			obs := ordjson.NewObject()
			obs.Set("at", store.Now())
			obs.Set("outcome", "brief-landed")
			obs.Set("reason", hcclient.AgentPromptStalled)
			if _, err = reservations.Transition(current, attemptID, "starting", store.Now(), obs, nil); err != nil {
				return nil, err
			}
		}
	} else {
		current.Set("status", "needs-attention")
		current.Set("error", stallMessage(cause))
		if state == "starting" {
			reason := hcclient.AgentPromptStalled
			if cause != nil {
				reason = hcclient.AgentPromptStalled + ": " + cause.Error()
			}
			if len(reason) > 500 {
				reason = reason[:500]
			}
			obs := ordjson.NewObject()
			obs.Set("at", store.Now())
			obs.Set("outcome", "launch-uncertain")
			obs.Set("reason", reason)
			if _, err = reservations.Transition(current, attemptID, "uncertain", store.Now(), obs, nil); err != nil {
				return nil, err
			}
		}
	}
	if err = s.SaveTask(current); err != nil {
		return nil, err
	}
	if !landed {
		return nil, fmt.Errorf("%s: %s", taskID, stallMessage(cause))
	}
	return current, nil
}

// Recover against the endpoint selected by the original attempt, even after
// the current conversation or workspace worker route changes.
func stalledRoute(s *store.Store, task *ordjson.Object, taskID, attemptID string) (store.Endpoint, error) {
	worker, err := reservations.Worker(task)
	if err != nil {
		return store.Endpoint{}, err
	}
	var params hcclient.RunParams
	native := asObject(policyField(worker, "native_launch"))
	if err = decode(policyField(native, "intent"), &params); err != nil {
		return store.Endpoint{}, err
	}
	env := params.Env
	if env["HC_SOCKET"] == "" || env["SUM_CORE_ID"] == "" || env["SUM_SESSION"] == "" || env["SUM_TASK"] != taskID || env["SUM_ATTEMPT"] != attemptID || env["SUM_ROLE"] != "worker" || params.Cwd != policyField(task, "worktree") {
		return store.Endpoint{}, fmt.Errorf("saved worker creation route is incomplete or mismatched")
	}
	host, err := s.Machine()
	if err != nil {
		return store.Endpoint{}, err
	}
	return store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, Socket: env["HC_SOCKET"], CoreID: env["SUM_CORE_ID"], Session: env["SUM_SESSION"]}, nil
}
