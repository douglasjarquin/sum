// Package migration stages an HC ledger generation without rewriting the
// original ledger or its sealed evidence. It never discovers or stops runtime
// processes: activation must supply and retain a proven exclusion lease.
package migration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// ExclusionLease is held across both snapshot checks and authority publication.
// Its implementation must exclude every legacy writer, callback, MCP sender and
// in-flight/lock-waiting operation, including processes with no open file today.
// A process census, removed configuration or a boolean operator assertion alone
// is insufficient. This package has no permissive default implementation.
type ExclusionLease interface {
	Verify(context.Context, string, string) (Evidence, error)
	Close() error
}
type Evidence struct {
	Mechanism    string `json:"mechanism"`
	Reference    string `json:"reference"`
	SourceDigest string `json:"source_digest"`
}
type Options struct {
	Home                 string
	Exclusion            ExclusionLease
	ExpectedSourceDigest string
}
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Mode   uint32 `json:"mode"`
}
type Journal struct {
	Schema       int        `json:"schema"`
	SourceSchema int        `json:"source_schema"`
	TargetSchema int        `json:"target_schema"`
	Generation   string     `json:"generation"`
	SourceDigest string     `json:"source_digest"`
	Export       string     `json:"export"`
	Phase        string     `json:"phase"`
	Files        []File     `json:"files"`
	Exclusion    []Evidence `json:"exclusion"`
	CreatedAt    string     `json:"created_at"`
	PublishedAt  string     `json:"published_at,omitempty"`
}

