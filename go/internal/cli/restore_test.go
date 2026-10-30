package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestBackupArchiveRestoresRecordsAtAnotherRootWithoutAuthority(t *testing.T) {
	for _, otherMachine := range []bool{false, true} {
		t.Run(map[bool]string{false: "same machine", true: "other machine"}[otherMachine], func(t *testing.T) {
			undo := machine.Pin("0123456789abcdef0123456789abcdef", "origin")
			defer undo()
			source, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = source.Init(); err != nil {
				t.Fatal(err)
			}
			task := ordjson.NewObject()
			task.Set("schema", json.Number("2"))
			task.Set("id", "t-0123456789ab")
			task.Set("status", "archived")
			task.Set("evidence", []any{})
			task.Set("questions", []any{})
			host, err := source.Machine()
			if err != nil {
				t.Fatal(err)
			}
			task.Set("machine", host.ID)
			endpoint := store.Endpoint{Schema: 2, Backend: "hc", Machine: host.ID, CoreID: "old-core", Session: "scratch", Socket: "/tmp/never-connect-old.sock", Address: "t7", Task: "t-0123456789ab", Role: "worker", Attempt: "old-attempt", BindingGeneration: 1, CreatedMS: 100, Cwd: "/saved/checkout"}
			task.Set("endpoint", endpoint.Object())
			task.Set("policy", ordjson.NewObject())
			if err = source.SaveTask(task); err != nil {
				t.Fatal(err)
			}
			if _, err = source.Register(endpoint, "worker", endpoint.Task, nil); err != nil {
				t.Fatal(err)
			}
			archivedTask, err := os.ReadFile(filepath.Join(source.Tasks, "t-0123456789ab", "task.json"))
			if err != nil {
				t.Fatal(err)
			}
			sealed := []byte("# Pinned worker procedure\nKeep these exact bytes, including the original helper reference /old/bin/sumctl.\n")
			sealedHash := fmt.Sprintf("%x", sha256.Sum256(sealed))
			sealedRelative := "procedure/sum-work-" + sealedHash + ".md"
			taskDir := filepath.Join(source.Tasks, "t-0123456789ab")
			if err = os.MkdirAll(filepath.Join(taskDir, "procedure"), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(taskDir, filepath.FromSlash(sealedRelative)), sealed, 0600); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(taskDir, "brief-r1.md"), []byte("sealed brief\n"), 0600); err != nil {
				t.Fatal(err)
			}
			version := fmt.Sprintf(`{"schema":1,"task":"t-0123456789ab","active":"r1","revisions":[{"id":"r1","path":"brief-r1.md","policy":{"procedure":[{"name":"sum-work","path":%q,"sha256":%q}]}}],"refresh":[]}`, sealedRelative, sealedHash)
			if err = os.WriteFile(filepath.Join(taskDir, "versions.json"), []byte(version), 0600); err != nil {
				t.Fatal(err)
			}
			oldInstance, _ := source.Instance()
			archive := filepath.Join(t.TempDir(), "records.tar.gz")
			if out, err := runCLI(t, source.Root, "backup", archive); err != nil {
				t.Fatalf("backup %s: %v", out, err)
			}
			files := restoreArchiveFiles(t, archive)
			if !bytes.Equal(files["state/tasks/t-0123456789ab/task.json"], archivedTask) {
				t.Fatal("backup changed archived task")
			}
			extracted := t.TempDir()
			for name, data := range files {
				full := filepath.Join(extracted, filepath.FromSlash(name))
				if err = os.MkdirAll(filepath.Dir(full), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(full, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = store.Open(filepath.Join(extracted, "state")); err == nil {
				t.Fatal("raw relocated archive granted live authority")
			}
			if otherMachine {
				revert := machine.Pin("fedcba9876543210fedcba9876543210", "destination")
				defer revert()
			}
			clearNativeEnv(t)
			target := filepath.Join(t.TempDir(), "restored-home")
			out, err := runCLI(t, source.Root, "restore", archive, target)
			if err != nil {
				t.Fatalf("restore: %v %s", err, out)
			}
			response := decodeObject(t, out)
			archiveBytes, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}
			if response["sha256"] != fmt.Sprintf("%x", sha256.Sum256(archiveBytes)) {
				t.Fatal("restore receipt digest does not describe the imported archive")
			}
			restored, err := store.Open(target)
			if err != nil {
				t.Fatal(err)
			}
			pointerValue, err := ordjson.ReadFile(filepath.Join(restored.Root, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			if backend, _ := pointerValue.(*ordjson.Object).Get("backend"); backend != "records" {
				t.Fatal("restore backend could be opened by an HC-only helper")
			}
			if restored.Designated() {
				t.Fatal("restore was not quarantined")
			}
			freshInstance, _ := restored.Instance()
			if freshInstance == "" || freshInstance == oldInstance {
				t.Fatal("restore inherited installation identity")
			}
			saved, err := os.ReadFile(filepath.Join(restored.Tasks, "t-0123456789ab", "task.json"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(saved, archivedTask) {
				t.Fatal("restore rewrote task or sealed policy bytes")
			}
			for name, want := range files {
				if strings.HasPrefix(name, "state/") {
					got, err := os.ReadFile(filepath.Join(restored.Root, "restore-source", filepath.FromSlash(name)))
					if err != nil || !bytes.Equal(got, want) {
						t.Fatalf("source bytes changed %s: %v", name, err)
					}
				}
			}
			restoredProcedure, err := os.ReadFile(filepath.Join(restored.Tasks, "t-0123456789ab", filepath.FromSlash(sealedRelative)))
			if err != nil || !bytes.Equal(restoredProcedure, sealed) {
				t.Fatalf("sealed procedure changed: %v", err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(restoredProcedure)); got != sealedHash {
				t.Fatal("sealed procedure hash changed")
			}
			if owner, err := restored.Owner(); err != nil || owner != nil {
				t.Fatalf("restored active owner: %v %v", owner, err)
			}
			if registrations, err := restored.Registrations(); err != nil || len(registrations) != 0 {
				t.Fatalf("restored active sessions: %v %v", registrations, err)
			}
			if err = restored.CheckWritable(); !errors.Is(err, store.ErrHistoricalReadOnly) {
				t.Fatalf("restored authority writable: %v", err)
			}
			if _, err = runCLI(t, target, "show", "t-0123456789ab"); err != nil {
				t.Fatalf("restored CLI read: %v", err)
			}
			for _, args := range [][]string{{"status"}, {"context", "t-0123456789ab", "--role", "coordinator"}} {
				if output, readErr := runCLI(t, target, args...); readErr != nil {
					t.Fatalf("restored %v read failed: %v %s", args, readErr, output)
				}
			}
			revisionOutput, readErr := runCLI(t, target, "context", "t-0123456789ab", "--role", "coordinator", "--section", "brief", "--revision", "r1")
			if readErr != nil {
				t.Fatalf("restored revision context failed: %v", readErr)
			}
			if !strings.Contains(revisionOutput, "sealed brief") || !strings.Contains(revisionOutput, filepath.Join(restored.Tasks, "t-0123456789ab", "brief-r1.md")) {
				t.Fatalf("revision did not read the imported relative file: %s", revisionOutput)
			}
			if err = restored.CheckWritable(); !errors.Is(err, store.ErrHistoricalReadOnly) {
				t.Fatalf("saved reads adopted restored authority: %v", err)
			}
			if _, err = runCLI(t, target, "coordinator", "start", "--socket", "/tmp/never-connect-old.sock", "--session", "scratch", "--hc-config-home", t.TempDir()); !errors.Is(err, store.ErrHistoricalReadOnly) {
				t.Fatal("restored records allowed a native launch")
			}
			if _, err = runCLI(t, target, "settings", "set", "--global", "3"); !errors.Is(err, store.ErrHistoricalReadOnly) {
				t.Fatal("restored records allowed command mutation")
			}
			if _, err = runCLI(t, target, "init", "--role", "coordinator"); err == nil {
				t.Fatal("restored records claimed coordinator")
			}
			if _, err = runCLI(t, source.Root, "restore", archive, target); err == nil {
				t.Fatal("restore overwrote existing destination")
			}
		})
	}
}
func restoreArchiveFiles(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		h, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = b
	}
	return out
}
