// Package procedure pins the worker procedure a brief references as an immutable task resource.
//
// A brief no longer embeds the procedure text. Each revision records rows naming write-once,
// content-addressed copies under the task directory, so a fresh or recovered worker reads exactly
// the procedure its revision was generated with, whatever the runtime later becomes. Sources lists
// what is pinned; changing that list or the files' content never changes the brief renderer.
package procedure

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/shquote"
)

const (
	// Dir is the task-relative directory holding pinned procedure resources.
	Dir = "procedure"
	// MaxBytes bounds one source; a larger file is a runaway, not a procedure.
	MaxBytes = 256 << 10

	// Required resources are read before any other work by a session that has not read them.
	Required = "required"
	// OnDemand resources are read when their When condition applies.
	OnDemand = "on-demand"
)

// Source is one runtime file pinned into every new brief revision.
type Source struct {
	Name string // stable resource name, also the pinned file's prefix
	Path string // slash-separated path relative to the runtime root
	Load string // Required or OnDemand
	When string // for OnDemand: when to read it
}

// Sources is the worker procedure, in reading order: the required core, then action-scoped files a
// worker reads only when their condition applies.
var Sources = []Source{
	{Name: "sum-worker", Path: "skills/sum-worker/SKILL.md", Load: Required},
	{Name: "sum-worker-evidence", Path: "skills/sum-worker/references/evidence.md", Load: OnDemand,
		When: "the task fixes something a user can see, or a feature-map row covering your change names a screenshot, screencast, or red/green pair"},
	{Name: "sum-worker-environment", Path: "skills/sum-worker/references/environment.md", Load: OnDemand,
		When: "you run the application, or start, record, inspect, or stop a service for this task"},
	{Name: "sum-worker-graph", Path: "skills/sum-worker/references/graph.md", Load: OnDemand,
		When: "the `## Code graph` section or `context --section execution` reports an index other than `not built`, or before you ask for one"},
	{Name: "sum-worker-refresh", Path: "skills/sum-worker/references/refresh.md", Load: OnDemand,
		When: "a `sum refresh` message or a `brief revision rN is requested` notice reaches you, or before you adopt any revision"},
	{Name: "sum-worker-factory", Path: "skills/sum-worker/references/factory.md", Load: OnDemand,
		When: "your brief says this task is a claimed factory issue"},
}

// Role cores that `init` names for a session it registers; they are read from the runtime, never pinned.
var (
	Coordinator = Source{Name: "coordinator", Path: "COORDINATOR.md", Load: Required}
	Developer   = Source{Name: "sum-develop", Path: "skills/sum-develop/SKILL.md", Load: Required}
)

// NoBrief is the consequence a worker-procedure failure reports.
const NoBrief = "no brief was written"

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sourceError(src Source, runtimeRoot, problem, consequence string) error {
	kind := "Worker procedure"
	if src.Name == Coordinator.Name || src.Name == Developer.Name {
		kind = "Role procedure"
	}
	return fmt.Errorf("%s %s %s in runtime %s; %s. Restore that tracked file (`git -C %s checkout -- %s` in a checkout runtime, or activate a verified release) and retry; never substitute another copy or memory.", kind, src.Path, problem, runtimeRoot, consequence, shquote.Quote(runtimeRoot), src.Path)
}

func readSource(runtimeRoot string, src Source, manifest *ordjson.Object) ([]byte, error) {
	return readSourceFor(runtimeRoot, src, manifest, NoBrief)
}

func readSourceFor(runtimeRoot string, src Source, manifest *ordjson.Object, consequence string) ([]byte, error) {
	fail := func(problem string) error { return sourceError(src, runtimeRoot, problem, consequence) }
	path := filepath.Join(runtimeRoot, filepath.FromSlash(src.Path))
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fail("is missing")
	}
	if !info.Mode().IsRegular() {
		return nil, fail("is not a regular file")
	}
	if info.Size() == 0 {
		return nil, fail("is empty")
	}
	if info.Size() > MaxBytes {
		return nil, fail(fmt.Sprintf("is larger than %d bytes", MaxBytes))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fail("is unreadable (" + err.Error() + ")")
	}
	if len(data) == 0 || len(data) > MaxBytes {
		return nil, fail("changed size while it was read")
	}
	if manifest != nil {
		files, _ := manifest.Get("files")
		listed, _ := files.(*ordjson.Object)
		var recorded string
		if listed != nil {
			value, _ := listed.Get(src.Path)
			recorded, _ = value.(string)
		}
		if recorded == "" {
			return nil, fail("is not listed in the release manifest")
		}
		if recorded != "sha256:"+sha256Hex(data) {
			return nil, fail("does not match its release manifest hash")
		}
	}
	return data, nil
}

