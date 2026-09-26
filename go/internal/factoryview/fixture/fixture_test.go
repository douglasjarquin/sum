package fixture

import (
	"os"
	"path/filepath"
	"testing"
)

// The standard home is readable by the store and stable across writes; SUM_FIXTURE_HOME also writes a copy
// for manual inspection with the CLI (the integrated replay reuses the same builder).
func TestWriteStandardIsStable(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	for _, home := range []string{first, second} {
		if _, err := WriteStandard(home); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{"state.json", "factory.json", filepath.Join("tasks", "t-a1aaaaaaaaaa", "task.json")} {
		a, err := os.ReadFile(filepath.Join(first, rel))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(second, rel))
		if string(a) != string(b) {
			t.Fatalf("%s differs between writes", rel)
		}
	}
	if out := os.Getenv("SUM_FIXTURE_HOME"); out != "" {
		if _, err := WriteStandard(out); err != nil {
			t.Fatal(err)
		}
	}
}
