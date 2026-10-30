package returns

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const ResolutionFile = "delivery-reconciliations.json"

type deliveryCoverage struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}
type applicationReceipt struct {
	Kind       string         `json:"kind"`
	Obligation string         `json:"obligation"`
	Revision   string         `json:"revision"`
	Endpoint   store.Endpoint `json:"endpoint"`
	At         string         `json:"at"`
}
type deliveryProof struct {
	Application *applicationReceipt `json:"application,omitempty"`
	Consumption *WakeReceipt        `json:"consumption,omitempty"`
}
type deliveryResolution struct {
	Delivery    string          `json:"delivery"`
	Digest      string          `json:"digest"`
	At          string          `json:"at"`
	Coordinator store.Endpoint  `json:"coordinator"`
	Proofs      []deliveryProof `json:"proofs"`
}
type resolutionLedger struct {
	Schema      int                  `json:"schema"`
	Task        string               `json:"task"`
	Resolutions []deliveryResolution `json:"resolutions"`
}

func digest(value any) string {
	raw, err := ordjson.MarshalCompact(value)
	if err != nil {
		raw, err = json.Marshal(value)
	}
	if err != nil {
		return ""
	}
	normalized, err := ordjson.Decode(raw)
	if err != nil {
		return ""
	}
	b, err := ordjson.MarshalSortedCompact(normalized)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func decodeProof(value any, out any) error {
	b, err := ordjson.MarshalCompact(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
func receiptAfter(row *ordjson.Object, at string) bool {
	source, ok := routeValue(row, "at").(string)
	if !ok {
		return false
	}
	sent, e1 := time.Parse(time.RFC3339, source)
	receipt, e2 := time.Parse(time.RFC3339, at)
	return e1 == nil && e2 == nil && !receipt.Before(sent)
}

func validStamp(at string) bool {
	_, err := time.Parse(time.RFC3339, at)
	return at != "" && err == nil
}

func ObligationRevision(task *ordjson.Object, id string) string {
	kind, ref, ok := strings.Cut(id, ":")
	if !ok || ref == "" {
		return ""
	}
	var source *ordjson.Object
	if kind == "answer" || kind == "question" {
		rows, _ := routeValue(task, "questions").([]any)
		for _, raw := range rows {
			q, _ := raw.(*ordjson.Object)
			if routeValue(q, "id") == ref {
				source = q
				break
			}
		}
		if source == nil {
			return ""
		}
		stable := ordjson.NewObject()
		for _, key := range []string{"id", "key", "text", "created_at", "choices", "recommended", "rationale", "sources", "human_action"} {
			if v, has := source.Get(key); has {
				stable.Set(key, v)
			}
		}
		if kind == "answer" {
			for _, key := range []string{"answer", "answered_at", "answer_choice"} {
				if v, has := source.Get(key); has {
					stable.Set(key, v)
				}
			}
			if routeValue(source, "answered_at") == nil {
				return ""
			}
		}
		return digest(stable)
	}
	if kind == "report" || kind == "review" || kind == "attention" {
		field := "evidence"
		if kind == "attention" {
			field = "attention"
		}
		rows, _ := routeValue(task, field).([]any)
		for _, raw := range rows {
			row, _ := raw.(*ordjson.Object)
			if routeValue(row, "id") == ref {
				source = cloneObject(row)
				source.Delete("status")
				return digest(source)
			}
		}
	}
	return ""
}

func ApplicationReceipt(task *ordjson.Object, id, kind string, endpoint store.Endpoint) *ordjson.Object {
	r := applicationReceipt{Kind: kind, Obligation: id, Revision: ObligationRevision(task, id), Endpoint: endpoint, At: store.Now()}
	o, _ := ordjson.FromValue(r)
	return o
}

func readResolutions(s *store.Store, task string) (*resolutionLedger, error) {
	dir, err := s.TaskPath(task)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, ResolutionFile)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return &resolutionLedger{Schema: 1, Task: task, Resolutions: []deliveryResolution{}}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("delivery reconciliation ledger must be a regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ledger resolutionLedger
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&ledger); err != nil {
		return nil, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("delivery reconciliation ledger has trailing data")
	}
	if ledger.Schema != 1 || ledger.Task != task || ledger.Resolutions == nil {
		return nil, fmt.Errorf("invalid delivery reconciliation ledger")
	}
	return &ledger, nil
}

func deliveryByID(s *store.Store, task, id string) (*ordjson.Object, error) {
	record, err := ReadReturns(s, task)
	if err != nil {
		return nil, err
	}
	rows, ok := routeValue(record, "deliveries").([]any)
	if !ok {
		return nil, fmt.Errorf("invalid delivery history")
	}
	var found *ordjson.Object
	for _, raw := range rows {
		row, _ := raw.(*ordjson.Object)
		if row == nil {
			return nil, fmt.Errorf("unreadable delivery row")
		}
		if routeValue(row, "id") == id {
			if found != nil {
				return nil, fmt.Errorf("duplicate delivery ID")
			}
			found = row
		}
	}
	if found == nil {
		return nil, fmt.Errorf("delivery %s is not recorded for task %s", id, task)
	}
	return found, nil
}
func coverageOf(row *ordjson.Object) ([]deliveryCoverage, error) {
	var cov []deliveryCoverage
	if err := decodeProof(routeValue(row, "coverage"), &cov); err != nil || len(cov) == 0 {
		return nil, fmt.Errorf("delivery lacks exact historical revision proof")
	}
	ids, ok := routeValue(row, "obligations").([]any)
	if !ok || len(ids) != len(cov) {
		return nil, fmt.Errorf("delivery coverage does not match its obligations")
	}
	seen := map[string]bool{}
	for _, c := range cov {
		if c.ID == "" || len(c.Revision) != 64 || seen[c.ID] {
			return nil, fmt.Errorf("invalid delivery coverage")
		}
		seen[c.ID] = true
	}
	for _, id := range ids {
		v, ok := id.(string)
		if !ok || !seen[v] {
			return nil, fmt.Errorf("unrelated delivery coverage")
		}
	}
	return cov, nil
}
func endpointOf(row *ordjson.Object) (store.Endpoint, error) {
	e := store.EndpointFromContext(func() *ordjson.Object { o, _ := routeValue(row, "recipient").(*ordjson.Object); return o }())
	return e, e.Validate()
}

func validateProofs(s *store.Store, task, row *ordjson.Object, proofs []deliveryProof) error {
	endpoint, err := endpointOf(row)
	if err != nil {
		return fmt.Errorf("delivery lacks full historical native endpoint: %w", err)
	}
	cov, err := coverageOf(row)
	if err != nil {
		return err
	}
	taskID, _ := routeValue(task, "id").(string)
	deliveryID, _ := routeValue(row, "id").(string)
	instance, err := s.Instance()
	if err != nil {
		return err
	}
	matched := map[string]bool{}
	for _, proof := range proofs {
		if (proof.Application == nil) == (proof.Consumption == nil) {
			return fmt.Errorf("invalid reconciliation proof kind")
		}
		if r := proof.Application; r != nil {
			if !validStamp(r.At) || !receiptAfter(row, r.At) || r.Endpoint.Validate() != nil || !r.Endpoint.Native().SameAuthority(endpoint.Native()) || r.Revision == "" {
				return fmt.Errorf("invalid application receipt endpoint or revision")
			}
			kind, ref, _ := strings.Cut(r.Obligation, ":")
			var source *ordjson.Object
			rows, _ := routeValue(task, "questions").([]any)
			for _, raw := range rows {
				q, _ := raw.(*ordjson.Object)
				if routeValue(q, "id") == ref {
					source = q
					break
				}
			}
			receiptField := "application_receipt"
			if kind == "question" {
				receiptField = "read_receipt"
			}
			if source == nil || digest(routeValue(source, receiptField)) != digest(r) || ObligationRevision(task, r.Obligation) != r.Revision {
				return fmt.Errorf("application receipt is stale or unrelated")
			}
			if kind == "answer" {
				if r.Kind != "answer-applied" || routeValue(source, "status") != "applied" || endpoint.Role != "worker" {
					return fmt.Errorf("answer has no matching applied receipt")
				}
			} else if kind == "question" {
				if r.Kind != "question-answered" || routeValue(source, "answered_at") == nil || endpoint.Role != "coordinator" {
					return fmt.Errorf("question has no matching read receipt")
				}
			} else {
				return fmt.Errorf("unsupported application receipt")
			}
			matched[r.Obligation+"\n"+r.Revision] = true
		}
		if r := proof.Consumption; r != nil {
			b := r.Boundary
			if r.Result != "consumed" || !validStamp(r.At) || !receiptAfter(row, r.At) || b == nil || r.Delivery != deliveryID || b.Generation != r.Generation || b.Generation < 1 || b.Episode == "" || routeValue(row, "wake_episode") != b.Episode || fmt.Sprint(routeValue(row, "wake_generation")) != fmt.Sprint(b.Generation) || b.Installation != instance || b.Schema != WakeSchema || r.Fingerprint != b.Fingerprint() {
				return fmt.Errorf("invalid consumed boundary receipt")
			}
			var evidence incarnation.NativeEvidence
			if json.Unmarshal(b.Incarnation, &evidence) != nil || !evidence.Endpoint.SameAuthority(endpoint.Native()) || endpoint.Role != "coordinator" {
				return fmt.Errorf("consumed boundary names another native recipient")
			}
			if b.Recipient.CoreID != endpoint.CoreID || b.Recipient.Address != endpoint.Address || b.Recipient.Machine != endpoint.Machine || b.Recipient.Role != "coordinator" {
				return fmt.Errorf("consumed boundary recipient mismatch")
			}
			for _, c := range r.Accepted {
				included := false
				for _, i := range b.Included {
					if i == c {
						included = true
					}
				}
				if !included {
					return fmt.Errorf("receipt accepts an omitted obligation")
				}
				if c.Task == taskID && c.InputRevision != "" && ObligationRevision(task, c.ID) == c.InputRevision {
					matched[c.ID+"\n"+c.InputRevision] = true
				}
			}
		}
	}
	for _, c := range cov {
		if !matched[c.ID+"\n"+c.Revision] {
			return fmt.Errorf("delivery %s has no matching durable receipt for %s revision %s", deliveryID, c.ID, c.Revision)
		}
	}
	return nil
}

func DeliveryResolved(s *store.Store, task *ordjson.Object, row *ordjson.Object) (bool, error) {
	id, _ := routeValue(task, "id").(string)
	ledger, err := readResolutions(s, id)
	if err != nil {
		return false, err
	}
	var found *deliveryResolution
	for i := range ledger.Resolutions {
		r := &ledger.Resolutions[i]
		if r.Delivery == routeValue(row, "id") {
			if found != nil {
				return false, fmt.Errorf("duplicate delivery reconciliation")
			}
			found = r
		}
	}
	if found == nil {
		return false, nil
	}
	if found.Digest != digest(row) || !validStamp(found.At) || found.Coordinator.Validate() != nil || found.Coordinator.Role != "coordinator" {
		return false, fmt.Errorf("invalid or stale delivery reconciliation")
	}
	if err = validateProofs(s, task, row, found.Proofs); err != nil {
		return false, err
	}
	return true, nil
}

func PendingDeliveryEffects(s *store.Store, task *ordjson.Object) error {
	id, _ := routeValue(task, "id").(string)
	history, err := ReadReturns(s, id)
	if err != nil {
		return err
	}
	rows, ok := routeValue(history, "deliveries").([]any)
	if !ok {
		return fmt.Errorf("unreadable delivery history")
	}
	for _, raw := range rows {
		row, _ := raw.(*ordjson.Object)
		if row == nil {
			return fmt.Errorf("unreadable delivery row")
		}
		switch routeValue(row, "state") {
		case "in-flight", "uncertain", "unknown":
			resolved, err := DeliveryResolved(s, task, row)
			if err != nil {
				return err
			}
			if !resolved {
				return fmt.Errorf("native delivery %v still has an unresolved effect; use delivery reconcile %s --delivery %v after an exact durable receipt exists", routeValue(row, "id"), id, routeValue(row, "id"))
			}
		}
	}
	return nil
}

func ReconcileDelivery(s *store.Store, ctx *ordjson.Object, taskID, deliveryID string) (*ordjson.Object, error) {
	if err := s.CheckWritable(); err != nil {
		return nil, err
	}
	actor, err := app.ObserveTaskActor(s, ctx)
	if err != nil {
		return nil, err
	}
	row, err := deliveryByID(s, taskID, deliveryID)
	if err != nil {
		return nil, err
	}
	endpoint, err := endpointOf(row)
	if err != nil {
		return nil, fmt.Errorf("delivery lacks full historical endpoint: %w", err)
	}
	lockCtx, cancel := context.WithTimeout(context.Background(), wakeLockWait)
	defer cancel()
	unlockRecipient, err := LockRecipient(s, lockCtx, endpoint.Object())
	if err != nil {
		return nil, err
	}
	defer unlockRecipient()
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err = s.CheckWritable(); err != nil {
		return nil, err
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	if err = app.CheckTaskRole(s, task, actor, "coordinator"); err != nil {
		return nil, err
	}
	current, err := deliveryByID(s, taskID, deliveryID)
	if err != nil {
		return nil, err
	}
	if digest(current) != digest(row) {
		return nil, fmt.Errorf("delivery changed before reconciliation; inspect again")
	}
	switch routeValue(row, "state") {
	case "in-flight", "uncertain", "unknown":
	default:
		return nil, fmt.Errorf("delivery has no unresolved effect")
	}
	resolved, err := DeliveryResolved(s, task, row)
	if err != nil {
		return nil, err
	}
	view := ordjson.NewObject()
	view.Set("task", taskID)
	view.Set("delivery", deliveryID)
	if resolved {
		view.Set("result", "repeated")
		return view, nil
	}
	cov, err := coverageOf(row)
	if err != nil {
		return nil, err
	}
	proofs := []deliveryProof{}
	for _, c := range cov {
		kind, ref, _ := strings.Cut(c.ID, ":")
		if kind != "question" && kind != "answer" {
			continue
		}
		rows, _ := routeValue(task, "questions").([]any)
		for _, raw := range rows {
			q, _ := raw.(*ordjson.Object)
			if routeValue(q, "id") != ref {
				continue
			}
			field := "application_receipt"
			if kind == "question" {
				field = "read_receipt"
			}
			var r applicationReceipt
			if raw := routeValue(q, field); raw != nil && decodeProof(raw, &r) == nil {
				proofs = append(proofs, deliveryProof{Application: &r})
			}
		}
	}
	if endpoint.Role == "coordinator" {
		host, err := s.Machine()
		if err != nil {
			return nil, err
		}
		w, status := readWake(s, readInstallation(s), [3]string{host.Canonical(endpoint.Machine), endpoint.CoreID, endpoint.Address})
		if status.State == WakeBlocked {
			return nil, fmt.Errorf("wake receipt unreadable: %s", status.Diagnostic)
		}
		if w != nil {
			for _, r := range w.Receipts {
				if r.Result == "consumed" && r.Delivery == deliveryID {
					receipt := r
					proofs = append(proofs, deliveryProof{Consumption: &receipt})
				}
			}
		}
	}
	if err = validateProofs(s, task, row, proofs); err != nil {
		return nil, err
	}
	ledger, err := readResolutions(s, taskID)
	if err != nil {
		return nil, err
	}
	ledger.Resolutions = append(ledger.Resolutions, deliveryResolution{Delivery: deliveryID, Digest: digest(row), At: store.Now(), Coordinator: store.EndpointFromContext(ctx), Proofs: proofs})
	dir, _ := s.TaskPath(taskID)
	object, _ := ordjson.FromValue(ledger)
	if err = ordjson.WriteFile(filepath.Join(dir, ResolutionFile), object); err != nil {
		return nil, err
	}
	view.Set("result", "reconciled")
	view.Set("original_state", routeValue(row, "state"))
	return view, nil
}
