package devcmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func developmentLab(t *testing.T) (*store.Store, string, *hctest.Fixture, *ordjson.Object) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("SHELL", "/bin/sh")
	for name, text := range map[string]string{".gitignore": ".sum/\n", "README.md": "Development fixture.\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, argv := range [][]string{{"init", "-q"}, {"add", ".gitignore", "README.md"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "core.hooksPath=/dev/null", "commit", "-qm", "Initial"}} {
		command := exec.Command("git", append([]string{"-C", root}, argv...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", argv, err, output)
		}
	}
	s, err := store.Open(filepath.Join(root, ".sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	root = filepath.Dir(s.Root)
	f := hctest.New(t)
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	caller := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: f.CoreID, Socket: f.Socket, Session: "development-lab", Address: "tparent", CreatedMS: 1, Cwd: root}.Object()
	return s, root, f, caller
}
func TestDevelopmentTerminalIsNativeOrdinaryAndReused(t *testing.T) {
	s, root, f, caller := developmentLab(t)
	result, err := Prepare(s, caller, root, "native", "HEAD", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ordjson.MarshalCompact(result); err != nil {
		t.Fatal(err)
	}
	path := asString(result, "path")
	marker, err := developmentMarker(path)
	if err != nil {
		t.Fatal(err)
	}
	if value := asString(marker, "installation_home"); value != s.Root || value == s.Home {
		t.Fatalf("installation home=%q stable=%q physical=%q", value, s.Root, s.Home)
	}
	if _, exists := marker.Get("panes"); exists {
		t.Fatal("fresh development marker recorded legacy pane metadata")
	}
	var run map[string]any
	for _, call := range f.Calls() {
		if call.Method == "terminal.run" {
			run = call.Params
		}
	}
	if run == nil {
		t.Fatal("no native terminal created")
	}
	if run["outlive_parent"] != true || run["no_notify"] != true || run["keep"] != false || run["rm"] != false {
		t.Fatalf("ordinary terminal lifecycle=%v", run)
	}
	env := run["env"].(map[string]any)
	for _, key := range []string{"SUM_HOME", "SUM_INSTALL_ROOT", "SUM_TASK", "SUM_ROLE", "SUM_ATTEMPT", "SUM_BINDING_GENERATION"} {
		if env[key] != "" {
			t.Fatalf("inherited authority %s=%v", key, env[key])
		}
	}
	if entries, err := os.ReadDir(s.Sessions); err != nil || len(entries) != 0 {
		t.Fatalf("developer shell obtained a role registration: %v %v", entries, err)
	}
	launchValue, _ := marker.Get("terminal_launch")
	launch := launchValue.(*ordjson.Object)
	terminalValue, _ := launch.Get("terminal")
	terminal := terminalValue.(*ordjson.Object)
	address := asString(terminal, "address")
	if address == "" {
		t.Fatal("native endpoint was not recorded")
	}
	if _, err = Prepare(s, caller, root, "native", "HEAD", true); err != nil {
		t.Fatal(err)
	}
	f.Exit(address, 0)
	if _, err = Prepare(s, caller, root, "native", "HEAD", true); err != nil {
		t.Fatal(err)
	}
	current := f.Terminals()[0]
	current.CreatedMS += 1000
	f.SetTerminal(current)
	replaced, err := Prepare(s, caller, root, "native", "HEAD", true)
	if err != nil {
		t.Fatal(err)
	}
	replacementValue, _ := replaced.Get("terminal")
	if !strings.Contains(asString(replacementValue.(*ordjson.Object), "observation_error"), "creation") {
		t.Fatal("reused address was silently adopted")
	}
	count := 0
	for _, call := range f.Calls() {
		if call.Method == "terminal.run" {
			count++
		}
		if call.Method == "terminal.send" {
			t.Fatal("ordinary developer shell received agent work")
		}
	}
	if count != 1 {
		t.Fatalf("repeated prepare or finished shell caused duplicate native launches: %d", count)
	}
}
func TestDevelopmentTerminalLostResponseNeverReplays(t *testing.T) {
	s, root, f, caller := developmentLab(t)
	f.QueueFault("terminal.run", hctest.Fault{LoseResponse: true})
	result, err := Prepare(s, caller, root, "uncertain", "HEAD", true)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := result.Get("terminal")
	launch := value.(*ordjson.Object)
	if asString(launch, "effect") != "uncertain" {
		t.Fatalf("lost native response=%v", launch)
	}
	if _, err = Prepare(s, caller, root, "uncertain", "HEAD", true); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, call := range f.Calls() {
		if call.Method == "terminal.run" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("uncertain launch replayed: %d", count)
	}
	marker, err := developmentMarker(asString(result, "path"))
	if err != nil {
		t.Fatal(err)
	}
	value, _ = marker.Get("terminal_launch")
	saved := value.(*ordjson.Object)
	if asString(saved, "effect") != "uncertain" {
		t.Fatal("native uncertainty erased")
	}
}
func TestDevelopmentWithoutPaneDoesNotObserveRuntime(t *testing.T) {
	s, root, f, _ := developmentLab(t)
	result, err := Prepare(s, nil, root, "graphless", "HEAD", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asString(result, "note"), "CODEGRAPH_NO_DAEMON=1") {
		t.Fatal("on-demand graph guidance lost")
	}
	if len(f.Calls()) != 0 {
		t.Fatal("plain checkout preparation touched a core")
	}
	if _, err = os.Stat(filepath.Join(asString(result, "path"), ".codegraph")); !os.IsNotExist(err) {
		t.Fatal("development preparation built a code graph")
	}
}

func TestDevelopmentPrepareFencesCachedGenerationBeforeGitMutation(t *testing.T) {
	s, root, f, caller := developmentLab(t)
	value, err := ordjson.ReadFile(filepath.Join(s.Root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	state := value.(*ordjson.Object)
	state.Set("generation", "g-"+strings.Repeat("0", 24))
	if err = ordjson.WriteFile(filepath.Join(s.Root, "state.json"), state); err != nil {
		t.Fatal(err)
	}
	if _, err = Prepare(s, caller, root, "fenced", "HEAD", true); !errors.Is(err, store.ErrInactiveGeneration) {
		t.Fatalf("cached developer helper was not fenced: %v", err)
	}
	if _, err = os.Stat(filepath.Join(root, ".sum", "dev", "fenced")); !os.IsNotExist(err) {
		t.Fatal("fenced helper created a checkout")
	}
	if len(f.Calls()) != 0 {
		t.Fatal("fenced helper contacted runtime")
	}
}

func TestDevelopmentPrepareRefusesSymlinkedCheckoutPaths(t *testing.T) {
	s, root, f, caller := developmentLab(t)
	external := t.TempDir()
	directory := filepath.Join(root, ".sum", "dev")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(directory, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(s, caller, root, "linked", "HEAD", true); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("linked development path accepted: %v", err)
	}
	if entries, err := os.ReadDir(external); err != nil || len(entries) != 0 {
		t.Fatalf("linked directory changed: %v %v", entries, err)
	}
	if len(f.Calls()) != 0 {
		t.Fatal("linked checkout triggered native launch")
	}
}
