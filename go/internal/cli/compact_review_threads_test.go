package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCompiledCompactInboxReviewThreadNullableLine(t *testing.T) {
	root := repoRoot(t)
	fixture, err := os.ReadFile(filepath.Join("testdata", "compact-review-thread-task.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, location := range []struct {
		name string
		line any
		toon string
	}{{"numeric", float64(42), "line: 42"}, {"null", nil, "line: null"}} {
		t.Run(location.name, func(t *testing.T) {
			var task map[string]any
			if err := json.Unmarshal(fixture, &task); err != nil {
				t.Fatal(err)
			}
			thread := asMap(asSlice(asMap(asMap(task["pr"])["review_threads"])["threads"])[0])
			thread["line"] = location.line
			if location.line == nil {
				thread["original_line"] = nil
			}
			body, err := json.Marshal(task)
			if err != nil {
				t.Fatal(err)
			}
			home := writeTaskFixture(t, t.TempDir(), asString(task["id"]), string(body))
			before := snapshotPresentationFiles(t, home)
			for _, format := range []string{"json", "toon"} {
				command := exec.Command(filepath.Join(root, ".local", "bin", "sumctl"), "--home", home, "--format", format, "inbox", "--compact")
				command.Env = demoEnv(t, root, t.TempDir())
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("compiled compact inbox (%s): %v\n%s", format, err, output)
				}
				if strings.Contains(string(output), "synthetic-read-secret") {
					t.Fatal("compact view exposed the synthetic token")
				}
				if format == "toon" {
					for _, field := range []string{"compact: true", location.toon, "fixture-reviewer", "Explain the saved guard.", "Address selected", "Discuss", "Draft reply", "Open comment"} {
						if !strings.Contains(string(output), field) {
							t.Fatalf("TOON compact view lost %q: %s", field, output)
						}
					}
					continue
				}
				view := decodeCLIMap(t, string(output))
				page := asMap(view["page"])
				if view["compact"] != true || page["total"] != float64(5) || len(asSlice(page["items"])) != 5 {
					t.Fatalf("primary compact page changed: %v", view)
				}
				var row map[string]any
				for _, item := range asSlice(page["items"]) {
					candidate := asMap(item)
					if asMap(candidate["source"])["kind"] == "github-review-thread" {
						row = candidate
					}
				}
				if row == nil {
					t.Fatal("compact inbox omitted the saved review thread")
				}
				line, present := row["line"]
				if !present || line != location.line {
					t.Fatalf("thread line=%v present=%v want %v", line, present, location.line)
				}
				if row["source_url"] != thread["source_url"] || row["author"] != thread["latest_author"] || row["file"] != thread["path"] || row["observed_at"] != thread["observed_at"] || row["resolved"] != false || row["outdated"] != false || asMap(row["text"])["text"] != "Explain the saved guard." {
					t.Fatalf("thread fields changed: %v", row)
				}
				source := asMap(row["source"])
				if source["id"] != thread["id"] || source["task"] != task["id"] || source["candidate"] != thread["observed_head_sha"] || asString(source["revision"]) == "" {
					t.Fatalf("thread source changed: %v", source)
				}
				actions := map[string]map[string]any{}
				for _, raw := range asSlice(row["actions"]) {
					action := asMap(raw)
					actions[asString(action["kind"])] = action
					if !reflect.DeepEqual(asMap(action["target"]), source) {
						t.Fatalf("action lost bound source: %v", action)
					}
				}
				for _, kind := range []string{"address-selected", "address-all", "discuss", "draft-reply", "open-comment"} {
					if len(asSlice(actions[kind]["arguments"])) == 0 {
						t.Fatalf("thread action %s missing: %v", kind, row)
					}
				}
				if !reflect.DeepEqual(stringArgs(row["detail"]), []string{"context", asString(task["id"]), "--section", "review_threads", "--max-chars", "0"}) {
					t.Fatalf("thread detail route changed: %v", row["detail"])
				}
			}
			if after := snapshotPresentationFiles(t, home); !reflect.DeepEqual(before, after) {
				t.Fatal("compact reads changed saved task or sidecar bytes")
			}
		})
	}
}
