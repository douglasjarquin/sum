package prcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

type Outcome string

const (
	Published Outcome = "published"
	Unchanged Outcome = "unchanged"
	Deferred  Outcome = "deferred"
	Refused   Outcome = "refused"
	Failed    Outcome = "failed"
	Uncertain Outcome = "uncertain"
	Skipped   Outcome = "skipped"
)

type Destination struct {
	Repository string
	Number     int
	HeadSHA    string
	State      string
}

type Run struct {
	ID   string
	Root string
}

type Publication struct {
	Run         string
	Outcome     Outcome
	Reason      string
	Scenarios   []string
	Attachments int
	ResultPath  string
}

type PublishArgs struct {
	Task                string
	Run                 string
	Visibility          string
	Scenarios           []string
	EvidenceRoot        string
	VerificationRuns    []string
	Timeout             int
	DryRun              bool
	AllowHeadMismatch   bool
	ReplaceForeignBlock bool
	Trigger             string
}

const (
	publisherScript = ".agents/skills/evidence/scripts/evidence_publish.py"
	planBound       = 5 * time.Minute
	publishBound    = 30 * time.Minute
)

var headMismatch = regexp.MustCompile(`^PR head [0-9a-f]{40} is not the recorded candidate`)

// Publish runs the runtime's publisher once per discovered evidence run and appends one publication record for each.
func Publish(s *store.Store, ctx *ordjson.Object, runtimeRoot string, args PublishArgs) ([]Publication, error) {
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	dest, err := destination(task)
	if err != nil {
		return nil, err
	}
	runs, err := discoverRuns(task, args)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, nil
	}
	publications := make([]Publication, 0, len(runs))
	if blocked := precondition(dest, task, args); blocked != nil {
		for _, run := range runs {
			publications = append(publications, Publication{Run: run.ID, Outcome: blocked.outcome, Reason: blocked.reason})
		}
		return publications, record(s, ctx, args, dest, publications)
	}
	python, err := toolpath.Find(runtimeRoot, "python3")
	if err != nil {
		return nil, err
	}
	gh, err := toolpath.Find(runtimeRoot, "gh")
	if err != nil {
		return nil, err
	}
	publisher := filepath.Join(runtimeRoot, filepath.FromSlash(publisherScript))
	if info, statErr := os.Stat(publisher); statErr != nil || info.IsDir() {
		return nil, fmt.Errorf("this runtime carries no %s; stage a release that does before publishing evidence", publisherScript)
	}
	if reason := attachUnavailable(python, publisher, gh); reason != "" {
		for _, run := range runs {
			publications = append(publications, Publication{Run: run.ID, Outcome: Deferred, Reason: reason})
		}
		return publications, record(s, ctx, args, dest, publications)
	}
	visibility := args.Visibility
	if visibility == "" {
		visibility, err = observedVisibility(gh, asString(task, "repository"), dest.Repository)
		if err != nil {
			return nil, err
		}
	}
	taskDir, err := s.TaskPath(args.Task)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		publications = append(publications, publishRun(python, publisher, gh, taskDir, task, dest, run, visibility, args))
	}
	return publications, record(s, ctx, args, dest, publications)
}

type block struct {
	outcome Outcome
	reason  string
}

func precondition(dest Destination, task *ordjson.Object, args PublishArgs) *block {
	if dest.State != "open" {
		return &block{Skipped, fmt.Sprintf("PR #%d is %s; evidence is published only into an open PR", dest.Number, dest.State)}
	}
	if args.AllowHeadMismatch {
		return nil
	}
	pr := asObject(func() any { v, _ := task.Get("pr"); return v }())
	for _, raw := range asList(func() any { v, _ := pr.Get("findings"); return v }()) {
		finding, _ := raw.(string)
		if headMismatch.MatchString(finding) {
			return &block{Refused, finding + "; reconcile the pushed candidate or pass --allow-head-mismatch"}
		}
	}
	return nil
}

