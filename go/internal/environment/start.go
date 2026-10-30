package environment

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"context"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
	"reflect"
)

const (
	ReadyTimeout    = 30
	ReadyTimeoutMax = 600
	StopTimeout     = 10
	ProcessTimeout  = 5
	ServicePoll     = 250 * time.Millisecond
)

var (
	packageRunners = [][2]string{{"pnpm-lock.yaml", "pnpm run"}, {"yarn.lock", "yarn run"}, {"bun.lockb", "bun run"}, {"bun.lock", "bun run"}}
	composeSources = map[string]bool{"compose.yaml": true, "compose.yml": true, "docker-compose.yaml": true, "docker-compose.yml": true}
	serviceActive  = map[string]bool{"intended": true, "starting": true, "running": true, "ready": true, "unknown": true, "stopping": true}
)

const serviceNote = "A service row records intent, exact HC creation binding, process identity, and independent readiness. Native status is not exit or readiness."

type StartArgs struct {
	Task        string
	Command     string
	Source      string
	URL         string
	Match       string
	Log         string
	Label       string
	Timeout     int
	TimeoutSet  bool
	RuntimeRoot string
}

type StopArgs struct {
	Caller      *ordjson.Object
	Task        string
	Service     string
	Timeout     int
	TimeoutSet  bool
	RuntimeRoot string
}

func newServiceID() (string, error) {
	buf := make([]byte, 5)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "s-" + hex.EncodeToString(buf), nil
}

func composeProject(task *ordjson.Object) string {
	id := strings.ReplaceAll(stringField(task, "id"), "t-", "")
	if len(id) > 12 {
		id = id[:12]
	}
	return "sum-" + id
}

func launchLine(row, task *ordjson.Object, worktree string) (string, *ordjson.Object, error) {
	source := stringField(row, "source")
	name := stringField(row, "name")
	if intField(row, "redactions") > 0 {
		return "", nil, fmt.Errorf("Declared command %q from %s contains credential-shaped text that was redacted at discovery; sum never reconstructs it. Run it yourself and record the URL.", name, source)
	}
	launch := ordjson.NewObject()
	launch.Set("via", "hc-terminal")
	switch source {
	case "mise.toml", ".mise.toml", ".mise/config.toml":
		launch.Set("runner", "mise")
		return "mise run " + shquote.Quote(name), launch, nil
	case "package.json":
		runner := "npm run"
		for _, pair := range packageRunners {
			if _, err := os.Stat(filepath.Join(worktree, pair[0])); err == nil {
				runner = pair[1]
				break
			}
		}
		launch.Set("runner", strings.Split(runner, " ")[0])
		return runner + " " + shquote.Quote(name), launch, nil
	case "Makefile":
		launch.Set("runner", "make")
		return "make " + shquote.Quote(name), launch, nil
	case "justfile", "Justfile":
		launch.Set("runner", "just")
		return "just " + shquote.Quote(name), launch, nil
	case "Procfile":
		launch.Set("runner", "procfile")
		return stringField(row, "command"), launch, nil
	default:
		if composeSources[source] {
			project := composeProject(task)
			launch.Set("via", "compose")
			launch.Set("runner", "docker compose")
			launch.Set("project", project)
			launch.Set("service", name)
			return fmt.Sprintf("docker compose -f %s --project-name %s up %s", shquote.Quote(source), project, shquote.Quote(name)), launch, nil
		}
		if source == "pyproject.toml" && name == "pytest" {
			launch.Set("runner", "pytest")
			return "pytest", launch, nil
		}
		return "", nil, fmt.Errorf("Declared entry %q from %s is a reference, not a launchable command (kind %s); use the repository's own workflow for it.", name, source, stringField(row, "kind"))
	}
}

func serviceCommand(record *ordjson.Object, name, source string) (*ordjson.Object, error) {
	discovery := objectField(record, "discovery")
	var matches []*ordjson.Object
	var known []string
	seen := map[string]bool{}
	for _, raw := range listField(discovery, "commands") {
		c, _ := raw.(*ordjson.Object)
		cmdName := stringField(c, "name")
		if !seen[cmdName] {
			known = append(known, cmdName)
			seen[cmdName] = true
		}
		if cmdName == name && (source == "" || stringField(c, "source") == source) {
			matches = append(matches, c)
		}
	}
	if len(matches) == 0 {
		if len(known) > 40 {
			known = known[:40]
		}
		return nil, fmt.Errorf("No declared command %q in the environment record (known: %v); sum launches only commands the repository declares. Run `env discover` first.", name, known)
	}
	if len(matches) > 1 {
		sources := make([]string, 0, len(matches))
		for _, m := range matches {
			sources = append(sources, stringField(m, "source"))
		}
		return nil, fmt.Errorf("Command %q is declared in several files (%v); pass --source to choose one.", name, sources)
	}
	return matches[0], nil
}

