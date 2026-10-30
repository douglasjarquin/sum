package review

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/bindcmd"
	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/evidenceview"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/prepare"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/taskcheckout"
	"github.com/douglasjarquin/sum/go/internal/versions"
	"reflect"
)

func Launch(s *store.Store, ctx *ordjson.Object, runtimeRoot, sumctlPath string, args LaunchArgs) (*ordjson.Object, error) {
	coordinator := store.EndpointFromContext(ctx)
	if err := coordinator.Validate(); err != nil || coordinator.Role != "coordinator" {
		return nil, fmt.Errorf("a registered native coordinator is required to launch a reviewer")
	}
	instance, err := s.Instance()
	if err != nil {
		return nil, err
	}
	if instance == "" {
		return nil, fmt.Errorf("permanent installation identity is missing; reviewer launch is refused")
	}
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	if err = s.CheckMachine(task); err != nil {
		return nil, err
	}
	parent := store.EndpointFromContext(objectField(task, "parent"))
	if !coordinator.Native().SameAuthority(parent.Native()) {
		return nil, fmt.Errorf("only the task's recorded coordinator may launch its reviewer")
	}
	owner, err := s.Owner()
	if err != nil {
		return nil, err
	}
	if !coordinator.Native().SameAuthority(store.EndpointFromContext(owner).Native()) {
		return nil, fmt.Errorf("coordinator ownership changed; reviewer launch refused")
	}
	worktree := asString(task, "worktree")
	if worktree == "" {
		return nil, fmt.Errorf("task checkout is missing")
	}
	var checkout taskcheckout.Intent
	if err = decodeObject(objectField(task, "checkout_intent"), &checkout); err != nil {
		return nil, err
	}
	if _, err = taskcheckout.Verify(context.Background(), checkout, false); err != nil {
		return nil, fmt.Errorf("reviewer checkout identity: %w", err)
	}
	if resolvePath(checkout.Path) != resolvePath(worktree) || evidenceview.CurrentCandidate(task) == "" {
		return nil, fmt.Errorf("reviewer requires the recorded Git checkout and current candidate")
	}
	var rebind *ordjson.Object
	if recorded := objectField(task, "reviewer"); recorded != nil {
		old := objectField(task, "reviewer")
		oldEndpoint := store.EndpointFromContext(old)
		if err := oldEndpoint.Validate(); err != nil {
			return nil, fmt.Errorf("recorded reviewer launch is incomplete or uncertain; explicit reconciliation required")
		}
		client := hcclient.Client{Socket: oldEndpoint.Socket, CoreID: oldEndpoint.CoreID}
		terminal, err := client.Get(context.Background(), oldEndpoint.Address)
		if err == nil && terminal.State == "live" {
			if args.newReviewerOnly() {
				return nil, fmt.Errorf("launch options apply only to a new reviewer")
			}

			return promptNativeReviewer(context.Background(), s, task, recorded, sumctlPath)
		}
		stop, err := bindcmd.ReviewerNativeStopped(context.Background(), s, task, old)
		if err != nil {
			return nil, err
		}
		rebind = ordjson.NewObject()
		rebind.Set("from", old)
		rebind.Set("stop_evidence", stop)
	}
	harness, argv, preset, permissions, err := reviewerLaunch(s, ctx, runtimeRoot, args)
	if err != nil {
		return nil, err
	}
	brief, err := versions.ActiveBrief(s, task)
	if err != nil {
		return nil, err
	}
	access, err := prepare.TaskAccessArgs(s, task, harness, brief, worktree)
	if err != nil {
		return nil, err
	}
	argv = append(argv, access...)
	argv = append(argv, prepare.ClaudeReviewerMCPIsolationArgs(harness)...)
	skill, skillSHA, err := pinReviewerSkill(s, task, runtimeRoot)
	if err != nil {
		return nil, err
	}
	templateHome := asString(objectField(owner, "native_template"), "config_home")
	var ownerTemplate returns.NativeTemplate
	if err = decodeObject(objectField(owner, "native_template"), &ownerTemplate); err != nil {
		return nil, err
	}
	options := ownerTemplate.Options
	options.Cwd = worktree
	spec, err := launch.PrepareHCTemplate(runtimeRoot, templateHome, harness, argv, options)
	if err != nil {
		return nil, err
	}
	client := hcclient.Client{Socket: coordinator.Socket, CoreID: coordinator.CoreID}
	templates, err := client.Templates(context.Background())
	if err != nil {
		return nil, err
	}
	found := false
	for _, t := range templates.Templates {
		if t.Name == spec.Name && t.Error == nil && t.Command == templateCommand(spec.Argv) {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("selected HC core does not expose the qualified reviewer template command")
	}
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		return nil, err
	}
	attempt := hex.EncodeToString(random)
	ep := store.Endpoint{Schema: 2, Backend: "hc", Machine: coordinator.Machine, CoreID: coordinator.CoreID, Session: coordinator.Session, Socket: coordinator.Socket, Task: args.Task, Role: "reviewer", Attempt: attempt, BindingGeneration: 1, Cwd: worktree}
	reviewer := ep.Object()
	reviewer.Set("harness", harness)
	reviewer.Set("argv", stringList(argv))
	reviewer.Set("permissions", permissions)
	reviewer.Set("skill_path", skill)
	reviewer.Set("skill_sha256", skillSHA)
	reviewer.Set("candidate", orNil(evidenceview.CurrentCandidate(task)))
	nativeTemplate, _ := ordjson.FromValue(returns.NativeTemplate{Spec: spec, ConfigHome: templateHome, Options: options})
	reviewer.Set("native_template", nativeTemplate)
	if rebind != nil {
		reviewer.Set("rebind", rebind)
	}
	prompt := kickoffPrompt(sumctlPath, args.Task, evidenceview.CurrentCandidate(task), skill, policyCandidate(task, evidenceview.CurrentCandidate(task)))
	labels := map[string]string{"sum.installation": instance, "sum.task": args.Task, "sum.role": "reviewer", "sum.attempt": attempt}
	params := hcclient.RunParams{Argv: spec.RunArgv(prompt), Cwd: worktree, Name: "sum-r-" + strings.TrimPrefix(args.Task, "t-") + "-" + attempt[:12], Parent: coordinator.Address, Caller: coordinator.Address, OutliveParent: true, NoNotify: true, Keep: false, RM: false, Labels: labels, Env: map[string]string{"SUM_HOME": s.Root, "SUM_TASK": args.Task, "SUM_ROLE": "reviewer", "SUM_ATTEMPT": attempt, "SUM_BINDING_GENERATION": strconv.FormatUint(ep.BindingGeneration, 10), "SUM_CORE_ID": ep.CoreID, "SUM_SESSION": ep.Session, "HC_SOCKET": ep.Socket}}
	result, err := launchPreparedNative(context.Background(), s, task, ep, reviewer, params)
	if result != nil {
		result.Set("preset", preset)
	}
	return result, err
}
func templateCommand(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = "'" + strings.ReplaceAll(a, "'", "'\"'\"'") + "'"
	}
	return strings.Join(parts, " ")
}
func decodeObject(obj *ordjson.Object, out any) error {
	raw, err := ordjson.MarshalCompact(obj)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// The intent is durable before HC I/O. A prior attempt, including an uncertain
// attempt without an address, must be reconciled explicitly rather than replayed.
func launchPreparedNative(ctx context.Context, s *store.Store, task *ordjson.Object, endpoint store.Endpoint, reviewer *ordjson.Object, params hcclient.RunParams) (*ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	current, err := s.ReadTask(endpoint.Task)
	if err != nil {
		unlock()
		return nil, err
	}
	if existing := objectField(current, "reviewer"); existing != nil && (objectField(reviewer, "rebind") == nil || !store.EndpointFromContext(objectField(current, "reviewer")).Native().SameAuthority(store.EndpointFromContext(objectField(objectField(reviewer, "rebind"), "from")).Native())) {
		unlock()
		return nil, fmt.Errorf("reviewer launch intent already exists; no second native terminal was created")
	}
	if evidenceview.CurrentCandidate(current) != evidenceview.CurrentCandidate(task) {
		unlock()
		return nil, fmt.Errorf("candidate changed before reviewer launch")
	}
	if parent := objectField(task, "parent"); parent != nil {
		owner, ownerErr := s.Owner()
		if ownerErr != nil || !store.EndpointFromContext(parent).Native().SameAuthority(store.EndpointFromContext(objectField(current, "parent")).Native()) || !store.EndpointFromContext(parent).Native().SameAuthority(store.EndpointFromContext(owner).Native()) {
			unlock()
			return nil, fmt.Errorf("coordinator authority changed before reviewer launch")
		}
	}
	intent := ordjson.NewObject()
	intent.Set("attempt", endpoint.Attempt)
	intent.Set("state", "in-flight")
	intent.Set("at", store.Now())
	value, _ := ordjson.FromValue(params)
	intent.Set("params", value)
	reviewer.Set("native_launch", intent)

	current.Set("reviewer", reviewer)
	err = s.SaveTask(current)
	unlock()
	if err != nil {
		return nil, err
	}
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID}
	run, runErr := client.Run(ctx, params)
	unlock, err = s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err = s.ReadTask(endpoint.Task)
	if err != nil {
		return nil, err
	}
	saved := objectField(current, "reviewer")
	if asString(objectField(saved, "native_launch"), "attempt") != endpoint.Attempt {
		return nil, fmt.Errorf("reviewer attempt changed during launch; effect requires reconciliation")
	}
	intent = objectField(saved, "native_launch")
	intent.Set("completed_at", store.Now())
	if runErr != nil {
		state := "uncertain"
		var e *hcclient.Error
		if errors.As(runErr, &e) && e.Effect == hcclient.NotSubmitted {
			state = "not-submitted"
		}
		intent.Set("state", state)
		intent.Set("detail", runErr.Error())
		prompt := ordjson.NewObject()
		promptState := "uncertain"
		if state == "not-submitted" {
			promptState = "not-delivered"
		}
		prompt.Set("state", promptState)
		prompt.Set("candidate", orNil(evidenceview.CurrentCandidate(task)))
		saved.Set("prompt", prompt)
		if err = s.SaveTask(current); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("reviewer launch %s; attempt retained without replay: %w", state, runErr)
	}
	if run.Address == "" {
		intent.Set("state", "uncertain")
		intent.Set("detail", "HC returned no terminal address")
		if err = s.SaveTask(current); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("reviewer launch has no recoverable terminal address; intent retained")
	}
	endpoint.Address = run.Address
	endpoint.CreatedMS = run.CreatedMS
	for _, key := range endpoint.Object().Keys() {
		v, _ := endpoint.Object().Get(key)
		saved.Set(key, v)
	}

	var template returns.NativeTemplate
	templateErr := decodeObject(objectField(saved, "native_template"), &template)
	templateValid := templateErr == nil && template.Spec.Name != "" && run.Template != nil && *run.Template == template.Spec.Name && run.Submit != nil && *run.Submit == "tab"

	intent.Set("state", "created")
	result, _ := ordjson.FromValue(run)
	intent.Set("result", result)
	first, _ := ordjson.FromValue(run.FirstMessage)
	intent.Set("first_message", first)
	prompt := ordjson.NewObject()
	prompt.Set("state", "uncertain")
	if run.FirstMessage != nil {
		if run.FirstMessage.Sent {
			prompt.Set("state", "submitted")
		} else {
			prompt.Set("state", "not-delivered")
		}
	}
	prompt.Set("candidate", orNil(evidenceview.CurrentCandidate(task)))
	saved.Set("prompt", prompt)
	if run.PID != nil && run.Foreground != nil {
		saved.Set("incarnation", (incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: *run.PID, Foreground: run.Foreground}).Record(store.Now()))
	}
	if objectField(saved, "native_template") != nil && !templateValid {
		intent.Set("qualification_error", "returned terminal does not attest the qualified template and Tab submission")
		prompt.Set("state", "uncertain")
	}
	if rebind := objectField(saved, "rebind"); rebind != nil {
		rebind.Set("to", endpoint.Object())
		record, appendErr := evidence.Append(current, "reviewer-rebind", "coordinator", rebind, nil, objectField(current, "parent"))
		if appendErr != nil {
			return nil, appendErr
		}
		record.Set("brief_revision", evidence.ActiveRevision(s, current))
	}
	if err = s.SaveTask(current); err != nil {
		return nil, err
	}
	out := ordjson.NewObject()
	out.Set("task", endpoint.Task)
	out.Set("reviewer", saved)
	out.Set("native_launch", intent)
	out.Set("action", "created")
	out.Set("note", "Native reviewer terminal created; first-message outcome is separate and a matching reviewer-init receipt is required for authority.")
	return out, nil
}

