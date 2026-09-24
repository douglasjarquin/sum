package procedure

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func writeSource(t *testing.T, runtime, body string) string {
	t.Helper()
	path := filepath.Join(runtime, "skills", "sum-worker", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func hashOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func row(t *testing.T, rows []any, i int) *ordjson.Object {
	t.Helper()
	if i >= len(rows) {
		t.Fatalf("rows = %v, want index %d", rows, i)
	}
	r, _ := rows[i].(*ordjson.Object)
	if r == nil {
		t.Fatalf("row %d is %T", i, rows[i])
	}
	return r
}

func field(r *ordjson.Object, key string) string {
	v, _ := r.Get(key)
	return fmt.Sprint(v)
}

func TestPinWritesContentAddressedWriteOnceResource(t *testing.T) {
	runtime := t.TempDir()
	body := "# sum-worker\nprocedure one\n"
	writeSource(t, runtime, body)
	taskDir := filepath.Join(t.TempDir(), "state home", "tasks", "t-aaaaaaaaaaaa")

	rows, err := Pin(runtime, taskDir)
	if err != nil {
		t.Fatal(err)
	}
	r := row(t, rows, 0)
	sha := hashOf(body)
	wantRel := "procedure/sum-worker-" + sha[:16] + ".md"
	if field(r, "path") != wantRel || field(r, "sha256") != sha || field(r, "bytes") != fmt.Sprint(len(body)) {
		t.Fatalf("row = %v, want path %s sha %s bytes %d", r, wantRel, sha, len(body))
	}
	if field(r, "name") != "sum-worker" || field(r, "source") != "skills/sum-worker/SKILL.md" || field(r, "load") != Required {
		t.Fatalf("row identity = %v", r)
	}
	full := filepath.Join(taskDir, filepath.FromSlash(wantRel))
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("pinned mode = %v, want 0600", info.Mode().Perm())
	}
	if data, _ := os.ReadFile(full); string(data) != body {
		t.Fatalf("pinned content = %q", data)
	}
	if err := Verify(taskDir, rows); err != nil {
		t.Fatalf("Verify intact = %v", err)
	}
	if got := Path(taskDir, r); got != full {
		t.Fatalf("Path = %q, want %q", got, full)
	}

	again, err := Pin(runtime, taskDir)
	if err != nil {
		t.Fatalf("second pin of unchanged content: %v", err)
	}
	if field(row(t, again, 0), "path") != wantRel {
		t.Fatalf("second pin path = %v", again)
	}

	changed := body + "update two\n"
	writeSource(t, runtime, changed)
	next, err := Pin(runtime, taskDir)
	if err != nil {
		t.Fatal(err)
	}
	if field(row(t, next, 0), "path") == wantRel {
		t.Fatal("changed procedure reused the old file name")
	}
	if data, _ := os.ReadFile(full); string(data) != body {
		t.Fatal("changed procedure touched the earlier pinned file")
	}
	if err := Verify(taskDir, rows); err != nil {
		t.Fatalf("earlier rows no longer verify: %v", err)
	}
}

func TestSourceRefusals(t *testing.T) {
	cases := map[string]func(t *testing.T, runtime string){
		"missing": func(t *testing.T, runtime string) {},
		"empty":   func(t *testing.T, runtime string) { writeSource(t, runtime, "") },
		"oversized": func(t *testing.T, runtime string) {
			writeSource(t, runtime, strings.Repeat("x", MaxBytes+1))
		},
		"symlink": func(t *testing.T, runtime string) {
			real := filepath.Join(runtime, "elsewhere.md")
			if err := os.WriteFile(real, []byte("# linked\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(runtime, "skills", "sum-worker", "SKILL.md")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, path); err != nil {
				t.Fatal(err)
			}
		},
		"directory": func(t *testing.T, runtime string) {
			if err := os.MkdirAll(filepath.Join(runtime, "skills", "sum-worker", "SKILL.md"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"release manifest mismatch": func(t *testing.T, runtime string) {
			writeSource(t, runtime, "# sum-worker\n")
			manifest := `{"schema": 1, "kind": "sum-release", "files": {"skills/sum-worker/SKILL.md": "sha256:` + hashOf("something else") + `"}}`
			if err := os.WriteFile(filepath.Join(runtime, "release.json"), []byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"release manifest without the source": func(t *testing.T, runtime string) {
			writeSource(t, runtime, "# sum-worker\n")
			if err := os.WriteFile(filepath.Join(runtime, "release.json"), []byte(`{"schema": 1, "files": {}}`), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			runtime := t.TempDir()
			setup(t, runtime)
			taskDir := t.TempDir()
			if err := Check(runtime); err == nil {
				t.Fatal("Check accepted the source")
			} else if !strings.Contains(err.Error(), "skills/sum-worker/SKILL.md") || !strings.Contains(err.Error(), "no brief was written") {
				t.Fatalf("Check error is not actionable: %v", err)
			}
			if _, err := Pin(runtime, taskDir); err == nil {
				t.Fatal("Pin accepted the source")
			}
			if entries, _ := os.ReadDir(filepath.Join(taskDir, Dir)); len(entries) != 0 {
				t.Fatalf("refused pin wrote %v", entries)
			}
		})
	}
}

func TestReleaseManifestMatchIsAccepted(t *testing.T) {
	runtime := t.TempDir()
	body := "# sum-worker\nreleased\n"
	writeSource(t, runtime, body)
	manifest := `{"schema": 1, "kind": "sum-release", "files": {"skills/sum-worker/SKILL.md": "sha256:` + hashOf(body) + `"}}`
	if err := os.WriteFile(filepath.Join(runtime, "release.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Check(runtime); err != nil {
		t.Fatal(err)
	}
}

func TestTamperedPinnedFileIsNeverOverwritten(t *testing.T) {
	runtime := t.TempDir()
	writeSource(t, runtime, "# sum-worker\n")
	taskDir := t.TempDir()
	rows, err := Pin(runtime, taskDir)
	if err != nil {
		t.Fatal(err)
	}
	full := Path(taskDir, row(t, rows, 0))
	if err := os.WriteFile(full, []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	verr := Verify(taskDir, rows)
	if verr == nil || !strings.Contains(verr.Error(), "does not match") || !strings.Contains(verr.Error(), "brief regenerate") {
		t.Fatalf("Verify tampered = %v", verr)
	}
	if _, err := Pin(runtime, taskDir); err == nil || !strings.Contains(err.Error(), "never overwritten") {
		t.Fatalf("Pin over tampered file = %v", err)
	}
	if data, _ := os.ReadFile(full); string(data) != "tampered\n" {
		t.Fatal("Pin rewrote the tampered file")
	}
	if err := os.Rename(full, full+".aside"); err != nil {
		t.Fatal(err)
	}
	if err := Verify(taskDir, rows); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("Verify missing = %v", err)
	}
	if _, err := Pin(runtime, taskDir); err != nil {
		t.Fatalf("re-pin after moving aside: %v", err)
	}
	if err := Verify(taskDir, rows); err != nil {
		t.Fatalf("original rows after re-pin: %v", err)
	}
}

func TestVerifyRefusesEscapingPaths(t *testing.T) {
	taskDir := t.TempDir()
	for _, path := range []string{"/etc/passwd", "../x.md", "procedure/../../x.md", "brief.md"} {
		r := ordjson.NewObject()
		r.Set("name", "sum-worker")
		r.Set("path", path)
		r.Set("sha256", hashOf(""))
		if err := Verify(taskDir, []any{r}); err == nil {
			t.Fatalf("Verify accepted path %q", path)
		}
	}
}

func TestRenderedRowsSupportSeveralResources(t *testing.T) {
	runtime := t.TempDir()
	writeSource(t, runtime, "# core\n")
	extra := filepath.Join(runtime, "skills", "sum-worker", "refresh.md")
	if err := os.WriteFile(extra, []byte("# refresh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := Sources
	t.Cleanup(func() { Sources = saved })
	Sources = []Source{
		{Name: "sum-worker", Path: "skills/sum-worker/SKILL.md", Load: Required},
		{Name: "sum-worker-refresh", Path: "skills/sum-worker/refresh.md", Load: OnDemand, When: "a refresh is requested"},
	}
	taskDir := t.TempDir()
	rows, err := Pin(runtime, taskDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || field(row(t, rows, 1), "load") != OnDemand || field(row(t, rows, 1), "when") != "a refresh is requested" {
		t.Fatalf("rows = %v", rows)
	}
	if SHA(rows, "sum-worker") != hashOf("# core\n") || SHA(rows, "absent") != "" {
		t.Fatalf("SHA lookup = %q", SHA(rows, "sum-worker"))
	}
}
