package roleinit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/project"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

type DesignatedOpts struct {
	RuntimeRoot    string
	SumctlPath     string
	Store          *store.Store
	Ctx            *ordjson.Object
	Role           string
	Task           string
	Reclaim        bool
	CoreConfigHome string
	Harness        string
	Argv           []string
}
type nativeTemplate struct {
	Spec       launch.HCTemplate        `json:"spec"`
	ConfigHome string                   `json:"config_home"`
	Options    launch.HCTemplateOptions `json:"options"`
}

func asString(o *ordjson.Object, key string) string {
	if o == nil {
		return ""
	}
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}
func newInstanceID() (string, error) {
	buf := make([]byte, 16)
	_, err := rand.Read(buf)
	return hex.EncodeToString(buf), err
}
func copyEndpoint(ctx *ordjson.Object) *ordjson.Object {
	o := ordjson.NewObject()
	for _, k := range ctx.Keys() {
		v, _ := ctx.Get(k)
		o.Set(k, v)
	}
	return o
}
func decode(value any, out any) error {
	raw, err := ordjson.MarshalCompact(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// BootstrapContext discovers identity only for an explicitly requested coordinator
// bootstrap. Discovery reads an existing socket and never starts a core.
func BootstrapContext(ctx context.Context, socket, session, address string, s *store.Store, cwd string) (*ordjson.Object, error) {
	if !filepath.IsAbs(socket) || session == "" || address == "" {
		return nil, fmt.Errorf("explicit HC_SOCKET, HC_TERMINAL_ID and HC session required")
	}
	hello, err := hcclient.Inspect(ctx, socket)
	if err != nil {
		return nil, err
	}
	client := hcclient.Client{Socket: socket, CoreID: hello.Core.ID}
	terminal, err := client.Get(ctx, address)
	if err != nil {
		return nil, err
	}
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	return (store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: hello.Core.ID, Socket: socket, Session: session, Address: address, CreatedMS: terminal.CreatedMS, Cwd: cwd}).Object(), nil
}

func InitDesignated(opts DesignatedOpts) (*ordjson.Object, error) {
	if (opts.Role == "worker" || opts.Role == "reviewer") && opts.Task == "" {
		return nil, fmt.Errorf("--role %s needs --task TASK_ID.", opts.Role)
	}
	s := opts.Store
	endpoint := store.EndpointFromContext(opts.Ctx)
	if endpoint.Schema != 2 || endpoint.Backend != "hc" || endpoint.CoreID == "" || endpoint.Address == "" || !filepath.IsAbs(endpoint.Socket) {
		return nil, fmt.Errorf("verified current HC terminal context required; legacy context grants no role")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID}
	terminal, err := client.Get(ctx, endpoint.Address)
	if err != nil {
		return nil, err
	}
	if terminal.State != "live" || terminal.Address != endpoint.Address || terminal.CreatedMS != endpoint.CreatedMS {
		return nil, fmt.Errorf("current HC terminal is not live at the recorded identity")
	}
	owner, err := s.Owner()
	if err != nil {
		return nil, err
	}
	var seenOwner []byte
	seenOwner, _ = ordjson.MarshalCompact(owner)
	var reclaim incarnation.Verdict
	if opts.Reclaim && owner != nil {
		saved := store.EndpointFromContext(owner)
		observed, obErr := (&hcclient.Client{Socket: saved.Socket, CoreID: saved.CoreID}).Get(ctx, saved.Address)
		if obErr != nil {
			var rpc *hcclient.RPCError
			var detail struct {
				Kind string `json:"kind"`
			}
			if !errors.As(obErr, &rpc) || json.Unmarshal(rpc.Data, &detail) != nil || detail.Kind != "not_found" {
				return nil, fmt.Errorf("refusing reclaim: recorded coordinator cannot be observed: %w", obErr)
			}
			observed = hcclient.Terminal{Address: saved.Address, State: "finished"}
		}
		if observed.State != "finished" {
			return nil, fmt.Errorf("refusing reclaim: recorded coordinator terminal is %s; only a native finished terminal or stable not_found proves it gone", observed.State)
		}
		reclaim = incarnation.Verdict{Outcome: incarnation.Absent, Reason: "recorded terminal is finished or native not_found"}

	}
	// Unbound sessions can inspect their development procedure but cannot claim a
	// coordinator unless the user explicitly selected the coordinator operation.
	if opts.Role == "" && endpoint.Role == "" {
		return developerView(opts, s, endpoint), nil
	}
	role := endpoint.Role
	if opts.Role != "" {
		role = opts.Role
	}
	if role == "developer" {
		return developerView(opts, s, endpoint), nil
	}
	if endpoint.Role != "" && role != endpoint.Role {
		return nil, fmt.Errorf("current HC role cannot be changed by init")
	}
	var core *ordjson.Object
	if role == "coordinator" {
		core, err = coordinatorCore(opts.RuntimeRoot)
		if err != nil {
			return nil, err
		}
	}
	var pending *ordjson.Object
	pendingPath := filepath.Join(s.Home, "coordinator-launch.json")
	if role == "coordinator" && owner == nil {
		value, readErr := ordjson.ReadFile(pendingPath)
		if readErr == nil {
			pending, _ = value.(*ordjson.Object)
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return nil, readErr
		}
	}
	fresh := role == "coordinator" && (owner == nil || opts.Reclaim)
	if fresh {
		if pending != nil {
			expectedValue, _ := pending.Get("endpoint")
			expectedObject, _ := expectedValue.(*ordjson.Object)
			expected := store.EndpointFromContext(expectedObject)
			expected.Address = endpoint.Address
			expected.CreatedMS = endpoint.CreatedMS
			if !expected.Native().SameAuthority(endpoint.Native()) || expected.Socket != endpoint.Socket || expected.Session != endpoint.Session {
				return nil, fmt.Errorf("coordinator caller does not match persisted launch intent")
			}
		} else {
			if opts.Role != "coordinator" {
				return nil, fmt.Errorf("explicit coordinator bootstrap required")
			}
			if endpoint.Task != "" || endpoint.Role == "worker" || endpoint.Role == "reviewer" {
				return nil, fmt.Errorf("task execution cannot claim coordinator authority")
			}
			endpoint.Role = "coordinator"
			endpoint.Task = ""
			endpoint.Attempt, err = newInstanceID()
			if err != nil {
				return nil, err
			}
			endpoint.BindingGeneration = 1
			if owner != nil {
				prior := store.EndpointFromContext(owner)
				endpoint.BindingGeneration = prior.BindingGeneration + 1
			}
		}
	}
	observed, terminal, err := app.ObserveNative(endpoint)
	if err != nil {
		return nil, err
	}
	if nested := project.PaneInside(s, project.InstallationOf(s, opts.RuntimeRoot), terminal.Cwd); role == "coordinator" && nested != nil {
		return nil, fmt.Errorf("managed project terminal cannot claim coordinator authority")
	}
	var qualified any
	if fresh {
		if pending != nil {
			qualified, _ = pending.Get("native_template")
			var provenance nativeTemplate
			if err = decode(qualified, &provenance); err != nil {
				return nil, err
			}
			if err = launch.ValidateHCTemplate(provenance.ConfigHome, provenance.Spec, provenance.Options); err != nil {
				return nil, err
			}
			if err = verifyLoadedTemplate(ctx, client, provenance.Spec); err != nil {
				return nil, err
			}
			if !nativeProgramMatches(terminal, provenance.Spec) {
				return nil, fmt.Errorf("coordinator program differs from persisted template")
			}
			var intent hcclient.RunParams
			intentValue, _ := pending.Get("intent")
			if err = decode(intentValue, &intent); err != nil {
				return nil, err
			}
			instance, _ := s.Instance()
			for key, want := range map[string]string{"sum.installation": instance, "sum.role": "coordinator", "sum.attempt": endpoint.Attempt} {
				if want == "" || intent.Labels[key] != want || terminal.Labels[key] != want {
					return nil, fmt.Errorf("coordinator launch label mismatch")
				}
			}
		} else {
			qualified, err = qualifyCoordinator(opts, client, terminal)
		}
		if err != nil {
			return nil, err
		}
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	currentOwner, err := s.Owner()
	if err != nil {
		return nil, err
	}
	currentBytes, _ := ordjson.MarshalCompact(currentOwner)
	if string(currentBytes) != string(seenOwner) {
		return nil, fmt.Errorf("coordinator record changed during observation; retry")
	}
	var task *ordjson.Object
	var attempt *ordjson.Object
	if role == "coordinator" && !fresh {
		if owner == nil {
			return nil, fmt.Errorf("explicit coordinator bootstrap required")
		}
		recorded, _ := owner.Get("incarnation")
		receipt, err := app.NativeReceipt(owner)
		if err != nil {
			return nil, err
		}
		verdict := incarnation.JudgeNative(recorded, endpoint.Native(), terminal, receipt)
		if !verdict.Verified {
			return nil, fmt.Errorf("coordinator authority refused (%s: %s)", verdict.Outcome, verdict.Reason)
		}
		qualified, _ = owner.Get("native_template")
	} else if role == "worker" || role == "reviewer" {
		if opts.Task != "" && opts.Task != endpoint.Task {
			return nil, fmt.Errorf("requested task differs from current native task binding")
		}
		task, err = s.ReadTask(endpoint.Task)
		if err != nil {
			return nil, err
		}
		attempt, qualified, err = qualifyTask(s, task, endpoint, terminal)
		if err != nil {
			return nil, err
		}
	} else if role != "coordinator" {
		return nil, fmt.Errorf("unsupported native init role %q", role)
	}
	if pending != nil {
		saved, readErr := ordjson.ReadFile(pendingPath)
		if readErr != nil {
			return nil, readErr
		}
		savedObject, _ := saved.(*ordjson.Object)
		before, _ := ordjson.MarshalCompact(pending)
		after, _ := ordjson.MarshalCompact(savedObject)
		if string(before) != string(after) {
			return nil, fmt.Errorf("coordinator launch intent changed during init")
		}
	}
	receipt := incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: role + "-init", Attempt: endpoint.Attempt}
	receiptObj, _ := ordjson.FromValue(receipt)
	registration, err := s.Register(endpoint, role, taskValue(endpoint.Task), observed.Record(store.Now()))
	if err != nil {
		return nil, err
	}
	registration.Set("native_receipt", receiptObj)
	registration.Set("native_template", qualified)
	if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(endpoint)+".json"), registration); err != nil {
		return nil, err
	}
	if role == "coordinator" && fresh {
		owner = endpoint.Object()
		owner.Set("role", role)
		instance, _ := s.Instance()
		owner.Set("instance", instance)
		owner.Set("claimed_at", store.Now())
		owner.Set("incarnation", observed.Record(store.Now()))
		owner.Set("native_receipt", receiptObj)
		owner.Set("native_template", qualified)
		if pending == nil {
			var provenance nativeTemplate
			if err = decode(qualified, &provenance); err != nil {
				return nil, err
			}
			owner.Set("launch", existingCoordinatorLaunch(provenance.Spec))
		}
		if pending != nil {
			launchValue, _ := pending.Get("launch")
			owner.Set("launch", launchValue)
			pending.Set("native_receipt", receiptObj)
			pending.Set("endpoint", endpoint.Object())
			pending.Set("incarnation", observed.Record(store.Now()))
			if err = ordjson.WriteFile(pendingPath, pending); err != nil {
				return nil, err
			}
		}
		if opts.Reclaim {
			owner.Set("reclaimed_from", currentOwner)
			owner.Set("previous_observed", reclaim.Outcome)
		}
		if err = ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
			return nil, err
		}
	}
	if task != nil {
		_, hadReceipt := attempt.Get("native_receipt")
		attempt.Set("native_receipt", receiptObj)
		attempt.Set("incarnation", observed.Record(store.Now()))
		if launchValue, ok := attempt.Get("native_launch"); ok {
			if nativeLaunch, is := launchValue.(*ordjson.Object); is {
				nativeLaunch.Set("incarnation", observed.Record(store.Now()))
				nativeLaunch.Set("endpoint", endpoint.Object())
			}
		}
		if role == "reviewer" {
			for _, key := range endpoint.Object().Keys() {
				value, _ := endpoint.Object().Get(key)
				attempt.Set(key, value)
			}
		} else {
			task.Set("worker_endpoint", endpoint.Object())
		}
		if role == "worker" && asString(attempt, "state") != "running" {
			if _, err = reservations.Transition(task, endpoint.Attempt, "running", store.Now(), nil, nil); err != nil {
				return nil, err
			}
		}
		if role == "worker" {
			reconcileWorkerInit(task, attempt, receiptObj)
		}
		body := ordjson.NewObject()
		body.Set("attempt", endpoint.Attempt)
		body.Set("native_receipt", receiptObj)
		if !hadReceipt {
			if _, err = evidence.Append(task, role+"-init", role, body, nil, endpoint.Object()); err != nil {
				return nil, err
			}
		}
		if err = s.SaveTask(task); err != nil {
			return nil, err
		}
	}
	view := ordjson.NewObject()
	view.Set("home", s.Home)
	view.Set("installation", true)
	view.Set("role", role)
	view.Set("task", taskValue(endpoint.Task))
	view.Set("endpoint", endpoint.Object())
	environment, _ := ordjson.FromValue(map[string]string{"SUM_CORE_ID": endpoint.CoreID, "SUM_ROLE": endpoint.Role, "SUM_ATTEMPT": endpoint.Attempt, "SUM_BINDING_GENERATION": fmt.Sprint(endpoint.BindingGeneration)})
	view.Set("environment", environment)
	view.Set("registration", registration)
	view.Set("coordinator", owner)
	view.Set("registered", true)
	view.Set("procedure", roleProcedure(opts.RuntimeRoot, s, role, task, core))
	view.Set("note", "Read the validated procedure named under procedure before continuing.")
	return view, nil
}
func taskValue(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func developerView(opts DesignatedOpts, s *store.Store, e store.Endpoint) *ordjson.Object {
	v := ordjson.NewObject()
	v.Set("home", s.Home)
	v.Set("installation", true)
	v.Set("role", "developer")
	v.Set("task", nil)
	v.Set("endpoint", e.Object())
	v.Set("registered", false)
	v.Set("procedure", roleProcedure(opts.RuntimeRoot, s, "developer", nil, nil))
	v.Set("note", "No coordinator or task role was claimed; follow the developer procedure.")
	return v
}
func qualifyCoordinator(opts DesignatedOpts, client hcclient.Client, t hcclient.Terminal) (any, error) {
	harness := opts.Harness
	if harness == "" {
		harness = "codex"
	}
	options := launch.HCTemplateOptions{Cwd: t.Cwd}
	spec, err := launch.PrepareHCTemplate(opts.RuntimeRoot, opts.CoreConfigHome, harness, opts.Argv, options)
	if err != nil {
		return nil, err
	}
	if !nativeProgramMatches(t, spec) {
		return nil, fmt.Errorf("coordinator must already run the exact qualified HC template with Tab submission")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = verifyLoadedTemplate(ctx, client, spec); err != nil {
		return nil, err
	}
	return ordjson.FromValue(nativeTemplate{Spec: spec, ConfigHome: opts.CoreConfigHome, Options: options})
}
func qualifyTask(s *store.Store, task *ordjson.Object, e store.Endpoint, t hcclient.Terminal) (*ordjson.Object, any, error) {
	var attempt *ordjson.Object
	var err error
	if e.Role == "worker" {
		attempt, err = reservations.Worker(task)
	} else {
		value, _ := task.Get("reviewer")
		attempt, _ = value.(*ordjson.Object)
	}
	if err != nil || attempt == nil {
		return nil, nil, fmt.Errorf("no current persisted native task attempt")
	}
	if e.Role == "worker" && asString(attempt, "state") == "released" {
		return nil, nil, fmt.Errorf("released worker attempt cannot acquire native authority")
	}
	if e.Role == "worker" && asString(attempt, "id") != e.Attempt {
		return nil, nil, fmt.Errorf("worker attempt changed")
	}
	if e.Role == "reviewer" {
		if _, err = reviewerProcedure(s, task); err != nil {
			return nil, nil, err
		}
	}
	key := "worker_endpoint"
	if e.Role == "reviewer" {
		key = "reviewer"
	}
	if value, ok := task.Get(key); ok {
		if recorded, is := value.(*ordjson.Object); is && asString(recorded, "address") != "" {
			saved := store.EndpointFromContext(recorded)
			if !saved.Native().SameAuthority(e.Native()) || saved.Socket != e.Socket || saved.CreatedMS != e.CreatedMS {
				return nil, nil, fmt.Errorf("task already binds a different native terminal")
			}
		}
	}
	if receiptValue, ok := attempt.Get("native_receipt"); ok {
		var receipt incarnation.NativeReceipt
		if err = decode(receiptValue, &receipt); err != nil {
			return nil, nil, err
		}
		recorded, _ := attempt.Get("incarnation")
		verdict := incarnation.JudgeNative(recorded, e.Native(), t, &receipt)
		if !verdict.Verified {
			return nil, nil, fmt.Errorf("attempt already holds different native authority (%s)", verdict.Reason)
		}
	}
	value, _ := attempt.Get("native_launch")
	launchRecord, _ := value.(*ordjson.Object)
	if launchRecord == nil {
		return nil, nil, fmt.Errorf("no persisted native launch intent; historical procedures do not adopt HC")
	}
	intentValue, _ := launchRecord.Get("intent")
	if e.Role == "reviewer" {
		intentValue, _ = launchRecord.Get("params")
		if asString(launchRecord, "attempt") != e.Attempt {
			return nil, nil, fmt.Errorf("review attempt changed")
		}
	}
	var intent hcclient.RunParams
	if err = decode(intentValue, &intent); err != nil {
		return nil, nil, err
	}
	if !intent.OutliveParent || !intent.NoNotify || intent.Keep || intent.RM || t.Keep || t.RM || t.DieWithParent || t.Notify {
		return nil, nil, fmt.Errorf("native task lifecycle differs from managed launch contract")
	}
	instance, err := s.Instance()
	if err != nil {
		return nil, nil, err
	}
	for key, want := range map[string]string{"sum.installation": instance, "sum.task": e.Task, "sum.role": e.Role, "sum.attempt": e.Attempt} {
		if want == "" || intent.Labels[key] != want || t.Labels[key] != want {
			return nil, nil, fmt.Errorf("native launch label %s does not match persisted task attempt", key)
		}
	}
	for key, want := range map[string]string{"SUM_HOME": s.Root, "SUM_SESSION": e.Session, "SUM_CORE_ID": e.CoreID, "HC_SOCKET": e.Socket, "SUM_TASK": e.Task, "SUM_ROLE": e.Role, "SUM_ATTEMPT": e.Attempt, "SUM_BINDING_GENERATION": fmt.Sprint(e.BindingGeneration)} {
		if intent.Env[key] != want {
			return nil, nil, fmt.Errorf("native launch identity %s does not match current caller", key)
		}
	}
	templateValue, _ := launchRecord.Get("native_template")
	if e.Role == "reviewer" {
		templateValue, _ = attempt.Get("native_template")
	}
	var provenance nativeTemplate
	if err = decode(templateValue, &provenance); err != nil {
		return nil, nil, err
	}
	if err = launch.ValidateHCTemplate(provenance.ConfigHome, provenance.Spec, provenance.Options); err != nil {
		return nil, nil, err
	}
	if !nativeProgramMatches(t, provenance.Spec) || intent.Cwd != t.Cwd {
		return nil, nil, fmt.Errorf("native program differs from sealed launch template")
	}
	if e.Role == "worker" {
		brief, err := versions.ActiveBrief(s, task)
		if err != nil {
			return nil, nil, err
		}
		if len(intent.Argv) != 2 || intent.Argv[0] != provenance.Spec.Name || !strings.Contains(intent.Argv[1], brief) {
			return nil, nil, fmt.Errorf("launch pointer does not name the current sealed brief")
		}
	}
	if e.Role == "reviewer" {
		if len(intent.Argv) != 2 || intent.Argv[0] != provenance.Spec.Name || asString(attempt, "candidate") == "" || !strings.Contains(intent.Argv[1], asString(attempt, "candidate")) || !strings.Contains(intent.Argv[1], asString(attempt, "skill_path")) {
			return nil, nil, fmt.Errorf("reviewer launch pointer differs from the pinned procedure and candidate")
		}
	}
	if previous, err := s.Registration(e); err != nil {
		return nil, nil, err
	} else if previous != nil {
		if _, hasReceipt := previous.Get("native_receipt"); !hasReceipt {
			if _, attemptHasReceipt := attempt.Get("native_receipt"); attemptHasReceipt {
				return nil, nil, fmt.Errorf("receipt-bound attempt cannot adopt an unreceipted registration")
			}
			// terminal.run can finish and save its exact registration before the agent
			// initializes. The sealed intent checks above authorize only this first init;
			// they grant no business authority until its new application receipt is saved.
			if recorded, ok := previous.Get("incarnation"); ok && recorded != nil {
				var baseline incarnation.NativeEvidence
				if err = decode(recorded, &baseline); err != nil {
					return nil, nil, err
				}
				if !baseline.Endpoint.SameAuthority(e.Native()) || (baseline.PID != 0 && (t.PID == nil || baseline.PID != *t.PID)) || (baseline.Foreground != nil && !reflect.DeepEqual(baseline.Foreground, t.Foreground)) {
					return nil, nil, fmt.Errorf("unreceipted launch registration process differs from the current terminal")
				}
			}
			return attempt, templateValue, nil
		}
		recorded, _ := previous.Get("incarnation")
		receipt, err := app.NativeReceipt(previous)
		if err != nil {
			return nil, nil, err
		}
		v := incarnation.JudgeNative(recorded, e.Native(), t, receipt)
		if !v.Verified {
			return nil, nil, fmt.Errorf("native task authority refused (%s: %s)", v.Outcome, v.Reason)
		}
	}
	return attempt, templateValue, nil
}
func incarnationView(judged *incarnation.Verdict, judgedRole, role string, observed *ordjson.Object) *ordjson.Object {
	v := ordjson.NewObject()
	v.Set("observed", observed)
	if judged != nil {
		v.Set("judged", judgedRole)
		v.Set("outcome", judged.Outcome)
		v.Set("reason", judged.Reason)
	}
	return v
}

func nativeProgramMatches(t hcclient.Terminal, spec launch.HCTemplate) bool {
	return t.Template != nil && *t.Template == spec.Name && t.Submit != nil && *t.Submit == "tab" && reflect.DeepEqual(t.Argv, []string{"sh", "-c", templateCommand(spec)})
}

func reconcileWorkerInit(task, attempt, receipt *ordjson.Object) {
	errorText := asString(task, "error")
	startupError := errorText == "Native terminal created without a submitted first message; inspect and explicitly redeliver this attempt. Do not launch again." || errorText == "Created terminal does not attest the qualified template and Tab submit; inspect without relaunching." || strings.HasPrefix(errorText, "Launch/prompt uncertain:")
	if status := asString(task, "status"); status == "starting" || (status == "needs-attention" && startupError) {
		task.Set("status", "running")
	}
	if startupError {
		task.Set("error", nil)
	}
	value, _ := attempt.Get("native_launch")
	native, _ := value.(*ordjson.Object)
	if native != nil {
		reconciled := ordjson.NewObject()
		reconciled.Set("state", "receipt-confirmed")
		reconciled.Set("receipt", receipt)
		reconciled.Set("at", store.Now())
		native.Set("application_reconciliation", reconciled)
	}
}
