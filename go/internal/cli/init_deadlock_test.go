package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Regression: init and bind used to hold .sum/.lock across returns.Pump, whose
// delivery stamping re-acquires the same flock through a fresh descriptor - a
// permanent self-deadlock whenever a delivery pass had real work. The commands
// must run as a child under a deadline: a flock blocked in-process cannot be
// interrupted by context cancellation.
func TestInitAndBindDeliveryPassDoNotSelfDeadlock(t *testing.T) {
	root, helper := repoReference(t)
	base := t.TempDir()
	home := filepath.Join(base, "state")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &demoLab{t: t, root: root, helper: helper, home: home, base: base, env: demoEnv(t, root, base)}
	if got := d.ctl(true, "init")["role"]; got != "coordinator" {
		t.Fatalf("init role = %v, want coordinator", got)
	}

	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	fixture := func(id, questionID string) string {
		return fmt.Sprintf(`{"schema": 1, "id": %q, "status": "running", "kind": "task", "machine": %q, "session": "sum-test", "pane": "w-worker:p1",
 "parent": {"machine": %q, "session": "sum-test", "pane": "w-parent:p1", "cwd": %q},
 "questions": [{"id": %q, "key": "k", "text": "still blocked?", "status": "open", "created_at": "2026-01-01T00:00:00+00:00"}],
 "attention": [], "evidence": [], "report": null, "notice": null, "brief": "worker task", "brief_path": "brief.md"}`,
			id, host, host, root, questionID)
	}
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", fixture("t-aaaaaaaaaaaa", "q-aaaa"))
	writeTaskFixture(t, home, "t-bbbbbbbbbbbb", fixture("t-bbbbbbbbbbbb", "q-bbbb"))

	const deadline = 20 * time.Second
	bound := func(args ...string) map[string]any {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), deadline)
		defer cancel()
		cmd := exec.CommandContext(ctx, d.helper, append([]string{"--format", "json", "--home", d.home}, args...)...)
		cmd.Env = d.env
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		runErr := cmd.Run()
		if ctx.Err() == context.DeadlineExceeded {
			t.Fatalf("sumctl %s did not finish within %s: the delivery pass deadlocked on .sum/.lock\n%s%s",
				strings.Join(args, " "), deadline, stdout.String(), stderr.String())
		}
		if runErr != nil {
			t.Fatalf("sumctl %s: %v\n%s%s", strings.Join(args, " "), runErr, stdout.String(), stderr.String())
		}
		raw := stdout.Bytes()
		if len(bytes.TrimSpace(raw)) == 0 {
			raw = stderr.Bytes()
		}
		var out map[string]any
		if unmarshalErr := json.Unmarshal(raw, &out); unmarshalErr != nil {
			t.Fatalf("json %s: %v\n%s", strings.Join(args, " "), unmarshalErr, raw)
		}
		return out
	}
	assertSubmitted := func(out map[string]any, cmd string) {
		t.Helper()
		returns, _ := out["returns"].(map[string]any)
		recipients, _ := returns["recipients"].([]any)
		if len(recipients) == 0 {
			t.Fatalf("%s delivered no returns; the regression needs a pending obligation", cmd)
		}
		row, _ := recipients[0].(map[string]any)
		if row["state"] != "submitted" || row["via"] != "inline" {
			t.Fatalf("%s returns row = %v, want submitted/inline", cmd, row)
		}
	}

	// bind re-saves the parent route and pumps inline for this task's open
	// question; it used to hold .lock across the pump and hang here.
	assertSubmitted(bound("bind", "t-bbbbbbbbbbbb", "--parent-only"), "bind")

	// init's post-registration delivery pass still owes task A's question to this
	// pane; it used to hang while holding both .lock and .deliver.lock.
	assertSubmitted(bound("init"), "init")
}
