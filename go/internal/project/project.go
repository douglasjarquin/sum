package project

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pyrepr"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	ProjectsDir    = "projects"
	ProjectsFile   = "projects.json"
	ProjectsSchema = 1
	DefaultGitHost = "github.com"
)

var (
	projectPartPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	gitHostPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}(?::[0-9]{1,5})?$`)
	sshLikePattern     = regexp.MustCompile(`^(?:ssh://)?(?:[A-Za-z0-9._-]+@)?([A-Za-z0-9.-]+(?::[0-9]+)?)[:/]([^/\s]+)/([^/\s]+?)(?:\.git)?/?$`)
	httpsPattern       = regexp.MustCompile(`^https?://(?:[^@/\s]+@)?([A-Za-z0-9.-]+(?::[0-9]+)?)/([^/\s]+)/([^/\s]+?)(?:\.git)?/?$`)

	projectSummaryKeys = []string{"name", "host", "owner", "repo", "kind", "path", "remote", "enrolled_at", "enrolled_by", "canonical_path", "note"}
)

// Identity is a host/owner/repo/name tuple mirroring `project_identity`'s dict.
type Identity struct {
	Host, Owner, Repo, Name string
}

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func intEquals(v any, n int) bool {
	num, ok := v.(json.Number)
	if !ok {
		return false
	}
	i, err := num.Int64()
	return err == nil && i == int64(n)
}

