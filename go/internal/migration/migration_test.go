package migration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type lease struct {
	checks int
	failAt int
	closed bool
}

func (l *lease) Verify(ctx context.Context, root, digest string) (Evidence, error) {
	l.checks++
	if l.checks == l.failAt {
		return Evidence{}, errors.New("sender exclusion lost")
	}
	return Evidence{Mechanism: "test-owned stopped process scope", Reference: "isolated fixture", SourceDigest: digest}, nil
}
func (l *lease) Close() error { l.closed = true; return nil }
func legacy(t *testing.T) (string, []byte) {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "state.json"), []byte("{\"schema\":1,\"instance\":\"original-instance\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	task := []byte("{\"schema\":1,\"id\":\"t-0123456789ab\",\"status\":\"archived\",\"questions\":[{\"answer\":\"preserved\"}],\"notifications\":[{\"state\":\"uncertain\"}],\"policy\":{\"prepared_worktree\":{\"workspace\":\"w1\"}}}\n")
	path := filepath.Join(root, "tasks", "t-0123456789ab")
	os.MkdirAll(path, 0700)
	os.WriteFile(filepath.Join(path, "task.json"), task, 0600)
	os.WriteFile(filepath.Join(path, "policy.snapshot"), []byte("original sealed bytes\n"), 0600)
	return root, task
}
func TestMigrationRequiresExclusionAndPreservesEvidence(t *testing.T) {
	root, original := legacy(t)
	if _, err := Migrate(context.Background(), Options{Home: root}); err == nil {
		t.Fatal("migration without exclusion succeeded")
	}
	l := &lease{}
	result, err := Migrate(context.Background(), Options{Home: root, Exclusion: l})
	if err != nil {
		t.Fatal(err)
	}
	if l.checks < 2 || !l.closed {
		t.Fatal("exclusion not checked through publication")
	}
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if s.Generation != result.Generation || s.Home == root {
		t.Fatal("authority not transferred")
	}
	got, err := os.ReadFile(filepath.Join(s.Tasks, "t-0123456789ab", "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatal("migration rewrote business or sealed evidence")
	}
	if _, err = s.ReadTask("t-0123456789ab"); err != nil {
		t.Fatal(err)
	}

	os.WriteFile(filepath.Join(root, "tasks", "t-0123456789ab", "task.json"), []byte("old paused writer"), 0600)
	got, _ = os.ReadFile(filepath.Join(s.Tasks, "t-0123456789ab", "task.json"))
	if string(got) != string(original) {
		t.Fatal("legacy cached path reached new authority")
	}
	again, err := Migrate(context.Background(), Options{Home: root, Exclusion: &lease{}})
	if err != nil || again.Generation != result.Generation {
		t.Fatalf("non-idempotent completion %v", err)
	}
}
func TestLostExclusionLeavesOriginalAuthorityAndResumes(t *testing.T) {
	root, _ := legacy(t)
	l := &lease{failAt: 2}
	if _, err := Migrate(context.Background(), Options{Home: root, Exclusion: l}); err == nil {
		t.Fatal("published after exclusion loss")
	}
	value, _ := ordjson.ReadFile(filepath.Join(root, "state.json"))
	schema, _ := value.(*ordjson.Object).Get("schema")
	if schema != json.Number("1") {
		t.Fatal("source authority changed")
	}
	first, err := readJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	next, err := Migrate(context.Background(), Options{Home: root, Exclusion: &lease{}})
	if err != nil {
		t.Fatal(err)
	}
	if next.Generation != first.Generation {
		t.Fatal("resume created another generation")
	}
}
func TestSourceChangeCannotReuseStagedGeneration(t *testing.T) {
	root, _ := legacy(t)
	if _, err := Migrate(context.Background(), Options{Home: root, Exclusion: &lease{failAt: 2}}); err == nil {
		t.Fatal("expected stop")
	}
	os.WriteFile(filepath.Join(root, "tasks", "t-0123456789ab", "policy.snapshot"), []byte("changed"), 0600)
	if _, err := Migrate(context.Background(), Options{Home: root, Exclusion: &lease{}}); err == nil {
		t.Fatal("changed source reused old export")
	}
}

func TestPublicationJournalCanFinishAfterPointerSwitch(t *testing.T) {
	root, _ := legacy(t)
	result, err := Migrate(context.Background(), Options{Home: root, Exclusion: &lease{}})
	if err != nil {
		t.Fatal(err)
	}
	result.Phase = "publishing"
	result.PublishedAt = ""
	if err = writeJournal(root, result); err != nil {
		t.Fatal(err)
	}
	recovered, err := Migrate(context.Background(), Options{Home: root, Exclusion: &lease{}})
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Phase != "published" || recovered.Generation != result.Generation {
		t.Fatal("publication was not recovered")
	}
}

func TestPublicationRecoveryPreservesPostCutoverWrites(t *testing.T) {
	root, _ := legacy(t)
	result, err := Migrate(context.Background(), Options{Home: root, Exclusion: &lease{}})
	if err != nil {
		t.Fatal(err)
	}
	result.Phase = "publishing"
	result.PublishedAt = ""
	if err = writeJournal(root, result); err != nil {
		t.Fatal(err)
	}
	current, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	task, err := current.ReadTask("t-0123456789ab")
	if err != nil {
		t.Fatal(err)
	}
	task.Set("post_cutover", "legitimate progress")
	if err = current.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(current.Tasks, "t-0123456789ab", "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := Migrate(context.Background(), Options{Home: root, Exclusion: &lease{}})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(current.Tasks, "t-0123456789ab", "task.json"))
	if resumed.Phase != "published" || string(before) != string(after) {
		t.Fatal("recovery rewrote selected live records")
	}
}
func TestPublicationRecoveryRejectsWrongPointerAndTamperedExport(t *testing.T) {
	for _, kind := range []string{"wrong generation", "tampered export"} {
		t.Run(kind, func(t *testing.T) {
			root, _ := legacy(t)
			result, err := Migrate(context.Background(), Options{Home: root, Exclusion: &lease{}})
			if err != nil {
				t.Fatal(err)
			}
			result.Phase = "publishing"
			if err = writeJournal(root, result); err != nil {
				t.Fatal(err)
			}
			if kind == "wrong generation" {
				pointer, _ := readObject(filepath.Join(root, "state.json"))
				pointer.Set("generation", "g-ffffffffffffffffffffffff")
				if err = ordjson.WriteFile(filepath.Join(root, "state.json"), pointer); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = os.WriteFile(filepath.Join(result.Export, "tasks", "t-0123456789ab", "task.json"), []byte("tampered immutable export"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = Migrate(context.Background(), Options{Home: root, Exclusion: &lease{}}); err == nil {
				t.Fatal("invalid publication evidence accepted")
			}
		})
	}
}
