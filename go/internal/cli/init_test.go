package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit_rejectsUnrecognizedRoleNatively(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	root := NewRoot("", &stdout, &stderr)
	root.SetArgs([]string{"--home", home, "init", "--role", "bogus"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected invalid init arguments")
	}
	if err.Error() != "invalid init arguments" {
		t.Fatalf("err = %v", err)
	}
}

func TestInitUnknownFlagsAreUsageErrorsBeforeInit(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "unknown flag", args: []string{"init", "--unexpected"}, unknown: true},
		{name: "unknown flag before role", args: []string{"init", "--unexpected", "--role", "developer"}, unknown: true},
		{name: "extra positional", args: []string{"init", "extra"}},
		{name: "extra after dashdash", args: []string{"init", "--", "extra"}},
		{name: "extra after --reclaim", args: []string{"init", "--reclaim", "extra"}},
		{name: "missing --role value", args: []string{"init", "--role"}},
		{name: "missing --task value", args: []string{"init", "--task"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearNativeEnv(t)
			home := writeDesignatedHome(t)
			before := readContextFile(t, home)
			stdout, stderr, err := runInit(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertInitUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			if got := readContextFile(t, home); got != before {
				t.Fatalf("context.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
			}
		})
	}
}

func runInit(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertInitUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "registered coordinator") || strings.Contains(msg, "HC terminal") {
		t.Fatalf("err = %v, want a usage failure", err)
	}
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "accepts 0 arg") ||
		strings.Contains(msg, "flag needs an argument") ||
		strings.Contains(msg, "invalid init arguments")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func readContextFile(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "context.json"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestDeveloperInitDoesNotRequireNativeRuntime(t *testing.T) {
	clearFallbackIdentity(t)
	checkout := t.TempDir()
	home := filepath.Join(checkout, ".sum")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "dev.json"), []byte(`{"schema":1,"kind":"development","installation":"/retained/installation"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"SUM_SESSION", "HC_SESSION", "HC_SOCKET", "HC_TERMINAL_ID", "SUM_CORE_ID", "SUM_INSTALL_ROOT", "SUM_HOME"} {
		t.Setenv(key, "")
	}
	var out, errOut bytes.Buffer
	root := NewRoot(filepath.Join(checkout, "bin", "sumctl"), &out, &errOut)
	root.SetArgs([]string{"--home", home, "--format", "json", "init"})
	if err := root.Execute(); err != nil {
		t.Fatalf("developer init requires runtime: %v", err)
	}
	if !strings.Contains(out.String(), `"role": "developer"`) && !strings.Contains(out.String(), `"role":"developer"`) {
		t.Fatalf("%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, "state.json")); !os.IsNotExist(err) {
		t.Fatal("developer init wrote state")
	}
}