func promptNativeReviewer(ctx context.Context, s *store.Store, task, recorded *ordjson.Object, sumctlPath string) (*ordjson.Object, error) {
	intent := objectField(recorded, "native_launch")
	if state := asString(intent, "state"); state != "created" || asString(intent, "qualification_error") != "" {
		return nil, fmt.Errorf("recorded reviewer launch outcome is unresolved or unqualified; explicit reconciliation is required")
	}
	endpoint := store.EndpointFromContext(objectField(task, "reviewer"))
	if err := endpoint.Validate(); err != nil {
		return nil, fmt.Errorf("recorded reviewer launch is incomplete or uncertain; explicit reconciliation is required")
	}
	unlockRecipient, err := returns.LockRecipient(s, ctx, endpoint.Object())
	if err != nil {
		return nil, err
	}
	defer unlockRecipient()
	recipient, err := returns.ObserveNative(ctx, endpoint, endpoint.Cwd)
	if err != nil {
		return nil, err
	}
	if err = recipient.Qualify(recorded); err != nil {
		return nil, err
	}
	registration, err := s.Registration(endpoint)
	if err != nil {
		return nil, err
	}
	var receipt incarnation.NativeReceipt
	if err = decodeObject(objectField(registration, "native_receipt"), &receipt); err != nil {
		return nil, fmt.Errorf("reviewer has no matching init receipt; inspect the saved launch and use explicit reviewer redeliver --attempt only for a proven unsent opening message")
	}
	verdict := incarnation.JudgeNative(objectField(recorded, "incarnation"), endpoint.Native(), recipient.Terminal, &receipt)
	if !verdict.Verified {
		return nil, fmt.Errorf("reviewer authority refused: %s", verdict.Reason)
	}
	candidate := evidenceview.CurrentCandidate(task)
	existing := objectField(recorded, "prompt")
	state := asString(existing, "state")
	if asString(existing, "candidate") == candidate && (state == "submitted" || state == "uncertain" || state == "in-flight" || state == "applied") {
		return nil, fmt.Errorf("reviewer kickoff already submitted or uncertain for this candidate; no automatic replay")
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	current, err := s.ReadTask(endpoint.Task)
	if err == nil && (!endpoint.Native().SameAuthority(store.EndpointFromContext(objectField(current, "reviewer")).Native()) || evidenceview.CurrentCandidate(current) != candidate) {
		err = fmt.Errorf("reviewer endpoint or candidate changed before input")
	}
	if err == nil {
		prior := objectField(objectField(current, "reviewer"), "prompt")
		priorState := asString(prior, "state")
		if asString(prior, "candidate") == candidate && (priorState == "submitted" || priorState == "uncertain" || priorState == "in-flight" || priorState == "applied") {
			err = fmt.Errorf("reviewer input already claimed for this candidate")
		}
	}
	if err == nil {
		prompt := ordjson.NewObject()
		prompt.Set("state", "in-flight")
		prompt.Set("candidate", orNil(candidate))
		prompt.Set("at", store.Now())
		objectField(current, "reviewer").Set("prompt", prompt)
		err = s.SaveTask(current)
	}
	unlock()
	if err != nil {
		return nil, err
	}
	result, sendErr := recipient.Send(ctx, kickoffPrompt(sumctlPath, endpoint.Task, candidate, asString(recorded, "skill_path"), policyCandidate(task, candidate)))
	unlock, err = s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err = s.ReadTask(endpoint.Task)
	if err != nil {
		return nil, err
	}
	if !endpoint.Native().SameAuthority(store.EndpointFromContext(objectField(current, "reviewer")).Native()) {
		return nil, fmt.Errorf("reviewer endpoint changed after input; outcome remains uncertain")
	}
	prompt := objectField(objectField(current, "reviewer"), "prompt")
	state, detail := returns.DeliveryFailure(sendErr)
	prompt.Set("state", state)
	prompt.Set("detail", detail)
	value, _ := ordjson.FromValue(result)
	prompt.Set("result", value)
	if err = s.SaveTask(current); err != nil {
		return nil, err
	}
	out := ordjson.NewObject()
	out.Set("task", endpoint.Task)
	out.Set("reviewer", objectField(current, "reviewer"))
	out.Set("prompt", prompt)
	out.Set("action", state)
	return out, nil
}

func validateNativeReview(s *store.Store, taskID, candidate, tool string, ctx *ordjson.Object) error {
	if ctx == nil {
		return nil
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	host, err := s.Machine()
	if err != nil {
		return err
	}
	role := endpointRole(host, task, ctx)
	if role == "worker" {
		return fmt.Errorf("the worker cannot record independent review of its own candidate")
	}
	if role != "reviewer" {
		return nil
	}
	endpoint := store.EndpointFromContext(ctx)
	registration, err := s.Registration(endpoint)
	if err != nil {
		return err
	}
	var receipt incarnation.NativeReceipt
	if err = decodeObject(objectField(registration, "native_receipt"), &receipt); err != nil {
		return fmt.Errorf("reviewer has no matching reviewer-init receipt")
	}
	c := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID}
	terminal, err := c.Get(context.Background(), endpoint.Address)
	if err != nil {
		return err
	}
	reviewer := objectField(task, "reviewer")
	v := incarnation.JudgeNative(objectField(reviewer, "incarnation"), endpoint.Native(), terminal, &receipt)
	if !v.Verified {
		return fmt.Errorf("reviewer authority refused: %s", v.Reason)
	}
	if candidate == "" || candidate != evidenceview.CurrentCandidate(task) || candidate != asString(objectField(reviewer, "prompt"), "candidate") {
		return fmt.Errorf("review must name the current candidate assigned to this reviewer")
	}
	return nil
}

// Redeliver is an explicit recovery of one known-unsent opening message. It
// never creates a terminal and never grants reviewer authority without init.
func Redeliver(s *store.Store, coordinator *ordjson.Object, taskID, attempt string) (*ordjson.Object, error) {
	if attempt == "" {
		return nil, fmt.Errorf("explicit reviewer launch attempt required")
	}
	instance, err := s.Instance()
	if err != nil {
		return nil, err
	}
	if instance == "" {
		return nil, fmt.Errorf("permanent installation identity is missing; reviewer recovery is refused")
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	caller := store.EndpointFromContext(coordinator)
	owner, err := s.Owner()
	if err != nil {
		return nil, err
	}
	if caller.Role != "coordinator" || caller.Validate() != nil || !caller.Native().SameAuthority(store.EndpointFromContext(owner).Native()) || !caller.Native().SameAuthority(store.EndpointFromContext(objectField(task, "parent")).Native()) {
		return nil, fmt.Errorf("only the recorded native coordinator may redeliver")
	}
	reviewer := objectField(task, "reviewer")
	ep := store.EndpointFromContext(reviewer)
	if ep.Validate() != nil || ep.Attempt != attempt {
		return nil, fmt.Errorf("reviewer attempt does not match the recorded native endpoint")
	}
	release, err := returns.LockRecipient(s, context.Background(), ep.Object())
	if err != nil {
		return nil, err
	}
	defer release()
	recipient, err := returns.ObserveNative(context.Background(), ep, ep.Cwd)
	if err != nil {
		return nil, err
	}
	if err = recipient.Qualify(reviewer); err != nil {
		return nil, err
	}
	intent := objectField(reviewer, "native_launch")
	first := objectField(objectField(intent, "result"), "first_message")
	var sent any
	if first != nil {
		sent, _ = first.Get("sent")
	}
	if first == nil || sent != false || asString(intent, "state") != "created" || asString(intent, "attempt") != attempt || asString(objectField(reviewer, "prompt"), "state") != "not-delivered" {
		return nil, fmt.Errorf("opening message is not proven unsent; submitted, uncertain and in-flight input requires explicit reconciliation")
	}
	var params hcclient.RunParams
	var template returns.NativeTemplate
	if err = decodeObject(objectField(intent, "params"), &params); err != nil {
		return nil, err
	}
	if err = decodeObject(objectField(reviewer, "native_template"), &template); err != nil {
		return nil, err
	}
	observed := recipient.Terminal
	if len(params.Argv) != 2 || params.Argv[0] != template.Spec.Name || observed.PID == nil || *observed.PID == 0 || observed.Foreground == nil || !reflect.DeepEqual(observed.Foreground.Argv, template.Spec.Argv) || resolvePath(observed.Foreground.Cwd) != resolvePath(ep.Cwd) {
		return nil, fmt.Errorf("reviewer opening process and qualified command cannot be proven")
	}
	for key, want := range map[string]string{"sum.installation": instance, "sum.task": taskID, "sum.role": "reviewer", "sum.attempt": attempt} {
		if observed.Labels[key] != want || params.Labels[key] != want {
			return nil, fmt.Errorf("reviewer launch ownership label differs: %s", key)
		}
	}
	if params.Env["SUM_ATTEMPT"] != attempt || params.Env["SUM_CORE_ID"] != ep.CoreID || params.Env["HC_SOCKET"] != ep.Socket || params.Env["SUM_TASK"] != taskID || params.Env["SUM_ROLE"] != "reviewer" {
		return nil, fmt.Errorf("reviewer launch environment does not match exact authority")
	}
	candidate := asString(reviewer, "candidate")
	if candidate == "" || candidate != evidenceview.CurrentCandidate(task) {
		return nil, fmt.Errorf("reviewer candidate changed before opening message recovery")
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	current, err := s.ReadTask(taskID)
	if err == nil {
		saved := objectField(current, "reviewer")
		if !ep.Native().SameAuthority(store.EndpointFromContext(saved).Native()) || !caller.Native().SameAuthority(store.EndpointFromContext(objectField(current, "parent")).Native()) || asString(objectField(saved, "prompt"), "state") != "not-delivered" || candidate != evidenceview.CurrentCandidate(current) {
			err = fmt.Errorf("reviewer recovery claim changed concurrently")
		} else {
			prompt := objectField(saved, "prompt")
			prompt.Set("state", "in-flight")
			prompt.Set("redeliver_attempt", attempt)
			saved.Set("incarnation", (incarnation.NativeEvidence{Endpoint: ep.Native(), PID: *observed.PID, Foreground: observed.Foreground}).Record(store.Now()))
			err = s.SaveTask(current)
		}
	}
	unlock()
	if err != nil {
		return nil, err
	}
	result, sendErr := recipient.Send(context.Background(), params.Argv[1])
	state, detail := returns.DeliveryFailure(sendErr)
	unlock, err = s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err = s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	saved := objectField(current, "reviewer")
	if !ep.Native().SameAuthority(store.EndpointFromContext(saved).Native()) {
		return nil, fmt.Errorf("reviewer endpoint changed after recovery input; outcome requires reconciliation")
	}
	prompt := objectField(saved, "prompt")
	prompt.Set("state", state)
	prompt.Set("detail", detail)
	value, _ := ordjson.FromValue(result)
	prompt.Set("result", value)
	if err = s.SaveTask(current); err != nil {
		return nil, err
	}
	out := ordjson.NewObject()
	out.Set("task", taskID)
	out.Set("reviewer", saved)
	out.Set("prompt", prompt)
	out.Set("note", "Opening message recovery never adopts a role; a matching reviewer-init receipt is required.")
	return out, nil
}
