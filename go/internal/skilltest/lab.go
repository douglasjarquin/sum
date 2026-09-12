package skilltest

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func python3(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	return p
}

func git(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func runPy(t *testing.T, env []string, script, cwd string, args ...string) (int, map[string]any, string, string) {
	t.Helper()
	cmd := exec.Command(python3(t), append([]string{script}, args...)...)
	cmd.Dir = cwd
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run %s: %v\n%s%s", script, err, stdout.String(), stderr.String())
		}
	}
	var record map[string]any
	if contains(args, "--json") && strings.TrimSpace(stdout.String()) != "" {
		if json.Unmarshal(stdout.Bytes(), &record) != nil {
			record = nil
		}
	}
	return code, record, stdout.String(), stderr.String()
}

func contains(args []string, needle string) bool {
	for _, a := range args {
		if a == needle {
			return true
		}
	}
	return false
}

func labEnv(t *testing.T, root, stop string) []string {
	t.Helper()
	bin := filepath.Join(stop, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	mise := filepath.Join(bin, "mise")
	if _, err := os.Lstat(mise); err != nil {
		if err := os.Symlink(filepath.Join(root, "tests/fixtures/mise.py"), mise); err != nil {
			t.Fatal(err)
		}
	}
	pythonDir := filepath.Dir(python3(t))
	var env []string
	for _, e := range os.Environ() {
		name, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(name, "SUM_") || strings.HasPrefix(name, "HERDR_") || strings.HasPrefix(name, "FAKE_") || strings.HasPrefix(name, "MISE_") || strings.HasPrefix(name, "EVIDENCE_") || strings.HasPrefix(name, "VERIFY_") {
			continue
		}
		env = append(env, e)
	}
	path := strings.Join([]string{bin, pythonDir, "/usr/bin", "/bin"}, string(os.PathListSeparator))
	if node, err := exec.LookPath("node"); err == nil {
		path = filepath.Dir(node) + string(os.PathListSeparator) + path
	}
	if ffmpeg, err := exec.LookPath("ffmpeg"); err == nil {
		path = filepath.Dir(ffmpeg) + string(os.PathListSeparator) + path
	}
	env = append(env, "PATH="+path, "FAKE_MISE_STOP="+stop, "MISE_QUIET=1")
	return env
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Name() == "__pycache__" || info.Name() == "data" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(link, target)
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func removeAll(t *testing.T, path string) {
	t.Helper()
	if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fileMap(t *testing.T, root string, skip ...string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, s := range skip {
			if strings.Contains(rel, s) {
				return nil
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = data
		return nil
	})
	return out
}

func startPy(t *testing.T, env []string, script, cwd string, extra map[string]string) (proc *exec.Cmd, line string, stdout io.ReadCloser) {
	t.Helper()
	cmd := exec.Command(python3(t), script)
	cmd.Dir = cwd
	cmd.Env = env
	for k, v := range extra {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	n, err := pipe.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd, strings.TrimSpace(string(buf[:n])), pipe
}
