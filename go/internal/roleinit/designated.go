package roleinit

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

type DesignatedOpts struct {
	RuntimeRoot string
	SumctlPath  string
	Store       *store.Store
	Ctx         *ordjson.Object
	Role        string
	Task        string
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
func decode(value any, out any) error {
	raw, err := ordjson.MarshalCompact(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func InitDesignated(opts DesignatedOpts) (*ordjson.Object, error) {
	s := opts.Store
	if opts.Role == "developer" && opts.Task == "" {
		return developerView(opts, s, store.Endpoint{}), nil
	}
	endpoint := store.EndpointFromContext(opts.Ctx)
	role := opts.Role
	if role == "" {
		role = endpoint.Role
	}
	if role != "worker" && role != "reviewer" {
		return nil, fmt.Errorf("native init requires a recorded worker or reviewer role")
	}
	if endpoint.Role != role || endpoint.Task == "" || (opts.Task != "" && opts.Task != endpoint.Task) {
		return nil, fmt.Errorf("requested task or role differs from current native task binding")
	}
	if endpoint.Schema != 2 || endpoint.Backend != "hc" || endpoint.CoreID == "" || endpoint.Address == "" || !filepath.IsAbs(endpoint.Socket) {
		return nil, fmt.Errorf("verified current HC task context required")
	}
	observed, terminal, err := app.ObserveNative(endpoint)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(terminal.Cwd) {
		return nil, fmt.Errorf("current HC terminal has no absolute observed working directory")
	}
	endpoint.Cwd = terminal.Cwd
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.ReadTask(endpoint.Task)
	if err != nil {
		return nil, err
	}
	attempt, qualified, err := qualifyTask(s, task, endpoint, terminal)
	if err != nil {
		return nil, err
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
	view.Set("registered", true)
	view.Set("procedure", roleProcedure(opts.RuntimeRoot, s, role, task, nil))
	view.Set("note", "Read the validated procedure named under `procedure` before continuing.")
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
	if e.Schema == 0 {
		v.Set("endpoint", nil)
	} else {
		v.Set("endpoint", e.Object())
	}
	v.Set("registered", false)
	v.Set("procedure", roleProcedure(opts.RuntimeRoot, s, "developer", nil, nil))
	v.Set("note", "No coordinator or task role was claimed; follow the developer procedure.")
	return v
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
	if launchRecord == nil && e.Role == "reviewer" {
		if _, hasReceipt := attempt.Get("native_receipt"); !hasReceipt {
			return nil, nil, fmt.Errorf("reviewer rebind needs its own native init receipt")
		}
		kickoffValue, _ := attempt.Get("kickoff")
		kickoff, _ := kickoffValue.(*ordjson.Object)
		if asString(kickoff, "kind") != "reviewer-kickoff" || asString(kickoff, "source") != "native-rebind" || asString(kickoff, "candidate") == "" || asString(kickoff, "candidate") != asString(attempt, "candidate") {
			return nil, nil, fmt.Errorf("reviewer rebind kickoff does not match its current candidate")
		}
		endpointValue, _ := kickoff.Get("endpoint")
		endpointObject, _ := endpointValue.(*ordjson.Object)
		bound := store.EndpointFromContext(endpointObject)
		if !bound.Native().SameAuthority(e.Native()) || bound.Socket != e.Socket || bound.Session != e.Session {
			return nil, nil, fmt.Errorf("reviewer kickoff belongs to another native binding")
		}
		templateValue, _ := attempt.Get("native_template")
		var provenance nativeTemplate
		if err = decode(templateValue, &provenance); err != nil {
			return nil, nil, err
		}
		if err = launch.ValidateHCTemplate(provenance.ConfigHome, provenance.Spec, provenance.Options); err != nil {
			return nil, nil, err
		}
		if !nativeProgramMatches(t, provenance.Spec) {
			return nil, nil, fmt.Errorf("reviewer rebind program differs from its qualified template")
		}
		return attempt, templateValue, nil
	}
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
	if !intent.OutliveParent || !intent.NoNotify || intent.Keep || intent.RM || t.RM || t.DieWithParent || t.Notify {
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
	pointer := ""
	if len(intent.Argv) == 2 {
		pointer = intent.Argv[1]
	} else if len(intent.Argv) == 1 && provenance.Spec.Schema == 2 {
		pointer = asString(launchRecord, "opening")
	}
	if e.Role == "worker" {
		brief, err := versions.ActiveBrief(s, task)
		if err != nil {
			return nil, nil, err
		}
		if len(intent.Argv) == 0 || intent.Argv[0] != provenance.Spec.Name || !strings.Contains(pointer, brief) {
			return nil, nil, fmt.Errorf("launch pointer does not name the current sealed brief")
		}
	}
	if e.Role == "reviewer" {
		if len(intent.Argv) == 0 || intent.Argv[0] != provenance.Spec.Name || asString(attempt, "candidate") == "" || !strings.Contains(pointer, asString(attempt, "candidate")) || !strings.Contains(pointer, asString(attempt, "skill_path")) {
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

			if recorded, ok := previous.Get("incarnation"); ok && recorded != nil {
				var baseline incarnation.NativeEvidence
				if err = decode(recorded, &baseline); err != nil {
					return nil, nil, err
				}
				if baseline.Foreground == nil && t.Keep {
					return nil, nil, fmt.Errorf("promotion preceded original foreground evidence; inspect without granting replacement authority")
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
	return t.Template != nil && *t.Template == spec.Name && t.Submit != nil && *t.Submit == spec.Submit && reflect.DeepEqual(t.Argv, []string{"sh", "-c", templateCommand(spec)})
}

func reconcileWorkerInit(task, attempt, receipt *ordjson.Object) {
	errorText := asString(task, "error")
	startupError := errorText == "Native terminal created without a submitted first message; inspect and explicitly redeliver this attempt. Do not launch again." || errorText == "Created terminal does not attest the qualified template and submit key; inspect without relaunching." || strings.HasPrefix(errorText, "Launch/prompt uncertain:")
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

func templateCommand(spec launch.HCTemplate) string { return spec.Command() }
