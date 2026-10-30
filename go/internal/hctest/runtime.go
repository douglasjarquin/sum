package hctest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// InstallRuntime builds a portable synthetic executable and matched contract for
// unit tests of the activation verifier. This is deliberately a controlled
// fixture, never proof of a published HC artifact or real harness qualification.
func InstallRuntime(t testing.TB, target string) {
	t.Helper()
	platform := runtime.GOOS + "-" + runtime.GOARCH
	keys := map[string]string{"darwin-arm64": "macos-aarch64", "darwin-amd64": "macos-x86_64", "linux-arm64": "linux-aarch64", "linux-amd64": "linux-x86_64"}
	key := keys[platform]
	if key == "" {
		t.Fatalf("synthetic HC fixture unsupported platform %s", platform)
	}
	write := func(name string, body []byte, mode os.FileMode) {
		t.Helper()
		path := filepath.Join(target, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, mode); err != nil {
			t.Fatal(err)
		}
	}
	encode := func(v any) []byte {
		t.Helper()
		body, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return append(body, '\n')
	}
	hash := func(body []byte) string { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }
	archive := strings.Repeat("a", 64)
	pin := map[string]any{"version": "0.1.14", "generation": 1, "targets": map[string]any{key: map[string]any{"url": "https://releases.herdr.dev/core/0.1.14/herdr-core-" + key + ".tar.gz", "sha256": archive}}}
	manifest := encode(pin)
	if existing, err := os.ReadFile(filepath.Join(target, "config/hc-release.json")); err == nil {
		var configured map[string]any
		if json.Unmarshal(existing, &configured) == nil {
			targets, _ := configured["targets"].(map[string]any)
			selected, _ := targets[key].(map[string]any)
			if value, ok := selected["sha256"].(string); ok && len(value) == 64 {
				archive = value
				manifest = existing
			}
		}
	}

	schema := []byte("{\"synthetic_unit_fixture\":true}\n")
	manual := []byte("Synthetic HC activation unit fixture.\n")
	executable := []byte("#!/bin/sh\ncase \"$1\" in\n--version) printf '%s\\n' 'hc 0.1.14';;\nschema) cat \"$(dirname \"$0\")/hc/openrpc.json\";;\n--skill) cat \"$(dirname \"$0\")/hc/manual.md\";;\n*) exit 2;;\nesac\n")
	write("config/hc-release.json", manifest, 0644)
	write(".local/share/hc/manifest.json", manifest, 0644)
	write(".local/share/hc/openrpc.json", schema, 0644)
	write(".local/share/hc/manual.md", manual, 0644)
	write(".local/share/herdr-core", executable, 0755)
	receipt := map[string]any{"version": "0.1.14", "generation": 1, "source_commit": "29e6d375623de40cb4820e0894aa131ba057b796", "platform": platform, "archive_sha256": archive, "binary": "herdr-core", "binary_sha256": hash(executable), "schema_sha256": hash(schema), "manual_sha256": hash(manual)}
	write(".local/share/hc/provenance.json", encode(receipt), 0644)
	link := filepath.Join(target, ".local/bin/hc")
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); err == nil {
		if err = os.Rename(link, link+".before-fixture"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../share/herdr-core", link); err != nil {
		t.Fatal(err)
	}
}