func asObject(v any) *ordjson.Object {
	obj, _ := v.(*ordjson.Object)
	return obj
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

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

func pick(o *ordjson.Object, keys []string) *ordjson.Object {
	result := ordjson.NewObject()
	for _, k := range keys {
		var v any
		if o != nil {
			v, _ = o.Get(k)
		}
		result.Set(k, v)
	}
	return result
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
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

// NormalizeHost ports `normalize_host`.
func NormalizeHost(host string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(host))
	if normalized == "" || !gitHostPattern.MatchString(normalized) {
		return "", fmt.Errorf("Invalid Git host %s.", pyrepr.Repr(host))
	}
	return normalized, nil
}

// ProjectIdentity ports `project_identity`.
func ProjectIdentity(host, owner, repo string) (*Identity, error) {
	normalizedHost, err := NormalizeHost(host)
	if err != nil {
		return nil, err
	}
	repo = strings.TrimSuffix(repo, ".git")
	for _, part := range []string{owner, repo} {
		if !projectPartPattern.MatchString(part) || part == "." || part == ".." || strings.HasPrefix(part, ".") {
			return nil, fmt.Errorf("Invalid repository path component %s: letters, digits, dot, underscore, or dash, not starting with a dot.", pyrepr.Repr(part))
		}
	}
	name := fmt.Sprintf("%s/%s", owner, repo)
	if normalizedHost != DefaultGitHost {
		name = fmt.Sprintf("%s/%s/%s", normalizedHost, owner, repo)
	}
	return &Identity{Host: normalizedHost, Owner: owner, Repo: repo, Name: name}, nil
}

// ParseRemoteIdentity ports `parse_remote_identity`: host/owner/repo from a Git remote URL, or nil when the URL
// does not name one (file://, local paths, other shapes).
func ParseRemoteIdentity(url string) *Identity {
	text := strings.TrimSpace(url)
	if text == "" {
		return nil
	}
	var match []string
	switch {
	case strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://"):
		match = httpsPattern.FindStringSubmatch(text)
	case strings.Contains(text, "://") && !strings.HasPrefix(text, "ssh://"):
		return nil
	default:
		match = sshLikePattern.FindStringSubmatch(text)
	}
	if match == nil {
		return nil
	}
	identity, err := ProjectIdentity(match[1], match[2], match[3])
	if err != nil {
		return nil
	}
	return identity
}

// SameRemote ports `same_remote`. `identity` mirrors Python's dict-shaped fallback parameter (any object with
// host/owner/repo keys, typically the registered record itself) — nil when not supplied.
func SameRemote(recorded string, observed any, identity *ordjson.Object) bool {
	observedStr, ok := observed.(string)
	if !ok {
		return false
	}
	a := ParseRemoteIdentity(recorded)
	b := ParseRemoteIdentity(observedStr)
	if a != nil && b != nil {
		return a.Host == b.Host && a.Owner == b.Owner && a.Repo == b.Repo
	}
	if identity != nil && b != nil {
		idHost := asString(getPath(identity, "host"))
		idOwner := asString(getPath(identity, "owner"))
		idRepo := asString(getPath(identity, "repo"))
		return idHost == b.Host && idOwner == b.Owner && idRepo == b.Repo
	}
	return strings.TrimRight(recorded, "/") == strings.TrimRight(observedStr, "/")
}

func emptyProjects() *ordjson.Object {
	result := ordjson.NewObject()
	result.Set("schema", jsonInt(ProjectsSchema))
	result.Set("projects", ordjson.NewObject())
	return result
}

// ReadProjects ports `read_projects`.
func ReadProjects(s *store.Store) (*ordjson.Object, error) {
	path := filepath.Join(s.Home, ProjectsFile)
	if isSymlink(path) {
		return nil, fmt.Errorf("%s must not be a symlink.", path)
	}
	info, statErr := os.Stat(path)
	if statErr != nil || info.IsDir() {
		return emptyProjects(), nil
	}
	value, err := ordjson.ReadFile(path)
	if err != nil {
		return nil, err
	}
	obj := asObject(value)
	if obj == nil {
		return nil, fmt.Errorf("Unsupported project registry %s; preserve it and use the matching sum release. No in-place migration.", path)
	}
	schemaValue, _ := obj.Get("schema")
	projectsValue, hasProjects := obj.Get("projects")
	_, projectsIsObject := projectsValue.(*ordjson.Object)
	if !intEquals(schemaValue, ProjectsSchema) || !hasProjects || !projectsIsObject {
		return nil, fmt.Errorf("Unsupported project registry %s; preserve it and use the matching sum release. No in-place migration.", path)
	}
	return obj, nil
}

func projectsRoot(root string) string {
	return filepath.Join(root, ProjectsDir)
}

// InstallationOf ports `installation_of`: the installation a designated `.sum` home belongs to; `runtimeRoot`
// (mirroring Python's `ROOT`) otherwise, i.e. a lab home under another name.
func InstallationOf(s *store.Store, runtimeRoot string) string {
	if filepath.Base(s.Home) == ".sum" && s.Designated() {
		return filepath.Dir(s.Home)
	}
	return runtimeRoot
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

func runGitIgnoringExit(args ...string) string {
	cmd := exec.Command("git", args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	_ = cmd.Run()
	return stdout.String()
}

func gitToplevel(path string) (string, bool) {
	out, err := runGit("-C", path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	resolved, err := resolvePath(strings.TrimSpace(out))
	if err != nil {
		return "", false
	}
	return resolved, true
}

func gitRemote(path, name string) (string, bool) {
	out, err := runGit("-C", path, "remote", "get-url", name)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(out), true
}

func linkedWorktrees(path string) ([]string, bool) {
	out, err := runGit("-C", path, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, false
	}
	resolvedSelf, _ := resolvePath(path)
	var rows []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		wt := strings.TrimPrefix(line, "worktree ")
		resolved, resolveErr := resolvePath(wt)
		if resolveErr != nil {
			resolved = wt
		}
		if resolved != resolvedSelf {
			rows = append(rows, resolved)
		}
	}
	return rows, true
}

// ObserveProject ports `observe_project`: one bounded look at a registered clone. Nothing is changed.
func ObserveProject(record *ordjson.Object) *ordjson.Object {
	path := asString(getPath(record, "path"))
	view := ordjson.NewObject()
	view.Set("path", path)
	view.Set("present", false)
	view.Set("git", false)
	view.Set("remote_matches", nil)
	view.Set("dirty", nil)
	view.Set("head", nil)
	view.Set("linked_worktrees", nil)

	if isSymlink(path) {
		view.Set("problem", "path is a symlink")
		return view
	}
	info, statErr := os.Stat(path)
	if statErr != nil || !info.IsDir() {
		view.Set("problem", "directory is missing; the registration stays, nothing was re-cloned")
		return view
	}
	view.Set("present", true)
	resolvedPath, _ := resolvePath(path)
	top, topOK := gitToplevel(path)
	if !topOK || top != resolvedPath {
		view.Set("problem", "not the top level of a Git checkout")
		return view
	}
	view.Set("git", true)
	remote, remoteOK := gitRemote(path, "origin")
	var remoteValue any
	if remoteOK {
		remoteValue = remote
	}
	view.Set("remote", remoteValue)
	recordedRemote := asString(getPath(record, "remote"))
	matches := SameRemote(recordedRemote, remoteValue, record)
	view.Set("remote_matches", matches)
	if !matches {
		view.Set("problem", fmt.Sprintf("origin is %s, not the recorded remote", pyrepr.Repr(remoteValue)))
	}
	if headOut, headErr := runGit("-C", path, "rev-parse", "HEAD"); headErr == nil {
		view.Set("head", strings.TrimSpace(headOut))
	}
	dirtyOut := runGitIgnoringExit("-C", path, "status", "--porcelain", "--untracked-files=all")
	view.Set("dirty", strings.TrimSpace(dirtyOut) != "")
	if worktrees, ok := linkedWorktrees(path); ok {
		list := make([]any, len(worktrees))
		for i, w := range worktrees {
			list[i] = w
		}
		view.Set("linked_worktrees", list)
	}
	return view
}

// ProjectSummary ports `project_summary`.
func ProjectSummary(record *ordjson.Object, observed any) *ordjson.Object {
	row := pick(record, projectSummaryKeys)
	if observed != nil {
		row.Set("observed", observed)
	}
	return row
}

// List ports `project_list`.
func List(s *store.Store, runtimeRoot string) (*ordjson.Object, error) {
	registry, err := ReadProjects(s)
	if err != nil {
		return nil, err
	}
	projectsObj := asObject(getPath(registry, "projects"))
	if projectsObj == nil {
		projectsObj = ordjson.NewObject()
	}
	names := append([]string(nil), projectsObj.Keys()...)
	sort.Strings(names)
	rows := make([]any, 0, len(names))
	for _, name := range names {
		record := asObject(getPath(projectsObj, name))
		rows = append(rows, ProjectSummary(record, ObserveProject(record)))
	}
	result := ordjson.NewObject()
	result.Set("projects", rows)
	result.Set("registry", filepath.Join(s.Home, ProjectsFile))
	result.Set("projects_dir", projectsRoot(InstallationOf(s, runtimeRoot)))
	result.Set("note", "Registrations and one bounded observation each; nothing was fetched, moved, or cleaned. Task checkouts are separate Herdr worktrees.")
	return result, nil
}

// Show ports `project_show`.
func Show(s *store.Store, name string) (*ordjson.Object, error) {
	registry, err := ReadProjects(s)
	if err != nil {
		return nil, err
	}
	projectsObj := asObject(getPath(registry, "projects"))
	var record *ordjson.Object
	if projectsObj != nil {
		record = asObject(getPath(projectsObj, name))
	}
	if record == nil {
		return nil, fmt.Errorf("No enrolled project %s. `project list` shows the registry; enroll with `project enroll owner/repo`.", pyrepr.Repr(name))
	}
	resolvedRecordPath, _ := resolvePath(asString(getPath(record, "path")))

	tasks, err := s.AllTasks()
	if err != nil {
		return nil, err
	}
	activeTasks := make([]any, 0)
	for _, t := range tasks {
		status := asString(getPath(t, "status"))
		if status == "archived" {
			continue
		}
		resolvedRepo, resolveErr := resolvePath(asString(getPath(t, "repository")))
		if resolveErr != nil || resolvedRepo != resolvedRecordPath {
			continue
		}
		entry := ordjson.NewObject()
		idValue, _ := t.Get("id")
		entry.Set("task", idValue)
		entry.Set("status", status)
		worktreeValue, _ := t.Get("worktree")
		entry.Set("worktree", worktreeValue)
		activeTasks = append(activeTasks, entry)
	}

	result := ordjson.NewObject()
	result.Set("project", ProjectSummary(record, ObserveProject(record)))
	result.Set("active_tasks", activeTasks)
	result.Set("registry", filepath.Join(s.Home, ProjectsFile))
	return result, nil
}
