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

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
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
	parent := store.EndpointFromContext(ctx)
	owner, err := s.Owner()
	if err != nil {
		unlock()
		return nil, err
	}
	matches, err := s.Matches(owner, parent)
	if err != nil || !matches {
		unlock()
		return nil, fmt.Errorf("coordinator template belongs to a different current endpoint")
	}
	var provenance NativeTemplate
	if err = decode(policyField(owner, "native_template"), &provenance); err != nil {
		unlock()
		return nil, fmt.Errorf("coordinator has no verified native template configuration: %w", err)
	}
	if err = launch.ValidateHCTemplate(provenance.ConfigHome, provenance.Spec, provenance.Options); err != nil {
		unlock()
		return nil, err
	}
	checkout := asString(policyField(task, "worktree"))
	options := launch.HCTemplateOptions{Cwd: checkout, CodexHome: provenance.Options.CodexHome, ConfigFiles: provenance.Options.ConfigFiles}
	template, err := launch.PrepareHCTemplate(runtimeRoot, provenance.ConfigHome, asString(policyField(launchSpec, "harness")), argv, options)
	if err != nil {
		unlock()
		return nil, err
	}
	client := hcclient.Client{Socket: parent.Socket, CoreID: parent.CoreID}
	attemptID := asString(policyField(worker, "id"))
	instance, err := s.Instance()
	if err != nil {
		unlock()
		return nil, err
	}
	params := hcclient.RunParams{Argv: template.RunArgv(WorkerPrompt(s, runtimeRoot, taskID, briefFile, task)), Cwd: checkout, Name: NativeName(instance, taskID, "worker", attemptID), Parent: parent.Address, Caller: parent.Address, OutliveParent: true, NoNotify: true, Keep: false, RM: false,
		Labels: map[string]string{"sum.installation": instance, "sum.task": taskID, "sum.role": "worker", "sum.attempt": attemptID},
		Env:    map[string]string{"SUM_HOME": s.Root, "SUM_SESSION": parent.Session, "SUM_CORE_ID": parent.CoreID, "HC_SOCKET": parent.Socket, "SUM_TASK": taskID, "SUM_ROLE": "worker", "SUM_ATTEMPT": attemptID, "SUM_BINDING_GENERATION": "1"}}
	intent, _ := ordjson.FromValue(params)
	qualified, _ := ordjson.FromValue(NativeTemplate{template, provenance.ConfigHome, options})
	native := ordjson.NewObject()
	native.Set("intent", intent)
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
	runErr := verifyCoreTemplate(runCtx, &client, provenance.ConfigHome, template)
	if runErr == nil {
		runErr = launchStillAuthorized(s, taskID, attemptID, parent)
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
					asObject(policyField(w, "native_launch")).Set("effect", effect)
					_ = s.SaveTask(current)
				}
			}
			lock()
		}
		return FailStart(s, taskID, runErr)
	}
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: parent.Machine, CoreID: parent.CoreID, Session: parent.Session, Socket: parent.Socket, Address: result.Address, CreatedMS: result.CreatedMS, Task: taskID, Role: "worker", Attempt: attemptID, BindingGeneration: 1, Cwd: checkout}
	if err = endpoint.Validate(); err != nil {
		return FailStart(s, taskID, err)
	}
	attested := result.Template != nil && *result.Template == template.Name && result.Submit != nil && *result.Submit == "tab"
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
	native.Set("endpoint", endpoint.Object())
	var first any
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
	native.Set("incarnation", evidence.Record(store.Now()))
	if !attested {
		current.Set("status", "needs-attention")
		current.Set("error", "Created terminal does not attest the qualified template and Tab submit; inspect without relaunching.")
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

func launchStillAuthorized(s *store.Store, taskID, attemptID string, parent store.Endpoint) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	owner, err := s.Owner()
	if err != nil {
		return err
	}
	matches, err := s.Matches(owner, parent)
	if err != nil || !matches {
		return fmt.Errorf("coordinator authority changed before native launch")
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
