package environment

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

const (
	listenerTimeout = 30 * time.Second
	lsofTimeout     = 30 * time.Second
)

type RecordArgs struct {
	Task       string
	URL        string
	Log        string
	Pane       string
	Container  string
	Label      string
	Ownership  string
	RuntimeRoot string
}

type InspectArgs struct {
	Task        string
	RuntimeRoot string
}

func Discover(s *store.Store, taskID string, endpoint *ordjson.Object, sumctlPath string) (*ordjson.Object, error) {
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	worktree, err := requireWorktree(task)
	if err != nil {
		return nil, err
	}
	discovery, err := DiscoverConfiguration(worktree)
	if err != nil {
		return nil, err
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err = s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	record, err := Ensure(s, task)
	if err != nil {
		return nil, err
	}
	previous := objectField(record, "discovery")
	changed := stringField(previous, "config_revision") != stringField(discovery, "config_revision")
	record.Set("discovery", discovery)
	if pane := stringField(task, "pane"); pane != "" {
		found := false
		for _, raw := range listField(record, "resources") {
			r, _ := raw.(*ordjson.Object)
			if stringField(r, "kind") == "pane" && stringField(r, "id") == pane {
				found = true
				break
			}
		}
		if !found {
			own := ordjson.NewObject()
			own.Set("kind", "pane")
			own.Set("id", pane)
			own.Set("session", func() any { v, _ := task.Get("session"); return v }())
			own.Set("ownership", "owned")
			own.Set("state", "observed")
			own.Set("note", "the task's own worker pane")
			own.Set("observed_at", stringField(discovery, "observed_at"))
			record.Set("resources", append(listField(record, "resources"), own))
		}
	}
	role := endpointRole(task, endpoint)
	if role == "" {
		role = "unattributed"
	}
	event := ordjson.NewObject()
	event.Set("event", "discover")
	event.Set("by", role)
	event.Set("config_revision", stringField(discovery, "config_revision"))
	event.Set("changed", changed)
	var previousRevision any
	if previous != nil {
		previousRevision, _ = previous.Get("config_revision")
	}
	event.Set("previous_revision", previousRevision)
	if err := Write(s, record, event); err != nil {
		return nil, err
	}
	path, _ := Path(s, taskID)
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("path", path)
	result.Set("config_revision", stringField(discovery, "config_revision"))
	result.Set("changed", changed)
	sources, _ := discovery.Get("sources")
	result.Set("sources", sources)
	summary, _ := discovery.Get("summary")
	result.Set("commands", summary)
	problems, _ := discovery.Get("problems")
	result.Set("problems", problems)
	origins, _ := discovery.Get("task_origins")
	result.Set("task_origins", origins)
	contract, _ := discovery.Get("verification_contract")
	result.Set("verification_contract", contract)
	result.Set("by", role)
	result.Set("note", Note)
	_ = sumctlPath
	return result, nil
}

func ParseEndpointURL(url string) (*ordjson.Object, error) {
	if strings.TrimSpace(url) == "" || len(url) > 400 || strings.Contains(url, "\x00") {
		return nil, fmt.Errorf("--url must be one URL without whitespace (at most 400 characters).")
	}
	url = strings.TrimSpace(url)
	for _, ch := range url {
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			return nil, fmt.Errorf("--url must be one URL without whitespace (at most 400 characters).")
		}
	}
	match := urlScheme.FindStringSubmatch(url)
	if match == nil {
		return nil, fmt.Errorf("--url %q is not scheme://host[:port][/path]; give the endpoint as the application reports it.", url)
	}
	scheme := strings.ToLower(match[1])
	authority := match[2]
	rest := match[3]
	if strings.Contains(authority, "@") {
		return nil, fmt.Errorf("The URL carries user information (user:password@host); reference where the credential lives instead of storing it.")
	}
	if _, n := Redact(url); n > 0 {
		return nil, fmt.Errorf("The URL contains credential-shaped text; environment records hold no secrets.")
	}
	if _, ok := defaultPorts[scheme]; !ok && scheme != "tcp" && scheme != "grpc" {
		keys := make([]string, 0, len(defaultPorts)+2)
		for k := range defaultPorts {
			keys = append(keys, k)
		}
		keys = append(keys, "tcp", "grpc")
		return nil, fmt.Errorf("Unsupported URL scheme %q; supported: %v.", scheme, keys)
	}
	hostMatch := hostPort.FindStringSubmatch(authority)
	if hostMatch == nil || hostMatch[1] == "" {
		return nil, fmt.Errorf("--url %q has no host.", url)
	}
	host := hostMatch[1]
	var port int
	explicit := hostMatch[2] != ""
	if explicit {
		n, err := strconv.Atoi(hostMatch[2])
		if err != nil {
			return nil, fmt.Errorf("--url %q has no host.", url)
		}
		port = n
	} else if p, ok := defaultPorts[scheme]; ok {
		port = p
	} else {
		return nil, fmt.Errorf("--url %q needs an explicit port for scheme %q.", url, scheme)
	}
	if port <= 0 || port >= 65536 {
		return nil, fmt.Errorf("Port %d is out of range.", port)
	}
	if len(rest) > 200 {
		rest = rest[:200]
	}
	result := ordjson.NewObject()
	result.Set("url", url)
	result.Set("scheme", scheme)
	result.Set("host", host)
	result.Set("port", jsonInt(port))
	result.Set("path", rest)
	result.Set("local", localHosts[strings.ToLower(host)])
	result.Set("explicit_port", explicit)
	return result, nil
}

