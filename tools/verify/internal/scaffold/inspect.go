package scaffold

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"

	"github.com/douglasjarquin/sum/tools/verify/internal/contract"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
	"github.com/douglasjarquin/sum/tools/verify/internal/value"
)

var (
	checkTaskNames = []string{"test", "tests", "check", "lint", "typecheck", "build", "unit", "e2e", "spec"}
	serveTaskNames = []string{"serve", "dev", "start", "run", "server", "preview"}
	// Capabilities are real installed commands; a surface whose driver is absent stays `manual`, never a guessed recipe.
	capabilityNames = []string{"python3", "node", "curl", "docker", "chrome-devtools-axi", "playwright", "tmux"}
	browserDrivers  = []string{"chrome-devtools-axi", "playwright"}
)

// Unicode-aware whitespace and word characters; RE2's \s and \w are ASCII-only.
const (
	space = `[` + pycompat.SpaceClass + `]`
	word  = `[` + pycompat.WordClass + `]`
)

var (
	makeTarget    = regexp.MustCompile(`(?m)^([A-Za-z0-9_.-]+)` + space + `*:`)
	loopbackPort  = regexp.MustCompile(`(?:127\.0\.0\.1|localhost|0\.0\.0\.0):(\d{2,5})`)
	pathVariable  = regexp.MustCompile(`^[A-Z][A-Z0-9_]{3,}_(?:DIR|PORT|URL|HOME|PATH)$`)
	routerCall    = regexp.MustCompile(`(?:app|router)\.(?:get|post|put|delete|patch|use)\(` + space + `*["']([^"']+)["']`)
	routePatterns = []*regexp.Regexp{
		regexp.MustCompile(`@` + word + `+\.(?:route|get|post|put|delete|patch)\(` + space + `*["']([^"']+)["']`),
		regexp.MustCompile(`self\.path` + space + `*(?:==|in|\.startswith\()` + space + `*\(?` + space + `*["']([^"']+)["']`),
		regexp.MustCompile(`(?:path|url)` + space + `*==` + space + `*["'](/[^"']*)["']`),
	}
	verifyAssignment = regexp.MustCompile(`(?m)^verify` + space + `*=`)
	verifyCycles     = []*regexp.Regexp{
		regexp.MustCompile(`(?m)^verify` + space + `*=` + space + `*["'].*mise run verify`),
		regexp.MustCompile(`(?ms)^\[tasks\.verify\].*?^run` + space + `*=` + space + `*["'].*mise run verify`),
	}
)

type miseTask struct {
	Name        string  `json:"name"`
	Source      *string `json:"source"`
	Description string  `json:"description"`
}

type miseTasks struct {
	Available bool       `json:"available"`
	Own       []miseTask `json:"own"`
	Inherited []miseTask `json:"inherited"`
}

type target struct {
	Runner string `json:"runner"`
	Name   string `json:"name"`
}

type readiness struct {
	Endpoints []string `json:"endpoints"`
	Ports     []int    `json:"ports"`
	Env       []string `json:"env"`
}

type testLocation struct {
	Dir      string   `json:"dir"`
	Files    int      `json:"files"`
	Examples []string `json:"examples"`
}

type existing struct {
	Path        string `json:"path"`
	FeatureMaps any    `json:"feature_maps"`
	Artifacts   any    `json:"artifacts"`
	Parsed      bool   `json:"parsed"`
}

type inspection struct {
	Root             string         `json:"root"`
	Guidance         []string       `json:"guidance"`
	Mise             miseTasks      `json:"mise"`
	PackageScripts   *value.Object  `json:"package_scripts"`
	MakeTargets      []target       `json:"make_targets"`
	Containers       []string       `json:"containers"`
	Readiness        readiness      `json:"readiness"`
	CLIEntrypoints   []string       `json:"cli_entrypoints"`
	Routes           []string       `json:"routes"`
	Tests            []testLocation `json:"tests"`
	CheckTasks       []string       `json:"check_tasks"`
	ServeTasks       []string       `json:"serve_tasks"`
	Capabilities     *value.Object  `json:"capabilities"`
	Surfaces         []string       `json:"surfaces"`
	ExistingContract *existing      `json:"existing_contract"`
	HasVerifyTask    bool           `json:"has_verify_task"`
	MentionsDist     bool           `json:"mentions_dist"`
	InheritedVerify  bool           `json:"inherited_verify"`
	VerifyCycle      bool           `json:"verify_cycle"`
}

