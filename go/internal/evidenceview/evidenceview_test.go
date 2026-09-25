package evidenceview

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func gitHead(t *testing.T) (worktree, sha string) {
	t.Helper()
	worktree = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", worktree}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "--quiet")
	if err := os.WriteFile(filepath.Join(worktree, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README")
	run("commit", "--quiet", "-m", "init")
	return worktree, run("rev-parse", "HEAD")
}

func openPRIdentity(sha string) *ordjson.Object {
	identity := ordjson.NewObject()
	identity.Set("number", json.Number("12"))
	identity.Set("url", "https://github.com/cofactorworks/nicebaas/pull/12")
	identity.Set("head_sha", sha)
	identity.Set("head_branch", "sum/t-example")
	identity.Set("base_branch", "main")
	pr := ordjson.NewObject()
	pr.Set("identity", identity)
	pr.Set("state", "open")
	pr.Set("complete", false)
	pr.Set("merged_for_task", false)
	pr.Set("findings", []any{})
	return pr
}

func missingStrings(view *ordjson.Object) []string {
	closure := asObject(getField(view, "closure"))
	raw := getField(closure, "missing")
	list, _ := raw.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}

func hasMissing(missing []string, want string) bool {
	for _, item := range missing {
		if item == want {
			return true
		}
	}
	return false
}

func TestView_openPRIdentityIsRecorded(t *testing.T) {
	worktree, sha := gitHead(t)
	task := ordjson.NewObject()
	task.Set("worktree", worktree)
	task.Set("pr", openPRIdentity(sha))
	view := View(task)
	missing := missingStrings(view)
	if hasMissing(missing, "complete PR identity from `pr reconcile`") {
		t.Fatalf("open PR identity still missing: %v", missing)
	}
	if asObject(getField(view, "pr")) == nil {
		t.Fatal("pr missing from view")
	}
	if truthy(getField(asObject(getField(view, "closure")), "merged_for_task")) {
		t.Fatal("merged_for_task should stay false for an open PR")
	}
}

func TestView_noPRIdentityIsMissing(t *testing.T) {
	task := ordjson.NewObject()
	view := View(task)
	if !hasMissing(missingStrings(view), "complete PR identity from `pr reconcile`") {
		t.Fatalf("missing = %v", missingStrings(view))
	}
}

func TestView_findingsBlockIdentity(t *testing.T) {
	worktree, sha := gitHead(t)
	pr := openPRIdentity(sha)
	pr.Set("findings", []any{"PR head is a fork"})
	task := ordjson.NewObject()
	task.Set("worktree", worktree)
	task.Set("pr", pr)
	if !hasMissing(missingStrings(View(task)), "complete PR identity from `pr reconcile`") {
		t.Fatal("findings should keep identity unready")
	}
}

func TestView_incompleteIdentityIsMissing(t *testing.T) {
	worktree, sha := gitHead(t)
	pr := openPRIdentity(sha)
	identity := asObject(getField(pr, "identity"))
	identity.Set("url", "")
	task := ordjson.NewObject()
	task.Set("worktree", worktree)
	task.Set("pr", pr)
	if !hasMissing(missingStrings(View(task)), "complete PR identity from `pr reconcile`") {
		t.Fatal("blank url should keep identity unready")
	}
}

func TestView_headSHAMismatchStays(t *testing.T) {
	worktree, _ := gitHead(t)
	pr := openPRIdentity(strings.Repeat("a", 40))
	task := ordjson.NewObject()
	task.Set("worktree", worktree)
	task.Set("pr", pr)
	missing := missingStrings(View(task))
	if hasMissing(missing, "complete PR identity from `pr reconcile`") {
		t.Fatalf("identity was recorded; missing = %v", missing)
	}
	want := "PR head SHA does not match the current candidate; reconcile again"
	if !hasMissing(missing, want) {
		t.Fatalf("missing = %v, want %q", missing, want)
	}
}