func listeners() (map[int][]*ordjson.Object, string) {
	lsof, err := exec.LookPath("lsof")
	if err != nil {
		return nil, err.Error()
	}
	result, runErr := proc.Run([]string{lsof, "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpn", "-w"}, "", listenerTimeout, false, nil)
	if runErr != nil && result.Code == 0 {
		return nil, runErr.Error()
	}
	rows := map[int][]*ordjson.Object{}
	var pid int
	hasPID := false
	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.HasPrefix(line, "p") {
			n, convErr := strconv.Atoi(line[1:])
			if convErr != nil {
				hasPID = false
				continue
			}
			pid = n
			hasPID = true
		} else if strings.HasPrefix(line, "n") && hasPID {
			address := line[1:]
			portStr := address
			if i := strings.LastIndex(address, ":"); i >= 0 {
				portStr = address[i+1:]
			}
			port, convErr := strconv.Atoi(portStr)
			if convErr != nil {
				continue
			}
			row := ordjson.NewObject()
			row.Set("pid", jsonInt(pid))
			row.Set("address", address)
			rows[port] = append(rows[port], row)
		}
	}
	if result.Code != 0 && len(rows) == 0 {
		stderr := strings.TrimSpace(result.Stderr)
		if stderr != "" {
			if len(stderr) > 200 {
				stderr = stderr[len(stderr)-200:]
			}
			return nil, fmt.Sprintf("lsof exited %d: %s", result.Code, stderr)
		}
	}
	return rows, ""
}

func processCwds(exclude map[int]bool) (map[int]string, string) {
	lsof, err := exec.LookPath("lsof")
	if err != nil {
		return nil, err.Error()
	}
	result, runErr := proc.Run([]string{lsof, "-a", "-d", "cwd", "-Fpn", "-w"}, "", lsofTimeout, false, nil)
	if runErr != nil && result.Code == 0 {
		return nil, runErr.Error()
	}
	rows := map[int]string{}
	var pid int
	hasPID := false
	self := os.Getpid()
	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.HasPrefix(line, "p") {
			n, convErr := strconv.Atoi(line[1:])
			if convErr != nil {
				hasPID = false
				continue
			}
			pid = n
			hasPID = true
		} else if strings.HasPrefix(line, "n") && hasPID && pid != self && !exclude[pid] {
			rows[pid] = line[1:]
		}
	}
	if len(rows) == 0 {
		stderr := strings.TrimSpace(result.Stderr)
		if len(stderr) > 200 {
			stderr = stderr[len(stderr)-200:]
		}
		return nil, fmt.Sprintf("lsof exited %d without a process table: %s", result.Code, stderr)
	}
	return rows, ""
}

func inside(path, root string) bool {
	roots := map[string]bool{root: true}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		roots[resolved] = true
	}
	for r := range roots {
		if path == r || strings.HasPrefix(path, r+"/") {
			return true
		}
	}
	return false
}

func taskCheckouts(s *store.Store, taskID string) ([]*ordjson.Object, error) {
	host, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	tasks, err := s.AllTasks()
	if err != nil {
		return nil, err
	}
	var rows []*ordjson.Object
	for _, other := range tasks {
		if stringField(other, "id") == taskID {
			continue
		}
		if stringField(other, "status") == "archived" {
			continue
		}
		if stringField(other, "worktree") == "" || stringField(other, "machine") != host {
			continue
		}
		rows = append(rows, other)
	}
	return rows, nil
}

type observationSnapshot struct {
	listeners    map[int][]*ordjson.Object
	listenerErr  string
	listenersSet bool
	cwds         map[int]string
	cwdErr       string
	cwdsSet      bool
}

