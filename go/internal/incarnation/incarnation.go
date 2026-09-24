// Package incarnation tells a reusable Herdr address apart from the occupant behind it.
//
// A Herdr pane ID is unique only while one server runs. After a restart Herdr restores the saved layout under the
// same IDs with new shells, and a pane that was never saved hands its ID to whatever is created next. So a recorded
// machine, session, and pane names an address, not an occupant. sum records what occupied the address when it bound
// it, and judges every later use against what occupies it now.
//
// The evidence is what the pinned Herdr reports, nothing invented:
//   - terminal_id, from pane get, agent get, and agent list. It changes on every server incarnation, including a
//     live handoff.
//   - agent_session, the native conversation an official integration reports. It survives restart and handoff.
//   - The pane shell's pid (pane process-info) and that process's start time, read from the local OS. A live handoff
//     keeps the process, a restart replaces it, and the start time tells a reused pid from the original.
//
// Herdr exposes no server generation and no prompt precondition, so a restart between the last observation and a
// prompt cannot be excluded here. Role bookkeeping built on this is not an OS security sandbox: any process in a pane
// can report an agent session.
package incarnation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

// Outcomes of Judge. Same, NewConversation, Handoff, Restored, and LegacyVerified are verified; the rest are not.
const (
	Same            = "same"
	NewConversation = "new-conversation"
	Handoff         = "handoff"
	Restored        = "restored"
	LegacyVerified  = "legacy-verified"
	Replaced        = "replaced"
	Unrecorded      = "unrecorded"
	Unobservable    = "unobservable"
	Absent          = "absent"
)

// Skew bounds clock noise between a record's time and a process start time: both are second-truncated, and ps
// derives start times from a boot time the kernel recomputes from the wall clock.
const Skew = 2 * time.Second

// ObserveTimeout bounds each Herdr observation and the local start-time read.
var ObserveTimeout = 5 * time.Second

// Session is the native agent conversation an integration reported for a pane.
type Session struct {
	Agent string
	Kind  string
	Value string
}

// Shell is the pane shell's process identity: its pid and its OS start time (RFC 3339, UTC, second resolution).
type Shell struct {
	PID     int
	Started string
}

// Evidence is what Herdr reports about the occupant of one address.
type Evidence struct {
	Terminal string
	Session  *Session
	// Agent is the agent kind Herdr detects in the pane now ("" when none).
	Agent string
	Shell *Shell
}

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

// FromInfo reads the evidence in a Herdr pane or agent object (a pane get, agent get, agent start, or agent list
// row), unwrapping a {"pane": ...} or {"agent": ...} envelope.
func FromInfo(value any) Evidence {
	obj := unwrap(value)
	if obj == nil {
		return Evidence{}
	}
	e := Evidence{Terminal: str(obj, "terminal_id"), Agent: str(obj, "agent")}
	if session, ok := field(obj, "agent_session").(*ordjson.Object); ok {
		e.Session = parseSession(session)
	}
	return e
}

func unwrap(value any) *ordjson.Object {
	obj, _ := value.(*ordjson.Object)
	if inner, ok := field(obj, "pane").(*ordjson.Object); ok {
		obj = inner
	}
	return herdrclient.UnwrapAgent(obj)
}

func parseSession(obj *ordjson.Object) *Session {
	s := &Session{Agent: str(obj, "agent"), Kind: str(obj, "kind"), Value: str(obj, "value")}
	if s.Value == "" {
		return nil
	}
	return s
}

// Record is the evidence as it is stored on a registration or owner record.
func (e Evidence) Record(at string) *ordjson.Object {
	obj := ordjson.NewObject()
	obj.Set("terminal", nullable(e.Terminal))
	if e.Session != nil {
		session := ordjson.NewObject()
		session.Set("agent", e.Session.Agent)
		session.Set("kind", e.Session.Kind)
		session.Set("value", e.Session.Value)
		obj.Set("agent_session", session)
	} else {
		obj.Set("agent_session", nil)
	}
	if e.Shell != nil {
		shell := ordjson.NewObject()
		shell.Set("pid", json.Number(strconv.Itoa(e.Shell.PID)))
		shell.Set("started", e.Shell.Started)
		obj.Set("shell", shell)
	} else {
		obj.Set("shell", nil)
	}
	obj.Set("observed_at", at)
	return obj
}

// Refresh is the record a verified occupant should be judged by next time: the observed terminal and native session,
// and the observed shell or, when none was observed in the same terminal, the recorded one. changed is false when that is what recordedValue
// already says, so a caller writes only real changes. Call it only after a verified Judge.
func Refresh(recordedValue any, observed Evidence, at string) (record *ordjson.Object, changed bool) {
	rec, _ := parse(recordedValue)
	if observed.Shell == nil && observed.Terminal == rec.Terminal {
		observed.Shell = rec.Shell
	}
	same := rec.Terminal == observed.Terminal && sameSession(rec.Session, observed.Session) && sameShellRecord(rec.Shell, observed.Shell)
	if same && rec.Terminal != "" {
		return nil, false
	}
	return observed.Record(at), true
}

