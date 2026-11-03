package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/prepare"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

var nativeLabs sync.Map

func nativeLabFor(t *testing.T, key string) *demoLab {
	t.Helper()
	value, ok := nativeLabs.Load(key)
	if !ok {
		t.Fatalf("no native fixture for %s", key)
	}
	return value.(*demoLab)
}

func nativeTestStore(t *testing.T, home string) *store.Store {
	t.Helper()
	s, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	return s
}

func newNativeDemoLab(t *testing.T, root, helper, base string) *demoLab {
	t.Helper()
	return newNativeDemoLabAt(t, root, helper, base, filepath.Join(base, "state"))
}

func newNativeDemoLabAt(t *testing.T, root, helper, base, home string) *demoLab {
	t.Helper()
	s := nativeTestStore(t, home)
	f := hctest.New(t)
	d := &demoLab{t: t, root: root, helper: helper, base: base, home: s.Home, env: demoEnv(t, root, base), native: f, config: filepath.Join(base, "hc-config")}
	// Ordinary entry needs a staged installation boundary without this source checkout's development marker.
	entryRoot := filepath.Join(base, "ordinary-runtime")
	d.entryHelper = filepath.Join(entryRoot, "bin", "sumctl")
	for _, name := range []string{"COORDINATOR.md", "skills/sum-develop/SKILL.md"} {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(entryRoot, name)
		if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(destination, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	rawHelper, err := os.ReadFile(filepath.Join(root, ".local", "bin", "sumctl"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(d.entryHelper), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(d.entryHelper, rawHelper, 0700); err != nil {
		t.Fatal(err)
	}
	codex := filepath.Join(base, "codex")
	if err := os.WriteFile(codex, []byte("#!/bin/sh\nprintf '%s\\n' 'codex-cli 0.166.0'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	codexHome := filepath.Join(base, "codex-home")
	for _, dir := range []string{d.config, codexHome} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SUM_CODEX_BIN", codex)
	t.Setenv("CODEX_HOME", codexHome)
	for key, suffix := range map[string]string{"HOME": "user-home", "CLAUDE_CONFIG_DIR": "claude-home", "GROK_HOME": "grok-home", "XDG_CONFIG_HOME": "xdg-config"} {
		path := filepath.Join(base, suffix)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, path)
		d.setEnv(key, path)
	}
	for harness, version := range map[string]string{"grok": "grok 1.0.67 (face1234) [stable]", "claude": "2.1.316 (Claude Code)", "devin": "devin 3000.11.234 (face1234)"} {
		path := filepath.Join(base, harness)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' '"+version+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
		key := "SUM_" + strings.ToUpper(harness) + "_BIN"
		t.Setenv(key, path)
		d.setEnv(key, path)
	}
	options := launch.HCTemplateOptions{Cwd: root, CodexHome: codexHome}
	_, err = launch.PrepareHCTemplate(root, d.config, "codex", nil, options)
	if err != nil {
		t.Fatal(err)
	}
	// Keep an unrelated native terminal so address allocation also covers existing cores.
	pid := uint32(100000)
	f.SetTerminal(hcclient.Terminal{Address: "t1", CreatedMS: 1, Cwd: root, State: "live", PID: &pid})
	f.SetStatus("t1", "idle")
	session, err := s.ClaimCoordinator("", "ordinary test conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	d.claim = session.Token()
	route, _ := ordjson.FromValue(map[string]any{"socket": f.Socket, "session": "sum-test", "config_home": d.config, "state_home": filepath.Join(base, "hc-state"), "core_id": f.CoreID})
	if err := ordjson.WriteFile(filepath.Join(s.Root, "runtime-route.json"), route); err != nil {
		t.Fatal(err)
	}
	settings, _ := ordjson.FromValue(map[string]any{"schema": 1, "worker": map[string]any{"harness": "codex"}, "template": map[string]any{"codex_home": codexHome}})
	if err := ordjson.WriteFile(filepath.Join(s.Home, "settings.json"), settings); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"SUM_INSTALL_ROOT": root, "SUM_CODEX_BIN": codex, "CODEX_HOME": codexHome} {
		d.setEnv(key, value)
	}
	if err := f.LoadTemplatesFrom(d.config); err != nil {
		t.Fatal(err)
	}
	nativeLabs.Store(s.Root, d)
	nativeLabs.Store(d.home, d)
	nativeLabs.Store(base, d)
	nativeLabs.Store(home, d)
	t.Cleanup(func() {
		nativeLabs.Delete(s.Root)
		nativeLabs.Delete(d.home)
		nativeLabs.Delete(base)
		nativeLabs.Delete(home)
	})
	return d
}

func nativeTemplateCommand(t *testing.T, config, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(config, "herdr-core", "templates", name+".md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "command: ") {
			return strings.TrimPrefix(line, "command: ")
		}
	}
	t.Fatal("qualified template has no command")
	return ""
}

func endpointEnv(value any) map[string]string {
	data, _ := json.Marshal(value)
	var e store.Endpoint
	_ = json.Unmarshal(data, &e)
	return map[string]string{"SUM_SESSION": e.Session, "SUM_CORE_ID": e.CoreID, "HC_SOCKET": e.Socket, "HC_TERMINAL_ID": e.Address, "SUM_ROLE": e.Role, "SUM_TASK": e.Task, "SUM_ATTEMPT": e.Attempt, "SUM_BINDING_GENERATION": strconv.FormatUint(e.BindingGeneration, 10)}
}

func workerAddress(record map[string]any) string {
	if endpoint := asMap(record["worker_endpoint"]); endpoint != nil {
		return asString(endpoint["address"])
	}
	if endpoint := asMap(record["endpoint"]); endpoint != nil {
		return asString(endpoint["address"])
	}
	return asString(record["address"])
}

func (d *demoLab) callerEnv(address string, args []string) map[string]string {
	d.t.Helper()
	var endpoint map[string]any
	files, err := filepath.Glob(filepath.Join(d.home, "tasks", "*", "task.json"))
	if err != nil {
		d.t.Fatal(err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			d.t.Fatal(err)
		}
		var task map[string]any
		if err = json.Unmarshal(raw, &task); err != nil {
			d.t.Fatal(err)
		}
		for _, candidate := range []map[string]any{asMap(task["worker_endpoint"]), asMap(task["reviewer"]), asMap(asMap(task["reviewer"])["endpoint"])} {
			if asString(candidate["address"]) == address {
				endpoint = candidate
			}
		}
	}
	if endpoint == nil {
		return map[string]string{"HC_TERMINAL_ID": address, "SUM_ROLE": "", "SUM_TASK": "", "SUM_ATTEMPT": "", "SUM_BINDING_GENERATION": ""}
	}
	if len(args) > 0 && args[0] == "init" {
		for _, terminal := range d.native.Terminals() {
			if terminal.Address == address && terminal.PID == nil {
				pid := uint32(200000 + terminal.CreatedMS)
				terminal.PID = &pid
				terminal.Foreground = &hcclient.Foreground{PID: int32(pid + 1), Name: "codex", Argv: []string{"codex", "--no-daemon"}, Cwd: terminal.Cwd}
				d.native.SetTerminal(terminal)
				d.native.SetStatus(address, "idle")
			}
		}
	}
	return endpointEnv(endpoint)
}

func (d *demoLab) taskRecord(taskID string) map[string]any {
	d.t.Helper()
	raw, err := os.ReadFile(filepath.Join(d.home, "tasks", taskID, "task.json"))
	if err != nil {
		d.t.Fatal(err)
	}
	var task map[string]any
	if err = json.Unmarshal(raw, &task); err != nil {
		d.t.Fatal(err)
	}
	return task
}

func (d *demoLab) workerReport(check bool, args ...string) map[string]any {
	d.t.Helper()
	if len(args) == 0 {
		d.t.Fatal("worker report needs a task")
	}
	task := d.taskRecord(args[0])
	address := workerAddress(task)
	if address == "" {
		d.t.Fatal("worker report fixture must first start a native worker")
	}
	d.ctlPane(address, true, "init")
	return d.ctlPane(address, check, append([]string{"report"}, args...)...)
}

func (d *demoLab) independentReview(check bool, args ...string) map[string]any {
	d.t.Helper()
	if len(args) == 0 {
		d.t.Fatal("review needs a task")
	}
	launched := d.ctl(true, "reviewer", "launch", args[0], "--harness", "codex")
	reviewer := asMap(launched["reviewer"])
	address := workerAddress(reviewer)
	if address == "" {
		d.t.Fatal("reviewer launch did not record a native endpoint")
	}
	d.ctlPane(address, true, "init")
	return d.ctlPane(address, check, append([]string{"review"}, args...)...)
}

func seedNativeWorker(t *testing.T, d *demoLab, s *store.Store, task *ordjson.Object) store.Endpoint {
	t.Helper()
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := task.Get("id")
	id := taskID.(string)
	session, err := s.CoordinatorSession()
	owner := session.Owner()
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := reservations.NewAttempt("worker", owner, s.Home, store.Now(), "running", nil)
	if err != nil {
		t.Fatal(err)
	}
	attemptID, _ := attempt.Get("id")
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: d.native.CoreID, Socket: d.native.Socket, Session: "sum-test", Address: "t2", CreatedMS: 2, Task: id, Role: "worker", Attempt: attemptID.(string), BindingGeneration: 1, Cwd: s.Home}
	pid := uint32(200000)
	foreground := &hcclient.Foreground{PID: 200001, Name: "codex", Argv: []string{"codex", "--no-daemon"}, Cwd: s.Home}
	options := launch.HCTemplateOptions{Cwd: s.Home, CodexHome: os.Getenv("CODEX_HOME")}
	spec, err := launch.PrepareHCTemplate(d.root, d.config, "codex", nil, options)
	if err != nil {
		t.Fatal(err)
	}
	provenance, _ := ordjson.FromValue(prepare.NativeTemplate{Spec: spec, ConfigHome: d.config, Options: options})
	name, key := spec.Name, "tab"
	d.native.SetTerminal(hcclient.Terminal{Address: "t2", CreatedMS: 2, Cwd: s.Home, State: "live", PID: &pid, Foreground: foreground, Template: &name, Submit: &key, Argv: []string{"sh", "-c", nativeTemplateCommand(t, d.config, spec.Name)}})
	d.native.SetStatus("t2", "idle")
	observed := incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: foreground}.Record(store.Now())
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: "worker-init", Attempt: endpoint.Attempt})
	registration, err := s.Register(endpoint, "worker", id, observed)
	if err != nil {
		t.Fatal(err)
	}
	registration.Set("native_receipt", receipt)
	registration.Set("native_template", provenance)
	if err = ordjson.WriteFile(filepath.Join(s.Sessions, store.RegistrationKey(endpoint)+".json"), registration); err != nil {
		t.Fatal(err)
	}
	attempt.Set("native_receipt", receipt)
	attempt.Set("incarnation", observed)
	task.Set("owner", owner)
	task.Set("schema", json.Number("2"))
	task.Set("worker_endpoint", endpoint.Object())
	task.Set("execution", reservations.NewExecution(attempt))
	task.Delete("pane")
	task.Set("machine", host.ID)
	task.Set("session", "sum-test")
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	return endpoint
}