// read returns a text file with universal newlines, or "" when it is absent, unreadable, or not UTF-8.
func read(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(data) {
		return ""
	}
	return pycompat.UniversalNewlines(string(data))
}

// suffix is the final extension of a file name; a leading or trailing dot is not one.
func suffix(name string) string {
	if i := strings.LastIndex(name, "."); i > 0 && i < len(name)-1 {
		return name[i:]
	}
	return ""
}

func sortedSet(values []string) []string {
	out := slices.Clone(values)
	sort.Strings(out)
	out = slices.Compact(out)
	if out == nil {
		return []string{}
	}
	return out
}

// walkFiles visits every regular file below dir. Symlinked directories are not descended into.
func walkFiles(dir string, skipDir []string, visit func(path string)) {
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		p := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			if !slices.Contains(skipDir, entry.Name()) {
				walkFiles(p, skipDir, visit)
			}
			continue
		}
		if entry.Type().IsRegular() || (entry.Type()&fs.ModeSymlink != 0 && pycompat.IsFile(p)) {
			visit(p)
		}
	}
}

func relative(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(rel)
}

func tomlTable(text string) map[string]any {
	var table map[string]any
	if _, err := toml.Decode(text, &table); err != nil {
		return nil
	}
	return table
}

// findMiseTasks lists the tasks mise resolves from the root, split into this repository's own and inherited ones. It never runs a task.
func findMiseTasks(root string) miseTasks {
	tasks := miseTasks{Own: []miseTask{}, Inherited: []miseTask{}}
	mise := contract.ListMiseTasks(root)
	if mise.Binary == "" {
		return tasks
	}
	tasks.Available = true
	var rows []any
	if json.Unmarshal(mise.Stdout, &rows) != nil {
		return tasks
	}
	for _, item := range rows {
		row, _ := item.(map[string]any)
		name, _ := row["name"].(string)
		if name == "" {
			continue
		}
		task := miseTask{Name: name}
		task.Description, _ = row["description"].(string)
		location, _ := row["source"].(string)
		if location == "" {
			location, _ = row["file"].(string)
		}
		own := false
		if location != "" {
			source := pycompat.ResolveExistingPrefix(location)
			task.Source = &source
			rel, err := filepath.Rel(root, source)
			own = err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
		}
		if own {
			tasks.Own = append(tasks.Own, task)
		} else {
			tasks.Inherited = append(tasks.Inherited, task)
		}
	}
	return tasks
}

func packageJSON(root string) map[string]json.RawMessage {
	var data map[string]json.RawMessage
	_ = json.Unmarshal([]byte(read(filepath.Join(root, "package.json"))), &data)
	return data
}

// packageScripts keeps the string-valued scripts in the order package.json declares them.
func packageScripts(root string) *value.Object {
	scripts := value.NewObject()
	dec := json.NewDecoder(bytes.NewReader(packageJSON(root)["scripts"]))
	if open, err := dec.Token(); err != nil || open != json.Delim('{') {
		return scripts
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			break
		}
		var value any
		if dec.Decode(&value) != nil {
			break
		}
		if command, ok := value.(string); ok {
			scripts.Set(key.(string), command)
		}
	}
	return scripts
}

func makeTargets(root string) []target {
	targets := []target{}
	for _, name := range []string{"Makefile", "makefile", "justfile", "Justfile"} {
		text := read(filepath.Join(root, name))
		runner := "make"
		if strings.Contains(strings.ToLower(name), "just") {
			runner = "just"
		}
		for _, match := range makeTarget.FindAllStringSubmatchIndex(text, -1) {
			// `name := value` is a variable assignment, not a target.
			if strings.HasPrefix(text[match[1]:], "=") {
				continue
			}
			targets = append(targets, target{Runner: runner, Name: text[match[2]:match[3]]})
		}
	}
	return targets
}

