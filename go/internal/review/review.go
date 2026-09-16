package review

import (
	"fmt"
	"regexp"

	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pipeline"
	"github.com/douglasjarquin/sum/go/internal/store"
)

var (
	verdicts    = map[string]bool{"approve": true, "changes-requested": true, "blocked": true, "comment": true}
	sha40       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	toolNamePat = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)
)

func identityEquals(a, b *ordjson.Object) bool {
	if a == nil || b == nil {
		return false
	}
	am, _ := a.Get("machine")
	bm, _ := b.Get("machine")
	as, _ := a.Get("session")
	bs, _ := b.Get("session")
	ap, _ := a.Get("pane")
	bp, _ := b.Get("pane")
	return am == bm && as == bs && ap == bp
}

func endpointRole(task, endpoint *ordjson.Object) string {
	if endpoint == nil {
		return ""
	}
	if pane, ok := task.Get("pane"); ok && pane != nil && identityEquals(task, endpoint) {
		return "worker"
	}
	if parent, ok := task.Get("parent"); ok {
		if p, is := parent.(*ordjson.Object); is && identityEquals(p, endpoint) {
			return "coordinator"
		}
	}
	if reviewer, ok := task.Get("reviewer"); ok {
		if r, is := reviewer.(*ordjson.Object); is && identityEquals(r, endpoint) {
			return "reviewer"
		}
	}
	return "other"
}

func Run(s *store.Store, taskID, verdict, candidate, toolName, text, runPath string, policyReviewed bool, endpoint *ordjson.Object) (*ordjson.Object, error) {
	if !verdicts[verdict] {
		return nil, fmt.Errorf("--verdict must be one of ['approve', 'changes-requested', 'blocked', 'comment']")
	}
	if candidate != "" && !sha40.MatchString(candidate) {
		return nil, fmt.Errorf("--candidate must be a full 40-hex commit SHA")
	}
	if toolName != "" && !toolNamePat.MatchString(toolName) {
		return nil, fmt.Errorf("--tool must be a short name of the review facility that produced these findings (for example `made`)")
	}
	var made *ordjson.Object
	if runPath != "" {
		if toolName == "" {
			toolName = "made"
		}
		if toolName != "made" {
			return nil, fmt.Errorf("--run is the Made report path; pass --tool made")
		}
		report, err := loadMadeReport(runPath)
		if err != nil {
			return nil, err
		}
		reportCandidate := ""
		if value, ok := report.Get("candidate"); ok {
			reportCandidate, _ = value.(string)
		}
		if candidate == "" {
			candidate = reportCandidate
		}
		if candidate != reportCandidate {
			return nil, fmt.Errorf("Made report candidate %s does not match --candidate %s", reportCandidate, candidate)
		}
		made = report
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	if made != nil {
		if repo, ok := made.Get("repository"); ok {
			if name, _ := repo.(string); name != "" {
				taskRepo, _ := task.Get("repository")
				if taskRepo != name {
					return nil, fmt.Errorf("Made report repository %s does not match task repository %v", name, taskRepo)
				}
			}
		}
		if blocking, _ := made.Get("blocking"); blocking == true && verdict == "approve" {
			return nil, fmt.Errorf("Made report has blocking findings; an approve is refused")
		}
		if text == "" {
			runID, _ := made.Get("run_id")
			text = fmt.Sprintf("Imported Made report %v", runID)
		}
	}
	role := endpointRole(task, endpoint)
	if role == "worker" {
		return nil, fmt.Errorf("The worker pane cannot record independent review of its own candidate. Save findings from the reviewer pane, or record `verify` as the coordinator.")
	}
	if endpoint != nil && role == "other" && toolName == "" {
		if existing, ok := task.Get("reviewer"); ok && existing != nil {
			rev := existing.(*ordjson.Object)
			pane, _ := rev.Get("pane")
			session, _ := rev.Get("session")
			return nil, fmt.Errorf("Task already has reviewer pane %v in session %v; a second reviewer endpoint is not adopted silently.", pane, session)
		}
		bound := ordjson.NewObject()
		for _, key := range []string{"machine", "session", "pane", "cwd"} {
			v, _ := endpoint.Get(key)
			bound.Set(key, v)
		}
		bound.Set("bound_at", store.Now())
		task.Set("reviewer", bound)
		role = "reviewer"
	}
	if role == "" {
		role = "unattributed"
	}
	body := ordjson.NewObject()
	body.Set("verdict", verdict)
	body.Set("text", text)
	if toolName == "" {
		body.Set("tool", nil)
	} else {
		body.Set("tool", toolName)
	}
	body.Set("policy_reviewed", policyReviewed)
	if made != nil {
		body.Set("made", made)
	} else {
		body.Set("made", nil)
	}
	var cand any
	if candidate != "" {
		cand = candidate
	}
	record, err := evidence.Append(task, "review", role, body, cand, endpoint)
	if err != nil {
		return nil, err
	}
	record.Set("brief_revision", evidence.ActiveRevision(s, task))
	if err := s.SaveTask(task); err != nil {
		return nil, err
	}
	note := pipeline.RefreshNote(s, task)
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("evidence", record)
	result.Set("pipeline_note", note)
	reviewer, _ := task.Get("reviewer")
	result.Set("reviewer", reviewer)
	result.Set("note", "Findings saved. They do not verify the candidate or close anything; a reviewer pane with saved findings is closable later, one without is not.")
	return result, nil
}
