package environment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

const (
	configMaxBytes = 256 * 1024
	historyLimit   = 30
	serviceLimit   = 20
)

var (
	environmentLimits = map[string]int{"commands": 200, "endpoints": 40, "logs": 40, "resources": 40, "sources": 40}
	ownershipValues   = map[string]bool{"owned": true, "shared": true, "unknown": true}
	localHosts        = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true, "0.0.0.0": true, "[::1]": true, "[::]": true, "::": true}
	defaultPorts      = map[string]int{"http": 80, "https": 443, "ws": 80, "wss": 443, "postgres": 5432, "postgresql": 5432, "mysql": 3306, "redis": 6379, "amqp": 5672, "mongodb": 27017}
	configFiles       = []string{"mise.toml", ".mise.toml", ".mise/config.toml", "package.json", "Makefile", "justfile", "Justfile", "Procfile",
		"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml", "Dockerfile", ".devcontainer/devcontainer.json",
		".devcontainer.json", "pyproject.toml"}
	verificationNames = regexp.MustCompile(`(?i)^(test|tests|lint|check|verify|ci|typecheck|fmt-check|format-check|e2e|smoke|coverage)(?:[:_-].*)?$`)
	serviceNames      = regexp.MustCompile(`(?i)^(dev|serve|start|run|up|watch|preview|server)(?:[:_-].*)?$`)
	urlUserinfo       = regexp.MustCompile(`(://)[^/\s@:]+:[^/\s@]*@`)
	makeTarget = regexp.MustCompile(`(?m)^([A-Za-z0-9][A-Za-z0-9_./-]*)\s*:`)
	justTarget = regexp.MustCompile(`(?m)^(?:@)?([a-zA-Z_][A-Za-z0-9_-]*)(?:\s+[^:\n]*)?:\s*(?:[^\n]*)?$`)
	procfileLine      = regexp.MustCompile(`(?m)^([A-Za-z0-9_-]+):\s*(.+)$`)
	portToken         = regexp.MustCompile(`["']?([0-9.:\[\]a-fA-F-]+(?:/(?:tcp|udp))?)["']?`)
	exposeLine        = regexp.MustCompile(`(?im)^\s*EXPOSE\s+(.+)$`)
	commentLine       = regexp.MustCompile(`(?m)^\s*//.*$`)
	urlScheme         = regexp.MustCompile(`(?i)^([a-z][a-z0-9+.-]*)://([^/?#]*)(.*)$`)
	hostPort          = regexp.MustCompile(`^(\[[0-9a-fA-F:.]+\]|[^:]+)(?::(\d{1,5}))?$`)
	containerID       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,79}$`)
	paneIDPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}:[A-Za-z0-9_-]{1,32}$`)
)

func emptyEnvironment(taskID string) *ordjson.Object {
	record := ordjson.NewObject()
	record.Set("schema", jsonInt(Schema))
	record.Set("task", taskID)
	record.Set("discovery", nil)
	record.Set("endpoints", []any{})
	record.Set("logs", []any{})
	record.Set("resources", []any{})
	record.Set("services", []any{})
	record.Set("history", []any{})
	record.Set("created_at", store.Now())
	record.Set("updated_at", nil)
	return record
}

func Write(s *store.Store, record *ordjson.Object, event *ordjson.Object) error {
	stamp := store.Now()
	record.Set("updated_at", stamp)
	event.Set("at", stamp)
	history := listField(record, "history")
	if len(history) > historyLimit-1 {
		history = history[len(history)-(historyLimit-1):]
	}
	record.Set("history", append(history, event))
	path, err := Path(s, stringField(record, "task"))
	if err != nil {
		return err
	}
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; refusing to write through it.", path)
	}
	return ordjson.WriteFile(path, record)
}

