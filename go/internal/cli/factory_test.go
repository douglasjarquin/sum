package cli

import (
	"strings"
	"testing"
)

func TestFactoryStatus_pinsStdoutAcrossScenarios(t *testing.T) {
	t.Run("no factory registry", func(t *testing.T) {
		home := t.TempDir()
		assertStdoutGolden(t, home, []string{"factory", "status"}, scenarioGolden(t))
	})
}

func TestFactoryEnable_requiresCoordinator(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	t.Setenv("HERDR_PANE_ID", "w-other:p1")
	_, stderr, err := runFactory(t, home, "factory", "enable", "owner/repo")
	if err == nil {
		t.Fatalf("expected coordinator requirement, stderr=%s", stderr)
	}
	if !strings.Contains(err.Error(), "not the registered coordinator") {
		t.Fatalf("err = %v", err)
	}
}

func TestFactoryClaim_requiresCoordinator(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	t.Setenv("HERDR_PANE_ID", "w-other:p1")
	_, stderr, err := runFactory(t, home, "factory", "claim", "owner/repo", "--issue", "1")
	if err == nil {
		t.Fatalf("expected coordinator requirement, stderr=%s", stderr)
	}
	if !strings.Contains(err.Error(), "not the registered coordinator") {
		t.Fatalf("err = %v", err)
	}
}

func TestFactoryMerge_requiresCoordinator(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	t.Setenv("HERDR_PANE_ID", "w-other:p1")
	_, stderr, err := runFactory(t, home, "factory", "merge", "t-aaaaaaaaaaaa")
	if err == nil {
		t.Fatalf("expected coordinator requirement, stderr=%s", stderr)
	}
	if !strings.Contains(err.Error(), "not the registered coordinator") {
		t.Fatalf("err = %v", err)
	}
}

func runFactory(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	out, runErr := runCLIForGolden(t, home, args)
	return out, "", runErr
}
