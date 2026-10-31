package roleinit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type CoordinatorStartOpts struct {
	RuntimeRoot    string
	SumctlPath     string
	Socket         string
	Session        string
	CoreConfigHome string
	Launch         launch.ResolveArgs
}

func StartCoordinator(s *store.Store, opts CoordinatorStartOpts) (*ordjson.Object, error) {
	if !s.Designated() {
		return nil, fmt.Errorf("coordinator start requires the designated installation")
	}
	if !filepath.IsAbs(opts.Socket) || opts.Session == "" || !filepath.IsAbs(opts.CoreConfigHome) {
		return nil, fmt.Errorf("explicit socket, session and HC config home required")
	}
	if _, err := coordinatorCore(opts.RuntimeRoot); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hello, err := hcclient.Inspect(ctx, opts.Socket)
	if err != nil {
		return nil, err
	}
	client := hcclient.Client{Socket: opts.Socket, CoreID: hello.Core.ID}
	opts.Launch.RuntimeRoot = opts.RuntimeRoot
	if opts.Launch.Harness == "" {
		opts.Launch.Harness = "codex"
	}
	resolved, err := launch.Resolve(s, nil, opts.Launch)
	if err != nil {
		return nil, err
	}
	argvValue, _ := resolved.Get("argv")
	var argv []string
	if err = decode(argvValue, &argv); err != nil {
		return nil, err
	}
	options := launch.HCTemplateOptions{Cwd: opts.RuntimeRoot}
	spec, err := launch.PrepareHCTemplate(opts.RuntimeRoot, opts.CoreConfigHome, asString(resolved, "harness"), argv, options)
	if err != nil {
		return nil, err
	}
	if err = verifyLoadedTemplate(ctx, client, spec); err != nil {
		return nil, err
	}
	attempt, err := newInstanceID()
	if err != nil {
		return nil, err
	}
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	instance, err := s.Instance()
	if err != nil {
		return nil, err
	}
	if instance == "" {
		return nil, fmt.Errorf("installation identity required before coordinator start")
	}
	intent := hcclient.RunParams{Argv: spec.RunArgv("First run `" + shquote.CommandFor(opts.SumctlPath, s.Root, "init") + "`, then read the verified coordinator procedure named by init and follow it."), Cwd: opts.RuntimeRoot, NoParent: true, OutliveParent: true, NoNotify: true, Keep: false, RM: false, Name: "sum-" + attempt[:28], Labels: map[string]string{"sum.installation": instance, "sum.role": "coordinator", "sum.attempt": attempt}, Env: map[string]string{"SUM_HOME": s.Root, "SUM_SESSION": opts.Session, "SUM_CORE_ID": hello.Core.ID, "HC_SOCKET": opts.Socket, "SUM_ROLE": "coordinator", "SUM_ATTEMPT": attempt, "SUM_BINDING_GENERATION": "1"}}
	expected := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: hello.Core.ID, Socket: opts.Socket, Session: opts.Session, Role: "coordinator", Attempt: attempt, BindingGeneration: 1, Cwd: opts.RuntimeRoot}
	record := ordjson.NewObject()
	record.Set("endpoint", expected.Object())
	record.Set("intent", mustObject(intent))
	record.Set("native_template", mustObject(nativeTemplate{Spec: spec, ConfigHome: opts.CoreConfigHome, Options: options}))
	record.Set("effect", "claimed")
	record.Set("claimed_at", store.Now())
	record.Set("launch", resolved)
	path := filepath.Join(s.Home, "coordinator-launch.json")
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	owner, err := s.Owner()
	if err != nil {
		unlock()
		return nil, err
	}
	if owner != nil {
		unlock()
		return nil, fmt.Errorf("coordinator already recorded; init verifies it, and explicit verified reclaim handles replacement")
	}
	if _, err = os.Stat(path); err == nil {
		unlock()
		return nil, fmt.Errorf("coordinator launch intent already exists; reconcile its receipt or uncertain native effect before another start")
	} else if !os.IsNotExist(err) {
		unlock()
		return nil, err
	}
	if err = ordjson.WriteFile(path, record); err != nil {
		unlock()
		return nil, err
	}
	if err = unlock(); err != nil {
		return nil, err
	}
	result, runErr := client.Run(ctx, intent)
	unlock, err = s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()

	value, err := ordjson.ReadFile(path)
	if err != nil {
		return nil, err
	}
	record, _ = value.(*ordjson.Object)
	if record == nil {
		return nil, fmt.Errorf("coordinator launch intent unreadable")
	}
	if runErr != nil {
		record.Set("effect", "uncertain")
		record.Set("error", runErr.Error())
	} else {
		record.Set("effect", "created")
		record.Set("result", mustObject(result))
		record.Set("first_message", mustObject(result.FirstMessage))
		if result.Address != "" {
			expected.Address = result.Address
			expected.CreatedMS = result.CreatedMS
			record.Set("endpoint", expected.Object())
		}
	}
	reconcileCoordinatorReceipt(record, expected)
	if err = ordjson.WriteFile(path, record); err != nil {
		return nil, err
	}
	record.Set("note", "Native launch is recorded; coordinator authority requires its exact init receipt. Native status and first-message started are diagnostic only.")
	return record, nil
}
func mustObject(value any) any { o, _ := ordjson.FromValue(value); return o }
func templateCommand(spec launch.HCTemplate) string {
	return launch.TemplateCommand(spec.Argv)
}
func verifyLoadedTemplate(ctx context.Context, client hcclient.Client, spec launch.HCTemplate) error {
	templates, err := client.Templates(ctx)
	if err != nil {
		return err
	}
	for _, item := range templates.Templates {
		if item.Name == spec.Name && item.Error == nil && item.Command == templateCommand(spec) {
			return nil
		}
	}
	return fmt.Errorf("selected HC core does not expose the exact qualified template command")
}

func reconcileCoordinatorReceipt(record *ordjson.Object, expected store.Endpoint) {
	receipt, err := app.NativeReceipt(record)
	if err != nil {
		return
	}
	endpointValue, _ := record.Get("endpoint")
	endpointObject, _ := endpointValue.(*ordjson.Object)
	saved := store.EndpointFromContext(endpointObject)
	if receipt.Kind != "coordinator-init" || receipt.Attempt != expected.Attempt || !receipt.Endpoint.SameAuthority(saved.Native()) || saved.Role != "coordinator" || saved.CoreID != expected.CoreID || saved.Machine != expected.Machine || saved.Socket != expected.Socket || saved.Session != expected.Session || saved.BindingGeneration != expected.BindingGeneration {
		return
	}
	if expected.Address != "" && !receipt.Endpoint.SameAuthority(expected.Native()) {
		return
	}
	state := ordjson.NewObject()
	state.Set("state", "receipt-confirmed")
	state.Set("receipt", mustObject(receipt))
	state.Set("at", store.Now())
	record.Set("application_reconciliation", state)
}