func Ensure(s *store.Store, task *ordjson.Object) (*ordjson.Object, error) {
	taskID := stringField(task, "id")
	record, err := Read(s, taskID)
	if err != nil {
		return nil, err
	}
	if record == nil {
		record = emptyEnvironment(taskID)
	}
	if _, has := record.Get("services"); !has {
		record.Set("services", []any{})
	}
	return record, nil
}

func requireWorktree(task *ordjson.Object) (string, error) {
	worktree := stringField(task, "worktree")
	if worktree == "" {
		return "", fmt.Errorf("Task %s has no recorded worktree; environment facts are recorded against a checkout.", stringField(task, "id"))
	}
	return worktree, nil
}

func endpointRole(task, endpoint *ordjson.Object) string {
	if endpoint == nil {
		return ""
	}
	if stringField(task, "pane") != "" && identitiesEqual(task, endpoint) {
		return "worker"
	}
	if identitiesEqual(objectField(task, "parent"), endpoint) {
		return "coordinator"
	}
	if identitiesEqual(objectField(task, "reviewer"), endpoint) {
		return "reviewer"
	}
	return "other"
}

func identitiesEqual(a, b *ordjson.Object) bool {
	if a == nil || b == nil {
		return false
	}
	return stringField(a, "machine") == stringField(b, "machine") &&
		stringField(a, "session") == stringField(b, "session") &&
		stringField(a, "pane") == stringField(b, "pane")
}

func sha256Text(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func classifyCommand(name, kind string) string {
	if kind != "" {
		return kind
	}
	if verificationNames.MatchString(name) {
		return "verification"
	}
	if serviceNames.MatchString(name) {
		return "service"
	}
	return "task"
}

func redactReference(value any) (any, int) {
	switch v := value.(type) {
	case string:
		text, count := Redact(v)
		replaced := urlUserinfo.ReplaceAllString(text, "${1}[redacted]@")
		n := len(urlUserinfo.FindAllString(text, -1))
		return replaced, count + n
	case []any:
		out := make([]any, len(v))
		total := 0
		for i, item := range v {
			redacted, n := redactReference(item)
			out[i] = redacted
			total += n
		}
		return out, total
	case *ordjson.Object:
		if v == nil {
			return v, 0
		}
		total := 0
		for _, k := range v.Keys() {
			raw, _ := v.Get(k)
			redacted, n := redactReference(raw)
			v.Set(k, redacted)
			total += n
		}
		return v, total
	default:
		return value, 0
	}
}

func commandRow(source, name, command, kind string, extra *ordjson.Object) *ordjson.Object {
	text, redactions := redactReference(command)
	textStr, _ := text.(string)
	if len([]rune(textStr)) > 400 {
		textStr = string([]rune(textStr)[:400])
	}
	nameRunes := []rune(name)
	if len(nameRunes) > 80 {
		name = string(nameRunes[:80])
	}
	row := ordjson.NewObject()
	row.Set("source", source)
	row.Set("name", name)
	row.Set("command", textStr)
	row.Set("kind", classifyCommand(name, kind))
	if extra != nil {
		for _, key := range extra.Keys() {
			value, _ := extra.Get(key)
			if value == nil {
				continue
			}
			if list, ok := value.([]any); ok && len(list) == 0 {
				continue
			}
			if s, ok := value.(string); ok && s == "" {
				continue
			}
			redacted, n := redactReference(value)
			redactions += n
			row.Set(key, redacted)
		}
	}
	row.Set("redactions", jsonInt(redactions))
	return row
}

func checkoutFile(worktree, relative string) (string, *ordjson.Object) {
	info := ordjson.NewObject()
	info.Set("path", relative)
	if link := symlinkedComponent(worktree, relative); link != "" {
		info.Set("skipped", fmt.Sprintf("symlink not followed (%s)", link))
		return "", info
	}
	path := filepath.Join(worktree, filepath.FromSlash(relative))
	stat, err := os.Stat(path)
	if err != nil || stat.IsDir() {
		return "", nil
	}
	if stat.Size() > configMaxBytes {
		info.Set("bytes", jsonInt(int(stat.Size())))
		info.Set("skipped", fmt.Sprintf("larger than %d bytes", configMaxBytes))
		return "", info
	}
	data, err := os.ReadFile(path)
	if err != nil {
		info.Set("skipped", fmt.Sprintf("unreadable: %s", err))
		return "", info
	}
	text := string(data)
	info.Set("bytes", jsonInt(len(data)))
	info.Set("sha256", sha256Text(text)[:16])
	return text, info
}

func symlinkedComponent(worktree, relative string) string {
	current := worktree
	parts := strings.Split(filepath.ToSlash(relative), "/")
	for i, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return ""
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return strings.Join(parts[:i+1], "/")
		}
	}
	return ""
}

