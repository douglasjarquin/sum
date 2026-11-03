package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func ordinaryInitLab(t *testing.T) (string, *store.Store) {
	t.Helper()
	clearFallbackIdentity(t)
	root := t.TempDir()
	for path, text := range map[string]string{"COORDINATOR.md": "# Coordinate approved work\n", "skills/sum-develop/SKILL.md": "# Develop in isolation\n"} {
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := store.Open(filepath.Join(root, ".sum"))
	if err != nil {
		t.Fatal(err)
	}
	return root, s
}

func ordinaryInit(t *testing.T, root string, args ...string) (map[string]any, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	command := NewRoot(filepath.Join(root, "bin", "sumctl"), &out, &errOut)
	command.SetArgs(append([]string{"--format", "json", "init"}, args...))
	err := command.Execute()
	if out.Len() == 0 {
		return nil, err
	}
	return decodeObject(t, out.String()), err
}

func TestOrdinaryInitCoordinatesWithoutHC(t *testing.T) {
	root, _ := ordinaryInitLab(t)
	f := hctest.New(t)
	t.Setenv("HC_SOCKET", f.Socket)
	t.Setenv("SUM_ROLE", "coordinator")
	t.Setenv("SUM_SESSION", "stale-session")
	t.Setenv("SUM_HOME", filepath.Join(t.TempDir(), "unrelated"))
	view, err := ordinaryInit(t, root)
	if err != nil {
		t.Fatal(err)
	}
	if view["role"] != "coordinator" || asString(view["claim"]) == "" {
		t.Fatalf("ordinary entry = %v", view)
	}
	for _, key := range []string{"endpoint", "registration", "hook_activation", "contract", "returns"} {
		if _, ok := view[key]; ok {
			t.Fatalf("ordinary entry retained %s", key)
		}
	}
	if len(f.Calls()) != 0 {
		t.Fatalf("ordinary init contacted HC: %v", f.Calls())
	}
	if procedureRow(t, view)["ok"] != true {
		t.Fatalf("procedure was not validated: %v", view)
	}
	opened, err := store.Open(filepath.Join(root, ".sum"))
	if err != nil || !opened.Designated() {
		t.Fatalf("fresh workspace not designated: %v", err)
	}
}

func TestOrdinaryInitResumesConflictsAndTransfers(t *testing.T) {
	root, _ := ordinaryInitLab(t)
	first, err := ordinaryInit(t, root)
	if err != nil {
		t.Fatal(err)
	}
	token := asString(first["claim"])
	if token == "" {
		t.Fatalf("missing claim: %v", first)
	}
	resumed, err := ordinaryInit(t, root, "--claim", token)
	if err != nil || resumed["claim"] != token {
		t.Fatalf("resume = %v %v", resumed, err)
	}
	conflict, err := ordinaryInit(t, root)
	if err == nil || conflict["blocker"] != "conflict" || asString(conflict["next"]) == "" {
		t.Fatalf("conflict = %v %v", conflict, err)
	}
	next, err := ordinaryInit(t, root, "--transfer")
	if err != nil || next["claim"] == token {
		t.Fatalf("transfer = %v %v", next, err)
	}
}

func TestOrdinaryInitInvalidTaskCannotBecomeDeveloper(t *testing.T) {
	root, _ := ordinaryInitLab(t)
	if err := os.MkdirAll(filepath.Join(root, ".sum"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".sum", "dev.json"), []byte(`{"schema":1,"kind":"development"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_ROLE", "worker")
	t.Setenv("SUM_TASK", "t-aaaaaaaaaaaa")
	t.Setenv("SUM_ATTEMPT", "missing")
	view, err := ordinaryInit(t, root)
	if err == nil || (view != nil && view["role"] == "developer") {
		t.Fatalf("invalid task became developer: %v %v", view, err)
	}
}

func TestOrdinaryInitRecordedTaskCheckoutCannotBecomeCoordinator(t *testing.T) {
	root, s := ordinaryInitLab(t)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	checkout := t.TempDir()
	task := ordjson.NewObject()
	task.Set("schema", store.Schema)
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("status", "running")
	task.Set("worktree", checkout)
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	t.Chdir(checkout)
	view, err := ordinaryInit(t, root)
	if err == nil || view["blocker"] != "task-context" || view["role"] != nil {
		t.Fatalf("task checkout acquired coordination: %v %v", view, err)
	}
	if _, err := os.Stat(filepath.Join(s.Home, "coordinator.json")); !os.IsNotExist(err) {
		t.Fatal("invalid task checkout wrote a coordinator claim")
	}
}

func TestOrdinaryInitLegacyStateIsStructuredAndPreserved(t *testing.T) {
	root, _ := ordinaryInitLab(t)
	if err := os.MkdirAll(filepath.Join(root, ".sum"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".sum", "state.json")
	before := []byte(`{"schema":1,"instance":"legacy"}`)
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	view, err := ordinaryInit(t, root)
	if err == nil || view["blocker"] != "legacy-state" || asString(view["next"]) == "" {
		t.Fatalf("legacy result = %v %v", view, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("legacy bytes changed: %s %v", after, err)
	}
}

func TestOrdinaryInitRetiresNativeOwnerWithoutChangingTaskHistory(t *testing.T) {
	clearFallbackIdentity(t)
	root, s, f, _ := developerFallbackLab(t)
	if err := os.WriteFile(filepath.Join(root, "COORDINATOR.md"), []byte("# Coordinate approved work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	owner, err := s.Owner()
	if err != nil {
		t.Fatal(err)
	}
	ownerPath := filepath.Join(s.Home, "context.json")
	ownerBefore, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", store.Schema)
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("status", "reported")
	task.Set("parent", owner)
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(s.Tasks, "t-aaaaaaaaaaaa", "task.json")
	taskBefore, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Home, "settings.json"), []byte(`{"schema":1,"hook":{"enabled":false},"worker":{"harness":"codex"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Home, "hook"), 0700); err != nil {
		t.Fatal(err)
	}
	health := []byte(`{"schema":2,"enabled":false,"errors":[]}`)
	if err := os.WriteFile(filepath.Join(s.Home, "hook", "health.json"), health, 0600); err != nil {
		t.Fatal(err)
	}
	view, err := ordinaryInit(t, root, "--retire-native")
	if err != nil || view["role"] != "coordinator" {
		t.Fatalf("retirement = %v %v", view, err)
	}
	for path, before := range map[string][]byte{ownerPath: ownerBefore, taskPath: taskBefore, filepath.Join(s.Home, "hook", "health.retired-native.json"): health} {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("retirement changed history %s: %v", path, err)
		}
	}
	settings := readJSON(t, filepath.Join(s.Home, "settings.json"))
	if _, present := settings["hook"]; present {
		t.Fatalf("obsolete observer preference remained: %v", settings)
	}
	if asMap(settings["worker"])["harness"] != "codex" {
		t.Fatalf("worker choice changed: %v", settings)
	}
	if len(f.Calls()) != 0 {
		t.Fatalf("retirement contacted HC: %v", f.Calls())
	}
}