func declaredContainers(root string) []string {
	found := []string{}
	for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml", "Dockerfile", ".devcontainer/devcontainer.json"} {
		if pycompat.IsFile(filepath.Join(root, name)) {
			found = append(found, name)
		}
	}
	return found
}

// readinessHints collects the health/readiness endpoints and ports the repository already mentions; nothing is invented from them.
func readinessHints(blob string) readiness {
	var endpoints []string
	names := []string{"health", "healthz", "ready", "readyz", "status", "ping", "live", "version"}
	for i := 0; i < len(blob); i++ {
		if blob[i] != '/' {
			continue
		}
		// An endpoint stands alone: not a later segment of a longer path, and not the start of a longer word.
		if before, size := utf8.DecodeLastRuneInString(blob[:i]); size > 0 && (before == '/' || pycompat.IsWord(before)) {
			continue
		}
		rest := blob[i+1:]
		for _, name := range names {
			if !strings.HasPrefix(rest, name) {
				continue
			}
			if after, size := utf8.DecodeRuneInString(rest[len(name):]); size == 0 || !pycompat.IsWord(after) {
				endpoints = append(endpoints, "/"+name)
				i += len(name)
				break
			}
		}
	}
	ports := []int{}
	for _, match := range loopbackPort.FindAllStringSubmatch(blob, -1) {
		if port, _ := strconv.Atoi(match[1]); port >= 1 && port <= 65535 {
			ports = append(ports, port)
		}
	}
	sort.Ints(ports)
	var env []string
	for _, run := range strings.FieldsFunc(blob, func(r rune) bool { return !pycompat.IsWord(r) }) {
		if pathVariable.MatchString(run) {
			env = append(env, run)
		}
	}
	return readiness{Endpoints: sortedSet(endpoints), Ports: slices.Compact(ports), Env: sortedSet(env)}
}

// serviceCommands collects the words of the tasks and scripts that start a server; the files they launch are services, not CLI entrypoints.
func serviceCommands(root string, scripts *value.Object) map[string]bool {
	words := map[string]bool{}
	add := func(command string) {
		for _, w := range strings.Fields(command) {
			words[w] = true
		}
	}
	for _, name := range []string{"mise.toml", ".mise.toml"} {
		tasks, _ := tomlTable(read(filepath.Join(root, name)))["tasks"].(map[string]any)
		for _, task := range serveTaskNames {
			run := tasks[task]
			if spec, ok := run.(map[string]any); ok {
				run = spec["run"]
			}
			switch run := run.(type) {
			case string:
				add(run)
			case []any:
				for _, item := range run {
					if command, ok := item.(string); ok {
						add(command)
					}
				}
			}
		}
	}
	for _, script := range scripts.Keys() {
		if slices.Contains(serveTaskNames, script) {
			add(value.Str(scripts.Get(script)))
		}
	}
	return words
}

// cliEntrypoints lists the scripts a user runs directly: files at the root, bin/, or cli/ with a shebang, and console_scripts / package.json bin entries.
func cliEntrypoints(root string, scripts *value.Object) []string {
	served := serviceCommands(root, scripts)
	var entries []string
	candidate := func(rel string) {
		name := path.Base(rel)
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || served[rel] || served[name] {
			return
		}
		if full := filepath.Join(root, rel); pycompat.IsFile(full) && hasShebang(full) {
			entries = append(entries, rel)
		}
	}
	for _, name := range rootPython(root) {
		candidate(name)
	}
	for _, dir := range []string{"bin", "cli"} {
		listed, _ := os.ReadDir(filepath.Join(root, dir))
		for _, entry := range listed {
			candidate(dir + "/" + entry.Name())
		}
	}
	if raw := packageJSON(root)["bin"]; raw != nil {
		var one string
		var many map[string]any
		if json.Unmarshal(raw, &one) == nil {
			entries = append(entries, one)
		} else if json.Unmarshal(raw, &many) == nil {
			for _, value := range many {
				if bin, ok := value.(string); ok {
					entries = append(entries, bin)
				}
			}
		}
	}
	project, _ := tomlTable(read(filepath.Join(root, "pyproject.toml")))["project"].(map[string]any)
	console, _ := project["scripts"].(map[string]any)
	for name := range console {
		entries = append(entries, name+" (console script)")
	}
	return sortedSet(entries)
}

