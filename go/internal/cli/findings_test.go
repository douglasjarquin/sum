package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestFindingCommandsCaptureAndRequireCoordinatorForDisposition(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	host, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	parent := ordjson.NewObject()
	parent.Set("machine", host)
	parent.Set("session", "sum-test")
	parent.Set("pane", "w-parent:p1")
	task := ordjson.NewObject()
	task.Set("schema", json.Number("1"))
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("created_at", store.Now())
	task.Set("status", "running")
	task.Set("machine", host)
	task.Set("session", "sum-test")
	task.Set("pane", "w-worker:p1")
	task.Set("parent", parent)
	project := ordjson.NewObject()
	project.Set("host", "github.com")
	project.Set("owner", "team")
	project.Set("repo", "repo")
	task.Set("project", project)
	task.Set("brief", "approved work")
	if err := os.MkdirAll(filepath.Join(home, "tasks", "t-aaaaaaaaaaaa"), 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PANE_ID", "w-worker:p1")
	stdout, err := runCLI(t, home, "--format", "json", "finding", "record", "t-aaaaaaaaaaaa", "--key", "k1", "--title", "A finding", "--explanation", "A specific explanation", "--next-step", "Inspect the source", "--reference", "source.go")
	if err != nil {
		t.Fatalf("record: %v: %s", err, stdout)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	finding := result["finding"].(map[string]any)
	if finding["task_id"] != "t-aaaaaaaaaaaa" || finding["project_id"] != "team/repo" || result["duplicate"] != false {
		t.Fatalf("record output: %#v", result)
	}
	id := finding["id"].(string)
	if _, err := runCLI(t, home, "finding", "defer", id, "--reason", "later"); err == nil {
		t.Fatal("worker changed a finding disposition")
	}
	t.Setenv("HERDR_PANE_ID", "w-parent:p1")
	if _, err := runCLI(t, home, "finding", "defer", id, "--reason", "later"); err != nil {
		t.Fatalf("coordinator defer: %v", err)
	}
	view, err := runCLI(t, home, "--format", "json", "finding", "list", "t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	var listed map[string]any
	if err := json.Unmarshal([]byte(view), &listed); err != nil {
		t.Fatal(err)
	}
	rows := listed["findings"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["disposition"] != "deferred" {
		t.Fatalf("list output: %#v", listed)
	}
	context, err := runCLI(t, home, "--format", "json", "context", "t-aaaaaaaaaaaa", "--section", "findings")
	if err != nil {
		t.Fatal(err)
	}
	var contextView map[string]any
	if err := json.Unmarshal([]byte(context), &contextView); err != nil {
		t.Fatal(err)
	}
	contextFindings := contextView["findings"].(map[string]any)["findings"].([]any)
	if len(contextFindings) != 1 || contextFindings[0].(map[string]any)["next_step"] != "Inspect the source" {
		t.Fatalf("context findings: %#v", contextView)
	}
	shown, err := runCLI(t, home, "--format", "json", "show", "t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	var showView map[string]any
	if err := json.Unmarshal([]byte(shown), &showView); err != nil {
		t.Fatal(err)
	}
	showFindings := showView["findings"].(map[string]any)["findings"].([]any)
	if len(showFindings) != 1 || showFindings[0].(map[string]any)["disposition"] != "deferred" {
		t.Fatalf("show findings: %#v", showView)
	}
	all, err := st.AllTasks()
	if err != nil || len(all) != 1 {
		t.Fatalf("capture created task: count=%d err=%v", len(all), err)
	}
}
