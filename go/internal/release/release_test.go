package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const releaseFixtureSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// buildSkillRelease is a minimal bundle that satisfies every VerifyRelease check
// except the worker-skill names the caller lists. An empty list omits both names.
func buildSkillRelease(t *testing.T, workerSkills []string) string {
	t.Helper()
	dir := t.TempDir()
	entries := []struct{ rel, content string }{
		{"bin/sumctl", "#!/bin/sh\necho sumctl\n"},
		{"go/cmd/sumctl/main.go", "package main\n"},
		{"config/hc-release.json", `{"version":"0.1.14","generation":1}`},
	}
	for _, rel := range workerSkills {
		entries = append(entries, struct{ rel, content string }{rel, "# worker\n"})
	}
	var files []string
	for _, entry := range entries {
		body := []byte(entry.content)
		writeReleaseFile(t, dir, entry.rel, body)
		sum := sha256.Sum256(body)
		files = append(files, fmt.Sprintf("%q: %q", entry.rel, "sha256:"+hex.EncodeToString(sum[:])))
	}
	if err := os.Chmod(filepath.Join(dir, "bin", "sumctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	var toolPaths []string
	for _, name := range CoreTools {
		targetRel := filepath.Join("native", name)
		writeReleaseFile(t, dir, targetRel, []byte("#!/bin/sh\n"))
		if err := os.Chmod(filepath.Join(dir, targetRel), 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, targetRel)
		link := filepath.Join(dir, ".local", "bin", name)
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		toolPaths = append(toolPaths, fmt.Sprintf("%q: %q", name, target))
	}
	offered := contract.BuildRelease()
	report, err := json.Marshal(offered)
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(dir, ".local/bin/sumctl")
	body := []byte("#!/bin/sh\ncat <<'SUM_CONTRACT'\n" + string(report) + "\nSUM_CONTRACT\n")
	writeReleaseFile(t, dir, ".local/bin/sumctl", body)
	if err = os.Chmod(helper, 0755); err != nil {
		t.Fatal(err)
	}
	helperHash := sha256.Sum256(body)
	manifest := map[string]any{
		"schema": 1, "kind": "sum-release", "sum_version": offered.SumVersion,
		"source":    map[string]any{"sha": releaseFixtureSHA, "tree": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "repository": "/tmp/installation"},
		"contracts": offered.Contracts, "supports": offered.Supports,
		"staged_at": "2026-01-01T00:00:00+00:00", "staged_by": map[string]any{"machine": "m1", "installation": "/tmp/installation", "instance": nil},
	}
	var fileMap map[string]string
	if err = json.Unmarshal([]byte("{"+strings.Join(files, ",")+"}"), &fileMap); err != nil {
		t.Fatal(err)
	}
	manifest["files"] = fileMap
	var toolMap map[string]string
	if err = json.Unmarshal([]byte("{"+strings.Join(toolPaths, ",")+"}"), &toolMap); err != nil {
		t.Fatal(err)
	}
	inventory := map[string]any{"schema": 1, "dependencies": []any{map[string]any{"id": "sumctl", "source": "go/cmd/sumctl", "version": "0.1.0", "checksum": "built artifact", "license": "MIT", "platforms": []string{nativePlatform()}, "requirements": []string{}, "role": "runtime", "owner": "sum", "contracts": map[string]any{}}}}
	manifest["dependencies"] = map[string]any{
		"tools":     map[string]any{"pins": map[string]any{}, "paths": toolMap},
		"inventory": inventory,
		"native":    map[string]any{"sumctl": map[string]any{"path": ".local/bin/sumctl", "platform": nativePlatform(), "sha256": hex.EncodeToString(helperHash[:])}},
		"hc":        map[string]any{"qualified": false, "reason": "offline test fixture"},
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeReleaseFile(t, dir, Manifest, encoded)
	return dir
}

func writeReleaseFile(t *testing.T, dir, rel string, body []byte) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRelease_acceptsEitherWorkerSkillName(t *testing.T) {
	t.Run("current name", func(t *testing.T) {
		dir := buildSkillRelease(t, []string{"skills/sum-work/SKILL.md"})
		if _, err := VerifyRelease(dir, releaseFixtureSHA); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("earlier name", func(t *testing.T) {
		dir := buildSkillRelease(t, []string{"skills/sum-worker/SKILL.md"})
		if _, err := VerifyRelease(dir, releaseFixtureSHA); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("current name and alias", func(t *testing.T) {
		dir := buildSkillRelease(t, []string{"skills/sum-work/SKILL.md", "skills/sum-worker/SKILL.md"})
		if _, err := VerifyRelease(dir, releaseFixtureSHA); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("neither name", func(t *testing.T) {
		dir := buildSkillRelease(t, nil)
		_, err := VerifyRelease(dir, releaseFixtureSHA)
		if err == nil || !strings.Contains(err.Error(), "release lacks a Sum worker skill resource") {
			t.Fatalf("got %v, want the missing worker-skill refusal", err)
		}
		if _, ok := err.(*VerifyError); !ok {
			t.Fatalf("error type %T, want *VerifyError", err)
		}
	})
}

func TestVerifyReleaseRequiresActualContractAndRuntimeReceipt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ordjson.Object)
	}{
		{"manifest claims different capabilities", func(m *ordjson.Object) { c := asObject(getPath(m, "contracts")); c.Set("hc_core", "0.1.13") }},
		{"legacy MCP contract", func(m *ordjson.Object) { c := asObject(getPath(m, "contracts")); c.Set("mcp", ordjson.NewObject()) }},
		{"missing native helper", func(m *ordjson.Object) {
			d := asObject(getPath(m, "dependencies"))
			d.Set("native", ordjson.NewObject())
		}},
		{"unmatched real runtime receipt", func(m *ordjson.Object) {
			d := asObject(getPath(m, "dependencies"))
			r := ordjson.NewObject()
			r.Set("version", "0.1.14")
			d.Set("hc", r)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := buildSkillRelease(t, []string{"skills/sum-work/SKILL.md"})
			v, err := ordjson.ReadFile(filepath.Join(root, Manifest))
			if err != nil {
				t.Fatal(err)
			}
			m := asObject(v)
			tc.change(m)
			if err = ordjson.WriteFile(filepath.Join(root, Manifest), m); err != nil {
				t.Fatal(err)
			}
			if _, err = VerifyRelease(root, releaseFixtureSHA); err == nil {
				t.Fatal("unverified release accepted")
			}
		})
	}
}
