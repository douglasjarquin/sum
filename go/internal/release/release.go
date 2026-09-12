package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	Manifest = "release.json"
	Schema   = 1

	// GraphDir is codegraph's own storage name; a release tree must never carry one.
	GraphDir = ".codegraph"
)

var (
	CoreTools = []string{"python3", "node", "herdr", "gh"}

	requiredFiles = []string{"bin/sumctl", "bin/herdr-mesh", "bin/herdr-scoped", "lib/sumctl.py"}
	workerSkills  = []string{"skills/sum-worker/SKILL.md", "skills/worker/SKILL.md"}

	sha40Hex        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	shaPrefix       = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	platformPattern = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)

	inventoryRequired = []string{"id", "source", "version", "checksum", "license", "platforms", "requirements", "role", "owner", "contracts"}
)

// VerifyError mirrors a Python `raise SumError(...)` inside verify_release: an expected, catchable validation
// failure. Any other error (an unexpected filesystem failure while hashing, say) propagates uncaught, exactly as
// an unrelated Python exception would crash past `except SumError`.
type VerifyError struct{ msg string }

func (e *VerifyError) Error() string { return e.msg }

func verifyErrorf(format string, args ...any) error {
	return &VerifyError{msg: fmt.Sprintf(format, args...)}
}

func releasesDir(root string) string {
	return filepath.Join(root, ".local", "releases")
}

func Sha256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// ContentID identifies a symlink by its target text, a regular file by its content hash.
func ContentID(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, linkErr := os.Readlink(path)
		if linkErr != nil {
			return "", linkErr
		}
		return "link:" + target, nil
	}
	hash, hashErr := Sha256File(path)
	if hashErr != nil {
		return "", hashErr
	}
	return "sha256:" + hash, nil
}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case json.Number:
		f, err := t.Float64()
		return err != nil || f != 0
	case []any:
		return len(t) > 0
	case *ordjson.Object:
		return t != nil && t.Len() > 0
	default:
		return v != nil
	}
}

func intEquals(v any, n int) bool {
	num, ok := v.(json.Number)
	if !ok {
		return false
	}
	i, err := num.Int64()
	return err == nil && i == int64(n)
}

// getPath walks nested *ordjson.Object values, mirroring Python's chained `.get(key, {})` default-empty-dict
// pattern: a missing key or non-object intermediate yields nil rather than an error.
func getPath(o *ordjson.Object, keys ...string) any {
	var cur any = o
	for _, k := range keys {
		obj, ok := cur.(*ordjson.Object)
		if !ok || obj == nil {
			return nil
		}
		v, has := obj.Get(k)
		if !has {
			return nil
		}
		cur = v
	}
	return cur
}

func asObject(v any) *ordjson.Object {
	obj, _ := v.(*ordjson.Object)
	return obj
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asList(v any) []any {
	list, _ := v.([]any)
	return list
}

func hasAllKeys(o *ordjson.Object, keys []string) bool {
	if o == nil {
		return false
	}
	for _, k := range keys {
		if _, has := o.Get(k); !has {
			return false
		}
	}
	return true
}

func contains(list []any, want string) bool {
	for _, v := range list {
		if s, ok := v.(string); ok && s == want {
			return true
		}
	}
	return false
}

// ValidateDependencyInventory ports `validate_dependency_inventory`.
func ValidateDependencyInventory(value *ordjson.Object) error {
	schemaValue := any(nil)
	var deps []any
	depsOK := false
	if value != nil {
		schemaValue, _ = value.Get("schema")
		if depsValue, has := value.Get("dependencies"); has {
			deps, depsOK = depsValue.([]any)
		}
	}
	if !intEquals(schemaValue, Schema) || !depsOK {
		return verifyErrorf("Dependency inventory has an unsupported schema")
	}
	seen := map[string]bool{}
	for _, dv := range deps {
		entry := asObject(dv)
		valid := entry != nil && hasAllKeys(entry, inventoryRequired)
		var id string
		if valid {
			idValue, _ := entry.Get("id")
			id = asString(idValue)
			valid = truthy(idValue)
		}
		if !valid {
			return verifyErrorf("Dependency inventory contains an incomplete entry")
		}
		if seen[id] {
			return verifyErrorf("Dependency inventory repeats %s", id)
		}
		seen[id] = true
	}
	return nil
}

func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if len(detail) > 4000 {
			detail = detail[len(detail)-4000:]
		}
		return "", fmt.Errorf("git exited %d: %s", exitErr.ExitCode(), detail)
	}
	return "", fmt.Errorf("git: %s", err)
}

func resolvePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

