package launch

import "testing"

func TestGrokAcceptsMinimumStableVersion(t *testing.T) {
	profile := nativeProfiles["grok"]
	for _, text := range []string{
		"grok 1.0.67 (2fe064390d07) [stable]",
		"grok 1.0.70 (f50904eaee6c) [stable]",
		"grok 1.2.0 (abcdef0) [stable]",
	} {
		if !profile.matchesVersion(text) {
			t.Fatalf("rejected %q", text)
		}
	}
	for _, text := range []string{
		"grok 1.0.66 (2fe064390d07) [stable]",
		"grok 1.0.70 (f50904eaee6c) [nightly]",
		"grok 1.0.70",
		"grok 1.0.70 (nothex) [stable]",
	} {
		if profile.matchesVersion(text) {
			t.Fatalf("accepted %q", text)
		}
	}
	if nativeProfiles["codex"].matchesVersion("codex-cli 0.166.1") {
		t.Fatal("codex accepted a newer version")
	}
}