func destination(task *ordjson.Object) (Destination, error) {
	pr := asObject(func() any { v, _ := task.Get("pr"); return v }())
	identity := asObject(func() any { v, _ := pr.Get("identity"); return v }())
	if identity == nil {
		return Destination{}, fmt.Errorf("pr evidence requires a reconciled PR and a captured evidence run; record the PR with `pr reconcile` first.")
	}
	number := intOf(func() any { v, _ := identity.Get("number"); return v }())
	head := asString(identity, "head_sha")
	if number == 0 || !sha40.MatchString(head) {
		return Destination{}, fmt.Errorf("the recorded PR identity has no number and head SHA to publish into; run `pr reconcile` again.")
	}
	repo := repositoryFromURL(asString(identity, "url"))
	if !repoName.MatchString(repo) {
		return Destination{}, fmt.Errorf("the recorded PR URL does not name an owner/repo destination; run `pr reconcile` again.")
	}
	return Destination{Repository: repo, Number: number, HeadSHA: head, State: asString(pr, "state")}, nil
}

func repositoryFromURL(url string) string {
	parts := strings.Split(strings.TrimPrefix(url, "https://"), "/")
	if len(parts) < 3 {
		return ""
	}
	return parts[1] + "/" + parts[2]
}

func discoverRuns(task *ordjson.Object, args PublishArgs) ([]Run, error) {
	worktree := asString(task, "worktree")
	roots := []string{worktree, args.EvidenceRoot}
	byID := map[string]*Run{}
	var order []string
	for _, raw := range asList(func() any { v, _ := task.Get("evidence"); return v }()) {
		record := asObject(raw)
		if asString(record, "kind") != "handoff" {
			continue
		}
		handoff := asObject(func() any { v, _ := record.Get("handoff"); return v }())
		if handoff == nil {
			continue
		}
		for _, item := range asList(func() any { v, _ := handoff.Get("artifacts"); return v }()) {
			path, _ := item.(string)
			if filepath.Base(path) != "comparison.json" {
				continue
			}
			resolved := path
			if !filepath.IsAbs(resolved) && worktree != "" {
				resolved = filepath.Join(worktree, resolved)
			}
			resolved = filepath.Clean(resolved)
			if !containedIn(resolved, roots) {
				return nil, fmt.Errorf("handoff artifact %s is outside the task checkout and the evidence root; publish it from a promoted copy with --evidence-root", path)
			}
			runDir := filepath.Dir(filepath.Dir(resolved))
			id := filepath.Base(runDir)
			if _, seen := byID[id]; !seen {
				byID[id] = &Run{ID: id, Root: filepath.Dir(runDir)}
				order = append(order, id)
			}
		}
	}
	sort.Strings(order)
	runs := make([]Run, 0, len(order))
	for _, id := range order {
		run := *byID[id]
		if args.EvidenceRoot != "" {
			run.Root = args.EvidenceRoot
		}
		if args.Run != "" && args.Run != id {
			continue
		}
		runs = append(runs, run)
	}
	if len(runs) == 0 && args.Run != "" {
		if args.EvidenceRoot == "" {
			return nil, fmt.Errorf("run %s is not in this task's handoff artifacts; name a run the worker recorded, or point --evidence-root at a promoted copy", args.Run)
		}
		if info, err := os.Stat(filepath.Join(args.EvidenceRoot, args.Run)); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("run %s is not under the evidence root %s", args.Run, args.EvidenceRoot)
		}
		runs = append(runs, Run{ID: args.Run, Root: args.EvidenceRoot})
	}
	return runs, nil
}