// InstallationRoot ports `installation_root`: `dev` and `release` commands act on the installation that owns the
// given state home, never on a development checkout.
func InstallationRoot(s *store.Store) (string, error) {
	if filepath.Base(s.Home) != ".sum" || !s.Designated() {
		return "", fmt.Errorf("%s is not a sum installation's state home; run dev and release commands with the installation's ./bin/sumctl.", s.Home)
	}
	root := filepath.Dir(s.Home)
	out, err := runGit("-C", root, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	toplevel, err := resolvePath(strings.TrimSpace(out))
	if err != nil {
		return "", err
	}
	resolvedRoot, err := resolvePath(root)
	if err != nil {
		return "", err
	}
	if toplevel != resolvedRoot {
		return "", fmt.Errorf("%s is not the top level of a Git checkout.", root)
	}
	return resolvedRoot, nil
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}

// VerifyRelease ports `verify_release`: a bundle is usable only when its manifest and every referenced file
// agree. Returns a *VerifyError for every expected validation failure (mirroring `raise SumError`); any other
// error is an unexpected filesystem failure and is not meant to be caught the same way.
func VerifyRelease(path, expectedSHA string) (*ordjson.Object, error) {
	manifestPath := filepath.Join(path, Manifest)
	if !isRegularFileOrSymlinkedFile(manifestPath) {
		return nil, verifyErrorf("%s: no %s", path, Manifest)
	}
	manifestValue, err := ordjson.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	manifest, ok := manifestValue.(*ordjson.Object)
	if !ok {
		return nil, verifyErrorf("%s: unsupported release manifest", path)
	}
	schemaValue, _ := manifest.Get("schema")
	kindValue, _ := manifest.Get("kind")
	if !intEquals(schemaValue, Schema) || asString(kindValue) != "sum-release" {
		return nil, verifyErrorf("%s: unsupported release manifest", path)
	}

	shaValue := getPath(manifest, "source", "sha")
	sha, shaIsString := shaValue.(string)
	if !shaIsString || !sha40Hex.MatchString(sha) || (expectedSHA != "" && sha != expectedSHA) {
		return nil, verifyErrorf("%s: manifest source SHA is missing or mismatched", path)
	}

	filesValue, _ := manifest.Get("files")
	files := asObject(filesValue)
	if files == nil || files.Len() == 0 {
		return nil, verifyErrorf("%s: manifest lists no files", path)
	}
	for _, name := range files.Keys() {
		expectedValue, _ := files.Get(name)
		member := filepath.Join(path, name)
		if !isSymlink(member) && !isRegularFile(member) {
			return nil, verifyErrorf("%s: missing %s", path, name)
		}
		id, idErr := ContentID(member)
		if idErr != nil {
			return nil, idErr
		}
		if id != asString(expectedValue) {
			return nil, verifyErrorf("%s: %s does not match its manifest hash", path, name)
		}
	}
	for _, required := range requiredFiles {
		if _, has := files.Get(required); !has {
			return nil, verifyErrorf("%s: release lacks %s", path, required)
		}
	}
	hasWorkerSkill := false
	for _, name := range workerSkills {
		if _, has := files.Get(name); has {
			hasWorkerSkill = true
			break
		}
	}
	if !hasWorkerSkill {
		return nil, verifyErrorf("%s: release lacks a Sum worker skill resource", path)
	}
	if !isExecutable(filepath.Join(path, "bin", "sumctl")) {
		return nil, verifyErrorf("%s: bin/sumctl is not executable", path)
	}
	if _, err := os.Lstat(filepath.Join(path, ".sum")); err == nil {
		return nil, verifyErrorf("%s: a release tree must not contain .sum state", path)
	}
	if _, err := os.Lstat(filepath.Join(path, GraphDir)); err == nil {
		return nil, verifyErrorf("%s: a release tree must not contain a %s index; graph state never rides an immutable bundle", path, GraphDir)
	}

	toolPaths := asObject(getPath(manifest, "dependencies", "tools", "paths"))
	if toolPaths == nil {
		toolPaths = ordjson.NewObject()
	}
	for _, name := range CoreTools {
		if _, has := toolPaths.Get(name); !has {
			return nil, verifyErrorf("%s: manifest lacks the pinned tool %s", path, name)
		}
	}
	for _, name := range toolPaths.Keys() {
		expectedValue, _ := toolPaths.Get(name)
		link := filepath.Join(path, ".local", "bin", name)
		linkIsSymlink := isSymlink(link)
		target := ""
		if linkIsSymlink {
			target, _ = os.Readlink(link)
		}
		if !linkIsSymlink || target != asString(expectedValue) || !isRegularFile(link) {
			return nil, verifyErrorf("%s: pinned tool %s is missing or does not resolve", path, name)
		}
	}

	native := asObject(getPath(manifest, "dependencies", "native"))
	inventoryValue := getPath(manifest, "dependencies", "inventory")
	if inventoryValue != nil {
		if err := ValidateDependencyInventory(asObject(inventoryValue)); err != nil {
			return nil, err
		}
	}
	if truthy(native) {
		if _, has := native.Get("sumctl-go"); !has {
			return nil, verifyErrorf("%s: native dependency metadata is incomplete", path)
		}
		if _, has := native.Get("herdr-mesh"); !has {
			return nil, verifyErrorf("%s: native dependency metadata is incomplete", path)
		}
		for _, name := range native.Keys() {
			artifactValue, _ := native.Get(name)
			artifact := asObject(artifactValue)
			relative := ""
			if artifact != nil {
				relativeValue, _ := artifact.Get("path")
				relative, _ = relativeValue.(string)
			}
			if relative == "" || filepath.IsAbs(relative) || containsDotDot(relative) {
				return nil, verifyErrorf("%s: native artifact %s has an invalid path", path, name)
			}
			member := filepath.Join(path, relative)
			if isSymlink(member) || !isExecutable(member) {
				return nil, verifyErrorf("%s: native artifact %s is missing or not executable", path, name)
			}
			expectedHash, _ := artifact.Get("sha256")
			hash, hashErr := Sha256File(member)
			if hashErr != nil || hash != asString(expectedHash) {
				return nil, verifyErrorf("%s: native artifact %s does not match its manifest hash", path, name)
			}
			targetValue, _ := artifact.Get("platform")
			target, targetIsString := targetValue.(string)
			if !targetIsString || !platformPattern.MatchString(target) {
				return nil, verifyErrorf("%s: native artifact %s lacks a valid GOOS-GOARCH target", path, name)
			}
			inv := asObject(inventoryValue)
			var catalogEntry *ordjson.Object
			if inv != nil {
				depsList := asList(getPath(inv, "dependencies"))
				for _, dv := range depsList {
					entry := asObject(dv)
					idValue, _ := entry.Get("id")
					if asString(idValue) == name {
						catalogEntry = entry
						break
					}
				}
			}
			if catalogEntry == nil || !contains(asList(getPath(catalogEntry, "platforms")), target) {
				return nil, verifyErrorf("%s: native artifact %s target %s is not in the dependency inventory", path, name, target)
			}
		}
	}
	return manifest, nil
}

func containsDotDot(relative string) bool {
	for _, part := range strings.Split(filepath.ToSlash(relative), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func isRegularFileOrSymlinkedFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// ReleaseSummary ports `release_summary`.
func ReleaseSummary(path string, manifest *ordjson.Object, staged bool) (*ordjson.Object, error) {
	sha := asString(getPath(manifest, "source", "sha"))
	result := ordjson.NewObject()
	result.Set("release", path)
	result.Set("sha", sha)
	result.Set("staged", staged)
	result.Set("activated", false)
	result.Set("manifest", manifest)
	result.Set("note", "Staged only. No pointer, MCP configuration, live process, or installed dependency was changed; activation is a separate, explicit step.")
	return result, nil
}

// List ports `release_list`.
func List(s *store.Store) (*ordjson.Object, error) {
	root, err := InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	releases := releasesDir(root)
	var names []string
	if entries, readErr := os.ReadDir(releases); readErr == nil {
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		sort.Strings(names)
	}
	var rows []any
	var inProgress []any
	for _, name := range names {
		if strings.HasPrefix(name, ".") {
			inProgress = append(inProgress, name)
			continue
		}
		entryPath := filepath.Join(releases, name)
		row := ordjson.NewObject()
		manifest, verifyErr := VerifyRelease(entryPath, name)
		if verifyErr != nil {
			if _, isVerifyError := verifyErr.(*VerifyError); !isVerifyError {
				return nil, verifyErr
			}
			row.Set("sha", name)
			row.Set("path", entryPath)
			row.Set("ok", false)
			row.Set("error", verifyErr.Error())
		} else {
			sumVersion, _ := manifest.Get("sum_version")
			stagedAt, _ := manifest.Get("staged_at")
			row.Set("sha", name)
			row.Set("path", entryPath)
			row.Set("ok", true)
			row.Set("sum_version", sumVersion)
			row.Set("staged_at", stagedAt)
		}
		rows = append(rows, row)
	}
	result := ordjson.NewObject()
	result.Set("installation", root)
	result.Set("releases", rows)
	result.Set("in_progress", inProgress)
	result.Set("activated", nil)
	result.Set("note", "Nothing here is active; staged bundles are kept until you remove one deliberately. Automatic garbage collection is out of scope.")
	return result, nil
}

// Show ports `release_show`.
func Show(s *store.Store, sha string) (*ordjson.Object, error) {
	root, err := InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	if !shaPrefix.MatchString(sha) {
		return nil, verifyErrorf("Give a release by its commit SHA.")
	}
	releases := releasesDir(root)
	var matches []string
	entries, readErr := os.ReadDir(releases)
	if readErr == nil {
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if strings.HasPrefix(name, sha) {
				matches = append(matches, filepath.Join(releases, name))
			}
		}
	}
	if len(matches) != 1 {
		return nil, verifyErrorf("%d staged releases match %s.", len(matches), sha)
	}
	name := filepath.Base(matches[0])
	manifest, err := VerifyRelease(matches[0], name)
	if err != nil {
		return nil, err
	}
	return ReleaseSummary(matches[0], manifest, false)
}
