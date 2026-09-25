package refreshcmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// A refresh instruction is sent only to the recorded occupant of a target pane: a pane Herdr restored under a new
// server with a new shell keeps its old contract and is told nothing.
func TestAttemptDeliveryRefusesAReplacedTargetPane(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fake := t.TempDir()
	worktree := t.TempDir()
	for k, v := range map[string]string{
		"SUM_HERDR_BIN": filepath.Join(root, "tests", "fixtures", "herdr.py"), "SUM_PS_BIN": filepath.Join(root, "tests", "fixtures", "ps.py"),
		"FAKE_HERDR_ROOT": fake, "FAKE_SESSION": "sum-test", "HERDR_PANE_ID": "w-parent:p1",
		"FAKE_PS_STARTS": `{"5151": "2030-01-01T00:00:00Z"}`,
	} {
		t.Setenv(k, v)
	}
	pane := map[string]any{"pane_id": "w1:p1", "cwd": worktree, "workspace_id": "w1", "agent_status": "idle", "agent": "codex",
		"created": true, "terminal_id": "term-after-restart", "shell_pid": 5151}
	raw, _ := json.Marshal(map[string]any{"panes": map[string]any{"w1:p1": pane}, "workspaces": map[string]any{}})
	if err := os.WriteFile(filepath.Join(fake, "state.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := machine.Local(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	endpoint := ordjson.NewObject()
	endpoint.Set("machine", host.ID)
	endpoint.Set("session", "sum-test")
	endpoint.Set("pane", "w1:p1")
	bound := incarnation.Evidence{Terminal: "term-w1:p1", Shell: &incarnation.Shell{PID: 4242, Started: "2025-01-01T00:00:00Z"}}.Record(store.Now())
	record := occupantRecord{role: "worker", value: bound, occupiedAt: store.Now(), ok: true}

	row := attemptDelivery(nil, newSnapshots(root), endpoint, worktree, "sum refresh t-x: read the revision", host, record, nil)
	if state, _ := row.Get("state"); state != "pending-unreachable" {
		t.Fatalf("state = %v (%v), want pending-unreachable", state, row)
	}
	if reason, _ := row.Get("reason"); !strings.Contains(reason.(string), incarnation.Replaced) {
		t.Fatalf("reason = %v, want the replaced outcome", reason)
	}
	calls, _ := os.ReadFile(filepath.Join(fake, "calls.jsonl"))
	if strings.Contains(string(calls), `"prompt"`) {
		t.Fatalf("a prompt reached the restored pane: %s", calls)
	}

	// The recorded occupant itself is still instructed.
	pane["terminal_id"], pane["shell_pid"] = "term-w1:p1", 4242
	raw, _ = json.Marshal(map[string]any{"panes": map[string]any{"w1:p1": pane}, "workspaces": map[string]any{}})
	if err := os.WriteFile(filepath.Join(fake, "state.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	row = attemptDelivery(nil, newSnapshots(root), endpoint, worktree, "sum refresh t-x: read the revision", host, record, nil)
	if state, _ := row.Get("state"); state != "submitted-unconfirmed" {
		t.Fatalf("recorded occupant state = %v (%v), want submitted-unconfirmed", state, row)
	}

	// A live handoff (a new terminal, the same shell) is still the recorded occupant.
	t.Setenv("FAKE_PS_STARTS", `{"4242": "2025-01-01T00:00:00Z"}`)
	pane["terminal_id"], pane["agent_status"] = "term-after-handoff", "idle"
	raw, _ = json.Marshal(map[string]any{"panes": map[string]any{"w1:p1": pane}, "workspaces": map[string]any{}})
	if err := os.WriteFile(filepath.Join(fake, "state.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	row = attemptDelivery(nil, newSnapshots(root), endpoint, worktree, "sum refresh t-x: read the revision", host, record, nil)
	if state, _ := row.Get("state"); state != "submitted-unconfirmed" {
		t.Fatalf("handoff state = %v (%v), want submitted-unconfirmed", state, row)
	}

	// A record rebound while the recipient was observed sends nothing.
	pane["agent_status"] = "idle"
	raw, _ = json.Marshal(map[string]any{"panes": map[string]any{"w1:p1": pane}, "workspaces": map[string]any{}})
	if err := os.WriteFile(filepath.Join(fake, "state.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record.current = func() (any, error) {
		return incarnation.Evidence{Terminal: "term-someone-else"}.Record(store.Now()), nil
	}
	before := strings.Count(func() string { b, _ := os.ReadFile(filepath.Join(fake, "calls.jsonl")); return string(b) }(), `"prompt"`)
	row = attemptDelivery(st, newSnapshots(root), endpoint, worktree, "sum refresh t-x: read the revision", host, record, nil)
	after := strings.Count(func() string { b, _ := os.ReadFile(filepath.Join(fake, "calls.jsonl")); return string(b) }(), `"prompt"`)
	if state, _ := row.Get("state"); state != "pending-unreachable" || after != before {
		t.Fatalf("rebound record = %v with %d new prompts, want pending-unreachable and none", row, after-before)
	}
}

func TestAttemptDeliveryNamesARecordedClosedPane(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	host, err := machine.Local(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	endpoint := ordjson.NewObject()
	endpoint.Set("machine", host.ID)
	endpoint.Set("session", "sum-test")
	endpoint.Set("pane", "w1:p1")
	task := ordjson.NewObject()
	task.Set("pane", "w1:p1")
	closed := ordjson.NewObject()
	closed.Set("pane", "w1:p1")
	closed.Set("role", "worker")
	closed.Set("reason", "report-submitted")
	closed.Set("at", "2026-01-01T00:00:00+00:00")
	task.Set("closed_panes", []any{closed})
	record := occupantRecord{role: "worker", ok: true}
	row := attemptDelivery(nil, newSnapshots(root), endpoint, t.TempDir(), "sum refresh t-x: read the revision", host, record, task)
	if state, _ := row.Get("state"); state != "pane-closed" {
		t.Fatalf("state = %v (%v), want pane-closed", state, row)
	}
}
