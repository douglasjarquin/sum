package updatecmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/release"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestApply_fastForwardsCleanInstallationCheckout(t *testing.T) {
	lab := newApplyLab(t, applyLabOpts{})
	view, err := Apply(lab.store, lab.ctx, lab.newSHA, true)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	head := git(t, lab.root, "rev-parse", "HEAD")
	if head != lab.newSHA {
		t.Fatalf("HEAD = %s, want selected SHA %s\nview=%s", head, lab.newSHA, dump(view))
	}
	agents, err := os.ReadFile(filepath.Join(lab.root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(agents) != "new instructions\n" {
		t.Fatalf("AGENTS.md = %q, want new instructions", agents)
	}
	if got := currentSHA(t, lab.root); got != lab.newSHA {
		t.Fatalf(".local/current SHA = %s, want %s", got, lab.newSHA)
	}
	if row := checkoutInstructions(view); row != nil {
		t.Fatalf("clean fast-forward still deferred checkout-instructions: %s", dump(row))
	}
}

func TestApply_refusesDirtyCheckoutAndKeepsRuntime(t *testing.T) {
	lab := newApplyLab(t, applyLabOpts{})
	dirty := filepath.Join(lab.root, "AGENTS.md")
	if err := os.WriteFile(dirty, []byte("DIRTY MARKER\nold instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := Apply(lab.store, lab.ctx, lab.newSHA, true)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	head := git(t, lab.root, "rev-parse", "HEAD")
	if head != lab.oldSHA {
		t.Fatalf("dirty HEAD moved: %s, want %s\nview=%s", head, lab.oldSHA, dump(view))
	}
	body, err := os.ReadFile(dirty)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "DIRTY MARKER") {
		t.Fatalf("dirty file was reverted: %q", body)
	}
	if got := currentSHA(t, lab.root); got != lab.newSHA {
		t.Fatalf(".local/current SHA = %s, want %s (runtime must still switch)", got, lab.newSHA)
	}
	row := checkoutInstructions(view)
	if row == nil {
		t.Fatalf("dirty apply omitted checkout-instructions\nview=%s", dump(view))
	}
	reason := strField(row, "reason")
	if reason == "" || !strings.Contains(strings.ToLower(reason), "tracked") {
		t.Fatalf("refuse reason = %q, want a tracked-change reason\nrow=%s", reason, dump(row))
	}
}

func TestApply_doesNotMutateNonInstallationPath(t *testing.T) {
	lab := newApplyLab(t, applyLabOpts{otherClone: true})
	otherHead := git(t, lab.other, "rev-parse", "HEAD")
	if otherHead != lab.oldSHA {
		t.Fatalf("other clone HEAD = %s, want %s", otherHead, lab.oldSHA)
	}
	if _, err := Apply(lab.store, lab.ctx, lab.newSHA, true); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := git(t, lab.other, "rev-parse", "HEAD"); got != lab.oldSHA {
		t.Fatalf("non-installation clone HEAD moved to %s, want %s", got, lab.oldSHA)
	}
	if git(t, lab.root, "rev-parse", "HEAD") != lab.newSHA {
		t.Fatalf("installation HEAD = %s, want %s", git(t, lab.root, "rev-parse", "HEAD"), lab.newSHA)
	}
}

type applyLabOpts struct {
	otherClone bool
}

type applyLab struct {
	root, home, other string
	oldSHA, newSHA    string
	store             *store.Store
	ctx               *ordjson.Object
}

func newApplyLab(t *testing.T, opts applyLabOpts) *applyLab {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "install")
	origin := filepath.Join(base, "origin.git")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-b", "main")
	git(t, root, "config", "user.name", "sum test")
	git(t, root, "config", "user.email", "sum@example.invalid")
	git(t, root, "config", "commit.gpgsign", "false")
	writeFile(t, filepath.Join(root, ".gitignore"), ".sum/\n.local/\n")
	writeFile(t, filepath.Join(root, "AGENTS.md"), "old instructions\n")
	writeFile(t, filepath.Join(root, "bin", "sumctl"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(root, "bin", "sumctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "old")
	oldSHA := git(t, root, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(root, "AGENTS.md"), "new instructions\n")
	git(t, root, "add", "AGENTS.md")
	git(t, root, "commit", "-m", "new")
	newSHA := git(t, root, "rev-parse", "HEAD")

	git(t, base, "clone", "--bare", "--quiet", root, origin)
	git(t, root, "remote", "add", "origin", origin)
	git(t, root, "fetch", "--quiet", "origin")
	git(t, root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	git(t, root, "reset", "--hard", oldSHA)

	home := filepath.Join(root, ".sum")
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, "state.json"), `{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00"}`+"\n")
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	ctx := ordjson.NewObject()
	ctx.Set("session", "sum-test")
	ctx.Set("pane", "w-parent:p1")
	ctx.Set("machine", host)
	ctx.Set("cwd", root)
	owner := ordjson.NewObject()
	owner.Set("session", "sum-test")
	owner.Set("pane", "w-parent:p1")
	owner.Set("machine", host)
	owner.Set("role", "coordinator")
	if err := ordjson.WriteFile(filepath.Join(home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Register(store.EndpointFromContext(ctx), "coordinator", nil); err != nil {
		t.Fatal(err)
	}
	buildCompatibleRelease(t, filepath.Join(root, ".local", "releases"), newSHA)

	lab := &applyLab{root: root, home: home, oldSHA: oldSHA, newSHA: newSHA, store: st, ctx: ctx}
	if opts.otherClone {
		other := filepath.Join(base, "other")
		git(t, base, "clone", "--quiet", origin, other)
		git(t, other, "reset", "--hard", oldSHA)
		lab.other = other
	}
	return lab
}

func buildCompatibleRelease(t *testing.T, releasesRoot, sha string) {
	t.Helper()
	dir := filepath.Join(releasesRoot, sha)
	required := []struct{ rel, content string }{
		{"bin/sumctl", "#!/bin/sh\nexit 0\n"},
		{"bin/herdr-mesh", "#!/bin/sh\necho herdr-mesh\n"},
		{"bin/herdr-scoped", "#!/bin/sh\necho herdr-scoped\n"},
		{"go/cmd/sumctl/main.go", "package main\n"},
		{"skills/sum-worker/SKILL.md", "# worker\n"},
	}
	var filesEntries []string
	for _, entry := range required {
		writeFile(t, filepath.Join(dir, entry.rel), entry.content)
		filesEntries = append(filesEntries, fmt.Sprintf("%q: %q", entry.rel, "sha256:"+sha256Hex([]byte(entry.content))))
	}
	if err := os.Chmod(filepath.Join(dir, "bin", "sumctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	var toolPaths []string
	for _, name := range release.CoreTools {
		targetRel := filepath.Join("native", name)
		writeFile(t, filepath.Join(dir, targetRel), "#!/bin/sh\n")
		if err := os.Chmod(filepath.Join(dir, targetRel), 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, targetRel)
		link := filepath.Join(dir, ".local", "bin", name)
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		toolPaths = append(toolPaths, fmt.Sprintf("%q: %q", name, target))
	}
	manifest := fmt.Sprintf(`{
  "schema": 1, "kind": "sum-release", "sum_version": "0.1.0",
  "source": {"sha": %q, "tree": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "repository": "/tmp/installation"},
  "files": {%s},
  "dependencies": {
    "tools": {"pins": {}, "paths": {%s}},
    "codegraph": {"package": "@colbymchenry/codegraph", "version": "1.5.0", "license": "MIT"},
    "inventory": null,
    "native": {}
  },
  "contracts": {},
  "supports": {"state_schema": [1], "brief_schema": [1]},
  "staged_at": "2026-01-01T00:00:00+00:00", "staged_by": {"machine": "m1", "installation": "/tmp/installation", "instance": null}
}`, sha, strings.Join(filesEntries, ", "), strings.Join(toolPaths, ", "))
	writeFile(t, filepath.Join(dir, "release.json"), manifest)
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func currentSHA(t *testing.T, root string) string {
	t.Helper()
	link := filepath.Join(root, ".local", "current")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink %s: %v", link, err)
	}
	path := target
	if !filepath.IsAbs(target) {
		path = filepath.Join(filepath.Dir(link), target)
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Base(resolved)
}

func checkoutInstructions(view *ordjson.Object) *ordjson.Object {
	if view == nil {
		return nil
	}
	compat := asObject(func() any { v, _ := view.Get("compatibility"); return v }())
	if compat == nil {
		return nil
	}
	raw, _ := compat.Get("deferred")
	list, _ := raw.([]any)
	for _, item := range list {
		row := asObject(item)
		if row == nil {
			continue
		}
		if strField(row, "what") == "checkout-instructions" {
			return row
		}
	}
	return nil
}

func strField(obj *ordjson.Object, key string) string {
	if obj == nil {
		return ""
	}
	v, _ := obj.Get(key)
	s, _ := v.(string)
	return s
}

func dump(v any) string {
	encoded, err := ordjson.MarshalCompact(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(encoded)
}
