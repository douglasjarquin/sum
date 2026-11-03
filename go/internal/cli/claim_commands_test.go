package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func claimCommand(t *testing.T, home, token string, args ...string) (map[string]any, error) {
	t.Helper()
	_, helper := repoReference(t)
	argv := []string{"--home", home, "--format", "json"}
	if token != "" {
		argv = append(argv, "--claim", token)
	}
	cmd := exec.Command(helper, append(argv, args...)...)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "SUM_") && !strings.HasPrefix(item, "HC_") && !strings.HasPrefix(item, "HERDR_") {
			cmd.Env = append(cmd.Env, item)
		}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	raw := stdout.Bytes()
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = stderr.Bytes()
	}
	var value map[string]any
	if decodeErr := json.Unmarshal(raw, &value); decodeErr != nil {
		t.Fatalf("claim command %v: %v: %s", args, decodeErr, raw)
	}
	return value, err
}

func claimCommandsLab(t *testing.T) (*store.Store, *store.CoordinatorSession) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "command conversation", false)
	if err != nil {
		t.Fatal(err)
	}
	host, err := s.Machine()
	if err != nil {
		t.Fatal(err)
	}
	task := ordjson.NewObject()
	task.Set("schema", json.Number("2"))
	task.Set("id", "t-aaaaaaaaaaaa")
	task.Set("status", "waiting")
	task.Set("machine", host.ID)
	task.Set("owner", session.Owner())
	task.Set("parent", nil)
	question := ordjson.NewObject()
	question.Set("id", "q-one")
	question.Set("text", "Continue?")
	question.Set("status", "open")
	question.Set("answer", nil)
	question.Set("created_at", store.Now())
	task.Set("questions", []any{question})
	task.Set("evidence", []any{})
	if err := s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	return s, session
}

func TestClaimCommandsWorkAcrossIndependentShells(t *testing.T) {
	s, session := claimCommandsLab(t)
	if value, err := claimCommand(t, s.Root, session.Token(), "settings", "set", "--worker-harness", "grok"); err != nil {
		t.Fatalf("settings: %v %v", value, err)
	}
	if value, err := claimCommand(t, s.Root, session.Token(), "answer", "t-aaaaaaaaaaaa", "q-one", "--text", "Continue with the approved work."); err != nil {
		t.Fatalf("answer: %v %v", value, err)
	}
	task, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	questions, _ := task.Get("questions")
	question := questions.([]any)[0].(*ordjson.Object)
	if answer, _ := question.Get("answer"); answer != "Continue with the approved work." {
		t.Fatalf("answer = %v", answer)
	}
}

func TestClaimCommandsRefuseStaleCapturedTokenBeforeMutation(t *testing.T) {
	s, session := claimCommandsLab(t)
	if _, err := s.ClaimCoordinator("", "replacement", true); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, s.Root)
	for _, args := range [][]string{{"settings", "set", "--worker-harness", "grok"}, {"answer", "t-aaaaaaaaaaaa", "q-one", "--text", "stale"}, {"start", "t-aaaaaaaaaaaa"}, {"cleanup", "t-aaaaaaaaaaaa", "--apply"}, {"sweep"}} {
		value, err := claimCommand(t, s.Root, session.Token(), args...)
		if err == nil || !strings.Contains(asString(value["error"]), "stale-claim") {
			t.Fatalf("%v accepted stale token: %v %v", args, value, err)
		}
	}
	assertTreeUnchanged(t, before, snapshotTree(t, s.Root))
}

func TestClaimCommandsMissingClaimNamesTheEntryAction(t *testing.T) {
	s, _ := claimCommandsLab(t)
	for _, args := range [][]string{{"settings", "set", "--worker-harness", "grok"}, {"answer", "t-aaaaaaaaaaaa", "q-one", "--text", "continue"}, {"factory", "tick"}} {
		value, err := claimCommand(t, s.Root, "", args...)
		if err == nil || !strings.Contains(asString(value["error"]), "claim_required") {
			t.Fatalf("%v missing claim = %v %v", args, value, err)
		}
		if asString(value["next"]) == "" {
			t.Fatalf("missing claim gives no next action: %v", value)
		}
	}
}

func TestClaimPumpNeedsNoNativeCoordinatorRegistration(t *testing.T) {
	s, session := claimCommandsLab(t)
	if value, err := claimCommand(t, s.Root, session.Token(), "pump"); err != nil {
		t.Fatalf("logical pump: %v %v", value, err)
	}
}

func TestClaimPreparePreservesLogicalOwnerAndStaleStartIsRefused(t *testing.T) {
	s, session := claimCommandsLab(t)
	repo := policyProject(t, t.TempDir(), "logical-prepare", map[string]string{"README.md": "Approved work.\n"})
	brief := policyBrief(t, t.TempDir())
	value, err := claimCommand(t, s.Root, session.Token(), "prepare", "--repo", repo, "--brief", brief, "--approved", "--harness", "codex")
	if err != nil {
		t.Fatalf("logical prepare: %v %v", value, err)
	}
	id := asString(value["id"])
	task, err := s.ReadTask(id)
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := task.Get("owner")
	parent, _ := task.Get("parent")
	if !store.LogicalOwnersMatch(owner.(*ordjson.Object), session.Owner()) || parent != nil {
		t.Fatalf("task ownership = %v", task)
	}
	if _, err := s.ClaimCoordinator("", "replacement", true); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, s.Root)
	value, err = claimCommand(t, s.Root, session.Token(), "start", id)
	if err == nil || !strings.Contains(asString(value["error"]), "stale-claim") {
		t.Fatalf("stale start = %v %v", value, err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, s.Root))
}