func ObservePort(s *store.Store, task *ordjson.Object, port int, snapshot *observationSnapshot) (*ordjson.Object, error) {
	if snapshot == nil {
		snapshot = &observationSnapshot{}
	}
	if !snapshot.listenersSet {
		snapshot.listeners, snapshot.listenerErr = listeners()
		snapshot.listenersSet = true
	}
	if snapshot.listeners == nil {
		result := ordjson.NewObject()
		result.Set("state", "unverified")
		result.Set("ownership", "unknown")
		result.Set("error", snapshot.listenerErr)
		result.Set("listeners", []any{})
		return result, nil
	}
	found := snapshot.listeners[port]
	if len(found) == 0 {
		result := ordjson.NewObject()
		result.Set("state", "not-listening")
		result.Set("ownership", "unknown")
		result.Set("listeners", []any{})
		return result, nil
	}
	if !snapshot.cwdsSet {
		snapshot.cwds, snapshot.cwdErr = processCwds(nil)
		snapshot.cwdsSet = true
	}
	others, err := taskCheckouts(s, stringField(task, "id"))
	if err != nil {
		return nil, err
	}
	ownership := "unknown"
	var notes []any
	var rows []any
	for _, item := range found {
		cwd := ""
		if snapshot.cwds != nil {
			pidVal, _ := item.Get("pid")
			if n, ok := pidVal.(json.Number); ok {
				i, _ := n.Int64()
				cwd = snapshot.cwds[int(i)]
			}
		}
		row := ordjson.NewObject()
		pid, _ := item.Get("pid")
		address, _ := item.Get("address")
		row.Set("pid", pid)
		row.Set("address", address)
		row.Set("cwd", cwd)
		switch {
		case cwd != "" && inside(cwd, stringField(task, "worktree")):
			row.Set("owner", "this-task")
			if ownership == "unknown" {
				ownership = "owned"
			}
		case cwd != "":
			var other *ordjson.Object
			for _, o := range others {
				if inside(cwd, stringField(o, "worktree")) {
					other = o
					break
				}
			}
			if other != nil {
				row.Set("owner", stringField(other, "id"))
				ownership = "shared"
				notes = append(notes, fmt.Sprintf("pid %v runs inside task %s's checkout %s", pid, stringField(other, "id"), stringField(other, "worktree")))
			} else {
				row.Set("owner", "unknown")
			}
		default:
			row.Set("owner", "unknown")
			if snapshot.cwdErr != "" {
				notes = append(notes, "process cwd table unavailable: "+snapshot.cwdErr)
			}
		}
		rows = append(rows, row)
		if len(rows) >= 10 {
			break
		}
	}
	result := ordjson.NewObject()
	result.Set("state", "observed")
	result.Set("ownership", ownership)
	result.Set("listeners", rows)
	result.Set("notes", notes)
	return result, nil
}

func endpointConflicts(s *store.Store, task, parsed *ordjson.Object) ([]any, error) {
	others, err := taskCheckouts(s, stringField(task, "id"))
	if err != nil {
		return nil, err
	}
	var rows []any
	parsedURL := stringField(parsed, "url")
	parsedLocal, _ := parsed.Get("local")
	parsedPort := intField(parsed, "port")
	for _, other := range others {
		record, readErr := Read(s, stringField(other, "id"))
		if readErr != nil || record == nil {
			continue
		}
		for _, raw := range listField(record, "endpoints") {
			endpoint, _ := raw.(*ordjson.Object)
			sameURL := stringField(endpoint, "url") == parsedURL
			local, _ := endpoint.Get("local")
			samePort := parsedLocal == true && local == true && intField(endpoint, "port") == parsedPort
			state := stringField(endpoint, "state")
			if (sameURL || samePort) && stringField(endpoint, "ownership") == "owned" && (state == "observed" || state == "not-listening") {
				row := ordjson.NewObject()
				row.Set("task", stringField(other, "id"))
				row.Set("url", stringField(endpoint, "url"))
				row.Set("endpoint", stringField(endpoint, "id"))
				row.Set("worktree", stringField(other, "worktree"))
				rows = append(rows, row)
			}
		}
	}
	return rows, nil
}

func intField(o *ordjson.Object, key string) int {
	if o == nil {
		return 0
	}
	v, _ := o.Get(key)
	switch n := v.(type) {
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case int:
		return n
	}
	return 0
}

func endpointID(parsed *ordjson.Object) string {
	return "u-" + sha256Text(stringField(parsed, "url"))[:10]
}

