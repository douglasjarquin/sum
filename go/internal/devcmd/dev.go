package devcmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/graph"
	"github.com/douglasjarquin/sum/go/internal/graphview"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/release"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,39}$`)

func Prepare(s *store.Store, ctx *ordjson.Object, runtimeRoot, name, base string, pane bool) (*ordjson.Object, error) {
	root, err := release.InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	if !namePattern.MatchString(name) {
		return nil, fmt.Errorf("Development name: lowercase letters, digits, dot, underscore, or dash; at most 40 characters.")
	}
	path := filepath.Join(root, ".sum", "dev", name)
	branch := "sum-dev/" + name
	existing, _ := developmentMarker(path)
	if _, statErr := os.Stat(path); statErr == nil && existing == nil {
		return nil, fmt.Errorf("%s exists but is not a sum development checkout. Inspect it; nothing was removed.", path)
	}
	reopened := existing != nil
	if existing == nil {
		shaOut, shaErr := runGit("-C", root, "rev-parse", "--verify", base+"^{commit}", "--")
		if shaErr != nil {
			return nil, shaErr
		}
		baseSHA := strings.TrimSpace(shaOut)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		hasBranch := runGitIgnoring("-C", root, "show-ref", "--verify", "--quiet", "refs/heads/"+branch) == 0
		if hasBranch {
			if _, err := runGit("-C", root, "worktree", "add", path, branch); err != nil {
				return nil, err
			}
			baseSHA = strings.TrimSpace(runGitOutput("-C", root, "rev-parse", branch))
		} else {
			if _, err := runGit("-C", root, "worktree", "add", "-b", branch, path, baseSHA); err != nil {
				return nil, err
			}
		}
		existing = ordjson.NewObject()
		existing.Set("schema", jsonInt(1))
		existing.Set("kind", "development")
		existing.Set("name", name)
		existing.Set("installation", root)
		existing.Set("installation_home", s.Home)
		existing.Set("branch", branch)
		existing.Set("base_sha", baseSHA)
		existing.Set("created_at", store.Now())
		existing.Set("panes", []any{})
		if err := os.MkdirAll(filepath.Join(path, ".sum"), 0o700); err != nil {
			return nil, err
		}
		if err := ordjson.WriteFile(filepath.Join(path, ".sum", "dev.json"), existing); err != nil {
			return nil, err
		}
	}
	graphRecord := graph.InitCheckout(s, runtimeRoot, path, "development", objectField(existing, "graph"))
	existing.Set("graph", graphview.Summary(graphRecord))
	if err := ordjson.WriteFile(filepath.Join(path, ".sum", "dev.json"), existing); err != nil {
		return nil, err
	}
	var paneView any
	if pane {
		if ctx == nil {
			return nil, fmt.Errorf("Herdr pane context is required for --pane.")
		}
		herdrPath, findErr := toolpath.Find(runtimeRoot, "herdr")
		if findErr != nil {
			return nil, findErr
		}
		session := asString(ctx, "session")
		created, callErr := herdrclient.Call(herdrPath, session, 30*time.Second, "workspace", "create", "--cwd", path, "--label", "sum-dev-"+name, "--no-focus")
		if callErr != nil {
			return nil, callErr
		}
		createdObj, _ := created.(*ordjson.Object)
		rootPane := objectField(createdObj, "root_pane")
		workspace := objectField(createdObj, "workspace")
		paneObj := ordjson.NewObject()
		paneObj.Set("pane", asString(rootPane, "pane_id"))
		paneObj.Set("workspace", asString(workspace, "workspace_id"))
		paneObj.Set("session", session)
		host, _ := os.Hostname()
		paneObj.Set("machine", host)
		paneObj.Set("at", store.Now())
		panes := listField(existing, "panes")
		existing.Set("panes", append(panes, paneObj))
		if err := ordjson.WriteFile(filepath.Join(path, ".sum", "dev.json"), existing); err != nil {
			return nil, err
		}
		paneView = paneObj
	}
	result := ordjson.NewObject()
	result.Set("name", name)
	result.Set("path", path)
	result.Set("branch", asString(existing, "branch"))
	result.Set("base_sha", asString(existing, "base_sha"))
	result.Set("installation", root)
	result.Set("reopened", reopened)
	result.Set("role", "developer")
	result.Set("pane", paneView)
	result.Set("graph", graphview.Summary(graphRecord))
	result.Set("note", "Development checkout: modify and test sum here only. No coordinator initialization, dispatch, production setup, or instance-wide updates.")
	return result, nil
}

