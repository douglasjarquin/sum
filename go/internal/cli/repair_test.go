package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepairUnknownFlagsAreUsageErrorsBeforeRepair(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "send unknown flag", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix", "--unexpected"}, unknown: true},
		{name: "send unknown flag before task", args: []string{"repair", "send", "--unexpected", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix"}, unknown: true},
		{name: "send extra positional", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix", "extra"}},
		{name: "send extra after dashdash", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix", "--", "extra"}},
		{name: "send missing --attempt value", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt"}},
		{name: "send missing --key value", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key"}},
		{name: "send missing --text value", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text"}},
		{name: "send --text and --file", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix", "--file", "note.md"}},
		{name: "extend unknown flag", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional", "2", "--approved", "--text", "grant", "--unexpected"}, unknown: true},
		{name: "extend extra after --text", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional", "2", "--approved", "--text", "grant", "extra"}},
		{name: "extend missing --additional value", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional"}},
		{name: "extend invalid --additional", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional", "nope", "--text", "grant"}},
		{name: "extend --text and --file", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional", "2", "--text", "grant", "--file", "note.md"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home, before := repairUsageLab(t)
			stdout, stderr, err := runRepairCLI(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertRepairUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			assertRepairTaskUnchanged(t, home, before)
		})
	}
}

func TestRepairCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		herdr     bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "valid send --text", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix"}, domainErr: "Herdr pane"},
		{name: "valid send --attempt= --key= --text=", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt=x-aaaaaaaaaaaa", "--key=k1", "--text=fix"}, domainErr: "Herdr pane"},
		{name: "valid send with herdr", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix"}, herdr: true, domainErr: "registered coordinator"},
		{name: "valid extend --approved --text", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional", "2", "--approved", "--text", "grant"}, domainErr: "Herdr pane"},
		{name: "valid extend --question= --additional= --approved=", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question=q-aaaaaaaaaa", "--additional=2", "--approved=true", "--text=grant"}, domainErr: "Herdr pane"},
		{name: "valid extend with herdr", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional", "2", "--approved", "--text", "grant"}, herdr: true, domainErr: "registered coordinator"},
		{name: "repair missing subcommand", args: []string{"repair"}, usage: true},
		{name: "send missing --attempt", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--key", "k1", "--text", "fix"}, usage: true},
		{name: "send missing --key", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--text", "fix"}, usage: true},
		{name: "send missing --text and --file", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1"}, usage: true},
		{name: "send --text and --file", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix", "--file", "note.md"}, usage: true},
		{name: "send unknown flag", args: []string{"repair", "send", "t-aaaaaaaaaaaa", "--attempt", "x-aaaaaaaaaaaa", "--key", "k1", "--text", "fix", "--unexpected"}, usage: true, unknown: true},
		{name: "extend missing --question", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--additional", "2", "--text", "grant"}, usage: true},
		{name: "extend missing --additional", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--text", "grant"}, usage: true},
		{name: "extend invalid --additional", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional", "nope", "--text", "grant"}, usage: true},
		{name: "extend unknown flag", args: []string{"repair", "extend", "t-aaaaaaaaaaaa", "--question", "q-aaaaaaaaaa", "--additional", "2", "--text", "grant", "--unexpected"}, usage: true, unknown: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, before := repairUsageLab(t)
			if tc.herdr {
				herdrEnv(t, home)
			} else {
				clearHerdrEnv(t)
			}
			stdout, stderr, err := runRepairCLI(t, home, tc.args...)
			switch {
			case tc.usage:
				assertRepairUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				assertRepairTaskUnchanged(t, home, before)
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s stderr=%s", stdout, stderr)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
				assertRepairTaskUnchanged(t, home, before)
			}
		})
	}
}

func repairUsageLab(t *testing.T) (home, before string) {
	t.Helper()
	home = writeDesignatedHome(t)
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "waiting", "repository": "owner/repo",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief_path": "brief.md"}`)
	before = readNamedTask(t, home, "t-aaaaaaaaaaaa")
	return home, before
}

func runRepairCLI(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertRepairUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "registered coordinator") || strings.Contains(msg, "Herdr pane") {
		t.Fatalf("err = %v, want a usage failure", err)
	}
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "accepts 1 arg") ||
		strings.Contains(msg, "accepts 0 arg") ||
		strings.Contains(msg, "flag needs an argument") ||
		strings.Contains(msg, "required flag") ||
		strings.Contains(msg, "if any flags in the group") ||
		strings.Contains(msg, "at least one of the flags") ||
		strings.Contains(msg, "command is required") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "invalid argument") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid repair")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func assertRepairTaskUnchanged(t *testing.T, home, before string) {
	t.Helper()
	if got := readNamedTask(t, home, "t-aaaaaaaaaaaa"); got != before {
		t.Fatalf("task.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}

const repairSendTask = "t-aaaaaaaaaaaa"
const repairSendAttempt = "x-aaaaaaaaaaaa"

// repairSendLab builds a lab where the coordinator is registered and a settled
// worker agent waits on pane w-worker:p1 in the fake Herdr session.
func repairSendLab(t *testing.T) (home, worktree string) {
	t.Helper()
	home = writeDesignatedHome(t)
	herdrEnv(t, home)
	if _, _, err := runRepairCLI(t, home, "init"); err != nil {
		t.Fatalf("init: %v", err)
	}
	worktree = t.TempDir()
	fakeRoot := filepath.Join(home, "fake-herdr")
	state := map[string]any{
		"panes": map[string]any{
			"w-worker:p1": map[string]any{
				"pane_id": "w-worker:p1", "cwd": worktree, "workspace_id": "w-worker",
				"agent_status": "idle", "agent": "claude", "created": true, "shell_pid": 4242,
			},
		},
		"workspaces": map[string]any{
			"w-worker": map[string]any{"workspace_id": "w-worker", "label": "task", "worktree": nil},
		},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fakeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeRoot, "state.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{host, "sum-test", "w-worker:p1"}, "\n")))
	key := hex.EncodeToString(sum[:])[:16]
	registration := fmt.Sprintf(`{"schema": 1, "key": %q, "role": "worker", "task": %q,
"machine": %q, "session": "sum-test", "pane": "w-worker:p1", "cwd": %q}`, key, repairSendTask, host, worktree)
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "sessions", key+".json"), []byte(registration), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTaskFixture(t, home, repairSendTask, fmt.Sprintf(`{"schema": 1, "id": %q, "status": "running", "repository": "owner/repo",
"machine": %q, "session": "sum-test", "pane": "w-worker:p1", "workspace": "w-worker",
"worktree": %q, "branch": "sum/t-aaaaaaaaaaaa",
"questions": [], "evidence": [], "report": {"text": "done"}, "notice": null, "attention": [],
"brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief_path": "brief.md",
"execution": {"schema": 1,
  "worker": {"id": %q, "kind": "worker", "state": "running", "generation": 1,
             "owner": {"machine": %q, "session": "sum-test", "pane": "w-worker:p1"}, "checkout": %q,
             "created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-01T00:00:00+00:00", "observations": []},
  "verifiers": []}}`, repairSendTask, host, worktree, repairSendAttempt, host, worktree))
	return home, worktree
}

// settleRepairWorker returns the fake worker pane to idle, like a worker that
// finished a turn and is settled for the next instruction.
func settleRepairWorker(t *testing.T, home string) {
	t.Helper()
	path := filepath.Join(home, "fake-herdr", "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	pane := state["panes"].(map[string]any)["w-worker:p1"].(map[string]any)
	pane["agent_status"] = "idle"
	out, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func repairLedger(t *testing.T, home string) map[string]any {
	t.Helper()
	raw := readNamedTask(t, home, repairSendTask)
	var task map[string]any
	if err := json.Unmarshal([]byte(raw), &task); err != nil {
		t.Fatal(err)
	}
	repairs, _ := task["repairs"].(map[string]any)
	if repairs == nil {
		t.Fatalf("task has no repairs ledger: %s", raw)
	}
	return repairs
}

func sendRepair(t *testing.T, home string, args ...string) (map[string]any, error) {
	t.Helper()
	settleRepairWorker(t, home)
	stdout, _, err := runRepairCLI(t, home, args...)
	if err != nil {
		return nil, err
	}
	return decodeCLIMap(t, stdout), nil
}

func TestRepairSend_classificationControlsCharging(t *testing.T) {
	home, _ := repairSendLab(t)

	// The default in-scope send is recorded but consumes nothing.
	view, err := sendRepair(t, home, "repair", "send", repairSendTask, "--attempt", repairSendAttempt, "--key", "k1", "--text", "rebase onto main")
	if err != nil {
		t.Fatalf("in-scope send: %v", err)
	}
	op := view["operation"].(map[string]any)
	if op["class"] != "in-scope" || op["state"] != "submitted" {
		t.Fatalf("operation = %v", op)
	}
	if got := repairLedger(t, home)["consumed"]; got != float64(0) {
		t.Fatalf("consumed = %v, want 0 after in-scope send", got)
	}

	// Repeating the same key replays the recorded operation; a different class is another instruction.
	view, err = sendRepair(t, home, "repair", "send", repairSendTask, "--attempt", repairSendAttempt, "--key", "k1", "--text", "rebase onto main")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if view["duplicate"] != true {
		t.Fatalf("replay = %v, want duplicate", view)
	}
	if _, err := sendRepair(t, home, "repair", "send", repairSendTask, "--attempt", repairSendAttempt, "--key", "k1", "--text", "rebase onto main", "--class", "expansion", "--reason", "new work"); err == nil {
		t.Fatal("same key with another class was accepted")
	}

	// Expansion sends are the only class that consumes the allowance.
	for _, key := range []string{"k2", "k3"} {
		view, err = sendRepair(t, home, "repair", "send", repairSendTask, "--attempt", repairSendAttempt, "--key", key, "--text", "extra work outside the brief", "--class", "expansion", "--reason", "user asked for more")
		if err != nil {
			t.Fatalf("expansion send %s: %v", key, err)
		}
		op = view["operation"].(map[string]any)
		if op["class"] != "expansion" || op["reason"] != "user asked for more" {
			t.Fatalf("operation = %v", op)
		}
	}
	if got := repairLedger(t, home)["consumed"]; got != float64(2) {
		t.Fatalf("consumed = %v, want 2 after two expansion sends", got)
	}

	// At exhaustion an expansion send saves the budget question and sends nothing.
	if _, err := sendRepair(t, home, "repair", "send", repairSendTask, "--attempt", repairSendAttempt, "--key", "k4", "--text", "even more", "--class", "expansion", "--reason", "still outside"); err == nil || !strings.Contains(err.Error(), "allowance is exhausted") {
		t.Fatalf("exhausted expansion send err = %v", err)
	}
	raw := readNamedTask(t, home, repairSendTask)
	var task map[string]any
	if err := json.Unmarshal([]byte(raw), &task); err != nil {
		t.Fatal(err)
	}
	questions, _ := task["questions"].([]any)
	if len(questions) != 1 {
		t.Fatalf("questions = %v", questions)
	}
	decision, _ := questions[0].(map[string]any)["decision"].(map[string]any)
	if decision["kind"] != "repair-allowance" {
		t.Fatalf("decision = %v", decision)
	}
	if got := repairLedger(t, home)["consumed"]; got != float64(2) {
		t.Fatalf("consumed = %v, want still 2", got)
	}
	if ops := repairLedger(t, home)["operations"].([]any); len(ops) != 3 {
		t.Fatalf("operations = %v, want the three recorded sends", ops)
	}

	// In-scope sends are never blocked by the exhausted allowance.
	view, err = sendRepair(t, home, "repair", "send", repairSendTask, "--attempt", repairSendAttempt, "--key", "k5", "--text", "fix the lint failure")
	if err != nil {
		t.Fatalf("in-scope send after exhaustion: %v", err)
	}
	if got := repairLedger(t, home)["consumed"]; got != float64(2) {
		t.Fatalf("consumed = %v, want still 2", got)
	}
}

func TestRepairSend_deliveryRefusalRecordsAndChargesNothing(t *testing.T) {
	home, _ := repairSendLab(t)
	t.Setenv("FAKE_FAIL_PROMPT_PANES", "w-worker:p1")

	// A hard refusal never reaches the worker: no operation, no consumption, same key retryable.
	if _, err := sendRepair(t, home, "repair", "send", repairSendTask, "--attempt", repairSendAttempt, "--key", "k1", "--text", "extra work", "--class", "expansion", "--reason", "outside the brief"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("refused expansion send err = %v", err)
	}
	repairs := repairLedger(t, home)
	if got := repairs["consumed"]; got != float64(0) {
		t.Fatalf("consumed = %v after refusal, want 0", got)
	}
	if ops := repairs["operations"].([]any); len(ops) != 0 {
		t.Fatalf("operations = %v after refusal, want none", ops)
	}

	t.Setenv("FAKE_FAIL_PROMPT_PANES", "")
	view, err := sendRepair(t, home, "repair", "send", repairSendTask, "--attempt", repairSendAttempt, "--key", "k1", "--text", "extra work", "--class", "expansion", "--reason", "outside the brief")
	if err != nil {
		t.Fatalf("retry after refusal: %v", err)
	}
	if view["duplicate"] != false {
		t.Fatalf("retry = %v, want a fresh send", view)
	}
	if got := repairLedger(t, home)["consumed"]; got != float64(1) {
		t.Fatalf("consumed = %v after delivered retry, want 1", got)
	}
}

func TestRepairSend_uncertainExpansionStaysCharged(t *testing.T) {
	home, _ := repairSendLab(t)
	t.Setenv("FAKE_PROMPT_HANG", "10")

	// The prompt reached the pane but the answer timed out: uncertain stays recorded and charged.
	if _, err := sendRepair(t, home, "repair", "send", repairSendTask, "--attempt", repairSendAttempt, "--key", "k1", "--text", "extra work", "--class", "expansion", "--reason", "outside the brief"); err == nil || !strings.Contains(err.Error(), "remains charged") {
		t.Fatalf("uncertain expansion send err = %v", err)
	}
	repairs := repairLedger(t, home)
	if got := repairs["consumed"]; got != float64(1) {
		t.Fatalf("consumed = %v after uncertain send, want 1", got)
	}
	ops := repairs["operations"].([]any)
	if len(ops) != 1 || ops[0].(map[string]any)["state"] != "uncertain" {
		t.Fatalf("operations = %v after uncertain send", ops)
	}

	// The same key replays the recorded operation instead of risking a second delivery.
	view, err := sendRepair(t, home, "repair", "send", repairSendTask, "--attempt", repairSendAttempt, "--key", "k1", "--text", "extra work", "--class", "expansion", "--reason", "outside the brief")
	if err != nil {
		t.Fatalf("uncertain replay: %v", err)
	}
	if view["duplicate"] != true {
		t.Fatalf("replay = %v, want duplicate", view)
	}
}

func TestRepairSend_classFlagValidation(t *testing.T) {
	home, _ := repairSendLab(t)
	before := readNamedTask(t, home, repairSendTask)

	if _, _, err := runRepairCLI(t, home, "repair", "send", repairSendTask, "--attempt", repairSendAttempt, "--key", "k1", "--text", "x", "--class", "bogus"); err == nil || !strings.Contains(err.Error(), "invalid repair class") {
		t.Fatalf("err = %v, want invalid class", err)
	}
	if _, _, err := runRepairCLI(t, home, "repair", "send", repairSendTask, "--attempt", repairSendAttempt, "--key", "k1", "--text", "x", "--class", "expansion"); err == nil || !strings.Contains(err.Error(), "--reason is required") {
		t.Fatalf("err = %v, want --reason requirement", err)
	}
	if got := readNamedTask(t, home, repairSendTask); got != before {
		t.Fatal("flag validation mutated the task")
	}
}