// releaseManifest returns the runtime's release.json when the runtime is a release tree.
func releaseManifest(runtimeRoot, consequence string) (*ordjson.Object, error) {
	path := filepath.Join(runtimeRoot, "release.json")
	if _, err := os.Lstat(path); err != nil {
		return nil, nil
	}
	value, err := ordjson.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Release manifest %s is unreadable (%v); %s.", path, err, consequence)
	}
	manifest, _ := value.(*ordjson.Object)
	if manifest == nil {
		return nil, fmt.Errorf("Release manifest %s is not a JSON object; %s.", path, consequence)
	}
	return manifest, nil
}

func readAll(runtimeRoot string) ([][]byte, error) {
	manifest, err := releaseManifest(runtimeRoot, NoBrief)
	if err != nil {
		return nil, err
	}
	contents := make([][]byte, 0, len(Sources))
	for _, src := range Sources {
		data, err := readSource(runtimeRoot, src, manifest)
		if err != nil {
			return nil, err
		}
		contents = append(contents, data)
	}
	return contents, nil
}

// Load validates one role source in the runtime exactly as pinning would (regular file, size bound,
// release-manifest hash) and returns its reference row with an absolute path, plus its content.
// consequence names what the caller refuses when the file is unusable, e.g. "no role was claimed".
func Load(runtimeRoot string, src Source, consequence string) (*ordjson.Object, []byte, error) {
	manifest, err := releaseManifest(runtimeRoot, consequence)
	if err != nil {
		return nil, nil, err
	}
	data, err := readSourceFor(runtimeRoot, src, manifest, consequence)
	if err != nil {
		return nil, nil, err
	}
	row := ordjson.NewObject()
	row.Set("name", src.Name)
	row.Set("source", src.Path)
	row.Set("path", filepath.Join(runtimeRoot, filepath.FromSlash(src.Path)))
	row.Set("sha256", sha256Hex(data))
	row.Set("bytes", json.Number(fmt.Sprint(len(data))))
	row.Set("load", src.Load)
	return row, data, nil
}

// Describe is Load without the content.
func Describe(runtimeRoot string, src Source, consequence string) (*ordjson.Object, error) {
	row, _, err := Load(runtimeRoot, src, consequence)
	return row, err
}

// Reference is Describe for a role that proceeds regardless: the row carries `ok`, and on failure the
// source's identity with the `error` instead of its path and hash.
func Reference(runtimeRoot string, src Source, consequence string) *ordjson.Object {
	row, err := Describe(runtimeRoot, src, consequence)
	if err != nil {
		row = ordjson.NewObject()
		row.Set("name", src.Name)
		row.Set("source", src.Path)
		row.Set("load", src.Load)
		row.Set("ok", false)
		row.Set("error", err.Error())
		return row
	}
	row.Set("ok", true)
	return row
}

// Check validates every source without writing anything, so a caller can refuse before side effects.
func Check(runtimeRoot string) error {
	_, err := readAll(runtimeRoot)
	return err
}

func relativePath(src Source, sha string) string {
	return Dir + "/" + src.Name + "-" + sha[:16] + ".md"
}

func pinnedMismatch(full, recorded, actual string) error {
	return fmt.Errorf("Pinned worker procedure %s does not match its recorded sha256 %s (found %s). It is never overwritten: move that file aside, then run `sumctl brief regenerate TASK_ID`, which pins it again from the verified runtime copy.", full, recorded, actual)
}