// hasShebang reports whether a UTF-8 text file starts with `#!`. The rest is read only once the first two bytes match.
func hasShebang(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	start := make([]byte, 2)
	if _, err := io.ReadFull(file, start); err != nil || string(start) != "#!" {
		return false
	}
	rest, err := io.ReadAll(file)
	return err == nil && utf8.Valid(rest)
}

// rootPython names the *.py entries directly under the root.
func rootPython(root string) []string {
	var names []string
	listed, _ := os.ReadDir(root)
	for _, entry := range listed {
		if strings.HasSuffix(entry.Name(), ".py") {
			names = append(names, entry.Name())
		}
	}
	return names
}

func testLocations(root string) []testLocation {
	found := []testLocation{}
	suffixes := []string{".py", ".js", ".mjs", ".ts", ".tsx", ".go", ".rs", ".rb"}
	for _, dir := range []string{"tests", "test", "spec", "__tests__", "e2e"} {
		if !pycompat.IsDir(filepath.Join(root, dir)) {
			continue
		}
		var files []string
		walkFiles(filepath.Join(root, dir), nil, func(p string) {
			name := filepath.Base(p)
			if (strings.Contains(name, "test") || strings.Contains(name, "spec")) && slices.Contains(suffixes, suffix(name)) {
				files = append(files, relative(root, p))
			}
		})
		if len(files) > 0 {
			sort.Strings(files)
			found = append(found, testLocation{Dir: dir, Files: len(files), Examples: files[:min(5, len(files))]})
		}
	}
	return found
}

// httpRoutes collects route declarations the source already contains (Flask/FastAPI/Express/http.server style); only literal paths are kept.
func httpRoutes(root string) []string {
	skipped := []string{"node_modules", ".git", "dist", "build", ".venv", "tests", "test"}
	// The exclusion applies to every part of the absolute path, so a checkout below a directory with one of these names reports no routes.
	for _, part := range strings.Split(filepath.ToSlash(root), "/") {
		if slices.Contains(skipped, part) {
			return []string{}
		}
	}
	suffixes := []string{".py", ".js", ".mjs", ".ts", ".go", ".rb"}
	var routes []string
	walkFiles(root, skipped, func(p string) {
		if !slices.Contains(suffixes, suffix(filepath.Base(p))) {
			return
		}
		text := read(p)
		found := routerCalls(text)
		for _, pattern := range routePatterns {
			for _, match := range pattern.FindAllStringSubmatch(text, -1) {
				found = append(found, match[1])
			}
		}
		for _, route := range found {
			if strings.HasPrefix(route, "/") {
				routes = append(routes, route)
			}
		}
	})
	return sortedSet(routes)
}

// routerCalls finds `app.get("/x")` style calls whose receiver is a whole word: `myapp.get(...)` is someone else's object.
func routerCalls(text string) []string {
	var found []string
	for pos := 0; pos < len(text); {
		match := routerCall.FindStringSubmatchIndex(text[pos:])
		if match == nil {
			break
		}
		start := pos + match[0]
		if before, size := utf8.DecodeLastRuneInString(text[:start]); size > 0 && pycompat.IsWord(before) {
			pos = start + 1
			continue
		}
		found = append(found, text[pos+match[2]:pos+match[3]])
		pos += match[1]
	}
	return found
}

