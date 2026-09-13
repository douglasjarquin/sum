package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/lsp"
)

func TestLspEnsure_installsMissingAllowlistedBinary(t *testing.T) {
	root, logPath, helper := setupLspCheckout(t)
	payload := `{
  "hookEventName": "PostToolUse",
  "toolName": "lsp_diagnostics",
  "toolResponse": "LSP server 'basedpyright' is configured but NOT INSTALLED.\nCommand not found: basedpyright-langserver\nTo install:\n  pip install basedpyright\n"
}`
	var stdout, stderr bytes.Buffer
	cmd := NewRoot(helper, &stdout, &stderr)
	cmd.SetIn(strings.NewReader(payload))
	cmd.SetArgs([]string{"lsp", "ensure"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("lsp ensure failed: %v (stderr=%s)", err, stderr.String())
	}
	link := filepath.Join(root, ".local", "bin", "basedpyright-langserver")
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("link missing: %v", err)
	}
	logBody := readLspFile(t, logPath)
	want := "install pipx:basedpyright@" + lsp.Allowlist["basedpyright-langserver"].Version
	if !strings.Contains(logBody, want) {
		t.Fatalf("mise log = %q, want %q", logBody, want)
	}
}

func TestLspEnsure_refusesUnknownBinary(t *testing.T) {
	root, logPath, helper := setupLspCheckout(t)
	var stdout, stderr bytes.Buffer
	cmd := NewRoot(helper, &stdout, &stderr)
	cmd.SetIn(strings.NewReader(`{"toolResponse":"Command not found: evil-langserver\nNOT INSTALLED\n"}`))
	cmd.SetArgs([]string{"lsp", "ensure"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("lsp ensure should fail open, got %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, ".local", "bin", "evil-langserver")); !os.IsNotExist(err) {
		t.Fatalf("unknown binary was linked: %v", err)
	}
	if logBody := readLspFile(t, logPath); logBody != "" {
		t.Fatalf("mise was invoked for an unknown binary: %q", logBody)
	}
}

func TestLspUnknownFlagsAreUsageErrorsBeforeEnsure(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "ensure unknown flag", args: []string{"lsp", "ensure", "--unexpected"}, unknown: true},
		{name: "ensure extra positional", args: []string{"lsp", "ensure", "extra"}},
		{name: "ensure extra after dashdash", args: []string{"lsp", "ensure", "--", "extra"}},
		{name: "lsp unknown flag", args: []string{"lsp", "--unexpected"}, unknown: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			root, logPath, helper := setupLspCheckout(t)
			var stdout, stderr bytes.Buffer
			cmd := NewRoot(helper, &stdout, &stderr)
			cmd.SetIn(strings.NewReader(`{"toolResponse":"Command not found: basedpyright-langserver\nNOT INSTALLED\n"}`))
			cmd.SetArgs(tc.args)
			err := cmd.ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout.String(), stderr.String())
			}
			msg := err.Error()
			usage := strings.Contains(msg, "unknown flag") ||
				strings.Contains(msg, "accepts 0 arg") ||
				strings.Contains(msg, "command is required") ||
				strings.Contains(msg, "unknown command") ||
				strings.Contains(msg, "unrecognized arguments")
			if !usage {
				t.Fatalf("err = %v, want a usage failure", err)
			}
			if tc.unknown && !strings.Contains(msg, "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout.String() != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout.String())
			}
			if _, err := os.Lstat(filepath.Join(root, ".local", "bin", "basedpyright-langserver")); !os.IsNotExist(err) {
				t.Fatalf("unknown flag installed a binary: %v", err)
			}
			if logBody := readLspFile(t, logPath); logBody != "" {
				t.Fatalf("mise was invoked: %q", logBody)
			}
		})
	}
}

func TestLspEnsure_doesNotRetargetExistingLink(t *testing.T) {
	root, logPath, helper := setupLspCheckout(t)
	link := filepath.Join(root, ".local", "bin", "basedpyright-langserver")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "/opt/keep-this-basedpyright-langserver"
	if err := os.Symlink(original, link); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	cmd := NewRoot(helper, &stdout, &stderr)
	cmd.SetIn(strings.NewReader("Command not found: basedpyright-langserver\nNOT INSTALLED\n"))
	cmd.SetArgs([]string{"lsp", "ensure"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("lsp ensure failed: %v", err)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != original {
		t.Fatalf("link retargeted to %q, want %q", got, original)
	}
	if logBody := readLspFile(t, logPath); logBody != "" {
		t.Fatalf("mise was invoked despite an existing link: %q", logBody)
	}
}

func setupLspCheckout(t *testing.T) (root, logPath, helper string) {
	t.Helper()
	root = t.TempDir()
	helperDir := filepath.Join(root, ".local", "bin")
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		t.Fatal(err)
	}
	helper = filepath.Join(helperDir, "sumctl")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath = filepath.Join(t.TempDir(), "mise.log")
	toolBin := filepath.Join(t.TempDir(), "tools")
	if err := os.MkdirAll(toolBin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$SUM_FAKE_MISE_LOG\"\n" +
		"cmd=$1\n" +
		"shift\n" +
		"case \"$cmd\" in\n" +
		"install)\n" +
		"  mkdir -p \"$SUM_FAKE_MISE_BIN\"\n" +
		"  for name in basedpyright-langserver basedpyright gopls; do\n" +
		"    if [ ! -x \"$SUM_FAKE_MISE_BIN/$name\" ]; then\n" +
		"      printf '#!/bin/sh\\necho fake\\n' > \"$SUM_FAKE_MISE_BIN/$name\"\n" +
		"      chmod +x \"$SUM_FAKE_MISE_BIN/$name\"\n" +
		"    fi\n" +
		"  done\n" +
		"  exit 0\n" +
		"  ;;\n" +
		"which)\n" +
		"  if [ -z \"$1\" ]; then exit 1; fi\n" +
		"  echo \"$SUM_FAKE_MISE_BIN/$1\"\n" +
		"  exit 0\n" +
		"  ;;\n" +
		"*)\n" +
		"  exit 1\n" +
		"  ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_FAKE_MISE_LOG", logPath)
	t.Setenv("SUM_FAKE_MISE_BIN", toolBin)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SUM_MISE_BIN", "")
	return root, logPath, helper
}

func readLspFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
