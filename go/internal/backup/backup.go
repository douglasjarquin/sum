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
	"path/filepath"
	"sort"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/graphview"
	"github.com/douglasjarquin/sum/go/internal/notes"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/project"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func graphBackupRows(s *store.Store, tasks []*ordjson.Object) []any {
	var rows []any
	for _, task := range tasks {
		id := asString(func() any { v, _ := task.Get("id"); return v }())
		record, err := graphview.Read(s, id)
		if err != nil {
			row := ordjson.NewObject()
			row.Set("task", id)
			row.Set("error", err.Error())
			rows = append(rows, row)
			continue
		}
		if record == nil {
			continue
		}
		row := ordjson.NewObject()
		row.Set("task", id)
		worktree, _ := record.Get("worktree")
		row.Set("worktree", worktree)
		indexPath, _ := record.Get("index_path")
		row.Set("index_path", indexPath)
		state, _ := record.Get("state")
		row.Set("state", state)
		tool := func() *ordjson.Object {
			v, _ := record.Get("tool")
			obj, _ := v.(*ordjson.Object)
			return obj
		}()
		var toolVersion any
		if tool != nil {
			toolVersion, _ = tool.Get("version")
		}
		row.Set("tool_version", toolVersion)
		row.Set("rebuild", fmt.Sprintf("codegraph init %v with %s@%s", worktree, "@colbymchenry/codegraph", graphview.CodegraphVersion))
		rows = append(rows, row)
	}
	if rows == nil {
		rows = []any{}
	}
	return rows
}

