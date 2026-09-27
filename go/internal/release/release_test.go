package release

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
		{"bin/herdr-mesh", "#!/bin/sh\necho herdr-mesh\n"},
		{"bin/herdr-scoped", "#!/bin/sh\necho herdr-scoped\n"},
		{"go/cmd/sumctl/main.go", "package main\n"},
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
	manifest := fmt.Sprintf(`{
  "schema": 1, "kind": "sum-release", "sum_version": "0.1.0",
  "source": {"sha": %q, "tree": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "repository": "/tmp/installation"},
  "files": {%s},
  "dependencies": {
    "tools": {"pins": {}, "paths": {%s}},
    "codegraph": {"package": "@colbymchenry/codegraph", "version": "1.5.0", "license": "MIT"},
    "inventory": null,
    "native": {}
  },
  "contracts": {}, "supports": {},
  "staged_at": "2026-01-01T00:00:00+00:00", "staged_by": {"machine": "m1", "installation": "/tmp/installation", "instance": null}
}`, releaseFixtureSHA, strings.Join(files, ", "), strings.Join(toolPaths, ", "))
	writeReleaseFile(t, dir, Manifest, []byte(manifest))
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
