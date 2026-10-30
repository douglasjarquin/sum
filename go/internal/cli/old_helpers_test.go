package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// A pre-native release is historical evidence, not an executable writer for the
// current authority. No command silently upgrades or adopts its saved routes.
func TestOlderHelpersOnCandidateRecords(t *testing.T) {
	root, helper := repoReference(t)
	for _, args := range [][]string{{"init"}, {"status"}, {"ask", "t-aaaaaaaaaaaa", "--text", "continue?"}, {"report", "t-aaaaaaaaaaaa", "--text", "done"}, {"answer", "t-aaaaaaaaaaaa", "q-one", "--text", "yes"}, {"bind", "t-aaaaaaaaaaaa", "--parent-only"}} {
		t.Run(strings.Join(args[:1], ""), func(t *testing.T) {
			base := t.TempDir()
			home := filepath.Join(base, "historical")
			if err := os.MkdirAll(filepath.Join(home, "tasks", "t-aaaaaaaaaaaa"), 0700); err != nil {
				t.Fatal(err)
			}
			for relative, body := range map[string]string{
				"state.json":                     `{"schema":1,"sum_version":"0.1.0","instance":"historical"}`,
				"context.json":                   `{"role":"coordinator","machine":"old-host","session":"old-session","pane":"old-parent"}`,
				"tasks/t-aaaaaaaaaaaa/task.json": `{"schema":1,"id":"t-aaaaaaaaaaaa","status":"waiting","questions":[{"id":"q-one","text":"continue?","status":"open"}],"evidence":[{"kind":"policy-review","text":"original policy history"}],"parent":{"machine":"old-host","session":"old-session","pane":"old-parent"}}`,
			} {
				if err := os.WriteFile(filepath.Join(home, relative), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotTree(t, home)
			d := &demoLab{t: t, root: root, helper: helper, base: base, home: home, env: demoEnv(t, root, base)}
			result := d.ctl(false, args...)
			if !strings.Contains(asString(result["error"]), "unsupported state schema") {
				t.Fatalf("historical writer was not explicitly refused: %v", result)
			}
			assertTreeUnchanged(t, before, snapshotTree(t, home))
		})
	}
}

func TestHistoricalReaderPreservesEvidenceAndRefusesWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tasks", "t-aaaaaaaaaaaa"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state.json"), []byte(`{"schema":1,"sum_version":"0.1.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	body := `{"schema":1,"id":"t-aaaaaaaaaaaa","evidence":[{"kind":"policy-review","text":"original policy history"}]}`
	if err := os.WriteFile(filepath.Join(root, "tasks", "t-aaaaaaaaaaaa", "task.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, root)
	historical, err := store.OpenHistorical(root)
	if err != nil {
		t.Fatal(err)
	}
	task, err := historical.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	evidence, _ := task.Get("evidence")
	if len(evidence.([]any)) != 1 {
		t.Fatal("historical policy evidence disappeared")
	}
	if _, err = historical.Lock(); err == nil {
		t.Fatal("historical reader acquired write authority")
	}
	assertTreeUnchanged(t, before, snapshotTree(t, root))
}

func TestPhysicalHelperGenerationFenceBeforeEffects(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "retired-generation", map[string]string{"README": "base\n"})
	task := d.ctl(true, "prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	before := snapshotTree(t, d.home)
	s, err := store.Open(d.home)
	if err != nil {
		t.Fatal(err)
	}
	generation := "g-bbbbbbbbbbbbbbbbbbbbbbbb"
	next := filepath.Join(s.Root, "generations", generation)
	for _, dir := range []string{next, filepath.Join(next, "tasks"), filepath.Join(next, "sessions")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	state := ordjson.NewObject()
	state.Set("schema", 2)
	state.Set("backend", "hc")
	state.Set("generation", generation)
	state.Set("root", s.Root)
	if err = ordjson.WriteFile(filepath.Join(next, "state.json"), state); err != nil {
		t.Fatal(err)
	}
	pointer := ordjson.NewObject()
	pointer.Set("schema", 2)
	pointer.Set("backend", "hc")
	pointer.Set("generation", generation)
	if err = ordjson.WriteFile(filepath.Join(s.Root, "state.json"), pointer); err != nil {
		t.Fatal(err)
	}
	calls := len(d.native.Calls())
	result := d.ctl(false, "start", asString(task["id"]))
	if !strings.Contains(asString(result["error"]), "inactive state generation") {
		t.Fatalf("retired physical callback accepted: %v", result)
	}
	for _, call := range d.native.Calls()[calls:] {
		if call.Method == "terminal.run" || call.Method == "terminal.send" || call.Method == "terminal.kill" || call.Method == "terminal.rm" {
			t.Fatalf("retired helper had native effect: %v", call)
		}
	}
	assertTreeUnchanged(t, before, snapshotTree(t, d.home))
}
