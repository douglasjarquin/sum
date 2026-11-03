package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestInitWithPendingWorkDoesNotSelfDeadlock(t *testing.T) {
	root, s := ordinaryInitLab(t)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", store.Schema)
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("status", "waiting")
	question := ordjson.NewObject()
	question.Set("id", "q-one")
	question.Set("status", "open")
	question.Set("text", "continue?")
	task.Set("questions", []any{question})
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(s.Tasks, "t-aaaaaaaaaaaa", "task.json")
	before, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, ".local", "bin", "sumctl")
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	built, err := os.ReadFile(filepath.Join(repoRoot(t), ".local", "bin", "sumctl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, built, 0700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) map[string]any {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, append([]string{"--format", "json", "init"}, args...)...)
		cmd.Dir = root
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "SUM_") && !strings.HasPrefix(entry, "HC_") && !strings.HasPrefix(entry, "HERDR_") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("init did not return within its deadline: %v %s", err, output)
		}
		var view map[string]any
		if err := json.Unmarshal(output, &view); err != nil {
			t.Fatal(err)
		}
		if view["role"] != "coordinator" || asMap(view["outstanding"])["total"] != float64(1) {
			t.Fatalf("pending work vanished: %v", view)
		}
		return view
	}
	first := run()
	run("--claim", asString(first["claim"]))
	after, err := os.ReadFile(taskPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("reading pending work changed the task: %v", err)
	}
}
