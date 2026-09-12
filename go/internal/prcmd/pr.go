package prcmd

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
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
		view := exec.Command(gh, "repo", "view", "--json", "nameWithOwner")
		view.Dir = repo
		if out, viewErr := view.Output(); viewErr == nil {
			var payload map[string]any
			if json.Unmarshal(out, &payload) == nil {
				remote, _ = payload["nameWithOwner"].(string)
			}
		}
	}
	cmd := exec.Command(gh, "pr", "view", fmt.Sprint(args.Number), "--json", "number,url,state,headRefName,headRefOid,baseRefName,headRepository,headRepositoryOwner,isCrossRepository,mergedAt,mergeCommit")
	if remote != "" {
		cmd.Args = append(cmd.Args, "--repo", remote)
	}
	cmd.Dir = repo
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return nil, fmt.Errorf("PR observation for #%d is uncertain: %s", args.Number, strings.TrimSpace(string(out)))
	}
	var data map[string]any
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, fmt.Errorf("gh did not return JSON: %s", string(out)[:min(300, len(out))])
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err = s.ReadTask(args.Task)
	if err != nil {
		return nil, err
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
	pr.Set("observed_at", store.Now())
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
		return nil, err
	}
	if err := s.SaveTask(task); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", args.Task)
	result.Set("pr", pr)
	result.Set("evidence", func() any { v, _ := record.Get("id"); return v }())
	result.Set("note", "An exact GitHub observation at one instant. Merged applies to this task only when the state is merged, a merge commit exists, and no identity finding remains.")
	return result, nil
}

func Evidence(s *store.Store, ctx *ordjson.Object, taskID, run, visibility string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	if visibility != "public" && visibility != "private" && visibility != "internal" {
		return nil, fmt.Errorf("--visibility must be public, private, or internal")
	}
	_, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("pr evidence requires a reconciled PR and a captured evidence run; record the PR with `pr reconcile` first.")
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
