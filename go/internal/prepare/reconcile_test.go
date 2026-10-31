package prepare

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
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
	coordinator := store.Endpoint{Schema: 2, Backend: "hc", Machine: machine.ID, CoreID: fixture.CoreID, Socket: fixture.Socket, Session: "sum-test", Address: "t1", CreatedMS: 1, Role: "coordinator", Attempt: "bootstrap", BindingGeneration: 1, Cwd: root}
	pid := uint32(100000)
	fg := &hcclient.Foreground{PID: 100001, Name: "codex", Argv: []string{"codex"}, Cwd: root}
	fixture.SetTerminal(hcclient.Terminal{Address: "t1", CreatedMS: 1, Cwd: root, State: "live", PID: &pid, Foreground: fg})
	bound := incarnation.NativeEvidence{Endpoint: coordinator.Native(), PID: pid, Foreground: fg}
	owner := coordinator.Object()
	owner.Set("incarnation", bound.Record(store.Now()))
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: coordinator.Native(), Kind: "coordinator-init", Attempt: "bootstrap"})
	owner.Set("native_receipt", receipt)
	if _, err = s.Register(coordinator, "coordinator", nil, bound.Record(store.Now())); err != nil {
		t.Fatal(err)
	}
	if err = ordjson.WriteFile(filepath.Join(s.Home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	id := "t-abcdef123456"
	intent, err := taskcheckout.Plan(s.Root, repo, id, base)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := reservations.NewAttempt("worker", coordinator.Object(), nil, store.Now(), "held", nil)
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
	task.Set("parent", coordinator.Object())
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
	result, err := Reconcile(s, coordinator.Object(), runtimeRoot, filepath.Join(runtimeRoot, "bin/sumctl"), id)
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
	again, err := Reconcile(s, coordinator.Object(), runtimeRoot, filepath.Join(runtimeRoot, "bin/sumctl"), id)
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
	if len(fixture.Terminals()) != 1 {
		t.Fatal("Git reconciliation launched a terminal")
	}
	again.Set("brief", "Changed unapproved scope")
	if err = s.SaveTask(again); err != nil {
		t.Fatal(err)
	}
	if _, err = Reconcile(s, coordinator.Object(), runtimeRoot, "", id); err == nil {
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
	if _, err = Reconcile(s, coordinator.Object(), runtimeRoot, "", id); err == nil {
		t.Fatal("uncertain native launch was reconciled as fresh preparation")
	}
}