func serviceByID(record *ordjson.Object, serviceID string) (*ordjson.Object, error) {
	for _, raw := range listField(record, "services") {
		s, _ := raw.(*ordjson.Object)
		if stringField(s, "id") == serviceID {
			return s, nil
		}
	}
	return nil, fmt.Errorf("No service %q recorded for this task.", serviceID)
}

func updateService(s *store.Store, taskID, serviceID, event string, changes *ordjson.Object) (*ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	record, err := Read(s, taskID)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("Environment record disappeared during the launch; nothing further is done.")
	}
	row, err := serviceByID(record, serviceID)
	if err != nil {
		return nil, err
	}
	previous := stringField(row, "state")
	if changes != nil {
		for _, k := range changes.Keys() {
			v, _ := changes.Get(k)
			row.Set(k, v)
		}
	}
	row.Set("updated_at", store.Now())
	history := listField(row, "history")
	if len(history) > 19 {
		history = history[len(history)-19:]
	}
	item := ordjson.NewObject()
	item.Set("at", stringField(row, "updated_at"))
	item.Set("event", event)
	item.Set("from", previous)
	item.Set("to", func() any { v, _ := row.Get("state"); return v }())
	row.Set("history", append(history, item))
	writeEvent := ordjson.NewObject()
	writeEvent.Set("event", event)
	writeEvent.Set("service", serviceID)
	writeEvent.Set("state", func() any { v, _ := row.Get("state"); return v }())
	if err := Write(s, record, writeEvent); err != nil {
		return nil, err
	}
	return row, nil
}

