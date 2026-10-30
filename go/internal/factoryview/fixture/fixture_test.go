package fixture

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/store"
)

func fixtureFiles(t *testing.T, home string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	if err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		files[relative] = data
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func normalizedFixtureState(t *testing.T, s *store.Store, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err = json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state["schema"] != float64(store.Schema) || state["backend"] != "hc" || state["generation"] != s.Generation {
		t.Fatalf("invalid native fixture state: %#v", state)
	}
	if root, ok := state["root"]; ok && root != s.Root {
		t.Fatalf("generation points at %v, want %s", root, s.Root)
	}

	data = bytes.ReplaceAll(data, []byte(s.Generation), []byte("<GENERATION>"))
	return bytes.ReplaceAll(data, []byte(s.Root), []byte("<ROOT>"))
}

func TestWriteStandardIsStable(t *testing.T) {
	t.Setenv("SUM_NOW", "2026-01-01T00:00:00+00:00")
	firstRoot, secondRoot := t.TempDir(), t.TempDir()
	var layouts []Standard
	var stores []*store.Store
	for _, root := range []string{firstRoot, secondRoot} {
		layout, err := WriteStandard(root)
		if err != nil {
			t.Fatal(err)
		}
		s, err := store.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		if layout.Home != s.Home || layout.Home == s.Root {
			t.Fatalf("layout home %s is not the physical generation %s", layout.Home, s.Home)
		}
		instance, err := s.Instance()
		if err != nil || instance != layout.Instance {
			t.Fatalf("instance = %q, err=%v", instance, err)
		}
		if physical, err := store.Open(layout.Home); err != nil || physical.Root != s.Root || physical.Generation != s.Generation {
			t.Fatalf("physical home lost its root authority: %#v %v", physical, err)
		}
		layouts = append(layouts, layout)
		stores = append(stores, s)
	}
	if stores[0].Generation == stores[1].Generation {
		t.Fatal("independent fixture homes reused one generated authority")
	}
	for _, relative := range []string{"state.json", filepath.Join("generations", "<GENERATION>", "state.json")} {
		paths := [2]string{}
		for i, s := range stores {
			paths[i] = filepath.Join(s.Root, strings.ReplaceAll(relative, "<GENERATION>", s.Generation))
		}
		first, second := normalizedFixtureState(t, stores[0], paths[0]), normalizedFixtureState(t, stores[1], paths[1])
		if !bytes.Equal(first, second) {
			t.Fatalf("%s differs beyond its generation/root identity:\n%s\n%s", relative, first, second)
		}
	}
	firstFiles, secondFiles := fixtureFiles(t, layouts[0].Home), fixtureFiles(t, layouts[1].Home)
	if len(firstFiles) != len(secondFiles) {
		t.Fatalf("fixture files = %d and %d", len(firstFiles), len(secondFiles))
	}
	for relative, first := range firstFiles {
		if relative == "state.json" {
			continue
		}
		if second, ok := secondFiles[relative]; !ok || !bytes.Equal(first, second) {
			t.Fatalf("%s differs between fixture generations", relative)
		}
	}
	before := fixtureFiles(t, stores[0].Root)
	t.Setenv("SUM_NOW", "2026-01-02T00:00:00+00:00")
	repeated, err := WriteStandard(firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Home != layouts[0].Home {
		t.Fatal("repeated fixture write replaced its active generation")
	}
	after := fixtureFiles(t, stores[0].Root)
	if len(before) != len(after) {
		t.Fatal("repeated fixture write changed its file set")
	}
	for path, first := range before {
		if !bytes.Equal(first, after[path]) {
			t.Fatalf("repeated write changed %s", path)
		}
	}
	if out := os.Getenv("SUM_FIXTURE_HOME"); out != "" {
		if _, err := WriteStandard(out); err != nil {
			t.Fatal(err)
		}
	}
}
