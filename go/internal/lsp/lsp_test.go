package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const basedpyrightPayload = `{
  "hookEventName": "PostToolUse",
  "toolName": "lsp_diagnostics",
  "toolResponse": "LSP server 'basedpyright' is configured but NOT INSTALLED.\nCommand not found: basedpyright-langserver\nTo install:\n  pip install basedpyright\n"
}`

func TestAllowlist_mapsBasedpyrightLangserver(t *testing.T) {
	pin, ok := Lookup("basedpyright-langserver")
	if !ok {
		t.Fatal("allowlist missing basedpyright-langserver")
	}
	if pin.Tool != "pipx:basedpyright" {
		t.Fatalf("tool = %q, want pipx:basedpyright", pin.Tool)
	}
	if pin.Version == "" || pin.Version == "latest" {
		t.Fatalf("version = %q, want a concrete pin", pin.Version)
	}
}

func TestEnsure_installsMissingAllowlistedBinary(t *testing.T) {
	root, logPath := setupFakeMise(t)
	if err := Ensure(root, []byte(basedpyrightPayload)); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	link := filepath.Join(root, ".local", "bin", "basedpyright-langserver")
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("link missing: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("basedpyright-langserver is not a symlink")
	}
	logBody := readFile(t, logPath)
	if !strings.Contains(logBody, "install pipx:basedpyright@"+Allowlist["basedpyright-langserver"].Version) {
		t.Fatalf("mise log = %q, want install pipx:basedpyright@%s", logBody, Allowlist["basedpyright-langserver"].Version)
	}
	if !strings.Contains(logBody, "which basedpyright-langserver") {
		t.Fatalf("mise log = %q, want which basedpyright-langserver", logBody)
	}
}

func TestEnsure_refusesUnknownBinary(t *testing.T) {
	root, logPath := setupFakeMise(t)
	payload, err := json.Marshal(map[string]string{
		"toolResponse": "LSP server 'evil' is configured but NOT INSTALLED.\nCommand not found: evil-langserver\nTo install:\n  pip install evil\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = Ensure(root, payload)
	if err == nil {
		t.Fatal("expected unknown binary to be refused")
	}
	if !strings.Contains(err.Error(), "unknown LSP binary evil-langserver") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(root, ".local", "bin", "evil-langserver")); !os.IsNotExist(statErr) {
		t.Fatalf("unknown binary was linked: %v", statErr)
	}
	if logBody := readFile(t, logPath); logBody != "" {
		t.Fatalf("mise was invoked for an unknown binary: %q", logBody)
	}
}

func TestWorkspaceRoot_readsGrokAndCodexPayloads(t *testing.T) {
	grokDir := t.TempDir()
	codexDir := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone")
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name: "grok workspaceRoot",
			payload: `{
  "hookEventName": "post_tool_use",
  "hook_event_name": "PostToolUse",
  "workspaceRoot": "` + grokDir + `",
  "cwd": "` + grokDir + `",
  "toolName": "read_file",
  "toolInput": {"target_file": "go/internal/cli/lsp.go"}
}`,
			want: grokDir,
		},
		{
			name: "codex cwd",
			payload: `{
  "hook_event_name": "PostToolUse",
  "cwd": "` + codexDir + `",
  "tool_name": "Bash",
  "tool_input": {"command": "ls"},
  "tool_response": "ok"
}`,
			want: codexDir,
		},
		{
			name:    "relative cwd ignored",
			payload: `{"cwd":"relative/path"}`,
			want:    "",
		},
		{
			name:    "missing path ignored",
			payload: `{"workspaceRoot":"` + missing + `"}`,
			want:    "",
		},
		{
			name:    "empty",
			payload: "",
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := WorkspaceRoot([]byte(tc.payload))
			if got != tc.want {
				t.Fatalf("WorkspaceRoot = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMissingBinaries_readsCodexToolResponse(t *testing.T) {
	payload := []byte(`{
  "hook_event_name": "PostToolUse",
  "cwd": "/workspace",
  "tool_name": "Bash",
  "tool_response": "LSP server 'basedpyright' is configured but NOT INSTALLED.\nCommand not found: basedpyright-langserver\n"
}`)
	got := MissingBinaries(payload)
	if len(got) != 1 || got[0] != "basedpyright-langserver" {
		t.Fatalf("MissingBinaries = %v, want [basedpyright-langserver]", got)
	}
}

func TestEnsure_doesNotRetargetExistingLink(t *testing.T) {
	root, logPath := setupFakeMise(t)
	link := filepath.Join(root, ".local", "bin", "basedpyright-langserver")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "/opt/keep-this-basedpyright-langserver"
	if err := os.Symlink(original, link); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(root, []byte(basedpyrightPayload)); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != original {
		t.Fatalf("link retargeted to %q, want %q", got, original)
	}
	if logBody := readFile(t, logPath); logBody != "" {
		t.Fatalf("mise was invoked despite an existing link: %q", logBody)
	}
}

func setupFakeMise(t *testing.T) (root, logPath string) {
	t.Helper()
	root = t.TempDir()
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
		"      printf '#!/bin/sh\\necho fake-%s\\n' \"$name\" > \"$SUM_FAKE_MISE_BIN/$name\"\n" +
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
	mise := filepath.Join(bin, "mise")
	if err := os.WriteFile(mise, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUM_FAKE_MISE_LOG", logPath)
	t.Setenv("SUM_FAKE_MISE_BIN", toolBin)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SUM_MISE_BIN", "")
	return root, logPath
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
