package verifycmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/evidenceview"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

var (
	sha40         = regexp.MustCompile(`^[0-9a-f]{40}$`)
	verifyResults = map[string]bool{"pass": true, "fail": true, "blocked": true, "inconclusive": true}
	runIDPattern  = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$`)
	runOutcomes   = map[string]bool{"pass": true, "fail": true, "blocked": true, "error": true, "checked": true}
	runOutcomeMap = map[string]string{"pass": "pass", "fail": "fail", "blocked": "blocked", "error": "inconclusive"}
)

type Args struct {
	Task        string
	Candidate   string
	Result      string
	Run         string
	Execute     bool
	Base        string
	Text        string
	File        string
	RuntimeRoot string
}

func Run(s *store.Store, ctx *ordjson.Object, args Args) (*ordjson.Object, error) {
	if args.Run != "" && args.Execute {
		return nil, fmt.Errorf("Pass either --run PATH (a run you executed) or --execute (sum runs the contract now), not both.")
	}
	if !sha40.MatchString(args.Candidate) {
		return nil, fmt.Errorf("--candidate must be the full 40-hex commit SHA that was actually verified")
	}
	if args.Result != "" && !verifyResults[args.Result] {
		return nil, fmt.Errorf("--result must be one of [pass, fail, blocked, inconclusive]")
	}
	var text string
	var err error
	if args.Text != "" || args.File != "" {
		text, err = app.TextInput(args.Text, args.File)
		if err != nil {
			return nil, err
		}
	}
	if args.Run == "" && !args.Execute && (args.Result == "" || text == "") {
		return nil, fmt.Errorf("Without --run or --execute, both --result and --text/--file are required: say what you executed and what happened.")
	}
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	body := ordjson.NewObject()
	body.Set("result", args.Result)
	body.Set("text", text)
	if args.Execute {
		record, kept, execErr := executeRootVerification(s, args.RuntimeRoot, task, args.Candidate, args.Base, ctx)
		if execErr != nil {
			return nil, execErr
		}
		fields, evErr := runEvidence(record, args.Candidate, task, kept, "separate-checkout")
		if evErr != nil {
			return nil, evErr
		}
		for _, k := range fields.Keys() {
			v, _ := fields.Get(k)
			body.Set(k, v)
		}
		if graph, ok := record.Get("graph"); ok {
			body.Set("graph", graph)
		}
	} else if args.Run != "" {
		record, readErr := readRunRecord(args.Run)
		if readErr != nil {
			return nil, readErr
		}
		isolation := "other-checkout"
		if worktree := asString(task, "worktree"); worktree != "" {
			root := asString(record, "root")
			if resolvedWork, e1 := filepath.Abs(worktree); e1 == nil {
				if resolvedRoot, e2 := filepath.Abs(root); e2 == nil && resolvedWork == resolvedRoot {
					isolation = "task-checkout"
				}
			}
		}
		fields, evErr := runEvidence(record, args.Candidate, task, args.Run, isolation)
		if evErr != nil {
			return nil, evErr
		}
		for _, k := range fields.Keys() {
			v, _ := fields.Get(k)
			body.Set(k, v)
		}
	}
	if runID := asString(body, "run_id"); runID != "" {
		if args.Result != "" && args.Result != asString(body, "result") {
			return nil, fmt.Errorf("--result %s contradicts the run record: outcome %s means %s. The record stands; do not relabel it.", args.Result, asString(body, "outcome"), asString(body, "result"))
		}
		if text == "" {
			body.Set("text", fmt.Sprintf("coordinator run %s (%s)", runID, asString(body, "outcome")))
		} else {
			body.Set("text", text)
		}
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
	if runID := asString(body, "run_id"); runID != "" {
		if source := recordedRun(task, runID); source != "" {
			return nil, fmt.Errorf("Run id %s was already recorded on this task by the %s. Root verification is a fresh execution under its own run id; the worker's record is a claim and is never re-labelled as the coordinator's.", runID, source)
		}
	}
	record, err := evidence.Append(task, "verification", "coordinator", body, args.Candidate, ctx)
	if err != nil {
		return nil, err
	}
	record.Set("brief_revision", evidence.ActiveRevision(s, task))
	if err := s.SaveTask(task); err != nil {
		return nil, err
	}
	view := evidenceview.View(task)
	verification, _ := view.Get("verification")
	result := ordjson.NewObject()
	result.Set("task", args.Task)
	result.Set("evidence", record)
	result.Set("verification", verification)
	return result, nil
}

func asString(o *ordjson.Object, key string) string {
	if o == nil {
		return ""
	}
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}

func readRunRecord(path string) (*ordjson.Object, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil, fmt.Errorf("--run %s is not a file; pass the run.json the verify runner printed as `record:`", path)
	}
	if info.Size() > 256*1024*4 {
		return nil, fmt.Errorf("--run %s is too large to be a run.json record", path)
	}
	value, err := ordjson.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--run %s is not valid JSON: %s", path, err)
	}
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("--run %s is not a schema 1 run record from .agents/skills/verify", path)
	}
	schema, _ := obj.Get("schema")
	n, _ := schema.(json.Number)
	i, _ := n.Int64()
	if i != 1 {
		return nil, fmt.Errorf("--run %s is not a schema 1 run record from .agents/skills/verify", path)
	}
	return obj, nil
}

func runEvidence(record *ordjson.Object, candidate string, task *ordjson.Object, recordPath, isolation string) (*ordjson.Object, error) {
	runID := asString(record, "run_id")
	if !runIDPattern.MatchString(runID) {
		return nil, fmt.Errorf("run record has no usable run_id")
	}
	runner := objectField(record, "runner")
	mode := asString(runner, "mode")
	outcome := asString(record, "outcome")
	if mode == "check" || outcome == "checked" {
		return nil, fmt.Errorf("run %s is a --check record: it validated the contract and executed nothing, so it verifies no candidate", runID)
	}
	if !runOutcomes[outcome] {
		return nil, fmt.Errorf("run %s has outcome %q; expected one of [pass, fail, blocked, error, checked]", runID, outcome)
	}
	candidateObj := objectField(record, "candidate")
	ran := asString(candidateObj, "sha")
	if ran != candidate {
		return nil, fmt.Errorf("run %s verified %s, not --candidate %s. Evidence for another SHA is historical; run the contract against this candidate.", runID, ran, candidate)
	}
	dirty, _ := candidateObj.Get("dirty")
	dirtyBool, _ := dirty.(bool)
	policy := objectField(record, "policy")
	dispatch := objectField(task, "verification_policy")
	contractObj := objectField(record, "contract")
	contractSHA := asString(contractObj, "sha256")
	contractChanged := asString(dispatch, "contract_sha256") != "" && contractSHA != "" && contractSHA != asString(dispatch, "contract_sha256")
	checked, _ := policy.Get("checked")
	requiresReview, _ := record.Get("requires_root_review")
	requires := requiresReview == true || contractChanged || checked != true
	result := runOutcomeMap[outcome]
	if dirtyBool && result == "pass" {
		result = "inconclusive"
	}
	body := ordjson.NewObject()
	body.Set("result", result)
	body.Set("run_id", runID)
	body.Set("outcome", outcome)
	body.Set("record", recordPath)
	root := boundString(asString(record, "root"), 500)
	if root == "" {
		body.Set("root", nil)
	} else {
		body.Set("root", root)
	}
	body.Set("isolation", isolation)
	body.Set("dirty", dirtyBool)
	var certifies any
	if asString(record, "certifies") == candidate {
		certifies = candidate
	}
	body.Set("certifies", certifies)
	body.Set("requires_root_review", requires)
	body.Set("contract_sha256", contractSHA)
	body.Set("contract_changed_since_dispatch", contractChanged)
	policyBody := ordjson.NewObject()
	policyBody.Set("checked", checked == true)
	if policyBase, ok := policy.Get("base"); ok {
		policyBody.Set("base", policyBase)
	} else {
		policyBody.Set("base", nil)
	}
	policyBody.Set("changed", boundStringList(policy, "changed", 500, 50))
	body.Set("policy", policyBody)
	body.Set("not_exercised", boundStringList(record, "not_exercised", 200, 100))
	if execution := objectField(record, "execution"); execution != nil {
		row := ordjson.NewObject()
		for _, key := range []string{"exit", "timed_out", "seconds"} {
			v, _ := execution.Get(key)
			row.Set(key, v)
		}
		body.Set("execution", row)
	} else {
		body.Set("execution", nil)
	}
	if reason := asString(record, "blocked_reason"); reason != "" {
		body.Set("blocked_reason", boundString(reason, 500))
	} else {
		body.Set("blocked_reason", nil)
	}
	return body, nil
}

func boundString(s string, limit int) string {
	runes := []rune(s)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return s
}

func boundStringList(o *ordjson.Object, key string, itemLimit, listLimit int) []any {
	if o == nil {
		return []any{}
	}
	raw, _ := o.Get(key)
	list, _ := raw.([]any)
	if list == nil {
		return []any{}
	}
	if len(list) > listLimit {
		list = list[:listLimit]
	}
	out := make([]any, 0, len(list))
	for _, item := range list {
		out = append(out, boundString(fmt.Sprint(item), itemLimit))
	}
	return out
}

func objectField(o *ordjson.Object, key string) *ordjson.Object {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	obj, _ := v.(*ordjson.Object)
	return obj
}

func recordedRun(task *ordjson.Object, runID string) string {
	list, _ := task.Get("evidence")
	items, _ := list.([]any)
	for _, raw := range items {
		rec, _ := raw.(*ordjson.Object)
		if rec == nil {
			continue
		}
		if asString(rec, "run_id") == runID {
			return asString(rec, "source")
		}
	}
	return ""
}
