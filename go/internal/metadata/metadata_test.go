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

func TestProjectionCountsOnlyHumanDecisionsAndShowsUnknownCoverage(t *testing.T) {
	for _, damaged := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy", true: "unreadable neighbor"}[damaged], func(t *testing.T) {
			home := t.TempDir()
			taskdir := filepath.Join(home, "tasks", "t-aaaaaaaaaaaa")
			if err := os.MkdirAll(taskdir, 0700); err != nil {
				t.Fatal(err)
			}
			task := map[string]any{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo", "pane": "worker", "questions": []any{map[string]any{"id": "q-one", "status": "open"}, map[string]any{"id": "q-two", "status": "answered", "answer": "yes"}}, "evidence": []any{}, "attention": []any{}}
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
			runtime := t.TempDir()
			if err := os.MkdirAll(filepath.Join(runtime, ".local", "bin"), 0700); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(t.TempDir(), "calls")
			t.Setenv("PROJECTION_TEST_LOG", log)
			t.Setenv("SUM_HERDR_BIN", filepath.Join(runtime, ".local", "bin", "herdr"))
			if err := os.WriteFile(filepath.Join(runtime, ".local", "bin", "herdr"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$PROJECTION_TEST_LOG\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			st, err := store.Open(home)
			if err != nil {
				t.Fatal(err)
			}
			ctx := ordjson.NewObject()
			ctx.Set("session", "sum-test-projection")
			ctx.Set("pane", "root")
			snapshot := inboxview.Read(st)
			if snapshot.Counts.Decisions != 1 || snapshot.Counts.Worker != 1 {
				t.Fatalf("fixture counts: %+v", snapshot.Counts)
			}
			if err := project(st, ctx, runtime); err != nil {
				t.Fatal(err)
			}
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
			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			taskdir := filepath.Join(st.Home, "tasks", "t-aaaaaaaaaaaa")
			if err := os.MkdirAll(taskdir, 0700); err != nil {
				t.Fatal(err)
			}
			raw := `{"schema":1,"id":"t-aaaaaaaaaaaa","status":"running","pane":"worker","parent":{"pane":"root"},` + tc.extra + `}`
			if err := os.WriteFile(filepath.Join(taskdir, "task.json"), []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.sidecar != "" {
				if err := os.WriteFile(filepath.Join(taskdir, "pipeline.json"), []byte(tc.sidecar), 0600); err != nil {
					t.Fatal(err)
				}
			}
			runtime := t.TempDir()
			log := filepath.Join(runtime, "calls")
			t.Setenv("PROJECTION_TEST_LOG", log)
			t.Setenv("SUM_HERDR_BIN", filepath.Join(runtime, ".local", "bin", "herdr"))
			if err := os.MkdirAll(filepath.Join(runtime, ".local", "bin"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(runtime, ".local", "bin", "herdr"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$PROJECTION_TEST_LOG\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			ctx := ordjson.NewObject()
			ctx.Set("session", "sum-test-projection")
			ctx.Set("pane", "root")
			if err := project(st, ctx, runtime); err != nil {
				t.Fatal(err)
			}
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

func TestInboxTokenKeepsUnknownAndDecisionsWithinNativeLimit(t *testing.T) {
	for _, n := range []int{100, 999999999} {
		snapshot := inboxview.Snapshot{Counts: inboxview.Counts{Decisions: n, Worker: n, Coordinator: 2 * n, Inspection: n}}
		token := inboxToken(snapshot)
		if len(token) > 80 || !strings.HasPrefix(token, "unknown;") || !strings.Contains(token, fmt.Sprintf("%d decisions", n)) {
			t.Fatalf("invalid native token: %q", token)
		}
	}
}
