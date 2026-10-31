package prepare

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/taskcheckout"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

type preparationLockLab struct {
	s       *store.Store
	ctx     *ordjson.Object
	runtime string
	intent  taskcheckout.Intent
	fixture *hctest.Fixture
}

func preparationLockFixture(t *testing.T) preparationLockLab {
	t.Helper()
	runtime, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
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
	if err = os.WriteFile(filepath.Join(repo, "README"), []byte("original\n"), 0600); err != nil {
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
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	fixture := hctest.New(t)
	endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: fixture.CoreID, Socket: fixture.Socket, Session: "sum-test", Address: "t1", CreatedMS: 1, Role: "coordinator", Attempt: "bootstrap", BindingGeneration: 1, Cwd: runtime}
	pid := uint32(100000)
	fg := &hcclient.Foreground{PID: 100001, Name: "codex", Argv: []string{"codex"}, Cwd: runtime}
	fixture.SetTerminal(hcclient.Terminal{Address: "t1", CreatedMS: 1, Cwd: runtime, State: "live", PID: &pid, Foreground: fg})
	bound := incarnation.NativeEvidence{Endpoint: endpoint.Native(), PID: pid, Foreground: fg}
	owner := endpoint.Object()
	owner.Set("incarnation", bound.Record(store.Now()))
	receipt, _ := ordjson.FromValue(incarnation.NativeReceipt{Endpoint: endpoint.Native(), Kind: "coordinator-init", Attempt: endpoint.Attempt})
	owner.Set("native_receipt", receipt)
	if _, err = s.Register(endpoint, "coordinator", nil, bound.Record(store.Now())); err != nil {
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
	attempt, err := reservations.NewAttempt("worker", endpoint.Object(), nil, store.Now(), "held", nil)
	if err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", json.Number("2"))
	task.Set("id", id)
	task.Set("machine", host.ID)
	task.Set("repository", repo)
	task.Set("base_sha", base)
	task.Set("base_branch", "main")
	task.Set("branch", intent.Branch)
	task.Set("brief", "Only approved scope.")
	task.Set("kind", "ship")
	task.Set("status", "preparing")
	task.Set("parent", endpoint.Object())
	task.Set("execution", reservations.NewExecution(attempt))
	task.Set("questions", []any{})
	task.Set("evidence", []any{})
	obj, _ := ordjson.FromValue(intent)
	task.Set("checkout_intent", obj)
	task.Set("preparation_approval", versions.ApprovedFingerprint(task))
	task.Set("preparation_launch_sha256", preparationLaunchHash(task))
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	return preparationLockLab{s, endpoint.Object(), runtime, intent, fixture}
}

func pausePreparationGit(t *testing.T, stage string) (entered string, release func()) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	entered = filepath.Join(directory, "entered")
	gate := filepath.Join(directory, "gate")
	if err = syscall.Mkfifo(gate, 0600); err != nil {
		t.Fatal(err)
	}
	pattern := "*\" worktree add \"*"
	if stage == "materialize" {
		pattern = "*\" ls-tree \"*"
	}
	script := "#!/bin/sh\ncase \" $* \" in " + pattern + ")\n if test ! -e " + shquote.Quote(entered) + "; then\n printf entered > " + shquote.Quote(entered) + "\n IFS= read -r release < " + shquote.Quote(gate) + "\n fi\n ;;\nesac\nexec " + shquote.Quote(realGit) + " \"$@\"\n"
	if err = os.WriteFile(filepath.Join(directory, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	var once sync.Once
	release = func() {
		once.Do(func() {
			if _, e := os.Stat(entered); e == nil {
				file, e := os.OpenFile(gate, os.O_WRONLY, 0600)
				if e == nil {
					_, _ = file.WriteString("continue\n")
					_ = file.Close()
				}
			}
		})
	}
	t.Cleanup(release)
	return entered, release
}

func waitPreparationGit(t *testing.T, entered string, done <-chan error) {
	t.Helper()
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(entered); err == nil {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("preparation ended before paused Git: %v", err)
		case <-poll.C:
		case <-deadline.C:
			t.Fatal("preparation did not reach paused Git")
		}
	}
}

func TestPreparationGitIOReleasesLedgerLock(t *testing.T) {
	for _, stage := range []string{"create", "materialize"} {
		t.Run(stage, func(t *testing.T) {
			lab := preparationLockFixture(t)
			entered, release := pausePreparationGit(t, stage)
			done := make(chan error, 1)
			go func() {
				_, err := Reconcile(lab.s, lab.ctx, lab.runtime, filepath.Join(lab.runtime, "bin/sumctl"), "t-abcdef123456")
				done <- err
			}()
			waitPreparationGit(t, entered, done)
			other, err := store.Open(lab.s.Root)
			if err != nil {
				t.Fatal(err)
			}
			type lockResult struct {
				unlock func() error
				err    error
			}
			locks := make(chan lockResult, 1)
			go func() { unlock, e := other.Lock(); locks <- lockResult{unlock, e} }()
			select {
			case held := <-locks:
				if held.err != nil {
					t.Fatal(held.err)
				}
				task, e := other.ReadTask("t-abcdef123456")
				if e != nil {
					t.Fatal(e)
				}
				task.Set("questions", []any{"concurrent question"})
				if e = other.SaveTask(task); e != nil {
					t.Fatal(e)
				}
				held.unlock()
			case <-time.After(2 * time.Second):
				release()
				<-done
				held := <-locks
				if held.unlock != nil {
					held.unlock()
				}
				t.Fatal("paused Git retained the installation ledger lock")
			}
			second := make(chan error, 1)
			go func() { _, e := Reconcile(other, lab.ctx, lab.runtime, "", "t-abcdef123456"); second <- e }()
			select {
			case e := <-second:
				if e == nil {
					t.Fatal("concurrent preparation was not serialized")
				}
			case <-time.After(2 * time.Second):
				release()
				<-done
				t.Fatal("same-task reconciliation waited behind Git instead of refusing active preparation")
			}
			if stage == "materialize" {
				if err = os.WriteFile(filepath.Join(lab.intent.Path, "README"), []byte("unfinished\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			release()
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			saved, err := lab.s.ReadTask("t-abcdef123456")
			if err != nil {
				t.Fatal(err)
			}
			questions, _ := saved.Get("questions")
			if fmt.Sprint(questions) != "[concurrent question]" {
				t.Fatal("preparation overwrote concurrent ledger records")
			}
			if stage == "materialize" {
				data, e := os.ReadFile(filepath.Join(lab.intent.Path, "README"))
				if e != nil || string(data) != "unfinished\n" {
					t.Fatal("preparation reset dirty checkout")
				}
			}
			if len(lab.fixture.Terminals()) != 1 {
				t.Fatal("Git reconciliation launched a terminal")
			}
			if _, err = taskcheckout.Verify(context.Background(), lab.intent, true); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPreparationRejectsChangedScopeAttemptAndHistoryAfterGit(t *testing.T) {
	for _, kind := range []string{"approved scope", "worker attempt", "sealed history", "cleanup intent", "checkout HEAD"} {
		t.Run(kind, func(t *testing.T) {
			lab := preparationLockFixture(t)
			entered, release := pausePreparationGit(t, "materialize")
			done := make(chan error, 1)
			go func() {
				_, err := Reconcile(lab.s, lab.ctx, lab.runtime, filepath.Join(lab.runtime, "bin/sumctl"), "t-abcdef123456")
				done <- err
			}()
			waitPreparationGit(t, entered, done)
			other, err := store.Open(lab.s.Root)
			if err != nil {
				t.Fatal(err)
			}
			unlock, err := other.Lock()
			if err != nil {
				t.Fatal(err)
			}
			task, err := other.ReadTask("t-abcdef123456")
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "approved scope":
				task.Set("brief", "A changed approval must not be adopted.")
			case "worker attempt":
				worker, e := reservations.Worker(task)
				if e != nil {
					t.Fatal(e)
				}
				worker.Set("id", "x-999999999999")
			case "cleanup intent":
				record := ordjson.NewObject()
				record.Set("intent", true)
				task.Set("cleanup", record)
			case "sealed history":
				path, e := other.TaskPath("t-abcdef123456")
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(filepath.Join(path, versions.File), []byte("concurrent sealed-history publication\n"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if err = other.SaveTask(task); err != nil {
				t.Fatal(err)
			}
			unlock()
			if kind == "checkout HEAD" {
				command := exec.Command("git", "-C", lab.intent.Path, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "concurrent candidate")
				if data, e := command.CombinedOutput(); e != nil {
					t.Fatalf("advance checkout: %v %s", e, data)
				}
			}
			dirty := filepath.Join(lab.intent.Path, "README")
			if err = os.WriteFile(dirty, []byte("unfinished concurrent work\n"), 0600); err != nil {
				t.Fatal(err)
			}
			release()
			if err = <-done; err == nil {
				t.Fatal("changed preparation was adopted")
			} else if kind != "checkout HEAD" && !strings.Contains(err.Error(), "changed during Git I/O") {
				t.Fatalf("changed preparation was adopted: %v", err)
			}
			saved, err := other.ReadTask("t-abcdef123456")
			if err != nil {
				t.Fatal(err)
			}
			if policyField(saved, "status") == "prepared" || policyField(saved, "verification_policy") != nil {
				t.Fatal("stale preparation committed a policy or marked the changed task prepared")
			}
			if kind == "approved scope" && policyField(saved, "brief") != "A changed approval must not be adopted." {
				t.Fatal("stale preparation overwrote newer scope")
			}
			if kind == "worker attempt" {
				worker, e := reservations.Worker(saved)
				if e != nil {
					t.Fatal(e)
				}
				if policyField(worker, "id") != "x-999999999999" || policyField(worker, "state") != "held" {
					t.Fatal("stale failure changed the successor attempt")
				}
			}
			if kind == "sealed history" {
				path, _ := other.TaskPath("t-abcdef123456")
				data, e := os.ReadFile(filepath.Join(path, versions.File))
				if e != nil || string(data) != "concurrent sealed-history publication\n" {
					t.Fatal("stale preparation rewrote sealed history")
				}
			}
			data, err := os.ReadFile(dirty)
			if err != nil || string(data) != "unfinished concurrent work\n" {
				t.Fatal("CAS refusal reset unfinished work")
			}
			if _, err = taskcheckout.Verify(context.Background(), lab.intent, kind != "checkout HEAD"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPreparationUnknownGitEffectReconcilesOneCheckout(t *testing.T) {
	lab := preparationLockFixture(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	first := filepath.Join(directory, "first-create")

	script := "#!/bin/sh\ncase \" $* \" in *\" worktree add \"*)\n if test ! -e " + shquote.Quote(first) + "; then\n  printf claimed > " + shquote.Quote(first) + "\n  " + shquote.Quote(realGit) + " \"$@\" || exit $?\n  exit 1\n fi\n ;;\nesac\nexec " + shquote.Quote(realGit) + " \"$@\"\n"
	if err = os.WriteFile(filepath.Join(directory, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err = Reconcile(lab.s, lab.ctx, lab.runtime, "", "t-abcdef123456"); err == nil {
		t.Fatal("lost Git creation response was accepted")
	}
	failed, err := lab.s.ReadTask("t-abcdef123456")
	if err != nil {
		t.Fatal(err)
	}
	worker, err := reservations.Worker(failed)
	if err != nil {
		t.Fatal(err)
	}
	if policyField(failed, "status") != "needs-attention" || policyField(worker, "state") != "uncertain" || policyField(failed, "checkout_intent") == nil {
		t.Fatal("unknown Git outcome lost its held intent")
	}
	dirty := filepath.Join(lab.intent.Path, "README")
	if err = os.WriteFile(dirty, []byte("keep unfinished work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Reconcile(lab.s, lab.ctx, lab.runtime, filepath.Join(lab.runtime, "bin/sumctl"), "t-abcdef123456")
	if err != nil {
		t.Fatal(err)
	}
	if policyField(result, "status") != "prepared" {
		t.Fatal("saved checkout did not reconcile")
	}
	data, err := os.ReadFile(dirty)
	if err != nil || string(data) != "keep unfinished work\n" {
		t.Fatal("reconciliation reset dirty work")
	}
	output, err := exec.Command(realGit, "-C", lab.intent.Repository, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(output), "worktree ") != 2 {
		t.Fatalf("duplicate Git checkout: %s", output)
	}
}
