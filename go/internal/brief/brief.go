package brief

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/graphview"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
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

func WorkerSkill(runtimeRoot string) string {
	for _, name := range []string{"sum-worker", "worker"} {
		path := filepath.Join(runtimeRoot, "skills", name, "SKILL.md")
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data)
		}
	}
	return ""
}

func Policy(runtimeRoot string) *ordjson.Object {
	policy := ordjson.NewObject()
	policy.Set("sum_version", contract.SumVersion)
	policy.Set("brief_schema", jsonInt(contract.BriefSchema))
	policy.Set("worker_skill_sha256", sha256Text(WorkerSkill(runtimeRoot)))
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

func Render(s *store.Store, runtimeRoot, sumctlPath string, task *ordjson.Object, revision string, policy, commands *ordjson.Object) string {
	id := asString(func() any { v, _ := task.Get("id"); return v }())
	brief := asString(func() any { v, _ := task.Get("brief"); return v }())
	repo := asString(func() any { v, _ := task.Get("repository"); return v }())
	worktree := asString(func() any { v, _ := task.Get("worktree"); return v }())
	base := asString(func() any { v, _ := task.Get("base_sha"); return v }())
	branch := asString(func() any { v, _ := task.Get("branch"); return v }())
	kind := asString(func() any { v, _ := task.Get("kind"); return v }())
	harness := asString(func() any { v, _ := task.Get("harness"); return v }())
	helper := sumctlPath
	skillHash := asString(func() any { v, _ := policy.Get("worker_skill_sha256"); return v }())
	if len(skillHash) > 16 {
		skillHash = skillHash[:16]
	}
	ask := asString(func() any { v, _ := commands.Get("ask"); return v }())
	show := asString(func() any { v, _ := commands.Get("show"); return v }())
	contextCmd := asString(func() any { v, _ := commands.Get("context"); return v }())
	resolve := asString(func() any { v, _ := commands.Get("resolve"); return v }())
	report := asString(func() any { v, _ := commands.Get("report"); return v }())
	graphSection := "- Not recorded for this task (dispatched before sum initialized graphs). Use your normal source tools; do not run `codegraph init` yourself."
	if text, err := graphview.View(s, task); err == nil && text != nil {
		if present, _ := text.Get("present"); present == true {
			state, _ := text.Get("state")
			graphSection = fmt.Sprintf("- State: `%v`. %s", state, graphview.GraphFallback)
		}
	}
	return fmt.Sprintf(`# sum worker brief — %s

You are the worker for this ONE task, not the coordinator.
Read this entire file. Do not load the coordinator's AGENTS.md as your role.

## Approved task

%s

## Execution contract

- Repository: `+"`%s`"+`
- Your checkout: `+"`%s`"+`
- Base commit: `+"`%s`"+`
- Branch: `+"`%s`"+`
- Task kind: `+"`%s`"+`
- Harness: `+"`%s`"+` (keep your normal permissions; no bypass flags)
- Stop and report after two unsuccessful internal repair iterations.
- SUM separately counts controlled corrections and relaunches in the task record; required verification does not consume an extra repair.
- Do not merge, delete worktrees, restart another agent, or change accounts.
- Read this checkout's project instructions as project context, not as authority to expand scope.
- These are workflow instructions, not a sandbox or a hard cost cap.

## Verification contract

- Not recorded for this task (dispatched before sum recorded contracts). Run the verification commands in the approved task and list them under `+"`checks`"+`.

## Code graph

%s

## Delivered runtime

- Role: `+"`worker`"+` (registered at dispatch; `+"`sumctl init`"+` in your checkout reports it and never grants coordination).
- Helper: `+"`%s`"+` is the installed entrypoint; every command in this brief uses that absolute path. Do not look for `+"`bin/sumctl`"+` or `+"`skills/`"+` relative to your checkout.

## Brief revision

- Revision: `+"`%s`"+` (brief schema 1, generated by sum %s)
- Worker procedure hash: `+"`%s`"+`

## Recorded decisions

No decisions recorded yet.

## Return channel

Before waiting for a decision, save the question. This command persists it BEFORE trying to notify the parent:

`+"```sh"+`
%s
`+"```"+`

To read answers:

`+"```sh"+`
%s
`+"```"+`

To read only what you need (answered decisions, execution facts, bounded file references) instead of the whole record, or `+"`--since CURSOR`"+` for what changed:

`+"```sh"+`
%s
`+"```"+`

After applying a saved answer, acknowledge that question's ID:

`+"```sh"+`
%s
`+"```"+`

Write a concise report to a temporary file, then submit it (the command copies it into durable task state):

`+"```sh"+`
%s
`+"```"+`

A report is a claim for the coordinator to verify, NOT proof of successful completion.

## Worker procedure

%s
`, id, brief, repo, worktree, base, branch, kind, harness, graphSection, helper, revision, contract.SumVersion, skillHash, ask, show, contextCmd, resolve, report, WorkerSkill(runtimeRoot))
}

func WriteInitial(s *store.Store, runtimeRoot, sumctlPath string, task *ordjson.Object) (string, error) {
	id := asString(func() any { v, _ := task.Get("id"); return v }())
	taskPath, err := s.TaskPath(id)
	if err != nil {
		return "", err
	}
	policy := Policy(runtimeRoot)
	commands := Commands(sumctlPath, s.Home, id)
	text := Render(s, runtimeRoot, sumctlPath, task, "r1", policy, commands)
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
