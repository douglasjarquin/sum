package mesh

import (
	"strings"
	"testing"
)

// pane sets the environment every LoadConfig call needs, minus the session.
func pane(t *testing.T) {
	t.Helper()
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w-lab:p1")
	t.Setenv("SUM_HOME", t.TempDir())
	t.Setenv("SUM_SESSION", "")
	t.Setenv("HERDR_SESSION", "")
	t.Setenv("HERDR_SOCKET_PATH", "")
}

// The Mesh server and sumctl run in the same pane. When mesh had its own
// session lookup, a pane where sumctl resolved a session could still refuse to
// start the server, which reached the user as a closed MCP connection.
func TestLoadConfig_paneWithoutSessionEnvResolvesLikeSumctl(t *testing.T) {
	pane(t)
	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if config.Session != "default" {
		t.Fatalf("session = %q, want the same default sumctl uses", config.Session)
	}
}

func TestLoadConfig_explicitSessionWins(t *testing.T) {
	pane(t)
	t.Setenv("HERDR_SESSION", "from-herdr")
	t.Setenv("SUM_SESSION", "from-sum")
	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if config.Session != "from-sum" {
		t.Fatalf("session = %q, want SUM_SESSION to win", config.Session)
	}
}

func TestLoadConfig_herdrSessionUsedWhenSumSessionIsUnset(t *testing.T) {
	pane(t)
	t.Setenv("HERDR_SESSION", "from-herdr")
	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if config.Session != "from-herdr" {
		t.Fatalf("session = %q, want HERDR_SESSION", config.Session)
	}
}

func TestLoadConfig_sessionReadFromASocketPath(t *testing.T) {
	pane(t)
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/herdr/sessions/lab-7/herdr.sock")
	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if config.Session != "lab-7" {
		t.Fatalf("session = %q, want the session named by the socket path", config.Session)
	}
}

func TestLoadConfig_malformedSessionNameIsRefused(t *testing.T) {
	pane(t)
	t.Setenv("SUM_SESSION", "bad/session name")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected a malformed session name to be refused")
	}
}

// A session is resolvable anywhere; being inside a pane is the separate
// requirement, and its own error must survive the shared lookup.
func TestLoadConfig_outsideAPaneStillRefuses(t *testing.T) {
	pane(t)
	t.Setenv("HERDR_ENV", "")
	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected a refusal outside a Herdr pane")
	}
	if !strings.Contains(err.Error(), "Herdr pane") {
		t.Fatalf("error = %v, want the pane requirement", err)
	}
}
