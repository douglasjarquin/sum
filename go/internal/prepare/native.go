package prepare

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcboot"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type NativeTemplate struct {
	Spec       launch.HCTemplate        `json:"spec"`
	ConfigHome string                   `json:"config_home"`
	Options    launch.HCTemplateOptions `json:"options"`
}

func decode(value any, target any) error {
	data, err := ordjson.MarshalCompact(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
func NativeName(installation, task, role, attempt string) string {
	sum := sha256.Sum256([]byte(installation + "\x00" + task + "\x00" + role + "\x00" + attempt))
	return "sum-" + hex.EncodeToString(sum[:14])
}

func startNative(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID, briefFile string, task, worker, launchSpec *ordjson.Object, argv []string, unlock func() error) (*ordjson.Object, error) {
	token := asString(policyField(ctx, "claim"))
	if err := s.CaptureClaim(token); err != nil {
		unlock()
		return nil, err
	}
	route, err := hcboot.SelectedRoute(s, "", "", "")
	if err != nil {
		unlock()
		return nil, err
	}
	loaded, err := settings.LoadSettings(s)
	if err != nil {
		unlock()
		return nil, err
	}
	checkout := asString(policyField(task, "worktree"))
	options := launch.HCTemplateOptions{Cwd: checkout, CodexHome: loaded.Template.CodexHome, ConfigFiles: loaded.Template.ConfigFiles, InputEnv: loaded.Template.InputEnv}
	template, err := launch.PrepareHCTemplate(runtimeRoot, route.ConfigHome, asString(policyField(launchSpec, "harness")), argv, options)
	if err != nil {
		unlock()
		return nil, err
	}
	unlock()
	route, err = hcboot.EnsureWorkspaceRoute(s, runtimeRoot)
	if err != nil {
		return nil, err
	}
	unlock, err = s.Lock()
	if err != nil {
		return nil, err
	}
	current, err := s.ReadTask(taskID)
	if err != nil {
		unlock()
		return nil, err
	}
	currentWorker, err := reservations.Worker(current)
	if err != nil {
		unlock()
		return nil, err
	}
	currentState := policyField(currentWorker, "state")
	if policyField(currentWorker, "id") != policyField(worker, "id") || (currentState != "held" && currentState != "running") || policyField(current, "status") != "prepared" || policyField(current, "worktree") != checkout {
		unlock()
		return nil, fmt.Errorf("worker attempt changed while ensuring its runtime")
	}
	task, worker = current, currentWorker
	task.Set("launch", launchSpec)
	host, err := s.Machine()
	if err != nil {
		unlock()
		return nil, err
	}
	client := hcclient.Client{Socket: route.Socket, CoreID: route.CoreID}
	attemptID := asString(policyField(worker, "id"))
	instance, err := s.Instance()
	if err != nil {
		unlock()
		return nil, err
	}
	opening := WorkerPrompt(s, runtimeRoot, taskID, briefFile, task)
	params := hcclient.RunParams{Argv: []string{template.Name}, Cwd: checkout, Name: NativeName(instance, taskID, "worker", attemptID), OutliveParent: true, NoNotify: true, Keep: false, RM: false,
		Labels: workerLabels(instance, taskID, attemptID),
		Env:    map[string]string{"SUM_HOME": s.Root, "SUM_SESSION": route.Session, "SUM_CORE_ID": route.CoreID, "HC_SOCKET": route.Socket, "SUM_TASK": taskID, "SUM_ROLE": "worker", "SUM_ATTEMPT": attemptID, "SUM_BINDING_GENERATION": "1"}}
	template.BindInputEnvironment(params.Env)
	intent, _ := ordjson.FromValue(params)
	qualified, _ := ordjson.FromValue(NativeTemplate{template, route.ConfigHome, options})
	native := ordjson.NewObject()
	native.Set("intent", intent)
	native.Set("opening", opening)
	native.Set("native_template", qualified)
	native.Set("effect", "claimed")
	native.Set("claimed_at", store.Now())
	worker.Set("native_launch", native)
	launchSpec.Set("hc_template", qualified)
	task.Set("status", "starting")
	if _, err = reservations.Transition(task, attemptID, "starting", store.Now(), nil, nil); err != nil {
		unlock()
		return nil, err
	}
	if err = s.SaveTask(task); err != nil {
		unlock()
		return nil, err
	}
	unlock()
	runCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var result hcclient.RunResult
	runErr := verifyCoreTemplate(runCtx, &client, route.ConfigHome, template)
	if runErr == nil {
		runErr = launchStillAuthorized(s, taskID, attemptID, token)
	}
	if runErr == nil && template.Harness == "grok" {
		runErr = launch.TrustGrokRepository(runCtx, template.InputEnv["GROK_HOME"], checkout)
	}
	if runErr != nil {
		runErr = &hcclient.Error{Method: "terminal.run", Effect: hcclient.NotSubmitted, Cause: runErr}
	} else {
		result, runErr = client.Run(runCtx, params)
	}
	if runErr != nil {
		effect := "uncertain"
		var nativeError *hcclient.Error
		if errors.As(runErr, &nativeError) {
			effect = string(nativeError.Effect)
		}
		if lock, e := s.Lock(); e == nil {
			if current, e := s.ReadTask(taskID); e == nil {
				if w, e := reservations.Worker(current); e == nil && asString(policyField(w, "id")) == attemptID {
					n := asObject(policyField(w, "native_launch"))
					if store.EndpointFromContext(asObject(policyField(current, "worker_endpoint"))).Attempt == attemptID || policyField(w, "native_receipt") != nil {
						n.Set("late_creation_error", runErr.Error())
					} else {
						n.Set("effect", effect)
					}
					_ = s.SaveTask(current)
				}
			}
			lock()
		}
		if current, readErr := s.ReadTask(taskID); readErr == nil && store.EndpointFromContext(asObject(policyField(current, "worker_endpoint"))).Attempt == attemptID {
			return current, nil
		}
		return FailStart(s, taskID, runErr)
	}
	created, err := func() (*ordjson.Object, error) {
		endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: route.CoreID, Session: route.Session, Socket: route.Socket, Address: result.Terminal.Address, CreatedMS: result.Terminal.CreatedMS, Task: taskID, Role: "worker", Attempt: attemptID, BindingGeneration: 1, Cwd: checkout}
		if err = endpoint.Validate(); err != nil {
			return FailStart(s, taskID, err)
		}
		attested := result.Template != nil && *result.Template == template.Name && result.Submit != nil && *result.Submit == template.Submit
		lock, err := s.Lock()
		if err != nil {
			return nil, err
		}
		defer lock()
		current, err := s.ReadTask(taskID)
		if err != nil {
			return nil, err
		}
		currentWorker, err := reservations.Worker(current)
		if err != nil {
			return nil, err
		}
		if asString(policyField(currentWorker, "id")) != attemptID || (asString(policyField(currentWorker, "state")) != "starting" && asString(policyField(currentWorker, "state")) != "running") {
			return nil, fmt.Errorf("worker attempt changed during creation; inspect terminal %s", result.Address)
		}
		native = asObject(policyField(currentWorker, "native_launch"))
		if existing := asObject(policyField(current, "worker_endpoint")); existing != nil && store.EndpointFromContext(existing).Attempt == attemptID {
			if !store.EndpointFromContext(existing).Native().SameAuthority(endpoint.Native()) {
				return nil, fmt.Errorf("late creation identity conflicts with reconciled endpoint")
			}
			return current, nil
		}
		native.Set("endpoint", endpoint.Object())
		var first any
		if result.FirstMessage == nil && template.Schema == 2 {
			result.FirstMessage = &hcclient.FirstMessage{Sent: false}
		}
		if result.FirstMessage != nil {
			first, _ = ordjson.FromValue(result.FirstMessage)
		}
		native.Set("first_message", first)
		native.Set("effect", "created")
		current.Set("worker_endpoint", endpoint.Object())
		evidence := incarnation.NativeEvidence{Endpoint: endpoint.Native(), Foreground: result.Foreground}
		if result.PID != nil {
			evidence.PID = *result.PID
		}
		if policyField(currentWorker, "native_receipt") == nil {
			native.Set("incarnation", evidence.Record(store.Now()))
		}
		if !attested {
			current.Set("status", "needs-attention")
			current.Set("error", "Created terminal does not attest the qualified template and submit key; inspect without relaunching.")
			obs := ordjson.NewObject()
			obs.Set("at", store.Now())
			obs.Set("outcome", "launch-uncertain")
			obs.Set("reason", "template attestation mismatch")
			if _, err = reservations.Transition(current, attemptID, "uncertain", store.Now(), obs, nil); err != nil {
				return nil, err
			}
			if err = s.SaveTask(current); err != nil {
				return nil, err
			}
			return current, nil
		}
		registration, err := s.Registration(endpoint)
		if err != nil {
			return nil, err
		}
		if registration == nil {
			registration, err = s.Register(endpoint, "worker", taskID, evidence.Record(store.Now()))
			if err != nil {
				return nil, err
			}
		}
		registration.Set("native_template", qualified)
		if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(endpoint)+".json"), registration); err != nil {
			return nil, err
		}
		if asString(policyField(currentWorker, "state")) != "running" && (result.FirstMessage == nil || !result.FirstMessage.Sent) {
			current.Set("status", "needs-attention")
			current.Set("error", "Native terminal created without a submitted first message; inspect and explicitly redeliver this attempt. Do not launch again.")
		}
		if err = s.SaveTask(current); err != nil {
			return nil, err
		}
		return current, nil
	}()
	if err != nil || created == nil {
		return created, err
	}
	return deliverWorkerOpening(s, created, template, result.Terminal, opening)
}

