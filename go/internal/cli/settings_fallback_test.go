package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSettings_fallsBackToReferenceWhenNotShowOrHomeUnset(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	reference := filepath.Join(dir, "reference.sh")
	if err := os.WriteFile(reference, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$SUM_GO_ARGS_FILE\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_GO_ARGS_FILE", argsFile)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "set with --home", args: []string{"--home", filepath.Join(dir, "state"), "settings", "set", "--global", "3"}, want: "--home\n" + filepath.Join(dir, "state") + "\nsettings\nset\n--global\n3\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			root := NewRoot(reference, &stdout, &stderr)
			root.SetArgs(tc.args)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
			}
			got, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("reference argv = %q, want %q", got, tc.want)
			}
			os.Remove(argsFile)
		})
	}
}
