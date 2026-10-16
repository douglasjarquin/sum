package usage

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func View(s *store.Store, taskID string) (*ordjson.Object, error) {
	loaded, err := settings.LoadSettings(s)
	if err != nil {
		return nil, err
	}
	if _, err := s.ReadTask(taskID); err != nil {
		return nil, err
	}
	ledger, exists, err := Load(s, taskID)
	if err != nil {
		return nil, err
	}
	view := ordjson.NewObject()
	view.Set("task", taskID)
	view.Set("accounting", settings.UsageView(loaded.Usage))
	if !exists {
		view.Set("ledger", nil)
		view.Set("collected_at", nil)
		view.Set("coverage", coverageObject(Coverage{Status: StatusUnavailable, Reasons: []string{"no usage ledger; nothing was collected for this task"}}))
		view.Set("totals", nil)
		view.Set("sessions", []any{})
		view.Set("note", "Totals are counts from Claude Code transcripts, not a bill; a null token field means the source never reported it.")
		return view, nil
	}
	path, _ := Path(s, taskID)
	view.Set("ledger", path)
	view.Set("harness", ledger.Harness)
	view.Set("collected_at", ledger.CollectedAt)
	view.Set("coverage", coverageObject(ledger.Coverage))
	totals := ordjson.NewObject()
	names := make([]string, 0, len(ledger.Totals))
	for name := range ledger.Totals {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		totals.Set(name, bucketObject(ledger.Totals[name]))
	}
	view.Set("totals", totals)
	rows := make([]any, 0, len(ledger.Sessions))
	for _, session := range ledger.Sessions {
		rows = append(rows, sessionObject(session))
	}
	view.Set("sessions", rows)
	view.Set("note", "Totals are counts from Claude Code transcripts, not a bill; a null token field means the source never reported it.")
	return view, nil
}

func coverageObject(coverage Coverage) *ordjson.Object {
	obj := ordjson.NewObject()
	obj.Set("status", coverage.Status)
	reasons := make([]any, 0, len(coverage.Reasons))
	for _, reason := range coverage.Reasons {
		reasons = append(reasons, reason)
	}
	obj.Set("reasons", reasons)
	return obj
}

func bucketObject(bucket *Bucket) *ordjson.Object {
	if bucket == nil {
		return nil
	}
	obj := ordjson.NewObject()
	obj.Set("sessions", jsonInt(int64(bucket.Sessions)))
	obj.Set("unknown_token_sessions", jsonInt(int64(bucket.UnknownTokenSessions)))
	obj.Set("invocations", invocationsObject(bucket.Invocations))
	obj.Set("tokens", tokensObject(bucket.Tokens))
	obj.Set("wall_ms", jsonInt(bucket.WallMs))
	return obj
}

func invocationsObject(invocations Invocations) *ordjson.Object {
	obj := ordjson.NewObject()
	obj.Set("total", jsonInt(invocations.Total))
	outcomes := ordjson.NewObject()
	names := make([]string, 0, len(invocations.ByOutcome))
	for name := range invocations.ByOutcome {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		outcomes.Set(name, jsonInt(invocations.ByOutcome[name]))
	}
	obj.Set("by_outcome", outcomes)
	obj.Set("api_errors", jsonInt(invocations.APIErrors))
	obj.Set("subagent", jsonInt(invocations.Subagent))
	return obj
}

func tokensObject(tokens Tokens) *ordjson.Object {
	obj := ordjson.NewObject()
	obj.Set("input", jsonIntOrNil(tokens.Input))
	obj.Set("output", jsonIntOrNil(tokens.Output))
	obj.Set("cache_creation", jsonIntOrNil(tokens.CacheCreation))
	obj.Set("cache_read", jsonIntOrNil(tokens.CacheRead))
	return obj
}

func sessionObject(session *Session) *ordjson.Object {
	invocations, tokens, wall, models := session.aggregate()
	obj := ordjson.NewObject()
	obj.Set("id", session.ID)
	obj.Set("role", session.Role)
	obj.Set("category", session.Category)
	obj.Set("attempt", nilIfEmpty(session.Attempt))
	obj.Set("repair", nilIfEmpty(session.Repair))
	obj.Set("status", session.Status)
	obj.Set("reason", nilIfEmpty(session.Reason))
	obj.Set("source", transcriptPath(session.ProjectsDir, session.Cwd, session.ID))
	obj.Set("sources", jsonInt(int64(len(session.Sources))))
	obj.Set("models", stringsList(models))
	obj.Set("invocations", invocationsObject(invocations))
	obj.Set("tokens", tokensObject(tokens))
	obj.Set("wall_ms", jsonInt(wall))
	obj.Set("first_seen_at", session.FirstSeenAt)
	obj.Set("collected_at", session.CollectedAt)
	return obj
}

func jsonInt(n int64) json.Number {
	return json.Number(fmt.Sprint(n))
}

func jsonIntOrNil(n *int64) any {
	if n == nil {
		return nil
	}
	return jsonInt(*n)
}

func nilIfEmpty(text string) any {
	if text == "" {
		return nil
	}
	return text
}

func stringsList(values []string) []any {
	list := make([]any, 0, len(values))
	for _, value := range values {
		list = append(list, value)
	}
	return list
}