func sameSession(a, b *Session) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func sameShellRecord(a, b *Shell) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// recorded is a stored incarnation. A record without a terminal is legacy: it predates incarnation tracking or was
// taken where Herdr reported none.
type recorded struct {
	Evidence
	at string
}

func parse(value any) (recorded, bool) {
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return recorded{}, false
	}
	r := recorded{Evidence: Evidence{Terminal: str(obj, "terminal")}, at: str(obj, "observed_at")}
	if session, ok := field(obj, "agent_session").(*ordjson.Object); ok {
		r.Session = parseSession(session)
	}
	if shell, ok := field(obj, "shell").(*ordjson.Object); ok {
		if pid, ok := intField(shell, "pid"); ok && pid > 0 && str(shell, "started") != "" {
			r.Shell = &Shell{PID: pid, Started: str(shell, "started")}
		}
	}
	return r, r.Terminal != ""
}

// ShellProbe observes the current occupant's shell when a judgment needs it. It is called at most once.
type ShellProbe func() (*Shell, error)

// Judge decides whether the incarnation recorded for an address still occupies it. occupiedAt is the record's own
// time at which the address was known occupied by the recorded party; it proves a legacy record (one without a
// terminal). The shell probe runs only when the terminal differs or the record is legacy.
func Judge(recordedValue any, occupiedAt string, observed Evidence, probe ShellProbe) Verdict {
	rec, ok := parse(recordedValue)
	if !ok {
		return judgeLegacy(occupiedAt, probe)
	}
	if observed.Terminal == "" {
		return refused(Unobservable, "Herdr reported no terminal_id for this pane, so its occupant cannot be compared with the recorded terminal "+rec.Terminal+".")
	}
	if observed.Terminal == rec.Terminal {
		if rec.Session != nil && observed.Session != nil && *rec.Session != *observed.Session {
			return verified(NewConversation, fmt.Sprintf("same terminal %s; the native %s conversation changed from %s to %s", rec.Terminal, observed.Session.Agent, rec.Session.Value, observed.Session.Value))
		}
		return verified(Same, "same terminal "+rec.Terminal)
	}
	changed := fmt.Sprintf("terminal %s is now %s", rec.Terminal, observed.Terminal)
	if rec.Session != nil && observed.Session != nil && *rec.Session == *observed.Session && observed.Agent != "" && observed.Agent == rec.Session.Agent {
		return verified(Restored, changed+"; Herdr restored the recorded native "+rec.Session.Agent+" conversation "+rec.Session.Value)
	}
	shell, err := runProbe(probe)
	if err != nil {
		return refused(Unobservable, changed+", and the pane shell cannot be observed ("+err.Error()+"), so a live handoff cannot be told apart from a different occupant.")
	}
	switch sameShell(rec.Shell, rec.at, shell) {
	case shellSame:
		return verified(Handoff, changed+"; the same shell process "+describe(shell)+" survived, as in a live handoff")
	case shellDifferent:
		return refused(Replaced, changed+" and the pane shell is "+describe(shell)+" instead of "+describeRecorded(rec.Shell, rec.at)+": a different occupant holds this address.")
	default:
		return refused(Unobservable, changed+", and the shell "+describe(shell)+" cannot be placed before or after the record at "+rec.at+".")
	}
}

func judgeLegacy(occupiedAt string, probe ShellProbe) Verdict {
	prefix := "the record predates incarnation tracking"
	if _, err := parseTime(occupiedAt); err != nil {
		return refused(Unrecorded, prefix+" and carries no occupancy time; missing metadata is not proof of a match.")
	}
	shell, err := runProbe(probe)
	if err != nil {
		return refused(Unrecorded, prefix+" and the pane shell cannot be observed ("+err.Error()+"); missing metadata is not proof of a match.")
	}
	switch sameShell(nil, occupiedAt, shell) {
	case shellSame:
		return verified(LegacyVerified, prefix+"; the pane shell "+describe(shell)+" already ran when it was written at "+occupiedAt)
	case shellDifferent:
		return refused(Replaced, prefix+" and the pane shell "+describe(shell)+" started after it was written at "+occupiedAt+": a different occupant holds this address.")
	default:
		return refused(Unrecorded, prefix+" and the pane shell "+describe(shell)+" started too close to "+occupiedAt+" to tell; missing metadata is not proof of a match.")
	}
}

type shellMatch int

const (
	shellUnknown shellMatch = iota
	shellSame
	shellDifferent
)

