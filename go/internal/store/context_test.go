package store

import "testing"

func TestContextDoesNotInferNativeAuthorityFromLegacyEnvironment(t *testing.T) {
	for _, key := range []string{"HC_SOCKET", "HC_TERMINAL_ID", "HC_SESSION", "SUM_CORE_ID", "SUM_SESSION"} {
		t.Setenv(key, "")
	}
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("HERDR_SESSION", "old")
	if _, err := Context("/tmp"); err == nil {
		t.Fatal("legacy environment granted native authority")
	}
	t.Setenv("SUM_SESSION", "scratch")
	if _, err := Context("/tmp"); err == nil {
		t.Fatal("session alone granted native authority")
	}
}
