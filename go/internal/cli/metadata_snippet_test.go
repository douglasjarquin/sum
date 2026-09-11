package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMetadataSnippet_matchesThePythonReferenceAcrossScenarios(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	reference := filepath.Join(repoRoot, "bin", "sumctl")
	if _, statErr := os.Stat(reference); statErr != nil {
		t.Skipf("reference bin/sumctl not found: %v", statErr)
	}

	cases := []struct {
		name string
		home func(t *testing.T) string
		args []string
	}{
		{name: "plain", home: func(t *testing.T) string { return t.TempDir() }, args: []string{"metadata", "snippet"}},
		{name: "raw", home: func(t *testing.T) string { return t.TempDir() }, args: []string{"metadata", "snippet", "--raw"}},
		{name: "home path contains spaces (exercises shlex quoting)", home: func(t *testing.T) string {
			dir := filepath.Join(t.TempDir(), "dir with spaces")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			return dir
		}, args: []string{"metadata", "snippet"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := tc.home(t)
			fullArgs := append([]string{"--home", home}, tc.args...)

			want, err := exec.Command(reference, fullArgs...).Output()
			if err != nil {
				t.Fatalf("python reference failed: %v", err)
			}

			var stdout, stderr bytes.Buffer
			root := NewRoot(reference, &stdout, &stderr)
			root.SetArgs(fullArgs)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("go command failed: %v (stderr=%s)", err, stderr.String())
			}

			if stdout.String() != string(want) {
				t.Fatalf("go output =\n%s\nwant (python reference)\n%s", stdout.String(), want)
			}
		})
	}
}