func checkoutHead(worktree string) any {
	result, err := proc.Run([]string{"git", "-C", worktree, "rev-parse", "HEAD"}, "", 20*time.Second, false, nil)
	if err != nil || result.Code != 0 {
		return nil
	}
	head := strings.TrimSpace(result.Stdout)
	if head == "" {
		return nil
	}
	return head
}

func DiscoverConfiguration(worktree string) (*ordjson.Object, error) {
	info, err := os.Stat(worktree)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("Recorded worktree %s is not a directory; nothing was discovered.", worktree)
	}
	var sources []any
	var commands []any
	var problems []any
	for _, relative := range configFiles {
		text, fileInfo := checkoutFile(worktree, relative)
		if fileInfo == nil {
			continue
		}
		sources = append(sources, fileInfo)
		if text == "" {
			continue
		}
		rows, errors := discoverFile(relative, text)
		for _, row := range rows {
			commands = append(commands, row)
		}
		for _, e := range errors {
			problems = append(problems, e)
		}
	}
	if len(commands) > environmentLimits["commands"] {
		commands = commands[:environmentLimits["commands"]]
	}
	origins := miseTaskOrigins(worktree)
	if problem, _ := origins.Get("problem"); problem != nil {
		problems = append(problems, problem)
	}
	contract := verificationContractStatus(worktree, origins)
	head := checkoutHead(worktree)
	sourcePairs := make([]any, 0, len(sources))
	for _, raw := range sources {
		src, _ := raw.(*ordjson.Object)
		pair := []any{stringField(src, "path"), func() any { v, _ := src.Get("sha256"); return v }()}
		sourcePairs = append(sourcePairs, pair)
	}
	payload := ordjson.NewObject()
	payload.Set("head", head)
	payload.Set("sources", sourcePairs)
	encoded, _ := ordjson.MarshalSortedCompact(payload)
	revision := sha256Text(string(encoded))[:16]
	if len(sources) > environmentLimits["sources"] {
		sources = sources[:environmentLimits["sources"]]
	}
	summary := ordjson.NewObject()
	for _, kind := range []string{"verification", "service", "container", "task"} {
		count := 0
		for _, raw := range commands {
			c, _ := raw.(*ordjson.Object)
			if stringField(c, "kind") == kind {
				count++
			}
		}
		summary.Set(kind, jsonInt(count))
	}
	result := ordjson.NewObject()
	result.Set("observed_at", store.Now())
	result.Set("worktree", worktree)
	result.Set("head", head)
	result.Set("config_revision", revision)
	result.Set("sources", sources)
	result.Set("commands", commands)
	result.Set("problems", problems)
	result.Set("task_origins", origins)
	result.Set("verification_contract", contract)
	result.Set("stale", false)
	result.Set("current_revision", revision)
	result.Set("summary", summary)
	result.Set("note", "Declared by the repository; classification by name. No command here was run, and absence of a `service` entry means none was declared, not that nothing runs.")
	return result, nil
}