func Run(s *store.Store, destination string) (*ordjson.Object, error) {
	expanded, err := filepath.Abs(destination)
	if err != nil {
		return nil, err
	}
	if resolved, resErr := filepath.EvalSymlinks(expanded); resErr == nil {
		expanded = resolved
	} else if _, statErr := os.Stat(expanded); statErr == nil {
		return nil, fmt.Errorf("Backup destination already exists; choose a new path.")
	}
	if _, err := os.Stat(expanded); err == nil {
		return nil, fmt.Errorf("Backup destination already exists; choose a new path.")
	}
	home := s.Home
	if expanded == home {
		return nil, fmt.Errorf("Put backups outside the state directory.")
	}
	rel, relErr := filepath.Rel(home, expanded)
	if relErr == nil && rel != ".." && !filepath.IsAbs(rel) && rel != "." {
		if rel == "." || (len(rel) > 0 && rel[0] != '.') {
			if !filepath.IsAbs(rel) && (rel == "." || (rel != ".." && !startsWithDotDot(rel))) {
				if isInside(home, expanded) {
					return nil, fmt.Errorf("Put backups outside the state directory.")
				}
			}
		}
	}
	if isInside(home, expanded) {
		return nil, fmt.Errorf("Put backups outside the state directory.")
	}

	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()

	tasks, err := s.AllTasks()
	if err != nil {
		return nil, err
	}
	host, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	worktrees := make([]any, 0, len(tasks))
	for _, t := range tasks {
		row := ordjson.NewObject()
		id, _ := t.Get("id")
		row.Set("task", id)
		path, _ := t.Get("worktree")
		row.Set("path", path)
		branch, _ := t.Get("branch")
		row.Set("branch", branch)
		worktrees = append(worktrees, row)
	}
	graphObj := ordjson.NewObject()
	graphObj.Set("indexes_included", false)
	graphObj.Set("rebuild", graphBackupRows(s, tasks))
	graphObj.Set("note", "graph.json records travel; a .codegraph/ index is a regenerable cache inside the checkout, never a source backup")
	projectsObj := ordjson.NewObject()
	projectsObj.Set("registry_included", isFile(filepath.Join(home, project.ProjectsFile)))
	projectsObj.Set("clone_code_included", false)
	projectsObj.Set("note", "projects.json registrations travel; clone and worktree contents are the user's code-backup responsibility")
	manifest := ordjson.NewObject()
	manifest.Set("schema", jsonInt(store.Schema))
	manifest.Set("sum_version", contract.SumVersion)
	manifest.Set("created_at", store.Now())
	manifest.Set("scope", "records-only")
	manifest.Set("includes_worktree_code", false)
	manifest.Set("credential_files_included", false)
	manifest.Set("content_redaction", "none; task text may be sensitive")
	manifest.Set("machine", host)
	manifest.Set("worktrees_not_captured", worktrees)
	manifest.Set("brief_revisions_included", true)
	manifest.Set("environment_records_included", true)
	manifest.Set("environment_exclusions", "command references and URLs redacted at write; no process environments, credentials, log content, or checkout code")
	manifest.Set("settings_included", isFile(filepath.Join(home, settings.File)))
	manifest.Set("graph", graphObj)
	manifest.Set("managed_projects", projectsObj)
	manifest.Set("restore", "Extract into a new directory. Start sumctl with --home <extracted>/state. Do not reuse pane bindings on another machine; inspect and bind explicitly.")

	if err := os.MkdirAll(filepath.Dir(expanded), 0o700); err != nil {
		return nil, err
	}
	handle, err := os.OpenFile(expanded, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("Backup destination already exists; choose a new path.")
		}
		return nil, err
	}
	ok := false
	defer func() {
		handle.Close()
		if !ok {
			os.Remove(expanded)
		}
	}()
	gz := gzip.NewWriter(handle)
	tw := tar.NewWriter(gz)

	manifestBytes, err := ordjson.MarshalIndent(manifest)
	if err != nil {
		return nil, err
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := writeTarBytes(tw, "manifest.json", manifestBytes); err != nil {
		return nil, err
	}

	var paths []string
	for _, name := range []string{"state.json", settings.File, project.ProjectsFile, "preferences.md", "projects.md"} {
		paths = append(paths, filepath.Join(home, name))
	}
	sessionEntries, _ := filepath.Glob(filepath.Join(s.Sessions, "*.json"))
	sort.Strings(sessionEntries)
	paths = append(paths, sessionEntries...)
	contractDir := filepath.Join(home, "coordinator")
	if isFile(filepath.Join(contractDir, versions.File)) {
		paths = append(paths, filepath.Join(contractDir, versions.File))
		contractVersions, err := versions.ReadContractVersions(s)
		if err == nil {
			revisions, _ := contractVersions.Get("revisions")
			list, _ := revisions.([]any)
			for _, raw := range list {
				rev, _ := raw.(*ordjson.Object)
				pathValue, _ := rev.Get("path")
				relative, _ := pathValue.(string)
				if relative != "" {
					paths = append(paths, filepath.Join(contractDir, filepath.FromSlash(relative)))
				}
			}
		}
	}
	for _, task := range tasks {
		id := asString(func() any { v, _ := task.Get("id"); return v }())
		taskPath, err := s.TaskPath(id)
		if err != nil {
			return nil, err
		}
		paths = append(paths, filepath.Join(taskPath, "task.json"))
		if brief, ok := task.Get("brief_path"); ok && brief != nil {
			paths = append(paths, filepath.Join(taskPath, "brief.md"))
		}
		notesPath := filepath.Join(taskPath, notes.File)
		if isFile(notesPath) && !isSymlink(notesPath) {
			paths = append(paths, notesPath)
		}
		envPath := filepath.Join(taskPath, environment.File)
		if isFile(envPath) && !isSymlink(envPath) {
			paths = append(paths, envPath)
		}
		graphPath := filepath.Join(taskPath, graphview.GraphFile)
		if isFile(graphPath) && !isSymlink(graphPath) {
			paths = append(paths, graphPath)
		}
		sidecar := filepath.Join(taskPath, versions.File)
		if isFile(sidecar) || isSymlink(sidecar) {
			paths = append(paths, sidecar)
			vers, versErr := versions.ReadVersions(s, task)
			if versErr != nil {
				list, _ := manifest.Get("unreadable_version_metadata")
				items, _ := list.([]any)
				row := ordjson.NewObject()
				row.Set("task", id)
				row.Set("error", versErr.Error())
				manifest.Set("unreadable_version_metadata", append(items, row))
			} else {
				revisions, _ := vers.Get("revisions")
				list, _ := revisions.([]any)
				for _, raw := range list {
					rev, _ := raw.(*ordjson.Object)
					pathValue, _ := rev.Get("path")
					relative, _ := pathValue.(string)
					if relative != "" {
						paths = append(paths, filepath.Join(taskPath, filepath.FromSlash(relative)))
					}
				}
			}
		}
	}

	seen := map[string]bool{}
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		if isSymlink(path) {
			return nil, fmt.Errorf("Refusing symlink in state backup.")
		}
		if !isFile(path) {
			continue
		}
		rel, err := filepath.Rel(home, path)
		if err != nil {
			return nil, err
		}
		if err := writeTarFile(tw, filepath.ToSlash(filepath.Join("state", rel)), path); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	if err := handle.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(expanded, 0o600); err != nil {
		return nil, err
	}
	ok = true
	sum, err := sha256File(expanded)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("backup", expanded)
	result.Set("manifest", manifest)
	result.Set("sha256", sum)
	return result, nil
}

func startsWithDotDot(rel string) bool {
	return rel == ".." || len(rel) >= 3 && rel[:3] == ".."+string(os.PathSeparator)
}

func isInside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !startsWithDotDot(rel)
}

func writeTarBytes(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(data))}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func writeTarFile(tw *tar.Writer, name, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = name
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

func sha256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
