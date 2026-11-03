package hctest

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func (f *Fixture) DefaultRoute(t testing.TB) string {
	t.Helper()
	return PointDefaultRoute(t, f.Socket)
}

func PointDefaultRoute(t testing.TB, socket string) string {
	t.Helper()
	config := t.TempDir()
	if err := os.MkdirAll(filepath.Join(config, "herdr-core"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "herdr-core/config.toml"), []byte("socket = "+strconv.Quote(socket)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", "")
	for _, key := range []string{"HC_SOCKET", "HC_SESSION", "SUM_SESSION"} {
		t.Setenv(key, "")
	}
	return config
}

func (f *Fixture) SetVersion(version string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.version = version
}
