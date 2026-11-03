package returns

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"path/filepath"
	"sort"
)

const (
	historicalSchema  = 2
	historicalOK      = "ok"
	historicalBlocked = "blocked"
	historicalAbsent  = "absent"
)

type historicalStatus struct{ State, Diagnostic, Path string }
type historicalRecipient struct {
	CoreID  string `json:"core_id"`
	Address string `json:"address"`
	Machine string `json:"machine"`
	Session string `json:"session,omitempty"`
	Pane    string `json:"pane,omitempty"`
	Role    string `json:"role"`
}

type historicalRef struct {
	Task string `json:"task"`
	ID   string `json:"id"`
}

type historicalCovered struct {
	InputRevision string `json:"input_revision,omitempty"`
	Task          string `json:"task"`
	ID            string `json:"id"`
	Revision      string `json:"revision,omitempty"`
}

type historicalReceipt struct {
	Boundary    *historicalBoundary `json:"boundary,omitempty"`
	Accepted    []historicalCovered `json:"accepted,omitempty"`
	Fingerprint string              `json:"fingerprint"`
	Generation  int                 `json:"generation"`
	At          string              `json:"at"`
	Result      string              `json:"result"`
	Delivery    string              `json:"delivery,omitempty"`
}

type historicalDecision struct {
	Task     string `json:"task"`
	ID       string `json:"id"`
	Revision string `json:"revision,omitempty"`
	Delivery string `json:"delivery"`
	State    string `json:"state"`
	At       string `json:"at"`
}

type historicalEpisode struct {
	ID         string          `json:"id"`
	Delivery   string          `json:"delivery,omitempty"`
	Phase      string          `json:"phase"`
	PreparedAt string          `json:"prepared_at"`
	Claims     []historicalRef `json:"claims"`
	IntentAt   string          `json:"intent_at,omitempty"`
	OutcomeAt  string          `json:"outcome_at,omitempty"`
	Reason     string          `json:"reason,omitempty"`
	ConsumedAt string          `json:"consumed_at,omitempty"`
}

type historicalSidecar struct {
	Schema       int                  `json:"schema"`
	Installation string               `json:"installation"`
	Recipient    historicalRecipient  `json:"recipient"`
	Incarnation  json.RawMessage      `json:"incarnation"`
	Generation   int                  `json:"generation"`
	Episode      *historicalEpisode   `json:"episode"`
	Covered      []historicalCovered  `json:"covered"`
	Decisions    []historicalDecision `json:"decisions"`
	Receipts     []historicalReceipt  `json:"receipts"`
	UpdatedAt    string               `json:"updated_at"`
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

func readHistoricalSidecar(s *store.Store, inst installation, endpoint [3]string) (*historicalSidecar, historicalStatus) {
	path := filepath.Join(s.Home, "deliver", store.RegistrationKey(store.Endpoint{Machine: endpoint[0], Session: endpoint[1], Pane: endpoint[2]})+store.WakeSuffix)
	w, status := readHistoricalSidecarFile(inst, path)
	if status.State != historicalOK {
		return nil, status
	}
	if w.Recipient.Role != "coordinator" || [3]string{w.Recipient.Machine, w.Recipient.CoreID, w.Recipient.Address} != endpoint {
		return nil, historicalStatus{State: historicalBlocked, Path: path, Diagnostic: fmt.Sprintf("%s records another recipient (%s %s on %s); inspect %s; the evidence was not accepted.", path, w.Recipient.CoreID, w.Recipient.Address, w.Recipient.Machine, "the preserved sidecar")}
	}
	return w, status
}

func readHistoricalSidecarFile(inst installation, path string) (*historicalSidecar, historicalStatus) {
	blocked := func(msg string) (*historicalSidecar, historicalStatus) {
		return nil, historicalStatus{State: historicalBlocked, Path: path, Diagnostic: fmt.Sprintf("%s %s; inspect %s; the evidence was not accepted.", path, msg, "the preserved sidecar")}
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, historicalStatus{State: historicalAbsent, Path: path}
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
	var w historicalSidecar
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&w); err != nil {
		return blocked("is not valid JSON for a wake sidecar: " + err.Error())
	}
	if w.Schema != historicalSchema {
		return blocked(fmt.Sprintf("uses wake schema %d, which this release does not support (it supports %d); sum never migrates it in place", w.Schema, historicalSchema))
	}
	if inst.err != nil {
		return blocked("cannot be checked against state.json: " + inst.err.Error())
	}
	if w.Installation != inst.id {
		return blocked(fmt.Sprintf("belongs to another installation (%q, this one is %q)", w.Installation, inst.id))
	}
	if w.Covered == nil {
		w.Covered = []historicalCovered{}
	}
	if w.Decisions == nil {
		w.Decisions = []historicalDecision{}
	}
	if w.Receipts == nil {
		w.Receipts = []historicalReceipt{}
	}
	return &w, historicalStatus{State: historicalOK, Path: path}
}

type historicalBoundary struct {
	Schema       int                 `json:"schema"`
	Installation string              `json:"installation"`
	Recipient    historicalRecipient `json:"recipient"`
	Incarnation  json.RawMessage     `json:"incarnation"`
	Episode      string              `json:"episode"`
	Generation   int                 `json:"generation"`
	Included     []historicalCovered `json:"included"`
	Omitted      []historicalCovered `json:"omitted"`
}

func normalizeCovered(items []historicalCovered) []historicalCovered {
	out := []historicalCovered{}
	seen := map[[4]string]bool{}
	for _, c := range items {
		k := [4]string{c.Task, c.ID, c.Revision, c.InputRevision}
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
		if out[i].Revision != out[j].Revision {
			return out[i].Revision < out[j].Revision
		}
		return out[i].InputRevision < out[j].InputRevision
	})
	return out
}

func (b *historicalBoundary) canonical() []byte {
	c := *b
	c.Incarnation = json.RawMessage(canonicalJSON(b.Incarnation))
	c.Included = normalizeCovered(b.Included)
	c.Omitted = normalizeCovered(b.Omitted)
	encoded, _ := json.Marshal(&c)
	return encoded
}

// Fingerprint identifies this exact boundary; the receipt records it.
func (b *historicalBoundary) Fingerprint() string {
	return TokenFingerprint(b.canonical())
}
