// Package incarnation verifies HC process and receipt bindings.
package incarnation

import (
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"time"
)

const (
	Same         = "same"
	Replaced     = "replaced"
	Unrecorded   = "unrecorded"
	Unobservable = "unobservable"
	Absent       = "absent"
)

// Verdict is the judgment of one recorded incarnation against the current occupant.
type Verdict struct {
	Outcome  string
	Verified bool
	Reason   string
}

// Object renders the verdict for command output.
func (v Verdict) Object() *ordjson.Object {
	obj := ordjson.NewObject()
	obj.Set("outcome", v.Outcome)
	obj.Set("verified", v.Verified)
	obj.Set("reason", v.Reason)
	return obj
}

func verified(outcome, reason string) Verdict {
	return Verdict{Outcome: outcome, Verified: true, Reason: reason}
}

func refused(outcome, reason string) Verdict { return Verdict{Outcome: outcome, Reason: reason} }

func CoordinatorRecord(owner *ordjson.Object) (any, string) {
	return field(owner, "incarnation"), earliest(str(owner, "claimed_at"), str(owner, "at"), str(owner, "upgraded_at"))
}

// OwnedCoordinatorRecord is CoordinatorRecord for the owner of s when that owner names endpoint; ok is false when no
// owner is recorded or it names another pane.
func OwnedCoordinatorRecord(s *store.Store, endpoint store.Endpoint) (recorded any, occupiedAt string, ok bool, err error) {
	owner, err := s.Owner()
	if err != nil || owner == nil {
		return nil, "", false, err
	}
	if owns, err := s.Matches(owner, endpoint); err != nil || !owns {
		return nil, "", false, err
	}
	recorded, occupiedAt = CoordinatorRecord(owner)
	return recorded, occupiedAt, true, nil
}

// RoleRecord is the incarnation that holds registration's role for endpoint: the owner's for the coordinator while
// the owner names endpoint, the registration's own for a worker. ok is false for any other role or owner.
func RoleRecord(s *store.Store, registration *ordjson.Object, endpoint store.Endpoint) (recorded any, occupiedAt string, ok bool, err error) {
	switch str(registration, "role") {
	case "coordinator":
		return OwnedCoordinatorRecord(s, endpoint)
	case "worker":
		recorded, occupiedAt = RegistrationRecord(registration)
		return recorded, occupiedAt, true, nil
	}
	return nil, "", false, nil
}

// WorkerRecord is the incarnation a worker registration recorded for taskID and the registration's first occupancy
// time, or ok false when registration is not that task's worker.
func WorkerRecord(registration *ordjson.Object, taskID any) (value any, occupiedAt string, ok bool) {
	// Registrations written before workers recorded their task carry task null; one cannot name another task.
	recordedTask := field(registration, "task")
	if str(registration, "role") != "worker" || taskID == nil || (recordedTask != nil && fmt.Sprint(recordedTask) != fmt.Sprint(taskID)) {
		return nil, "", false
	}
	value, occupiedAt = RegistrationRecord(registration)
	return value, occupiedAt, true
}

// RegistrationRecord is the incarnation a registration recorded and its first occupancy time (registered_at, which
// re-registration keeps).
func RegistrationRecord(registration *ordjson.Object) (any, string) {
	return field(registration, "incarnation"), earliest(str(registration, "registered_at"))
}

func earliest(values ...string) string {
	best := ""
	var bestTime time.Time
	for _, v := range values {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			continue
		}
		if best == "" || t.Before(bestTime) {
			best, bestTime = v, t
		}
	}
	return best
}

// Recovery names the explicit action after a refused native binding.
func Recovery(role, outcome string) string {
	preserved := "Tasks, decisions, reservations, and delivery history are unchanged; nothing is taken over or relaunched automatically. "
	if outcome == Unobservable {
		return preserved + "Rerun once the recorded HC core can report the terminal and foreground process."
	}
	if role == "coordinator" {
		return preserved + "If the user confirms the recorded coordinator terminal is gone, run `sumctl init --role coordinator --reclaim` in the intended native terminal."
	}
	if role == "worker" {
		return preserved + "After inspecting the native terminal, the coordinator rebinds it deliberately with `sumctl bind TASK --worker-terminal ADDRESS`."
	}
	return preserved + "Inspect the recorded native binding before explicitly replacing it."
}

func field(obj *ordjson.Object, key string) any {
	if obj == nil {
		return nil
	}
	v, _ := obj.Get(key)
	return v
}

func str(obj *ordjson.Object, key string) string {
	s, _ := field(obj, key).(string)
	return s
}
