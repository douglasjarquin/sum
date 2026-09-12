package environment

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

const (
	ReadyTimeout    = 30
	ReadyTimeoutMax = 600
	StopTimeout     = 10
	ProcessTimeout  = 5
	ServicePoll     = 250 * time.Millisecond
	InterruptKey    = "ctrl+c"
)

var (
	packageRunners = [][2]string{{"pnpm-lock.yaml", "pnpm run"}, {"yarn.lock", "yarn run"}, {"bun.lockb", "bun run"}, {"bun.lock", "bun run"}}
	composeSources = map[string]bool{"compose.yaml": true, "compose.yml": true, "docker-compose.yaml": true, "docker-compose.yml": true}
	shells         = map[string]bool{"bash": true, "zsh": true, "sh": true, "fish": true, "dash": true, "ksh": true, "tcsh": true, "csh": true, "nu": true, "pwsh": true, "-bash": true, "-zsh": true, "-sh": true, "-fish": true}
	serviceActive  = map[string]bool{"intended": true, "starting": true, "running": true, "ready": true, "unknown": true, "stopping": true}
)

const serviceNote = "A service row is sum's launch record: intent first, then the pane and the observed process instance. Ownership is proven by pane, " +
	"workspace, shell pid, process pid, and argv together; stop authority follows only that proof."

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
	launch.Set("via", "herdr-pane")
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

func herdrPath(runtimeRoot string) (string, error) {
	return toolpath.Find(runtimeRoot, "herdr")
}

func paneProcesses(runtimeRoot, session, paneID string) (*ordjson.Object, string, error) {
	path, err := herdrPath(runtimeRoot)
	if err != nil {
		return nil, "", err
	}
	info, code, obsErr := herdrclient.Observe(path, session, 5*time.Second, "pane", "process-info", "--pane", paneID)
	if obsErr != nil {
		return nil, "", obsErr
	}
	if info == nil {
		return nil, code, nil
	}
	obj, _ := info.(*ordjson.Object)
	if inner, ok := obj.Get("process_info"); ok {
		if innerObj, isObj := inner.(*ordjson.Object); isObj {
			obj = innerObj
		}
	}
	shell := obj.Get
	shellPID, _ := shell("shell_pid")
	var rows []any
	for _, raw := range listField(obj, "foreground_processes") {
		p, _ := raw.(*ordjson.Object)
		pid, _ := p.Get("pid")
		argv0 := stringField(p, "argv0")
		if argv0 == "" {
			argv0 = stringField(p, "name")
		}
		if pid == shellPID && shells[argv0] {
			continue
		}
		row := ordjson.NewObject()
		for _, k := range []string{"pid", "name", "argv", "cwd"} {
			v, _ := p.Get(k)
			row.Set(k, v)
		}
		rows = append(rows, row)
	}
	result := ordjson.NewObject()
	result.Set("shell_pid", shellPID)
	result.Set("processes", rows)
	return result, "", nil
}

func normalizedArgv(argv any) []string {
	list, ok := argv.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	out := make([]string, 0, len(list))
	for i, item := range list {
		s := fmt.Sprint(item)
		if i == 0 {
			s = filepath.Base(s)
		}
		out = append(out, s)
	}
	return out
}

func sameInstance(recorded, process *ordjson.Object) bool {
	if recorded == nil || process == nil {
		return false
	}
	mine := normalizedArgv(func() any { v, _ := recorded.Get("argv"); return v }())
	seen := normalizedArgv(func() any { v, _ := process.Get("argv"); return v }())
	if mine == nil || seen == nil {
		return false
	}
	if intField(process, "pid") != intField(recorded, "pid") {
		return false
	}
	if len(mine) != len(seen) {
		return false
	}
	for i := range mine {
		if mine[i] != seen[i] {
			return false
		}
	}
	return true
}

