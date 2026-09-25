package cli

import (
	"encoding/json"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func presentationHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	questions := []map[string]any{
		{"id": "q-answered", "status": "answered", "text": strings.Repeat("answered ", 100), "answer": "Proceed", "created_at": "2026-01-01T00:00:00Z"},
		{"id": "q-first", "status": "open", "text": strings.Repeat("界", 700) + " $(touch should-not-exist)", "created_at": "2026-01-02T00:00:00Z"},
		{"id": "q-last", "status": "open", "text": "Which path?", "created_at": "2026-01-03T00:00:00Z"},
	}
	path := filepath.Join(home, "tasks", "t-aaaaaaaaaaaa", "task.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo", "questions": questions, "attention": []any{}, "evidence": []any{}, "report": nil})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return home
}

func compactRead(t *testing.T, home string, args ...string) map[string]any {
	t.Helper()
	out, stderr, err := runStatus(t, home, append([]string{"--format", "json"}, args...)...)
	if err != nil {
		t.Fatalf("%v: %v %s", args, err, stderr)
	}
	var view map[string]any
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func TestCompactDecisionsPagingAndFullDetail(t *testing.T) {
	clearHerdrEnv(t)
	home := presentationHome(t)
	full := compactRead(t, home, "status")
	if got := len(full["tasks"].([]any)[0].(map[string]any)["questions"].([]any)); got != 3 {
		t.Fatalf("full questions = %d", got)
	}
	for _, command := range [][]string{{"status", "--compact"}, {"inbox", "--compact"}, {"metadata", "inbox"}} {
		first := compactRead(t, home, append(command, "--limit", "1", "--max-chars", "40")...)
		counts := first["counts"].(map[string]any)
		if counts["decisions"] != float64(2) || counts["worker"] != float64(1) || first["complete"] != true {
			t.Fatalf("counts/coverage: %v", first)
		}
		page := first["page"].(map[string]any)
		if page["omitted"].(float64) < 2 || page["next_after"] != float64(1) {
			t.Fatalf("page: %v", page)
		}
		item := page["items"].([]any)[0].(map[string]any)
		text := item["text"].(map[string]any)
		if item["presentation"] != "decision" || text["truncated"] != true || len([]rune(text["text"].(string))) != 40 {
			t.Fatalf("item: %v", item)
		}
		detailArgs := stringArgs(item["detail"])
		detail := compactRead(t, home, detailArgs...)
		decision := detail["decisions"].(map[string]any)["items"].([]any)[0].(map[string]any)
		if decision["id"] != "q-first" || decision["text"].(map[string]any)["truncated"] != false {
			t.Fatalf("detail: %v", decision)
		}
		if !reflect.DeepEqual(stringArgs(item["answer"]), []string{"answer", "t-aaaaaaaaaaaa", "q-first", "--text"}) {
			t.Fatalf("answer route: %v", item["answer"])
		}
		second := compactRead(t, home, append(command, "--limit", "1", "--after", "1")...)
		if second["counts"].(map[string]any)["decisions"] != float64(2) {
			t.Fatal("page lost global count")
		}
		last := compactRead(t, home, append(command, "--limit", "1", "--after", "2")...)
		worker := last["page"].(map[string]any)["items"].([]any)[0].(map[string]any)
		if worker["text"].(map[string]any)["truncated"] != true {
			t.Fatal("routine text was not bounded")
		}
		if worker["owner"] != "worker" || worker["presentation"] != "routine" {
			t.Fatalf("answered obligation: %v", worker)
		}
	}
}

func stringArgs(value any) []string {
	out := []string{}
	for _, raw := range value.([]any) {
		out = append(out, raw.(string))
	}
	return out
}

func TestCompactMalformedNeighborPreservesDecision(t *testing.T) {
	clearHerdrEnv(t)
	home := presentationHome(t)
	path := filepath.Join(home, "tasks", "t-bbbbbbbbbbbb", "task.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range [][]string{{"status", "--compact"}, {"inbox", "--compact"}, {"metadata", "inbox"}} {
		view := compactRead(t, home, command...)
		counts := view["counts"].(map[string]any)
		if view["complete"] != false || counts["unknown_sources"] != float64(1) || counts["decisions"] != float64(2) {
			t.Fatalf("coverage: %v", view)
		}
	}
}

func snapshotPresentationFiles(t *testing.T, home string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestPresentationReadsArePure(t *testing.T) {
	clearHerdrEnv(t)
	home := presentationHome(t)
	log := filepath.Join(t.TempDir(), "external.log")
	bin := t.TempDir()
	for _, name := range []string{"herdr", "gh"} {
		script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' '%s' >> '%s'\nexit 99\n", name, log)
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("SUM_HERDR_BIN", filepath.Join(bin, "herdr"))
	t.Setenv("SUM_GH_BIN", filepath.Join(bin, "gh"))
	before := snapshotPresentationFiles(t, home)
	for i := 0; i < 2; i++ {
		if i == 1 {
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_PANE_ID", "sum-test:p1")
			t.Setenv("HERDR_SESSION", "sum-test-presentation")
		}
		for _, command := range [][]string{{"status"}, {"inbox"}, {"status", "--compact"}, {"inbox", "--compact"}, {"metadata", "inbox"}} {
			compactRead(t, home, command...)
		}
	}
	if after := snapshotPresentationFiles(t, home); !reflect.DeepEqual(before, after) {
		t.Fatal("read changed source files")
	}
	if raw, err := os.ReadFile(log); err == nil {
		t.Fatalf("read called external helper: %s", raw)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestCompactRejectsInvalidBoundsWithoutWrites(t *testing.T) {
	clearHerdrEnv(t)
	home := presentationHome(t)
	before := snapshotPresentationFiles(t, home)
	for _, args := range [][]string{
		{"status", "--limit", "1"}, {"inbox", "--after", "1"}, {"status", "--max-chars", "0"},
		{"status", "--compact", "--limit", "0"}, {"inbox", "--compact", "--limit", "101"},
		{"metadata", "inbox", "--after", "-1"}, {"metadata", "inbox", "--max-chars", "0"},
		{"status", "--compact", "--max-chars", "2001"},
	} {
		stdout, _, err := runStatus(t, home, args...)
		if err == nil || stdout != "" {
			t.Fatalf("invalid bounds accepted: %v %s %v", args, stdout, err)
		}
	}
	if !reflect.DeepEqual(before, snapshotPresentationFiles(t, home)) {
		t.Fatal("invalid read modified records")
	}
	empty := compactRead(t, home, "inbox", "--compact", "--after", "999999999999")
	page := empty["page"].(map[string]any)
	if len(page["items"].([]any)) != 0 || page["next_after"] != nil || empty["counts"].(map[string]any)["decisions"] != float64(2) {
		t.Fatalf("out-of-range page: %v", empty)
	}
}

func TestCompactLiveHasExplicitPageScopeAndNoStateWrites(t *testing.T) {
	clearHerdrEnv(t)
	home := presentationHome(t)
	herdrEnv(t, home)
	fake := t.TempDir()
	t.Setenv("FAKE_HERDR_ROOT", fake)
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	host, err := st.Machine()
	if err != nil {
		t.Fatal(err)
	}
	task, err := st.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	task.Set("machine", host.ID)
	task.Set("session", "sum-test")
	task.Set("pane", "w-parent:p1")
	if err := ordjson.WriteFile(filepath.Join(home, "tasks", "t-aaaaaaaaaaaa", "task.json"), task); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(home, "tasks", "t-bbbbbbbbbbbb", "task.json")
	if err := os.MkdirAll(filepath.Dir(other), 0700); err != nil {
		t.Fatal(err)
	}
	task.Set("id", "t-bbbbbbbbbbbb")
	if err := ordjson.WriteFile(other, task); err != nil {
		t.Fatal(err)
	}
	before := snapshotPresentationFiles(t, home)
	view := compactRead(t, home, "status", "--compact", "--live", "--limit", "1")
	scope := view["observation_scope"].(map[string]any)
	if view["live"] != true || scope["tasks"] != float64(1) || scope["omitted_tasks"] != float64(1) || view["counts"].(map[string]any)["decisions"] != float64(4) {
		t.Fatalf("live coverage: %v", view)
	}
	if !reflect.DeepEqual(before, snapshotPresentationFiles(t, home)) {
		t.Fatal("live read modified records")
	}
	calls, err := os.ReadFile(filepath.Join(fake, "calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"args": ["agent", "list"`) {
		t.Fatalf("expected one bounded observation: %s", calls)
	}
}

func TestMetadataProjectionAndCompactAgree(t *testing.T) {
	clearHerdrEnv(t)
	home := presentationHome(t)
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema":1,"sum_version":"0.1.0","created_at":"2026-01-01T00:00:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}
	herdrEnv(t, home)
	fake := t.TempDir()
	t.Setenv("FAKE_HERDR_ROOT", fake)
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	host, err := st.Machine()
	if err != nil {
		t.Fatal(err)
	}
	ctx := ordjson.NewObject()
	ctx.Set("machine", host.ID)
	ctx.Set("session", "sum-test")
	ctx.Set("pane", "w-parent:p1")
	ctx.Set("cwd", home)
	owner := ordjson.NewObject()
	for _, key := range ctx.Keys() {
		value, _ := ctx.Get(key)
		owner.Set(key, value)
	}
	owner.Set("incarnation", incarnation.Evidence{Terminal: "term-w-parent:p1"}.Record(store.Now()))
	if err := ordjson.WriteFile(filepath.Join(home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Register(store.EndpointFromContext(ctx), "coordinator", nil, nil); err != nil {
		t.Fatal(err)
	}
	view := compactRead(t, home, "inbox", "--compact")
	counts := view["counts"].(map[string]any)
	compactRead(t, home, "metadata", "sync")
	calls, err := os.ReadFile(filepath.Join(fake, "calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("sum_inbox=%.0f decisions; %.0f worker", counts["decisions"], counts["worker"])
	if !strings.Contains(string(calls), want) {
		t.Fatalf("metadata differs from compact %v: %s", counts, calls)
	}
}

func TestPresentationHelpMatchesImplementedReadCommands(t *testing.T) {
	for _, topic := range []string{"status", "inbox", "metadata", "metadata-inbox"} {
		assertStdoutGolden(t, t.TempDir(), []string{"help", topic}, "help-"+topic)
	}
}