func requireServiceCaller(s *store.Store, task, caller *ordjson.Object) error {
	role := endpointRole(s, task, caller)
	if role == "coordinator" {
		return app.RequireCoordinator(s, caller)
	}
	if role != "worker" && role != "reviewer" {
		return fmt.Errorf("service effect requires current task worker, reviewer, or coordinator")
	}
	endpoint := store.EndpointFromContext(caller)
	reg, err := s.Registration(endpoint)
	if err != nil {
		return err
	}
	_, terminal, err := app.ObserveNative(endpoint)
	if err != nil {
		return err
	}
	receipt, err := app.NativeReceipt(reg)
	if err != nil {
		return err
	}
	v, _ := reg.Get("incarnation")
	verdict := incarnation.JudgeNative(v, endpoint.Native(), terminal, receipt)
	if !verdict.Verified {
		return fmt.Errorf("service caller process is not receipt-current: %s", verdict.Reason)
	}
	return nil
}
func serviceEndpoint(service *ordjson.Object) store.Endpoint {
	return store.EndpointFromContext(objectField(service, "endpoint"))
}
func nativeCallContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}
func terminalFinished(t hcclient.Terminal) bool {
	return t.State == "finished" && t.FinishedMS != nil && (t.ExitCode != nil || t.Signal != nil)
}
func nativeProcess(t hcclient.Terminal) *ordjson.Object {
	p := ordjson.NewObject()
	if t.PID != nil {
		p.Set("pid", jsonInt(int(*t.PID)))
	}
	if t.Foreground != nil {
		p.Set("foreground_pid", jsonInt(int(t.Foreground.PID)))
		p.Set("name", t.Foreground.Name)
		p.Set("foreground_argv", toAny(t.Foreground.Argv))
		p.Set("foreground_cwd", t.Foreground.Cwd)
	}
	p.Set("argv", toAny(t.Argv))
	p.Set("cwd", t.Cwd)
	p.Set("observed_at", store.Now())
	return p
}
func toAny(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}
func processExited(process *ordjson.Object) error {
	if intField(process, "pid") <= 0 {
		return fmt.Errorf("no saved native process identity")
	}
	for _, key := range []string{"pid", "foreground_pid"} {
		pid := intField(process, key)
		if pid <= 0 {
			continue
		}
		alive, err := proc.PIDRunning(pid)
		if err != nil {
			return err
		}
		if alive {
			return fmt.Errorf("recorded %s %d still runs", key, pid)
		}
		children, err := proc.Descendants(pid)
		if err != nil {
			return err
		}
		if len(children) > 0 {
			return fmt.Errorf("recorded process descendants survive: %v", children)
		}
	}
	group := intField(process, "process_group")
	if group <= 0 {
		return fmt.Errorf("saved service process group is unproven")
	}
	if err := syscall.Kill(-group, 0); err == nil || errors.Is(err, syscall.EPERM) {
		return fmt.Errorf("service process group %d still has a surviving process", group)
	} else if !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("service process group cannot be observed: %w", err)
	}
	for _, raw := range listField(process, "descendants") {
		n, ok := raw.(json.Number)
		if !ok {
			return fmt.Errorf("malformed saved descendant identity")
		}
		pid, _ := n.Int64()
		alive, err := proc.PIDRunning(int(pid))
		if err != nil {
			return err
		}
		if alive {
			return fmt.Errorf("saved descendant %d survives", pid)
		}
	}
	return nil
}
func ObserveService(task, service *ordjson.Object, runtimeRoot string) (*ordjson.Object, error) {
	return observeService(task, service, runtimeRoot)
}
func observeService(task, service *ordjson.Object, runtimeRoot string) (*ordjson.Object, error) {
	view := ordjson.NewObject()
	view.Set("id", stringField(service, "id"))
	view.Set("endpoint", objectField(service, "endpoint"))
	view.Set("terminal_state", "uncertain")
	view.Set("running", nil)
	view.Set("ownership", "unknown")
	view.Set("reasons", []any{})
	refuse := func(reason string) (*ordjson.Object, error) { view.Set("reasons", []any{reason}); return view, nil }
	if ownership := stringField(service, "ownership"); ownership == "shared" || ownership == "foreign" {
		return refuse("shared or foreign service has no task stop authority")
	}
	endpoint := serviceEndpoint(service)
	if err := endpoint.Validate(); err != nil {
		return refuse("no complete native service endpoint; interrupted intent or historical record requires inspection")
	}
	if endpoint.Task != stringField(task, "id") || endpoint.Role != "service" || endpoint.Attempt != stringField(service, "id") {
		return refuse("service endpoint does not match task and launch intent")
	}
	process := objectField(service, "process")
	ctx, cancel := nativeCallContext()
	defer cancel()
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID}
	terminal, err := client.Get(ctx, endpoint.Address)
	if err != nil {
		var rpc *hcclient.RPCError
		var diagnostic struct {
			Kind string `json:"kind"`
		}
		verified, _ := service.Get("exit_verified")
		if errors.As(err, &rpc) && json.Unmarshal(rpc.Data, &diagnostic) == nil && diagnostic.Kind == "not_found" && verified == true && processExited(process) == nil {
			view.Set("terminal_state", "absent")
			view.Set("running", false)
			view.Set("ownership", "owned")
			return view, nil
		}
		return refuse("native terminal observation failed: " + err.Error())
	}
	view.Set("terminal_state", terminal.State)
	if terminal.Address != endpoint.Address || terminal.CreatedMS != endpoint.CreatedMS || terminal.Keep || !samePathNative(terminal.Cwd, stringField(task, "worktree")) {
		return refuse("native terminal creation, lifetime, or checkout binding changed")
	}
	if process == nil || intField(process, "pid") <= 0 {
		return refuse("native process identity was not saved")
	}
	if terminal.State == "live" {
		view.Set("running", true)
		if terminal.PID == nil || int(*terminal.PID) != intField(process, "pid") || !reflect.DeepEqual(listField(process, "argv"), toAny(terminal.Argv)) {
			return refuse("native program process differs from saved launch")
		}
		if terminal.Foreground == nil || int(terminal.Foreground.PID) != intField(process, "foreground_pid") || !reflect.DeepEqual(listField(process, "foreground_argv"), toAny(terminal.Foreground.Argv)) {
			return refuse("native foreground differs from saved launch")
		}
	} else {
		if !terminalFinished(terminal) {
			return refuse("native terminal has no finished exit evidence")
		}
		if err := processExited(process); err != nil {
			return refuse(err.Error())
		}
		view.Set("running", false)
	}
	view.Set("ownership", "owned")
	return view, nil
}
func samePathNative(a, b string) bool {
	a1, e1 := filepath.Abs(a)
	b1, e2 := filepath.Abs(b)
	return a != "" && b != "" && e1 == nil && e2 == nil && a1 == b1
}
func activeOrFailed(state string) bool { return serviceActive[state] || state == "failed" }

