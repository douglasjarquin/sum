package launch

import (
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestResolveRequiresAnExplicitOrSavedWorkerHarness(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = Resolve(s, nil, ResolveArgs{})
	if err == nil || !strings.Contains(err.Error(), "settings set --worker-harness") || strings.Contains(err.Error(), "coordinator") {
		t.Fatalf("missing worker choice did not name its settings action: %v", err)
	}
	if _, err := settings.Write(s, settings.WriteArgs{WorkerHarness: "grok"}); err != nil {
		t.Fatal(err)
	}
	chosen, err := Resolve(s, nil, ResolveArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if harness, _ := chosen.Get("harness"); harness != "grok" {
		t.Fatalf("saved worker ignored: %v", chosen)
	}
	explicit, err := Resolve(s, nil, ResolveArgs{Harness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if harness, _ := explicit.Get("harness"); harness != "codex" {
		t.Fatalf("explicit worker ignored: %v", explicit)
	}
}