func (d *demoLab) launchPointers(address string) []string {
	d.t.Helper()
	var pointers []string
	for _, call := range d.native.Calls() {
		if call.Method == "terminal.send" && call.Params["terminal"] == address {
			pointers = append(pointers, asString(call.Params["text"]))
		}
	}
	return pointers
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

var fixturePythonOnce sync.Once
var fixturePythonPath string
var fixturePythonError error

func pinnedFixturePython(t *testing.T) string {
	t.Helper()
	fixturePythonOnce.Do(func() {
		command := exec.Command("mise", "where", "python")
		command.Dir = repoRoot(t)
		output, err := command.Output()
		if err != nil {
			fixturePythonError = err
			return
		}
		fixturePythonPath = filepath.Join(strings.TrimSpace(string(output)), "bin", "python3")
		_, fixturePythonError = os.Stat(fixturePythonPath)
	})
	if fixturePythonError != nil {
		t.Fatalf("project-pinned Python fixture interpreter: %v", fixturePythonError)
	}
	return fixturePythonPath
}

func useLogicalCoordinator(t *testing.T, home string) {
	t.Helper()
	for key := range endpointEnv(nil) {
		t.Setenv(key, "")
	}
}
func useNativeTaskWorker(t *testing.T, home, taskID string) {
	t.Helper()
	s := nativeTestStore(t, home)
	task, err := s.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	var endpoint store.Endpoint
	if value, ok := task.Get("worker_endpoint"); ok {
		object, _ := value.(*ordjson.Object)
		endpoint = store.EndpointFromContext(object)
	} else {
		endpoint = seedNativeWorker(t, nativeLabFor(t, home), s, task)
	}
	for key, value := range endpointEnv(endpoint) {
		t.Setenv(key, value)
	}
}

func mustCLI(t *testing.T, home string, args ...string) string {
	t.Helper()
	out, err := runCLI(t, home, args...)
	if err != nil {
		t.Fatalf("sumctl %v: %v\n%s", args, err, out)
	}
	return out
}
