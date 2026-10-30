package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestoreRefusesUnsafeArchiveBeforeCreatingDestination(t *testing.T) {
	for _, tc := range []struct {
		name     string
		headers  []tar.Header
		contents [][]byte
	}{
		{"absolute", []tar.Header{{Name: "/absolute", Typeflag: tar.TypeReg}}, [][]byte{nil}},
		{"hardlink", []tar.Header{{Name: "state/tasks/link", Typeflag: tar.TypeLink, Linkname: "state/state.json"}}, [][]byte{nil}},
		{"traversal", []tar.Header{{Name: "state/../../escape", Size: 1, Typeflag: tar.TypeReg}}, [][]byte{[]byte("x")}},
		{"symlink", []tar.Header{{Name: "state/tasks/link", Typeflag: tar.TypeSymlink, Linkname: "/tmp/elsewhere"}}, [][]byte{nil}},
		{"duplicates", []tar.Header{{Name: "manifest.json", Size: 2, Typeflag: tar.TypeReg}, {Name: "manifest.json", Size: 2, Typeflag: tar.TypeReg}}, [][]byte{[]byte("{}"), []byte("{}")}},
		{"unknown schema", []tar.Header{{Name: "manifest.json", Size: 36, Typeflag: tar.TypeReg}}, [][]byte{[]byte(`{"schema":99,"scope":"records-only"}`)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			archive := filepath.Join(root, "invalid.tar.gz")
			file, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			gz := gzip.NewWriter(file)
			writer := tar.NewWriter(gz)
			for i, h := range tc.headers {
				h.Size = int64(len(tc.contents[i]))
				if err = writer.WriteHeader(&h); err != nil {
					t.Fatal(err)
				}
				if _, err = writer.Write(tc.contents[i]); err != nil {
					t.Fatal(err)
				}
			}
			if err = writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err = gz.Close(); err != nil {
				t.Fatal(err)
			}
			if err = file.Close(); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(root, "restored")
			if _, err = Restore(archive, destination); err == nil {
				t.Fatal("invalid archive accepted")
			}
			if _, err = os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("invalid archive created destination")
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
	root := t.TempDir()
	archive := filepath.Join(root, "bad.tar.gz")
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	writer := tar.NewWriter(gz)
	manifest := []byte(`{"schema":2,"scope":"records-only"}`)
	if err := writer.WriteHeader(&tar.Header{Name: "manifest.json", Size: int64(len(manifest)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	writer.Write(manifest)
	writer.Close()
	gz.Close()
	raw := compressed.Bytes()
	raw[len(raw)-8] ^= 0xff
	if err := os.WriteFile(archive, raw, 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "restored")
	if _, err := Restore(archive, target); err == nil {
		t.Fatal("corrupt gzip accepted")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
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