func buildEndpoint(record, parsed, observation *ordjson.Object, claimed, label, role, stamp string, conflicts []any) (*ordjson.Object, error) {
	if len(conflicts) > 0 && claimed != "shared" {
		names := make([]string, 0, len(conflicts))
		for _, raw := range conflicts {
			c, _ := raw.(*ordjson.Object)
			names = append(names, fmt.Sprintf("%s (%s)", stringField(c, "task"), stringField(c, "url")))
		}
		return nil, fmt.Errorf("%s is recorded as owned by another active task: %s. Parallel tasks never reuse a URL by accident; use the port the task's own environment reports, or pass --ownership shared for a deliberately shared service.", stringField(parsed, "url"), strings.Join(names, ", "))
	}
	obsOwnership := stringField(observation, "ownership")
	if obsOwnership == "shared" && claimed == "owned" {
		notes := listField(observation, "notes")
		parts := make([]string, 0, len(notes))
		for _, n := range notes {
			parts = append(parts, fmt.Sprint(n))
		}
		return nil, fmt.Errorf("Observation places the listener inside another task's checkout; it cannot be recorded as owned. %s", strings.Join(parts, "; "))
	}
	ownership := obsOwnership
	if ownership == "unknown" && claimed == "shared" {
		ownership = "shared"
	}
	row := ordjson.NewObject()
	row.Set("id", endpointID(parsed))
	for _, k := range []string{"url", "scheme", "host", "port", "local"} {
		v, _ := parsed.Get(k)
		row.Set(k, v)
	}
	var labelValue any
	if label != "" {
		labelValue = label
	}
	row.Set("label", labelValue)
	row.Set("ownership", ownership)
	var claimedValue any
	if claimed != "" {
		claimedValue = claimed
	}
	row.Set("claimed_ownership", claimedValue)
	row.Set("state", stringField(observation, "state"))
	row.Set("observed_at", stamp)
	row.Set("observation", observation)
	row.Set("conflicts", conflicts)
	var configRevision any
	if discovery := objectField(record, "discovery"); discovery != nil {
		configRevision, _ = discovery.Get("config_revision")
	}
	row.Set("config_revision", configRevision)
	row.Set("recorded_by", role)
	row.Set("history", []any{})
	return row, nil
}

func upsertEndpoint(record, row *ordjson.Object) error {
	id := stringField(row, "id")
	endpoints := listField(record, "endpoints")
	for i, raw := range endpoints {
		prev, _ := raw.(*ordjson.Object)
		if stringField(prev, "id") == id {
			history := listField(prev, "history")
			if len(history) > 9 {
				history = history[len(history)-9:]
			}
			item := ordjson.NewObject()
			item.Set("at", func() any { v, _ := prev.Get("observed_at"); return v }())
			item.Set("state", stringField(prev, "state"))
			item.Set("ownership", stringField(prev, "ownership"))
			row.Set("history", append(history, item))
			endpoints[i] = row
			record.Set("endpoints", endpoints)
			return nil
		}
	}
	if len(endpoints) >= environmentLimits["endpoints"] {
		return fmt.Errorf("At most %d endpoints per task; re-record an existing URL to refresh it.", environmentLimits["endpoints"])
	}
	record.Set("endpoints", append(endpoints, row))
	return nil
}

func validateLogPath(task *ordjson.Object, text string) (*ordjson.Object, error) {
	if strings.TrimSpace(text) == "" || len(text) > 400 || strings.Contains(text, "\x00") || strings.Contains(text, "\n") {
		return nil, fmt.Errorf("--log must be one path (at most 400 characters).")
	}
	if _, n := Redact(text); n > 0 {
		return nil, fmt.Errorf("The log path contains credential-shaped text; environment records hold no secrets.")
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "~") {
		return nil, fmt.Errorf("Give the log path without `~`; it is recorded literally for other panes.")
	}
	worktree, err := requireWorktree(task)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	if strings.HasPrefix(text, "/") {
		normal := filepath.Clean(text)
		if !inside(normal, worktree) {
			result.Set("path", normal)
			result.Set("scope", "outside-checkout")
			return result, nil
		}
		relative, relErr := filepath.Rel(worktree, normal)
		if relErr != nil {
			return nil, relErr
		}
		result.Set("path", filepath.Join(worktree, relative))
		result.Set("relative", relative)
		result.Set("scope", logScope(worktree, relative))
		return result, nil
	}
	relative := filepath.Clean(text)
	if relative == "." || strings.HasPrefix(relative, "..") {
		return nil, fmt.Errorf("Relative log path %q leaves the checkout; give the absolute path of a log outside it.", text)
	}
	result.Set("path", filepath.Join(worktree, relative))
	result.Set("relative", relative)
	result.Set("scope", logScope(worktree, relative))
	return result, nil
}

func logScope(worktree, relative string) string {
	if symlinkedComponent(worktree, relative) != "" {
		return "symlink-not-followed"
	}
	return "checkout"
}

