package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/procedure"
)

// roleRuntime is a disposable runtime holding the bootstrap and the role cores `init` names, so a test can
// remove or change them without touching the repository. The path contains a space on purpose.
func roleRuntime(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	runtime := filepath.Join(t.TempDir(), "role runtime")
	for _, rel := range []string{"AGENTS.md", procedure.Coordinator.Path, procedure.Developer.Path} {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(runtime, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return runtime
}

// runCLIFrom runs the helper as if installed at runtime, so the runtime root is that directory.
func runCLIFrom(t *testing.T, runtime, home string, args ...string) (map[string]any, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := NewRoot(filepath.Join(runtime, "bin", "sumctl"), &stdout, &stderr)
	root.SetArgs(append([]string{"--home", home, "--format", "json"}, args...))
	if err := root.ExecuteContext(context.Background()); err != nil {
		return nil, stderr.String() + err.Error(), err
	}
	return decodeObject(t, stdout.String()), "", nil
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func procedureRow(t *testing.T, view map[string]any) map[string]any {
	t.Helper()
	rows := asSlice(view["procedure"])
	if len(rows) != 1 {
		t.Fatalf("procedure = %v, want one role core", view["procedure"])
	}
	return asMap(rows[0])
}

func TestInitNamesEachRoleCoreFromTheRuntime(t *testing.T) {
	runtime := roleRuntime(t)
	home := writeDesignatedHome(t)
	herdrEnv(t, home)

	coordinator, errText, err := runCLIFrom(t, runtime, home, "init")
	if err != nil {
		t.Fatalf("init: %s", errText)
	}
	row := procedureRow(t, coordinator)
	core := filepath.Join(runtime, "COORDINATOR.md")
	if coordinator["role"] != "coordinator" || row["path"] != core || row["sha256"] != fileSHA(t, core) || row["load"] != procedure.Required || row["ok"] != true {
		t.Fatalf("coordinator init = role %v, procedure %v", coordinator["role"], row)
	}
	if !strings.Contains(asString(coordinator["note"]), "`procedure`") {
		t.Fatalf("coordinator note does not point at the core: %v", coordinator["note"])
	}

	t.Setenv("HERDR_PANE_ID", "w-second:p1")
	developer, errText, err := runCLIFrom(t, runtime, home, "init")
	if err != nil {
		t.Fatalf("second init: %s", errText)
	}
	row = procedureRow(t, developer)
	if developer["role"] != "developer" || row["name"] != procedure.Developer.Name || row["path"] != filepath.Join(runtime, procedure.Developer.Path) || row["ok"] != true {
		t.Fatalf("developer init = role %v, procedure %v", developer["role"], row)
	}

	// A developer claims no coordination, so a missing developer procedure is reported, not refused.
	if err := os.Remove(filepath.Join(runtime, procedure.Developer.Path)); err != nil {
		t.Fatal(err)
	}
	developer, errText, err = runCLIFrom(t, runtime, home, "init")
	if err != nil {
		t.Fatalf("developer init without its procedure: %s", errText)
	}
	row = procedureRow(t, developer)
	if developer["role"] != "developer" || row["ok"] != false || !strings.Contains(asString(row["error"]), procedure.Developer.Path+" is missing") {
		t.Fatalf("developer init without its procedure = role %v, procedure %v", developer["role"], row)
	}
}

func TestCoordinatorInitRefusesARuntimeWithoutItsCore(t *testing.T) {
	runtime := roleRuntime(t)
	core := filepath.Join(runtime, "COORDINATOR.md")
	body, err := os.ReadFile(core)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(core); err != nil {
		t.Fatal(err)
	}
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	contextPath := filepath.Join(home, "context.json")

	// First claim: refused before anything is recorded.
	if _, errText, err := runCLIFrom(t, runtime, home, "init"); err == nil || !strings.Contains(errText, "COORDINATOR.md is missing") || !strings.Contains(errText, "no coordinator role was granted") {
		t.Fatalf("first claim without the core = %v %s", err, errText)
	}
	if _, err := os.Stat(contextPath); err == nil {
		t.Fatal("a refused claim recorded a coordinator")
	}
	if entries, _ := os.ReadDir(filepath.Join(home, "sessions")); len(entries) != 0 {
		t.Fatalf("a refused claim registered the pane: %v", entries)
	}

	// The owner's own re-init is refused too, and leaves its record untouched.
	if err := os.WriteFile(core, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if view, errText, err := runCLIFrom(t, runtime, home, "init"); err != nil || view["role"] != "coordinator" {
		t.Fatalf("claim with the core restored = %v %s", view, errText)
	}
	before, _ := os.ReadFile(contextPath)
	if err := os.WriteFile(core, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errText, err := runCLIFrom(t, runtime, home, "init"); err == nil || !strings.Contains(errText, "COORDINATOR.md is empty") {
		t.Fatalf("owner re-init with an empty core = %v %s", err, errText)
	}

	// A reclaim of a verifiably replaced pane is refused as well: the old record stays.
	restartHerdr(t, "term-after-restart")
	if _, errText, err := runCLIFrom(t, runtime, home, "init", "--role", "coordinator", "--reclaim"); err == nil || !strings.Contains(errText, "COORDINATOR.md is empty") {
		t.Fatalf("reclaim with an empty core = %v %s", err, errText)
	}
	if after, _ := os.ReadFile(contextPath); string(after) != string(before) {
		t.Fatalf("a refused init changed the coordinator record:\nbefore %s\nafter  %s", before, after)
	}
}

func TestContractRevisionEmbedsTheCoordinatorCore(t *testing.T) {
	runtime := roleRuntime(t)
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	if _, errText, err := runCLIFrom(t, runtime, home, "init"); err != nil {
		t.Fatalf("init: %s", errText)
	}
	core := filepath.Join(runtime, "COORDINATOR.md")
	body, _ := os.ReadFile(core)
	contract := func(id string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(home, "coordinator", "contracts", id+".md"))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	if _, errText, err := runCLIFrom(t, runtime, home, "refresh", "request"); err != nil {
		t.Fatalf("refresh request: %s", errText)
	}
	r1 := contract("r1")
	agents, _ := os.ReadFile(filepath.Join(runtime, "AGENTS.md"))
	if !strings.Contains(r1, string(agents)) || !strings.Contains(r1, "## Coordinator core (COORDINATOR.md at this revision)\n\n"+string(body)) {
		t.Fatalf("contract r1 does not carry the bootstrap and the core:\n%s", r1)
	}

	// Changing only the core stages a new revision that says so.
	if err := os.WriteFile(core, append(append([]byte{}, body...), []byte("\nOne more coordinator rule.\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	result, errText, err := runCLIFrom(t, runtime, home, "refresh", "request")
	if err != nil {
		t.Fatalf("second refresh request: %s", errText)
	}
	if !strings.Contains(contract("r2"), "One more coordinator rule.") || !strings.Contains(asString(result["coordinator"])+stringsOf(result), "COORDINATOR.md changed") {
		t.Fatalf("second refresh = %v", result)
	}

	// A runtime without the core stages nothing.
	if err := os.Remove(core); err != nil {
		t.Fatal(err)
	}
	if _, errText, err := runCLIFrom(t, runtime, home, "refresh", "request"); err == nil || !strings.Contains(errText, "COORDINATOR.md is missing") || !strings.Contains(errText, "no coordinator contract revision was staged") {
		t.Fatalf("refresh without the core = %v %s", err, errText)
	}
	if _, err := os.Stat(filepath.Join(home, "coordinator", "contracts", "r3.md")); err == nil {
		t.Fatal("a revision was staged without the core")
	}
}

// stringsOf flattens a decoded view for substring checks on nested summaries.
func stringsOf(v any) string {
	switch x := v.(type) {
	case map[string]any:
		var parts []string
		for _, value := range x {
			parts = append(parts, stringsOf(value))
		}
		return strings.Join(parts, " ")
	case []any:
		var parts []string
		for _, value := range x {
			parts = append(parts, stringsOf(value))
		}
		return strings.Join(parts, " ")
	case string:
		return x
	}
	return ""
}
