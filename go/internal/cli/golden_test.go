package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	tmpPathPattern = regexp.MustCompile(`(?i)(?:/private)?(?:/var/folders/[^\s"'\\]+|/tmp/Test[^\s"'\\]+)`)
	gitSHAPattern  = regexp.MustCompile(`\b[0-9a-f]{40}\b`)
	cursorHashPat  = regexp.MustCompile(`\.[0-9a-f]{12}\.`)
	codegraphPat   = regexp.MustCompile(`/[^\s"']+/\.local/releases/[^\s"']*codegraph`)
)

func pathAliases(p string) []string {
	if p == "" {
		return nil
	}
	seen := map[string]bool{p: true}
	out := []string{p}
	add := func(alias string) {
		if alias != "" && !seen[alias] {
			seen[alias] = true
			out = append(out, alias)
		}
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		add(resolved)
	}
	if strings.HasPrefix(p, "/var/") {
		add("/private" + p)
	}
	if strings.HasPrefix(p, "/tmp/") {
		add("/private" + p)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func TestNormalizeCLIOutputKeepsTokens(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	in := "worktree: /tmp/TestFoo/002/checkout\ncandidate: " + sha + "\ncursor: c0.1.abcdefabcdef.2026-01-01T00:00:00+00:00\n"
	got := normalizeCLIOutput(in, "", "")
	if !strings.Contains(got, "$TMP") || !strings.Contains(got, "$SHA") || !strings.Contains(got, ".$CURSOR.") {
		t.Fatalf("normalized = %q, want $TMP $SHA and .$CURSOR. tokens", got)
	}
	if strings.Contains(got, sha) || strings.Contains(got, "/tmp/TestFoo") {
		t.Fatalf("normalized leaked a volatile value: %q", got)
	}
}

func normalizeCLIOutput(s, home, root string) string {
	for _, alias := range pathAliases(home) {
		s = strings.ReplaceAll(s, alias, "$HOME")
	}
	for _, alias := range pathAliases(root) {
		s = strings.ReplaceAll(s, alias, "$ROOT")
	}
	s = tmpPathPattern.ReplaceAllString(s, "$$TMP")
	s = codegraphPat.ReplaceAllString(s, "$$CODEGRAPH")
	s = gitSHAPattern.ReplaceAllString(s, "$$SHA")
	s = cursorHashPat.ReplaceAllString(s, ".$$CURSOR.")
	return s
}

func goldenPath(name, suffix string) string {
	return filepath.Join("testdata", name+"."+suffix)
}

func scenarioGolden(t *testing.T) string {
	t.Helper()
	name := t.Name()
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, ",", "")
	name = strings.ReplaceAll(name, "'", "")
	name = strings.ReplaceAll(name, "(", "")
	name = strings.ReplaceAll(name, ")", "")
	return name
}

func writeGolden(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readGolden(t *testing.T, path string) string {
	t.Helper()
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s: %v (run with UPDATE_GOLDEN=1)", path, err)
	}
	return string(want)
}

func runCLIForGolden(t *testing.T, home string, args []string) (stdout string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	if home != "" {
		args = append([]string{"--home", home}, args...)
	}
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), err
}

func assertStdoutGolden(t *testing.T, home string, args []string, name string) string {
	t.Helper()
	stdout, err := runCLIForGolden(t, home, args)
	if err != nil {
		t.Fatalf("command failed: %v stdout=%s", err, stdout)
	}
	got := normalizeCLIOutput(stdout, home, repoRoot(t))
	path := goldenPath(name, "stdout")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		writeGolden(t, path, got)
		return stdout
	}
	if got != readGolden(t, path) {
		t.Fatalf("stdout =\n%s\nwant\n%s", got, readGolden(t, path))
	}
	return stdout
}

func assertStdoutGoldenNormalized(t *testing.T, home string, args []string, name string, extra func(string) string) string {
	t.Helper()
	stdout, err := runCLIForGolden(t, home, args)
	if err != nil {
		t.Fatalf("command failed: %v stdout=%s", err, stdout)
	}
	got := normalizeCLIOutput(stdout, home, repoRoot(t))
	if extra != nil {
		got = extra(got)
	}
	path := goldenPath(name, "stdout")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		writeGolden(t, path, got)
		return stdout
	}
	if got != readGolden(t, path) {
		t.Fatalf("stdout =\n%s\nwant\n%s", got, readGolden(t, path))
	}
	return stdout
}

func assertErrorGolden(t *testing.T, home string, args []string, name string) {
	t.Helper()
	stdout, err := runCLIForGolden(t, home, args)
	if err == nil {
		t.Fatalf("expected failure, stdout=%s", stdout)
	}
	got := normalizeCLIOutput(err.Error(), home, repoRoot(t))
	path := goldenPath(name, "err")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		writeGolden(t, path, got)
		return
	}
	if got != readGolden(t, path) {
		t.Fatalf("error = %q, want %q", got, readGolden(t, path))
	}
}
