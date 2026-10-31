package roleinit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/procedure"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
	"os"
	"path/filepath"
	"strings"
)

// coordinatorCore validates the runtime's coordinator core before any coordinator role is granted or kept.
func coordinatorCore(runtimeRoot string) (*ordjson.Object, error) {
	return procedure.Describe(runtimeRoot, procedure.Coordinator, "no coordinator role was granted and nothing was recorded")
}

// roleProcedure is what init names for the returned role to read: the coordinator core or the developer
// procedure from the runtime, or a worker's pinned procedure from its task record. A developer or worker
// reference that fails validation is reported with `ok: false` rather than refusing the role, since those
// roles claim no coordination.
func roleProcedure(runtimeRoot string, s *store.Store, role string, task, coordinator *ordjson.Object) any {
	switch role {
	case "coordinator":
		if coordinator == nil {
			return nil
		}
		row := coordinator
		row.Set("ok", true)
		return []any{row}
	case "developer":
		return []any{procedure.Reference(runtimeRoot, procedure.Developer, "read it from a verified runtime before developing")}
	case "reviewer":
		row, err := reviewerProcedure(s, task)
		if err != nil {
			return []any{unreadableProcedure(err)}
		}
		return []any{row}
	case "worker":
		if s == nil || task == nil {
			return nil
		}
		versionsObj, err := versions.ReadVersions(s, task)
		if err != nil {
			return []any{unreadableProcedure(err)}
		}
		active := versions.ActiveRevision(versionsObj)
		if active == nil {
			return nil
		}
		policy, _ := active.Get("policy")
		policyObj, _ := policy.(*ordjson.Object)
		taskPath, err := s.TaskPath(asString(task, "id"))
		if err != nil {
			return []any{unreadableProcedure(err)}
		}
		refs := procedure.References(taskPath, procedure.Rows(policyObj))
		if refs == nil {
			return nil
		}
		return refs
	}
	return nil
}

// unreadableProcedure is the row a worker sees when its task record cannot say which procedure it pinned,
// so the failure is visible instead of reading like a brief without pinned files.
func unreadableProcedure(err error) *ordjson.Object {
	row := ordjson.NewObject()
	row.Set("ok", false)
	row.Set("error", "The task record naming your pinned worker procedure is unreadable ("+err.Error()+"); stop and report it rather than working from memory or another copy.")
	return row
}

func reviewerProcedure(s *store.Store, task *ordjson.Object) (*ordjson.Object, error) {
	value, _ := task.Get("reviewer")
	reviewer, _ := value.(*ordjson.Object)
	path := asString(reviewer, "skill_path")
	expected := asString(reviewer, "skill_sha256")
	directory, err := s.TaskPath(asString(task, "id"))
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(filepath.Join(directory, procedure.Dir), path)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." || filepath.IsAbs(relative) {
		return nil, fmt.Errorf("reviewer procedure is outside the task's sealed procedure directory")
	}
	for current := filepath.Dir(path); current != filepath.Join(directory, procedure.Dir); current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("reviewer procedure directory contains symlink")
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("reviewer procedure must be a regular nonsymlink file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	if len(data) == 0 || len(data) > procedure.MaxBytes || hex.EncodeToString(digest[:]) != expected {
		return nil, fmt.Errorf("reviewer procedure does not match its pinned digest")
	}
	row := ordjson.NewObject()
	row.Set("name", "sum-deliver")
	row.Set("path", path)
	row.Set("sha256", expected)
	row.Set("bytes", len(data))
	row.Set("load", procedure.Required)
	row.Set("ok", true)
	return row, nil
}
