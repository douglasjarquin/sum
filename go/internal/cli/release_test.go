package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"encoding/json"
	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/release"
	"runtime"
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeReleaseFile(t *testing.T, dir, rel string, content []byte, mode os.FileMode) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, content, mode); err != nil {
		t.Fatal(err)
	}
}

func buildValidRelease(t *testing.T, releasesRoot, sha string) {
	t.Helper()
	dir := filepath.Join(releasesRoot, sha)
	for name, body := range map[string]string{"bin/sumctl": "#!/bin/sh\necho sumctl\n", "go/cmd/sumctl/main.go": "package main\n", "skills/sum-work/SKILL.md": "# worker\n"} {
		writeReleaseFile(t, dir, name, []byte(body), 0755)
	}
	hctest.InstallRuntime(t, dir)
	helper, err := os.ReadFile(filepath.Join(repoRoot(t), ".local/bin/sumctl"))
	if err != nil {
		t.Fatal(err)
	}
	writeReleaseFile(t, dir, ".local/bin/sumctl", helper, 0755)
	files := map[string]string{}
	for _, name := range []string{"bin/sumctl", "go/cmd/sumctl/main.go", "skills/sum-work/SKILL.md", "config/hc-release.json"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = "sha256:" + sha256Hex(body)
	}
	tools := map[string]string{}
	for _, name := range release.CoreTools {
		if name == "hc" {
			resolved, err := os.Readlink(filepath.Join(dir, ".local/bin/hc"))
			if err != nil {
				t.Fatal(err)
			}
			tools[name] = resolved
			continue
		}
		target := filepath.Join(dir, "native", name)
		writeReleaseFile(t, dir, filepath.Join("native", name), []byte("#!/bin/sh\n"), 0755)
		link := filepath.Join(dir, ".local/bin", name)
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		tools[name] = target
	}
	offered := contract.BuildRelease()
	platform := runtime.GOOS + "-" + runtime.GOARCH
	inventory := map[string]any{"schema": 1, "dependencies": []any{map[string]any{"id": "sumctl", "source": "go/cmd/sumctl", "version": offered.SumVersion, "checksum": "built artifact", "license": "MIT", "platforms": []string{platform}, "requirements": []string{}, "role": "runtime", "owner": "sum", "contracts": map[string]any{}}}}
	var hcReceipt map[string]any
	receiptBody, err := os.ReadFile(filepath.Join(dir, ".local/share/hc/provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(receiptBody, &hcReceipt); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"schema": 1, "kind": "sum-release", "sum_version": offered.SumVersion, "source": map[string]any{"sha": sha, "tree": strings.Repeat("d", 40), "repository": "/tmp/installation"}, "files": files, "contracts": offered.Contracts, "supports": offered.Supports, "staged_at": "2026-01-01T00:00:00+00:00", "staged_by": map[string]any{"machine": "m1", "installation": "/tmp/installation", "instance": nil}, "dependencies": map[string]any{"tools": map[string]any{"pins": map[string]any{}, "paths": tools}, "inventory": inventory, "native": map[string]any{"sumctl": map[string]any{"path": ".local/bin/sumctl", "platform": platform, "sha256": sha256Hex(helper)}}, "hc": hcReceipt}}
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeReleaseFile(t, dir, "release.json", body, 0644)
}

// buildInstallation creates a synthetic "installation" root: a git repo whose .sum/state.json designates it
// (state.json present, no dev.json), matching what `installation_root` requires without touching the real
// installation this dev checkout is a worktree of.
func buildInstallation(t *testing.T) (root, home string) {
	t.Helper()
	root = t.TempDir()
	if out, err := exec.Command("git", "init", "--quiet", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	home = filepath.Join(root, ".sum")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	home = nativeTestStore(t, home).Home
	return root, home
}

func TestReleaseList_emptyAndMixedBundles(t *testing.T) {
	t.Run("no releases directory", func(t *testing.T) {
		_, home := buildInstallation(t)
		out := runRelease(t, home, []string{"release", "list"})
		if !strings.Contains(out, "releases:") && !strings.Contains(out, `"releases"`) {
			t.Fatalf("missing releases key: %s", out)
		}
	})

	t.Run("one valid release, one broken (missing manifest), one in-progress staging dir", func(t *testing.T) {
		root, home := buildInstallation(t)
		releasesRoot := filepath.Join(root, ".local", "releases")
		validSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		buildValidRelease(t, releasesRoot, validSHA)
		brokenSHA := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		if err := os.MkdirAll(filepath.Join(releasesRoot, brokenSHA), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(releasesRoot, ".staging-ccccccccc"), 0o755); err != nil {
			t.Fatal(err)
		}
		out := runRelease(t, home, []string{"--format", "json", "release", "list"})
		view := decodeCLIMap(t, out)
		states := map[string]any{}
		for _, raw := range asSlice(view["releases"]) {
			row := asMap(raw)
			states[asString(row["sha"])] = row["ok"]
		}
		if states[validSHA] != true || states[brokenSHA] != false {
			t.Fatalf("valid and broken bundle results: %v", view)
		}
		if len(asSlice(view["in_progress"])) != 1 {
			t.Fatalf("staging directory lost: %v", view)
		}

	})
}

func TestReleaseShow_shaAndPrefix(t *testing.T) {
	t.Run("full SHA match", func(t *testing.T) {
		root, home := buildInstallation(t)
		validSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		buildValidRelease(t, filepath.Join(root, ".local", "releases"), validSHA)
		out := runRelease(t, home, []string{"release", "show", validSHA})
		if !strings.Contains(out, validSHA) {
			t.Fatalf("show missing SHA: %s", out)
		}
	})

	t.Run("short SHA prefix match", func(t *testing.T) {
		root, home := buildInstallation(t)
		validSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		buildValidRelease(t, filepath.Join(root, ".local", "releases"), validSHA)
		out := runRelease(t, home, []string{"release", "show", "aaaaaaa"})
		if !strings.Contains(out, validSHA) {
			t.Fatalf("prefix show missing SHA: %s", out)
		}
	})

	t.Run("no match is a command-level failure", func(t *testing.T) {
		root, home := buildInstallation(t)
		validSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		buildValidRelease(t, filepath.Join(root, ".local", "releases"), validSHA)
		var stdout, stderr bytes.Buffer
		cli := NewRoot("", &stdout, &stderr)
		cli.SetArgs([]string{"--home", home, "release", "show", "ffffffff"})
		err := cli.ExecuteContext(context.Background())
		if err == nil {
			t.Fatalf("expected failure, got %s", stdout.String())
		}
	})
}

func runRelease(t *testing.T, home string, args []string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := NewRoot("", &stdout, &stderr)
	root.SetArgs(append([]string{"--home", home}, args...))
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("go command failed: %v (stderr=%s)", err, stderr.String())
	}
	return stdout.String()
}

func TestReleaseStage_requiresInstallationHome(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	home = nativeTestStore(t, home).Home
	root := NewRoot("", &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"--home", home, "release", "stage", "--ref", "HEAD"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected installation home requirement")
	}
}

func TestReleaseStage_unknownFlagOrExtraPositionalDoesNotStage(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "unknown flag", args: []string{"release", "stage", "--unexpected"}, unknown: true},
		{name: "extra positional after --ref", args: []string{"release", "stage", "--ref", "HEAD", "extra"}},
		{name: "extra positional after --ref=", args: []string{"release", "stage", "--ref=HEAD", "extra"}},
		{name: "extra positional after HEAD", args: []string{"release", "stage", "HEAD", "extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, home := buildInstallation(t)
			commitInstallation(t, root)
			releasesDir := filepath.Join(root, ".local", "releases")

			stdout, stderr, err := runReleaseErr(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertReleaseUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			if _, statErr := os.Stat(releasesDir); statErr == nil {
				t.Fatal("release.Stage wrote .local/releases")
			}
		})
	}
}

func TestReleaseCommands(t *testing.T) {
	validSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cases := []struct {
		name      string
		args      []string
		lab       string
		success   bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "valid list", args: []string{"release", "list"}, lab: "empty", success: true},
		{name: "valid list after dashdash", args: []string{"release", "list", "--"}, lab: "empty", success: true},
		{name: "valid show", args: []string{"release", "show", validSHA}, lab: "show", success: true},
		{name: "valid show after dashdash", args: []string{"release", "show", "--", validSHA}, lab: "show", success: true},
		{name: "valid stage default HEAD", args: []string{"release", "stage"}, lab: "empty", domainErr: "git"},
		{name: "valid stage --ref", args: []string{"release", "stage", "--ref", "HEAD"}, lab: "empty", domainErr: "git"},
		{name: "valid stage --ref=", args: []string{"release", "stage", "--ref=HEAD"}, lab: "empty", domainErr: "git"},
		{name: "valid stage positional ref", args: []string{"release", "stage", "HEAD"}, lab: "empty", domainErr: "git"},
		{name: "release missing subcommand", args: []string{"release"}, usage: true},
		{name: "show missing sha", args: []string{"release", "show"}, usage: true},
		{name: "list extra positional", args: []string{"release", "list", "extra"}, lab: "empty", usage: true},
		{name: "list unknown flag", args: []string{"release", "list", "--unexpected"}, lab: "empty", usage: true, unknown: true},
		{name: "show extra positional", args: []string{"release", "show", validSHA, "extra"}, lab: "show", usage: true},
		{name: "show unknown flag", args: []string{"release", "show", validSHA, "--unexpected"}, lab: "show", usage: true, unknown: true},
		{name: "stage extra positional", args: []string{"release", "stage", "HEAD", "extra"}, lab: "commit", usage: true},
		{name: "stage unknown flag", args: []string{"release", "stage", "--unexpected"}, lab: "commit", usage: true, unknown: true},
		{name: "stage extra after --ref", args: []string{"release", "stage", "--ref", "HEAD", "extra"}, lab: "commit", usage: true},
		{name: "stage extra after --ref=", args: []string{"release", "stage", "--ref=HEAD", "extra"}, lab: "commit", usage: true},
		{name: "stage extra after dashdash", args: []string{"release", "stage", "HEAD", "--", "extra"}, lab: "commit", usage: true},
		{name: "stage missing --ref value", args: []string{"release", "stage", "--ref"}, usage: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, home := releaseUsageLab(t, tc.lab)
			stdout, stderr, err := runReleaseErr(t, home, tc.args...)
			switch {
			case tc.success:
				if err != nil {
					t.Fatalf("err = %v stderr=%s stdout=%s", err, stderr, stdout)
				}
				if !strings.Contains(stdout, "releases") && !strings.Contains(stdout, validSHA) {
					t.Fatalf("stdout missing release payload:\n%s", stdout)
				}
			case tc.usage:
				assertReleaseUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				if root != "" {
					if _, statErr := os.Stat(filepath.Join(root, ".local", "releases")); statErr == nil && tc.lab == "commit" {
						t.Fatal("release.Stage wrote .local/releases")
					}
				}
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s", stdout)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
			}
		})
	}
}

func runReleaseErr(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot("", &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertReleaseUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "is not a sum installation") || strings.Contains(msg, "git exited") || strings.Contains(msg, "Needed a single revision") {
		t.Fatalf("err = %v, want a usage failure", err)
	}
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "accepts 0 arg") ||
		strings.Contains(msg, "accepts 1 arg") ||
		strings.Contains(msg, "accepts at most 1 arg") ||
		strings.Contains(msg, "flag needs an argument") ||
		strings.Contains(msg, "command is required") ||
		strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid release")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func releaseUsageLab(t *testing.T, lab string) (root, home string) {
	t.Helper()
	switch lab {
	case "show":
		root, home = buildInstallation(t)
		buildValidRelease(t, filepath.Join(root, ".local", "releases"), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	case "commit":
		root, home = buildInstallation(t)
		commitInstallation(t, root)
	case "empty":
		root, home = buildInstallation(t)
	default:
		home = writeDesignatedHome(t)
	}
	return root, home
}

func commitInstallation(t *testing.T, root string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "--quiet", "-m", "initial")
}
