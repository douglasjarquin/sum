package usage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	File   = "usage.json"
	Schema = 1

	StatusComplete    = "complete"
	StatusPartial     = "partial"
	StatusUnavailable = "unavailable"

	CategoryImplementation = "implementation"
	CategoryReview         = "review"
	CategoryRepair         = "repair"
	CategoryUnknown        = "unknown"
)

type Tokens struct {
	Input         *int64 `json:"input"`
	Output        *int64 `json:"output"`
	CacheCreation *int64 `json:"cache_creation"`
	CacheRead     *int64 `json:"cache_read"`
}

type Invocations struct {
	Total     int64            `json:"total"`
	ByOutcome map[string]int64 `json:"by_outcome"`
	APIErrors int64            `json:"api_errors"`
	Subagent  int64            `json:"subagent"`
}

type Source struct {
	Path               string      `json:"path"`
	Subagent           bool        `json:"subagent"`
	Offset             int64       `json:"offset"`
	Lines              int64       `json:"lines"`
	LastMessageID      string      `json:"last_message_id"`
	LastMessageOutcome string      `json:"last_message_outcome"`
	FirstAt            string      `json:"first_at"`
	LastAt             string      `json:"last_at"`
	Models             []string    `json:"models"`
	Invocations        Invocations `json:"invocations"`
	Tokens             Tokens      `json:"tokens"`
	Status             string      `json:"status"`
	Reason             string      `json:"reason"`
}

type Session struct {
	ID              string    `json:"id"`
	Role            string    `json:"role"`
	Category        string    `json:"category"`
	Attempt         string    `json:"attempt"`
	Repair          string    `json:"repair"`
	RegistrationKey string    `json:"registration_key"`
	Cwd             string    `json:"cwd"`
	ProjectsDir     string    `json:"projects_dir"`
	Sources         []*Source `json:"sources"`
	FirstSeenAt     string    `json:"first_seen_at"`
	Status          string    `json:"status"`
	Reason          string    `json:"reason"`
	CollectedAt     string    `json:"collected_at"`
}

type Bucket struct {
	Sessions             int         `json:"sessions"`
	UnknownTokenSessions int         `json:"unknown_token_sessions"`
	Invocations          Invocations `json:"invocations"`
	Tokens               Tokens      `json:"tokens"`
	WallMs               int64       `json:"wall_ms"`
}

type Coverage struct {
	Status  string   `json:"status"`
	Reasons []string `json:"reasons"`
}

type Ledger struct {
	Schema      int                `json:"schema"`
	Task        string             `json:"task"`
	Harness     string             `json:"harness"`
	CollectedAt string             `json:"collected_at"`
	Sessions    []*Session         `json:"sessions"`
	Totals      map[string]*Bucket `json:"totals"`
	Coverage    Coverage           `json:"coverage"`
}

func Path(s *store.Store, taskID string) (string, error) {
	taskPath, err := s.TaskPath(taskID)
	if err != nil {
		return "", err
	}
	return filepath.Join(taskPath, File), nil
}

func Load(s *store.Store, taskID string) (*Ledger, bool, error) {
	path, err := Path(s, taskID)
	if err != nil {
		return nil, false, err
	}
	ledger := &Ledger{Schema: Schema, Task: taskID, Totals: map[string]*Bucket{}}
	info, statErr := os.Lstat(path)
	if statErr != nil || info.IsDir() {
		return ledger, false, nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("%s must not be a symlink", path)
	}
	value, err := ordjson.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	stored, ok := value.(*ordjson.Object)
	if !ok {
		return nil, false, fmt.Errorf("%s is not a usage ledger", path)
	}
	schema, _ := stored.Get("schema")
	if number, isNumber := schema.(json.Number); !isNumber || number.String() != fmt.Sprint(Schema) {
		return nil, false, fmt.Errorf("%s has usage schema %v; this release reads schema %d", path, schema, Schema)
	}
	raw, err := ordjson.MarshalCompact(stored)
	if err != nil {
		return nil, false, err
	}
	if err := json.Unmarshal(raw, ledger); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	if ledger.Task != taskID {
		return nil, false, fmt.Errorf("%s belongs to task %s, not %s", path, ledger.Task, taskID)
	}
	if ledger.Totals == nil {
		ledger.Totals = map[string]*Bucket{}
	}
	return ledger, true, nil
}

func (l *Ledger) save(s *store.Store) (string, error) {
	path, err := Path(s, l.Task)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s must not be a symlink", path)
	}
	obj, err := ordjson.FromValue(l)
	if err != nil {
		return "", err
	}
	if err := ordjson.WriteFile(path, obj); err != nil {
		return "", err
	}
	return path, nil
}

func (l *Ledger) session(id string) *Session {
	for _, session := range l.Sessions {
		if session.ID == id {
			return session
		}
	}
	return nil
}