// sameShell compares the observed shell with a recorded one, or, when none was recorded, places its start time
// against the time the record says the address was occupied. Within one Herdr server a pane's shell never changes, so
// a shell that already ran then is the recorded occupant's and one that started later is not.
func sameShell(rec *Shell, recordedAt string, observed *Shell) shellMatch {
	if observed == nil {
		return shellUnknown
	}
	if rec != nil {
		// ps derives a start time from the boot time the kernel recomputes from the wall clock, so one process can read
		// a second or so apart over days; a pid reused within Skew of the original's start is not a realistic case.
		if rec.PID == observed.PID && closeTimes(rec.Started, observed.Started) {
			return shellSame
		}
		return shellDifferent
	}
	at, err := parseTime(recordedAt)
	if err != nil {
		return shellUnknown
	}
	started, err := parseTime(observed.Started)
	if err != nil {
		return shellUnknown
	}
	// Only a shell that already ran comfortably before the record proves the recorded occupant: a new shell read a
	// little early must never pass. A shell that started after the record is a different one; reading a legitimate
	// shell a little late only asks for a deliberate recovery.
	switch {
	case started.Before(at.Add(-Skew)):
		return shellSame
	case started.After(at):
		return shellDifferent
	}
	return shellUnknown
}

func closeTimes(a, b string) bool {
	ta, errA := parseTime(a)
	tb, errB := parseTime(b)
	if errA != nil || errB != nil {
		return false
	}
	d := ta.Sub(tb)
	return d <= Skew && d >= -Skew
}

func runProbe(probe ShellProbe) (*Shell, error) {
	if probe == nil {
		return nil, fmt.Errorf("no shell observation was taken")
	}
	shell, err := probe()
	if err == nil && shell == nil {
		err = fmt.Errorf("Herdr reported no shell pid")
	}
	return shell, err
}

func describe(s *Shell) string {
	return fmt.Sprintf("pid %d started %s", s.PID, s.Started)
}

func describeRecorded(s *Shell, at string) string {
	if s == nil {
		return "a shell that already ran at " + at
	}
	return describe(s)
}

// Call runs one bounded Herdr observation in one session and returns the decoded result, a Herdr error code, or an
// error, like herdrclient.ObserveContext.
type Call func(timeout time.Duration, args ...string) (any, string, error)

// SessionCall is a Call for one Herdr session under ctx.
func SessionCall(ctx context.Context, herdrPath, session string) Call {
	return func(timeout time.Duration, args ...string) (any, string, error) {
		return herdrclient.ObserveContext(ctx, herdrPath, session, timeout, args...)
	}
}

// ObservePane reads a pane's terminal, native session, and detected agent with one pane get. A Herdr error code
// (pane_not_found) comes back as code with a nil error.
func ObservePane(call Call, pane string) (Evidence, string, error) {
	value, code, err := call(ObserveTimeout, "pane", "get", pane)
	if err != nil || code != "" {
		return Evidence{}, code, err
	}
	return FromInfo(value), "", nil
}

// ProbeShell reads the pane shell's pid from Herdr and its start time from the OS.
func ProbeShell(call Call, pane string) (*Shell, error) {
	value, code, err := call(ObserveTimeout, "pane", "process-info", "--pane", pane)
	if err != nil {
		return nil, err
	}
	if code != "" {
		return nil, fmt.Errorf("pane process-info: %s", code)
	}
	obj, _ := value.(*ordjson.Object)
	if inner, ok := field(obj, "process_info").(*ordjson.Object); ok {
		obj = inner
	}
	pid, ok := intField(obj, "shell_pid")
	if !ok || pid <= 0 {
		return nil, fmt.Errorf("Herdr reported no shell pid")
	}
	started, err := StartTime(pid)
	if err != nil {
		return nil, err
	}
	return &Shell{PID: pid, Started: started}, nil
}

// Observe is ObservePane plus ProbeShell: the full evidence recorded when a pane is bound. A shell that cannot be read
// leaves Shell nil rather than failing the observation.
func Observe(call Call, pane string) (Evidence, string, error) {
	e, code, err := ObservePane(call, pane)
	if err != nil || code != "" {
		return e, code, err
	}
	if shell, err := ProbeShell(call, pane); err == nil {
		e.Shell = shell
	}
	return e, "", nil
}

// StartTime is a local process's start time, RFC 3339 in UTC. Tests replace it; a pid that does not exist is an error.
var StartTime = processStart

func processStart(pid int) (string, error) {
	ps, err := toolPath("ps")
	if err != nil {
		return "", err
	}
	result, err := proc.RunContext(context.Background(), proc.Cmd{
		Argv:    []string{ps, "-o", "lstart=", "-p", strconv.Itoa(pid)},
		Env:     append(os.Environ(), "LC_ALL=C", "TZ=UTC"),
		Timeout: ObserveTimeout,
	})
	if err != nil {
		return "", err
	}
	if result.Code != 0 {
		return "", fmt.Errorf("process %d is not running", pid)
	}
	return ParseStart(result.Stdout)
}

