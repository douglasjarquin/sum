package pipeline

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// Every gate command records one coordinator record carrying both the outcome and the sentence its row shows,
// so a row's text is written once where the facts are and Derive only maps the outcome onto a status.
const (
	outcomeUnavailable = "unavailable"
	gitBound           = 120 * time.Second
	networkBound       = 600 * time.Second
)

// An outcome this release does not know is a failure, never a silent pass.
func gateRow(task *ordjson.Object, stage Stage, kind, unobserved string, outcomes map[string]Status) Row {
	latest := latestFor(task, kind, "coordinator", Candidate(task))
	if latest == nil {
		return Row{Stage: stage, Status: Pending, Result: unobserved}
	}
	status, known := outcomes[stringField(latest, "outcome")]
	if !known {
		status = Fail
	}
	return Row{
		Stage:    stage,
		Status:   status,
		Result:   stringField(latest, "summary"),
		At:       stringField(latest, "at"),
		Evidence: []string{stringField(latest, "id")},
	}
}

func gateTask(s *store.Store, ctx *ordjson.Object, taskID, candidate string) (*ordjson.Object, string, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, "", err
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, "", err
	}
	if candidate == "" {
		candidate = Candidate(task)
	}
	if !sha40.MatchString(candidate) {
		return nil, "", fmt.Errorf("this task records no candidate; pass --candidate with the full 40-hex commit SHA")
	}
	return task, candidate, nil
}

// recordGate re-derives the table from the record it just appended, so a gate never writes a row directly.
func recordGate(s *store.Store, ctx *ordjson.Object, taskID, kind, candidate string, body *ordjson.Object, summary string) (*ordjson.Object, error) {
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
	appended, err := evidence.Append(task, kind, "coordinator", body, candidate, ctx)
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

func runDir(s *store.Store, taskID, gate string) (string, error) {
	taskPath, err := s.TaskPath(taskID)
	if err != nil {
		return "", err
	}
	stamp, err := runStamp()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(taskPath, pipelineDir, gate, stamp)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func runStamp() (string, error) {
	buf := make([]byte, 3)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(buf), nil
}

// A timeout or a failure to start is reported as exit -1 with the reason as stderr, so every caller reads one shape.
func gitIn(dir string, bound time.Duration, args ...string) proc.Result {
	run, err := proc.Run(append([]string{"git", "-C", dir}, args...), "", bound, false, gitEnv())
	if err != nil {
		run.Code = -1
		if strings.TrimSpace(run.Stderr) == "" {
			run.Stderr = err.Error()
		}
	}
	return run
}

// A credential prompt inside a coordinator command would hang with nobody watching.
func gitEnv() []string {
	return append(proc.ScrubbedEnv(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
}

func lastLineOf(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if len(trimmed) > 300 {
			return trimmed[:300]
		}
		return trimmed
	}
	return ""
}

func jsonNumber(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func shortSHA(sha string) string {
	if len(sha) < 7 {
		return sha
	}
	return sha[:7]
}
