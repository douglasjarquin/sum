package pipeline

import (
	"fmt"
	"strings"
)

// GatesSettled is whether every delivery gate is pass, skipped, or an allowed not_declared (lint and CI).
// Factory merge-check and draft promotion both evaluate this record; they do not copy the rule.
func GatesSettled(record Record) (bool, string) {
	var failed []string
	for _, def := range Stages {
		row := record.Get(def.Stage)
		switch row.Status {
		case Pass, Skipped:
			continue
		case NotDeclared:
			if def.Stage == StageLint || def.Stage == StageCI {
				continue
			}
			failed = append(failed, fmt.Sprintf("%s=%s", def.Display, row.Status))
		default:
			failed = append(failed, fmt.Sprintf("%s=%s", def.Display, row.Status))
		}
	}
	if len(failed) > 0 {
		return false, strings.Join(failed, ", ")
	}
	return true, "all pipeline stages pass, skipped, or allowed not_declared"
}