func Start(s *store.Store, args StartArgs, caller *ordjson.Object) (*ordjson.Object, error) {
	if err := s.CheckWritable(); err != nil {
		return nil, err
	}
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	if err = s.CheckMachine(task); err != nil {
		return nil, err
	}
	if err = requireServiceCaller(s, task, caller); err != nil {
		return nil, err
	}
	worker, err := reservations.Worker(task)
	if err != nil {
		return nil, err
	}
	if stringField(worker, "state") == "released" {
		return nil, fmt.Errorf("worker reservation is released")
	}
	worktree, err := requireWorktree(task)
	if err != nil {
		return nil, err
	}
	parent := store.EndpointFromContext(caller)
	if err = parent.Validate(); err != nil {
		return nil, fmt.Errorf("service launch requires actual native caller: %w", err)
	}
	workerEndpoint := store.EndpointFromContext(objectField(task, "worker_endpoint"))
	if err = workerEndpoint.Validate(); err != nil {
		return nil, err
	}
	if parent.Machine != workerEndpoint.Machine || parent.CoreID != workerEndpoint.CoreID || parent.Socket != workerEndpoint.Socket {
		return nil, fmt.Errorf("service caller must use task's local native core")
	}
	record, err := Read(s, args.Task)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("run env discover first")
	}
	row, err := serviceCommand(record, args.Command, args.Source)
	if err != nil {
		return nil, err
	}
	command, launch, err := launchLine(row, task, worktree)
	if err != nil {
		return nil, err
	}
	timeout := ReadyTimeout
	if args.TimeoutSet {
		timeout = args.Timeout
	}
	if timeout < 1 || timeout > ReadyTimeoutMax {
		return nil, fmt.Errorf("--timeout must be between 1 and %d seconds", ReadyTimeoutMax)
	}
	if len(args.Match) > 200 {
		return nil, fmt.Errorf("--match must be short readiness text")
	}
	if _, n := Redact(args.Match + args.Label); n > 0 {
		return nil, fmt.Errorf("readiness or label contains credential-shaped text")
	}
	var parsed *ordjson.Object
	if args.URL != "" {
		parsed, err = ParseEndpointURL(args.URL)
		if err != nil {
			return nil, err
		}
		conflicts, err := endpointConflicts(s, task, parsed)
		if err != nil {
			return nil, err
		}
		if len(conflicts) > 0 {
			return nil, fmt.Errorf("URL is owned by another active task")
		}
		if local, _ := parsed.Get("local"); local == true {
			obs, err := ObservePort(s, task, intField(parsed, "port"), nil)
			if err != nil {
				return nil, err
			}
			if stringField(obs, "state") == "observed" {
				return nil, fmt.Errorf("port %d is occupied; no process launched or stopped", intField(parsed, "port"))
			}
		}
	}
	if args.Log != "" {
		if _, err = validateLogPath(task, args.Log); err != nil {
			return nil, err
		}
	}
	id, err := newServiceID()
	if err != nil {
		return nil, err
	}
	instance, err := s.Instance()
	if err != nil {
		return nil, err
	}
	argv := serviceArgv(row, launch, command)
	params := hcclient.RunParams{Argv: argv, Cwd: worktree, Env: map[string]string{"SUM_HOME": s.Root, "SUM_TASK": args.Task, "SUM_ROLE": "service", "SUM_ATTEMPT": id}, Labels: map[string]string{"sum.installation": instance, "sum.task": args.Task, "sum.role": "service", "sum.attempt": id}, Parent: parent.Address, Caller: parent.Address, OutliveParent: true, Keep: false, RM: false, NoNotify: true}
	launch.Set("intent", nativeObject(params))
	service := ordjson.NewObject()
	for _, key := range []string{"name", "source", "kind"} {
		v, _ := row.Get(key)
		service.Set(key, v)
	}
	service.Set("id", id)
	service.Set("command", command)
	service.Set("launch", launch)
	service.Set("state", "intended")
	service.Set("intent_at", store.Now())
	service.Set("by", parent.Role)
	service.Set("ownership", "owned")
	service.Set("label", args.Label)
	service.Set("history", []any{})
	service.Set("match", args.Match)
	if parsed != nil {
		service.Set("url", stringField(parsed, "url"))
		v, _ := parsed.Get("port")
		service.Set("port", v)
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	currentTask, err := s.ReadTask(args.Task)
	if err == nil {
		err = repair.RefuseDuringCleanup(currentTask, "Service start")
	}
	if err != nil {
		unlock()
		return nil, err
	}
	currentWorker, err := reservations.Worker(currentTask)
	if err != nil || stringField(currentWorker, "id") != stringField(worker, "id") || stringField(currentWorker, "state") == "released" {
		unlock()
		return nil, fmt.Errorf("worker reservation changed before launch")
	}
	current, err := Ensure(s, currentTask)
	if err != nil {
		unlock()
		return nil, err
	}
	active := 0
	for _, raw := range listField(current, "services") {
		previous, _ := raw.(*ordjson.Object)
		if activeOrFailed(stringField(previous, "state")) {
			active++
			if stringField(previous, "name") == args.Command && stringField(previous, "source") == stringField(row, "source") {
				unlock()
				view, e := observeService(task, previous, args.RuntimeRoot)
				if e != nil {
					return nil, e
				}
				if stringField(view, "ownership") == "owned" {
					running, _ := view.Get("running")
					if running == true {
						out := ordjson.NewObject()
						out.Set("task", args.Task)
						out.Set("service", previous)
						out.Set("already_running", true)
						return out, nil
					}
				}
				return nil, fmt.Errorf("previous service intent %s is unresolved; inspect and stop before another launch", stringField(previous, "id"))
			}
		}
	}
	if active >= serviceLimit {
		unlock()
		return nil, fmt.Errorf("at most %d active services per task", serviceLimit)
	}
	current.Set("services", append(listField(current, "services"), service))
	event := ordjson.NewObject()
	event.Set("event", "start-intent")
	event.Set("service", id)
	err = Write(s, current, event)
	unlock()
	if err != nil {
		return nil, err
	}
	if err = s.CheckWritable(); err != nil {
		return nil, err
	}
	if err = requireServiceCaller(s, currentTask, caller); err != nil {
		return nil, err
	}
	client := hcclient.Client{Socket: parent.Socket, CoreID: parent.CoreID}
	ctx, cancel := nativeCallContext()
	defer cancel()
	launched, err := client.Run(ctx, params)
	changes := ordjson.NewObject()
	changes.Set("state", "unknown")
	if err != nil {
		changes.Set("error", err.Error())
		_, saveErr := updateService(s, args.Task, id, "launch-uncertain", changes)
		if saveErr != nil {
			return nil, saveErr
		}
		return nil, fmt.Errorf("service intent %s retained; launch outcome must be reconciled without retry: %w", id, err)
	}
	endpoint := parent
	endpoint.Task = args.Task
	endpoint.Role = "service"
	endpoint.Attempt = id
	endpoint.Address = launched.Address
	endpoint.CreatedMS = launched.CreatedMS
	endpoint.Cwd = worktree
	endpoint.BindingGeneration = 1
	changes.Set("endpoint", endpoint.Object())
	changes.Set("launched_at", store.Now())
	changes.Set("first_message", nativeObject(launched.FirstMessage))
	changes.Set("process", nativeProcess(launched.Terminal))
	changes.Set("launch_outcome", nativeObject(launched.Terminal))
	if err = endpoint.Validate(); err != nil {
		changes.Set("error", err.Error())
		_, _ = updateService(s, args.Task, id, "launch-invalid", changes)
		return nil, err
	}
	service, err = updateService(s, args.Task, id, "native-created", changes)
	if err != nil {
		return nil, err
	}
	captured, captureErr := captureServiceForeground(client, launched.Terminal)
	if captureErr != nil {
		changes = ordjson.NewObject()
		changes.Set("error", captureErr.Error())
		_, saveErr := updateService(s, args.Task, id, "foreground-unproven", changes)
		if saveErr != nil {
			return nil, saveErr
		}
		return nil, captureErr
	}
	changes = ordjson.NewObject()
	changes.Set("process", nativeProcess(captured))
	changes.Set("state", "unknown")
	process := objectField(changes, "process")
	if captured.PID != nil && captured.State == "live" {
		group, groupErr := serviceProcessGroup(int(*captured.PID))
		if groupErr != nil {
			changes.Set("error", groupErr.Error())
			_, saveErr := updateService(s, args.Task, id, "process-group-unproven", changes)
			if saveErr != nil {
				return nil, saveErr
			}
			return nil, groupErr
		}
		process.Set("process_group", jsonInt(group))
	}
	var descendants []any
	if pid := intField(process, "pid"); pid > 0 {
		children, e := proc.Descendants(pid)
		if e != nil {
			return nil, e
		}
		for _, p := range children {
			descendants = append(descendants, jsonInt(p))
		}
	}
	process.Set("descendants", descendants)
	if captured.State == "live" && captured.PID != nil && captured.Foreground != nil {
		changes.Set("state", "running")
	}
	service, err = updateService(s, args.Task, id, "launched", changes)
	if err != nil {
		return nil, err
	}
	readiness, err := waitNativeReady(s, task, service, parsed, args.Match, timeout)
	if err != nil {
		return nil, err
	}
	changes = ordjson.NewObject()
	changes.Set("readiness", readiness)
	ready, _ := readiness.Get("ready")
	if ready == true {
		changes.Set("state", "ready")
	} else if ready == false {
		changes.Set("state", "failed")
	}
	service, err = updateService(s, args.Task, id, "readiness", changes)
	if err != nil {
		return nil, err
	}
	out := ordjson.NewObject()
	out.Set("task", args.Task)
	out.Set("service", service)
	out.Set("already_running", false)
	out.Set("note", serviceNote)
	if args.Log != "" {
		recorded, err := Record(s, RecordArgs{Task: args.Task, Log: args.Log, Label: args.Label}, caller)
		if err != nil {
			return nil, err
		}
		out.Set("log", recorded)
	}
	if ready == true && parsed != nil {
		unlock, err := s.Lock()
		if err != nil {
			return nil, err
		}
		current, err := Ensure(s, task)
		if err == nil {
			conflicts, e := endpointConflicts(s, task, parsed)
			if e != nil {
				err = e
			} else {
				entry, e := buildEndpoint(current, parsed, objectField(readiness, "observation"), "", args.Label, parent.Role, store.Now(), conflicts)
				if e != nil {
					err = e
				} else {
					entry.Set("service", id)
					err = upsertEndpoint(current, entry)
					if err == nil {
						event := ordjson.NewObject()
						event.Set("event", "service-endpoint")
						event.Set("service", id)
						err = Write(s, current, event)
						out.Set("endpoint", entry)
					}
				}
			}
		}
		unlock()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// HC returns creation before its foreground snapshot is necessarily available.
// Follow-up observations retain the exact returned creation, program and argv;
// they never adopt another terminal or synthesize status readiness.
func serviceProcessGroup(pid int) (int, error) {
	result, err := proc.Run([]string{"ps", "-p", strconv.Itoa(pid), "-o", "pgid="}, "", 3*time.Second, true, nil)
	if err != nil {
		return 0, err
	}
	group, err := strconv.Atoi(strings.TrimSpace(result.Stdout))
	if err != nil || group <= 0 || group == syscall.Getpgrp() {
		return 0, fmt.Errorf("service process group is absent or shares this helper's group")
	}
	return group, nil
}
func captureServiceForeground(client hcclient.Client, created hcclient.Terminal) (hcclient.Terminal, error) {
	if created.PID == nil || *created.PID == 0 || created.Foreground != nil || created.State != "live" {
		return created, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), ProcessTimeout*time.Second)
	defer cancel()
	for {
		observed, err := client.Get(ctx, created.Address)
		if err != nil {
			return created, err
		}
		if observed.Address != created.Address || observed.CreatedMS != created.CreatedMS || observed.Keep || observed.PID == nil || *observed.PID != *created.PID || observed.Cwd != created.Cwd || !reflect.DeepEqual(observed.Argv, created.Argv) {
			return created, fmt.Errorf("service program binding changed before foreground capture")
		}
		if observed.Foreground != nil || terminalFinished(observed) {
			return observed, nil
		}
		select {
		case <-ctx.Done():
			return created, fmt.Errorf("service foreground remained unproven within bounded capture: %w", ctx.Err())
		case <-time.After(ServicePoll):
		}
	}
}
func serviceArgv(row, launch *ordjson.Object, command string) []string {
	name := stringField(row, "name")
	runner := stringField(launch, "runner")
	switch runner {
	case "mise":
		return []string{"mise", "run", name}
	case "npm", "pnpm", "yarn", "bun":
		return []string{runner, "run", name}
	case "make", "just":
		return []string{runner, name}
	case "pytest":
		return []string{"pytest"}
	case "docker compose":
		return []string{"docker", "compose", "-f", stringField(row, "source"), "--project-name", stringField(launch, "project"), "up", name}
	}
	return []string{"/bin/sh", "-c", command}
}
func nativeObject(v any) any { b, _ := json.Marshal(v); o, _ := ordjson.Decode(b); return o }
func waitNativeReady(s *store.Store, task, service, parsed *ordjson.Object, match string, timeout int) (*ordjson.Object, error) {
	out := ordjson.NewObject()
	out.Set("ready", nil)
	out.Set("checked", "process-only")
	if parsed == nil && match == "" {
		return out, nil
	}
	endpoint := serviceEndpoint(service)
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID}
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	for {
		view, err := observeService(task, service, "")
		if err != nil {
			return nil, err
		}
		running, _ := view.Get("running")
		if stringField(view, "ownership") != "owned" || running != true {
			out.Set("ready", false)
			out.Set("reason", "native process identity changed or exited during readiness")
			return out, nil
		}
		if parsed != nil {
			local, _ := parsed.Get("local")
			if local == true {
				obs, err := ObservePort(s, task, intField(parsed, "port"), nil)
				if err != nil {
					return nil, err
				}
				out.Set("checked", "listener")
				out.Set("observation", obs)
				if stringField(obs, "state") == "observed" {
					owned := map[int]bool{intField(objectField(service, "process"), "pid"): true}
					children, err := proc.Descendants(intField(objectField(service, "process"), "pid"))
					if err != nil {
						return nil, err
					}
					for _, p := range children {
						owned[p] = true
					}
					for _, raw := range listField(obs, "listeners") {
						listener, _ := raw.(*ordjson.Object)
						if !owned[intField(listener, "pid")] {
							out.Set("ready", false)
							out.Set("reason", "listener is not owned by the launched process tree")
							return out, nil
						}
					}
					out.Set("ready", true)
					return out, nil
				}
			}
		}
		if match != "" {
			ctx, cancel := nativeCallContext()
			read, err := client.Read(ctx, endpoint.Address, 200)
			cancel()
			if err != nil {
				return nil, err
			}
			out.Set("checked", "output")
			if strings.Contains(read.Text, match) {
				out.Set("ready", true)
				return out, nil
			}
		}
		if time.Now().After(deadline) {
			out.Set("ready", false)
			out.Set("reason", "readiness deadline elapsed")
			return out, nil
		}
		time.Sleep(ServicePoll)
	}
}
func Stop(s *store.Store, args StopArgs) (*ordjson.Object, error) {
	if err := s.CheckWritable(); err != nil {
		return nil, err
	}
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	if err = s.CheckMachine(task); err != nil {
		return nil, err
	}
	caller := args.Caller
	if caller == nil {
		caller = app.OptionalContext(args.RuntimeRoot)
	}
	if err = requireServiceCaller(s, task, caller); err != nil {
		return nil, err
	}
	timeout := StopTimeout
	if args.TimeoutSet {
		timeout = args.Timeout
	}
	if timeout < 1 || timeout > ReadyTimeoutMax {
		return nil, fmt.Errorf("invalid stop timeout")
	}
	var ids []string
	if args.Service != "" {
		ids = []string{args.Service}
	}
	rows, err := stopServices(s, task, ids, timeout, args.RuntimeRoot)
	if err != nil {
		return nil, err
	}
	out := ordjson.NewObject()
	out.Set("task", args.Task)
	out.Set("services", rows)
	return out, nil
}
func stopServices(s *store.Store, task *ordjson.Object, ids []string, timeout int, runtimeRoot string) ([]any, error) {
	record, err := Read(s, stringField(task, "id"))
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	var rows []any
	for _, raw := range listField(record, "services") {
		service, _ := raw.(*ordjson.Object)
		id := stringField(service, "id")
		if len(ids) > 0 && !wanted[id] {
			continue
		}
		if len(ids) == 0 && !activeOrFailed(stringField(service, "state")) {
			continue
		}
		delete(wanted, id)
		row, err := stopService(s, task, service, timeout, runtimeRoot)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if len(wanted) > 0 {
		return nil, fmt.Errorf("unknown service IDs: %v", wanted)
	}
	return rows, nil
}
func stopService(s *store.Store, task, service *ordjson.Object, timeout int, runtimeRoot string) (*ordjson.Object, error) {
	if err := s.CheckWritable(); err != nil {
		return nil, err
	}
	view, err := observeService(task, service, runtimeRoot)
	if err != nil {
		return nil, err
	}
	out := ordjson.NewObject()
	out.Set("id", stringField(service, "id"))
	out.Set("before", view)
	out.Set("closed_terminal", false)
	if stringField(view, "ownership") != "owned" {
		out.Set("action", "refused")
		out.Set("state", "unknown")
		out.Set("reasons", listField(view, "reasons"))
		return out, nil
	}
	endpoint := serviceEndpoint(service)
	client := hcclient.Client{Socket: endpoint.Socket, CoreID: endpoint.CoreID}
	running, _ := view.Get("running")
	action := "none"
	if running == true {
		// Persist the single interrupt claim before input; retries observe without replay.
		claimed, current, err := claimServiceInterrupt(s, task, service)
		if err != nil {
			return nil, err
		}
		service = current
		if claimed {
			if err = s.CheckWritable(); err != nil {
				return nil, err
			}
			ctx, cancel := nativeCallContext()
			_, err = client.Interrupt(ctx, endpoint.Address)
			cancel()
			if err != nil {
				return nil, err
			}
		}

		action = "interrupt"
		deadline := time.Now().Add(time.Duration(timeout) * time.Second)
		for {
			view, err = observeService(task, service, runtimeRoot)
			if err != nil {
				return nil, err
			}
			running, _ = view.Get("running")
			if stringField(view, "ownership") == "owned" && running == false {
				break
			}
			if time.Now().After(deadline) {
				out.Set("action", action)
				out.Set("state", "stopping")
				out.Set("reason", "exit or surviving-process proof remains unresolved; no escalation or repeat interrupt")
				return out, nil
			}
			time.Sleep(ServicePoll)
		}
	}
	if port := intField(service, "port"); port != 0 {
		obs, err := ObservePort(s, task, port, nil)
		if err != nil {
			return nil, err
		}
		if stringField(obs, "state") == "observed" {
			out.Set("state", "unknown")
			out.Set("action", action)
			out.Set("reason", "listener survives terminal exit")
			return out, nil
		}
	}
	changes := ordjson.NewObject()
	changes.Set("state", "stopped")
	changes.Set("stopped_at", store.Now())
	changes.Set("exit_verified", true)
	if _, err = updateService(s, stringField(task, "id"), stringField(service, "id"), "stopped", changes); err != nil {
		return nil, err
	}
	out.Set("state", "stopped")
	out.Set("action", action)
	out.Set("exit_verified", true)
	return out, nil
}

func claimServiceInterrupt(s *store.Store, task, service *ordjson.Object) (bool, *ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return false, nil, err
	}
	defer unlock()
	record, err := Read(s, stringField(task, "id"))
	if err != nil {
		return false, nil, err
	}
	current, err := serviceByID(record, stringField(service, "id"))
	if err != nil {
		return false, nil, err
	}
	if !serviceEndpoint(current).Native().SameAuthority(serviceEndpoint(service).Native()) {
		return false, nil, fmt.Errorf("service binding changed before interrupt")
	}
	if stringField(objectField(current, "stop"), "action") == "interrupt" {
		return false, current, nil
	}
	process := objectField(current, "process")
	children, err := proc.Descendants(intField(process, "pid"))
	if err != nil {
		return false, nil, err
	}
	seen := map[int]bool{}
	for _, raw := range listField(process, "descendants") {
		if n, ok := raw.(json.Number); ok {
			pid, _ := n.Int64()
			seen[int(pid)] = true
		}
	}
	for _, pid := range children {
		seen[pid] = true
	}
	var saved []any
	for pid := range seen {
		saved = append(saved, jsonInt(pid))
	}
	process.Set("descendants", saved)
	stop := ordjson.NewObject()
	stop.Set("action", "interrupt")
	stop.Set("at", store.Now())
	current.Set("stop", stop)
	current.Set("state", "stopping")
	event := ordjson.NewObject()
	event.Set("event", "stop-interrupt")
	event.Set("service", stringField(current, "id"))
	if err = Write(s, record, event); err != nil {
		return false, nil, err
	}
	return true, current, nil
}
