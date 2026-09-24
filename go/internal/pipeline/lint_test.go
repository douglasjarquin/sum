package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func TestDiscoverBootstrap_miseDepsTask(t *testing.T) {
	checkout := writeLintCheckout(t, map[string]string{
		filepath.Join("mise-tasks", "deps"): "#!/bin/sh\nexit 0\n",
	})

	command, ok := discoverBootstrap(t.TempDir(), checkout)

	if !ok || command.Display != "mise run deps" || command.Declared != filepath.Join("mise-tasks", "deps") {
		t.Fatalf("discoverBootstrap = %+v ok=%v, want the checkout's mise deps task", command, ok)
	}
}

func TestDiscoverBootstrap_miseTomlTasksDeps(t *testing.T) {
	checkout := writeLintCheckout(t, map[string]string{
		"mise.toml": "[tasks.deps]\nrun = \"echo deps\"\n",
	})

	command, ok := discoverBootstrap(t.TempDir(), checkout)

	if !ok || command.Display != "mise run deps" || command.Declared != "mise.toml" {
		t.Fatalf("discoverBootstrap = %+v ok=%v, want [tasks.deps] in mise.toml", command, ok)
	}
}

func TestDiscoverBootstrap_prefersMiseDepsOverLockfile(t *testing.T) {
	checkout := writeLintCheckout(t, map[string]string{
		filepath.Join("mise-tasks", "deps"): "#!/bin/sh\nexit 0\n",
		"package.json":                      `{"name":"app"}`,
		"pnpm-lock.yaml":                    "lockfileVersion: '9.0'\n",
	})

	command, ok := discoverBootstrap(t.TempDir(), checkout)

	if !ok || command.Display != "mise run deps" {
		t.Fatalf("discoverBootstrap = %+v ok=%v, want mise deps ahead of the lockfile", command, ok)
	}
}

func TestDiscoverBootstrap_lockfilePicksTheManager(t *testing.T) {
	cases := []struct {
		lock    string
		bin     string
		display string
		args    []string
	}{
		{"pnpm-lock.yaml", "pnpm", "pnpm install --frozen-lockfile", []string{"install", "--frozen-lockfile"}},
		{"package-lock.json", "npm", "npm ci", []string{"ci"}},
		{"yarn.lock", "yarn", "yarn install --frozen-lockfile", []string{"install", "--frozen-lockfile"}},
	}
	for _, tc := range cases {
		t.Run(tc.bin, func(t *testing.T) {
			checkout := writeLintCheckout(t, map[string]string{
				"package.json": `{"name":"app"}`,
				tc.lock:        "lock\n",
			})
			bin := t.TempDir()
			writeLintFile(t, filepath.Join(bin, tc.bin), "#!/bin/sh\nexit 0\n")
			t.Setenv("PATH", bin)

			command, ok := discoverBootstrap(t.TempDir(), checkout)

			if !ok || command.Display != tc.display || command.Declared != tc.lock {
				t.Fatalf("discoverBootstrap = %+v ok=%v, want %s from %s", command, ok, tc.display, tc.lock)
			}
			if len(command.Argv) != 1+len(tc.args) || filepath.Base(command.Argv[0]) != tc.bin {
				t.Fatalf("argv %v, want %s %v", command.Argv, tc.bin, tc.args)
			}
			if !reflect.DeepEqual(command.Argv[1:], tc.args) {
				t.Fatalf("argv extras %v, want %v", command.Argv[1:], tc.args)
			}
		})
	}
}