// Pin copies every source into taskDir as write-once, content-addressed files and returns the rows a
// revision records. Identical content reuses its existing file; nothing is ever rewritten.
func Pin(runtimeRoot, taskDir string) ([]any, error) {
	contents, err := readAll(runtimeRoot)
	if err != nil {
		return nil, err
	}
	rows := make([]any, 0, len(Sources))
	for i, src := range Sources {
		data := contents[i]
		sha := sha256Hex(data)
		rel := relativePath(src, sha)
		full := filepath.Join(taskDir, filepath.FromSlash(rel))
		if info, statErr := os.Lstat(full); statErr == nil {
			existing, readErr := os.ReadFile(full)
			if !info.Mode().IsRegular() || readErr != nil {
				return nil, pinnedMismatch(full, sha, "an unreadable or non-regular file")
			}
			if actual := sha256Hex(existing); actual != sha {
				return nil, pinnedMismatch(full, sha, actual)
			}
		} else if err := WriteOnce(full, data); err != nil {
			return nil, err
		}
		row := ordjson.NewObject()
		row.Set("name", src.Name)
		row.Set("source", src.Path)
		row.Set("path", rel)
		row.Set("sha256", sha)
		row.Set("bytes", json.Number(fmt.Sprint(len(data))))
		row.Set("load", src.Load)
		if src.When != "" {
			row.Set("when", src.When)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// WriteOnce publishes data at path through a hard link from a synced 0600 temporary file, so the
// target appears complete or not at all and an existing file is never replaced.
func WriteOnce(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".write-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	return os.Link(tmpPath, path)
}

func asString(o *ordjson.Object, key string) string {
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}

// Rows returns the procedure rows a revision policy records, or nil for a revision written before them.
func Rows(policy *ordjson.Object) []any {
	if policy == nil {
		return nil
	}
	value, _ := policy.Get("procedure")
	rows, _ := value.([]any)
	return rows
}

// Path is a row's absolute file inside taskDir (not validated; Verify does that).
func Path(taskDir string, row *ordjson.Object) string {
	return filepath.Join(taskDir, filepath.FromSlash(asString(row, "path")))
}

// SHA is the recorded hash of the named resource, or "" when the rows do not name it.
func SHA(rows []any, name string) string {
	for _, raw := range rows {
		if row, _ := raw.(*ordjson.Object); row != nil && asString(row, "name") == name {
			return asString(row, "sha256")
		}
	}
	return ""
}

// ResourcePath is a row's file inside taskDir, and whether the recorded path is a plain file name
// directly under Dir (never absolute, never escaping the task directory).
func ResourcePath(taskDir string, row *ordjson.Object) (string, bool) {
	rel := asString(row, "path")
	name := strings.TrimPrefix(rel, Dir+"/")
	if name == rel || name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", false
	}
	return filepath.Join(taskDir, Dir, name), true
}

// VerifyRow checks one recorded resource: a task-relative path under Dir, a regular file, the recorded hash.
func VerifyRow(taskDir string, row *ordjson.Object) error {
	if row == nil {
		return fmt.Errorf("A worker procedure row is malformed.")
	}
	recorded := asString(row, "sha256")
	full, ok := ResourcePath(taskDir, row)
	if !ok || recorded == "" {
		return fmt.Errorf("Worker procedure row %q names an invalid resource path %q.", asString(row, "name"), asString(row, "path"))
	}
	info, err := os.Lstat(full)
	if err != nil {
		return fmt.Errorf("Pinned worker procedure %s (sha256 %s) is missing. Restore it from a state backup, or move nothing and run `sumctl brief regenerate TASK_ID`, which pins it again from the verified runtime copy when that copy still has this hash.", full, recorded)
	}
	if !info.Mode().IsRegular() {
		return pinnedMismatch(full, recorded, "a non-regular file")
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return pinnedMismatch(full, recorded, "an unreadable file")
	}
	if actual := sha256Hex(data); actual != recorded {
		return pinnedMismatch(full, recorded, actual)
	}
	return nil
}

// References lists recorded rows with absolute paths and their integrity (`ok`, and `error` when not),
// so a fresh or recovered worker finds its procedure from the task record. Nil before rows existed.
func References(taskDir string, rows []any) []any {
	if rows == nil {
		return nil
	}
	refs := make([]any, 0, len(rows))
	for _, raw := range rows {
		row, _ := raw.(*ordjson.Object)
		ref := ordjson.NewObject()
		for _, key := range []string{"name", "sha256", "bytes", "load", "when"} {
			if row == nil {
				break
			}
			if v, has := row.Get(key); has {
				ref.Set(key, v)
			}
		}
		if row != nil {
			if full, ok := ResourcePath(taskDir, row); ok {
				ref.Set("path", full)
			}
		}
		if err := VerifyRow(taskDir, row); err != nil {
			ref.Set("ok", false)
			ref.Set("error", err.Error())
		} else {
			ref.Set("ok", true)
		}
		refs = append(refs, ref)
	}
	return refs
}

// Verify checks every recorded resource of a revision.
func Verify(taskDir string, rows []any) error {
	for _, raw := range rows {
		row, _ := raw.(*ordjson.Object)
		if err := VerifyRow(taskDir, row); err != nil {
			return err
		}
	}
	return nil
}