// ParseStart reads `ps -o lstart=` output taken under LC_ALL=C and TZ=UTC.
func ParseStart(text string) (string, error) {
	fields := strings.Fields(text)
	parsed, err := time.Parse("Mon Jan 2 15:04:05 2006", strings.Join(fields, " "))
	if err != nil {
		return "", fmt.Errorf("unreadable process start time %q", strings.TrimSpace(text))
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339, value)
}

var (
	runtimeMu   sync.Mutex
	runtimeRoot string
)

// UseRuntime sets the runtime root the caller check finds Herdr under, as every other helper does.
func UseRuntime(root string) {
	runtimeMu.Lock()
	runtimeRoot = root
	runtimeMu.Unlock()
}

// HerdrPath locates Herdr for the caller check.
func HerdrPath() (string, error) { return toolPath("herdr") }

func toolPath(name string) (string, error) {
	runtimeMu.Lock()
	root := runtimeRoot
	runtimeMu.Unlock()
	return toolpath.Find(root, name)
}

// Caller judges the calling pane (session, pane) against the incarnation recorded for it: one pane get, plus a shell
// probe only when the judgment needs one.
func Caller(session, pane string, recordedValue any, occupiedAt string) Verdict {
	verdict, _ := CallerObserved(session, pane, recordedValue, occupiedAt)
	return verdict
}

// CallerObserved is Caller plus the evidence it observed (with the shell when the judgment probed it), for a caller
// that refreshes a verified record.
func CallerObserved(session, pane string, recordedValue any, occupiedAt string) (Verdict, Evidence) {
	herdrPath, err := HerdrPath()
	if err != nil {
		return refused(Unobservable, "Herdr cannot be found to verify this pane: "+err.Error()), Evidence{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*ObserveTimeout)
	defer cancel()
	return PaneObserved(SessionCall(ctx, herdrPath, session), pane, recordedValue, occupiedAt)
}

// Pane observes pane through call and judges it against recordedValue.
func Pane(call Call, pane string, recordedValue any, occupiedAt string) Verdict {
	verdict, _ := PaneObserved(call, pane, recordedValue, occupiedAt)
	return verdict
}

// PaneObserved is Pane plus the evidence it observed.
func PaneObserved(call Call, pane string, recordedValue any, occupiedAt string) (Verdict, Evidence) {
	observed, code, err := ObservePane(call, pane)
	if err != nil {
		return refused(Unobservable, "Herdr cannot report this pane's occupant: "+err.Error()), observed
	}
	if code == "pane_not_found" {
		return refused(Absent, "Herdr reports pane "+pane+" as pane_not_found."), observed
	}
	if code != "" {
		return refused(Unobservable, "Herdr cannot report this pane's occupant: "+code), observed
	}
	verdict := Judge(recordedValue, occupiedAt, observed, func() (*Shell, error) {
		shell, err := ProbeShell(call, pane)
		if err == nil {
			observed.Shell = shell
		}
		return shell, err
	})
	return verdict, observed
}

// CoordinatorRecord is the incarnation that holds coordinator authority and the earliest time the owner record says
// its pane was occupied by the coordinator (its claim, context, or upgrade time). The earliest stamp is the one no later
// occupant rewrote: a registration's updated_at is restamped by every init at the address, including an older
// release's init by the occupant this check exists to refuse.
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
		t, err := parseTime(v)
		if err != nil {
			continue
		}
		if best == "" || t.Before(bestTime) {
			best, bestTime = v, t
		}
	}
	return best
}

// Recovery is the deliberate step that recovers a role refused with outcome, for messages.
func Recovery(role, outcome string) string {
	unchanged := "Tasks, decisions, reservations, and delivery history are unchanged; nothing is taken over or relaunched automatically. "
	switch {
	case outcome == Unobservable:
		return unchanged + "Rerun once Herdr can report this pane's terminal and shell."
	case role == "coordinator" && (outcome == Replaced || outcome == Absent):
		return unchanged + "If the user confirms this pane should coordinate, run `sumctl init --role coordinator --reclaim` here."
	case role == "coordinator":
		return unchanged + "The record cannot be proven or disproven from this pane; if the user confirms the recorded coordinator pane is gone, close it so Herdr reports pane_not_found, then run `sumctl init --role coordinator --reclaim`."
	case role == "worker":
		return unchanged + "After inspecting the pane, the coordinator rebinds it deliberately with `sumctl bind TASK --worker-pane PANE`."
	}
	return unchanged + "Rerun once Herdr can report the pane."
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

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func intField(obj *ordjson.Object, key string) (int, bool) {
	switch v := field(obj, key).(type) {
	case json.Number:
		n, err := v.Int64()
		return int(n), err == nil
	case float64:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}
