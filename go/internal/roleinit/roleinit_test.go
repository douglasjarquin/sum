package roleinit

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func jsonInt(n int) json.Number {
	return json.Number(strconv.Itoa(n))
}

func fakeContext(machine, session, pane, cwd string) *ordjson.Object {
	ctx := ordjson.NewObject()
	ctx.Set("session", session)
	ctx.Set("pane", pane)
	ctx.Set("machine", machine)
	ctx.Set("cwd", cwd)
	ctx.Set("at", "2026-09-10T00:00:00+00:00")
	return ctx
}

func TestInit_requiresTaskForRoleWorker(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = Init(root, s, fakeContext("m", "s", "p", root), "worker", "")
	if err == nil || err.Error() != "--role worker needs --task TASK_ID." {
		t.Fatalf("err = %v, want the worker/task requirement message", err)
	}
}

func TestInit_refusesCoordinatorWhenNotDesignated(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = Init(root, s, fakeContext("m", "s", "p", root), "coordinator", "")
	if err == nil {
		t.Fatal("expected an error refusing coordinator role")
	}
}

func TestInit_returnsErrDesignated(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("store init: %v", err)
	}
	_, err = Init(root, s, fakeContext("m", "s", "p", root), "", "")
	if err != ErrDesignated {
		t.Fatalf("err = %v, want ErrDesignated", err)
	}
}

func TestInit_plainDeveloperWithNoHintOrMarker(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	view, err := Init(root, s, fakeContext("m", "s", "p", root), "", "")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	role, _ := view.Get("role")
	if role != "developer" {
		t.Fatalf("role = %v, want developer", role)
	}
	noteValue, _ := view.Get("note")
	want := "Development checkout: modify and test sum here only. No coordinator initialization, dispatch, production setup, or instance-wide updates."
	if noteValue != want {
		t.Fatalf("note = %q, want %q", noteValue, want)
	}
	installationHome, _ := view.Get("installation_home")
	if installationHome != nil {
		t.Fatalf("installation_home = %v, want nil (no linked worktree here)", installationHome)
	}
}

func TestInit_developmentMarkerAppendsCallbackSentence(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	marker := ordjson.NewObject()
	marker.Set("schema", jsonInt(store.Schema))
	marker.Set("kind", "development")
	marker.Set("installation", "/installations/sum")
	if err := ordjson.WriteFile(filepath.Join(root, ".sum", "dev.json"), marker); err != nil {
		t.Fatal(err)
	}

	view, err := Init(root, s, fakeContext("m", "s", "p", root), "", "")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	noteValue, _ := view.Get("note")
	want := "Development checkout: modify and test sum here only. No coordinator initialization, dispatch, production setup, or instance-wide updates. " +
		"Tests use temporary --home state and a named lab Herdr session; the installed helper at /installations/sum/bin/sumctl owns any parent-task callbacks."
	if noteValue != want {
		t.Fatalf("note = %q, want %q", noteValue, want)
	}
}

func TestMatchingTask_findsAPaneMatchingNonArchivedTask(t *testing.T) {
	hintHome := t.TempDir()
	hintStore, err := store.Open(hintHome)
	if err != nil {
		t.Fatalf("open hint store: %v", err)
	}
	if err := hintStore.Init(); err != nil {
		t.Fatalf("hint store init: %v", err)
	}

	endpoint := store.Endpoint{Machine: "m1", Session: "s1", Pane: "p1"}

	archived := ordjson.NewObject()
	archived.Set("schema", jsonInt(store.Schema))
	archived.Set("id", "t-aaaaaaaaaaaa")
	archived.Set("status", "archived")
	archived.Set("pane", "p1")
	archived.Set("session", "s1")
	archived.Set("machine", "m1")
	if err := hintStore.SaveTask(archived); err != nil {
		t.Fatalf("save archived task: %v", err)
	}

	running := ordjson.NewObject()
	running.Set("schema", jsonInt(store.Schema))
	running.Set("id", "t-bbbbbbbbbbbb")
	running.Set("status", "running")
	running.Set("pane", "p1")
	running.Set("session", "s1")
	running.Set("machine", "m1")
	if err := hintStore.SaveTask(running); err != nil {
		t.Fatalf("save running task: %v", err)
	}

	task, err := matchingTask(hintStore, endpoint)
	if err != nil {
		t.Fatalf("matchingTask: %v", err)
	}
	if task == nil {
		t.Fatal("expected a matching task, got nil")
	}
	id, _ := task.Get("id")
	if id != "t-bbbbbbbbbbbb" {
		t.Fatalf("matched task id = %v, want t-bbbbbbbbbbbb (the archived one must be skipped)", id)
	}
}

func TestMatchingTask_nilWhenNoIdentityMatches(t *testing.T) {
	hintHome := t.TempDir()
	hintStore, err := store.Open(hintHome)
	if err != nil {
		t.Fatalf("open hint store: %v", err)
	}
	if err := hintStore.Init(); err != nil {
		t.Fatalf("hint store init: %v", err)
	}
	task, err := matchingTask(hintStore, store.Endpoint{Machine: "m1", Session: "s1", Pane: "p1"})
	if err != nil {
		t.Fatalf("matchingTask: %v", err)
	}
	if task != nil {
		t.Fatalf("task = %v, want nil", task)
	}
}

func TestInstallationHint_findsTheLinkedWorktreesInstallation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	base := t.TempDir()
	installation := filepath.Join(base, "installation")
	if err := os.MkdirAll(installation, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, installation, "init", "-q")
	runGit(t, installation, "config", "user.email", "test@example.com")
	runGit(t, installation, "config", "user.name", "test")
	runGit(t, installation, "commit", "--allow-empty", "-q", "-m", "root")
	if err := os.MkdirAll(filepath.Join(installation, ".sum"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installation, ".sum", "state.json"), []byte(`{"schema":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	worktree := filepath.Join(base, "worktree")
	runGit(t, installation, "worktree", "add", worktree, "-b", "wt")

	hint, err := InstallationHint(worktree)
	if err != nil {
		t.Fatalf("installationHint: %v", err)
	}
	wantSuffix := filepath.Join(installation, ".sum")
	if hint == "" {
		t.Fatal("hint is empty, want the installation's .sum")
	}
	resolvedHint, _ := filepath.EvalSymlinks(hint)
	resolvedWant, _ := filepath.EvalSymlinks(wantSuffix)
	if resolvedHint != resolvedWant {
		t.Fatalf("hint = %s, want %s", hint, wantSuffix)
	}
}

func TestInstallationHint_emptyForAnOrdinaryRepoRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	hint, err := InstallationHint(root)
	if err != nil {
		t.Fatalf("installationHint: %v", err)
	}
	if hint != "" {
		t.Fatalf("hint = %q, want empty for a plain (non-worktree) repo root", hint)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
