package prcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pipeline"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

var repoName = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
var sha40 = regexp.MustCompile(`^[0-9a-f]{40}$`)

type ReconcileArgs struct {
	Task    string
	Number  int
	Repo    string
	Replace bool
}

func Reconcile(s *store.Store, ctx *ordjson.Object, runtimeRoot string, args ReconcileArgs) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	if args.Repo != "" && !repoName.MatchString(args.Repo) {
		return nil, fmt.Errorf("--repo must be owner/name")
	}
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	gh, err := toolpath.Find(runtimeRoot, "gh")
	if err != nil {
		return nil, err
	}
	repo := asString(task, "repository")
	remote := args.Repo
	if remote == "" {
		remote = pipeline.RemoteRepository(gh, repo)
	}
	data, err := viewPR(gh, repo, remote, args.Number)
	if err != nil {
		return nil, err
	}
	pr, recordID, err := recordObservation(s, ctx, args.Task, data)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", args.Task)
	result.Set("pr", pr)
	result.Set("evidence", recordID)
	result.Set("ci", observeCI(s, ctx, runtimeRoot, args.Task, asList(data["statusCheckRollup"])))
	result.Set("evidence_publication", autoPublish(s, ctx, runtimeRoot, args.Task, pr))
	result.Set("pipeline_publication", autoPipeline(s, ctx, runtimeRoot, args.Task, pr))
	result.Set("note", "An exact GitHub observation at one instant. Merged applies to this task only when the state is merged, a merge commit exists, and no identity finding remains.")
	return result, nil
}

// prViewFields is what one PR observation reads from GitHub.
const prViewFields = "number,url,state,headRefName,headRefOid,baseRefName,headRepository,headRepositoryOwner,isCrossRepository,mergedAt,mergeCommit,mergeable,mergeStateStatus,statusCheckRollup"

// GHBound bounds one gh call, the pipeline's default (a variable so tests can shorten it).
var GHBound = pipeline.DefaultGHBound

// viewPR observes one PR through gh, bounded, and parses only stdout: gh's warnings on stderr never reach the JSON,
// and incomplete or oversized stdout is rejected rather than read as an observation.
func viewPR(gh, repo, remote string, number int) (map[string]any, error) {
	argv := []string{gh, "pr", "view", fmt.Sprint(number), "--json", prViewFields}
	if remote != "" {
		argv = append(argv, "--repo", remote)
	}
	res, err := proc.RunContext(context.Background(), proc.Cmd{Argv: argv, Dir: repo, Timeout: GHBound})
	if err != nil {
		detail := strings.TrimSpace(res.Stderr)
		if detail != "" {
			detail = ": " + detail
		}
		return nil, fmt.Errorf("PR observation for #%d is uncertain: %w%s", number, err, detail)
	}
	if res.Code != 0 {
		return nil, fmt.Errorf("PR observation for #%d is uncertain: %s", number, res.Detail())
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &data); err != nil {
		return nil, fmt.Errorf("gh did not return JSON: %s", res.Stdout[:min(300, len(res.Stdout))])
	}
	return data, nil
}

func recordObservation(s *store.Store, ctx *ordjson.Object, taskID string, data map[string]any) (*ordjson.Object, any, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, nil, err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, nil, err
	}
	identity := ordjson.NewObject()
	identity.Set("number", json.Number(fmt.Sprint(intOf(data["number"]))))
	identity.Set("url", fmt.Sprint(data["url"]))
	identity.Set("head_sha", fmt.Sprint(data["headRefOid"]))
	identity.Set("head_branch", fmt.Sprint(data["headRefName"]))
	identity.Set("base_branch", fmt.Sprint(data["baseRefName"]))
	pr := ordjson.NewObject()
	pr.Set("identity", identity)
	state := strings.ToLower(fmt.Sprint(data["state"]))
	pr.Set("state", state)
	observedAt := store.Now()
	pr.Set("observed_at", observedAt)
	pipeline.SetMergeability(pr, data["mergeable"], data["mergeStateStatus"], observedAt)
	by := ordjson.NewObject()
	for _, k := range []string{"machine", "session", "pane"} {
		v, _ := ctx.Get(k)
		by.Set(k, v)
	}
	pr.Set("observed_by", by)
	var findings []any
	headSHA := fmt.Sprint(data["headRefOid"])
	var candidate string
	if report := asObject(func() any { v, _ := task.Get("report"); return v }()); report != nil {
		candidate = asString(report, "candidate")
	}
	if candidate == "" {
		for _, raw := range asList(func() any { v, _ := task.Get("evidence"); return v }()) {
			ev := asObject(raw)
			if asString(ev, "kind") == "handoff" {
				if c := asString(ev, "candidate"); c != "" {
					candidate = c
				}
			}
		}
	}
	if candidate != "" && headSHA != candidate {
		findings = append(findings, fmt.Sprintf("PR head %s is not the recorded candidate %s", headSHA, candidate))
	}
	var mergeCommit any
	if mc, ok := data["mergeCommit"]; ok && mc != nil {
		if m, isMap := mc.(map[string]any); isMap {
			mergeCommit = m["oid"]
		} else {
			mergeCommit = mc
		}
	}
	pr.Set("findings", findings)
	pr.Set("merge_commit", mergeCommit)
	complete := state == "merged" && mergeCommit != nil && len(findings) == 0
	pr.Set("complete", complete)
	pr.Set("merged_for_task", complete)
	task.Set("pr", pr)
	body := ordjson.NewObject()
	body.Set("outcome", "observed")
	body.Set("pr", pr)
	record, err := evidence.Append(task, "publication", "github", body, identityValue(identity, "head_sha"), ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := s.SaveTask(task); err != nil {
		return nil, nil, err
	}
	_ = pipeline.RefreshNote(s, task)
	recordID, _ := record.Get("id")
	return pr, recordID, nil
}