// Migrate copies the historical ledger byte-for-byte into a physically isolated
// generation. Only its state header changes; all old endpoints remain historical
// and acquire no native HC authority. Active work needs a separate handover.
func Migrate(ctx context.Context, opts Options) (result Journal, err error) {
	if opts.Exclusion == nil {
		return result, fmt.Errorf("migration requires a held, verifiable legacy writer and sender exclusion lease")
	}
	defer func() { err = errors.Join(err, opts.Exclusion.Close()) }()
	root, err := filepath.Abs(opts.Home)
	if err != nil {
		return result, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return result, err
	}
	migrationUnlock, err := lock(ctx, filepath.Join(root, ".migration.lock"))
	if err != nil {
		return result, err
	}
	defer migrationUnlock()
	state, err := readObject(filepath.Join(root, "state.json"))
	if err != nil {
		return result, err
	}
	if integer(state, "schema") == 2 {
		result, err = readJournal(root)
		if err != nil {
			return result, err
		}
		generation, _ := state.Get("generation")
		if result.Generation != generation || !validGeneration(result.Generation) || result.Export != filepath.Join(root, "exports", result.SourceDigest) || fileDigest(result.Files) != result.SourceDigest || (result.Phase != "published" && result.Phase != "publishing") {
			return result, fmt.Errorf("schema2 state and migration journal disagree")
		}
		if result.Phase == "publishing" {
			target, readErr := readObject(filepath.Join(root, "generations", result.Generation, "state.json"))
			if readErr != nil {
				return result, readErr
			}
			if integer(target, "schema") != 2 {
				return result, fmt.Errorf("published target schema mismatch")
			}
			if err = validateFiles(result.Export, result.Files); err != nil {
				return result, err
			}
			if err = validateFiles(filepath.Join(root, "generations", result.Generation), withoutHeader(result.Files)); err != nil {
				return result, err
			}
			result.Phase = "published"
			result.PublishedAt = store.Now()
			if err = writeJournal(root, result); err != nil {
				return result, err
			}
		}
		return result, nil
	}
	if integer(state, "schema") != 1 {
		return result, fmt.Errorf("only explicit schema1 to schema2 migration is supported")
	}
	// Preserve the legacy lock inodes. The exclusion lease must additionally
	// exclude helpers that had not yet acquired these locks.
	deliveryUnlock, err := lock(ctx, filepath.Join(root, ".deliver.lock"))
	if err != nil {
		return result, err
	}
	defer deliveryUnlock()
	stateUnlock, err := lock(ctx, filepath.Join(root, ".lock"))
	if err != nil {
		return result, err
	}
	defer stateUnlock()
	files, digest, err := inventory(root)
	if err != nil {
		return result, err
	}
	if opts.ExpectedSourceDigest != "" && opts.ExpectedSourceDigest != digest {
		return result, fmt.Errorf("source export digest differs from reviewed migration")
	}
	proof, err := verify(ctx, opts.Exclusion, root, digest)
	if err != nil {
		return result, err
	}
	result, err = readJournal(root)
	if errors.Is(err, os.ErrNotExist) {
		bytes := make([]byte, 12)
		if _, err = rand.Read(bytes); err != nil {
			return result, err
		}
		result = Journal{Schema: 1, SourceSchema: 1, TargetSchema: 2, Generation: "g-" + hex.EncodeToString(bytes), SourceDigest: digest, Export: filepath.Join(root, "exports", digest), Phase: "prepared", Files: files, CreatedAt: store.Now()}
		err = writeJournal(root, result)
	}
	if err != nil {
		return result, err
	}
	if result.Schema != 1 || result.SourceDigest != digest || !validGeneration(result.Generation) || result.Export != filepath.Join(root, "exports", digest) || fileDigest(result.Files) != digest {
		return result, fmt.Errorf("original generation changed since migration began; preserve this journal and export for explicit recovery")
	}
	result.Exclusion = append(result.Exclusion, proof)
	if err = privateDir(root, result.Export); err != nil {
		return result, err
	}
	if err = copyFiles(root, result.Export, files); err != nil {
		return result, err
	}
	if err = validateFiles(result.Export, files); err != nil {
		return result, err
	}
	if err = validateRecords(result.Export); err != nil {
		return result, err
	}
	result.Phase = "exported"
	if err = writeJournal(root, result); err != nil {
		return result, err
	}
	target := filepath.Join(root, "generations", result.Generation)
	if err = privateDir(root, target); err != nil {
		return result, err
	}
	if err = copyFiles(result.Export, target, withoutHeader(files)); err != nil {
		return result, err
	}
	if err = validateFiles(target, withoutHeader(files)); err != nil {
		return result, err
	}
	header, err := readObject(filepath.Join(result.Export, "state.json"))
	if err != nil {
		return result, err
	}
	header.Set("schema", json.Number("2"))
	header.Set("backend", "hc")
	header.Set("generation", result.Generation)
	header.Set("root", root)
	if err = ordjson.WriteFile(filepath.Join(target, "state.json"), header); err != nil {
		return result, err
	}
	if err = os.MkdirAll(filepath.Join(target, "sessions"), 0700); err != nil {
		return result, err
	}
	result.Phase = "staged"
	if err = writeJournal(root, result); err != nil {
		return result, err
	}
	_, afterDigest, err := inventory(root)
	if err != nil {
		return result, err
	}
	if afterDigest != digest {
		return result, fmt.Errorf("source changed while staging; authority remains with the original generation")
	}
	proof, err = verify(ctx, opts.Exclusion, root, digest)
	if err != nil {
		return result, err
	}
	result.Exclusion = append(result.Exclusion, proof)
	current, err := readObject(filepath.Join(root, "state.json"))
	if err != nil {
		return result, err
	}
	if integer(current, "schema") != 1 {
		return result, fmt.Errorf("source authority changed during migration")
	}
	evidence := ordjson.NewObject()
	evidence.Set("schema", json.Number("1"))
	evidence.Set("source_digest", digest)
	evidence.Set("export", result.Export)
	evidence.Set("generation", result.Generation)
	evidence.Set("legacy_bindings", "historical-only; explicit native handover required")
	evidence.Set("exclusion", proofObjects(result.Exclusion))
	if err = ordjson.WriteFile(filepath.Join(target, "migration.json"), evidence); err != nil {
		return result, err
	}
	// Record publication intent before the single atomic authority switch. A crash
	// afterward is recoverable from the root pointer and this exact target.
	result.Phase = "publishing"
	if err = writeJournal(root, result); err != nil {
		return result, err
	}
	pointer := ordjson.NewObject()
	pointer.Set("schema", json.Number("2"))
	pointer.Set("backend", "hc")
	pointer.Set("generation", result.Generation)
	pointer.Set("migration_digest", digest)
	if err = ordjson.WriteFile(filepath.Join(root, "state.json"), pointer); err != nil {
		return result, err
	}
	result.Phase = "published"
	result.PublishedAt = store.Now()
	err = writeJournal(root, result)
	return result, err
}
func verify(ctx context.Context, lease ExclusionLease, root, digest string) (Evidence, error) {
	if err := ctx.Err(); err != nil {
		return Evidence{}, err
	}
	e, err := lease.Verify(ctx, root, digest)
	if err != nil {
		return e, err
	}
	if e.Mechanism == "" || e.Reference == "" || e.SourceDigest != digest {
		return e, fmt.Errorf("exclusion lease supplied incomplete or stale evidence")
	}
	return e, nil
}
func lock(ctx context.Context, path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
		}
		if err != syscall.EWOULDBLOCK {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func validGeneration(s string) bool {
	if !strings.HasPrefix(s, "g-") || len(s) != 26 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s[2:])
	return err == nil
}
func integer(o *ordjson.Object, key string) int64 {
	v, _ := o.Get(key)
	n, ok := v.(json.Number)
	if !ok {
		return -1
	}
	i, err := n.Int64()
	if err != nil {
		return -1
	}
	return i
}
func readObject(path string) (*ordjson.Object, error) {
	v, err := ordjson.ReadFile(path)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("%s is not an object", path)
	}
	return o, nil
}
func journalPath(root string) string { return filepath.Join(root, "migration", "hc-schema2.json") }
func readJournal(root string) (Journal, error) {
	var j Journal
	b, err := os.ReadFile(journalPath(root))
	if err != nil {
		return j, err
	}
	err = json.Unmarshal(b, &j)
	return j, err
}
func writeJournal(root string, j Journal) error {
	object, err := ordjson.FromValue(j)
	if err != nil {
		return err
	}
	return ordjson.WriteFile(journalPath(root), object)
}