func TestDiscoverBootstrap_pnpmWinsWhenSeveralLockfilesExist(t *testing.T) {
	checkout := writeLintCheckout(t, map[string]string{
		"package.json":      `{"name":"app"}`,
		"pnpm-lock.yaml":    "pnpm\n",
		"package-lock.json": "npm\n",
		"yarn.lock":         "yarn\n",
	})
	bin := t.TempDir()
	writeLintFile(t, filepath.Join(bin, "pnpm"), "#!/bin/sh\nexit 0\n")
	writeLintFile(t, filepath.Join(bin, "npm"), "#!/bin/sh\nexit 0\n")
	writeLintFile(t, filepath.Join(bin, "yarn"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin)

	command, ok := discoverBootstrap(t.TempDir(), checkout)

	if !ok || command.Display != "pnpm install --frozen-lockfile" {
		t.Fatalf("discoverBootstrap = %+v ok=%v, want pnpm when every lockfile is present", command, ok)
	}
}

func TestDiscoverBootstrap_packageJsonWithoutLockfileIsNone(t *testing.T) {
	checkout := writeLintCheckout(t, map[string]string{
		"package.json": `{"name":"app"}`,
	})

	command, ok := discoverBootstrap(t.TempDir(), checkout)

	if ok || command.Display != "" {
		t.Fatalf("discoverBootstrap = %+v ok=%v, want no guessed install", command, ok)
	}
}

func TestDiscoverBootstrap_lockfileWithoutPackageJsonIsNone(t *testing.T) {
	checkout := writeLintCheckout(t, map[string]string{
		"pnpm-lock.yaml": "lock\n",
	})

	command, ok := discoverBootstrap(t.TempDir(), checkout)

	if ok {
		t.Fatalf("discoverBootstrap = %+v, want nothing without package.json", command)
	}
}

func TestRunLint_runsMiseDepsBeforeLint(t *testing.T) {
	runtime := writeFakeMiseRuntime(t)
	checkout := writeLintCheckout(t, map[string]string{
		filepath.Join("mise-tasks", "deps"): "#!/bin/sh\necho deps-ok\ntouch .bootstrapped\nexit 0\n",
		filepath.Join("mise-tasks", "lint"): "#!/bin/sh\nif [ ! -f .bootstrapped ]; then echo lint-first; exit 2; fi\necho lint-ok\nexit 0\n",
	})
	dir := t.TempDir()

	body, summary := RunLint(runtime, checkout, dir)

	if stringField(body, "outcome") != "pass" || summary != "Passed (`mise run lint`)" {
		t.Fatalf("RunLint outcome=%q summary=%q body=%s", stringField(body, "outcome"), summary, encodeBody(t, body))
	}
	if stringField(body, "bootstrap_command") != "mise run deps" {
		t.Fatalf("bootstrap_command = %q, want mise run deps", stringField(body, "bootstrap_command"))
	}
	if stringField(body, "command") != "mise run lint" {
		t.Fatalf("command = %q", stringField(body, "command"))
	}
	if !jsonNumberIs(body, "bootstrap_exit", 0) || !jsonNumberIs(body, "exit", 0) {
		t.Fatalf("exits body=%s", encodeBody(t, body))
	}
	if stringField(body, "bootstrap_log") == "" || stringField(body, "log") == "" {
		t.Fatalf("missing logs body=%s", encodeBody(t, body))
	}
}

func TestRunLint_bootstrapFailureSkipsLint(t *testing.T) {
	runtime := writeFakeMiseRuntime(t)
	checkout := writeLintCheckout(t, map[string]string{
		filepath.Join("mise-tasks", "deps"): "#!/bin/sh\necho deps-broke\nexit 7\n",
		filepath.Join("mise-tasks", "lint"): "#!/bin/sh\necho lint-ran\nexit 0\n",
	})
	dir := t.TempDir()

	body, summary := RunLint(runtime, checkout, dir)

	if stringField(body, "outcome") != "fail" {
		t.Fatalf("outcome = %q, want fail; body=%s", stringField(body, "outcome"), encodeBody(t, body))
	}
	if summary != "Bootstrap failed (`mise run deps`): deps-broke" {
		t.Fatalf("summary = %q", summary)
	}
	if stringField(body, "command") != "mise run lint" {
		t.Fatalf("command = %q, the lint is still the declared task", stringField(body, "command"))
	}
	if bodyValue(body, "exit") != nil || bodyValue(body, "log") != nil {
		t.Fatalf("lint ran after a failed bootstrap: %s", encodeBody(t, body))
	}
	if !jsonNumberIs(body, "bootstrap_exit", 7) {
		t.Fatalf("bootstrap_exit body=%s", encodeBody(t, body))
	}
	if _, err := os.Stat(filepath.Join(checkout, "mise-tasks", "lint")); err != nil {
		t.Fatal(err)
	}
}

func TestRunLint_noDeclaredBootstrapBehavesAsToday(t *testing.T) {
	runtime := writeFakeMiseRuntime(t)
	checkout := writeLintCheckout(t, map[string]string{
		filepath.Join("mise-tasks", "lint"): "#!/bin/sh\necho lint-ok\nexit 0\n",
	})
	dir := t.TempDir()

	body, summary := RunLint(runtime, checkout, dir)

	if stringField(body, "outcome") != "pass" || summary != "Passed (`mise run lint`)" {
		t.Fatalf("RunLint outcome=%q summary=%q body=%s", stringField(body, "outcome"), summary, encodeBody(t, body))
	}
	if bodyValue(body, "bootstrap_command") != nil || bodyValue(body, "bootstrap_exit") != nil ||
		bodyValue(body, "bootstrap_seconds") != nil || bodyValue(body, "bootstrap_log") != nil {
		t.Fatalf("bootstrap fields should stay empty: %s", encodeBody(t, body))
	}
}

func TestRunLint_lockfileInstallRunsBeforeLint(t *testing.T) {
	runtime := writeFakeMiseRuntime(t)
	checkout := writeLintCheckout(t, map[string]string{
		"package.json":                      `{"name":"app"}`,
		"pnpm-lock.yaml":                    "lockfileVersion: '9.0'\n",
		filepath.Join("mise-tasks", "lint"): "#!/bin/sh\nif [ ! -f pnpm.argv ]; then echo lint-first; exit 2; fi\necho lint-ok\nexit 0\n",
	})
	bin := t.TempDir()
	writeLintFile(t, filepath.Join(bin, "pnpm"), "#!/bin/sh\necho \"$*\" > pnpm.argv\nexit 0\n")
	t.Setenv("PATH", bin)
	dir := t.TempDir()

	body, summary := RunLint(runtime, checkout, dir)

	if stringField(body, "outcome") != "pass" || summary != "Passed (`mise run lint`)" {
		t.Fatalf("RunLint outcome=%q summary=%q body=%s", stringField(body, "outcome"), summary, encodeBody(t, body))
	}
	if stringField(body, "bootstrap_command") != "pnpm install --frozen-lockfile" {
		t.Fatalf("bootstrap_command = %q", stringField(body, "bootstrap_command"))
	}
	got, err := os.ReadFile(filepath.Join(checkout, "pnpm.argv"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "install --frozen-lockfile" {
		t.Fatalf("pnpm argv = %q", got)
	}
}

func TestRunLint_missingLockfileRunnerIsUnavailableAndSkipsLint(t *testing.T) {
	runtime := writeFakeMiseRuntime(t)
	checkout := writeLintCheckout(t, map[string]string{
		"package.json":                      `{"name":"app"}`,
		"pnpm-lock.yaml":                    "lock\n",
		filepath.Join("mise-tasks", "lint"): "#!/bin/sh\necho lint-ran\nexit 0\n",
	})
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()

	body, summary := RunLint(runtime, checkout, dir)

	if stringField(body, "outcome") != outcomeUnavailable {
		t.Fatalf("outcome = %q, want unavailable; body=%s", stringField(body, "outcome"), encodeBody(t, body))
	}
	if !strings.Contains(summary, "pnpm-lock.yaml") {
		t.Fatalf("summary = %q, want the lockfile named", summary)
	}
	if bodyValue(body, "exit") != nil {
		t.Fatalf("lint ran without its installer: %s", encodeBody(t, body))
	}
}

func writeFakeMiseRuntime(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeLintFile(t, filepath.Join(root, ".local", "bin", "mise"), `#!/bin/sh
if [ "$1" = "run" ] && [ "$2" = "lint" ]; then exec ./mise-tasks/lint; fi
if [ "$1" = "run" ] && [ "$2" = "deps" ]; then exec ./mise-tasks/deps; fi
if [ "$1" = "tasks" ] && [ "$2" = "ls" ]; then echo '[]'; exit 0; fi
echo "unexpected mise $*" >&2
exit 90
`)
	return root
}

func writeLintCheckout(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		writeLintFile(t, filepath.Join(dir, name), body)
	}
	return dir
}

func writeLintFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0o644)
	if strings.HasPrefix(body, "#!") {
		mode = 0o755
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func jsonNumberIs(body *ordjson.Object, key string, want int) bool {
	value := bodyValue(body, key)
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	got, err := number.Int64()
	return err == nil && int(got) == want
}

func bodyValue(body *ordjson.Object, key string) any {
	value, _ := body.Get(key)
	return value
}

func encodeBody(t *testing.T, body *ordjson.Object) string {
	t.Helper()
	raw, err := ordjson.MarshalCompact(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
