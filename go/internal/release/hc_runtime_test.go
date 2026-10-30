package release

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hcRuntimeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	docs := filepath.Join(root, ".deps/hc/fixture/contract")
	if err := os.MkdirAll(docs, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	platformKey := map[string]string{"darwin-arm64": "macos-aarch64", "darwin-amd64": "macos-x86_64", "linux-arm64": "linux-aarch64", "linux-amd64": "linux-x86_64"}[nativePlatform()]
	manifestData, _ := json.Marshal(map[string]any{"version": "0.1.14", "generation": 1, "targets": map[string]any{platformKey: map[string]any{"sha256": strings.Repeat("a", 64), "url": "https://releases.herdr.dev/core/0.1.14/herdr-core-" + platformKey + ".tar.gz"}}})
	manifest := string(manifestData)
	write(filepath.Join(root, "config/hc-release.json"), manifest, 0644)
	write(filepath.Join(docs, "manifest.json"), manifest, 0644)
	binary := filepath.Join(filepath.Dir(docs), "herdr-core")
	write(binary, "#!/bin/sh\ncase \"$1\" in\n--version) echo 'hc 0.1.14';;\nschema) echo '{\"methods\":[]}';;\n--skill) echo 'fixture manual';;\n*) exit 1;;\nesac\n", 0755)
	write(filepath.Join(docs, "openrpc.json"), "{\"methods\":[]}\n", 0644)
	write(filepath.Join(docs, "manual.md"), "fixture manual\n", 0644)
	hash := func(path string) string {
		t.Helper()
		value, err := Sha256File(path)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	receipt := map[string]any{"version": "0.1.14", "generation": 1, "source_commit": "29e6d375623de40cb4820e0894aa131ba057b796", "platform": nativePlatform(), "archive_sha256": strings.Repeat("a", 64), "binary": "herdr-core", "binary_sha256": hash(binary), "schema_sha256": hash(filepath.Join(docs, "openrpc.json")), "manual_sha256": hash(filepath.Join(docs, "manual.md"))}
	body, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(docs, "provenance.json"), string(body), 0644)
	for link, target := range map[string]string{filepath.Join(root, ".local/bin/hc"): binary, filepath.Join(root, ".local/share/hc"): docs} {
		if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestVerifyHCRuntime(t *testing.T) {
	t.Run("matched executable and contract", func(t *testing.T) {
		root := hcRuntimeFixture(t)
		if err := VerifyHCRuntime(context.Background(), root); err != nil {
			t.Fatal(err)
		}
	})
	for _, member := range []string{".local/bin/hc", ".local/share/hc/openrpc.json", ".local/share/hc/manual.md", ".local/share/hc/manifest.json", ".local/share/hc/provenance.json", "config/hc-release.json"} {
		t.Run("tampered "+member, func(t *testing.T) {
			root := hcRuntimeFixture(t)
			if err := os.WriteFile(filepath.Join(root, member), []byte("tampered\n"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := VerifyHCRuntime(context.Background(), root); err == nil {
				t.Fatal("accepted unmatched runtime")
			}
		})
	}
	t.Run("offline marker refuses real matched runtime", func(t *testing.T) {
		root := hcRuntimeFixture(t)
		writeReleaseFile(t, root, Manifest, []byte(`{"dependencies":{"hc":{"qualified":false,"reason":"offline test fixture"}}}`))
		if err := VerifyHCRuntime(context.Background(), root); err == nil {
			t.Fatal("offline fixture qualified despite explicit marker")
		}
	})
	t.Run("offline fixture not qualified", func(t *testing.T) {
		if err := VerifyHCRuntime(context.Background(), t.TempDir()); err == nil {
			t.Fatal("accepted absent runtime")
		}
	})
}