func discoverFile(relative, text string) ([]*ordjson.Object, []any) {
	switch relative {
	case "mise.toml", ".mise.toml", ".mise/config.toml":
		return discoverMise(relative, text)
	case "package.json":
		return discoverPackage(relative, text)
	case "Makefile":
		return discoverMake(relative, text)
	case "justfile", "Justfile":
		return discoverJust(relative, text)
	case "Procfile":
		return discoverProcfile(relative, text)
	case "compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml":
		return discoverCompose(relative, text)
	case "Dockerfile":
		return discoverDockerfile(relative, text)
	case ".devcontainer/devcontainer.json", ".devcontainer.json":
		return discoverDevcontainer(relative, text)
	case "pyproject.toml":
		return discoverPyproject(relative, text)
	default:
		return nil, nil
	}
}

func discoverMise(relative, text string) ([]*ordjson.Object, []any) {
	var data map[string]any
	if err := toml.Unmarshal([]byte(text), &data); err != nil {
		return nil, []any{fmt.Sprintf("%s: %s", relative, err)}
	}
	tasks, _ := data["tasks"].(map[string]any)
	var rows []*ordjson.Object
	for name, spec := range tasks {
		switch v := spec.(type) {
		case string:
			rows = append(rows, commandRow(relative, name, v, "", nil))
		case map[string]any:
			command := ""
			switch run := v["run"].(type) {
			case string:
				command = run
			case []any:
				parts := make([]string, 0, len(run))
				for _, item := range run {
					parts = append(parts, fmt.Sprint(item))
				}
				command = strings.Join(parts, "\n")
			}
			if command == "" {
				if file, ok := v["file"].(string); ok && file != "" {
					command = "file: " + file
				}
			}
			extra := ordjson.NewObject()
			if desc, ok := v["description"].(string); ok {
				extra.Set("description", desc)
			}
			if depends := v["depends"]; depends != nil {
				extra.Set("depends", depends)
			}
			rows = append(rows, commandRow(relative, name, command, "", extra))
		}
	}
	return rows, nil
}

func discoverPackage(relative, text string) ([]*ordjson.Object, []any) {
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		return nil, []any{fmt.Sprintf("%s: %s", relative, err)}
	}
	scripts, _ := data["scripts"].(map[string]any)
	var rows []*ordjson.Object
	for name, command := range scripts {
		if s, ok := command.(string); ok {
			rows = append(rows, commandRow(relative, name, s, "", nil))
		}
	}
	return rows, nil
}

func discoverMake(relative, text string) ([]*ordjson.Object, []any) {
	seen := map[string]bool{}
	var rows []*ordjson.Object
	for _, match := range makeTarget.FindAllStringSubmatchIndex(text, -1) {
		name := text[match[2]:match[3]]
		if match[1] < len(text) && text[match[1]] == '=' {
			continue
		}
		if strings.HasPrefix(name, ".") || seen[name] {
			continue
		}
		seen[name] = true
		rows = append(rows, commandRow(relative, name, "make "+name, "", nil))
	}
	return rows, nil
}

