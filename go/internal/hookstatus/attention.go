package hookstatus

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/inboxview"
	"github.com/douglasjarquin/sum/go/internal/incarnation"
	"github.com/douglasjarquin/sum/go/internal/maintenance"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// reconcileAttention saves runtime observations, never task reports or receipts.
// The subscription is only a hint: every fact comes from the current core snapshot.
func reconcileAttention(ctx context.Context, s *store.Store, scope store.Endpoint) error {
	ctx, cancel := context.WithTimeout(ctx, returns.DefaultPassBudget)
	defer cancel()
	lease, err := s.DeliveryShared(ctx)
	if err != nil {
		return err
	}
	defer lease()
	if err = s.CheckWritable(); err != nil {
		return err
	}
	instance, err := s.Instance()
	if err != nil {
		return err
	}
	host, err := s.Machine()
	if err != nil {
		return err
	}
	client := hcclient.Client{Socket: scope.Socket, CoreID: scope.CoreID, Timeout: returns.ObserveTimeout}
	snapshot, err := client.List(ctx, hcclient.ListParams{All: true, Labels: map[string]string{"sum.installation": instance}})
	if err != nil {
		return err
	}
	candidates := map[string]bool{}
	for _, terminal := range snapshot.Terminals {
		if attentionKind(terminal) != "" {
			candidates[terminal.Address] = true
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	tasks, err := s.AllTasks()
	if err != nil {
		return err
	}
	type target struct {
		task       *ordjson.Object
		field, key string
	}
	targets := []target{}
	for _, task := range tasks {
		for _, field := range []string{"worker_endpoint", "reviewer"} {
			targets = append(targets, target{task, field, fmt.Sprint(value(task, "id")) + "/" + field})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].key < targets[j].key })
	health, err := readHealth(s)
	if err != nil {
		return err
	}
	cursor, _ := value(health, "attention_cursor").(string)
	split := 0
	for split < len(targets) && targets[split].key <= cursor {
		split++
	}
	targets = append(targets[split:], targets[:split]...)
	madeProgress := false
	for _, target := range targets {
		task := target.task
		if ctx.Err() != nil {
			if madeProgress {
				returns.MarkDirty(s)
			}
			return nil
		}
		endpoint := store.EndpointFromContext(asObject(value(task, target.field)))
		if !host.Is(endpoint.Machine) || endpoint.Task != fmt.Sprint(value(task, "id")) || endpoint.CoreID != scope.CoreID || endpoint.Session != scope.Session || !candidates[endpoint.Address] || endpoint.Validate() != nil || value(task, "status") == "archived" {
			continue
		}
		changes := ordjson.NewObject()
		changes.Set("attention_cursor", target.key)
		if _, err = writeHealth(s, nil, changes); err != nil {
			return err
		}
		terminal, err := client.Get(ctx, endpoint.Address)
		if err != nil {
			continue
		}
		registration, err := s.Registration(endpoint)
		if err != nil || !attentionBinding(endpoint, terminal, registration) {
			continue
		}
		kind := attentionKind(terminal)
		observation, _ := ordjson.FromValue(struct {
			Endpoint   hcclient.Endpoint `json:"endpoint"`
			Kind       string            `json:"kind"`
			StatusMS   *uint64           `json:"status_ms"`
			FinishedMS *uint64           `json:"finished_ms"`
			Turn       uint64            `json:"turn"`
		}{endpoint.Native(), kind, terminal.StatusMS, terminal.FinishedMS, terminal.Turn})
		raw, _ := ordjson.MarshalCompact(observation)
		id := fmt.Sprintf("a-%x", sha256.Sum256(raw))
		if attentionRecorded(task, id) {
			continue
		}
		excerpt := ""
		readError := ""
		if screen, err := client.Read(ctx, endpoint.Address, 40); err == nil {
			excerpt = inboxview.Sanitize(screen.Text)
		} else {
			readError = err.Error()
		}
		chars := []rune(strings.TrimSpace(excerpt))
		if len(chars) > 2000 {
			chars = chars[:2000]
		}
		excerpt = string(chars)
		// Refuse a transition or replacement observed while reading its screen.
		current, err := client.Get(ctx, endpoint.Address)
		if err != nil || !reflect.DeepEqual(current, terminal) {
			continue
		}
		unlock, err := s.Lock()
		if err != nil {
			return err
		}
		err = func() error {
			defer unlock()
			if err := s.CheckWritable(); err != nil {
				return err
			}
			latest, err := s.ReadTask(endpoint.Task)
			if err != nil {
				return err
			}
			if value(latest, "status") == "archived" || maintenance.Refuse(latest, "record native attention") != nil {
				return nil
			}
			bound := store.EndpointFromContext(asObject(value(latest, target.field)))
			if !bound.Native().SameAuthority(endpoint.Native()) {
				return nil
			}
			fresh, err := s.Registration(endpoint)
			if err != nil {
				return err
			}
			if !attentionBinding(endpoint, terminal, fresh) || !sameAttentionProof(registration, fresh) || attentionRecorded(latest, id) {
				return nil
			}
			record := ordjson.NewObject()
			record.Set("id", id)
			record.Set("kind", kind)
			record.Set("status", "open")
			record.Set("at", store.Now())
			record.Set("source", "hc-observation")
			record.Set("role", endpoint.Role)
			record.Set("observed", kind)
			record.Set("observation", observation)
			record.Set("excerpt", excerpt)
			record.Set("message", endpoint.Role+" terminal observed "+kind+". "+excerpt)
			if readError != "" {
				record.Set("read_error", readError)
			}
			records := asList(value(latest, "attention"))
			records = append(records, record)
			latest.Set("attention", records)
			if err := s.SaveTask(latest); err != nil {
				return err
			}
			madeProgress = true
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return nil
}
func attentionKind(t hcclient.Terminal) string {
	if t.State == "finished" && !t.Keep && t.FinishedMS != nil {
		return "exited"
	}
	if t.State == "live" && t.Status != nil && t.Status.State == "blocked" {
		return "blocked"
	}
	return ""
}
func attentionBinding(endpoint store.Endpoint, terminal hcclient.Terminal, record *ordjson.Object) bool {
	if record == nil || (endpoint.Role != "worker" && endpoint.Role != "reviewer") || endpoint.Task == "" || !store.EndpointFromContext(record).Native().SameAuthority(endpoint.Native()) || terminal.Address != endpoint.Address || terminal.CreatedMS != endpoint.CreatedMS || terminal.Keep {
		return false
	}
	var receipt incarnation.NativeReceipt
	var evidence incarnation.NativeEvidence
	raw, err := ordjson.MarshalCompact(value(record, "native_receipt"))
	if err != nil || json.Unmarshal(raw, &receipt) != nil || receipt.Kind != endpoint.Role+"-init" || receipt.Attempt != endpoint.Attempt || !receipt.Endpoint.SameAuthority(endpoint.Native()) {
		return false
	}
	raw, err = ordjson.MarshalCompact(value(record, "incarnation"))
	if err != nil || json.Unmarshal(raw, &evidence) != nil || !evidence.Endpoint.SameAuthority(endpoint.Native()) || evidence.PID == 0 || evidence.Foreground == nil {
		return false
	}
	if attentionKind(terminal) == "exited" {
		return true
	}
	return attentionKind(terminal) == "blocked" && incarnation.JudgeNative(value(record, "incarnation"), endpoint.Native(), terminal, &receipt).Verified
}
func attentionRecorded(task *ordjson.Object, id string) bool {
	for _, raw := range asList(value(task, "attention")) {
		if record := asObject(raw); record != nil && value(record, "id") == id {
			return true
		}
	}
	return false
}
func sameAttentionProof(a, b *ordjson.Object) bool {
	for _, key := range []string{"incarnation", "native_receipt"} {
		x, _ := ordjson.MarshalCompact(value(a, key))
		y, _ := ordjson.MarshalCompact(value(b, key))
		if string(x) != string(y) {
			return false
		}
	}
	return true
}
func value(o *ordjson.Object, key string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	return v
}
