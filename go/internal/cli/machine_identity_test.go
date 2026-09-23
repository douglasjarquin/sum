package cli

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// Herdr pane IDs are unique only within one Herdr server, so sum's machine
// identity must stay put when the host is renamed and differ between hosts.
// The coordinator pane in these labs is herdrEnv's w-parent:p1 in sum-test.

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
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	id := onHost(t, thisHostRaw, "dev")
	mustRole(t, home, "coordinator")

	onHost(t, thisHostRaw, "host-development.example")
	mustRole(t, home, "coordinator")
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", questionTask("t-aaaaaaaaaaaa", id, id, home))
	bindReturnsInline(t, home, "t-aaaaaaaaaaaa")
	if owner := readJSON(t, filepath.Join(home, "context.json")); owner["machine"] != id {
		t.Fatalf("coordinator machine = %v, want the stable identity %s", owner["machine"], id)
	}
}

func TestMachineIdentity_anotherMachinesCoordinatorIsStillRefused(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	onHost(t, thisHostRaw, "dev")
	mustRole(t, home, "coordinator")
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", questionTask("t-aaaaaaaaaaaa", "elsewhere", "elsewhere", home))

	// Same hostname, different machine: the pane ID w-parent:p1 names a pane on
	// another Herdr server.
	onHost(t, otherHostRaw, "dev")
	mustRole(t, home, "developer")
	if _, err := initRole(t, home, "--role", "coordinator", "--reclaim"); err == nil || !strings.Contains(err.Error(), "other-machine") {
		t.Fatalf("reclaim of another machine's coordinator = %v, want the other-machine refusal", err)
	}
	onHost(t, thisHostRaw, "dev")
	out, err := runCLI(t, home, "bind", "t-aaaaaaaaaaaa", "--parent-only")
	if err == nil || !strings.Contains(err.Error(), "Cross-machine") {
		t.Fatalf("bind of a task recorded on machine %q = %v %s, want the cross-machine refusal", "elsewhere", err, out)
	}
}