func observeLog(path, worktree, relative string) *ordjson.Object {
	result := ordjson.NewObject()
	if worktree != "" && relative != "" && symlinkedComponent(worktree, relative) != "" {
		result.Set("state", "symlink-not-followed")
		result.Set("bytes", nil)
		result.Set("scope", "symlink-not-followed")
		return result
	}
	info, err := os.Lstat(path)
	if err != nil {
		result.Set("state", "missing")
		result.Set("bytes", nil)
		if !os.IsNotExist(err) {
			result.Set("error", err.Error())
		}
		return result
	}
	if info.Mode()&os.ModeSymlink != 0 {
		result.Set("state", "symlink-not-followed")
		result.Set("bytes", nil)
		return result
	}
	if !info.Mode().IsRegular() {
		result.Set("state", "not-a-file")
		result.Set("bytes", nil)
		return result
	}
	result.Set("state", "present")
	result.Set("bytes", jsonInt(int(info.Size())))
	result.Set("modified_at", info.ModTime().UTC().Format("2006-01-02T15:04:05+00:00"))
	return result
}

func buildLog(validated, observed *ordjson.Object, claimed, label, role, stamp string) *ordjson.Object {
	row := ordjson.NewObject()
	row.Set("id", "l-"+sha256Text(stringField(validated, "path"))[:10])
	for _, k := range validated.Keys() {
		v, _ := validated.Get(k)
		row.Set(k, v)
	}
	var labelValue any
	if label != "" {
		labelValue = label
	}
	row.Set("label", labelValue)
	ownership := claimed
	if ownership == "" {
		if stringField(validated, "scope") == "checkout" {
			ownership = "owned"
		} else {
			ownership = "unknown"
		}
	}
	row.Set("ownership", ownership)
	for _, k := range observed.Keys() {
		v, _ := observed.Get(k)
		row.Set(k, v)
	}
	row.Set("observed_at", stamp)
	row.Set("recorded_by", role)
	return row
}

func upsertLog(record, row *ordjson.Object) error {
	id := stringField(row, "id")
	logs := listField(record, "logs")
	exists := false
	for i, raw := range logs {
		prev, _ := raw.(*ordjson.Object)
		if stringField(prev, "id") == id {
			logs[i] = row
			exists = true
			break
		}
	}
	if !exists && len(logs) >= environmentLimits["logs"] {
		return fmt.Errorf("At most %d log references per task.", environmentLimits["logs"])
	}
	if !exists {
		logs = append(logs, row)
	}
	record.Set("logs", logs)
	return nil
}

func observePane(task *ordjson.Object, paneID, runtimeRoot string) (*ordjson.Object, error) {
	if !paneIDPattern.MatchString(paneID) {
		return nil, fmt.Errorf("--pane must be a Herdr pane ID like w1:p2.")
	}
	row := ordjson.NewObject()
	row.Set("kind", "pane")
	row.Set("id", paneID)
	row.Set("session", func() any { v, _ := task.Get("session"); return v }())
	if paneID == stringField(task, "pane") {
		row.Set("ownership", "owned")
		row.Set("state", "observed")
		row.Set("note", "the task's own worker pane")
		return row, nil
	}
	session := stringField(task, "session")
	if session == "" {
		row.Set("ownership", "unknown")
		row.Set("state", "unverified")
		row.Set("error", "task has no recorded session")
		return row, nil
	}
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		return nil, err
	}
	info, code, obsErr := herdrclient.Observe(herdrPath, session, 5*time.Second, "pane", "get", paneID)
	if obsErr != nil {
		return nil, obsErr
	}
	if info == nil {
		row.Set("ownership", "unknown")
		row.Set("state", "unverified")
		row.Set("error", code)
		return row, nil
	}
	pane := info
	if obj, ok := info.(*ordjson.Object); ok {
		if inner, has := obj.Get("pane"); has {
			if innerObj, ok := inner.(*ordjson.Object); ok {
				pane = innerObj
			}
		}
	}
	paneObj, _ := pane.(*ordjson.Object)
	cwd := stringField(paneObj, "cwd")
	if cwd == "" {
		cwd = stringField(paneObj, "working_directory")
	}
	ownership := "unknown"
	if cwd != "" && inside(cwd, stringField(task, "worktree")) {
		ownership = "owned"
	}
	row.Set("ownership", ownership)
	row.Set("state", "observed")
	row.Set("cwd", cwd)
	if paneObj != nil {
		agent, _ := paneObj.Get("agent")
		row.Set("agent", agent)
	}
	return row, nil
}

