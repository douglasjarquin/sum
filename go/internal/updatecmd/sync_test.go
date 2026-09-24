package updatecmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A git status that fails on the installation checkout must refuse the fast-forward, never read as "clean".
func TestSyncInstallationCheckout_refusesWhenCheckoutStateCannotBeRead(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "--allow-empty", "-m", "one")
	head := git(t, root, "rev-parse", "HEAD")
	git(t, root, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "--allow-empty", "-m", "two")
	next := git(t, root, "rev-parse", "HEAD")
	git(t, root, "update-ref", "refs/remotes/origin/main", next)
	git(t, root, "reset", "-q", "--hard", head)

	shim := t.TempDir()
	script := "#!/bin/sh\nfor arg in \"$@\"; do if [ \"$arg\" = status ]; then echo 'fatal: index file corrupt' >&2; exit 128; fi; done\nexec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))

	result := syncInstallationCheckout(root, next)
	if got := strField(result, "result"); got != "refused" {
		t.Fatalf("result = %q, want refused: %s", got, dump(result))
	}
	if reason := strField(result, "reason"); !strings.Contains(reason, "checkout state cannot be read") {
		t.Fatalf("reason = %q", reason)
	}
	if now := git(t, root, "rev-parse", "HEAD"); now != head {
		t.Fatalf("checkout moved to %s despite the refusal", now)
	}
}
