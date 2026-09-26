package returns

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// The coordinator wake sidecar: one versioned file per canonical coordinator recipient under <home>/deliver/, keyed
// like its recipient lock. It records the current routine wake episode (the prompt an adopted coordinator may have
// outstanding), the exact claims that episode carries, the presentation coverage a consume receipt records, and a
// bounded set of receipt fingerprints. It is notification bookkeeping only: per-task returns.json attempts stay the
// authoritative delivery record and keep their vocabulary.
//
// Lock order for every reader and writer that decides anything: delivery compatibility lock (shared) → this
// recipient's lock → state lock. The sidecar is read and written while the recipient lock is held; the state lock is
// never held across Herdr I/O. Phases, in the order one episode moves through them:
//
//	prepared      the claims to send are persisted; nothing is stamped or sent (needs reconciliation if left here)
//	claimed       the per-task in-flight attempts are stamped; nothing is sent yet          (outstanding)
//	intent        the prompt call is about to start                                          (outstanding)
//	submitted     Herdr accepted the prompt                                                  (outstanding)
//	uncertain     the prompt may have reached the recipient                                  (outstanding)
//	not-delivered the prompt provably did not reach Herdr                                    (closed)
//	not-submitted the claim sent nothing (quiet, refused, stale, deferred, busy)              (closed)
//	consumed      the coordinator recorded an exact consume receipt (`sumctl wake consume`)  (closed)
//	replaced      the recorded occupant changed; reconciled closed, it granted the new one nothing (closed)
//
// The pump writes prepared, claimed, intent and the outcome; the attempt finalize in returns.json always precedes the
// outcome write so an old reader sees an uncertain attempt before the episode says anything. Recovery of a prepared,
// claimed, intent, or uncertain episode is explicit (`sumctl wake reconcile`); no pass resends by itself.
//
// Consumption (`sumctl wake consume --boundary TOKEN`) is a presentation attestation: the coordinator records that
// one exact snapshot (the boundary's `included` identities) was rendered to it. It closes only the named episode,
// unions those identities into `covered` (withheld from later routine prompts until canonical closure prunes them),
// and appends a receipt. It answers, applies, verifies, approves, and closes nothing else; `omitted` identities and
// anything outside `included` were never covered and stay eligible for the next pass.
const (
	WakeSchema        = 1
	WakeReceiptsBound = 20

	WakePrepared     = "prepared"
	WakeClaimed      = "claimed"
	WakeIntent       = "intent"
	WakeSubmitted    = "submitted"
	WakeUncertain    = "uncertain"
	WakeNotDelivered = "not-delivered"
	WakeNotSubmitted = "not-submitted"
	WakeConsumed     = "consumed"
	WakeReplaced     = "replaced"

	// WakeAbsent, WakeOK and WakeBlocked are the read states: only an absent file means no episode.
	WakeAbsent  = "absent"
	WakeOK      = "ok"
	WakeBlocked = "blocked"

	// WakeLegacy is the pass row's `wake` value for a coordinator that has not adopted the protocol: prompting is
	// unchanged and uncoalesced, and the row says so.
	WakeLegacy = "legacy-uncoalesced"

	wakeShow      = "`sumctl wake show`"
	wakeReconcile = "`sumctl wake reconcile`"
	wakeConsume   = "`sumctl wake consume`"
)

type WakeRecipient struct {
	Machine string `json:"machine"`
	Session string `json:"session"`
	Pane    string `json:"pane"`
	Role    string `json:"role"`
}

// WakeRef names one obligation of one task, as the pass listing does.
type WakeRef struct {
	Task string `json:"task"`
	ID   string `json:"id"`
}

// WakeCovered is one presentation-consumed identity; it is pruned only after canonical closure is established.
type WakeCovered struct {
	Task     string `json:"task"`
	ID       string `json:"id"`
	Revision string `json:"revision,omitempty"`
}

type WakeReceipt struct {
	Fingerprint string `json:"fingerprint"`
	Generation  int    `json:"generation"`
	At          string `json:"at"`
	Result      string `json:"result"`
	Delivery    string `json:"delivery,omitempty"`
}

