package metadata

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/inboxview"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// scriptedHerdr is a logging fake: every argv line is appended to PROJECTION_TEST_LOG, `pane get` answers with the
// cwd in FAKE_CWD and a fixed terminal, and every other call succeeds silently like 0.9.0 report-metadata.
func scriptedHerdr(t *testing.T, cwd string) (runtime, log string) {
	t.Helper()
	runtime = t.TempDir()
	log = filepath.Join(runtime, "calls")
	t.Setenv("PROJECTION_TEST_LOG", log)
	t.Setenv("FAKE_CWD", cwd)
	bin := filepath.Join(runtime, ".local", "bin", "herdr")
	t.Setenv("SUM_HERDR_BIN", bin)
	if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$PROJECTION_TEST_LOG\"\n" +
		"case \"$*\" in *'pane get'*) printf '{\"result\":{\"pane\":{\"pane_id\":\"x\",\"cwd\":\"%s\",\"terminal_id\":\"term-root\"}}}\\n' \"$FAKE_CWD\";; esac\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return runtime, log
}

// coordinatorHome opens home with a recorded coordinator owner whose pane the scripted fake verifies.
func coordinatorHome(t *testing.T, home, cwd string) *store.Store {
	t.Helper()
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	host, err := st.Machine()
	if err != nil {
		t.Fatal(err)
	}
	owner := map[string]any{"role": "coordinator", "machine": host.ID, "session": "sum-test-projection", "pane": "root", "cwd": cwd,
		"claimed_at": "2026-01-01T00:00:00+00:00", "incarnation": map[string]any{"terminal": "term-root", "observed_at": "2026-01-01T00:00:00+00:00"}}
	raw, _ := json.Marshal(owner)
	if err := os.WriteFile(filepath.Join(home, "context.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return st
}

func enabledMeta() *ordjson.Object {
	meta := emptyMetadata()
	meta.Set("enabled", true)
	meta.Set("source", "sum:testinstance")
	caps := ordjson.NewObject()
	caps.Set("pane_tokens", true)
	caps.Set("workspace_tokens", true)
	meta.Set("capabilities", caps)
	return meta
}

func runPass(t *testing.T, st *store.Store, meta *ordjson.Object, runtime string) *ordjson.Object {
	t.Helper()
	p := newPass(st, meta, filepath.Join(runtime, ".local", "bin", "herdr"), false)
	return p.run(nil, "test")
}

func countLines(t *testing.T, log, exact string) int {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if line == exact {
			n++
		}
	}
	return n
}

func TestProjectionCountsOnlyHumanDecisionsAndShowsUnknownCoverage(t *testing.T) {
	for _, damaged := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy", true: "unreadable neighbor"}[damaged], func(t *testing.T) {
			home := t.TempDir()
			worktree := t.TempDir()
			taskdir := filepath.Join(home, "tasks", "t-aaaaaaaaaaaa")
			if err := os.MkdirAll(taskdir, 0700); err != nil {
				t.Fatal(err)
			}
			task := map[string]any{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo", "pane": "worker", "session": "sum-test-projection", "worktree": worktree, "questions": []any{map[string]any{"id": "q-one", "status": "open"}, map[string]any{"id": "q-two", "status": "answered", "answer": "yes"}}, "evidence": []any{}, "attention": []any{}}
			raw, _ := json.Marshal(task)
			if err := os.WriteFile(filepath.Join(taskdir, "task.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			if damaged {
				bad := filepath.Join(home, "tasks", "t-bbbbbbbbbbbb")
				if err := os.MkdirAll(bad, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(bad, "task.json"), []byte("bad"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			runtime, log := scriptedHerdr(t, worktree)
			st := coordinatorHome(t, home, worktree)
			snapshot := inboxview.Read(st)
			if snapshot.Counts.Decisions != 1 || snapshot.Counts.Worker != 1 {
				t.Fatalf("fixture counts: %+v", snapshot.Counts)
			}
			meta := enabledMeta()
			result := runPass(t, st, meta, runtime)
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			want := "sum_inbox=1 decision; 1 worker"
			if damaged {
				want = "sum_inbox=unknown; 1 decision; 1 worker; 1 inspection"
			}
			if !strings.Contains(string(calls), want+"\n") || strings.Contains(string(calls), "sum_inbox=clear") {
				t.Fatalf("projection must agree with snapshot counts/coverage: %s", calls)
			}
			rootRow, _ := result.Get("root")
			outcome, _ := rootRow.(*ordjson.Object).Get("outcome")
			if outcome != "written" {
				t.Fatalf("root row: %v", rootRow)
			}
			if countLines(t, log, "report-metadata") != 2 {
				t.Fatalf("one write per endpoint (worker pane, coordinator pane): %s", calls)
			}
			// The same facts again: nothing differs, nothing is written, the panes are not even observed.
			before := len(calls)
			runPass(t, st, meta, runtime)
			after, _ := os.ReadFile(log)
			if len(after) != before {
				t.Fatalf("unchanged pass called Herdr: %s", after[before:])
			}
		})
	}
}

func TestProjectionRoutineInspectionAndPassiveStates(t *testing.T) {
	cases := []struct{ name, extra, sidecar, state, inbox string }{
		{"report", `"evidence":[{"id":"e1","kind":"report","at":"1"}]`, "", "review-ready", "1 coordinator"},
		{"review", `"evidence":[{"id":"e1","kind":"review","candidate":"sha","verdict":"reject","at":"1"}]`, "", "review-ready", "1 coordinator"},
		{"native attention", `"attention":[{"id":"a1","kind":"blocked","status":"open","at":"1"}]`, "", "needs-attention", "1 inspection"},
		{"answered", `"questions":[{"id":"q1","status":"answered","answer":"yes"}]`, "", "answer-pending", "1 worker"},
		{"passive pipeline", `"questions":[]`, `{"schema":1,"task":"t-aaaaaaaaaaaa","candidate":"sha","rows":[{"stage":"test","status":"pass"}]}`, "running", "clear"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			worktree := t.TempDir()
			taskdir := filepath.Join(home, "tasks", "t-aaaaaaaaaaaa")
			if err := os.MkdirAll(taskdir, 0700); err != nil {
				t.Fatal(err)
			}
			raw := `{"schema":1,"id":"t-aaaaaaaaaaaa","status":"running","pane":"worker","session":"sum-test-projection","worktree":` + strconv(worktree) + `,"parent":{"pane":"root"},` + tc.extra + `}`
			if err := os.WriteFile(filepath.Join(taskdir, "task.json"), []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.sidecar != "" {
				if err := os.WriteFile(filepath.Join(taskdir, "pipeline.json"), []byte(tc.sidecar), 0600); err != nil {
					t.Fatal(err)
				}
			}
			runtime, log := scriptedHerdr(t, worktree)
			st := coordinatorHome(t, home, worktree)
			runPass(t, st, enabledMeta(), runtime)
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"sum_state=" + tc.state + "\n", "sum_inbox=" + tc.inbox + "\n"} {
				if !strings.Contains(string(calls), want) {
					t.Fatalf("missing %q: %s", want, calls)
				}
			}
		})
	}
}

func strconv(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

func TestProjectionSkipsWithoutCapabilityAndWritesOnlyVerifiedPanes(t *testing.T) {
	home := t.TempDir()
	worktree := t.TempDir()
	taskdir := filepath.Join(home, "tasks", "t-aaaaaaaaaaaa")
	if err := os.MkdirAll(taskdir, 0700); err != nil {
		t.Fatal(err)
	}
	raw := `{"schema":1,"id":"t-aaaaaaaaaaaa","status":"running","pane":"worker","workspace":"w-1","session":"sum-test-projection","worktree":"/elsewhere/checkout","questions":[]}`
	if err := os.WriteFile(filepath.Join(taskdir, "task.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	runtime, log := scriptedHerdr(t, worktree)
	st := coordinatorHome(t, home, worktree)
	meta := enabledMeta()
	result := runPass(t, st, meta, runtime)
	rows, _ := result.Get("tasks")
	endpoints, _ := rows.([]any)[0].(*ordjson.Object).Get("endpoints")
	outcomes := map[string]string{}
	for _, e := range endpoints.([]any) {
		kind, _ := e.(*ordjson.Object).Get("kind")
		outcome, _ := e.(*ordjson.Object).Get("outcome")
		outcomes[kind.(string)] = outcome.(string)
	}
	if outcomes["pane"] != "stale" || outcomes["workspace"] != "written" {
		t.Fatalf("a pane in another checkout is stale, the recorded workspace is written: %v", outcomes)
	}
	calls, _ := os.ReadFile(log)
	if strings.Contains(string(calls), "worker\n--source") {
		t.Fatalf("stale pane must not be written: %s", calls)
	}
	resources, _ := meta.Get("resources")
	rec, _ := resources.(*ordjson.Object).Get("t-aaaaaaaaaaaa")
	if _, has := rec.(*ordjson.Object).Get("pane"); has {
		t.Fatalf("stale pane must not be recorded: %v", rec)
	}

	caps := ordjson.NewObject()
	caps.Set("pane_tokens", false)
	caps.Set("workspace_tokens", false)
	meta.Set("capabilities", caps)
	meta.Set("resources", ordjson.NewObject())
	meta.Set("root", nil)
	before, _ := os.ReadFile(log)
	result = runPass(t, st, meta, runtime)
	rootRow, _ := result.Get("root")
	if outcome, _ := rootRow.(*ordjson.Object).Get("outcome"); outcome != "unsupported" {
		t.Fatalf("no capability: %v", rootRow)
	}
	if after, _ := os.ReadFile(log); len(after) != len(before) {
		t.Fatalf("unsupported endpoints must not reach Herdr: %s", after[len(before):])
	}
}

func TestInboxTokenKeepsUnknownAndDecisionsWithinNativeLimit(t *testing.T) {
	for _, n := range []int{100, 999999999} {
		snapshot := inboxview.Snapshot{Counts: inboxview.Counts{Decisions: n, Worker: n, Coordinator: 2 * n, Inspection: n}}
		token := inboxToken(snapshot)
		if len(token) > 80 || !strings.HasPrefix(token, "unknown;") || !strings.Contains(token, fmt.Sprintf("%d decisions", n)) {
			t.Fatalf("invalid native token: %q", token)
		}
	}
}

func TestSourceDerivesFromInstanceNotHomeBasename(t *testing.T) {
	for _, name := range []string{"a.sum", "b.sum"} {
		home := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(home, 0o700); err != nil {
			t.Fatal(err)
		}
		st, err := store.Open(home)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema":1}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Source(st); err == nil || !strings.Contains(err.Error(), "no identity") {
			t.Fatalf("no instance must refuse: %v", err)
		}
		if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema":1,"instance":"`+name+`0123456789abcdef"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		source, err := Source(st)
		if err != nil || source != "sum:"+(name + "0123456789abcdef")[:12] {
			t.Fatalf("source %q %v", source, err)
		}
	}
}