func verifyCoreTemplate(ctx context.Context, client *hcclient.Client, config string, spec launch.HCTemplate) error {
	data, err := os.ReadFile(filepath.Join(config, "herdr-core/templates", spec.Name+".md"))
	if err != nil {
		return err
	}
	expected := ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "command: ") {
			expected = strings.TrimPrefix(line, "command: ")
			break
		}
	}
	templates, err := client.Templates(ctx)
	if err != nil {
		return err
	}
	for _, entry := range templates.Templates {
		if entry.Name == spec.Name && entry.Error == nil && entry.Command == expected && expected != "" {
			return nil
		}
	}
	return fmt.Errorf("selected HC core does not expose the qualified template command; verify its explicit config home")
}

func launchStillAuthorized(s *store.Store, taskID, attemptID, token string) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := s.CheckClaim(token); err != nil {
		return err
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	worker, err := reservations.Worker(task)
	if err != nil {
		return err
	}
	if policyField(worker, "id") != attemptID || policyField(worker, "state") != "starting" || policyField(task, "status") != "starting" {
		return fmt.Errorf("worker attempt changed before native launch")
	}
	return nil
}

func deliverWorkerOpening(s *store.Store, task *ordjson.Object, spec launch.HCTemplate, initial hcclient.Terminal, opening string) (*ordjson.Object, error) {
	ep := store.EndpointFromContext(asObject(policyField(task, "worker_endpoint")))
	client := hcclient.Client{Socket: ep.Socket, CoreID: ep.CoreID}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	release, err := s.RecipientLock(ctx, [3]string{ep.Machine, ep.CoreID, ep.Address})
	if err != nil {
		return task, err
	}
	defer release()
	current, err := s.ReadTask(ep.Task)
	if err != nil {
		return nil, err
	}
	w, err := reservations.Worker(current)
	if err != nil {
		return nil, err
	}
	n := asObject(policyField(w, "native_launch"))
	if policyField(w, "native_receipt") != nil || policyField(n, "opening_delivery") != nil || policyField(n, "redelivery") != nil {
		return current, nil
	}
	launch.DeliverOpening(ctx, &client, initial, spec, opening, func(progress launch.OpeningProgress) error {
		unlock, err := s.Lock()
		if err != nil {
			return err
		}
		defer unlock()
		current, err := s.ReadTask(ep.Task)
		if err != nil {
			return err
		}
		w, err := reservations.Worker(current)
		if err != nil {
			return err
		}
		if policyField(w, "id") != ep.Attempt {
			return fmt.Errorf("worker attempt changed before opening")
		}
		n := asObject(policyField(w, "native_launch"))
		if n == nil {
			return fmt.Errorf("worker launch intent missing")
		}
		if policyField(w, "native_receipt") != nil {
			return fmt.Errorf("worker already initialized; no opening input sent")
		}
		value, _ := ordjson.FromValue(progress)
		n.Set("opening_delivery", value)
		n.Set("first_message", func() any { v, _ := ordjson.FromValue(progress.First); return v }())
		if progress.Terminal.PID != nil && progress.Terminal.Foreground != nil && launch.OriginalProgram(initial, progress.Terminal, spec) == nil {
			evidence := incarnation.NativeEvidence{Endpoint: ep.Native(), PID: *progress.Terminal.PID, Foreground: progress.Terminal.Foreground}.Record(store.Now())
			n.Set("incarnation", evidence)
			if err = s.SetIncarnation(ep, evidence); err != nil {
				return err
			}
		}
		if progress.State == "submitted" {
			current.Set("status", "starting")
			current.Set("error", nil)
		} else if progress.State != "in-flight" {
			current.Set("status", "needs-attention")
			current.Set("error", LaunchUncertainPrefix+": "+progress.Detail)
		}
		return s.SaveTask(current)
	})
	return s.ReadTask(ep.Task)
}
