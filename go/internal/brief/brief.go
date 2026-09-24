package brief

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/procedure"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

func sha256Text(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// policyFor is a revision's recorded policy: the runtime contract plus the pinned procedure rows.
// worker_skill_sha256 stays for readers that predate the rows and for change summaries.
func policyFor(rows []any) *ordjson.Object {
	policy := ordjson.NewObject()
	policy.Set("sum_version", contract.SumVersion)
	policy.Set("brief_schema", jsonInt(contract.BriefSchema))
	policy.Set("worker_skill_sha256", procedure.SHA(rows, "sum-worker"))
	policy.Set("procedure", rows)
	return policy
}

func Commands(sumctlPath, home, taskID string) *ordjson.Object {
	cmds := ordjson.NewObject()
	cmds.Set("ask", shquote.CommandFor(sumctlPath, home, "ask", taskID, "--key", "short-question-name", "--text", "Your exact question and recommendation"))
	cmds.Set("show", shquote.CommandFor(sumctlPath, home, "show", taskID))
	cmds.Set("resolve", shquote.CommandFor(sumctlPath, home, "resolve", taskID, "QUESTION_ID"))
	cmds.Set("report", shquote.CommandFor(sumctlPath, home, "report", taskID, "--file", "/absolute/path/to/report.md"))
	cmds.Set("brief", shquote.CommandFor(sumctlPath, home, "brief", "list", taskID))
	cmds.Set("context", shquote.CommandFor(sumctlPath, home, "context", taskID, "--role", "worker"))
	return cmds
}

func writeOnce(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("Refusing to overwrite %s; a brief a worker may be reading is never rewritten.", path)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".write-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(text); err != nil {
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
	if err := os.Link(tmpPath, path); err != nil {
		return err
	}
	return nil
}

func WriteInitial(s *store.Store, runtimeRoot, sumctlPath string, task *ordjson.Object) (string, error) {
	id := asString(func() any { v, _ := task.Get("id"); return v }())
	taskPath, err := s.TaskPath(id)
	if err != nil {
		return "", err
	}
	rows, err := procedure.Pin(runtimeRoot, taskPath)
	if err != nil {
		return "", err
	}
	policy := policyFor(rows)
	commands := Commands(sumctlPath, s.Home, id)
	text := render(s, sumctlPath, briefData{Task: task, TaskDir: taskPath, Revision: "r1", Policy: policy, Commands: commands})
	path := filepath.Join(taskPath, "brief.md")
	if err := writeOnce(path, text); err != nil {
		return "", err
	}
	approved := versions.ApprovedFingerprint(task)
	approved.Set("recorded_at", store.Now())
	runtime := ordjson.NewObject()
	runtime.Set("sum_version", contract.SumVersion)
	runtime.Set("brief_schema", jsonInt(contract.BriefSchema))
	runtime.Set("recorded_at", store.Now())
	rev := ordjson.NewObject()
	rev.Set("id", "r1")
	rev.Set("path", "brief.md")
	rev.Set("status", "active")
	rev.Set("created_at", store.Now())
	rev.Set("sha256", sha256Text(text))
	rev.Set("policy", policy)
	rev.Set("decisions", []any{})
	rev.Set("commands", commands)
	rev.Set("approved", versions.ApprovedFingerprint(task))
	rev.Set("summary", []any{"initial brief"})
	rev.Set("verification_affected", false)
	sidecar := ordjson.NewObject()
	sidecar.Set("schema", jsonInt(versions.Schema))
	sidecar.Set("task", id)
	sidecar.Set("legacy", false)
	sidecar.Set("runtime", runtime)
	sidecar.Set("brief_schema", jsonInt(contract.BriefSchema))
	sidecar.Set("approved", approved)
	sidecar.Set("revisions", []any{rev})
	sidecar.Set("active", "r1")
	sidecar.Set("requested", nil)
	sidecar.Set("refresh", []any{})
	if err := ordjson.WriteFile(filepath.Join(taskPath, versions.File), sidecar); err != nil {
		return "", err
	}
	return path, nil
}
