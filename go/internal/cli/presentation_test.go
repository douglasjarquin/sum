package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/helpview"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/pflag"
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

func assertFullDecisionDetail(t *testing.T, detail map[string]any) {
	t.Helper()
	decision := detail["decisions"].(map[string]any)["items"].([]any)[0].(map[string]any)
	text := decision["text"].(map[string]any)
	if decision["id"] != "q-first" || text["truncated"] != false || text["text"] != strings.Repeat("界", 700)+" $(touch should-not-exist)" {
		t.Fatalf("detail lost question text: %v", decision)
	}
}

func TestCompactDecisionDetailSurvivesMalformedVersionsAndReport(t *testing.T) {
	clearHerdrEnv(t)
	for _, fixture := range []struct {
		name, revisions, report, errorText string
	}{
		{"null revision", `[null]`, `null`, "revisions[0] must be an object"},
		{"scalar revision", `[42]`, `null`, "revisions[0] must be an object"},
		{"object revisions", `{}`, `null`, "revisions must be an array"},
		{"string report", `[]`, `"broken"`, "report must be an object or null"},
		{"array report", `[]`, `[]`, "report must be an object or null"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			home := presentationHome(t)
			taskDir := filepath.Join(home, "tasks", "t-aaaaaaaaaaaa")
			taskPath := filepath.Join(taskDir, "task.json")
			raw, err := os.ReadFile(taskPath)
			if err != nil {
				t.Fatal(err)
			}
			raw = bytes.Replace(raw, []byte(`"report":null`), []byte(`"report":`+fixture.report), 1)
			if err := os.WriteFile(taskPath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			version := `{"schema":1,"task":"t-aaaaaaaaaaaa","requested":"r1","revisions":` + fixture.revisions + `}`
			if err := os.WriteFile(filepath.Join(taskDir, "versions.json"), []byte(version), 0600); err != nil {
				t.Fatal(err)
			}
			before := snapshotPresentationFiles(t, home)
			compact := compactRead(t, home, "inbox", "--compact", "--limit", "1", "--max-chars", "40")
			item := compact["page"].(map[string]any)["items"].([]any)[0].(map[string]any)
			if item["text"].(map[string]any)["truncated"] != true {
				t.Fatal("fixture did not need a full-detail route")
			}
			detail := compactRead(t, home, stringArgs(item["detail"])...)
			assertFullDecisionDetail(t, detail)
			if problem, _ := detail["versions_error"].(string); !strings.Contains(problem, fixture.errorText) {
				t.Fatalf("versions error = %q", problem)
			}
			if after := snapshotPresentationFiles(t, home); !reflect.DeepEqual(before, after) {
				t.Fatal("detail read changed source files")
			}
		})
	}
}

func TestCompactDecisionDetailRejectsVersionsFIFO(t *testing.T) {
	clearHerdrEnv(t)
	home := presentationHome(t)
	path := filepath.Join(home, "tasks", "t-aaaaaaaaaaaa", "versions.json")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	read := func(args ...string) map[string]any {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, filepath.Join(repoRoot(t), ".local", "bin", "sumctl"), append([]string{"--home", home, "--format", "json"}, args...)...)
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "SUM_") && !strings.HasPrefix(value, "HERDR_") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v failed or blocked: %v, deadline: %v\n%s", args, err, ctx.Err(), out)
		}
		var result map[string]any
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("decode %v: %v\n%s", args, err, out)
		}
		return result
	}
	compact := read("inbox", "--compact", "--limit", "1", "--max-chars", "40")
	item := compact["page"].(map[string]any)["items"].([]any)[0].(map[string]any)
	detail := read(stringArgs(item["detail"])...)
	assertFullDecisionDetail(t, detail)
	if problem, _ := detail["versions_error"].(string); !strings.Contains(problem, "must be a regular file") {
		t.Fatalf("versions error = %q", problem)
	}
}