func Evidence(s *store.Store, ctx *ordjson.Object, runtimeRoot string, args PublishArgs) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	if args.Visibility != "" && args.Visibility != "public" && args.Visibility != "private" && args.Visibility != "internal" {
		return nil, fmt.Errorf("--visibility must be public, private, or internal")
	}
	args.Trigger = "manual"
	publications, err := Publish(s, ctx, runtimeRoot, args)
	if err != nil {
		return nil, err
	}
	if len(publications) == 0 {
		return nil, fmt.Errorf("this task records no evidence run to publish; a worker's handoff must list a comparison.json, or pass --run with --evidence-root for a promoted copy.")
	}
	result := ordjson.NewObject()
	result.Set("task", args.Task)
	result.Set("publications", publicationRows(publications))
	result.Set("note", "The block states the worker's claim about the candidate build. It is not verification, review, or a merge decision.")
	return result, nil
}

// observeCI never fails reconcile: the PR observation is already saved, and an unreadable check list is its own
// record. This is the moment sum is already talking to GitHub about this PR, so it is where the checks are read.
func observeCI(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID string, rollup []any) any {
	row, err := pipeline.ObserveCI(s, ctx, runtimeRoot, taskID, rollup, 0)
	if err != nil {
		failed := ordjson.NewObject()
		failed.Set("gate", "ci")
		failed.Set("outcome", "unavailable")
		failed.Set("summary", err.Error())
		return failed
	}
	return row
}

// autoPublish never fails reconcile: the PR observation is already saved, and a publication failure is its own record.
func autoPublish(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID string, pr *ordjson.Object) []any {
	if asString(pr, "state") != "open" {
		return []any{}
	}
	if len(asList(func() any { v, _ := pr.Get("findings"); return v }())) > 0 {
		return []any{}
	}
	loaded, err := settings.LoadSettings(s)
	if err != nil {
		row := ordjson.NewObject()
		row.Set("run", nil)
		row.Set("outcome", string(Skipped))
		row.Set("reason", err.Error())
		return []any{row}
	}
	if !loaded.AutoPublishEvidence() {
		return []any{}
	}
	publications, err := Publish(s, ctx, runtimeRoot, PublishArgs{Task: taskID, Trigger: "reconcile"})
	if err != nil {
		row := ordjson.NewObject()
		row.Set("run", nil)
		row.Set("outcome", string(Failed))
		row.Set("reason", err.Error())
		return []any{row}
	}
	return publicationRows(publications)
}

// autoPipeline never fails reconcile: the PR observation is already saved, and a publication failure is its own record.
func autoPipeline(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID string, pr *ordjson.Object) any {
	if asString(pr, "state") != "open" {
		return nil
	}
	loaded, err := settings.LoadSettings(s)
	if err != nil {
		return pipelineRow(pipeline.OutcomeSkipped, err.Error())
	}
	if !loaded.AutoPublishEvidence() {
		return nil
	}
	publication, err := pipeline.Publish(s, ctx, runtimeRoot, pipeline.PublishArgs{Task: taskID, Trigger: "reconcile"})
	if err != nil {
		return pipelineRow(pipeline.OutcomeFailed, err.Error())
	}
	return publication.Row()
}

func pipelineRow(outcome pipeline.Outcome, reason string) *ordjson.Object {
	row := ordjson.NewObject()
	row.Set("block", "pipeline")
	row.Set("outcome", string(outcome))
	row.Set("reason", reason)
	return row
}

func asObject(v any) *ordjson.Object {
	o, _ := v.(*ordjson.Object)
	return o
}

func asList(v any) []any {
	list, _ := v.([]any)
	return list
}

func asString(o *ordjson.Object, key string) string {
	if o == nil {
		return ""
	}
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}

func identityValue(o *ordjson.Object, key string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	return v
}

func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case int:
		return n
	}
	return 0
}