func discoverJust(relative, text string) ([]*ordjson.Object, []any) {
	seen := map[string]bool{}
	var rows []*ordjson.Object
	for _, match := range justTarget.FindAllStringSubmatch(text, -1) {
		name := match[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		rows = append(rows, commandRow(relative, name, "just "+name, "", nil))
	}
	return rows, nil
}

func discoverProcfile(relative, text string) ([]*ordjson.Object, []any) {
	var rows []*ordjson.Object
	for _, match := range procfileLine.FindAllStringSubmatch(text, -1) {
		rows = append(rows, commandRow(relative, match[1], match[2], "service", nil))
	}
	return rows, nil
}

func declaredPorts(text string) []any {
	var ports []any
	seen := map[int]bool{}
	for _, match := range portToken.FindAllStringSubmatch(text, -1) {
		token := match[1]
		hostPortParts := strings.Split(strings.Split(token, "/")[0], ":")
		candidate := hostPortParts[0]
		if len(hostPortParts) >= 2 {
			candidate = hostPortParts[len(hostPortParts)-2]
		}
		candidate = strings.Split(candidate, "-")[0]
		n := 0
		if _, err := fmt.Sscanf(candidate, "%d", &n); err != nil {
			continue
		}
		if n > 0 && n < 65536 && !seen[n] {
			seen[n] = true
			ports = append(ports, jsonInt(n))
		}
	}
	return ports
}

func discoverCompose(relative, text string) ([]*ordjson.Object, []any) {
	type spec struct {
		image string
		ports []any
		build bool
	}
	services := map[string]*spec{}
	var order []string
	inServices, inPorts := false, false
	current := ""
	for _, raw := range strings.Split(text, "\n") {
		line := raw
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		stripped := strings.TrimSpace(line)
		if indent == 0 {
			inServices = stripped == "services:"
			current = ""
			continue
		}
		if !inServices {
			continue
		}
		if indent == 2 && strings.HasSuffix(stripped, ":") && !strings.HasPrefix(stripped, "-") {
			current = strings.Trim(strings.TrimSuffix(stripped, ":"), `"'`)
			if _, ok := services[current]; !ok {
				order = append(order, current)
			}
			services[current] = &spec{}
			inPorts = false
		} else if current != "" && indent >= 4 {
			if indent == 4 && strings.HasPrefix(stripped, "image:") {
				services[current].image = strings.Trim(strings.TrimSpace(strings.TrimPrefix(stripped, "image:")), `"'`)
				if len(services[current].image) > 120 {
					services[current].image = services[current].image[:120]
				}
				inPorts = false
			} else if indent == 4 && strings.HasPrefix(stripped, "build") {
				services[current].build = true
				inPorts = false
			} else if indent == 4 && strings.HasPrefix(stripped, "ports:") {
				inPorts = true
				inline := strings.TrimSpace(strings.TrimPrefix(stripped, "ports:"))
				if strings.HasPrefix(inline, "[") {
					services[current].ports = append(services[current].ports, declaredPorts(inline)...)
					inPorts = false
				}
			} else if indent == 4 {
				inPorts = false
			} else if inPorts && strings.HasPrefix(stripped, "-") {
				services[current].ports = append(services[current].ports, declaredPorts(stripped[1:])...)
			}
		}
	}
	var rows []*ordjson.Object
	for _, name := range order {
		spec := services[name]
		extra := ordjson.NewObject()
		if spec.image != "" {
			extra.Set("image", spec.image)
		}
		if len(spec.ports) > 0 {
			extra.Set("declared_ports", spec.ports)
		}
		if spec.build {
			extra.Set("build", true)
		}
		rows = append(rows, commandRow(relative, name, "docker compose up "+name, "service", extra))
	}
	return rows, nil
}

func discoverDockerfile(relative, text string) ([]*ordjson.Object, []any) {
	var ports []any
	seen := map[int]bool{}
	for _, match := range exposeLine.FindAllStringSubmatch(text, -1) {
		for _, p := range declaredPorts(match[1]) {
			n, _ := p.(json.Number)
			i, _ := n.Int64()
			if !seen[int(i)] {
				seen[int(i)] = true
				ports = append(ports, p)
			}
		}
	}
	extra := ordjson.NewObject()
	if len(ports) > 0 {
		extra.Set("declared_ports", ports)
	}
	return []*ordjson.Object{commandRow(relative, "image", "docker build -f "+relative+" .", "container", extra)}, nil
}

func discoverDevcontainer(relative, text string) ([]*ordjson.Object, []any) {
	cleaned := commentLine.ReplaceAllString(text, "")
	var data map[string]any
	if err := json.Unmarshal([]byte(cleaned), &data); err != nil {
		row := commandRow(relative, "devcontainer", "devcontainer (declared; configuration not parsed)", "container", nil)
		return []*ordjson.Object{row}, []any{fmt.Sprintf("%s: JSON with comments not parsed", relative)}
	}
	var ports []any
	if list, ok := data["forwardPorts"].([]any); ok {
		for _, p := range list {
			switch n := p.(type) {
			case float64:
				ports = append(ports, jsonInt(int(n)))
			case json.Number:
				i, _ := n.Int64()
				ports = append(ports, jsonInt(int(i)))
			}
		}
	}
	var image any
	if v, ok := data["image"]; ok {
		image = v
	} else if build, ok := data["build"].(map[string]any); ok {
		image = build["dockerfile"]
	}
	extra := ordjson.NewObject()
	if image != nil {
		extra.Set("image", image)
	}
	if len(ports) > 0 {
		extra.Set("declared_ports", ports)
	}
	if post, ok := data["postCreateCommand"].(string); ok {
		extra.Set("post_create", post)
	}
	return []*ordjson.Object{commandRow(relative, "devcontainer", "devcontainer up", "container", extra)}, nil
}

func discoverPyproject(relative, text string) ([]*ordjson.Object, []any) {
	var data map[string]any
	if err := toml.Unmarshal([]byte(text), &data); err != nil {
		return nil, []any{fmt.Sprintf("%s: %s", relative, err)}
	}
	var rows []*ordjson.Object
	tool, _ := data["tool"].(map[string]any)
	if _, ok := tool["pytest"]; ok {
		extra := ordjson.NewObject()
		extra.Set("declared", "[tool.pytest]")
		rows = append(rows, commandRow(relative, "pytest", "pytest", "verification", extra))
	}
	project, _ := data["project"].(map[string]any)
	scripts, _ := project["scripts"].(map[string]any)
	for name, spec := range scripts {
		rows = append(rows, commandRow(relative, name, fmt.Sprint(spec), "task", nil))
	}
	return rows, nil
}

func miseTaskOrigins(worktree string) *ordjson.Object {
	result := ordjson.NewObject()
	result.Set("available", false)
	result.Set("tasks", []any{})
	result.Set("inherited", []any{})
	result.Set("verification", nil)
	binary, err := toolpath.Find(worktree, "mise")
	if err != nil {
		result.Set("error", err.Error())
		return result
	}
	info, statErr := os.Stat(binary)
	if statErr != nil || info.Mode()&0o111 == 0 {
		result.Set("error", fmt.Sprintf("%s is not executable", binary))
		return result
	}
	result.Set("available", true)
	env := append([]string{}, os.Environ()...)
	env = append(env, "MISE_QUIET=1")
	run, runErr := proc.Run([]string{binary, "tasks", "ls", "--json"}, worktree, 30*time.Second, false, env)
	warnings := strings.TrimSpace(run.Stderr)
	if len(warnings) > 400 {
		warnings = warnings[len(warnings)-400:]
	}
	if runErr != nil && strings.TrimSpace(run.Stdout) == "" {
		result.Set("error", runErr.Error())
		result.Set("tasks", []any{})
		result.Set("inherited", []any{})
		return result
	}
	if run.Code != 0 && strings.TrimSpace(run.Stdout) == "" {
		suffix := warnings
		if len(suffix) > 200 {
			suffix = suffix[len(suffix)-200:]
		}
		result.Set("error", fmt.Sprintf("mise exited %d: %s", run.Code, suffix))
		return result
	}
	raw := strings.TrimSpace(run.Stdout)
	if raw == "" {
		raw = "[]"
	}
	decoded, decodeErr := ordjson.Decode([]byte(raw))
	if decodeErr != nil {
		suffix := warnings
		if len(suffix) > 200 {
			suffix = suffix[len(suffix)-200:]
		}
		result.Set("error", fmt.Sprintf("mise tasks ls exited %d without JSON: %s", run.Code, suffix))
		return result
	}
	list, _ := decoded.([]any)
	root, _ := filepath.EvalSymlinks(worktree)
	if root == "" {
		root, _ = filepath.Abs(worktree)
	}
	var tasks []any
	var inherited []any
	ownedNames := map[string]bool{}
	for _, rawRow := range list {
		row, _ := rawRow.(*ordjson.Object)
		if row == nil {
			continue
		}
		name := stringField(row, "name")
		if name == "" {
			continue
		}
		if len([]rune(name)) > 80 {
			name = string([]rune(name)[:80])
		}
		source := stringField(row, "source")
		if source == "" {
			source = stringField(row, "file")
		}
		resolvedSource, _ := filepath.EvalSymlinks(source)
		if resolvedSource == "" {
			resolvedSource = source
		}
		owned := source != "" && (resolvedSource == root || strings.HasPrefix(resolvedSource, root+string(os.PathSeparator)))
		redactedSource, _ := redactReference(source)
		sourceStr, _ := redactedSource.(string)
		if len(sourceStr) > 300 {
			sourceStr = sourceStr[:300]
		}
		entry := ordjson.NewObject()
		entry.Set("name", name)
		entry.Set("source", sourceStr)
		entry.Set("owned", owned)
		tasks = append(tasks, entry)
		if owned {
			ownedNames[name] = true
		} else {
			inherited = append(inherited, entry)
		}
	}
	if len(tasks) > environmentLimits["commands"] {
		tasks = tasks[:environmentLimits["commands"]]
	}
	if len(inherited) > environmentLimits["commands"] {
		inherited = inherited[:environmentLimits["commands"]]
	}
	var inheritedVerification []any
	for _, raw := range inherited {
		entry, _ := raw.(*ordjson.Object)
		name := stringField(entry, "name")
		if name == "verify" || name == "test" {
			inheritedVerification = append(inheritedVerification, name)
		}
	}
	verification := ordjson.NewObject()
	verification.Set("verify", ownedNames["verify"])
	verification.Set("test", ownedNames["test"])
	verification.Set("inherited_verification", inheritedVerification)
	result.Set("exit", jsonInt(run.Code))
	result.Set("tasks", tasks)
	result.Set("inherited", inherited)
	result.Set("verification", verification)
	if warnings == "" {
		result.Set("warnings", nil)
	} else {
		result.Set("warnings", warnings)
	}
	if len(inherited) > 0 {
		names := make([]string, 0, len(inherited))
		for i, raw := range inherited {
			if i >= 6 {
				break
			}
			entry, _ := raw.(*ordjson.Object)
			names = append(names, stringField(entry, "name"))
		}
		suffix := ""
		if len(inherited) > 6 {
			suffix = ", ..."
		}
		result.Set("problem", fmt.Sprintf("mise resolves %d task(s) from outside this checkout (%s%s): `mise run NAME` here would execute another repository's task. Report only tasks this checkout defines as its own verification.", len(inherited), strings.Join(names, ", "), suffix))
	}
	return result
}

func verificationContractStatus(worktree string, origins *ordjson.Object) *ordjson.Object {
	present := false
	if info, err := os.Stat(filepath.Join(worktree, "VERIFY.md")); err == nil && info.Mode().IsRegular() {
		present = true
	}
	verification := objectField(origins, "verification")
	ownedVerify := false
	if verification != nil {
		v, _ := verification.Get("verify")
		ownedVerify, _ = v.(bool)
	}
	status, why := "not-yet-standardized", "no VERIFY.md at the checkout root; the project keeps its current verification path"
	if present && ownedVerify {
		status, why = "standardized", "VERIFY.md at the root and a `verify` task this checkout defines"
	} else if present {
		status, why = "not-yet-standardized", "VERIFY.md exists but mise resolves no `verify` task owned by this checkout"
	}
	var runner any
	if info, err := os.Stat(filepath.Join(worktree, ".agents", "skills", "verify", "scripts", "verify_run.py")); err == nil && info.Mode().IsRegular() {
		runner = ".agents/skills/verify/scripts/verify_run.py"
	}
	result := ordjson.NewObject()
	result.Set("status", status)
	result.Set("verify_md", present)
	result.Set("verify_task_owned", ownedVerify)
	result.Set("why", why)
	result.Set("runner", runner)
	return result
}