// Stable installation assets, checkout ownership and live lock inodes are not
// ledger data. They retain their original paths and are never copied or removed.
func excluded(path string, isDir bool) bool {
	first := strings.Split(filepath.ToSlash(path), "/")[0]
	switch first {
	case "generations", "exports", "migration", "checkouts", "dev":
		return true
	}
	if path == ".lock" || path == ".deliver.lock" || path == ".migration.lock" || path == ".init.lock" || path == "dev.json" {
		return true
	}
	return !isDir && first == "deliver" && strings.HasSuffix(path, ".lock")
}
func inventory(root string) ([]File, string, error) {
	var files []File
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if excluded(rel, d.IsDir()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("migration refuses non-regular ledger entry %s", rel)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		files = append(files, File{Path: rel, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data)), Mode: uint32(info.Mode().Perm())})
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	encoded, _ := json.Marshal(files)
	sum := sha256.Sum256(encoded)
	return files, hex.EncodeToString(sum[:]), nil
}
func copyFiles(source, target string, files []File) error {
	for _, entry := range files {
		if filepath.IsAbs(entry.Path) || entry.Path == ".." || strings.HasPrefix(entry.Path, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe export path")
		}
		data, err := os.ReadFile(filepath.Join(source, entry.Path))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != entry.SHA256 || int64(len(data)) != entry.Bytes {
			return fmt.Errorf("source changed: %s", entry.Path)
		}
		path := filepath.Join(target, entry.Path)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if old, err := os.ReadFile(path); err == nil {
			if string(old) == string(data) {
				continue
			}
			return fmt.Errorf("staged file differs from immutable export: %s", entry.Path)
		} else if !os.IsNotExist(err) {
			return err
		}
		f, err := os.CreateTemp(filepath.Dir(path), ".migration-copy-")
		if err != nil {
			return err
		}
		temp := f.Name()
		if err = f.Chmod(os.FileMode(entry.Mode) & 0600); err != nil {
			f.Close()
			os.Remove(temp)
			return err
		}
		_, writeErr := f.Write(data)
		syncErr := f.Sync()
		closeErr := f.Close()
		if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
			os.Remove(temp)
			return err
		}
		if err = os.Rename(temp, path); err != nil {
			os.Remove(temp)
			return err
		}
		dir, err := os.Open(filepath.Dir(path))
		if err != nil {
			return err
		}
		syncErr = dir.Sync()
		closeErr = dir.Close()
		if err = errors.Join(syncErr, closeErr); err != nil {
			return err
		}

	}
	return nil
}
func validateFiles(root string, files []File) error {
	for _, entry := range files {
		b, err := os.ReadFile(filepath.Join(root, entry.Path))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		if int64(len(b)) != entry.Bytes || hex.EncodeToString(sum[:]) != entry.SHA256 {
			return fmt.Errorf("export validation failed: %s", entry.Path)
		}
	}
	return nil
}
func validateRecords(root string) error {
	paths, err := filepath.Glob(filepath.Join(root, "tasks", "t-*", "task.json"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		o, err := readObject(path)
		if err != nil {
			return err
		}
		id, _ := o.Get("id")
		if id != filepath.Base(filepath.Dir(path)) || integer(o, "schema") != 1 {
			return fmt.Errorf("historical task identity/schema mismatch at %s", path)
		}
	}
	return nil
}

func withoutHeader(files []File) []File {
	var out []File
	for _, entry := range files {
		if entry.Path != "state.json" {
			out = append(out, entry)
		}
	}
	return out
}
func proofObjects(proofs []Evidence) []any {
	out := make([]any, 0, len(proofs))
	for _, proof := range proofs {
		object, _ := ordjson.FromValue(proof)
		out = append(out, object)
	}
	return out
}

func fileDigest(files []File) string {
	encoded, _ := json.Marshal(files)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
func privateDir(root, path string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("generation path escapes state root")
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err = os.Mkdir(current, 0700); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("ledger generation path must not contain symlinks: %s", current)
		}
	}
	return nil
}
