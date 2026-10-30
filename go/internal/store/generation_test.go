package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func TestGenerationSeparatesWritableAuthority(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	header, err := ordjson.ReadFile(filepath.Join(s.Home, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if instance, _ := header.(*ordjson.Object).Get("instance"); instance == nil || instance == "" {
		t.Fatal("fresh native home has no installation identity")
	}
	if s.Root != root || s.Home == root || filepath.Dir(s.Home) != filepath.Join(root, "generations") {
		t.Fatalf("not physically separated: %#v", s)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Home != s.Home {
		t.Fatal("pointer did not reopen generation")
	}
	task := ordjson.NewObject()
	task.Set("schema", json.Number("2"))
	task.Set("id", "t-0123456789ab")
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "tasks", "t-0123456789ab", "task.json")); !os.IsNotExist(err) {
		t.Fatal("live task leaked into original generation")
	}
	state, err := ordjson.ReadFile(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	state.(*ordjson.Object).Set("generation", "g-replaced")
	if err = ordjson.WriteFile(filepath.Join(root, "state.json"), state); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveTask(task); !errors.Is(err, ErrInactiveGeneration) {
		t.Fatalf("stale opened Store wrote after transfer: %v", err)
	}
}
func TestLegacyOpenNeverConverts(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	original := []byte("{\"schema\":1,\"instance\":\"original\"}\n")
	if err := os.WriteFile(filepath.Join(root, "state.json"), original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err == nil {
		t.Fatal("legacy state opened writable")
	}
	got, _ := os.ReadFile(filepath.Join(root, "state.json"))
	if string(got) != string(original) {
		t.Fatal("Open converted history")
	}
	s, err := OpenHistorical(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err == nil {
		t.Fatal("historical store became writable")
	}
}
