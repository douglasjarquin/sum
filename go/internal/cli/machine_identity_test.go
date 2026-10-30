package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/hctest"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/taskcheckout"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// Native core and terminal identity must keep the stable machine component
// across host renames and refuse authority on a different machine.

const (
	thisHostRaw  = "11111111111111111111111111111111"
	otherHostRaw = "22222222222222222222222222222222"
)

func onHost(t *testing.T, raw, hostname string) string {
	t.Helper()
	t.Cleanup(machine.Pin(raw, hostname))
	id, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func initRole(t *testing.T, home string, args ...string) (string, error) {
	t.Helper()
	out, err := runCLI(t, home, append([]string{"init"}, args...)...)
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, out)
	}
	role, _ := decodeObject(t, out)["role"].(string)
	return role, nil
}

func mustRole(t *testing.T, home, want string) {
	t.Helper()
	role, err := initRole(t, home)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if role != want {
		t.Fatalf("init role = %q, want %q", role, want)
	}
}

func questionTask(id, taskMachine, parentMachine, cwd string) string {
	return fmt.Sprintf(`{"schema": 1, "id": %q, "status": "running", "kind": "task", "machine": %q, "session": "sum-test", "pane": "w-worker:p1",
 "parent": {"machine": %q, "session": "sum-test", "pane": "w-parent:p1", "cwd": %q},
 "questions": [{"id": "q-%s", "key": "k", "text": "still blocked?", "status": "open", "created_at": "2026-01-01T00:00:00+00:00"}],
 "attention": [], "evidence": [], "report": null, "notice": null, "brief": "worker task"}`, id, taskMachine, parentMachine, cwd, id[2:6])
}

// bindReturnsInline rebinds a task to this coordinator and requires its open
// question to have reached this pane inline, in this delivery pass or an
// earlier one, and never a second time.
func bindReturnsInline(t *testing.T, home, taskID string) {
	t.Helper()
	attachNativeTaskCheckout(t, home, taskID)
	out, err := runCLI(t, home, "bind", taskID, "--parent-only")
	if err != nil {
		t.Fatalf("bind %s: %v\n%s", taskID, err, out)
	}
	returns, _ := decodeObject(t, out)["returns"].(map[string]any)
	recipients, _ := returns["recipients"].([]any)
	for _, raw := range recipients {
		row, _ := raw.(map[string]any)
		obligations, _ := row["obligations"].([]any)
		for _, rawObligation := range obligations {
			obligation, _ := rawObligation.(map[string]any)
			if obligation["task"] != taskID {
				continue
			}
			notification, _ := obligation["notification"].(map[string]any)
			sentNow := row["state"] == "submitted" && row["via"] == "inline" && notification["attempts"] == float64(0)
			sentBefore := notification["state"] == "submitted" && notification["via"] == "inline" && notification["attempts"] == float64(1)
			if !sentNow && !sentBefore {
				t.Fatalf("bind %s return = %v, want one inline submission to this pane", taskID, row)
			}
			return
		}
	}
	t.Fatalf("bind %s routed no return to the coordinator: %v", taskID, returns)
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func sessionPath(home, machineValue, pane string) string {
	key := store.RegistrationKey(store.Endpoint{Machine: machineValue, Session: "sum-test", Pane: pane})
	return filepath.Join(home, "sessions", key+".json")
}

func TestMachineIdentity_aRenamedHostKeepsTheCoordinatorAndItsRoutes(t *testing.T) {
	id := onHost(t, thisHostRaw, "dev")
	home := writeDesignatedHome(t)
	nativeEnv(t, home)
	mustRole(t, home, "coordinator")

	onHost(t, thisHostRaw, "host-development.example")
	mustRole(t, home, "coordinator")
	home = writeTaskFixture(t, home, "t-aaaaaaaaaaaa", questionTask("t-aaaaaaaaaaaa", id, id, home))
	bindReturnsInline(t, home, "t-aaaaaaaaaaaa")
	if owner := readJSON(t, filepath.Join(home, "context.json")); owner["machine"] != id {
		t.Fatalf("coordinator machine = %v, want the stable identity %s", owner["machine"], id)
	}
}

func TestMachineIdentity_anotherMachinesCoordinatorIsStillRefused(t *testing.T) {
	onHost(t, thisHostRaw, "dev")
	home := writeDesignatedHome(t)
	nativeEnv(t, home)
	mustRole(t, home, "coordinator")
	home = writeTaskFixture(t, home, "t-aaaaaaaaaaaa", questionTask("t-aaaaaaaaaaaa", "elsewhere", "elsewhere", home))

	// Same hostname, different machine: the pane ID w-parent:p1 names a pane on
	// another Herdr server.
	onHost(t, otherHostRaw, "dev")
	if role, err := initRole(t, home); err == nil && role != "developer" {
		t.Fatalf("another machine inherited role %q", role)
	}
	if _, err := initRole(t, home, "--role", "coordinator", "--reclaim"); err == nil {
		t.Fatalf("reclaim of another machine's coordinator = %v, want the other-machine refusal", err)
	}
	onHost(t, thisHostRaw, "dev")
	out, err := runCLI(t, home, "bind", "t-aaaaaaaaaaaa", "--parent-only")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "another machine") {
		t.Fatalf("bind of a task recorded on machine %q = %v %s, want the cross-machine refusal", "elsewhere", err, out)
	}
}