func addTokens(sum *Tokens, delta Tokens) {
	add := func(dst **int64, src *int64) {
		if src == nil {
			return
		}
		if *dst == nil {
			zero := int64(0)
			*dst = &zero
		}
		**dst += *src
	}
	add(&sum.Input, delta.Input)
	add(&sum.Output, delta.Output)
	add(&sum.CacheCreation, delta.CacheCreation)
	add(&sum.CacheRead, delta.CacheRead)
}

func (t Tokens) known() bool {
	return t.Input != nil || t.Output != nil || t.CacheCreation != nil || t.CacheRead != nil
}

func addInvocations(sum *Invocations, delta Invocations) {
	if sum.ByOutcome == nil {
		sum.ByOutcome = map[string]int64{}
	}
	sum.Total += delta.Total
	sum.APIErrors += delta.APIErrors
	sum.Subagent += delta.Subagent
	for outcome, n := range delta.ByOutcome {
		sum.ByOutcome[outcome] += n
	}
}

func (se *Session) aggregate() (Invocations, Tokens, int64, []string) {
	var invocations Invocations
	var tokens Tokens
	var wall int64
	models := map[string]bool{}
	first, last := "", ""
	for _, source := range se.Sources {
		addInvocations(&invocations, source.Invocations)
		addTokens(&tokens, source.Tokens)
		for _, model := range source.Models {
			models[model] = true
		}
		if source.FirstAt != "" && (first == "" || source.FirstAt < first) {
			first = source.FirstAt
		}
		if source.LastAt > last {
			last = source.LastAt
		}
	}
	if first != "" && last != "" {
		wall = wallMs(first, last)
	}
	names := make([]string, 0, len(models))
	for model := range models {
		names = append(names, model)
	}
	sort.Strings(names)
	if invocations.ByOutcome == nil {
		invocations.ByOutcome = map[string]int64{}
	}
	return invocations, tokens, wall, names
}

type Known struct {
	WorkerAttempts  []string
	ReviewerPresent bool
	Unfolded        []string
}

func (l *Ledger) finalize(known Known, at string) {
	totals := map[string]*Bucket{}
	for _, name := range []string{CategoryImplementation, CategoryReview, CategoryRepair, CategoryUnknown, "task"} {
		totals[name] = &Bucket{Invocations: Invocations{ByOutcome: map[string]int64{}}}
	}
	var reasons []string
	readable := 0
	for _, session := range l.Sessions {
		invocations, tokens, wall, _ := session.aggregate()
		for _, name := range []string{session.Category, "task"} {
			bucket := totals[name]
			if bucket == nil {
				bucket = &Bucket{Invocations: Invocations{ByOutcome: map[string]int64{}}}
				totals[name] = bucket
			}
			bucket.Sessions++
			if !tokens.known() {
				bucket.UnknownTokenSessions++
			}
			addInvocations(&bucket.Invocations, invocations)
			addTokens(&bucket.Tokens, tokens)
			bucket.WallMs += wall
		}
		if session.Status != StatusUnavailable {
			readable++
		}
		if session.Status != StatusComplete {
			reasons = append(reasons, fmt.Sprintf("session %s %s: %s", session.ID, session.Status, session.Reason))
		}
	}
	l.Totals = totals
	for _, attempt := range known.WorkerAttempts {
		covered := false
		for _, session := range l.Sessions {
			if session.Attempt == attempt {
				covered = true
				break
			}
		}
		if !covered {
			reasons = append(reasons, fmt.Sprintf("worker attempt %s has no recorded session", attempt))
		}
	}
	if known.ReviewerPresent {
		covered := false
		for _, session := range l.Sessions {
			if session.Role == "reviewer" {
				covered = true
				break
			}
		}
		if !covered {
			reasons = append(reasons, "the reviewer endpoint has no recorded session")
		}
	}
	for _, id := range known.Unfolded {
		reasons = append(reasons, fmt.Sprintf("registration names session %s that is not folded", id))
	}
	switch {
	case l.Harness != "claude":
		l.Coverage = Coverage{Status: StatusUnavailable, Reasons: []string{fmt.Sprintf("harness %s has no usage adapter", l.Harness)}}
	case len(l.Sessions) == 0:
		l.Coverage = Coverage{Status: StatusUnavailable, Reasons: []string{"no session was recorded for this task"}}
	case readable == 0:
		l.Coverage = Coverage{Status: StatusUnavailable, Reasons: reasons}
	case len(reasons) == 0:
		l.Coverage = Coverage{Status: StatusComplete, Reasons: []string{}}
	default:
		l.Coverage = Coverage{Status: StatusPartial, Reasons: reasons}
	}
	l.CollectedAt = at
}
