package updatecmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/hookstatus"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/release"
	"github.com/douglasjarquin/sum/go/internal/settings"
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

func TestApply_preservesExplicitNativeEventOptOut(t *testing.T) {
	lab := newApplyLab(t, applyLabOpts{})
	if err := settings.SetNativeEventsEnabled(lab.store, false); err != nil {
		t.Fatalf("disable native events: %v", err)
	}
	view, err := Apply(lab.store, lab.ctx, lab.newSHA, true)
	if err != nil {
		t.Fatalf("apply after explicit disable: %v", err)
	}
	activation := asObject(func() any { v, _ := view.Get("observer_activation"); return v }())
	if skipped, _ := activation.Get("skipped"); skipped != true {
		t.Fatalf("hook activation = %v, want skipped", activation)
	}
	enabled, err := settings.NativeEventsEnabled(lab.store)
	if err != nil || enabled {
		t.Fatalf("native events enabled = %v, err = %v", enabled, err)
	}
}

func TestApply_launchesNativeObserverAfterSelection(t *testing.T) {
	lab := newApplyLab(t, applyLabOpts{})
	startNativeObserverProgram(t, lab)
	if err := settings.SetNativeEventsEnabled(lab.store, true); err != nil {
		t.Fatal(err)
	}
	view, err := Apply(lab.store, lab.ctx, lab.newSHA, true)
	if err != nil {
		t.Fatal(err)
	}
	activation := asObject(func() any { v, _ := view.Get("observer_activation"); return v }())
	if strField(activation, "readiness") != "ready" {
		t.Fatalf("observer bootstrap unconfirmed: %s", dump(activation))
	}
	if strField(activation, "launch_effect") != "submitted" {
		t.Fatalf("observer launch: %s", dump(activation))
	}
	count := 0
	for _, call := range lab.fixture.Calls() {
		if call.Method == "terminal.run" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("observer launches=%d", count)
	}
}

func TestApply_uncertainObserverLaunchReportsSelectedRuntimeWithoutRetry(t *testing.T) {
	lab := newApplyLab(t, applyLabOpts{})
	if err := settings.SetNativeEventsEnabled(lab.store, true); err != nil {
		t.Fatal(err)
	}
	lab.fixture.QueueFault("terminal.run", hctest.Fault{LoseResponse: true})
	_, err := Apply(lab.store, lab.ctx, lab.newSHA, true)
	if err == nil || !strings.Contains(err.Error(), "runtime selection completed") {
		t.Fatalf("uncertain observer launch: %v", err)
	}
	if currentSHA(t, lab.root) != lab.newSHA {
		t.Fatal("lost observer response silently changed runtime selection")
	}
	health, err := hookstatus.Health(lab.store)
	if err != nil {
		t.Fatal(err)
	}
	if strField(health, "launch_effect") != "uncertain" {
		t.Fatalf("observer health: %s", dump(health))
	}
	count := 0
	for _, call := range lab.fixture.Calls() {
		if call.Method == "terminal.run" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("observer launch replayed %d times", count)
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
	fixture           *hctest.Fixture
}

func newApplyLab(t *testing.T, opts applyLabOpts) *applyLab {
	t.Helper()
	lab := nativeApplyLab(t)
	if opts.otherClone {
		lab.other = filepath.Join(t.TempDir(), "other")
		origin := git(t, lab.root, "remote", "get-url", "origin")
		git(t, t.TempDir(), "clone", "--quiet", origin, lab.other)
		git(t, lab.other, "reset", "--hard", lab.oldSHA)
	}
	return lab
}

func buildCompatibleRelease(t *testing.T, releasesRoot, sha string) {
	t.Helper()
	buildCompatibleReleaseSkills(t, releasesRoot, sha, []string{"skills/sum-work/SKILL.md"})
}

// buildCompatibleReleaseSkills is buildCompatibleRelease with an explicit worker-skill
// file list. An empty list omits every worker-skill path so verification must refuse.
func buildCompatibleReleaseSkills(t *testing.T, releasesRoot, sha string, workerSkills []string) {
	t.Helper()
	root := filepath.Dir(filepath.Dir(releasesRoot))
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	tree := git(t, root, "rev-parse", sha+"^{tree}")
	dir := filepath.Join(releasesRoot, sha)
	required := []struct{ rel, content string }{
		{"bin/sumctl", "#!/bin/sh\nexit 0\n"},
		{"go/cmd/sumctl/main.go", "package main\n"},
	}
	for _, rel := range workerSkills {
		required = append(required, struct{ rel, content string }{rel, "# worker\n"})
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
		if existing, err := os.Readlink(link); err == nil && existing == target {
		} else if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		toolPaths = append(toolPaths, fmt.Sprintf("%q: %q", name, target))
	}
	manifest := fmt.Sprintf(`{
  "schema": 1, "kind": "sum-release", "sum_version": "0.1.0",
  "source": {"sha": %q, "tree": %q, "repository": %q},
  "files": {%s},
  "dependencies": {
    "tools": {"pins": {}, "paths": {%s}},
    "codegraph": {"package": "@colbymchenry/codegraph", "version": "1.5.0", "license": "MIT"},
    "inventory": null,
    "native": {}
  },
  "contracts": {},
  "supports": {"state_schema": [1], "brief_schema": [1]},
  "staged_at": "2026-01-01T00:00:00+00:00", "staged_by": {"machine": "m1", "installation": %q, "instance": null}
}`, sha, tree, root, strings.Join(filesEntries, ", "), strings.Join(toolPaths, ", "), root)
	writeFile(t, filepath.Join(dir, "release.json"), manifest)
	plantNativeHelper(t, dir, workingHelper(""))
	stateStore, err := store.Open(filepath.Join(root, ".sum"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := ordjson.ReadFile(filepath.Join(stateStore.Home, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := ordjson.ReadFile(filepath.Join(dir, "release.json"))
	if err != nil {
		t.Fatal(err)
	}
	object := asObject(value)
	staged, _ := object.Get("staged_by")
	instance, _ := asObject(state).Get("instance")
	asObject(staged).Set("instance", instance)
	offered, _ := ordjson.FromValue(contract.BuildRelease())
	contracts, _ := offered.Get("contracts")
	supports, _ := offered.Get("supports")
	object.Set("contracts", contracts)
	object.Set("supports", supports)
	if err = ordjson.WriteFile(filepath.Join(dir, "release.json"), object); err != nil {
		t.Fatal(err)
	}

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

// repoRootForFixtures is this repository's root, where tests/fixtures lives.
func repoRootForFixtures(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