// legacyHome writes records the way the hostname-keyed release did: every
// machine value is a hostname and every session file is keyed by it.
func legacyHome(t *testing.T, hostname string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1, "sum_version": "0.1.0", "created_at": "2026-01-01T00:00:00+00:00", "instance": "inst-legacy"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	owner := fmt.Sprintf(`{"machine": %q, "session": "sum-test", "pane": "w-parent:p1", "cwd": %q, "at": "2026-01-01T00:00:00+00:00", "role": "coordinator", "instance": "inst-legacy", "sum_version": "0.1.0", "claimed_at": "2026-01-01T00:00:00+00:00"}`, hostname, home)
	if err := os.WriteFile(filepath.Join(home, "context.json"), []byte(owner), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	for pane, role := range map[string]string{"w-parent:p1": "coordinator", "w-worker:p1": "worker"} {
		registration := fmt.Sprintf(`{"schema": 1, "role": %q, "task": null, "machine": %q, "session": "sum-test", "pane": %q, "instance": "inst-legacy", "registered_at": "2026-01-01T00:00:00+00:00"}`, role, hostname, pane)
		if err := os.WriteFile(sessionPath(home, hostname, pane), []byte(registration), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestMachineIdentity_hostnameKeyedRecordsRequireExplicitMigration(t *testing.T) {
	onHost(t, thisHostRaw, "dev")
	home := legacyHome(t, "dev")
	before := snapshotHome(t, home)
	for _, args := range [][]string{{"init"}, {"init", "--role", "coordinator"}, {"init", "--role", "coordinator", "--reclaim"}} {
		if out, err := runCLI(t, home, args...); err == nil || !strings.Contains(err.Error(), "unsupported state schema") {
			t.Fatalf("legacy authority adoption %v: %v %s", args, err, out)
		}
	}
	historical, err := store.OpenHistorical(home)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := historical.Owner()
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := owner.Get("machine"); value != "dev" {
		t.Fatalf("historical owner rewritten: %v", value)
	}
	if err = historical.CheckWritable(); !errors.Is(err, store.ErrHistoricalReadOnly) {
		t.Fatalf("historical write allowed: %v", err)
	}
	if snapshotHome(t, home) != before {
		t.Fatal("normal init or historical observation changed legacy records")
	}
}

func TestMachineIdentity_historicalNamesNeverGrantNativeAuthority(t *testing.T) {
	home := legacyHome(t, "dev")
	before := snapshotHome(t, home)
	for _, hostname := range []string{"dev", "flipped", "third-name"} {
		onHost(t, thisHostRaw, hostname)
		if role, err := initRole(t, home); err == nil {
			t.Fatalf("historical role %q adopted after rename to %s", role, hostname)
		}
	}
	if snapshotHome(t, home) != before {
		t.Fatal("historical aliases were rewritten during normal init")
	}
}

func TestMachineIdentity_aClonedVMWithANewMachineIDDoesNotInheritTheOriginal(t *testing.T) {
	id := onHost(t, thisHostRaw, "dev")
	original := writeDesignatedHome(t)
	nativeEnv(t, original)
	mustRole(t, original, "coordinator")
	original = writeTaskFixture(t, original, "t-aaaaaaaaaaaa", questionTask("t-aaaaaaaaaaaa", id, id, original))
	source, err := store.Open(original)
	if err != nil {
		t.Fatal(err)
	}
	cloneRoot := filepath.Join(t.TempDir(), "state")
	copyTree(t, source.Root, cloneRoot)
	onHost(t, otherHostRaw, "dev")
	cloned, err := store.Open(cloneRoot)
	if err != nil {
		t.Fatal(err)
	}
	if role, err := initRole(t, cloneRoot); err == nil && role != "developer" {
		t.Fatalf("clone inherited role %q", role)
	}
	if _, err := initRole(t, cloneRoot, "--role", "coordinator", "--reclaim"); err == nil {
		t.Fatal("clone reclaimed original coordinator")
	}
	task, err := cloned.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if err = cloned.CheckMachine(task); err == nil {
		t.Fatal("clone adopted original task despite different permanent machine identity")
	}
}

func TestMachineIdentity_aRestoredBackupFromAnotherMachineIsRefused(t *testing.T) {
	id := onHost(t, thisHostRaw, "dev")
	home := writeDesignatedHome(t)
	nativeEnv(t, home)
	mustRole(t, home, "coordinator")
	home = writeTaskFixture(t, home, "t-aaaaaaaaaaaa", questionTask("t-aaaaaaaaaaaa", id, id, home))
	home = writeTaskFixture(t, home, "t-bbbbbbbbbbbb", questionTask("t-bbbbbbbbbbbb", id, id, home))
	archive := filepath.Join(t.TempDir(), "records.tar.gz")
	if out, err := runCLI(t, home, "backup", archive); err != nil {
		t.Fatalf("backup: %v %s", err, out)
	}
	if manifest := readArchiveManifest(t, archive); manifest["machine"] != id || manifest["hostname"] != "dev" {
		t.Fatalf("manifest identity %v", manifest)
	}
	exported := filepath.Join(extractArchive(t, archive), "state")
	if _, err := os.Stat(filepath.Join(exported, "context.json")); !os.IsNotExist(err) {
		t.Fatal("records backup included coordinator ownership")
	}
	// Deliberately import only domain records into a fresh native generation on the other host.
	// This does not reinterpret any task endpoint as a local execution.
	onHost(t, otherHostRaw, "dev")
	restored := writeDesignatedHome(t)
	copyTree(t, filepath.Join(exported, "tasks"), filepath.Join(restored, "tasks"))
	nativeEnv(t, restored)
	mustRole(t, restored, "coordinator")
	for _, taskID := range []string{"t-aaaaaaaaaaaa", "t-bbbbbbbbbbbb"} {
		if out, err := runCLI(t, restored, "bind", taskID, "--parent-only"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "another machine") {
			t.Fatalf("bind of restored %s: %v %s", taskID, err, out)
		}
		s, err := store.Open(restored)
		if err != nil {
			t.Fatal(err)
		}
		task, err := s.ReadTask(taskID)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.CheckMachine(task); err == nil {
			t.Fatalf("restored %s resolved to another host", taskID)
		}
	}
}

func openArchive(t *testing.T, archive string) (*tar.Reader, func()) {
	t.Helper()
	handle, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(handle)
	if err != nil {
		t.Fatal(err)
	}
	return tar.NewReader(gz), func() { gz.Close(); handle.Close() }
}

func readArchiveManifest(t *testing.T, archive string) map[string]any {
	t.Helper()
	tr, done := openArchive(t, archive)
	defer done()
	for {
		header, err := tr.Next()
		if err != nil {
			t.Fatalf("manifest.json not found: %v", err)
		}
		if header.Name == "manifest.json" {
			var manifest map[string]any
			if err := json.NewDecoder(tr).Decode(&manifest); err != nil {
				t.Fatal(err)
			}
			return manifest
		}
	}
}

func extractArchive(t *testing.T, archive string) string {
	t.Helper()
	dir := t.TempDir()
	tr, done := openArchive(t, archive)
	defer done()
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return dir
		}
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, filepath.FromSlash(header.Name))
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0o700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMachineIdentity_aNativeWorkerStillCannotAnswerItsOwnQuestionAfterRename(t *testing.T) {
	id := onHost(t, thisHostRaw, "dev")
	home := writeDesignatedHome(t)
	nativeEnv(t, home)
	home = writeTaskFixture(t, home, "t-aaaaaaaaaaaa", questionTask("t-aaaaaaaaaaaa", id, id, home))
	useNativeTaskWorker(t, home, "t-aaaaaaaaaaaa")
	onHost(t, thisHostRaw, "renamed")
	out, err := runCLI(t, home, "answer", "t-aaaaaaaaaaaa", "q-aaaa", "--text", "approved")
	if err == nil || !strings.Contains(err.Error(), "required task role") {
		t.Fatalf("worker answered its own question: %v %s", err, out)
	}
	task := readJSON(t, filepath.Join(home, "tasks", "t-aaaaaaaaaaaa", "task.json"))
	if status := asString(asMap(asSlice(task["questions"])[0])["status"]); status != "open" {
		t.Fatal("refused self-answer changed question")
	}
}

func TestMachineIdentity_aNativeUncertainDeliveryIsNotReplayedAfterRename(t *testing.T) {
	id := onHost(t, thisHostRaw, "dev")
	home := writeDesignatedHome(t)
	nativeEnv(t, home)
	mustRole(t, home, "coordinator")
	home = writeTaskFixture(t, home, "t-aaaaaaaaaaaa", questionTask("t-aaaaaaaaaaaa", id, id, home))
	useNativeTaskWorker(t, home, "t-aaaaaaaaaaaa")
	lab := nativeLabFor(t, home)
	lab.native.QueueFault("terminal.send", hctest.Fault{LoseResponse: true})
	if _, err := runCLI(t, home, "notice", "t-aaaaaaaaaaaa", "--to", "parent"); err != nil {
		t.Fatal(err)
	}
	before := len(lab.native.Inputs("t1"))
	if before != 1 {
		t.Fatalf("lost-response send effects=%d", before)
	}
	onHost(t, thisHostRaw, "renamed")
	useNativeCoordinator(t, home)
	mustRole(t, home, "coordinator")
	if got := len(lab.native.Inputs("t1")); got != before {
		t.Fatalf("uncertain native send replayed after rename: %d->%d", before, got)
	}
}

func TestMachineIdentity_aNativeWorkerKeepsItsRecordedRoleAfterARename(t *testing.T) {
	id := onHost(t, thisHostRaw, "dev")
	home := writeDesignatedHome(t)
	nativeEnv(t, home)
	worktree := t.TempDir()
	task := strings.Replace(questionTask("t-aaaaaaaaaaaa", id, id, home), `"brief": "worker task"`, fmt.Sprintf(`"brief": "worker task", "worktree": %q`, worktree), 1)
	home = writeTaskFixture(t, home, "t-aaaaaaaaaaaa", task)
	useNativeTaskWorker(t, home, "t-aaaaaaaaaaaa")
	onHost(t, thisHostRaw, "renamed")
	if out, err := runCLI(t, home, "notes", "t-aaaaaaaaaaaa", "--text", "checkpoint"); err != nil {
		t.Fatalf("native worker notes after rename: %v %s", err, out)
	}
	notes, err := os.ReadFile(filepath.Join(home, "tasks", "t-aaaaaaaaaaaa", "notes.md"))
	if err != nil {
		t.Fatal(err)
	}
	record := readJSON(t, filepath.Join(home, "tasks", "t-aaaaaaaaaaaa", "task.json"))
	worker := asMap(record["worker_endpoint"])
	who := "worker " + asString(worker["core_id"]) + "/" + asString(worker["address"])
	if !strings.Contains(string(notes), who) {
		t.Fatalf("note lost worker attribution: %s", notes)
	}
	out, err := runCLI(t, home, "env", "discover", "t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("native worker discover after rename: %v %s", err, out)
	}
	if by := decodeObject(t, out)["by"]; by != "worker" {
		t.Fatalf("discovery role=%v", by)
	}
}

func attachNativeTaskCheckout(t *testing.T, home, taskID string) {
	t.Helper()
	s := nativeTestStore(t, home)
	task, err := s.ReadTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := task.Get("checkout_intent"); exists {
		return
	}
	repo := t.TempDir()
	head := initGitRepoWithCommit(t, repo)
	intent, err := taskcheckout.Plan(s.Root, repo, taskID, strings.TrimSpace(head))
	if err != nil {
		t.Fatal(err)
	}
	checkout, err := taskcheckout.Create(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	value, err := ordjson.FromValue(intent)
	if err != nil {
		t.Fatal(err)
	}
	task.Set("checkout_intent", value)
	task.Set("repository", intent.Repository)
	task.Set("worktree", checkout.Path)
	task.Set("branch", intent.Branch)
	task.Set("base_sha", intent.BaseSHA)
	task.Set("schema", json.Number("2"))
	if err = s.SaveTask(task); err != nil {
		t.Fatal(err)
	}
}