func Record(s *store.Store, args RecordArgs, endpoint *ordjson.Object) (*ordjson.Object, error) {
	given := 0
	for _, v := range []string{args.URL, args.Log, args.Pane, args.Container} {
		if v != "" {
			given++
		}
	}
	if given != 1 {
		return nil, fmt.Errorf("Give exactly one of --url, --log, --pane, or --container per record call.")
	}
	if args.Ownership != "" && !ownershipValues[args.Ownership] {
		return nil, fmt.Errorf("--ownership must be one of [owned, shared, unknown]")
	}
	label := strings.TrimSpace(args.Label)
	if len([]rune(label)) > 80 {
		label = string([]rune(label)[:80])
	}
	if _, n := Redact(label); n > 0 {
		return nil, fmt.Errorf("The label contains credential-shaped text.")
	}
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	worktree, err := requireWorktree(task)
	if err != nil {
		return nil, err
	}
	var parsed, observation, validated, observedLog, observedPane *ordjson.Object
	if args.URL != "" {
		parsed, err = ParseEndpointURL(args.URL)
		if err != nil {
			return nil, err
		}
		if local, _ := parsed.Get("local"); local == true {
			observation, err = ObservePort(s, task, intField(parsed, "port"), nil)
			if err != nil {
				return nil, err
			}
		} else {
			observation = ordjson.NewObject()
			observation.Set("state", "unverified")
			observation.Set("ownership", "unknown")
			observation.Set("listeners", []any{})
			observation.Set("note", "remote host: sum observes only local listeners")
		}
	} else if args.Log != "" {
		validated, err = validateLogPath(task, args.Log)
		if err != nil {
			return nil, err
		}
		if args.Ownership == "owned" && stringField(validated, "scope") != "checkout" {
			return nil, fmt.Errorf("Log path %s is %s; only a regular path inside the checkout can be recorded as owned.", stringField(validated, "path"), stringField(validated, "scope"))
		}
		observedLog = observeLog(stringField(validated, "path"), worktree, stringField(validated, "relative"))
	} else if args.Pane != "" {
		observedPane, err = observePane(task, args.Pane, args.RuntimeRoot)
		if err != nil {
			return nil, err
		}
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err = s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	record, err := Ensure(s, task)
	if err != nil {
		return nil, err
	}
	role := endpointRole(task, endpoint)
	if role == "" {
		role = "unattributed"
	}
	stamp := store.Now()
	event := ordjson.NewObject()
	event.Set("event", "record")
	event.Set("by", role)
	result := ordjson.NewObject()
	result.Set("task", stringField(task, "id"))
	path, _ := Path(s, stringField(task, "id"))
	result.Set("path", path)
	switch {
	case args.URL != "":
		conflicts, confErr := endpointConflicts(s, task, parsed)
		if confErr != nil {
			return nil, confErr
		}
		row, buildErr := buildEndpoint(record, parsed, observation, args.Ownership, label, role, stamp, conflicts)
		if buildErr != nil {
			return nil, buildErr
		}
		if err := upsertEndpoint(record, row); err != nil {
			return nil, err
		}
		event.Set("kind", "url")
		event.Set("id", stringField(row, "id"))
		event.Set("state", stringField(row, "state"))
		event.Set("ownership", stringField(row, "ownership"))
		result.Set("endpoint", row)
	case args.Log != "":
		row := buildLog(validated, observedLog, args.Ownership, label, role, stamp)
		if err := upsertLog(record, row); err != nil {
			return nil, err
		}
		event.Set("kind", "log")
		event.Set("id", stringField(row, "id"))
		event.Set("state", stringField(row, "state"))
		result.Set("log", row)
	default:
		var row *ordjson.Object
		if args.Pane != "" {
			row = observedPane
			if args.Ownership == "owned" && stringField(row, "ownership") != "owned" {
				return nil, fmt.Errorf("Pane %s is not the task's pane and its cwd is not inside the checkout; it cannot be recorded as owned.", args.Pane)
			}
			if args.Ownership == "shared" && stringField(row, "ownership") == "unknown" {
				row.Set("ownership", "shared")
			}
		} else {
			if !containerID.MatchString(args.Container) {
				return nil, fmt.Errorf("--container must be a container name or ID (letters, digits, `_ . -`, at most 80 characters).")
			}
			row = ordjson.NewObject()
			row.Set("kind", "container")
			row.Set("id", args.Container)
			ownership := args.Ownership
			if ownership == "" {
				ownership = "unknown"
			}
			row.Set("ownership", ownership)
			row.Set("state", "unverified")
			row.Set("note", "Container identity recorded as reported; sum does not inspect or control container runtimes in this slice.")
		}
		var labelValue any
		if label != "" {
			labelValue = label
		}
		row.Set("label", labelValue)
		row.Set("observed_at", stamp)
		row.Set("recorded_by", role)
		var claimed any
		if args.Ownership != "" {
			claimed = args.Ownership
		}
		row.Set("claimed_ownership", claimed)
		kind := stringField(row, "kind")
		id := stringField(row, "id")
		resources := listField(record, "resources")
		exists := false
		for i, raw := range resources {
			prev, _ := raw.(*ordjson.Object)
			if stringField(prev, "kind") == kind && stringField(prev, "id") == id {
				resources[i] = row
				exists = true
				break
			}
		}
		if !exists && len(resources) >= environmentLimits["resources"] {
			return nil, fmt.Errorf("At most %d pane/container references per task.", environmentLimits["resources"])
		}
		if !exists {
			resources = append(resources, row)
		}
		record.Set("resources", resources)
		event.Set("kind", kind)
		event.Set("id", id)
		event.Set("ownership", stringField(row, "ownership"))
		result.Set("resource", row)
	}
	if err := Write(s, record, event); err != nil {
		return nil, err
	}
	result.Set("by", role)
	result.Set("note", Note)
	return result, nil
}

func Inspect(s *store.Store, args InspectArgs, endpoint *ordjson.Object) (*ordjson.Object, error) {
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	worktree, err := requireWorktree(task)
	if err != nil {
		return nil, err
	}
	snapshotRecord, err := Read(s, args.Task)
	if err != nil {
		return nil, err
	}
	if snapshotRecord == nil {
		return nil, fmt.Errorf("Task %s has no environment record yet; run `env discover` or `env record` first.", args.Task)
	}
	var current *ordjson.Object
	if objectField(snapshotRecord, "discovery") != nil {
		if info, statErr := os.Stat(worktree); statErr == nil && info.IsDir() {
			current, err = DiscoverConfiguration(worktree)
			if err != nil {
				return nil, err
			}
		}
	}
	snapshot := &observationSnapshot{}
	portObservations := map[string]*ordjson.Object{}
	for _, raw := range listField(snapshotRecord, "endpoints") {
		row, _ := raw.(*ordjson.Object)
		if local, _ := row.Get("local"); local == true {
			obs, obsErr := ObservePort(s, task, intField(row, "port"), snapshot)
			if obsErr != nil {
				return nil, obsErr
			}
			portObservations[stringField(row, "id")] = obs
		}
	}
	logObservations := map[string]*ordjson.Object{}
	for _, raw := range listField(snapshotRecord, "logs") {
		row, _ := raw.(*ordjson.Object)
		logObservations[stringField(row, "id")] = observeLog(stringField(row, "path"), worktree, stringField(row, "relative"))
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err = s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	record, err := Read(s, args.Task)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("Task %s has no environment record yet; run `env discover` or `env record` first.", args.Task)
	}
	role := endpointRole(task, endpoint)
	if role == "" {
		role = "unattributed"
	}
	stamp := store.Now()
	changes := ordjson.NewObject()
	changes.Set("config_drift", false)
	changes.Set("endpoints", []any{})
	changes.Set("logs", []any{})
	discovery := objectField(record, "discovery")
	if discovery != nil && current != nil {
		discovery.Set("current_revision", stringField(current, "config_revision"))
		stale := stringField(current, "config_revision") != stringField(discovery, "config_revision")
		discovery.Set("stale", stale)
		discovery.Set("checked_at", stamp)
		changes.Set("config_drift", stale)
		if stale {
			discovery.Set("stale_reason", fmt.Sprintf("configuration revision %s recorded, %s now; run `env discover` to refresh the command references", stringField(discovery, "config_revision"), stringField(current, "config_revision")))
		}
	} else if discovery != nil {
		discovery.Set("stale", true)
		discovery.Set("checked_at", stamp)
		discovery.Set("stale_reason", fmt.Sprintf("recorded worktree %s is missing", stringField(task, "worktree")))
		changes.Set("config_drift", true)
	}
	var endpointChanges []any
	for _, raw := range listField(record, "endpoints") {
		row, _ := raw.(*ordjson.Object)
		if local, _ := row.Get("local"); local == true {
			if _, ok := portObservations[stringField(row, "id")]; !ok {
				continue
			}
		}
		beforeState := stringField(row, "state")
		beforeOwnership := stringField(row, "ownership")
		historyItem := ordjson.NewObject()
		historyItem.Set("at", func() any { v, _ := row.Get("observed_at"); return v }())
		historyItem.Set("state", beforeState)
		historyItem.Set("ownership", beforeOwnership)
		if local, _ := row.Get("local"); local == true {
			observation := portObservations[stringField(row, "id")]
			previousPids := pidSet(objectField(row, "observation"))
			currentPids := pidSet(observation)
			obsState := stringField(observation, "state")
			switch {
			case obsState == "not-listening" && (beforeState == "observed" || beforeState == "stale"):
				row.Set("state", "stale")
				row.Set("stale_reason", fmt.Sprintf("nothing listens on port %d any more", intField(row, "port")))
			case obsState == "observed" && len(previousPids) > 0 && !sameIntSet(previousPids, currentPids):
				row.Set("state", "stale")
				row.Set("stale_reason", fmt.Sprintf("listener changed from pid(s) %v to %v", sortedInts(previousPids), sortedInts(currentPids)))
			case obsState == "unverified":
				row.Set("state", "unverified")
				row.Set("stale_reason", func() any { v, _ := observation.Get("error"); return v }())
			default:
				row.Set("state", obsState)
				row.Delete("stale_reason")
			}
			if obsState == "observed" {
				obsOwnership := stringField(observation, "ownership")
				if obsOwnership != "unknown" || stringField(row, "claimed_ownership") != "shared" {
					row.Set("ownership", obsOwnership)
				} else {
					row.Set("ownership", "shared")
				}
			}
			row.Set("observation", observation)
		} else {
			row.Set("state", "unverified")
		}
		if discovery != nil && stringField(row, "config_revision") != "" {
			currentRev := stringField(discovery, "current_revision")
			if currentRev == "" {
				currentRev = stringField(discovery, "config_revision")
			}
			if stringField(row, "config_revision") != currentRev {
				row.Set("config_stale", true)
			} else {
				row.Delete("config_stale")
			}
		} else {
			row.Delete("config_stale")
		}
		row.Set("observed_at", stamp)
		history := listField(row, "history")
		if len(history) > 9 {
			history = history[len(history)-9:]
		}
		row.Set("history", append(history, historyItem))
		configStale, _ := row.Get("config_stale")
		if stringField(row, "state") != beforeState || stringField(row, "ownership") != beforeOwnership || truthy(configStale) {
			change := ordjson.NewObject()
			change.Set("id", stringField(row, "id"))
			change.Set("url", stringField(row, "url"))
			change.Set("from", []any{beforeState, beforeOwnership})
			change.Set("to", []any{stringField(row, "state"), stringField(row, "ownership")})
			change.Set("config_stale", truthy(configStale))
			endpointChanges = append(endpointChanges, change)
		}
	}
	changes.Set("endpoints", endpointChanges)
	var logChanges []any
	for _, raw := range listField(record, "logs") {
		row, _ := raw.(*ordjson.Object)
		obs, ok := logObservations[stringField(row, "id")]
		if !ok {
			continue
		}
		before := stringField(row, "state")
		for _, k := range obs.Keys() {
			v, _ := obs.Get(k)
			row.Set(k, v)
		}
		row.Set("observed_at", stamp)
		if stringField(row, "state") != before {
			change := ordjson.NewObject()
			change.Set("id", stringField(row, "id"))
			change.Set("path", stringField(row, "path"))
			change.Set("from", before)
			change.Set("to", stringField(row, "state"))
			logChanges = append(logChanges, change)
		}
	}
	changes.Set("logs", logChanges)
	event := ordjson.NewObject()
	event.Set("event", "inspect")
	event.Set("by", role)
	event.Set("config_drift", func() any { v, _ := changes.Get("config_drift"); return v }())
	event.Set("endpoints", jsonInt(len(endpointChanges)))
	event.Set("logs", jsonInt(len(logChanges)))
	if err := Write(s, record, event); err != nil {
		return nil, err
	}
	path, _ := Path(s, args.Task)
	var discoveryView any
	if discovery != nil {
		discoveryView = pick(discovery, []string{"config_revision", "current_revision", "stale", "stale_reason"})
	}
	var endpointViews []any
	for _, raw := range listField(record, "endpoints") {
		e, _ := raw.(*ordjson.Object)
		endpointViews = append(endpointViews, pick(e, []string{"id", "url", "state", "ownership", "stale_reason", "config_stale"}))
	}
	var logViews []any
	for _, raw := range listField(record, "logs") {
		l, _ := raw.(*ordjson.Object)
		logViews = append(logViews, pick(l, []string{"id", "path", "state", "bytes"}))
	}
	result := ordjson.NewObject()
	result.Set("task", args.Task)
	result.Set("path", path)
	result.Set("inspected_at", stamp)
	result.Set("changes", changes)
	result.Set("discovery", discoveryView)
	result.Set("endpoints", endpointViews)
	result.Set("logs", logViews)
	result.Set("by", role)
	result.Set("touched", "nothing was started, stopped, or reconfigured")
	result.Set("note", Note)
	return result, nil
}

func pidSet(observation *ordjson.Object) map[int]bool {
	set := map[int]bool{}
	if observation == nil {
		return set
	}
	for _, raw := range listField(observation, "listeners") {
		l, _ := raw.(*ordjson.Object)
		if n := intField(l, "pid"); n != 0 {
			set[n] = true
		}
	}
	return set
}

func sameIntSet(a, b map[int]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func sortedInts(set map[int]bool) []int {
	out := make([]int, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

