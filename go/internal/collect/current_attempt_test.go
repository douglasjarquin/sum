package collect

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func TestCollectExitedWorkerRequiresCurrentAttemptReport(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		attempt string
		legacy  bool
		failure bool
	}{
		{"current attempt report", "x-aaaaaaaaaaaa", false, false},
		{"previous attempt report", "x-bbbbbbbbbbbb", false, true},
		{"historical report without endpoint attempt", "", false, true},
		{"legacy task report", "", true, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			l := newLab(t)
			endpoint := l.endpoint.Object()
			endpoint.Set("attempt", scenario.attempt)
			if scenario.attempt == "" {
				endpoint.Delete("attempt")
			}
			if scenario.legacy {
				report := ordjson.NewObject()
				report.Set("submitted_at", "2026-01-01T00:00:00Z")
				l.task.Set("report", report)
			} else {
				record, err := evidence.Append(l.task, "report", "worker", nil, strings.Repeat("a", 40), endpoint)
				if err != nil {
					t.Fatal(err)
				}
				if scenario.attempt == "" {
					object(record, "endpoint").Delete("attempt")
				}
			}
			assertExitedCollection(t, l, "worker", scenario.failure, strings.Repeat("a", 40))
		})
	}
}

func TestCollectExitedReviewerRequiresCurrentAttemptAndCandidateReview(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		attempt   string
		candidate string
		failure   bool
	}{
		{"current attempt and candidate review", "x-aaaaaaaaaaaa", strings.Repeat("a", 40), false},
		{"previous reviewer and candidate review", "x-bbbbbbbbbbbb", strings.Repeat("b", 40), true},
		{"previous reviewer current candidate review", "x-bbbbbbbbbbbb", strings.Repeat("a", 40), true},
		{"current reviewer previous candidate review", "x-aaaaaaaaaaaa", strings.Repeat("b", 40), true},
		{"historical review without endpoint attempt", "", strings.Repeat("a", 40), true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			l := newLab(t)
			l.endpoint.Role = "reviewer"
			l.task.Set("worker_endpoint", nil)
			reviewer := l.endpoint.Object()
			prompt := ordjson.NewObject()
			prompt.Set("candidate", strings.Repeat("a", 40))
			reviewer.Set("prompt", prompt)
			l.task.Set("reviewer", reviewer)
			endpoint := l.endpoint.Object()
			endpoint.Set("attempt", scenario.attempt)
			if scenario.attempt == "" {
				endpoint.Delete("attempt")
			}
			record, err := evidence.Append(l.task, "review", "reviewer", nil, scenario.candidate, endpoint)
			if err != nil {
				t.Fatal(err)
			}
			if scenario.attempt == "" {
				object(record, "endpoint").Delete("attempt")
			}
			assertExitedCollection(t, l, "reviewer", scenario.failure, scenario.candidate)
		})
	}
}

func assertExitedCollection(t *testing.T, l lab, role string, failure bool, candidate string) {
	t.Helper()
	if _, err := evidence.Append(l.task, "verification", "coordinator", nil, candidate, nil); err != nil {
		t.Fatal(err)
	}
	l.save(t)
	path, err := l.s.TaskPath(l.endpoint.Task)
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(path, "task.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	l.f.Exit(l.endpoint.Address, 1)
	out := l.run(t, time.Second)
	want := 0
	if failure {
		want = 1
	}
	if len(items(out)) != want || hasKind(out, "worker-failure") != failure || get(out, "deadline_hit") != false {
		t.Errorf("exited %s collect=%v, want %d failure items", role, out, want)
	}
	if failure && len(items(out)) == 1 {
		item, _ := items(out)[0].(*ordjson.Object)
		if get(item, "role") != role || get(item, "attempt") != l.endpoint.Attempt {
			t.Errorf("failure identifies wrong execution: %v", item)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("collection changed saved evidence or task history")
	}
}
