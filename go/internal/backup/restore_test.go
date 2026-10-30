package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"

	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type restoreEntry struct {
	header tar.Header
	data   []byte
}

func validRestoreEntries(t *testing.T) []restoreEntry {
	t.Helper()
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
	if err = source.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "valid-records.tar.gz")
	if _, err = Run(source, archive); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	compressed, err := gzip.NewReader(input)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	var entries []restoreEntry
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, restoreEntry{header: *header, data: data})
	}
	return entries
}
func writeRestoreEntries(t *testing.T, entries []restoreEntry) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "records.tar.gz")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	writer := tar.NewWriter(compressed)
	for _, entry := range entries {
		header := entry.header
		header.Size = int64(len(entry.data))
		if err = writer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if _, err = writer.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err = compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	return filename
}
func changeRestoreJSON(t *testing.T, entries []restoreEntry, name, key string, value any) []restoreEntry {
	t.Helper()
	for i := range entries {
		if entries[i].header.Name != name {
			continue
		}
		var object map[string]any
		if err := json.Unmarshal(entries[i].data, &object); err != nil {
			t.Fatal(err)
		}
		object[key] = value
		data, err := json.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		entries[i].data = data
		return entries
	}
	t.Fatalf("valid archive lacks %s", name)
	return nil
}
func TestRestoreRefusesUnsafeArchiveBeforeCreatingDestination(t *testing.T) {
	baseline := validRestoreEntries(t)
	t.Run("valid control", func(t *testing.T) {
		archive := writeRestoreEntries(t, baseline)
		target := filepath.Join(t.TempDir(), "restored")
		if _, err := Restore(archive, target); err != nil {
			t.Fatalf("otherwise-valid control failed: %v", err)
		}
		restored, err := store.Open(target)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = restored.ReadTask("t-0123456789ab"); err != nil {
			t.Fatal(err)
		}
		if !restored.RecordsOnly() || restored.Designated() {
			t.Fatal("control did not restore as historical records")
		}
		if err = restored.CheckWritable(); !errors.Is(err, store.ErrHistoricalReadOnly) {
			t.Fatalf("control adopted live authority: %v", err)
		}
	})
	for _, tc := range []struct {
		name  string
		fault func(*testing.T, []restoreEntry) []restoreEntry
		want  string
	}{
		{"absolute", func(t *testing.T, e []restoreEntry) []restoreEntry {
			return append(e, restoreEntry{header: tar.Header{Name: filepath.Join(t.TempDir(), "absolute"), Typeflag: tar.TypeReg}})
		}, "unsafe records archive path"},
		{"traversal", func(t *testing.T, e []restoreEntry) []restoreEntry {
			return append(e, restoreEntry{header: tar.Header{Name: "state/../../escape", Typeflag: tar.TypeReg}, data: []byte("x")})
		}, "unsafe records archive path"},
		{"backslash", func(t *testing.T, e []restoreEntry) []restoreEntry {
			return append(e, restoreEntry{header: tar.Header{Name: `state\tasks\escape`, Typeflag: tar.TypeReg}})
		}, "unsafe records archive path"},
		{"symlink", func(t *testing.T, e []restoreEntry) []restoreEntry {
			return append(e, restoreEntry{header: tar.Header{Name: "state/tasks/link", Typeflag: tar.TypeSymlink, Linkname: "state.json"}})
		}, "non-regular archive entry"},
		{"hardlink", func(t *testing.T, e []restoreEntry) []restoreEntry {
			return append(e, restoreEntry{header: tar.Header{Name: "state/tasks/link", Typeflag: tar.TypeLink, Linkname: "state/state.json"}})
		}, "non-regular archive entry"},
		{"duplicates", func(t *testing.T, e []restoreEntry) []restoreEntry { return append(e, e[0]) }, "duplicate records archive entry"},
		{"scope", func(t *testing.T, e []restoreEntry) []restoreEntry {
			return changeRestoreJSON(t, e, "manifest.json", "scope", "live-installation")
		}, "restore requires a records-only backup"},
		{"manifest schema", func(t *testing.T, e []restoreEntry) []restoreEntry {
			return changeRestoreJSON(t, e, "manifest.json", "schema", 99)
		}, "unsupported records backup schema"},
		{"source schema", func(t *testing.T, e []restoreEntry) []restoreEntry {
			return changeRestoreJSON(t, e, "state/state.json", "schema", 99)
		}, "unsupported source state schema"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := tc.fault(t, append([]restoreEntry(nil), baseline...))
			archive := writeRestoreEntries(t, entries)
			destination := filepath.Join(t.TempDir(), "restored")
			if _, err := Restore(archive, destination); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q for this single archive fault, got %v", tc.want, err)
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("invalid archive selected destination")
			}
		})
	}
}

func TestRestoreRefusesOversizedEntryBeforeReadingBody(t *testing.T) {
	var raw bytes.Buffer
	writer := tar.NewWriter(&raw)
	if err := writer.WriteHeader(&tar.Header{Name: "state/oversized.json", Size: MaxRestoreEntryBytes + 1, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readArchive(bytes.NewReader(raw.Bytes())); err == nil || !strings.Contains(err.Error(), "import bound") {
		t.Fatalf("oversized header did not fail before body read: %v", err)
	}
}
func TestRestoreRefusesCorruptGzipBeforeCreatingDestination(t *testing.T) {
	archive := writeRestoreEntries(t, validRestoreEntries(t))
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-8] ^= 0xff
	if err = os.WriteFile(archive, raw, 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, err = Restore(archive, target); !errors.Is(err, gzip.ErrChecksum) {
		t.Fatalf("expected gzip checksum failure, got %v", err)
	}
	if _, err = os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("corrupt gzip selected destination")
	}
}

type zeroArchiveReader struct{}

func (zeroArchiveReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func TestRestoreBoundsExpandedDataAfterTarEnd(t *testing.T) {
	if _, _, err := readArchive(zeroArchiveReader{}); err == nil || !strings.Contains(err.Error(), "expanded import bound") {
		t.Fatalf("unbounded trailing expansion: %v", err)
	}
}
