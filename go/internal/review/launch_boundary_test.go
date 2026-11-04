package review

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/evidenceview"
	"github.com/douglasjarquin/sum/go/internal/hcboot"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/taskcheckout"
)

func firstReviewerLab(t *testing.T) (*store.Store, *ordjson.Object, string, *hctest.Fixture) {
	t.Helper()
	s, actor, _, core := recoveryLab(t)
	core.SetVersion("0.1.17")
	task, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	var template returns.NativeTemplate
	if err = decodeObject(objectField(objectField(task, "reviewer"), "native_template"), &template); err != nil {
		t.Fatal(err)
	}
	runtime := filepath.Dir(filepath.Dir(filepath.Dir(template.Spec.Binary)))
	skill := filepath.Join(runtime, "skills/sum-deliver/SKILL.md")
	if err = os.MkdirAll(filepath.Dir(skill), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(skill, []byte("# Independent reviewer\nRead the candidate.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	intent, err := taskcheckout.Plan(s.Root, asString(task, "worktree"), asString(task, "id"), evidenceview.CurrentCandidate(task))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = taskcheckout.Create(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	value, _ := ordjson.FromValue(intent)
	task.Set("checkout_intent", value)
	task.Set("worktree", intent.Path)
	task.Delete("reviewer")
	dir, _ := s.TaskPath(asString(task, "id"))
	brief := filepath.Join(dir, "brief.md")
	if err = os.WriteFile(brief, []byte("Approved scope\n"), 0600); err != nil {
		t.Fatal(err)
	}
	task.Set("brief_path", brief)
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(s.Root, "runtime-route.json")); err != nil {
		t.Fatal(err)
	}
	core.DefaultRoute(t)
	route, err := hcboot.SelectedRoute(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(route.ConfigHome, "herdr-core/templates")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config home is not fresh: %v", err)
	}
	if err = core.LoadTemplatesFrom(route.ConfigHome); err != nil {
		t.Fatal(err)
	}
	return s, actor, runtime, core
}

func TestFirstReviewerLaunchPreparesDefaultRouteBeforeTemplate(t *testing.T) {
	s, actor, runtime, core := firstReviewerLab(t)
	terminalsBefore := len(core.Terminals())
	task, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Launch(s, actor, runtime, "/tmp/sumctl", LaunchArgs{Task: asString(task, "id"), Harness: "codex"}); err != nil {
		t.Fatal(err)
	}
	if len(core.Terminals()) != terminalsBefore+1 {
		t.Fatal("first launch did not create exactly one reviewer")
	}
	instance, _ := s.Instance()
	want := launch.VisibilityLabels(instance, asString(task, "id"))
	launched, err := s.ReadTask(asString(task, "id"))
	if err != nil {
		t.Fatal(err)
	}
	created := store.EndpointFromContext(objectField(launched, "reviewer"))
	var reviewerTerminal hcclient.Terminal
	for _, candidate := range core.Terminals() {
		if candidate.Address == created.Address {
			reviewerTerminal = candidate
		}
	}
	if reviewerTerminal.Labels["herdr.tab"] != want["herdr.tab"] || reviewerTerminal.Labels["herdr.space"] != want["herdr.space"] || reviewerTerminal.Labels["sum.role"] != "reviewer" {
		t.Fatalf("reviewer was not adopted into the task tab: %v", reviewerTerminal.Labels)
	}
	if visibility := objectField(objectField(objectField(launched, "reviewer"), "native_launch"), "visibility"); asString(visibility, "state") != launch.VisibilityApplied {
		t.Fatalf("reviewer visibility = %v", visibility)
	}
	route, err := hcboot.SelectedRoute(s)
	if err != nil || route.CoreID != core.CoreID {
		t.Fatalf("route: %+v %v", route, err)
	}
}

func TestReviewerLaunchExcludesTransferThroughCreationAccounting(t *testing.T) {
	s, task, ep, core := reviewerLab(t)
	session, err := s.ClaimCoordinator("", "review launch boundary", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.RequireClaim(s, session.Token()); err != nil {
		t.Fatal(err)
	}
	task.Set("owner", session.Owner())
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	entered, release := core.HoldReplyNext("terminal.run", "")
	defer release()
	core.QueueFault("terminal.run", hctest.Fault{LoseResponse: true})
	launched := make(chan error, 1)
	go func() {
		_, err := launchPreparedNative(context.Background(), s, task, ep, ep.Object(), hcclient.RunParams{Argv: []string{"review"}, Cwd: ep.Cwd, OutliveParent: true, NoNotify: true})
		launched <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("launch never reached native creation")
	}
	other, err := store.Open(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	transferred := make(chan error, 1)
	go func() { _, err := other.ClaimCoordinator("", "replacement", true); transferred <- err }()
	select {
	case err := <-transferred:
		release()
		<-launched
		t.Fatalf("transfer completed before reviewer result accounting: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case err := <-launched:
		if err == nil {
			t.Fatal("lost response accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("launch did not finish")
	}
	select {
	case err := <-transferred:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("transfer stayed blocked after result accounting")
	}
	task, err = s.ReadTask(ep.Task)
	if err != nil {
		t.Fatal(err)
	}
	if asString(objectField(objectField(task, "reviewer"), "native_launch"), "state") != "uncertain" || len(core.Terminals()) != 1 {
		t.Fatal("lost creation was not durably retained as one uncertain reviewer")
	}
}

func TestMissingCoreDoesNotRecordReviewerLaunch(t *testing.T) {
	s, actor, runtime, core := firstReviewerLab(t)
	before := len(core.Terminals())
	socket := filepath.Join(t.TempDir(), "missing.sock")
	hctest.PointDefaultRoute(t, socket)
	_, err := Launch(s, actor, runtime, "/tmp/sumctl", LaunchArgs{Task: "t-aaaaaaaaaaaa", Harness: "codex"})
	if err == nil || !strings.Contains(err.Error(), "task t-aaaaaaaaaaaa attempt ") || !strings.Contains(err.Error(), socket) || !strings.Contains(err.Error(), "open Herdr") {
		t.Fatalf("refusal: %v", err)
	}
	task, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if objectField(task, "reviewer") != nil || len(core.Terminals()) != before {
		t.Fatal("missing core recorded or created a reviewer")
	}
}