// legacyHome writes records the way the hostname-keyed release did: every
// machine value is a hostname and every session file is keyed by it.
func legacyHome(t *testing.T, hostname string) string {
	t.Helper()
	home := writeDesignatedHome(t)
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

func TestMachineIdentity_hostnameKeyedRecordsMigrateWithoutAManualStep(t *testing.T) {
	home := legacyHome(t, "dev")
	herdrEnv(t, home)
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", questionTask("t-aaaaaaaaaaaa", "dev", "dev", home))

	// The first run of this release, on the host that wrote the records.
	id := onHost(t, thisHostRaw, "dev")
	mustRole(t, home, "coordinator")
	if owner := readJSON(t, filepath.Join(home, "context.json")); owner["machine"] != id {
		t.Fatalf("coordinator machine = %v, want %s", owner["machine"], id)
	}
	if _, err := os.Stat(sessionPath(home, "dev", "w-parent:p1")); !os.IsNotExist(err) {
		t.Fatalf("hostname-keyed coordinator session still present: %v", err)
	}
	if registration := readJSON(t, sessionPath(home, id, "w-parent:p1")); registration["role"] != "coordinator" || registration["machine"] != id {
		t.Fatalf("re-keyed coordinator session = %v", registration)
	}

	// The host is renamed afterwards: nothing about roles or routing changes,
	// and the worker session still filed under the old hostname's key resolves.
	onHost(t, thisHostRaw, "host-development.example")
	mustRole(t, home, "coordinator")
	bindReturnsInline(t, home, "t-aaaaaaaaaaaa")
	s, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := s.Registration(store.Endpoint{Machine: id, Session: "sum-test", Pane: "w-worker:p1"})
	if err != nil || worker == nil {
		t.Fatalf("worker session keyed by the old hostname = %v, %v; want it resolved after the rename", worker, err)
	}
	if role, _ := worker.Get("role"); role != "worker" {
		t.Fatalf("worker session role = %v", role)
	}
}

func TestMachineIdentity_recordsUnderSeveralOldNamesResolveOnceThisHostCarriedThem(t *testing.T) {
	home := legacyHome(t, "dev")
	herdrEnv(t, home)
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", questionTask("t-aaaaaaaaaaaa", "dev", "dev", home))
	writeTaskFixture(t, home, "t-bbbbbbbbbbbb", questionTask("t-bbbbbbbbbbbb", "flipped", "flipped", home))

	onHost(t, thisHostRaw, "dev")
	mustRole(t, home, "coordinator")
	out, err := runCLI(t, home, "bind", "t-bbbbbbbbbbbb", "--parent-only")
	if err == nil || !strings.Contains(err.Error(), "Cross-machine") {
		t.Fatalf("a name this host has not been seen carrying resolved to it: %v %s", err, out)
	}

	// The hostname flips, as it did on 2026-09-10 and 2026-09-23.
	onHost(t, thisHostRaw, "flipped")
	mustRole(t, home, "coordinator")
	onHost(t, thisHostRaw, "third-name")
	mustRole(t, home, "coordinator")
	bindReturnsInline(t, home, "t-aaaaaaaaaaaa")
	bindReturnsInline(t, home, "t-bbbbbbbbbbbb")
}

func TestMachineIdentity_aClonedVMWithANewMachineIDDoesNotInheritTheOriginal(t *testing.T) {
	original := legacyHome(t, "dev")
	herdrEnv(t, original)
	writeTaskFixture(t, original, "t-aaaaaaaaaaaa", questionTask("t-aaaaaaaaaaaa", "dev", "dev", original))
	onHost(t, thisHostRaw, "dev")
	mustRole(t, original, "coordinator")

	// The clone copies the whole state directory, alias table included, keeps the
	// hostname, and regenerates /etc/machine-id.
	clone := filepath.Join(t.TempDir(), "state")
	copyTree(t, original, clone)
	herdrEnv(t, clone)
	onHost(t, otherHostRaw, "dev")
	mustRole(t, clone, "developer")
	if _, err := initRole(t, clone, "--role", "coordinator", "--reclaim"); err == nil || !strings.Contains(err.Error(), "other-machine") {
		t.Fatalf("clone reclaim = %v, want the other-machine refusal", err)
	}
	s, err := store.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckMachine(task); err == nil {
		t.Fatal("the clone adopted a task the original recorded under its hostname")
	}
}

func TestMachineIdentity_aRestoredBackupFromAnotherMachineIsRefused(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	id := onHost(t, thisHostRaw, "dev")
	mustRole(t, home, "coordinator")
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", questionTask("t-aaaaaaaaaaaa", id, id, home))
	// A record the hostname-keyed release wrote while this host had another name.
	writeTaskFixture(t, home, "t-bbbbbbbbbbbb", questionTask("t-bbbbbbbbbbbb", "old-name", "old-name", home))
	archive := filepath.Join(t.TempDir(), "records.tar.gz")
	if out, err := runCLI(t, home, "backup", archive); err != nil {
		t.Fatalf("backup: %v\n%s", err, out)
	}
	if manifest := readArchiveManifest(t, archive); manifest["machine"] != id || manifest["hostname"] != "dev" {
		t.Fatalf("manifest machine/hostname = %v/%v, want %s/dev", manifest["machine"], manifest["hostname"], id)
	}

	// Restored on a different host that shares the hostname. A records backup
	// carries no coordinator claim, so the restoring pane may claim a fresh one;
	// the tasks and their pane bindings stay the other machine's until they are
	// recovered and bound explicitly.
	restored := filepath.Join(extractArchive(t, archive), "state")
	herdrEnv(t, restored)
	onHost(t, otherHostRaw, "dev")
	mustRole(t, restored, "coordinator")
	for _, taskID := range []string{"t-aaaaaaaaaaaa", "t-bbbbbbbbbbbb"} {
		out, err := runCLI(t, restored, "bind", taskID, "--parent-only")
		if err == nil || !strings.Contains(err.Error(), "Cross-machine") {
			t.Fatalf("bind of restored %s = %v %s, want the cross-machine refusal", taskID, err, out)
		}
	}
	s, err := store.Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	for _, taskID := range []string{"t-aaaaaaaaaaaa", "t-bbbbbbbbbbbb"} {
		task, err := s.ReadTask(taskID)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.CheckMachine(task); err == nil {
			t.Fatalf("restored %s resolved to this machine", taskID)
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

func TestMachineIdentity_aLegacyWorkerPaneStillCannotAnswerItsOwnQuestion(t *testing.T) {
	home := legacyHome(t, "dev")
	herdrEnv(t, home)
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", questionTask("t-aaaaaaaaaaaa", "dev", "dev", home))
	onHost(t, thisHostRaw, "dev")
	mustRole(t, home, "coordinator")
	onHost(t, thisHostRaw, "renamed")

	t.Setenv("HERDR_PANE_ID", "w-worker:p1")
	out, err := runCLI(t, home, "answer", "t-aaaaaaaaaaaa", "q-aaaa", "--text", "approved")
	if err == nil || !strings.Contains(err.Error(), "worker pane cannot record") {
		t.Fatalf("answer from the task's own worker pane = %v %s, want the worker refusal", err, out)
	}
}

func TestMachineIdentity_aDeliveryRecordedUnderTheHostnameKeyStillCounts(t *testing.T) {
	home := legacyHome(t, "dev")
	herdrEnv(t, home)
	id := onHost(t, thisHostRaw, "dev")
	// t-aaaa, routed under the stable identity, sorts first and shares the
	// coordinator's bucket with the legacy t-bbbb.
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", questionTask("t-aaaaaaaaaaaa", id, id, home))
	writeTaskFixture(t, home, "t-bbbbbbbbbbbb", questionTask("t-bbbbbbbbbbbb", "dev", "dev", home))
	legacyKey := store.RegistrationKey(store.Endpoint{Machine: "dev", Session: "sum-test", Pane: "w-parent:p1"})
	interrupted := fmt.Sprintf(`{"schema": 1, "task": "t-bbbbbbbbbbbb", "deliveries": [{"id": "d-0000000001", "at": "2026-01-01T00:00:00+00:00",
 "recipient": {"recipient": "parent", "role": "coordinator", "machine": "dev", "session": "sum-test", "pane": "w-parent:p1", "key": %q},
 "state": "in-flight", "runtime": {"sum_version": "0.1.0", "sha": null}, "obligations": ["question:q-bbbb"]}]}`, legacyKey)
	returnsPath := filepath.Join(home, "tasks", "t-bbbbbbbbbbbb", "returns.json")
	if err := os.WriteFile(returnsPath, []byte(interrupted), 0o600); err != nil {
		t.Fatal(err)
	}

	mustRole(t, home, "coordinator")
	deliveries, _ := readJSON(t, returnsPath)["deliveries"].([]any)
	if len(deliveries) != 1 {
		t.Fatalf("an interrupted delivery recorded under the hostname key was sent again: %d deliveries", len(deliveries))
	}
}
