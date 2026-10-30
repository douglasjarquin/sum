package backup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	MaxRestoreBytes      int64 = 512 << 20
	MaxRestoreEntryBytes int64 = 64 << 20
	MaxRestoreEntries          = 100000
)

func Restore(archive, destination string) (*ordjson.Object, error) {
	archive, err := filepath.Abs(archive)
	if err != nil {
		return nil, err
	}
	input, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	digestHash := sha256.New()
	compressed, err := gzip.NewReader(io.TeeReader(input, digestHash))
	if err != nil {
		return nil, err
	}
	defer compressed.Close()
	entries, manifest, err := readArchive(compressed)
	if err != nil {
		return nil, err
	}
	if scope, _ := manifest.Get("scope"); scope != "records-only" {
		return nil, fmt.Errorf("restore requires a records-only backup")
	}
	schema, _ := manifest.Get("schema")
	if schema != json.Number(fmt.Sprint(store.Schema)) {
		return nil, fmt.Errorf("unsupported records backup schema")
	}
	sourceState, ok := entries["state/state.json"]
	if !ok {
		return nil, fmt.Errorf("records backup lacks source state.json")
	}
	value, err := ordjson.Decode(sourceState)
	if err != nil {
		return nil, err
	}
	state, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("source state.json is not an object")
	}
	if schema, _ := state.Get("schema"); schema != json.Number(fmt.Sprint(store.Schema)) {
		return nil, fmt.Errorf("unsupported source state schema")
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return nil, err
	}
	if err = os.Mkdir(destination, 0700); err != nil {
		return nil, fmt.Errorf("restore requires a new destination: %w", err)
	}
	s, err := store.Open(destination)
	if err != nil {
		return nil, err
	}
	if err = s.InitRecordsOnly(); err != nil {
		return nil, err
	}
	for name, data := range entries {
		if err = writeRestored(filepath.Join(s.Root, "restore-source", filepath.FromSlash(name)), data); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(name, "state/") {
			continue
		}
		relative := strings.TrimPrefix(name, "state/")
		if !domainRecord(relative) {
			continue
		}
		if err = writeRestored(filepath.Join(s.Home, filepath.FromSlash(relative)), data); err != nil {
			return nil, err
		}
	}
	if _, err = s.AllTasks(); err != nil {
		return nil, fmt.Errorf("restored task validation: %w", err)
	}
	digest := hex.EncodeToString(digestHash.Sum(nil))
	evidence := ordjson.NewObject()
	evidence.Set("schema", json.Number("1"))
	evidence.Set("archive", archive)
	evidence.Set("sha256", digest)
	evidence.Set("records_only", true)
	evidence.Set("created_at", store.Now())
	evidence.Set("generation", s.Generation)
	evidence.Set("source_manifest", manifest)
	if err = ordjson.WriteFile(filepath.Join(s.Home, "restore.json"), evidence); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("home", s.Root)
	result.Set("generation", s.Generation)
	result.Set("records_only", true)
	result.Set("sha256", digest)
	result.Set("note", "Historical records are readable. No live role, endpoint, receipt or execution authority was adopted; this home cannot launch, bind, claim or send.")
	return result, nil
}
func domainRecord(relative string) bool {
	first := strings.Split(relative, "/")[0]
	switch first {
	case "tasks", "coordinator", "findings":
		return true
	case "settings.json", "projects.json", "preferences.md", "projects.md":
		return relative == first
	}
	return false
}
func readArchive(reader io.Reader) (map[string][]byte, *ordjson.Object, error) {
	entries := map[string][]byte{}
	bounded := &io.LimitedReader{R: reader, N: MaxRestoreBytes + 1}
	tarReader := tar.NewReader(bounded)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		if header.Size < 0 || header.Size > MaxRestoreEntryBytes {
			return nil, nil, fmt.Errorf("records archive entry exceeds the %d-byte import bound", MaxRestoreEntryBytes)
		}
		if len(entries) >= MaxRestoreEntries {
			return nil, nil, fmt.Errorf("records archive exceeds the %d-entry import bound", MaxRestoreEntries)
		}
		name := header.Name
		if path.IsAbs(name) || path.Clean(name) != name || strings.Contains(name, "\\") || name == ".." || strings.HasPrefix(name, "../") || (name != "manifest.json" && !strings.HasPrefix(name, "state/")) {
			return nil, nil, fmt.Errorf("unsafe records archive path %q", name)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return nil, nil, fmt.Errorf("records restore refuses non-regular archive entry %q", name)
		}
		if _, exists := entries[name]; exists {
			return nil, nil, fmt.Errorf("duplicate records archive entry %q", name)
		}
		data, err := io.ReadAll(tarReader)
		if err != nil {
			return nil, nil, err
		}
		entries[name] = data
	}
	if _, err := io.Copy(io.Discard, bounded); err != nil {
		return nil, nil, err
	}
	if bounded.N <= 0 {
		return nil, nil, fmt.Errorf("records archive exceeds the %d-byte expanded import bound", MaxRestoreBytes)
	}
	data, exists := entries["manifest.json"]
	if !exists {
		return nil, nil, fmt.Errorf("records backup lacks manifest.json")
	}
	value, err := ordjson.Decode(data)
	if err != nil {
		return nil, nil, err
	}
	manifest, ok := value.(*ordjson.Object)
	if !ok {
		return nil, nil, fmt.Errorf("backup manifest is not an object")
	}
	return entries, manifest, nil
}
func writeRestored(name string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if writeErr != nil {
		file.Close()
		return writeErr
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