func splitCommand(command string) []any {
	var parts []any
	var current strings.Builder
	inSingle, inDouble := false, false
	for i := 0; i < len(command); i++ {
		ch := command[i]
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case ch == ' ' && !inSingle && !inDouble:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

func launchedProcess(info *ordjson.Object, command string) *ordjson.Object {
	wanted := normalizedArgv(splitCommand(command))
	if wanted == nil || info == nil {
		return nil
	}
	for _, raw := range listField(info, "processes") {
		p, _ := raw.(*ordjson.Object)
		got := normalizedArgv(func() any { v, _ := p.Get("argv"); return v }())
		if len(got) != len(wanted) {
			continue
		}
		match := true
		for i := range wanted {
			if got[i] != wanted[i] {
				match = false
				break
			}
		}
		if match {
			return p
		}
	}
	return nil
}

func processIdentity(process *ordjson.Object, paneID string, shellPID any) *ordjson.Object {
	row := ordjson.NewObject()
	row.Set("pane", paneID)
	row.Set("shell_pid", shellPID)
	for _, k := range []string{"pid", "name", "argv", "cwd"} {
		v, _ := process.Get(k)
		row.Set(k, v)
	}
	row.Set("observed_at", store.Now())
	return row
}

func ObserveService(task, service *ordjson.Object, runtimeRoot string) (*ordjson.Object, error) {
	return observeService(task, service, runtimeRoot)
}

func observeService(task, service *ordjson.Object, runtimeRoot string) (*ordjson.Object, error) {
	view := ordjson.NewObject()
	view.Set("id", stringField(service, "id"))
	view.Set("pane", func() any { v, _ := service.Get("pane"); return v }())
	view.Set("pane_state", nil)
	view.Set("running", nil)
	view.Set("ownership", "unknown")
	view.Set("reasons", []any{})
	view.Set("processes", []any{})
	paneID := stringField(service, "pane")
	session := stringField(task, "session")
	if paneID == "" || session == "" {
		view.Set("pane_state", "none")
		view.Set("running", nil)
		view.Set("reasons", []any{"no pane was recorded for this launch; the intent was interrupted before a pane existed"})
		return view, nil
	}
	path, err := herdrPath(runtimeRoot)
	if err != nil {
		return nil, err
	}
	pane, code, obsErr := herdrclient.Observe(path, session, 5*time.Second, "pane", "get", paneID)
	if obsErr != nil {
		return nil, obsErr
	}
	if pane == nil {
		if code == "pane_not_found" {
			view.Set("pane_state", "absent")
			view.Set("running", false)
		} else {
			view.Set("pane_state", "uncertain")
			view.Set("running", nil)
			view.Set("reasons", []any{fmt.Sprintf("pane %s cannot be observed (%s)", paneID, code)})
		}
		return view, nil
	}
	paneObj, _ := pane.(*ordjson.Object)
	if inner, ok := paneObj.Get("pane"); ok {
		if innerObj, isObj := inner.(*ordjson.Object); isObj {
			paneObj = innerObj
		}
	}
	view.Set("pane_state", "present")
	var reasons []any
	workspaceID, _ := paneObj.Get("workspace_id")
	taskWorkspace, _ := task.Get("workspace")
	if workspaceID != nil && workspaceID != taskWorkspace {
		reasons = append(reasons, fmt.Sprintf("pane %s sits in workspace %v, not the task workspace %v", paneID, workspaceID, taskWorkspace))
	}
	cwd := stringField(paneObj, "cwd")
	if cwd == "" {
		cwd = stringField(paneObj, "working_directory")
	}
	if cwd == "" || !inside(cwd, stringField(task, "worktree")) {
		reasons = append(reasons, fmt.Sprintf("pane %s runs in %q, not inside the task checkout", paneID, cwd))
	}
	info, procCode, procErr := paneProcesses(runtimeRoot, session, paneID)
	if procErr != nil {
		return nil, procErr
	}
	if info == nil {
		reasons = append(reasons, fmt.Sprintf("process observation for pane %s is uncertain (%s)", paneID, procCode))
		view.Set("reasons", reasons)
		return view, nil
	}
	recorded := objectField(service, "process")
	view.Set("processes", listField(info, "processes"))
	if recorded != nil {
		if recShell, has := recorded.Get("shell_pid"); has && recShell != nil && recShell != func() any { v, _ := info.Get("shell_pid"); return v }() {
			reasons = append(reasons, fmt.Sprintf("pane shell pid changed from %v to %v; the pane was reused or restarted", recShell, func() any { v, _ := info.Get("shell_pid"); return v }()))
		}
	}
	processes := listField(info, "processes")
	if len(processes) == 0 {
		view.Set("running", false)
	} else {
		view.Set("running", true)
		if recorded == nil || func() any { v, _ := recorded.Get("pid"); return v }() == nil {
			reasons = append(reasons, "a foreground process runs but no process instance was recorded for this launch")
		} else {
			matched := false
			for _, raw := range processes {
				p, _ := raw.(*ordjson.Object)
				if sameInstance(recorded, p) {
					matched = true
					break
				}
			}
			if !matched {
				pairs := make([]any, 0, len(processes))
				for _, raw := range processes {
					p, _ := raw.(*ordjson.Object)
					pairs = append(pairs, []any{func() any { v, _ := p.Get("pid"); return v }(), stringField(p, "name")})
				}
				reasons = append(reasons, fmt.Sprintf("foreground process(es) %v differ from the recorded instance pid %v %q; restarted or replaced outside sum", pairs, func() any { v, _ := recorded.Get("pid"); return v }(), stringField(recorded, "name")))
			}
		}
	}
	view.Set("reasons", reasons)
	running, _ := view.Get("running")
	if len(reasons) == 0 && running == true {
		view.Set("ownership", "owned")
	} else if len(reasons) == 0 && running == false {
		view.Set("ownership", "owned")
	}
	return view, nil
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

func workspaceOf(paneID string) any {
	if paneID == "" {
		return nil
	}
	parts := strings.SplitN(paneID, ":", 2)
	return parts[0]
}

func unrecordedPanes(task, record *ordjson.Object, runtimeRoot string) ([]any, string, error) {
	session := stringField(task, "session")
	workspace := stringField(task, "workspace")
	if session == "" || workspace == "" {
		return []any{}, "task has no session or workspace", nil
	}
	path, err := herdrPath(runtimeRoot)
	if err != nil {
		return nil, "", err
	}
	listed, code, obsErr := herdrclient.Observe(path, session, 5*time.Second, "pane", "list", "--workspace", workspace)
	if obsErr != nil {
		return nil, "", obsErr
	}
	if listed == nil {
		return nil, code, nil
	}
	var panes []any
	switch v := listed.(type) {
	case *ordjson.Object:
		if inner, ok := v.Get("panes"); ok {
			panes, _ = inner.([]any)
		}
	case []any:
		panes = v
	}
	known := map[string]bool{}
	if p := stringField(task, "pane"); p != "" {
		known[p] = true
	}
	if reviewer := objectField(task, "reviewer"); reviewer != nil {
		if p := stringField(reviewer, "pane"); p != "" {
			known[p] = true
		}
	}
	for _, raw := range listField(record, "services") {
		svc, _ := raw.(*ordjson.Object)
		if p := stringField(svc, "pane"); p != "" {
			known[p] = true
		}
	}
	for _, raw := range listField(record, "resources") {
		r, _ := raw.(*ordjson.Object)
		if stringField(r, "kind") == "pane" {
			known[stringField(r, "id")] = true
		}
	}
	var extra []any
	for _, raw := range panes {
		p, _ := raw.(*ordjson.Object)
		id := stringField(p, "pane_id")
		if known[id] {
			continue
		}
		row := ordjson.NewObject()
		for _, k := range []string{"pane_id", "cwd", "agent"} {
			v, _ := p.Get(k)
			row.Set(k, v)
		}
		extra = append(extra, row)
	}
	return extra, "", nil
}

func waitForListener(s *store.Store, task, parsed *ordjson.Object, session, paneID string, process *ordjson.Object, timeout int, runtimeRoot string) (*ordjson.Object, error) {
	if process == nil || func() any { v, _ := process.Get("pid"); return v }() == nil {
		result := ordjson.NewObject()
		result.Set("ready", false)
		result.Set("checked", "listener")
		result.Set("waited_s", 0.0)
		result.Set("changed", true)
		result.Set("reason", "no recorded process instance to wait for; the launched line never became the pane foreground, so no listener can be attributed to it")
		return result, nil
	}
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	waited := 0.0
	misses := 0
	for {
		info, code, err := paneProcesses(runtimeRoot, session, paneID)
		if err != nil {
			return nil, err
		}
		if info == nil {
			result := ordjson.NewObject()
			result.Set("ready", false)
			result.Set("checked", "listener")
			result.Set("waited_s", json.Number(fmt.Sprintf("%.2f", waited)))
			result.Set("changed", true)
			result.Set("reason", fmt.Sprintf("the service pane cannot be observed during startup (%s); the launch is unknown", code))
			return result, nil
		}
		matched := false
		for _, raw := range listField(info, "processes") {
			p, _ := raw.(*ordjson.Object)
			if sameInstance(process, p) {
				matched = true
				break
			}
		}
		if matched {
			misses = 0
		} else {
			misses++
		}
		if misses >= 2 {
			result := ordjson.NewObject()
			result.Set("ready", false)
			result.Set("checked", "listener")
			result.Set("waited_s", json.Number(fmt.Sprintf("%.2f", waited)))
			result.Set("changed", true)
			result.Set("processes", listField(info, "processes"))
			pairs := make([]any, 0)
			for _, raw := range listField(info, "processes") {
				p, _ := raw.(*ordjson.Object)
				pairs = append(pairs, []any{func() any { v, _ := p.Get("pid"); return v }(), stringField(p, "name")})
			}
			result.Set("reason", fmt.Sprintf("the pane foreground changed during startup from pid %v %q to %v; the launch is unknown and the new process is not adopted", func() any { v, _ := process.Get("pid"); return v }(), stringField(process, "name"), pairs))
			return result, nil
		}
		if misses > 0 {
			if time.Now().After(deadline) {
				result := ordjson.NewObject()
				result.Set("ready", false)
				result.Set("checked", "listener")
				result.Set("waited_s", json.Number(fmt.Sprintf("%.2f", waited)))
				result.Set("changed", true)
				result.Set("processes", listField(info, "processes"))
				result.Set("reason", fmt.Sprintf("the recorded instance was not the pane foreground at the deadline (%ds); the launch is unknown", timeout))
				return result, nil
			}
			time.Sleep(ServicePoll)
			waited += ServicePoll.Seconds()
			continue
		}
		panePIDs := map[int]bool{}
		for _, raw := range listField(info, "processes") {
			p, _ := raw.(*ordjson.Object)
			if n := intField(p, "pid"); n != 0 {
				panePIDs[n] = true
			}
		}
		observation, obsErr := ObservePort(s, task, intField(parsed, "port"), nil)
		if obsErr != nil {
			return nil, obsErr
		}
		if stringField(observation, "state") == "observed" {
			var mine []any
			for _, raw := range listField(observation, "listeners") {
				l, _ := raw.(*ordjson.Object)
				if panePIDs[intField(l, "pid")] {
					mine = append(mine, l)
				}
			}
			if len(mine) > 0 {
				result := ordjson.NewObject()
				result.Set("ready", true)
				result.Set("checked", "listener")
				result.Set("waited_s", json.Number(fmt.Sprintf("%.2f", waited)))
				result.Set("observation", observation)
				result.Set("listener", mine[0])
				return result, nil
			}
			pairs := make([]any, 0)
			for _, raw := range listField(observation, "listeners") {
				l, _ := raw.(*ordjson.Object)
				pairs = append(pairs, []any{func() any { v, _ := l.Get("pid"); return v }(), func() any { v, _ := l.Get("owner"); return v }()})
			}
			result := ordjson.NewObject()
			result.Set("ready", false)
			result.Set("checked", "listener")
			result.Set("waited_s", json.Number(fmt.Sprintf("%.2f", waited)))
			result.Set("observation", observation)
			result.Set("reason", fmt.Sprintf("port %d is taken by a process that is not in the service pane (%v); a checkout cwd alone is not ownership, reported and not terminated", intField(parsed, "port"), pairs))
			return result, nil
		}
		if stringField(observation, "state") == "unverified" {
			result := ordjson.NewObject()
			result.Set("ready", false)
			result.Set("checked", "listener")
			result.Set("waited_s", json.Number(fmt.Sprintf("%.2f", waited)))
			result.Set("observation", observation)
			result.Set("reason", fmt.Sprintf("listeners cannot be observed: %v", func() any { v, _ := observation.Get("error"); return v }()))
			return result, nil
		}
		if time.Now().After(deadline) {
			result := ordjson.NewObject()
			result.Set("ready", false)
			result.Set("checked", "listener")
			result.Set("waited_s", json.Number(fmt.Sprintf("%.2f", waited)))
			result.Set("observation", observation)
			result.Set("reason", fmt.Sprintf("nothing listened on port %d within %ds", intField(parsed, "port"), timeout))
			return result, nil
		}
		time.Sleep(ServicePoll)
		waited += ServicePoll.Seconds()
	}
}

func captureProcess(runtimeRoot, session, paneID, command string) (*ordjson.Object, *ordjson.Object, string, error) {
	deadline := time.Now().Add(time.Duration(ProcessTimeout) * time.Second)
	var info *ordjson.Object
	var found *ordjson.Object
	var code string
	for {
		var err error
		info, code, err = paneProcesses(runtimeRoot, session, paneID)
		if err != nil {
			return nil, nil, "", err
		}
		found = launchedProcess(info, command)
		if found != nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(ServicePoll)
	}
	if info == nil {
		return nil, nil, fmt.Sprintf("process observation uncertain (%s)", code), nil
	}
	if found != nil {
		return info, found, "", nil
	}
	if len(listField(info, "processes")) == 0 {
		return info, nil, "no foreground process appeared; the command exited at once or has not started", nil
	}
	pairs := make([]any, 0)
	for _, raw := range listField(info, "processes") {
		p, _ := raw.(*ordjson.Object)
		pairs = append(pairs, []any{func() any { v, _ := p.Get("pid"); return v }(), stringField(p, "name")})
	}
	return info, nil, fmt.Sprintf("the pane runs %v but none is the launched line %q; identity unproven", pairs, command), nil
}

func activeOrFailed(state string) bool {
	return serviceActive[state] || state == "failed"
}

func Start(s *store.Store, args StartArgs, endpoint *ordjson.Object) (*ordjson.Object, error) {
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	if err := s.CheckMachine(task); err != nil {
		return nil, err
	}
	workerReservation, err := reservations.Worker(task)
	if err != nil {
		if _, ok := err.(*reservations.FormatError); ok {
			return nil, fmt.Errorf("Malformed execution reservation for %s: %s. Service start is refused.", args.Task, err)
		}
		return nil, err
	}
	state := stringField(workerReservation, "state")
	if state == "released" {
		return nil, fmt.Errorf("Worker execution reservation %s is %s; service start is refused before any launch.", stringField(workerReservation, "id"), state)
	}
	worktree, err := requireWorktree(task)
	if err != nil {
		return nil, err
	}
	if stringField(task, "session") == "" || stringField(task, "pane") == "" || stringField(task, "workspace") == "" {
		return nil, fmt.Errorf("The task has no recorded Herdr session, pane, and workspace; services are launched only beside a dispatched worker pane.")
	}
	record, err := Read(s, args.Task)
	if err != nil {
		return nil, err
	}
	if record == nil || objectField(record, "discovery") == nil {
		return nil, fmt.Errorf("Task %s has no discovered commands; run `env discover %s` first. sum launches only what the repository declares.", args.Task, args.Task)
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
		return nil, fmt.Errorf("--timeout must be between 1 and %d seconds.", ReadyTimeoutMax)
	}
	var parsed *ordjson.Object
	if args.URL != "" {
		parsed, err = ParseEndpointURL(args.URL)
		if err != nil {
			return nil, err
		}
	}
	match := strings.TrimSpace(args.Match)
	if match == "" {
		match = ""
	}
	if match != "" && (len(match) > 200 || func() int { _, n := Redact(match); return n }() > 0) {
		return nil, fmt.Errorf("--match must be short readiness text without credential-shaped content.")
	}
	label := strings.TrimSpace(args.Label)
	if len([]rune(label)) > 80 {
		label = string([]rune(label)[:80])
	}
	if _, n := Redact(label); n > 0 {
		return nil, fmt.Errorf("The label contains credential-shaped text.")
	}
	role := endpointRole(task, endpoint)
	if role == "" {
		role = "unattributed"
	}
	session := stringField(task, "session")
	var reusePane string
	reuseCreated := false
	for _, raw := range listField(record, "services") {
		previous, _ := raw.(*ordjson.Object)
		if stringField(previous, "name") != stringField(row, "name") || stringField(previous, "source") != stringField(row, "source") {
			continue
		}
		prevState := stringField(previous, "state")
		if !activeOrFailed(prevState) {
			continue
		}
		view, viewErr := observeService(task, previous, args.RuntimeRoot)
		if viewErr != nil {
			return nil, viewErr
		}
		paneState := stringField(view, "pane_state")
		switch paneState {
		case "none":
			changes := ordjson.NewObject()
			changes.Set("state", "lost")
			changes.Set("reconciled", view)
			if _, err := updateService(s, args.Task, stringField(previous, "id"), "reconciled-no-pane", changes); err != nil {
				return nil, err
			}
		case "absent":
			changes := ordjson.NewObject()
			changes.Set("state", "lost")
			changes.Set("reconciled", view)
			if _, err := updateService(s, args.Task, stringField(previous, "id"), "reconciled-pane-absent", changes); err != nil {
				return nil, err
			}
		case "uncertain":
			reasons := listField(view, "reasons")
			parts := make([]string, 0, len(reasons))
			for _, r := range reasons {
				parts = append(parts, fmt.Sprint(r))
			}
			return nil, fmt.Errorf("Service %s (%s) cannot be observed right now (%s); not launching a possible duplicate.", stringField(previous, "id"), stringField(previous, "name"), strings.Join(parts, "; "))
		default:
			running, _ := view.Get("running")
			if running == true && stringField(view, "ownership") == "owned" && prevState == "failed" {
				changes := ordjson.NewObject()
				changes.Set("reconciled", view)
				if _, err := updateService(s, args.Task, stringField(previous, "id"), "reconciled-failed-still-running", changes); err != nil {
					return nil, err
				}
				processes := listField(view, "processes")
				pid := any(nil)
				if len(processes) > 0 {
					p, _ := processes[0].(*ordjson.Object)
					pid, _ = p.Get("pid")
				}
				return nil, fmt.Errorf("The earlier launch %s of %q failed readiness but its process (pid %v) still runs in pane %s; read that pane, then `env stop %s --service %s` before starting again. Nothing was launched twice.", stringField(previous, "id"), stringField(previous, "name"), pid, stringField(previous, "pane"), args.Task, stringField(previous, "id"))
			}
			if running == true && stringField(view, "ownership") == "owned" {
				nextState := prevState
				if nextState != "running" && nextState != "ready" {
					nextState = "running"
				}
				changes := ordjson.NewObject()
				changes.Set("state", nextState)
				changes.Set("reconciled", view)
				updated, updErr := updateService(s, args.Task, stringField(previous, "id"), "reconciled-running", changes)
				if updErr != nil {
					return nil, updErr
				}
				path, _ := Path(s, args.Task)
				result := ordjson.NewObject()
				result.Set("task", args.Task)
				result.Set("service", updated)
				result.Set("already_running", true)
				result.Set("path", path)
				result.Set("note", "The recorded instance is still running in its pane; nothing was launched twice.")
				return result, nil
			}
			if running == true {
				changes := ordjson.NewObject()
				changes.Set("state", "unknown")
				changes.Set("reconciled", view)
				if _, err := updateService(s, args.Task, stringField(previous, "id"), "reconciled-unknown-process", changes); err != nil {
					return nil, err
				}
				reasons := listField(view, "reasons")
				parts := make([]string, 0, len(reasons))
				for _, r := range reasons {
					parts = append(parts, fmt.Sprint(r))
				}
				return nil, fmt.Errorf("Pane %s of service %s hosts a process sum did not start (%s); not launching a duplicate and not stopping it. Inspect the pane.", stringField(previous, "pane"), stringField(previous, "id"), strings.Join(parts, "; "))
			}
			reasons := listField(view, "reasons")
			if len(reasons) > 0 {
				changes := ordjson.NewObject()
				changes.Set("state", "unknown")
				changes.Set("reconciled", view)
				if _, err := updateService(s, args.Task, stringField(previous, "id"), "reconciled-pane-changed", changes); err != nil {
					return nil, err
				}
				parts := make([]string, 0, len(reasons))
				for _, r := range reasons {
					parts = append(parts, fmt.Sprint(r))
				}
				return nil, fmt.Errorf("Pane %s recorded for service %s changed (%s); inspect it before launching again.", stringField(previous, "pane"), stringField(previous, "id"), strings.Join(parts, "; "))
			}
			changes := ordjson.NewObject()
			changes.Set("state", "stopped")
			changes.Set("reconciled", view)
			changes.Set("exit_verified", true)
			if _, err := updateService(s, args.Task, stringField(previous, "id"), "reconciled-exited", changes); err != nil {
				return nil, err
			}
			reusePane = stringField(previous, "pane")
			if launchObj := objectField(previous, "launch"); launchObj != nil {
				created, _ := launchObj.Get("pane_created")
				reuseCreated, _ = created.(bool)
			}
		}
	}
	extra, extraCode, extraErr := unrecordedPanes(task, record, args.RuntimeRoot)
	if extraErr != nil {
		return nil, extraErr
	}
	if extra == nil && extraCode != "" {
		return nil, fmt.Errorf("Panes of workspace %s cannot be listed (%s); not launching without seeing the workspace.", stringField(task, "workspace"), extraCode)
	}
	if len(extra) > 0 {
		ids := make([]string, 0, len(extra))
		for _, raw := range extra {
			p, _ := raw.(*ordjson.Object)
			ids = append(ids, stringField(p, "pane_id"))
		}
		return nil, fmt.Errorf("Unrecorded pane(s) %v sit in the task workspace, possibly from an interrupted launch. sum neither adopts nor closes them: record one with `env record %s --pane ID` after checking it, or close it yourself, then start again.", ids, args.Task)
	}
	if parsed != nil {
		conflicts, confErr := endpointConflicts(s, task, parsed)
		if confErr != nil {
			return nil, confErr
		}
		if len(conflicts) > 0 {
			pairs := make([]any, 0, len(conflicts))
			for _, raw := range conflicts {
				c, _ := raw.(*ordjson.Object)
				pairs = append(pairs, []any{stringField(c, "task"), stringField(c, "url")})
			}
			return nil, fmt.Errorf("%s is recorded as owned by another active task: %v; choose the port this task's environment reports.", stringField(parsed, "url"), pairs)
		}
		if local, _ := parsed.Get("local"); local == true {
			busy, busyErr := ObservePort(s, task, intField(parsed, "port"), nil)
			if busyErr != nil {
				return nil, busyErr
			}
			if stringField(busy, "state") == "observed" {
				serviceID, idErr := newServiceID()
				if idErr != nil {
					return nil, idErr
				}
				service := ordjson.NewObject()
				service.Set("id", serviceID)
				service.Set("name", stringField(row, "name"))
				service.Set("source", stringField(row, "source"))
				service.Set("kind", stringField(row, "kind"))
				service.Set("command", command)
				service.Set("launch", launch)
				var labelValue any
				if label != "" {
					labelValue = label
				}
				service.Set("label", labelValue)
				service.Set("url", stringField(parsed, "url"))
				service.Set("port", func() any { v, _ := parsed.Get("port"); return v }())
				service.Set("state", "conflict")
				service.Set("intent_at", store.Now())
				service.Set("by", role)
				service.Set("pane", nil)
				service.Set("process", nil)
				service.Set("conflict", busy)
				service.Set("history", []any{})
				unlock, lockErr := s.Lock()
				if lockErr != nil {
					return nil, lockErr
				}
				current, ensErr := Ensure(s, task)
				if ensErr != nil {
					unlock()
					return nil, ensErr
				}
				services := listField(current, "services")
				if len(services) > serviceLimit-1 {
					services = services[len(services)-(serviceLimit-1):]
				}
				current.Set("services", append(services, service))
				event := ordjson.NewObject()
				event.Set("event", "start-conflict")
				event.Set("service", serviceID)
				event.Set("port", func() any { v, _ := parsed.Get("port"); return v }())
				if writeErr := Write(s, current, event); writeErr != nil {
					unlock()
					return nil, writeErr
				}
				unlock()
				pairs := make([]any, 0)
				for _, raw := range listField(busy, "listeners") {
					l, _ := raw.(*ordjson.Object)
					pairs = append(pairs, []any{func() any { v, _ := l.Get("pid"); return v }(), func() any { v, _ := l.Get("owner"); return v }(), func() any { v, _ := l.Get("cwd"); return v }()})
				}
				return nil, fmt.Errorf("Port %d is already taken by %v; recorded as a conflict (%s). sum never terminates the occupant; pick the port the repository's configuration reports or stop that service yourself.", intField(parsed, "port"), pairs, serviceID)
			}
		}
	}
	serviceID, err := newServiceID()
	if err != nil {
		return nil, err
	}
	service := ordjson.NewObject()
	service.Set("id", serviceID)
	service.Set("name", stringField(row, "name"))
	service.Set("source", stringField(row, "source"))
	service.Set("kind", stringField(row, "kind"))
	service.Set("command", command)
	launch.Set("cwd", worktree)
	launch.Set("pane_created", false)
	service.Set("launch", launch)
	var labelValue any
	if label != "" {
		labelValue = label
	}
	service.Set("label", labelValue)
	if parsed != nil {
		service.Set("url", stringField(parsed, "url"))
		service.Set("port", func() any { v, _ := parsed.Get("port"); return v }())
	} else {
		service.Set("url", nil)
		service.Set("port", nil)
	}
	var matchValue any
	if match != "" {
		matchValue = match
	}
	service.Set("match", matchValue)
	service.Set("readiness_timeout", jsonInt(timeout))
	service.Set("state", "intended")
	service.Set("intent_at", store.Now())
	service.Set("by", role)
	service.Set("pane", nil)
	service.Set("workspace", nil)
	service.Set("process", nil)
	service.Set("readiness", nil)
	service.Set("history", []any{})
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	currentTask, err := s.ReadTask(args.Task)
	if err != nil {
		unlock()
		return nil, err
	}
	if err := repair.RefuseDuringCleanup(currentTask, "Service start"); err != nil {
		unlock()
		return nil, err
	}
	currentReservation, err := reservations.Worker(currentTask)
	if err != nil {
		unlock()
		if _, ok := err.(*reservations.FormatError); ok {
			return nil, fmt.Errorf("Malformed execution reservation for %s: %s. Service start is refused.", args.Task, err)
		}
		return nil, err
	}
	if stringField(currentReservation, "id") != stringField(workerReservation, "id") || stringField(currentReservation, "state") == "released" {
		unlock()
		return nil, fmt.Errorf("Worker execution reservation changed during service inspection; no service was launched.")
	}
	current, err := Ensure(s, task)
	if err != nil {
		unlock()
		return nil, err
	}
	services := listField(current, "services")
	active := 0
	for _, raw := range services {
		svc, _ := raw.(*ordjson.Object)
		if serviceActive[stringField(svc, "state")] {
			active++
		}
	}
	if active >= serviceLimit {
		unlock()
		return nil, fmt.Errorf("At most %d active services per task; stop or reconcile one first.", serviceLimit)
	}
	keep := serviceLimit*2 - 1
	if len(services) > keep {
		services = services[len(services)-keep:]
	}
	current.Set("services", append(services, service))
	event := ordjson.NewObject()
	event.Set("event", "start-intent")
	event.Set("service", serviceID)
	event.Set("command", stringField(row, "name"))
	event.Set("by", role)
	if err := Write(s, current, event); err != nil {
		unlock()
		return nil, err
	}
	unlock()
	paneID := reusePane
	created := reuseCreated
	path, err := herdrPath(args.RuntimeRoot)
	if err != nil {
		return nil, err
	}
	if paneID == "" {
		split, splitErr := herdrclient.Call(path, session, 15*time.Second, "pane", "split", stringField(task, "pane"), "--direction", "down", "--cwd", worktree, "--no-focus")
		if splitErr != nil {
			changes := ordjson.NewObject()
			changes.Set("state", "unknown")
			changes.Set("error", fmt.Sprintf("pane split returned no pane id: %s", splitErr))
			if _, updErr := updateService(s, args.Task, serviceID, "split-unrecognized", changes); updErr != nil {
				return nil, updErr
			}
			return nil, fmt.Errorf("Herdr `pane split` returned no pane ID (%s); the intent %s stays recorded for reconciliation.", splitErr, serviceID)
		}
		splitObj, _ := split.(*ordjson.Object)
		paneObj := objectField(splitObj, "pane")
		paneID = stringField(paneObj, "pane_id")
		created = true
		if paneID == "" || !paneIDPattern.MatchString(paneID) {
			encoded, _ := json.Marshal(split)
			preview := string(encoded)
			if len(preview) > 200 {
				preview = preview[:200]
			}
			changes := ordjson.NewObject()
			changes.Set("state", "unknown")
			changes.Set("error", "pane split returned no pane id: "+preview)
			if _, updErr := updateService(s, args.Task, serviceID, "split-unrecognized", changes); updErr != nil {
				return nil, updErr
			}
			return nil, fmt.Errorf("Herdr `pane split` returned no pane ID (%s); the intent %s stays recorded for reconciliation.", preview, serviceID)
		}
	}
	info, _, _ := paneProcesses(args.RuntimeRoot, session, paneID)
	var shellPID any
	if info != nil {
		shellPID, _ = info.Get("shell_pid")
	}
	processStub := ordjson.NewObject()
	processStub.Set("pane", paneID)
	processStub.Set("shell_pid", shellPID)
	processStub.Set("pid", nil)
	processStub.Set("argv", nil)
	processStub.Set("name", nil)
	processStub.Set("cwd", nil)
	processStub.Set("observed_at", store.Now())
	launchCopy := ordjson.NewObject()
	for _, k := range launch.Keys() {
		v, _ := launch.Get(k)
		launchCopy.Set(k, v)
	}
	launchCopy.Set("pane_created", created)
	changes := ordjson.NewObject()
	changes.Set("state", "starting")
	changes.Set("pane", paneID)
	changes.Set("workspace", workspaceOf(paneID))
	changes.Set("session", session)
	changes.Set("launch", launchCopy)
	changes.Set("process", processStub)
	if _, err := updateService(s, args.Task, serviceID, "pane-recorded", changes); err != nil {
		return nil, err
	}
	unlock, err = s.Lock()
	if err != nil {
		return nil, err
	}
	current, err = Ensure(s, task)
	if err != nil {
		unlock()
		return nil, err
	}
	resource := ordjson.NewObject()
	resource.Set("kind", "pane")
	resource.Set("id", paneID)
	resource.Set("session", session)
	resource.Set("ownership", "owned")
	resource.Set("state", "observed")
	resource.Set("label", "service "+stringField(row, "name"))
	resource.Set("note", "pane sum split for service "+serviceID)
	resource.Set("observed_at", store.Now())
	resource.Set("recorded_by", role)
	resource.Set("claimed_ownership", nil)
	resource.Set("service", serviceID)
	resources := listField(current, "resources")
	replaced := false
	for i, raw := range resources {
		r, _ := raw.(*ordjson.Object)
		if stringField(r, "kind") == "pane" && stringField(r, "id") == paneID {
			resources[i] = resource
			replaced = true
			break
		}
	}
	if !replaced {
		resources = append(resources, resource)
	}
	current.Set("resources", resources)
	resEvent := ordjson.NewObject()
	resEvent.Set("event", "record")
	resEvent.Set("kind", "pane")
	resEvent.Set("id", paneID)
	resEvent.Set("ownership", "owned")
	resEvent.Set("by", role)
	if err := Write(s, current, resEvent); err != nil {
		unlock()
		return nil, err
	}
	unlock()
	if _, err := herdrclient.CallRaw(path, session, 15*time.Second, "pane", "run", paneID, command); err != nil {
		return nil, err
	}
	info, found, problem, capErr := captureProcess(args.RuntimeRoot, session, paneID, command)
	if capErr != nil {
		return nil, capErr
	}
	var process *ordjson.Object
	if found != nil {
		var shell any
		if info != nil {
			shell, _ = info.Get("shell_pid")
		}
		process = processIdentity(found, paneID, shell)
		var siblings []any
		for _, raw := range listField(info, "processes") {
			p, _ := raw.(*ordjson.Object)
			pid, _ := p.Get("pid")
			if pid != nil && pid != func() any { v, _ := found.Get("pid"); return v }() {
				siblings = append(siblings, pid)
			}
		}
		process.Set("siblings", siblings)
	}
	state = "running"
	if process == nil {
		state = "unknown"
	}
	procValue := process
	if procValue == nil {
		procValue = processStub
	}
	changes = ordjson.NewObject()
	changes.Set("state", state)
	changes.Set("process", procValue)
	changes.Set("launched_at", store.Now())
	changes.Set("problem", problem)
	if _, err := updateService(s, args.Task, serviceID, "process-observed", changes); err != nil {
		return nil, err
	}
	var readiness *ordjson.Object
	if parsed != nil {
		if local, _ := parsed.Get("local"); local == true {
			readiness, err = waitForListener(s, task, parsed, session, paneID, process, timeout, args.RuntimeRoot)
			if err != nil {
				return nil, err
			}
		}
	}
	if readiness == nil && match != "" {
		_, waitErr := herdrclient.CallRaw(path, session, time.Duration(timeout+10)*time.Second, "pane", "wait-output", paneID, "--match", match, "--timeout", fmt.Sprint(timeout*1000))
		readiness = ordjson.NewObject()
		if waitErr == nil {
			readiness.Set("ready", true)
			readiness.Set("checked", "output")
			readiness.Set("match", match)
		} else {
			reason := waitErr.Error()
			if len(reason) > 120 {
				reason = reason[len(reason)-120:]
			}
			readiness.Set("ready", false)
			readiness.Set("checked", "output")
			readiness.Set("match", match)
			readiness.Set("reason", fmt.Sprintf("readiness text not seen within %ds (%s)", timeout, reason))
		}
	}
	if readiness == nil {
		readiness = ordjson.NewObject()
		readiness.Set("ready", nil)
		readiness.Set("checked", "process-only")
		readiness.Set("note", "no --url or --match given; the process is observed, readiness is not asserted")
	}
	if ready, _ := readiness.Get("ready"); ready == true {
		info2, _, _ := paneProcesses(args.RuntimeRoot, session, paneID)
		matched := false
		if info2 != nil && process != nil {
			for _, raw := range listField(info2, "processes") {
				p, _ := raw.(*ordjson.Object)
				if sameInstance(process, p) {
					matched = true
					break
				}
			}
		}
		if info2 == nil || process == nil || !matched {
			readiness.Set("ready", false)
			readiness.Set("changed", true)
			readiness.Set("reason", "the pane foreground is not the recorded instance after readiness; the launch is unknown")
			state = "unknown"
		} else {
			state = "ready"
			var siblings []any
			for _, raw := range listField(info2, "processes") {
				p, _ := raw.(*ordjson.Object)
				pid, _ := p.Get("pid")
				if pid != nil && pid != func() any { v, _ := process.Get("pid"); return v }() {
					siblings = append(siblings, pid)
				}
			}
			process.Set("siblings", siblings)
		}
	}
	if ready, _ := readiness.Get("ready"); ready == false {
		if changed, _ := readiness.Get("changed"); changed == true {
			state = "unknown"
		} else if stringField(readiness, "checked") == "listener" {
			obs := objectField(readiness, "observation")
			if stringField(obs, "state") == "observed" {
				state = "conflict"
			} else {
				state = "failed"
			}
		} else {
			state = "failed"
		}
	}
	changes = ordjson.NewObject()
	changes.Set("state", state)
	changes.Set("readiness", readiness)
	if process != nil {
		changes.Set("process", process)
	}
	rowAfter, err := updateService(s, args.Task, serviceID, "readiness", changes)
	if err != nil {
		return nil, err
	}
	envPath, _ := Path(s, args.Task)
	result := ordjson.NewObject()
	result.Set("task", args.Task)
	result.Set("service", rowAfter)
	result.Set("path", envPath)
	result.Set("already_running", false)
	result.Set("by", role)
	result.Set("note", serviceNote)
	if state == "ready" && parsed != nil {
		unlock, err = s.Lock()
		if err != nil {
			return nil, err
		}
		current, err = Ensure(s, task)
		if err != nil {
			unlock()
			return nil, err
		}
		observation := objectField(readiness, "observation")
		conflicts, _ := endpointConflicts(s, task, parsed)
		endpointRow, buildErr := buildEndpoint(current, parsed, observation, "", func() string {
			if label != "" {
				return label
			}
			return stringField(row, "name")
		}(), role, store.Now(), conflicts)
		if buildErr != nil {
			unlock()
			return nil, buildErr
		}
		endpointRow.Set("service", serviceID)
		if err := upsertEndpoint(current, endpointRow); err != nil {
			unlock()
			return nil, err
		}
		ev := ordjson.NewObject()
		ev.Set("event", "record")
		ev.Set("kind", "url")
		ev.Set("id", stringField(endpointRow, "id"))
		ev.Set("state", stringField(endpointRow, "state"))
		ev.Set("ownership", stringField(endpointRow, "ownership"))
		ev.Set("by", role)
		if err := Write(s, current, ev); err != nil {
			unlock()
			return nil, err
		}
		unlock()
		result.Set("endpoint", endpointRow)
	}
	if args.Log != "" {
		validated, valErr := validateLogPath(task, args.Log)
		if valErr != nil {
			return nil, valErr
		}
		unlock, err = s.Lock()
		if err != nil {
			return nil, err
		}
		current, err = Ensure(s, task)
		if err != nil {
			unlock()
			return nil, err
		}
		logLabel := label
		if logLabel == "" {
			logLabel = stringField(row, "name")
		}
		logRow := buildLog(validated, observeLog(stringField(validated, "path"), worktree, stringField(validated, "relative")), "", logLabel, role, store.Now())
		logRow.Set("service", serviceID)
		if err := upsertLog(current, logRow); err != nil {
			unlock()
			return nil, err
		}
		ev := ordjson.NewObject()
		ev.Set("event", "record")
		ev.Set("kind", "log")
		ev.Set("id", stringField(logRow, "id"))
		ev.Set("state", stringField(logRow, "state"))
		ev.Set("by", role)
		if err := Write(s, current, ev); err != nil {
			unlock()
			return nil, err
		}
		unlock()
		result.Set("log", logRow)
		logChanges := ordjson.NewObject()
		logChanges.Set("log", stringField(logRow, "path"))
		if _, err := updateService(s, args.Task, serviceID, "log-recorded", logChanges); err != nil {
			return nil, err
		}
		fresh, _ := Read(s, args.Task)
		svc, _ := serviceByID(fresh, serviceID)
		result.Set("service", svc)
	}
	if state == "failed" || state == "conflict" || state == "unknown" {
		warning := stringField(readiness, "reason")
		if warning == "" {
			warning = problem
		}
		if warning == "" {
			warning = "the launch did not reach a verified running state; the pane is left as it is for inspection"
		}
		result.Set("warning", warning)
	}
	return result, nil
}

func Stop(s *store.Store, args StopArgs) (*ordjson.Object, error) {
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	if err := s.CheckMachine(task); err != nil {
		return nil, err
	}
	if _, err := requireWorktree(task); err != nil {
		return nil, err
	}
	if stringField(task, "session") == "" {
		return nil, fmt.Errorf("The task has no recorded Herdr session; nothing can be observed or stopped.")
	}
	record, err := Read(s, args.Task)
	if err != nil {
		return nil, err
	}
	if record == nil || len(listField(record, "services")) == 0 {
		result := ordjson.NewObject()
		result.Set("task", args.Task)
		result.Set("services", []any{})
		result.Set("note", "No services were launched through sum for this task; nothing to stop. Manually started environments stay untouched.")
		return result, nil
	}
	timeout := StopTimeout
	if args.TimeoutSet {
		timeout = args.Timeout
	}
	if timeout < 1 || timeout > ReadyTimeoutMax {
		return nil, fmt.Errorf("--timeout must be between 1 and %d seconds.", ReadyTimeoutMax)
	}
	var ids []string
	if args.Service != "" {
		ids = []string{args.Service}
	}
	results, err := stopServices(s, task, ids, timeout, args.RuntimeRoot)
	if err != nil {
		return nil, err
	}
	path, _ := Path(s, args.Task)
	var stopped, refused, pending []any
	for _, raw := range results {
		r, _ := raw.(*ordjson.Object)
		id := stringField(r, "id")
		switch stringField(r, "state") {
		case "stopped":
			stopped = append(stopped, id)
		}
		if stringField(r, "action") == "refused" {
			refused = append(refused, id)
		}
		if stringField(r, "state") == "stopping" || stringField(r, "state") == "unknown" {
			pending = append(pending, id)
		}
	}
	result := ordjson.NewObject()
	result.Set("task", args.Task)
	result.Set("services", results)
	result.Set("stopped", stopped)
	result.Set("refused", refused)
	result.Set("pending", pending)
	result.Set("path", path)
	result.Set("note", "Only instances re-proven by pane, shell, pid, and argv received one interrupt; nothing was killed by name, port, or cwd.")
	return result, nil
}

func stopServices(s *store.Store, task *ordjson.Object, serviceIDs []string, timeout int, runtimeRoot string) ([]any, error) {
	record, err := Read(s, stringField(task, "id"))
	if err != nil {
		return nil, err
	}
	if record == nil {
		record = emptyEnvironment(stringField(task, "id"))
	}
	var rows []*ordjson.Object
	if serviceIDs != nil {
		wanted := map[string]bool{}
		for _, id := range serviceIDs {
			wanted[id] = true
		}
		for _, raw := range listField(record, "services") {
			svc, _ := raw.(*ordjson.Object)
			if wanted[stringField(svc, "id")] {
				rows = append(rows, svc)
				delete(wanted, stringField(svc, "id"))
			}
		}
		if len(wanted) > 0 {
			missing := make([]string, 0, len(wanted))
			for id := range wanted {
				missing = append(missing, id)
			}
			return nil, fmt.Errorf("No service %v recorded for task %s.", missing, stringField(task, "id"))
		}
	} else {
		for _, raw := range listField(record, "services") {
			svc, _ := raw.(*ordjson.Object)
			st := stringField(svc, "state")
			if serviceActive[st] || st == "failed" {
				rows = append(rows, svc)
			}
		}
	}
	var results []any
	for _, svc := range rows {
		row, stopErr := stopService(s, task, svc, timeout, runtimeRoot)
		if stopErr != nil {
			return nil, stopErr
		}
		results = append(results, row)
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := Read(s, stringField(task, "id"))
	if err != nil || current == nil {
		return results, err
	}
	closed := map[string]bool{}
	for _, raw := range results {
		r, _ := raw.(*ordjson.Object)
		if v, _ := r.Get("closed_pane"); v == true {
			closed[stringField(r, "pane")] = true
		}
	}
	if len(closed) == 0 {
		return results, nil
	}
	for _, raw := range listField(current, "resources") {
		resource, _ := raw.(*ordjson.Object)
		if stringField(resource, "kind") == "pane" && closed[stringField(resource, "id")] {
			note := stringField(resource, "note")
			if note != "" {
				note = strings.TrimPrefix(note+"; closed after verified exit", "; ")
			} else {
				note = "closed after verified exit"
			}
			resource.Set("state", "closed")
			resource.Set("observed_at", store.Now())
			resource.Set("note", note)
		}
	}
	ids := make([]any, 0, len(closed))
	for id := range closed {
		ids = append(ids, id)
	}
	event := ordjson.NewObject()
	event.Set("event", "stop")
	event.Set("closed_panes", ids)
	if err := Write(s, current, event); err != nil {
		return nil, err
	}
	return results, nil
}

func stopService(s *store.Store, task, service *ordjson.Object, timeout int, runtimeRoot string) (*ordjson.Object, error) {
	session := stringField(task, "session")
	view, err := observeService(task, service, runtimeRoot)
	if err != nil {
		return nil, err
	}
	outcome := ordjson.NewObject()
	outcome.Set("id", stringField(service, "id"))
	outcome.Set("name", stringField(service, "name"))
	outcome.Set("pane", func() any { v, _ := service.Get("pane"); return v }())
	outcome.Set("before", view)
	paneState := stringField(view, "pane_state")
	if paneState == "none" || paneState == "absent" {
		state := "lost"
		reason := "no pane was ever recorded"
		if paneState == "absent" {
			state = "stopped"
			reason = "pane already absent"
		}
		stop := ordjson.NewObject()
		stop.Set("action", "none")
		stop.Set("reason", reason)
		changes := ordjson.NewObject()
		changes.Set("state", state)
		changes.Set("stopped_at", store.Now())
		changes.Set("exit_verified", false)
		changes.Set("stop", stop)
		row, updErr := updateService(s, stringField(task, "id"), stringField(service, "id"), "stop-pane-absent", changes)
		if updErr != nil {
			return nil, updErr
		}
		outcome.Set("action", "none")
		outcome.Set("state", stringField(row, "state"))
		outcome.Set("closed_pane", false)
		return outcome, nil
	}
	running, _ := view.Get("running")
	if paneState == "uncertain" || stringField(view, "ownership") != "owned" {
		stop := ordjson.NewObject()
		stop.Set("action", "refused")
		stop.Set("reasons", listField(view, "reasons"))
		nextState := "unknown"
		if running == false && len(listField(view, "reasons")) == 0 {
			nextState = stringField(service, "state")
		}
		changes := ordjson.NewObject()
		changes.Set("state", nextState)
		changes.Set("stop", stop)
		row, updErr := updateService(s, stringField(task, "id"), stringField(service, "id"), "stop-refused", changes)
		if updErr != nil {
			return nil, updErr
		}
		outcome.Set("action", "refused")
		outcome.Set("state", stringField(row, "state"))
		outcome.Set("reasons", listField(view, "reasons"))
		outcome.Set("closed_pane", false)
		return outcome, nil
	}
	sent := false
	path, err := herdrPath(runtimeRoot)
	if err != nil {
		return nil, err
	}
	if running == true {
		stop := ordjson.NewObject()
		stop.Set("action", "interrupt")
		stop.Set("key", InterruptKey)
		stop.Set("at", store.Now())
		stop.Set("timeout_s", jsonInt(timeout))
		changes := ordjson.NewObject()
		changes.Set("state", "stopping")
		changes.Set("stop", stop)
		if _, err := updateService(s, stringField(task, "id"), stringField(service, "id"), "stop-interrupt", changes); err != nil {
			return nil, err
		}
		if _, err := herdrclient.CallRaw(path, session, 10*time.Second, "pane", "send-keys", stringField(service, "pane"), InterruptKey); err != nil {
			return nil, err
		}
		sent = true
		deadline := time.Now().Add(time.Duration(timeout) * time.Second)
		for {
			info, code, procErr := paneProcesses(runtimeRoot, session, stringField(service, "pane"))
			if procErr != nil {
				return nil, procErr
			}
			if info != nil && len(listField(info, "processes")) == 0 {
				break
			}
			if time.Now().After(deadline) {
				stop := ordjson.NewObject()
				stop.Set("action", "interrupt")
				stop.Set("key", InterruptKey)
				stop.Set("result", "still running after the bound")
				stop.Set("timeout_s", jsonInt(timeout))
				if info != nil {
					stop.Set("processes", listField(info, "processes"))
				} else {
					stop.Set("processes", nil)
				}
				stop.Set("error", code)
				changes := ordjson.NewObject()
				changes.Set("state", "stopping")
				changes.Set("stop", stop)
				if _, err := updateService(s, stringField(task, "id"), stringField(service, "id"), "stop-timeout", changes); err != nil {
					return nil, err
				}
				outcome.Set("action", "interrupt")
				outcome.Set("state", "stopping")
				outcome.Set("closed_pane", false)
				outcome.Set("reason", fmt.Sprintf("process still runs %ds after the interrupt; sum escalates nothing (no kill, no pkill). Stop it yourself or run stop again later.", timeout))
				return outcome, nil
			}
			time.Sleep(ServicePoll)
		}
	}
	var portCheck *ordjson.Object
	if port := intField(service, "port"); port != 0 {
		var obsErr error
		portCheck, obsErr = ObservePort(s, task, port, nil)
		if obsErr != nil {
			return nil, obsErr
		}
		if stringField(portCheck, "state") == "observed" {
			pairs := make([]any, 0)
			for _, raw := range listField(portCheck, "listeners") {
				l, _ := raw.(*ordjson.Object)
				pairs = append(pairs, []any{func() any { v, _ := l.Get("pid"); return v }(), func() any { v, _ := l.Get("owner"); return v }()})
			}
			action := "none"
			if sent {
				action = "interrupt"
			}
			stop := ordjson.NewObject()
			stop.Set("action", action)
			stop.Set("result", fmt.Sprintf("process exited but port %d is still taken by %v", port, pairs))
			changes := ordjson.NewObject()
			changes.Set("state", "unknown")
			changes.Set("stop", stop)
			if _, err := updateService(s, stringField(task, "id"), stringField(service, "id"), "stop-port-still-taken", changes); err != nil {
				return nil, err
			}
			outcome.Set("action", action)
			outcome.Set("state", "unknown")
			outcome.Set("closed_pane", false)
			outcome.Set("reason", fmt.Sprintf("port %d is still taken after the exit; a detached child or another process holds it, nothing is terminated", port))
			return outcome, nil
		}
	}
	closed := false
	if launch := objectField(service, "launch"); launch != nil {
		if created, _ := launch.Get("pane_created"); created == true {
			result, code, obsErr := herdrclient.Observe(path, session, 10*time.Second, "pane", "close", stringField(service, "pane"))
			if obsErr != nil {
				return nil, obsErr
			}
			if result == nil && code != "pane_not_found" {
				action := "none"
				if sent {
					action = "interrupt"
				}
				stop := ordjson.NewObject()
				stop.Set("action", action)
				stop.Set("result", "exited")
				stop.Set("pane_close", code)
				changes := ordjson.NewObject()
				changes.Set("state", "stopped")
				changes.Set("exit_verified", true)
				changes.Set("stopped_at", store.Now())
				changes.Set("stop", stop)
				if _, err := updateService(s, stringField(task, "id"), stringField(service, "id"), "stop-close-failed", changes); err != nil {
					return nil, err
				}
				outcome.Set("action", action)
				outcome.Set("state", "stopped")
				outcome.Set("closed_pane", false)
				outcome.Set("reason", fmt.Sprintf("pane close returned %s; the empty pane stays", code))
				return outcome, nil
			}
			closed = true
		}
	}
	action := "none"
	if sent {
		action = "interrupt"
	}
	stop := ordjson.NewObject()
	stop.Set("action", action)
	stop.Set("result", "exited")
	if portCheck != nil {
		stop.Set("port", stringField(portCheck, "state"))
	} else {
		stop.Set("port", nil)
	}
	stop.Set("pane_closed", closed)
	changes := ordjson.NewObject()
	changes.Set("state", "stopped")
	changes.Set("exit_verified", true)
	changes.Set("stopped_at", store.Now())
	changes.Set("stop", stop)
	row, err := updateService(s, stringField(task, "id"), stringField(service, "id"), "stopped", changes)
	if err != nil {
		return nil, err
	}
	_ = row
	outcome.Set("action", action)
	outcome.Set("state", "stopped")
	outcome.Set("closed_pane", closed)
	outcome.Set("exit_verified", true)
	return outcome, nil
}