func TestCompactSidecarDetailRoutesReadSavedContentWithoutWrites(t *testing.T) {
	clearHerdrEnv(t)
	home := presentationHome(t)
	result := strings.Repeat("saved failure 界 ", 100)
	laneText := strings.Repeat("saved lane explanation ", 100)
	lane := map[string]any{"task": "t-aaaaaaaaaaaa", "issue": 239, "state": "gated", "claimed_at": "2026-01-01T00:00:00Z", "reason": laneText}
	fixtures := map[string]any{
		"tasks/t-aaaaaaaaaaaa/pipeline.json": map[string]any{"schema": 1, "task": "t-aaaaaaaaaaaa", "candidate": "sha1", "rows": []any{map[string]any{"stage": "test", "status": "fail", "result": result, "at": "2026-01-01T00:00:00Z"}}},
		"factory.json":                       map[string]any{"schema": 1, "projects": map[string]any{"owner/repo": map[string]any{"name": "owner/repo", "lanes": 1, "lanes_held": []any{lane}}}},
	}
	for path, fixture := range fixtures {
		raw, err := json.Marshal(fixture)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, path), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	before := snapshotPresentationFiles(t, home)
	compact := compactRead(t, home, "inbox", "--compact", "--max-chars", "40")
	found := map[string]bool{}
	for _, raw := range compact["page"].(map[string]any)["items"].([]any) {
		item := raw.(map[string]any)
		kind := item["source"].(map[string]any)["kind"].(string)
		if kind != "pipeline" && kind != "factory" {
			continue
		}
		found[kind] = true
		detail := compactRead(t, home, stringArgs(item["detail"])...)
		if kind == "pipeline" {
			if item["text"].(map[string]any)["truncated"] != true || detail["candidate"] != "sha1" {
				t.Fatalf("pipeline detail: %v", detail)
			}
			matched := false
			for _, raw := range detail["rows"].([]any) {
				row := raw.(map[string]any)
				if row["stage"] == "test" && row["result"] == result {
					matched = true
				}
			}
			if !matched {
				t.Fatal("detail lost full saved pipeline result")
			}
		} else {
			project := detail["projects"].([]any)[0].(map[string]any)
			got := project["lanes_held"].([]any)[0].(map[string]any)
			if got["task"] != lane["task"] || got["reason"] != laneText || got["state"] != "gated" {
				t.Fatalf("detail lost saved factory lane: %v", got)
			}
		}
	}
	if !found["pipeline"] || !found["factory"] {
		t.Fatalf("sidecar routes missing: %v", found)
	}
	if after := snapshotPresentationFiles(t, home); !reflect.DeepEqual(before, after) {
		t.Fatal("detail read changed source files")
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
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema":1,"sum_version":"0.1.0","created_at":"2026-01-01T00:00:00Z","instance":"presentation0001"}`), 0600); err != nil {
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
	compactRead(t, home, "metadata", "enable")
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
	for _, topic := range []string{"status", "inbox", "metadata", "metadata-inbox", "factory-status"} {
		assertStdoutGolden(t, t.TempDir(), []string{"help", topic}, "help-"+topic)
	}
	// Every registered flag reaches the catalog, so a future flag cannot skip the help topic.
	var out, errOut bytes.Buffer
	root := NewRoot("sumctl", &out, &errOut)
	for _, topic := range []string{"status", "inbox", "factory-status"} {
		cmd, _, err := root.Find(strings.Split(topic, "-"))
		if err != nil || cmd == nil || cmd.Name() != strings.TrimPrefix(topic, "factory-") {
			t.Fatalf("command %s: %v", topic, err)
		}
		view, err := helpview.View("", topic)
		if err != nil {
			t.Fatal(err)
		}
		listed := map[string]bool{}
		rawArgs, _ := view.Get("arguments")
		for _, raw := range rawArgs.([]any) {
			name, _ := raw.(*ordjson.Object).Get("name")
			listed[name.(string)] = true
		}
		cmd.NonInheritedFlags().VisitAll(func(flag *pflag.Flag) {
			if flag.Hidden || flag.Name == "format" || flag.Name == "help" {
				return
			}
			if !listed["--"+flag.Name] {
				t.Errorf("help catalog topic %s omits registered flag --%s", topic, flag.Name)
			}
		})
	}
}

func groupedHome(t *testing.T) string {
	t.Helper()
	home := presentationHome(t)
	write := func(id string, task map[string]any) {
		t.Helper()
		path := filepath.Join(home, "tasks", id, "task.json")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(task)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("t-aaaaaaaaaaaa", map[string]any{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "/w/a", "project": map[string]any{"host": "github.com", "owner": "a", "repo": "repo"}, "questions": []any{map[string]any{"id": "q-a", "status": "open", "text": "A: which path?", "created_at": "2026-01-02T00:00:00Z"}}, "attention": []any{}, "evidence": []any{}, "report": nil})
	write("t-bbbbbbbbbbbb", map[string]any{"schema": 1, "id": "t-bbbbbbbbbbbb", "status": "running", "repository": "/w/b", "project": map[string]any{"host": "ghe.example.com", "owner": "b", "repo": "repo"}, "questions": []any{map[string]any{"id": "q-b", "status": "open", "text": "B: \x1b[31mred\x1b[0m\x07 question?", "created_at": "2026-01-03T00:00:00Z"}}, "attention": []any{}, "evidence": []any{}, "report": nil})
	write("t-cccccccccccc", map[string]any{"schema": 1, "id": "t-cccccccccccc", "status": "running", "questions": []any{}, "attention": []any{}, "evidence": []any{}, "report": nil})
	return home
}

func groupKeys(view map[string]any) []string {
	keys := []string{}
	for _, raw := range view["groups"].([]any) {
		keys = append(keys, raw.(map[string]any)["key"].(string))
	}
	return keys
}

func TestGroupedOverviewCommandsGroupByCanonicalIdentity(t *testing.T) {
	clearHerdrEnv(t)
	home := groupedHome(t)
	before := snapshotPresentationFiles(t, home)
	for _, command := range [][]string{{"status", "--grouped"}, {"inbox", "--grouped"}, {"metadata", "inbox", "--grouped"}} {
		view := compactRead(t, home, command...)
		if got := groupKeys(view); !reflect.DeepEqual(got, []string{"a/repo", "ghe.example.com/b/repo"}) {
			t.Fatalf("%v groups = %v", command, got)
		}
		counts := view["counts"].(map[string]any)
		if counts["decisions"] != float64(2) || view["complete"] != true || len(view["needs_you"].([]any)) != 2 {
			t.Fatalf("%v global facts: %v", command, view)
		}
		standalone := view["standalone"].(map[string]any)
		if standalone["key"] != "" || standalone["tasks"].([]any)[0].(map[string]any)["id"] != "t-cccccccccccc" {
			t.Fatalf("%v standalone: %v", command, standalone)
		}
		if _, has := view["page"]; has {
			t.Fatalf("%v grouped output must not be the compact page", command)
		}
		for _, raw := range view["needs_you"].([]any) {
			row := raw.(map[string]any)
			if row["project"] == "ghe.example.com/b/repo" && !strings.Contains(row["text"].(map[string]any)["text"].(string), "\x1b") {
				t.Fatal("JSON contract carries saved text verbatim; only the terminal view sanitizes")
			}
		}
	}
	if !reflect.DeepEqual(before, snapshotPresentationFiles(t, home)) {
		t.Fatal("grouped read changed source files")
	}
	compact := compactRead(t, home, "status", "--compact")
	if _, has := compact["groups"]; has || compact["compact"] != true {
		t.Fatal("existing compact output changed")
	}
}

func TestGroupedProjectFocusKeepsGlobalCountsAndNeedsYou(t *testing.T) {
	clearHerdrEnv(t)
	home := groupedHome(t)
	if err := os.WriteFile(filepath.Join(home, "tasks", "t-bbbbbbbbbbbb", "versions.json"), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(home, "tasks", "t-dddddddddddd", "task.json")
	if err := os.MkdirAll(filepath.Dir(broken), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range [][]string{{"status", "--grouped"}, {"inbox", "--grouped"}, {"metadata", "inbox", "--grouped"}} {
		view := compactRead(t, home, append(command, "--project", "a/repo")...)
		if got := groupKeys(view); !reflect.DeepEqual(got, []string{"a/repo"}) || view["standalone"] != nil || view["project"] != "a/repo" {
			t.Fatalf("%v focus = %v", command, view)
		}
		counts := view["counts"].(map[string]any)
		if counts["decisions"] != float64(2) || view["complete"] != false || counts["unknown_sources"] != float64(2) || len(view["gaps"].([]any)) != 1 {
			t.Fatalf("%v focus dropped global facts: %v", command, view)
		}
		projects := map[string]bool{}
		for _, raw := range view["needs_you"].([]any) {
			projects[raw.(map[string]any)["project"].(string)] = true
		}
		if !projects["ghe.example.com/b/repo"] || !projects["a/repo"] {
			t.Fatalf("%v needs_you lost the other project's question: %v", command, projects)
		}
	}
	for _, args := range [][]string{{"status", "--project", "a/repo"}, {"inbox", "--view"}, {"status", "--grouped", "--compact"}, {"status", "--width", "10"}, {"metadata", "inbox", "--project", "x"}} {
		stdout, _, err := runStatus(t, home, args...)
		if err == nil || stdout != "" || !strings.Contains(err.Error(), "--grouped") {
			t.Fatalf("%v accepted without prerequisites: %v %q", args, err, stdout)
		}
	}
}

func TestGroupedViewRunsScriptedSessionReadOnly(t *testing.T) {
	clearHerdrEnv(t)
	home := groupedHome(t)
	t.Setenv("COLUMNS", "100")
	t.Setenv("LINES", "30")
	before := snapshotPresentationFiles(t, home)
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetIn(strings.NewReader("n\nd\nb\nq\n"))
	root.SetArgs([]string{"--home", home, "status", "--grouped", "--view"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("view: %v %s", err, errBuf.String())
	}
	out := outBuf.String()
	if !strings.HasPrefix(out, "SUM  2 decisions") || !strings.Contains(out, "NEEDS YOU") || !strings.Contains(out, "TASK t-") || strings.Contains(out, "\x1b") || strings.Contains(out, "\x07") {
		t.Fatalf("view output:\n%s", out)
	}
	for _, row := range strings.Split(out, "\n") {
		if len([]rune(row)) > 100 {
			t.Fatalf("line exceeds COLUMNS: %q", row)
		}
	}
	if !reflect.DeepEqual(before, snapshotPresentationFiles(t, home)) {
		t.Fatal("view changed source files")
	}
	outBuf.Reset()
	root = NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetIn(strings.NewReader("q\n"))
	root.SetArgs([]string{"--home", home, "inbox", "--grouped", "--view", "--project", "a/repo", "--width", "60", "--height", "12"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("focused view: %v", err)
	}
	if !strings.Contains(outBuf.String(), "focus: a/repo") || strings.Contains(outBuf.String(), "ghe.example.com/b/repo  0 decisions") {
		t.Fatalf("focused view:\n%s", outBuf.String())
	}
}
