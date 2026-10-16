package usage

import (
	"fmt"
	"os"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type observed struct {
	id       string
	role     string
	key      string
	cwd      string
	live     bool
	attempt  string
	repairID string
	category string
}

func Collect(s *store.Store, taskID string) (*ordjson.Object, error) {
	enabled, err := settings.UsageEnabled(s)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("enabled", enabled)
	if !enabled {
		result.Set("collected", false)
		result.Set("note", "Usage accounting is off; nothing was read. Enable it with `settings set --usage-accounting true`.")
		return result, nil
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return collectLocked(s, taskID, result)
}

func collectLocked(s *store.Store, taskID string, result *ordjson.Object) (*ordjson.Object, error) {
	warnings := []any{}
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	if recorded, _ := task.Get("machine"); !host.Is(recorded) {
		result.Set("collected", false)
		result.Set("warnings", []any{"Task belongs to another machine; its transcripts are not on this host, nothing was read."})
		return result, nil
	}
	ledger, _, err := Load(s, taskID)
	if err != nil {
		return nil, err
	}
	ledger.Harness = stringOf(task, "harness")
	known := Known{}
	stamp := store.Now()
	if ledger.Harness == "claude" {
		sessions, discoverWarnings := discover(s, task)
		warnings = append(warnings, discoverWarnings...)
		projectsDir := ProjectsDir(os.Getenv)
		for _, seen := range sessions {
			session := ledger.session(seen.id)
			if session == nil {
				session = &Session{ID: seen.id, Role: seen.role, Category: seen.category, Attempt: seen.attempt, Repair: seen.repairID, RegistrationKey: seen.key, Cwd: seen.cwd, FirstSeenAt: stamp}
				ledger.Sessions = append(ledger.Sessions, session)
			}
			session.ProjectsDir = projectsDir
			read(session, seen.live, projectsDir, stamp)
			if session.Status == StatusUnavailable || (session.Status == StatusPartial && session.Reason != "session-live") {
				warnings = append(warnings, fmt.Sprintf("session %s %s: %s", session.ID, session.Status, session.Reason))
			}
			for _, source := range session.Sources {
				if source.Status == StatusPartial || (source.Status == StatusUnavailable && source.Subagent) {
					if !contains(stringsOf(warnings), source.Reason) {
						warnings = append(warnings, fmt.Sprintf("session %s source %s: %s", session.ID, source.Status, source.Reason))
					}
				}
			}
		}
		known = knownFor(task, ledger)
	}
	ledger.finalize(known, stamp)
	path, err := ledger.save(s)
	if err != nil {
		return nil, err
	}
	result.Set("collected", true)
	result.Set("ledger", path)
	result.Set("sessions", len(ledger.Sessions))
	result.Set("coverage", coverageObject(ledger.Coverage))
	result.Set("totals", bucketObject(ledger.Totals["task"]))
	result.Set("warnings", warnings)
	return result, nil
}

func read(session *Session, live bool, projectsDir, stamp string) {
	main := transcriptPath(projectsDir, session.Cwd, session.ID)
	paths := append([]string{main}, subagentPaths(projectsDir, session.Cwd, session.ID)...)
	for _, path := range paths {
		found := false
		for _, source := range session.Sources {
			if source.Path == path {
				found = true
				break
			}
		}
		if !found {
			session.Sources = append(session.Sources, &Source{Path: path, Subagent: path != main})
		}
	}
	status, reason := StatusComplete, ""
	for _, source := range session.Sources {
		readSource(source)
		switch {
		case source.Path == main && source.Status == StatusUnavailable:
			status, reason = StatusUnavailable, source.Reason
		case source.Status == StatusPartial && status == StatusComplete:
			status, reason = StatusPartial, source.Reason
		}
	}
	if status != StatusUnavailable && live {
		status, reason = StatusPartial, "session-live"
	}
	session.Status, session.Reason, session.CollectedAt = status, reason, stamp
}

func discover(s *store.Store, task *ordjson.Object) ([]observed, []any) {
	var warnings []any
	taskID := stringOf(task, "id")
	worker, workerErr := reservations.Worker(task)
	attemptID, attemptLive, checkout := "", false, ""
	if workerErr == nil && worker != nil {
		attemptID = stringOf(worker, "id")
		checkout = stringOf(worker, "checkout")
		attemptLive = reservationHeld(worker)
	}
	category := CategoryImplementation
	repairID := ""
	if workerErr == nil && worker != nil {
		if value, has := worker.Get("repair"); has && value != nil {
			repairID = fmt.Sprint(value)
			if repairIsSend(task, repairID) {
				category = CategoryRepair
			}
		}
	}
	var sessions []observed
	registrations, err := s.Registrations()
	if err != nil {
		return nil, []any{"Cannot list pane registrations: " + err.Error()}
	}
	for _, registration := range registrations {
		if stringOf(registration, "role") != "worker" || stringOf(registration, "task") != taskID {
			continue
		}
		id := claudeSession(objectOf(registration, "incarnation"))
		if id == "" {
			warnings = append(warnings, fmt.Sprintf("worker registration %s names no Claude conversation", stringOf(registration, "key")))
			continue
		}
		entry := observed{id: id, role: "worker", key: stringOf(registration, "key"), cwd: stringOf(registration, "cwd"), category: CategoryUnknown}
		if attemptID != "" && (checkout == "" || samePath(checkout, entry.cwd)) {
			entry.attempt, entry.repairID, entry.category, entry.live = attemptID, repairID, category, attemptLive
		}
		sessions = append(sessions, entry)
	}
	if reviewer := objectOf(task, "reviewer"); reviewer != nil {
		if id := claudeSession(objectOf(reviewer, "incarnation")); id != "" {
			sessions = append(sessions, observed{id: id, role: "reviewer", cwd: stringOf(reviewer, "cwd"), category: CategoryReview, live: true})
		} else {
			warnings = append(warnings, "the reviewer endpoint records no Claude conversation")
		}
	}
	return sessions, warnings
}

func knownFor(task *ordjson.Object, ledger *Ledger) Known {
	known := Known{}
	if worker, err := reservations.Worker(task); err == nil && worker != nil {
		if id := stringOf(worker, "id"); id != "" {
			known.WorkerAttempts = append(known.WorkerAttempts, id)
		}
	}
	for _, raw := range listOf(task, "evidence") {
		record := asObject(raw)
		if record == nil || stringOf(record, "kind") != "execution" {
			continue
		}
		if attempt := objectOf(record, "attempt"); attempt != nil && stringOf(attempt, "kind") == "worker" {
			if id := stringOf(attempt, "id"); id != "" && !contains(known.WorkerAttempts, id) {
				known.WorkerAttempts = append(known.WorkerAttempts, id)
			}
		}
	}
	known.ReviewerPresent = objectOf(task, "reviewer") != nil
	return known
}

func reservationHeld(attempt *ordjson.Object) bool {
	switch stringOf(attempt, "state") {
	case "held", "observing", "starting", "running", "uncertain":
		return true
	}
	return false
}

func repairIsSend(task *ordjson.Object, repairID string) bool {
	ledger, err := repair.Ledger(task)
	if err != nil || ledger == nil {
		return false
	}
	for _, raw := range listOf(ledger, "operations") {
		op := asObject(raw)
		if op != nil && stringOf(op, "id") == repairID {
			return stringOf(op, "kind") == "send"
		}
	}
	return false
}

func claudeSession(incarnation *ordjson.Object) string {
	session := objectOf(incarnation, "agent_session")
	if session == nil || stringOf(session, "agent") != "claude" || stringOf(session, "kind") != "id" {
		return ""
	}
	return stringOf(session, "value")
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	return Slug(a) == Slug(b)
}

func stringOf(obj *ordjson.Object, key string) string {
	if obj == nil {
		return ""
	}
	value, _ := obj.Get(key)
	text, _ := value.(string)
	return text
}

func objectOf(obj *ordjson.Object, key string) *ordjson.Object {
	if obj == nil {
		return nil
	}
	value, _ := obj.Get(key)
	return asObject(value)
}

func asObject(value any) *ordjson.Object {
	obj, _ := value.(*ordjson.Object)
	return obj
}

func listOf(obj *ordjson.Object, key string) []any {
	if obj == nil {
		return nil
	}
	value, _ := obj.Get(key)
	list, _ := value.([]any)
	return list
}

func BestEffort(s *store.Store, taskID string) *ordjson.Object {
	result, err := Collect(s, taskID)
	if err != nil {
		result = ordjson.NewObject()
		result.Set("task", taskID)
		result.Set("collected", false)
		result.Set("warnings", []any{"Usage collection failed and changed nothing else: " + err.Error()})
	}
	return result
}

func stringsOf(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, fmt.Sprint(value))
	}
	return out
}
