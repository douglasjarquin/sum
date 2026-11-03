package prepare

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/taskcheckout"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

func TestReconcileInterruptedCheckoutPreservesDirtyWorkAndSealedPolicy(t *testing.T) {
	runtimeRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	fixture := hctest.New(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(root, "repo")
	if err = os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		data, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %v %s", args, e, data)
		}
		return strings.TrimSpace(string(data))
	}
	git("init", "-b", "main")
	if err = os.WriteFile(filepath.Join(repo, "README"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "README")
	git("commit", "-m", "base")
	base := git("rev-parse", "HEAD")
	s, err := store.Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	machine, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "reconciliation conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := session.Owner()
	ctx.Set("claim", session.Token())
	id := "t-abcdef123456"
	intent, err := taskcheckout.Plan(s.Root, repo, id, base)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := reservations.NewAttempt("worker", session.Owner(), nil, store.Now(), "held", nil)
	if err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", json.Number("2"))
	task.Set("id", id)
	task.Set("machine", machine.ID)
	task.Set("repository", repo)
	task.Set("base_sha", base)
	task.Set("base_branch", "main")
	task.Set("branch", intent.Branch)
	task.Set("brief", "Only this approved work.")
	task.Set("kind", "ship")
	task.Set("status", "preparing")
	task.Set("owner", session.Owner())
	task.Set("parent", nil)
	task.Set("execution", reservations.NewExecution(attempt))
	task.Set("evidence", []any{})
	intentObj, _ := ordjson.FromValue(intent)
	task.Set("checkout_intent", intentObj)
	task.Set("preparation_approval", versions.ApprovedFingerprint(task))
	task.Set("preparation_launch_sha256", preparationLaunchHash(task))
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err = taskcheckout.Create(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	dirty := filepath.Join(intent.Path, "README")
	if err = os.WriteFile(dirty, []byte("unfinished user work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Reconcile(s, ctx, runtimeRoot, filepath.Join(runtimeRoot, "bin/sumctl"), id)
	if err != nil {
		t.Fatal(err)
	}
	if policyField(result, "status") != "prepared" {
		t.Fatal("interrupted checkout did not reconcile")
	}
	policyBefore, _ := ordjson.MarshalCompact(policyField(result, "verification_policy"))
	taskPath, err := s.TaskPath(id)
	if err != nil {
		t.Fatal(err)
	}
	versionsBefore, err := os.ReadFile(filepath.Join(taskPath, versions.File))
	if err != nil {
		t.Fatal(err)
	}
	again, err := Reconcile(s, ctx, runtimeRoot, filepath.Join(runtimeRoot, "bin/sumctl"), id)
	if err != nil {
		t.Fatal(err)
	}
	policyAfter, _ := ordjson.MarshalCompact(policyField(again, "verification_policy"))
	versionsAfter, err := os.ReadFile(filepath.Join(taskPath, versions.File))
	if err != nil {
		t.Fatal(err)
	}
	if string(policyBefore) != string(policyAfter) || string(versionsBefore) != string(versionsAfter) {
		t.Fatal("reconciliation rewrote sealed policy or brief history")
	}
	bytes, err := os.ReadFile(dirty)
	if err != nil || string(bytes) != "unfinished user work\n" {
		t.Fatal("dirty work was changed")
	}
	if count := strings.Count(git("worktree", "list", "--porcelain"), "worktree "); count != 2 {
		t.Fatalf("duplicate checkout count %d", count)
	}
	if len(fixture.Terminals()) != 0 {
		t.Fatal("Git reconciliation launched a terminal")
	}
	again.Set("brief", "Changed unapproved scope")
	if err = s.SaveTask(again); err != nil {
		t.Fatal(err)
	}
	if _, err = Reconcile(s, ctx, runtimeRoot, "", id); err == nil {
		t.Fatal("changed approved scope was silently adopted")
	}
	again.Set("brief", "Only this approved work.")
	worker, err := reservations.Worker(again)
	if err != nil {
		t.Fatal(err)
	}
	native := ordjson.NewObject()
	native.Set("effect", "uncertain")
	worker.Set("native_launch", native)
	again.Set("status", "needs-attention")
	if err = s.SaveTask(again); err != nil {
		t.Fatal(err)
	}
	if _, err = Reconcile(s, ctx, runtimeRoot, "", id); err == nil {
		t.Fatal("uncertain native launch was reconciled as fresh preparation")
	}
}
