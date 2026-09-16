package review

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

var madeOutcomes = map[string]bool{"pass": true, "fail": true, "blocked": true, "incomplete": true}

func loadMadeReport(path string) (*ordjson.Object, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Made report %s is unreadable: %w", path, err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("Made report is not JSON: %w", err)
	}
	candidate, _ := parsed["candidate"].(string)
	if !sha40.MatchString(candidate) {
		return nil, fmt.Errorf("Made report candidate must be a full 40-hex commit SHA")
	}
	runID, _ := parsed["run_id"].(string)
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("Made report is missing run_id")
	}
	outcome, _ := parsed["outcome"].(string)
	if !madeOutcomes[outcome] {
		return nil, fmt.Errorf("Made report outcome must be one of pass, fail, blocked, incomplete")
	}
	report := ordjson.NewObject()
	report.Set("imported", true)
	report.Set("run_id", runID)
	report.Set("candidate", candidate)
	report.Set("outcome", outcome)
	if repo, ok := parsed["repository"].(string); ok && repo != "" {
		report.Set("repository", repo)
	} else {
		report.Set("repository", nil)
	}
	if limitations, ok := parsed["limitations"].(string); ok {
		report.Set("limitations", limitations)
	} else {
		report.Set("limitations", nil)
	}
	var stages []any
	if rawStages, ok := parsed["stages"].([]any); ok {
		for _, item := range rawStages {
			if name, ok := item.(string); ok && name != "" {
				stages = append(stages, name)
			}
		}
	}
	if stages == nil {
		stages = []any{}
	}
	report.Set("stages", stages)
	var findings []any
	blocking := false
	if rawFindings, ok := parsed["findings"].([]any); ok {
		for _, item := range rawFindings {
			row, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("Made report findings must be objects with path, invariant, and severity")
			}
			severity, _ := row["severity"].(string)
			if severity != "blocking" && severity != "advisory" {
				return nil, fmt.Errorf("Made finding severity must be blocking or advisory")
			}
			if severity == "blocking" {
				blocking = true
			}
			finding := ordjson.NewObject()
			finding.Set("path", stringOrEmpty(row["path"]))
			finding.Set("invariant", stringOrEmpty(row["invariant"]))
			finding.Set("severity", severity)
			finding.Set("detail", stringOrEmpty(row["detail"]))
			findings = append(findings, finding)
		}
	}
	if findings == nil {
		findings = []any{}
	}
	report.Set("findings", findings)
	report.Set("blocking", blocking)
	return report, nil
}

func stringOrEmpty(v any) string {
	s, _ := v.(string)
	return s
}
