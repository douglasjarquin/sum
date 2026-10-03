package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestNativeEventsDefaultOnAndOptOutPersistsAcrossSettingsWrites(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	enabled, err := NativeEventsEnabled(s)
	if err != nil || !enabled {
		t.Fatalf("default enabled = %v, err = %v", enabled, err)
	}
	if err := SetNativeEventsEnabled(s, false); err != nil {
		t.Fatalf("disable native events: %v", err)
	}
	if _, err := Write(s, WriteArgs{Global: intPointer(3)}); err != nil {
		t.Fatalf("write capacity: %v", err)
	}
	enabled, err = NativeEventsEnabled(s)
	if err != nil || enabled {
		t.Fatalf("after settings write enabled = %v, err = %v", enabled, err)
	}
	data, err := os.ReadFile(filepath.Join(s.Home, File))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || !strings.Contains(string(data), `"hook"`) || !strings.Contains(string(data), `"enabled": false`) {
		t.Fatalf("settings did not preserve the native-events opt-out: %s", data)
	}
}

func intPointer(value int) *int {
	return &value
}