func existingContract(root string) *existing {
	file := filepath.Join(root, contract.File)
	if !pycompat.IsFile(file) {
		return nil
	}
	// A block that does not parse still counts as found, with nothing declared.
	config, found, _ := contract.Config(read(file))
	return &existing{Path: contract.File, FeatureMaps: config["feature_maps"], Artifacts: config["artifacts"], Parsed: found}
}

func guidanceFiles(root string) []string {
	found := []string{}
	for _, name := range []string{
		"README.md", "AGENTS.md", "Agents.md", "CLAUDE.md", "CONTRIBUTING.md", "VERIFY.md",
		"docs/README.md", "ARCHITECTURE.md", "docs/ARCHITECTURE.md", "CODEOWNERS", ".github/CODEOWNERS",
	} {
		if pycompat.IsFile(filepath.Join(root, name)) {
			found = append(found, name)
		}
	}
	if pycompat.IsDir(filepath.Join(root, ".github", "workflows")) {
		found = append(found, ".github/workflows/")
	}
	return found
}

// verifyRunCycle reports whether a `verify` task would invoke `mise run verify` again.
func verifyRunCycle(root string) bool {
	for _, name := range []string{"mise.toml", ".mise.toml"} {
		text := read(filepath.Join(root, name))
		for _, pattern := range verifyCycles {
			if pattern.MatchString(text) {
				return true
			}
		}
	}
	return false
}

func taskNames(tasks []miseTask, wanted []string) []string {
	names := []string{}
	for _, task := range tasks {
		if slices.Contains(wanted, task.Name) {
			names = append(names, task.Name)
		}
	}
	return names
}

func inspect(root string, requested []string) inspection {
	tasks := findMiseTasks(root)
	scripts := packageScripts(root)
	guidance := guidanceFiles(root)
	var blobs []string
	for _, name := range guidance {
		blobs = append(blobs, read(filepath.Join(root, name)))
	}
	for _, name := range []string{"mise.toml", ".mise.toml", "package.json", "Procfile"} {
		if pycompat.IsFile(filepath.Join(root, name)) {
			blobs = append(blobs, read(filepath.Join(root, name)))
		}
	}
	python := rootPython(root)
	for _, name := range python[:min(20, len(python))] {
		blobs = append(blobs, read(filepath.Join(root, name)))
	}
	blob := strings.Join(blobs, "\n")
	info := inspection{
		Root: root, Guidance: guidance, Mise: tasks, PackageScripts: scripts, MakeTargets: makeTargets(root),
		Containers: declaredContainers(root), Readiness: readinessHints(blob), CLIEntrypoints: cliEntrypoints(root, scripts), Routes: httpRoutes(root),
		Tests: testLocations(root), CheckTasks: taskNames(tasks.Own, checkTaskNames), ServeTasks: taskNames(tasks.Own, serveTaskNames),
		Capabilities: value.NewObject(), Surfaces: []string{}, ExistingContract: existingContract(root),
		HasVerifyTask:   len(taskNames(tasks.Own, []string{"verify"})) > 0,
		MentionsDist:    strings.Contains(blob, "dist"),
		InheritedVerify: len(taskNames(tasks.Inherited, []string{"verify"})) > 0,
		VerifyCycle:     verifyRunCycle(root),
	}
	for _, name := range capabilityNames {
		_, err := exec.LookPath(name)
		info.Capabilities.Set(name, err == nil)
	}
	if len(info.Routes) > 0 || len(info.ServeTasks) > 0 || len(info.Readiness.Endpoints) > 0 {
		info.Surfaces = append(info.Surfaces, "service")
	}
	for _, asset := range []string{"src/index.html", "src/index.html.in", "public/index.html", "index.html", "dist/index.html"} {
		if pycompat.Exists(filepath.Join(root, asset)) {
			info.Surfaces = append(info.Surfaces, "web")
			break
		}
	}
	if len(info.CLIEntrypoints) > 0 {
		info.Surfaces = append(info.Surfaces, "cli")
	}
	for _, surface := range requested {
		if !slices.Contains(info.Surfaces, surface) {
			info.Surfaces = append(info.Surfaces, surface)
		}
	}
	return info
}