func containedIn(path string, roots []string) bool {
	for _, root := range roots {
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(filepath.Clean(root), path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func attachUnavailable(python, publisher, gh string) string {
	runCtx, cancel := context.WithTimeout(context.Background(), planBound)
	defer cancel()
	cmd := exec.CommandContext(runCtx, python, publisher, "capabilities", "--gh", gh, "--json")
	out, _ := cmd.Output()
	var caps struct {
		Attach bool   `json:"attach"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(out, &caps) != nil {
		return "the runtime's gh could not be inspected for --attach support; nothing was uploaded"
	}
	if caps.Attach {
		return ""
	}
	if caps.Reason == "" {
		return "the runtime's gh cannot attach media"
	}
	return caps.Reason
}

func observedVisibility(gh, repoDir, remote string) (string, error) {
	res, err := proc.Run([]string{gh, "repo", "view", remote, "--json", "visibility"}, repoDir, ghBound, true, nil)
	if err != nil {
		return "", fmt.Errorf("gh could not report the visibility of %s; pass --visibility yourself after checking it", remote)
	}
	var payload struct {
		Visibility string `json:"visibility"`
	}
	if json.Unmarshal([]byte(res.Stdout), &payload) != nil || payload.Visibility == "" {
		return "", fmt.Errorf("gh did not report a visibility for %s; pass --visibility yourself after checking it", remote)
	}
	return strings.ToLower(payload.Visibility), nil
}

func publishRun(python, publisher, gh, taskDir string, task *ordjson.Object, dest Destination, run Run, visibility string, args PublishArgs) Publication {
	publishDir := filepath.Join(taskDir, "publish", run.ID)
	planPath := filepath.Join(publishDir, "plan.json")
	planArgs := []string{publisher, "plan", "--run", run.ID, "--repo", dest.Repository, "--pr", fmt.Sprint(dest.Number),
		"--candidate", dest.HeadSHA, "--evidence-root", run.Root, "--publish-dir", publishDir}
	if base := asString(task, "base_sha"); sha40.MatchString(base) {
		planArgs = append(planArgs, "--base", base)
	}
	for _, id := range verificationRuns(task, args) {
		planArgs = append(planArgs, "--verification-run", id)
	}
	for _, scenario := range args.Scenarios {
		planArgs = append(planArgs, "--scenario", scenario)
	}
	if _, stderr, code, err := runPublisher(python, planArgs, publishDir, planBound); err != nil || code != 0 {
		return Publication{Run: run.ID, Outcome: outcomeForExit(code, err), Reason: firstLine(stderr, "the publish plan did not finish")}
	}
	scenarios := plannedScenarios(planPath)
	before := resultFiles(publishDir)
	publishArgs := []string{publisher, "publish", "--plan", planPath, "--receipts", filepath.Join(taskDir, "publish", "receipts.json"),
		"--visibility", visibility, "--gh", gh}
	if args.Timeout > 0 {
		publishArgs = append(publishArgs, "--timeout", fmt.Sprint(args.Timeout))
	}
	if args.DryRun {
		publishArgs = append(publishArgs, "--dry-run")
	}
	if args.AllowHeadMismatch {
		publishArgs = append(publishArgs, "--allow-head-mismatch")
	}
	if args.ReplaceForeignBlock {
		publishArgs = append(publishArgs, "--replace-foreign-block")
	}
	_, stderr, code, err := runPublisher(python, publishArgs, publishDir, publishBound)
	publication := Publication{Run: run.ID, Scenarios: scenarios}
	result := newestResult(publishDir, before)
	if result == "" {
		publication.Outcome = outcomeForExit(code, err)
		publication.Reason = firstLine(stderr, "the publisher wrote no result record")
		return publication
	}
	publication.ResultPath = result
	outcome, reason, uploaded := readResult(result)
	publication.Outcome = outcome
	publication.Reason = reason
	publication.Attachments = uploaded
	return publication
}

func runPublisher(python string, argv []string, dir string, bound time.Duration) (stdout, stderr string, code int, err error) {
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return "", "", 0, mkErr
	}
	runCtx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	cmd := exec.CommandContext(runCtx, python, argv...)
	cmd.Dir = dir
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		return out.String(), errBuf.String(), 4, nil
	}
	if runErr != nil {
		exit, isExit := runErr.(*exec.ExitError)
		if !isExit {
			return out.String(), errBuf.String(), 0, runErr
		}
		return out.String(), errBuf.String(), exit.ExitCode(), nil
	}
	return out.String(), errBuf.String(), 0, nil
}

func outcomeForExit(code int, err error) Outcome {
	if err != nil {
		return Failed
	}
	switch code {
	case 1:
		return Refused
	case 2:
		return Deferred
	case 4:
		return Uncertain
	}
	return Failed
}

func firstLine(text, fallback string) string {
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return fallback
}

func verificationRuns(task *ordjson.Object, args PublishArgs) []string {
	if len(args.VerificationRuns) > 0 {
		return args.VerificationRuns
	}
	seen := map[string]bool{}
	var ids []string
	for _, raw := range asList(func() any { v, _ := task.Get("evidence"); return v }()) {
		record := asObject(raw)
		if asString(record, "kind") != "verification" {
			continue
		}
		id := asString(record, "run_id")
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

func plannedScenarios(planPath string) []string {
	data, err := os.ReadFile(planPath)
	if err != nil {
		return nil
	}
	var plan struct {
		Publishable []string `json:"publishable_scenarios"`
	}
	if json.Unmarshal(data, &plan) != nil {
		return nil
	}
	return plan.Publishable
}

func resultFiles(publishDir string) map[string]bool {
	seen := map[string]bool{}
	matches, _ := filepath.Glob(filepath.Join(publishDir, "results", "*.json"))
	for _, m := range matches {
		seen[m] = true
	}
	return seen
}

func newestResult(publishDir string, before map[string]bool) string {
	matches, _ := filepath.Glob(filepath.Join(publishDir, "results", "*.json"))
	var fresh []string
	for _, m := range matches {
		if !before[m] {
			fresh = append(fresh, m)
		}
	}
	if len(fresh) == 0 {
		return ""
	}
	sort.Strings(fresh)
	return fresh[len(fresh)-1]
}

func readResult(path string) (Outcome, string, int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Uncertain, "the publisher's result record could not be read", 0
	}
	var result struct {
		Outcome  string `json:"outcome"`
		Reason   string `json:"reason"`
		Uploaded []any  `json:"uploaded"`
	}
	if json.Unmarshal(data, &result) != nil || result.Outcome == "" {
		return Uncertain, "the publisher's result record is not readable JSON", 0
	}
	return Outcome(result.Outcome), result.Reason, len(result.Uploaded)
}

func record(s *store.Store, ctx *ordjson.Object, args PublishArgs, dest Destination, publications []Publication) error {
	if len(publications) == 0 {
		return nil
	}
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return err
	}
	for i, publication := range publications {
		body := ordjson.NewObject()
		body.Set("run", publication.Run)
		body.Set("outcome", string(publication.Outcome))
		body.Set("reason", nilIfEmpty(publication.Reason))
		body.Set("destination", destinationObject(dest))
		body.Set("scenarios", anyStrings(publication.Scenarios))
		body.Set("attachments", json.Number(fmt.Sprint(publication.Attachments)))
		body.Set("result_path", nilIfEmpty(publication.ResultPath))
		body.Set("trigger", args.Trigger)
		body.Set("dry_run", args.DryRun)
		if _, err := evidence.Append(task, "publication", "coordinator", body, dest.HeadSHA, ctx); err != nil {
			return err
		}
		publications[i] = publication
	}
	return s.SaveTask(task)
}

func destinationObject(dest Destination) *ordjson.Object {
	row := ordjson.NewObject()
	row.Set("repository", dest.Repository)
	row.Set("number", json.Number(fmt.Sprint(dest.Number)))
	row.Set("head_sha", dest.HeadSHA)
	row.Set("state", dest.State)
	return row
}

func nilIfEmpty(text string) any {
	if text == "" {
		return nil
	}
	return text
}

func anyStrings(values []string) []any {
	out := make([]any, 0, len(values))
	for _, v := range values {
		out = append(out, v)
	}
	return out
}

func publicationRows(publications []Publication) []any {
	rows := make([]any, 0, len(publications))
	for _, publication := range publications {
		row := ordjson.NewObject()
		row.Set("run", publication.Run)
		row.Set("outcome", string(publication.Outcome))
		row.Set("reason", nilIfEmpty(publication.Reason))
		row.Set("attachments", json.Number(fmt.Sprint(publication.Attachments)))
		rows = append(rows, row)
	}
	return rows
}
