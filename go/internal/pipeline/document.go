package pipeline

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	auditScript  = ".agents/skills/maintain-verification/scripts/verify_audit.py"
	contractFile = "VERIFY.md"
	auditBound   = 10 * time.Minute
	documentDir  = "document"
	pipelineDir  = "pipeline"
)

// placeholderOnly is the one finding kind that does not fail the gate: a scaffold TODO is an unfinished map, not a wrong one.
const placeholderOnly = "placeholder"

type DocumentArgs struct {
	Task      string
	Candidate string
}

type audit struct {
	AuditID       string `json:"audit_id"`
	Outcome       string `json:"outcome"`
	BlockedReason string `json:"blocked_reason"`
	Findings      []struct {
		Kind   string `json:"kind"`
		File   string `json:"file"`
		Detail string `json:"detail"`
	} `json:"findings"`
}

// Document audits the candidate's own VERIFY.md and feature maps in a throwaway checkout of that SHA, and records the result.
// The project keeps owning its contract: the audit reads it, and never edits it.
func Document(s *store.Store, ctx *ordjson.Object, runtimeRoot string, args DocumentArgs) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	candidate := args.Candidate
	if candidate == "" {
		candidate = Candidate(task)
	}
	if !sha40.MatchString(candidate) {
		return nil, fmt.Errorf("this task records no candidate to audit; pass --candidate with the full 40-hex commit SHA")
	}
	worktree := stringField(task, "worktree")
	if info, statErr := os.Stat(worktree); worktree == "" || statErr != nil || !info.IsDir() {
		return nil, fmt.Errorf("The task checkout is gone; nothing can be audited against the candidate from here.")
	}
	script := filepath.Join(runtimeRoot, filepath.FromSlash(auditScript))
	if info, statErr := os.Stat(script); statErr != nil || info.IsDir() {
		return nil, fmt.Errorf("this runtime carries no %s; stage a release that does before auditing documentation", auditScript)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		return nil, fmt.Errorf("python3 is not on PATH; cannot run %s", auditScript)
	}
	taskDir, err := s.TaskPath(args.Task)
	if err != nil {
		return nil, err
	}
	stamp, err := documentStamp()
	if err != nil {
		return nil, err
	}
	runDir := filepath.Join(taskDir, pipelineDir, documentDir, stamp)
	checkout := filepath.Join(runDir, "checkout")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return nil, err
	}
	if out, addErr := git(worktree, "worktree", "add", "--detach", checkout, candidate); addErr != nil {
		return nil, fmt.Errorf("could not check out %s to audit it: %s", candidate, out)
	}
	defer removeCheckout(worktree, checkout)

	result, summary := runAudit(python, script, checkout, stringField(task, "base_sha"), filepath.Join(runDir, "audit.json"))
	return recordDocumentation(s, ctx, args.Task, candidate, result, summary)
}

func runAudit(python, script, checkout, base, recordPath string) (*ordjson.Object, string) {
	body := ordjson.NewObject()
	if info, err := os.Stat(filepath.Join(checkout, contractFile)); err != nil || info.IsDir() {
		body.Set("result", "skipped")
		body.Set("audit_id", nil)
		body.Set("record", nil)
		body.Set("findings", ordjson.NewObject())
		return body, "Project declares no VERIFY.md"
	}
	argv := []string{script, "--root", checkout, "--json", "--no-record"}
	if sha40.MatchString(base) {
		argv = append(argv, "--base", base)
	}
	runCtx, cancel := context.WithTimeout(context.Background(), auditBound)
	defer cancel()
	cmd := exec.CommandContext(runCtx, python, argv...)
	cmd.Dir = checkout
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		body.Set("result", "fail")
		body.Set("audit_id", nil)
		body.Set("record", nil)
		body.Set("findings", ordjson.NewObject())
		return body, "The documentation audit did not finish in time"
	}
	var parsed audit
	if err := json.Unmarshal([]byte(stdout.String()), &parsed); err != nil || parsed.AuditID == "" {
		detail := firstLine(stderr.String())
		if detail == "" {
			detail = reasonOf(runErr, "the audit printed no JSON record")
		}
		body.Set("result", "fail")
		body.Set("audit_id", nil)
		body.Set("record", nil)
		body.Set("findings", ordjson.NewObject())
		return body, "The documentation audit did not report a record: " + detail
	}
	if err := os.WriteFile(recordPath, []byte(stdout.String()), 0o600); err != nil {
		recordPath = ""
	}
	counts := map[string]int{}
	for _, finding := range parsed.Findings {
		counts[finding.Kind]++
	}
	body.Set("result", auditResult(parsed, counts))
	body.Set("audit_id", parsed.AuditID)
	body.Set("record", nilIfEmpty(recordPath))
	body.Set("outcome", parsed.Outcome)
	body.Set("blocked_reason", nilIfEmpty(parsed.BlockedReason))
	body.Set("findings", countsObject(counts))
	return body, auditSummary(parsed, counts)
}

func auditResult(parsed audit, counts map[string]int) string {
	if parsed.Outcome == "blocked" {
		return "fail"
	}
	for kind := range counts {
		if kind != placeholderOnly {
			return "fail"
		}
	}
	return "pass"
}

func auditSummary(parsed audit, counts map[string]int) string {
	if parsed.Outcome == "blocked" {
		return "The documentation audit was blocked: " + parsed.BlockedReason
	}
	if auditResult(parsed, counts) == "pass" {
		if placeholders := counts[placeholderOnly]; placeholders > 0 {
			return fmt.Sprintf("Passed with %s still in the maps", plural(placeholders, "placeholder", "placeholders"))
		}
		return "Passed"
	}
	return "Failed: " + strings.Join(countsText(counts), ", ")
}

func countsText(counts map[string]int) []string {
	kinds := make([]string, 0, len(counts))
	for kind := range counts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	out := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		out = append(out, fmt.Sprintf("%d %s", counts[kind], kind))
	}
	return out
}

func countsObject(counts map[string]int) *ordjson.Object {
	kinds := make([]string, 0, len(counts))
	for kind := range counts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	out := ordjson.NewObject()
	for _, kind := range kinds {
		out.Set(kind, json.Number(fmt.Sprint(counts[kind])))
	}
	return out
}

func recordDocumentation(s *store.Store, ctx *ordjson.Object, taskID, candidate string, body *ordjson.Object, summary string) (*ordjson.Object, error) {
	body.Set("summary", summary)
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	appended, err := evidence.Append(task, "documentation", "coordinator", body, candidate, ctx)
	if err != nil {
		return nil, err
	}
	note := RefreshNote(s, task)
	if err := s.SaveTask(task); err != nil {
		return nil, err
	}
	record, err := Load(s, taskID)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("evidence", appended)
	result.Set("pipeline", View(record))
	result.Set("pipeline_note", note)
	return result, nil
}

func git(worktree string, args ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", append([]string{"-C", worktree}, args...)...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// An unremovable checkout stays a named blocker for a person: sum never forces a removal.
func removeCheckout(worktree, checkout string) {
	if out, err := git(worktree, "worktree", "remove", checkout); err != nil {
		note := fmt.Sprintf("git worktree remove failed: %s\nThe documentation audit checkout was left in place; inspect it, then remove it with `git worktree remove`.\n", out)
		_ = os.WriteFile(filepath.Join(filepath.Dir(checkout), "checkout-not-removed.txt"), []byte(note), 0o600)
	}
}

func documentStamp() (string, error) {
	buf := make([]byte, 3)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(buf), nil
}