type WakeEpisode struct {
	ID         string    `json:"id"`
	Delivery   string    `json:"delivery,omitempty"`
	Phase      string    `json:"phase"`
	PreparedAt string    `json:"prepared_at"`
	Claims     []WakeRef `json:"claims"`
	IntentAt   string    `json:"intent_at,omitempty"`
	OutcomeAt  string    `json:"outcome_at,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	ConsumedAt string    `json:"consumed_at,omitempty"`
}

type Wake struct {
	Schema       int             `json:"schema"`
	Installation string          `json:"installation"`
	Recipient    WakeRecipient   `json:"recipient"`
	Incarnation  json.RawMessage `json:"incarnation"`
	Generation   int             `json:"generation"`
	Episode      *WakeEpisode    `json:"episode"`
	Covered      []WakeCovered   `json:"covered"`
	Receipts     []WakeReceipt   `json:"receipts"`
	UpdatedAt    string          `json:"updated_at"`
}

// WakeStatus is the result of reading one recipient's sidecar. A blocked read names why and how to inspect it;
// admission then submits nothing for that recipient.
type WakeStatus struct {
	State      string
	Diagnostic string
	Path       string
}

// WakeEntry is one listed sidecar, by path, for callers that do not know the endpoint (the update gate).
type WakeEntry struct {
	Path   string
	Wake   *Wake
	Status WakeStatus
}

// WakeSyncError reports a write whose rename succeeded but whose directory sync did not: the file may or may not be
// durable, so the caller reloads under its locks (CommitWake) rather than assuming nothing was committed.
type WakeSyncError struct{ Err error }

func (e *WakeSyncError) Error() string {
	return "wake sidecar renamed but its directory could not be synced: " + e.Err.Error()
}
func (e *WakeSyncError) Unwrap() error { return e.Err }

// wakeSyncDir syncs the sidecar's directory after the rename; tests inject a failure here.
var wakeSyncDir = func(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}

func (w *Wake) Endpoint() [3]string {
	return [3]string{w.Recipient.Machine, w.Recipient.Session, w.Recipient.Pane}
}

// IsOutstanding reports an episode that may have a prompt in the recipient's pane: claimed, intent, submitted, or
// uncertain. A prepared-only episode is not outstanding but needs reconciliation (NeedsReconcile).
func (w *Wake) IsOutstanding() bool {
	if w == nil || w.Episode == nil {
		return false
	}
	switch w.Episode.Phase {
	case WakeClaimed, WakeIntent, WakeSubmitted, WakeUncertain:
		return true
	}
	return false
}

func (w *Wake) NeedsReconcile() bool {
	return w != nil && w.Episode != nil && w.Episode.Phase == WakePrepared
}

// Prepare starts the next episode with the claims that are about to be sent.
func (w *Wake) Prepare(id string, claims []WakeRef) {
	w.Generation++
	if claims == nil {
		claims = []WakeRef{}
	}
	w.Episode = &WakeEpisode{ID: id, Phase: WakePrepared, PreparedAt: store.Now(), Claims: claims}
}

func (w *Wake) AddReceipt(receipt WakeReceipt) {
	w.Receipts = append(w.Receipts, receipt)
	if len(w.Receipts) > WakeReceiptsBound {
		w.Receipts = w.Receipts[len(w.Receipts)-WakeReceiptsBound:]
	}
}

// NewWake is an empty sidecar for endpoint bound to this installation and the coordinator incarnation recorded now.
func NewWake(s *store.Store, endpoint [3]string, incarnation json.RawMessage) (*Wake, error) {
	installation, err := s.Instance()
	if err != nil {
		return nil, err
	}
	if len(incarnation) == 0 {
		incarnation = json.RawMessage("null")
	}
	return &Wake{
		Schema:       WakeSchema,
		Installation: installation,
		Recipient:    WakeRecipient{Machine: endpoint[0], Session: endpoint[1], Pane: endpoint[2], Role: "coordinator"},
		Incarnation:  incarnation,
		Covered:      []WakeCovered{},
		Receipts:     []WakeReceipt{},
	}, nil
}

// IncarnationJSON encodes a recorded incarnation (an ordjson value) for the sidecar.
func IncarnationJSON(recorded any) json.RawMessage {
	encoded, err := ordjson.MarshalCompact(recorded)
	if err != nil || len(encoded) == 0 {
		return json.RawMessage("null")
	}
	return json.RawMessage(encoded)
}

// IncarnationMatches reports whether two encoded incarnations are the same record, independent of key order.
func IncarnationMatches(a, b json.RawMessage) bool {
	return canonicalJSON(a) == canonicalJSON(b)
}

func canonicalJSON(raw json.RawMessage) string {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	out, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(out)
}

// installation is this installation's instance ID as state.json records it, read once per operation; a failed
// read is carried so each sidecar check reports it at the same point a direct read would.
type installation struct {
	id  string
	err error
}

func readInstallation(s *store.Store) installation {
	id, err := s.Instance()
	return installation{id: id, err: err}
}

// ReadWake reads endpoint's sidecar. Only an absent file means no episode; anything else that is not a valid file
// for this installation and this recipient is blocked with a diagnostic.
func ReadWake(s *store.Store, endpoint [3]string) (*Wake, WakeStatus) {
	return readWake(s, readInstallation(s), endpoint)
}

func readWake(s *store.Store, inst installation, endpoint [3]string) (*Wake, WakeStatus) {
	path := s.WakePath(endpoint)
	w, status := readWakeFile(inst, path)
	if status.State != WakeOK {
		return nil, status
	}
	if w.Recipient.Role != "coordinator" || w.Endpoint() != endpoint {
		return nil, WakeStatus{State: WakeBlocked, Path: path, Diagnostic: fmt.Sprintf("%s records another recipient (%s %s on %s); inspect it with %s. Nothing is submitted to this recipient until it is resolved.", path, w.Recipient.Session, w.Recipient.Pane, w.Recipient.Machine, wakeShow)}
	}
	return w, status
}

// readWakeFile reads one sidecar by path and validates everything but the recipient.
func readWakeFile(inst installation, path string) (*Wake, WakeStatus) {
	blocked := func(msg string) (*Wake, WakeStatus) {
		return nil, WakeStatus{State: WakeBlocked, Path: path, Diagnostic: fmt.Sprintf("%s %s; inspect it with %s. Nothing is submitted to this recipient until it is resolved.", path, msg, wakeShow)}
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, WakeStatus{State: WakeAbsent, Path: path}
		}
		return blocked("cannot be read: " + err.Error())
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return blocked("is a symlink, which sum never follows for delivery state")
	}
	if info.IsDir() {
		return blocked("is a directory, not a wake sidecar")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return blocked("cannot be read: " + err.Error())
	}
	var w Wake
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&w); err != nil {
		return blocked("is not valid JSON for a wake sidecar: " + err.Error())
	}
	if w.Schema != WakeSchema {
		return blocked(fmt.Sprintf("uses wake schema %d, which this release does not support (it supports %d); sum never migrates it in place", w.Schema, WakeSchema))
	}
	if inst.err != nil {
		return blocked("cannot be checked against state.json: " + inst.err.Error())
	}
	if w.Installation != inst.id {
		return blocked(fmt.Sprintf("belongs to another installation (%q, this one is %q)", w.Installation, inst.id))
	}
	if w.Covered == nil {
		w.Covered = []WakeCovered{}
	}
	if w.Receipts == nil {
		w.Receipts = []WakeReceipt{}
	}
	return &w, WakeStatus{State: WakeOK, Path: path}
}

// WriteWake replaces w's sidecar atomically: temp file, fsync, rename, directory sync. A *WakeSyncError means the
// rename happened but its durability is unknown; CommitWake handles that by reloading.
func WriteWake(s *store.Store, w *Wake) error {
	return writeWakeAt(s.WakePath(w.Endpoint()), w)
}

func writeWakeAt(path string, w *Wake) error {
	w.UpdatedAt = store.Now()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".wake-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(append(encoded, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if err := wakeSyncDir(dir); err != nil {
		return &WakeSyncError{Err: err}
	}
	return nil
}

// CommitWake writes w and, when only the directory sync failed, reloads the sidecar under the caller's locks: the
// persisted episode and phase decide, never an assumption that nothing was committed. It returns the persisted
// record on success.
func CommitWake(s *store.Store, w *Wake) (*Wake, error) {
	err := WriteWake(s, w)
	var syncErr *WakeSyncError
	if err == nil {
		return w, nil
	}
	if !errors.As(err, &syncErr) {
		return nil, err
	}
	persisted, status := ReadWake(s, w.Endpoint())
	if status.State != WakeOK {
		return nil, fmt.Errorf("%v; reloading found %s", err, status.Diagnostic)
	}
	if persisted.Episode == nil || w.Episode == nil || persisted.Episode.ID != w.Episode.ID || persisted.Episode.Phase != w.Episode.Phase || persisted.Generation != w.Generation {
		return nil, fmt.Errorf("%v; the persisted sidecar shows episode %s phase %s, not the write's %s phase %s", err, wakeEpisodeID(persisted), wakePhase(persisted), wakeEpisodeID(w), wakePhase(w))
	}
	return persisted, nil
}

func wakeEpisodeID(w *Wake) string {
	if w == nil || w.Episode == nil {
		return ""
	}
	return w.Episode.ID
}

func wakePhase(w *Wake) string {
	if w == nil || w.Episode == nil {
		return ""
	}
	return w.Episode.Phase
}

// ListWakes lists every wake sidecar in this home, valid or not, sorted by path.
func ListWakes(s *store.Store) ([]WakeEntry, error) {
	return listWakes(s, readInstallation(s))
}

func listWakes(s *store.Store, inst installation) ([]WakeEntry, error) {
	dir := filepath.Join(s.Home, "deliver")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []WakeEntry
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), store.WakeSuffix) || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		w, status := readWakeFile(inst, path)
		out = append(out, WakeEntry{Path: path, Wake: w, Status: status})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// WakeAdopted reports whether the coordinator owner record has adopted the wake protocol this binary speaks. An
// owner record without the field was written by an older helper and is not adopted.
func WakeAdopted(owner *ordjson.Object) bool {
	if owner == nil {
		return false
	}
	v, ok := owner.Get("wake_protocol")
	if !ok {
		return false
	}
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		return err == nil && int(i) == contract.WakeProtocol
	case float64:
		return int(n) == contract.WakeProtocol
	case int:
		return n == contract.WakeProtocol
	}
	return false
}

// PruneCovered drops covered identities whose obligation is established closed: the task reads and the obligation is
// not among its open ones. Anything unreadable keeps its coverage.
func PruneCovered(s *store.Store, w *Wake) bool {
	if w == nil || len(w.Covered) == 0 {
		return false
	}
	open := map[string]map[string]bool{}
	kept := w.Covered[:0]
	changed := false
	for _, c := range w.Covered {
		ids, seen := open[c.Task]
		if !seen {
			task, err := s.ReadTask(c.Task)
			if err == nil {
				obligations, oErr := OpenObligations(s, task)
				if oErr == nil {
					ids = map[string]bool{}
					for _, o := range obligations {
						id, _ := o.Get("id")
						ids[fmt.Sprint(id)] = true
					}
				}
			}
			open[c.Task] = ids
		}
		if ids != nil && !ids[c.ID] {
			changed = true
			continue
		}
		kept = append(kept, c)
	}
	w.Covered = kept
	return changed
}

// wakeView is the row's `wake` object for a valid sidecar.
func wakeView(w *Wake, admission string) *ordjson.Object {
	view := ordjson.NewObject()
	view.Set("admission", admission)
	if w == nil {
		view.Set("episode", nil)
		view.Set("generation", jsonInt(0))
		view.Set("phase", nil)
		return view
	}
	view.Set("episode", wakeEpisodeID(w))
	view.Set("generation", jsonInt(w.Generation))
	if w.Episode == nil {
		view.Set("phase", nil)
	} else {
		view.Set("phase", w.Episode.Phase)
	}
	return view
}

// wakeAdmission is one pass's decision for one coordinator recipient, taken under the compatibility (shared) and
// recipient locks before anything is claimed.
type wakeAdmission struct {
	decision   string
	reason     string
	wake       *Wake
	recorded   any
	superseded string // the outstanding episode an explicit forced notice replaces
	// pending is the episode a forced notice will open in place of the superseded one. The outstanding episode is
	// left untouched (its boundary stays valid) until the new claims are stamped; only then is it superseded.
	pending   *WakeEpisode
	committed bool // pending replaced the superseded episode
}

const (
	wakeDecisionLegacy    = "legacy-uncoalesced" // owner not adopted: unchanged prompting, disclosed
	wakeDecisionBlocked   = "blocked"            // invalid sidecar, or an episode that needs reconciliation
	wakeDecisionCoalesced = "coalesced"          // an episode is outstanding; nothing new is sent
	wakeDecisionPermitted = "permitted"          // a new episode may be prepared
)

func (a *wakeAdmission) view() *ordjson.Object {
	if a.decision == wakeDecisionLegacy {
		view := ordjson.NewObject()
		view.Set("admission", WakeLegacy)
		return view
	}
	view := wakeView(a.wake, a.decision)
	if a.superseded != "" {
		if a.committed {
			view.Set("superseded", a.superseded)
		} else {
			view.Set("kept", a.superseded)
		}
	}
	return view
}

// admitWake decides the coordinator recipient of route. Both the runtime (this binary, by construction) and the owner
// record must speak the protocol; the record is read now, under the compatibility lock, so a helper that started
// before a runtime switch decides from the record the switch left.
func (p *pass) admitWake(route *ordjson.Object, force bool) (*wakeAdmission, error) {
	owner, err := p.s.Owner()
	if err != nil {
		return nil, err
	}
	if !WakeAdopted(owner) {
		return &wakeAdmission{decision: wakeDecisionLegacy}, nil
	}
	recorded, _ := incarnation.CoordinatorRecord(owner)
	endpoint := identity(p.host, route)
	w, status := ReadWake(p.s, endpoint)
	switch status.State {
	case WakeBlocked:
		return &wakeAdmission{decision: wakeDecisionBlocked, reason: status.Diagnostic}, nil
	case WakeAbsent:
		if w, err = NewWake(p.s, endpoint, IncarnationJSON(recorded)); err != nil {
			return nil, err
		}
	}
	adm := &wakeAdmission{wake: w, recorded: recorded}
	if !IncarnationMatches(w.Incarnation, IncarnationJSON(recorded)) && len(w.Covered) > 0 {
		// Coverage was consumed by another occupant; a new occupant inherits no consumption authority, so nothing
		// is withheld from it. Persisted with the next episode (or by reconcile), never by itself.
		w.Covered = []WakeCovered{}
	}
	switch {
	case w.NeedsReconcile():
		adm.decision = wakeDecisionBlocked
		adm.reason = fmt.Sprintf("routine wake %s (generation %d) was prepared and never claimed; nothing is sent to this coordinator until it is reconciled with %s (inspect it with %s)", w.Episode.ID, w.Generation, wakeReconcile, wakeShow)
	case w.IsOutstanding() && !IncarnationMatches(w.Incarnation, IncarnationJSON(recorded)):
		adm.decision = wakeDecisionBlocked
		adm.reason = fmt.Sprintf("routine wake %s (generation %d, %s) is outstanding for a coordinator occupant that is no longer the recorded one; the old episode grants the new occupant nothing, so nothing is sent until it is reconciled with %s (inspect it with %s)", w.Episode.ID, w.Generation, w.Episode.Phase, wakeReconcile, wakeShow)
	case w.IsOutstanding() && force:
		// `sumctl notice --to parent` is the documented explicit single retry: it is not a routine arrival, so it
		// supersedes the outstanding episode with the next generation rather than waiting behind it. The supersede
		// is committed only once the new claims are stamped (claimed); until then the outstanding episode is kept.
		adm.decision = wakeDecisionPermitted
		adm.superseded = w.Episode.ID
		PruneCovered(p.s, w)
	case w.IsOutstanding():
		adm.decision = wakeDecisionCoalesced
		adm.reason = fmt.Sprintf("routine wake %s (generation %d, %s) is outstanding for this coordinator; new returns accumulate behind it in their own delivery state until it is consumed (`sumctl wake consume`) or inspected (%s). Nothing was sent or stamped.", w.Episode.ID, w.Generation, w.Episode.Phase, wakeShow)
		if PruneCovered(p.s, w) {
			if _, err := CommitWake(p.s, w); err != nil {
				adm.reason += " Coverage pruning was not persisted: " + err.Error()
			}
		}
	default:
		adm.decision = wakeDecisionPermitted
		PruneCovered(p.s, w)
	}
	return adm, nil
}

func wakeRefs(items [][2]*ordjson.Object) []WakeRef {
	refs := []WakeRef{}
	for _, pair := range items {
		k := pairKey(pair)
		refs = append(refs, WakeRef{Task: k[0], ID: k[1]})
	}
	return refs
}

// prepare persists the next episode with the claims about to be sent and the delivery id the claim will stamp; a
// non-empty result is why nothing may proceed. A forced notice over an outstanding episode prepares nothing durable:
// the new episode is held on the admission and replaces the outstanding one only in claimed, so a claim that never
// happens leaves the outstanding episode and its boundary exactly as they were.
func (a *wakeAdmission) prepare(s *store.Store, items [][2]*ordjson.Object, deliveryID string) string {
	id, err := newID("w-")
	if err != nil {
		return "the wake episode could not be identified: " + err.Error() + "; nothing was sent or recorded"
	}
	if a.superseded != "" {
		a.pending = &WakeEpisode{ID: id, Delivery: deliveryID, Phase: WakePrepared, PreparedAt: store.Now(), Claims: wakeRefs(items)}
		return ""
	}
	a.wake.Incarnation = IncarnationJSON(a.recorded)
	a.wake.Prepare(id, wakeRefs(items))
	a.wake.Episode.Delivery = deliveryID
	if _, err := CommitWake(s, a.wake); err != nil {
		return "the wake episode could not be persisted before the prompt: " + err.Error() + "; nothing was sent or recorded. Inspect it with " + wakeShow
	}
	return ""
}

// claimed records the delivery and the surviving claims once the in-flight attempts are stamped (state lock held).
// A pending forced episode supersedes the outstanding one here: the old episode leaves a receipt carrying its
// delivery (so `wake show` keeps accounting for its prompt) and the new one takes the next generation.
func (a *wakeAdmission) claimed(s *store.Store, deliveryID string, survivors [][2]*ordjson.Object) error {
	if a.pending != nil {
		old := a.wake.Episode
		a.wake.AddReceipt(WakeReceipt{Generation: a.wake.Generation, At: store.Now(), Result: "superseded", Delivery: old.Delivery})
		a.wake.Prepare(a.pending.ID, a.pending.Claims)
		a.wake.Episode.PreparedAt = a.pending.PreparedAt
		a.committed = true
	}
	a.wake.Episode.Delivery = deliveryID
	a.wake.Episode.Claims = wakeRefs(survivors)
	a.wake.Episode.Phase = WakeClaimed
	_, err := CommitWake(s, a.wake)
	return err
}

// intent is written immediately before the prompt call.
func (a *wakeAdmission) intent(s *store.Store) error {
	a.wake.Episode.Phase = WakeIntent
	a.wake.Episode.IntentAt = store.Now()
	_, err := CommitWake(s, a.wake)
	return err
}

// outcome closes or settles the episode after the attempt was finalized. A failed write leaves the persisted phase
// (at worst intent, still outstanding) and is disclosed on the row rather than failing the pass.
func (a *wakeAdmission) outcome(s *store.Store, row *ordjson.Object, phase, reason string) {
	if a.pending != nil && !a.committed {
		// The forced notice never claimed: the outstanding episode is untouched and its boundary stays valid.
		row.Set("wake", a.view())
		return
	}
	a.wake.Episode.Phase = phase
	a.wake.Episode.Reason = reason
	a.wake.Episode.OutcomeAt = store.Now()
	if _, err := CommitWake(s, a.wake); err != nil {
		row.Set("wake_error", err.Error())
	}
	row.Set("wake", a.view())
}

// Boundary is the exact snapshot a coordinator attests it rendered: this installation, the recipient pane, the
// occupant the sidecar recorded, the episode and generation, and the explicit coverage. `Included` is every identity
// the reader rendered; `Omitted` is every identity it knew it left out (an unreadable task, a page it did not show).
// Both are explicit so a consume can never claim more than was shown. The token is opaque to callers but
// self-checking: its payload is bound to a fingerprint that the receipt keeps.
type Boundary struct {
	Schema       int             `json:"schema"`
	Installation string          `json:"installation"`
	Recipient    WakeRecipient   `json:"recipient"`
	Incarnation  json.RawMessage `json:"incarnation"`
	Episode      string          `json:"episode"`
	Generation   int             `json:"generation"`
	Included     []WakeCovered   `json:"included"`
	Omitted      []WakeCovered   `json:"omitted"`
}

// BuildBoundary binds the coverage a reader rendered to w's current episode.
func BuildBoundary(s *store.Store, w *Wake, included, omitted []WakeCovered) (*Boundary, error) {
	return buildBoundary(readInstallation(s), w, included, omitted)
}

func buildBoundary(inst installation, w *Wake, included, omitted []WakeCovered) (*Boundary, error) {
	if w == nil || w.Episode == nil {
		return nil, fmt.Errorf("no wake episode to bound")
	}
	if inst.err != nil {
		return nil, inst.err
	}
	return &Boundary{
		Schema:       WakeSchema,
		Installation: inst.id,
		Recipient:    w.Recipient,
		Incarnation:  json.RawMessage(canonicalJSON(w.Incarnation)),
		Episode:      w.Episode.ID,
		Generation:   w.Generation,
		Included:     normalizeCovered(included),
		Omitted:      normalizeCovered(omitted),
	}, nil
}

func normalizeCovered(items []WakeCovered) []WakeCovered {
	out := []WakeCovered{}
	seen := map[[3]string]bool{}
	for _, c := range items {
		k := [3]string{c.Task, c.ID, c.Revision}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Task != out[j].Task {
			return out[i].Task < out[j].Task
		}
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Revision < out[j].Revision
	})
	return out
}

func (b *Boundary) canonical() []byte {
	c := *b
	c.Incarnation = json.RawMessage(canonicalJSON(b.Incarnation))
	c.Included = normalizeCovered(b.Included)
	c.Omitted = normalizeCovered(b.Omitted)
	encoded, _ := json.Marshal(&c)
	return encoded
}

// Fingerprint identifies this exact boundary; the receipt records it.
func (b *Boundary) Fingerprint() string {
	sum := sha256.Sum256(b.canonical())
	return hex.EncodeToString(sum[:])[:16]
}

// Token is the opaque form: base64url(payload) + "." + fingerprint.
func (b *Boundary) Token() (string, error) {
	payload := b.canonical()
	if len(payload) == 0 {
		return "", fmt.Errorf("boundary could not be encoded")
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." + b.Fingerprint(), nil
}

// ParseBoundary decodes a token and verifies its fingerprint; anything else is unverifiable and consumes nothing.
func ParseBoundary(token string) (*Boundary, error) {
	unverifiable := func(why string) (*Boundary, error) {
		return nil, fmt.Errorf("The boundary token is not verifiable (%s); nothing was consumed. Take a fresh one from %s.", why, wakeShow)
	}
	payload, fingerprint, ok := strings.Cut(strings.TrimSpace(token), ".")
	if !ok || payload == "" || fingerprint == "" {
		return unverifiable("not a boundary token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return unverifiable("payload is not base64url")
	}
	var b Boundary
	if err := json.Unmarshal(raw, &b); err != nil {
		return unverifiable("payload is not a boundary")
	}
	if b.Schema != WakeSchema {
		return unverifiable(fmt.Sprintf("boundary schema %d is not supported", b.Schema))
	}
	if b.Included == nil {
		b.Included = []WakeCovered{}
	}
	if b.Omitted == nil {
		b.Omitted = []WakeCovered{}
	}
	if b.Fingerprint() != fingerprint {
		return unverifiable("fingerprint does not match the payload")
	}
	return &b, nil
}

// wakeLockWait bounds how long consume and reconcile wait for a pass that holds this recipient's lock.
var wakeLockWait = 2 * DefaultPassBudget

// lockWakeRecipient takes the delivery compatibility lock shared, the recipient lock, then the state lock: the same
// order every pass uses. The caller releases all three through the returned function.
func lockWakeRecipient(s *store.Store, endpoint [3]string) (func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), wakeLockWait)
	defer cancel()
	unlockRecipient, err := lockRecipient(s, ctx, ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("another operation is delivering to this coordinator; nothing was changed. Retry after it finishes: %v", err)
	}
	unlockState, err := s.Lock()
	if err != nil {
		unlockRecipient()
		return nil, err
	}
	return func() {
		_ = unlockState()
		unlockRecipient()
	}, nil
}

func callerEndpoint(s *store.Store, ctx *ordjson.Object) ([3]string, error) {
	host, err := s.Machine()
	if err != nil {
		return [3]string{}, err
	}
	return identity(host, ctx), nil
}

// ownerOccupies reports whether the owner record names endpoint and records the same coordinator incarnation the
// sidecar was bound to; false means the occupant changed and the sidecar's episode grants the current one nothing.
func ownerOccupies(s *store.Store, w *Wake, endpoint [3]string) (bool, error) {
	owner, err := s.Owner()
	if err != nil {
		return false, err
	}
	if owner == nil {
		return false, nil
	}
	host, err := s.Machine()
	if err != nil {
		return false, err
	}
	if identity(host, owner) != endpoint {
		return false, nil
	}
	recorded, _ := incarnation.CoordinatorRecord(owner)
	return IncarnationMatches(w.Incarnation, IncarnationJSON(recorded)), nil
}

func coveredList(items []WakeCovered) []any {
	out := []any{}
	for _, c := range items {
		row := ordjson.NewObject()
		row.Set("task", c.Task)
		row.Set("id", c.ID)
		if c.Revision != "" {
			row.Set("revision", c.Revision)
		}
		out = append(out, row)
	}
	return out
}

const consumeNote = "Consumption is a presentation attestation: it records that this exact snapshot was rendered to the coordinator. It answers, applies, verifies, approves, and closes nothing; obligations outside the included list, including the omitted ones, were never covered and stay eligible for the next pass."

// Consume records the coordinator's exact consume receipt for the boundary in token. Coordinator-only, judged from
// the caller's Herdr context as `attention --seen` is; the caller must be the boundary's recipient and the occupant
// the sidecar recorded. Under compat(shared) → recipient → state locks it re-reads the sidecar and decides:
//
//	an exact repeat of a retained receipt  → repeated, no write (after later episodes and pruning too)
//	an older generation, not receipted    → expired, no write; it never adds coverage or closes the current episode
//	consumed under another fingerprint    → refused: altered coverage under an already-consumed identity
//	prepared (nothing submitted)          → refused: reconcile it
//	closed otherwise                       → refused: nothing outstanding
//	claimed | intent | submitted | uncertain, current generation → consumed: coverage ∪ included (valid identities
//	  only, unknown ones listed under rejected), phase consumed, one receipt, one file replacement
//
// It never touches returns.json attempts, closes no question, report, or review, and changes no reservation.
func Consume(s *store.Store, ctx *ordjson.Object, token string) (*ordjson.Object, error) {
	b, err := ParseBoundary(token)
	if err != nil {
		return nil, err
	}
	inst := readInstallation(s)
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	caller, err := callerEndpoint(s, ctx)
	if err != nil {
		return nil, err
	}
	host, _ := s.Machine()
	target := [3]string{host.Canonical(b.Recipient.Machine), b.Recipient.Session, b.Recipient.Pane}
	if target != caller {
		return nil, fmt.Errorf("The boundary names another recipient (%s %s on %s); only that coordinator pane may consume it. Nothing was consumed.", b.Recipient.Session, b.Recipient.Pane, b.Recipient.Machine)
	}
	unlock, err := lockWakeRecipient(s, caller)
	if err != nil {
		return nil, err
	}
	defer unlock()
	w, status := readWake(s, inst, caller)
	switch status.State {
	case WakeBlocked:
		return nil, fmt.Errorf("Nothing was consumed: %s", status.Diagnostic)
	case WakeAbsent:
		return nil, fmt.Errorf("No wake episode is recorded for this coordinator; nothing was consumed.")
	}
	if b.Installation != w.Installation {
		return nil, fmt.Errorf("The boundary belongs to another installation; nothing was consumed.")
	}
	if !IncarnationMatches(b.Incarnation, w.Incarnation) {
		return nil, fmt.Errorf("The boundary was taken for another coordinator occupant; a new occupant does not inherit old consumption authority. Nothing was consumed; take a fresh boundary from %s.", wakeShow)
	}
	same, err := ownerOccupies(s, w, caller)
	if err != nil {
		return nil, err
	}
	if !same {
		return nil, fmt.Errorf("The recorded coordinator occupant is no longer the one this wake was bound to; a new occupant does not inherit old consumption authority. Nothing was consumed; reconcile it with %s.", wakeReconcile)
	}
	fingerprint := b.Fingerprint()
	view := ordjson.NewObject()
	finish := func(result string) *ordjson.Object {
		view.Set("result", result)
		view.Set("episode", wakeEpisodeID(w))
		view.Set("generation", jsonInt(w.Generation))
		view.Set("phase", wakePhase(w))
		if w.Episode != nil {
			view.Set("delivery", w.Episode.Delivery)
		}
		view.Set("boundary_generation", jsonInt(b.Generation))
		view.Set("fingerprint", fingerprint)
		view.Set("covered_count", jsonInt(len(w.Covered)))
		view.Set("included", coveredList(b.Included))
		view.Set("omitted", coveredList(b.Omitted))
		if _, has := view.Get("rejected"); !has {
			view.Set("rejected", []any{})
		}
		view.Set("receipts", jsonInt(len(w.Receipts)))
		view.Set("note", consumeNote)
		return view
	}
	for _, r := range w.Receipts {
		// Superseded and replaced receipts carry no fingerprint: they account for a delivery, not a consume.
		if r.Fingerprint == "" || r.Fingerprint != fingerprint {
			continue
		}
		view.Set("prior_result", r.Result)
		view.Set("prior_at", r.At)
		return finish("repeated"), nil
	}
	if b.Generation < w.Generation {
		view.Set("reason", fmt.Sprintf("the boundary is generation %d and the current episode is generation %d; an older receipt consumes nothing and adds no coverage", b.Generation, w.Generation))
		return finish("expired"), nil
	}
	if b.Generation > w.Generation || w.Episode == nil || b.Episode != w.Episode.ID {
		return nil, fmt.Errorf("The boundary names episode %s generation %d, which this coordinator's sidecar does not record (current: %s generation %d); nothing was consumed. Take a fresh boundary from %s.", b.Episode, b.Generation, wakeEpisodeID(w), w.Generation, wakeShow)
	}
	switch w.Episode.Phase {
	case WakeConsumed:
		return nil, fmt.Errorf("Episode %s is already consumed under another receipt; altered coverage under an already-consumed identity is refused. Nothing was consumed; the next pass covers new work with a new episode.", w.Episode.ID)
	case WakePrepared:
		return nil, fmt.Errorf("Episode %s was prepared and never claimed: nothing was submitted, so there is nothing to consume. Reconcile it with %s.", w.Episode.ID, wakeReconcile)
	case WakeClaimed, WakeIntent, WakeSubmitted, WakeUncertain:
	default:
		return nil, fmt.Errorf("Episode %s is closed (%s); nothing is outstanding to consume.", w.Episode.ID, w.Episode.Phase)
	}
	// Coverage: only identities that are open obligations of a readable task owed to this coordinator.
	open := map[string]map[string]bool{}
	openIDs := func(taskID string) (map[string]bool, string) {
		if ids, seen := open[taskID]; seen {
			if ids == nil {
				return nil, "task record is unreadable"
			}
			return ids, ""
		}
		task, err := s.ReadTask(taskID)
		if err != nil {
			open[taskID] = nil
			return nil, "task record is unreadable: " + err.Error()
		}
		obligations, err := OpenObligations(s, task)
		if err != nil {
			open[taskID] = nil
			return nil, "obligations are unreadable: " + err.Error()
		}
		ids := map[string]bool{}
		for _, o := range obligations {
			recipient, _ := o.Get("recipient")
			if identity(host, ReturnRoute(task, fmt.Sprint(recipient))) != caller {
				continue
			}
			id, _ := o.Get("id")
			ids[fmt.Sprint(id)] = true
		}
		open[taskID] = ids
		return ids, ""
	}
	rejected := []any{}
	existing := map[[2]string]bool{}
	for _, c := range w.Covered {
		existing[[2]string{c.Task, c.ID}] = true
	}
	for _, c := range b.Included {
		ids, why := openIDs(c.Task)
		if why == "" && !ids[c.ID] {
			why = "not an open obligation of that task owed to this coordinator"
		}
		if why != "" {
			row := ordjson.NewObject()
			row.Set("task", c.Task)
			row.Set("id", c.ID)
			row.Set("reason", why)
			rejected = append(rejected, row)
			continue
		}
		if existing[[2]string{c.Task, c.ID}] {
			continue
		}
		existing[[2]string{c.Task, c.ID}] = true
		w.Covered = append(w.Covered, c)
	}
	now := store.Now()
	w.Episode.Phase = WakeConsumed
	w.Episode.ConsumedAt = now
	w.AddReceipt(WakeReceipt{Fingerprint: fingerprint, Generation: w.Generation, At: now, Result: "consumed", Delivery: w.Episode.Delivery})
	persisted, err := CommitWake(s, w)
	if err != nil {
		return nil, fmt.Errorf("the receipt could not be persisted: %v", err)
	}
	w = persisted
	view.Set("rejected", rejected)
	return finish("consumed"), nil
}

// Reconcile settles one coordinator's episode conservatively, per the wake recovery table. It never resends, never
// closes an episode because a PID vanished or time passed, and never rewrites an unreadable file. recipientKey names
// a sidecar by its file key (`sumctl wake show` lists them); empty means the caller's own pane.
//
//	blocked / absent / no episode / closed  → nothing, reported
//	occupant replaced (owner record no longer names this pane's recorded occupant), episode prepared or outstanding
//	                                        → replaced (closed): the old episode reaches no one and grants nothing; attempts kept
//	prepared                                → not-submitted: prepared but never claimed; the next pass may prompt
//	claimed | intent                        → uncertain (outstanding): the attempts stay as they are; consume is the way out
//	submitted | uncertain                   → unchanged; consume is the way out
func Reconcile(s *store.Store, ctx *ordjson.Object, recipientKey string) (*ordjson.Object, error) {
	inst := readInstallation(s)
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	caller, err := callerEndpoint(s, ctx)
	if err != nil {
		return nil, err
	}
	path := s.WakePath(caller)
	if recipientKey != "" {
		if strings.ContainsAny(recipientKey, "/\\.") {
			return nil, fmt.Errorf("--recipient takes a sidecar key as %s lists it", wakeShow)
		}
		path = filepath.Join(s.Home, "deliver", recipientKey+store.WakeSuffix)
	}
	view := ordjson.NewObject()
	view.Set("path", path)
	report := func(status WakeStatus, w *Wake, action, note string) *ordjson.Object {
		view.Set("status", status.State)
		if status.Diagnostic != "" {
			view.Set("diagnostic", status.Diagnostic)
		}
		if w != nil {
			view.Set("recipient", recipientView(w))
			view.Set("generation", jsonInt(w.Generation))
			view.Set("episode", wakeEpisodeID(w))
			if w.Episode != nil {
				view.Set("delivery", w.Episode.Delivery)
			}
		}
		if _, has := view.Get("before"); !has {
			view.Set("before", wakePhase(w))
		}
		view.Set("after", wakePhase(w))
		view.Set("action", action)
		view.Set("remaining", note)
		view.Set("note", "Reconciliation never resends and never clears an episode because a process disappeared or time passed; attempts in returns.json are untouched.")
		return view
	}
	// The endpoint to lock comes from the file; a first unlocked read learns it, and the decision re-reads under
	// the locks.
	w, status := readWakeFile(inst, path)
	switch status.State {
	case WakeAbsent:
		return report(status, nil, "none", "no wake sidecar; nothing to reconcile"), nil
	case WakeBlocked:
		return report(status, nil, "none", "the file is unreadable or foreign; inspect it by path, sum never rewrites it"), nil
	}
	endpoint := w.Endpoint()
	unlock, err := lockWakeRecipient(s, endpoint)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if w, status = readWakeFile(inst, path); status.State != WakeOK {
		return report(status, nil, "none", "the file changed while the locks were taken; run it again"), nil
	}
	view.Set("before", wakePhase(w))
	if w.Episode == nil {
		return report(status, w, "none", "no episode recorded"), nil
	}
	same, err := ownerOccupies(s, w, endpoint)
	if err != nil {
		return nil, err
	}
	commit := func(phase, reason, action, note string) (*ordjson.Object, error) {
		w.Episode.Phase = phase
		w.Episode.Reason = reason
		w.Episode.OutcomeAt = store.Now()
		if _, err := CommitWake(s, w); err != nil {
			return nil, err
		}
		return report(status, w, action, note), nil
	}
	if !w.IsOutstanding() && !w.NeedsReconcile() {
		return report(status, w, "none", fmt.Sprintf("episode %s is closed (%s); nothing is outstanding", w.Episode.ID, w.Episode.Phase)), nil
	}
	if !same {
		// The new occupant inherits neither the old occupant's coverage nor its episode; the receipt keeps the old
		// prompt accounted for in `wake show` without granting anything.
		w.Covered = []WakeCovered{}
		w.AddReceipt(WakeReceipt{Generation: w.Generation, At: store.Now(), Result: WakeReplaced, Delivery: w.Episode.Delivery})
		return commit(WakeReplaced, "the recorded coordinator occupant changed; the episode reaches no one and grants the new occupant nothing; reconciled", "closed",
			"the old occupant's attempts in returns.json are preserved as they are; the next pass decides afresh for the recorded coordinator")
	}
	switch w.Episode.Phase {
	case WakePrepared:
		if stamped, err := deliveryStamped(s, w.Episode); err != nil {
			return nil, err
		} else if stamped {
			return commit(WakeUncertain, "interrupted between the attempt stamp and the claim; the prompt was not sent", "held",
				fmt.Sprintf("delivery %s's attempts read uncertain; %s or a forced notice (`sumctl notice TASK --to parent`) settles it; nothing is resent", w.Episode.Delivery, wakeConsume))
		}
		return commit(WakeNotSubmitted, "prepared but never claimed; reconciled", "closed", "nothing was stamped or sent; the next pass may prompt for the same returns")
	case WakeClaimed, WakeIntent:
		return commit(WakeUncertain, fmt.Sprintf("interrupted at %s; the prompt may have reached the recipient", w.Episode.Phase), "held",
			fmt.Sprintf("delivery %s's attempts stay in-flight and read as uncertain; the episode is outstanding until the coordinator consumes its boundary (%s, then %s); nothing is resent", w.Episode.Delivery, wakeShow, wakeConsume))
	}
	return report(status, w, "none", fmt.Sprintf("episode %s is %s and outstanding; %s after the coordinator has read its wake is the way out (%s shows the boundary), or `sumctl notice TASK --to parent` supersedes it explicitly", w.Episode.ID, w.Episode.Phase, wakeConsume, wakeShow)), nil
}

// deliveryStamped reports whether any claimed task's returns.json carries an attempt with the episode's delivery id:
// the claim stamped before it could record itself.
func deliveryStamped(s *store.Store, episode *WakeEpisode) (bool, error) {
	if episode.Delivery == "" {
		return false, nil
	}
	seen := map[string]bool{}
	for _, c := range episode.Claims {
		if seen[c.Task] {
			continue
		}
		seen[c.Task] = true
		returnsObj, err := ReadReturns(s, c.Task)
		if err != nil {
			return false, err
		}
		list, _ := returnsObj.Get("deliveries")
		entries, _ := list.([]any)
		for _, d := range entries {
			obj, _ := d.(*ordjson.Object)
			if obj == nil {
				continue
			}
			if id, _ := obj.Get("id"); fmt.Sprint(id) == episode.Delivery {
				return true, nil
			}
		}
	}
	return false, nil
}

func recipientView(w *Wake) *ordjson.Object {
	row := ordjson.NewObject()
	row.Set("machine", w.Recipient.Machine)
	row.Set("session", w.Recipient.Session)
	row.Set("pane", w.Recipient.Pane)
	row.Set("role", w.Recipient.Role)
	row.Set("key", store.RegistrationKey(store.Endpoint{Machine: w.Recipient.Machine, Session: w.Recipient.Session, Pane: w.Recipient.Pane}))
	return row
}

// Show is the read-only view of every wake sidecar (or the one recipientKey names): status, episode, generation,
// coverage and receipt counts, and for an outstanding episode the boundary token built from the CURRENT open
// obligations to that coordinator (included = every readable open obligation owed to it; omitted = every task whose
// record or obligations could not be read, with the gap reason). `uncoalesced_legacy_prompts` lists prompt attempts
// to that recipient in returns.json that no episode accounts for: prompts an older or non-adopted helper sent.
// It writes nothing.
func Show(s *store.Store, recipientKey string) (*ordjson.Object, error) {
	inst := readInstallation(s)
	entries, err := listWakes(s, inst)
	if err != nil {
		return nil, err
	}
	host, err := s.Machine()
	if err != nil {
		return nil, err
	}
	tasks, gaps := readTasksForShow(s, host)
	rows := []any{}
	for _, entry := range entries {
		key := strings.TrimSuffix(filepath.Base(entry.Path), store.WakeSuffix)
		if recipientKey != "" && key != recipientKey {
			continue
		}
		row := ordjson.NewObject()
		row.Set("key", key)
		row.Set("path", entry.Path)
		row.Set("status", entry.Status.State)
		if entry.Status.State != WakeOK {
			row.Set("diagnostic", entry.Status.Diagnostic)
			row.Set("next", wakeReconcile+" reports it; the user inspects the file by path")
			rows = append(rows, row)
			continue
		}
		w := entry.Wake
		row.Set("recipient", recipientView(w))
		row.Set("generation", jsonInt(w.Generation))
		row.Set("episode", episodeView(w))
		row.Set("outstanding", w.IsOutstanding())
		row.Set("needs_reconcile", w.NeedsReconcile())
		row.Set("covered_count", jsonInt(len(w.Covered)))
		row.Set("covered", coveredList(w.Covered))
		row.Set("receipts_count", jsonInt(len(w.Receipts)))
		accounted := map[string]bool{}
		if w.Episode != nil && w.Episode.Delivery != "" {
			accounted[w.Episode.Delivery] = true
		}
		for _, r := range w.Receipts {
			if r.Delivery != "" {
				accounted[r.Delivery] = true
			}
		}
		endpoint := w.Endpoint()
		included, omitted := currentCoverage(host, endpoint, tasks, gaps)
		row.Set("included", coveredList(included))
		row.Set("omitted", omittedList(omitted))
		switch {
		case w.IsOutstanding():
			b, err := buildBoundary(inst, w, included, omittedCovered(omitted))
			if err != nil {
				return nil, err
			}
			token, err := b.Token()
			if err != nil {
				return nil, err
			}
			row.Set("boundary", token)
			row.Set("next", wakeConsume+" --boundary TOKEN after reading the included work; it covers exactly `included`")
		case w.NeedsReconcile():
			row.Set("next", wakeReconcile+": the episode was prepared and never claimed")
		default:
			row.Set("next", "nothing outstanding; the next pass may open a new episode")
		}
		row.Set("uncoalesced_legacy_prompts", legacyPrompts(host, endpoint, tasks, accounted))
		rows = append(rows, row)
	}
	view := ordjson.NewObject()
	view.Set("recipients", rows)
	view.Set("note", "Read-only: nothing is consumed, sent, or reconciled by showing it. A boundary covers exactly its included identities; omitted ones and later arrivals stay eligible.")
	return view, nil
}

func episodeView(w *Wake) any {
	if w.Episode == nil {
		return nil
	}
	e := ordjson.NewObject()
	e.Set("id", w.Episode.ID)
	e.Set("phase", w.Episode.Phase)
	e.Set("delivery", w.Episode.Delivery)
	e.Set("prepared_at", w.Episode.PreparedAt)
	claims := []any{}
	for _, c := range w.Episode.Claims {
		ref := ordjson.NewObject()
		ref.Set("task", c.Task)
		ref.Set("id", c.ID)
		claims = append(claims, ref)
	}
	e.Set("claims", claims)
	if w.Episode.IntentAt != "" {
		e.Set("intent_at", w.Episode.IntentAt)
	}
	if w.Episode.OutcomeAt != "" {
		e.Set("outcome_at", w.Episode.OutcomeAt)
	}
	if w.Episode.ConsumedAt != "" {
		e.Set("consumed_at", w.Episode.ConsumedAt)
	}
	if w.Episode.Reason != "" {
		e.Set("reason", w.Episode.Reason)
	}
	return e
}

type coverageGap struct {
	task   string
	reason string
}

// showTask is one local task as Show reads it: its record, open obligations (or why they could not be
// established), and recorded deliveries, read once and filtered per recipient.
type showTask struct {
	id             string
	task           *ordjson.Object
	obligations    []*ordjson.Object
	obligationsErr error
	deliveries     []any
}

// readTasksForShow reads every local task record one by one, so one unreadable record is a named gap rather than
// a failed view.
func readTasksForShow(s *store.Store, host machine.Identity) ([]showTask, []coverageGap) {
	paths, _ := filepath.Glob(filepath.Join(s.Tasks, "t-*", "task.json"))
	sort.Strings(paths)
	var tasks []showTask
	var gaps []coverageGap
	for _, path := range paths {
		id := filepath.Base(filepath.Dir(path))
		task, err := s.ReadTask(id)
		if err != nil {
			gaps = append(gaps, coverageGap{task: id, reason: "task record is unreadable: " + err.Error()})
			continue
		}
		if machineValue, _ := task.Get("machine"); !host.Is(machineValue) {
			continue
		}
		idValue, _ := task.Get("id")
		st := showTask{id: fmt.Sprint(idValue), task: task}
		st.obligations, st.obligationsErr = OpenObligations(s, task)
		if returnsObj, err := ReadReturns(s, st.id); err == nil {
			deliveriesValue, _ := returnsObj.Get("deliveries")
			st.deliveries, _ = deliveriesValue.([]any)
		}
		tasks = append(tasks, st)
	}
	return tasks, gaps
}

// currentCoverage lists the open obligations owed to endpoint's coordinator now, and the tasks whose obligations
// could not be established.
func currentCoverage(host machine.Identity, endpoint [3]string, tasks []showTask, gaps []coverageGap) ([]WakeCovered, []coverageGap) {
	var included []WakeCovered
	omitted := append([]coverageGap{}, gaps...)
	for _, t := range tasks {
		if t.obligationsErr != nil {
			omitted = append(omitted, coverageGap{task: t.id, reason: "obligations are unreadable: " + t.obligationsErr.Error()})
			continue
		}
		for _, o := range t.obligations {
			recipient, _ := o.Get("recipient")
			if identity(host, ReturnRoute(t.task, fmt.Sprint(recipient))) != endpoint {
				continue
			}
			id, _ := o.Get("id")
			c := WakeCovered{Task: t.id, ID: fmt.Sprint(id)}
			if ref, ok := o.Get("ref"); ok && ref != nil {
				c.Revision = fmt.Sprint(ref)
			}
			included = append(included, c)
		}
	}
	return normalizeCovered(included), omitted
}

func omittedCovered(gaps []coverageGap) []WakeCovered {
	out := []WakeCovered{}
	for _, g := range gaps {
		out = append(out, WakeCovered{Task: g.task})
	}
	return out
}

func omittedList(gaps []coverageGap) []any {
	out := []any{}
	for _, g := range gaps {
		row := ordjson.NewObject()
		row.Set("task", g.task)
		row.Set("reason", g.reason)
		out = append(out, row)
	}
	return out
}

// legacyPrompts lists prompt attempts to endpoint recorded in returns.json that no episode or receipt accounts for,
// still submitted or uncertain: what an older or non-adopted helper sent outside the wake protocol.
func legacyPrompts(host machine.Identity, endpoint [3]string, tasks []showTask, accounted map[string]bool) []any {
	route := ordjson.NewObject()
	route.Set("machine", endpoint[0])
	route.Set("session", endpoint[1])
	route.Set("pane", endpoint[2])
	keys := map[any]bool{}
	for _, k := range routeKeys(host, route) {
		keys[k] = true
	}
	out := []any{}
	for _, t := range tasks {
		for _, d := range t.deliveries {
			delivery, _ := d.(*ordjson.Object)
			if delivery == nil {
				continue
			}
			recipientValue, _ := delivery.Get("recipient")
			recipient, _ := recipientValue.(*ordjson.Object)
			if recipient == nil {
				continue
			}
			key, _ := recipient.Get("key")
			role, _ := recipient.Get("role")
			via, _ := delivery.Get("via")
			state, _ := delivery.Get("state")
			deliveryID, _ := delivery.Get("id")
			if !keys[key] || role != "coordinator" || via != "prompt" || accounted[fmt.Sprint(deliveryID)] {
				continue
			}
			if state != "submitted" && state != "uncertain" && state != "in-flight" {
				continue
			}
			row := ordjson.NewObject()
			row.Set("task", t.id)
			row.Set("delivery", deliveryID)
			row.Set("state", state)
			at, _ := delivery.Get("at")
			row.Set("at", at)
			obligations, _ := delivery.Get("obligations")
			row.Set("obligations", obligations)
			row.Set("reason", "a prompt outside the wake protocol (an older or non-adopted helper); it was not coalesced and no episode accounts for it")
			out = append(out, row)
		}
	}
	return out
}
