package bootstraptest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func buildNativeBaseHelper(t *testing.T) string {
	t.Helper()
	return hctest.BuildNativeBaseHelper(t, repoRoot(t))
}

func clearFallbackIdentity(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "SUM_") || strings.HasPrefix(key, "HC_") || strings.HasPrefix(key, "HERDR_") {
			t.Setenv(key, "")
		}
	}
}

func nativeTestStore(t *testing.T, home string) *store.Store {
	t.Helper()
	s, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	return s
}

func decodeObject(t *testing.T, raw string) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode result: %v %s", err, raw)
	}
	return result
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}