func List(s *store.Store) (*ordjson.Object, error) {
	root, err := release.InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	var rows []any
	matches, _ := filepath.Glob(filepath.Join(root, ".sum", "dev", "*", ".sum", "dev.json"))
	for _, marker := range matches {
		path := filepath.Dir(filepath.Dir(marker))
		value, readErr := developmentMarker(path)
		if readErr != nil || value == nil {
			row := ordjson.NewObject()
			row.Set("path", path)
			if readErr != nil {
				row.Set("error", readErr.Error())
			}
			rows = append(rows, row)
			continue
		}
		row := ordjson.NewObject()
		row.Set("name", asString(value, "name"))
		row.Set("path", path)
		row.Set("branch", asString(value, "branch"))
		row.Set("panes", listField(value, "panes"))
		rows = append(rows, row)
	}
	result := ordjson.NewObject()
	result.Set("installation", root)
	result.Set("checkouts", rows)
	return result, nil
}

func Remove(s *store.Store, name string) (*ordjson.Object, error) {
	root, err := release.InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	if !namePattern.MatchString(name) {
		return nil, fmt.Errorf("Invalid development name.")
	}
	path := filepath.Join(root, ".sum", "dev", name)
	marker, _ := developmentMarker(path)
	if marker == nil {
		return nil, fmt.Errorf("%s is not a sum development checkout; nothing was removed.", path)
	}
	statusOut := runGitOutput("-C", path, "status", "--porcelain", "--untracked-files=all")
	if strings.TrimSpace(statusOut) != "" {
		return nil, fmt.Errorf("%s has uncommitted or untracked work; commit, stash, or move it yourself. Nothing was removed.", path)
	}
	if _, err := runGit("-C", root, "worktree", "remove", path); err != nil {
		return nil, err
	}
	branch := asString(marker, "branch")
	branchRemoved := runGitIgnoring("-C", root, "branch", "-d", branch) == 0
	note := "Clean checkout and merged branch removed."
	if !branchRemoved {
		note = "Branch kept because it has unmerged commits; delete it yourself after merging."
	}
	result := ordjson.NewObject()
	result.Set("removed", path)
	result.Set("branch", branch)
	result.Set("branch_removed", branchRemoved)
	result.Set("note", note)
	return result, nil
}

func developmentMarker(path string) (*ordjson.Object, error) {
	marker := filepath.Join(path, ".sum", "dev.json")
	info, err := os.Stat(marker)
	if err != nil || info.IsDir() {
		return nil, nil
	}
	value, err := ordjson.ReadFile(marker)
	if err != nil {
		return nil, err
	}
	obj, _ := value.(*ordjson.Object)
	if obj == nil || asString(obj, "kind") != "development" {
		return nil, fmt.Errorf("%s is not a development marker", marker)
	}
	return obj, nil
}

func jsonInt(n int) json.Number { return json.Number(fmt.Sprint(n)) }

func asString(o *ordjson.Object, key string) string {
	if o == nil {
		return ""
	}
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}

func objectField(o *ordjson.Object, key string) *ordjson.Object {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	obj, _ := v.(*ordjson.Object)
	return obj
}

func listField(o *ordjson.Object, key string) []any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	list, _ := v.([]any)
	return list
}

func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func runGitOutput(args ...string) string {
	cmd := exec.Command("git", args...)
	out, _ := cmd.Output()
	return string(out)
}

func runGitIgnoring(args ...string) int {
	cmd := exec.Command("git", args...)
	_ = cmd.Run()
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	return 1
}
