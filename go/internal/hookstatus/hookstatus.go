package hookstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"path/filepath"
	"time"
)

const (
	HookDir         = "hook"
	HookHealth      = "health.json"
	HookSchema      = 2
	HookErrors      = 20
	InboxEntrypoint = "inbox"
)

var HookEvents = []string{"snapshot", "event", "missed", "reconnect", "sum-commit"}

func jsonInt(n int) json.Number      { return json.Number(fmt.Sprint(n)) }
func asObject(v any) *ordjson.Object { o, _ := v.(*ordjson.Object); return o }
func asString(v any) string          { s, _ := v.(string); return s }
func asList(v any) []any             { l, _ := v.([]any); return l }
func truthy(v any) bool              { return v != nil && v != false && v != "" }
func intField(o *ordjson.Object, key string) int {
	v, _ := o.Get(key)
	if n, ok := v.(json.Number); ok {
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}
func readHealth(s *store.Store) (*ordjson.Object, error) {
	path := filepath.Join(s.Home, HookDir, HookHealth)
	_, statErr := os.Stat(path)
	var value any
	var err error
	if statErr == nil {
		value, err = ordjson.ReadFile(path)
	} else {
		err = statErr
	}
	if os.IsNotExist(err) {
		o := ordjson.NewObject()
		o.Set("schema", jsonInt(HookSchema))
		o.Set("enabled", false)
		o.Set("errors", []any{})
		return o, nil
	}
	if err != nil {
		return nil, err
	}
	object, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("observer health is not an object")
	}
	return object, nil
}
func Health(s *store.Store) (*ordjson.Object, error) { return readHealth(s) }
func writeHealth(s *store.Store, increments map[string]int, changes *ordjson.Object) (*ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	health, err := readHealth(s)
	if err != nil {
		return nil, err
	}
	for key, n := range increments {
		health.Set(key, jsonInt(intField(health, key)+n))
	}
	if changes != nil {
		for _, key := range changes.Keys() {
			v, _ := changes.Get(key)
			health.Set(key, v)
		}
	}
	health.Set("schema", jsonInt(HookSchema))
	health.Set("updated_at", store.Now())
	if err = ordjson.WriteFile(filepath.Join(s.Home, HookDir, HookHealth), health); err != nil {
		return nil, err
	}
	return health, nil
}
func pendingSummary(s *store.Store, tasks []*ordjson.Object) (*ordjson.Object, error) {
	if tasks == nil {
		var err error
		tasks, err = s.AllTasks()
		if err != nil {
			return nil, err
		}
	}
	count := 0
	oldest := ""
	for _, task := range tasks {
		items, err := returns.OpenObligations(s, task)
		if err != nil {
			return nil, err
		}
		count += len(items)
		for _, item := range items {
			value, _ := item.Get("since")
			at := asString(value)
			if at != "" && (oldest == "" || at < oldest) {
				oldest = at
			}
		}
	}
	row := ordjson.NewObject()
	row.Set("count", jsonInt(count))
	row.Set("oldest_at", oldest)
	if stamp, err := time.Parse(time.RFC3339, oldest); err == nil {
		if now, err := time.Parse(time.RFC3339, store.Now()); err == nil {
			row.Set("oldest_age_s", jsonInt(max(0, int(now.Sub(stamp).Seconds()))))
		}
	}
	return row, nil
}
func Summary(s *store.Store) (*ordjson.Object, error) { return SummaryOf(s, nil) }
func SummaryOf(s *store.Store, tasks []*ordjson.Object) (*ordjson.Object, error) {
	health, err := readHealth(s)
	if err != nil {
		return nil, err
	}
	pending, err := pendingSummary(s, tasks)
	if err != nil {
		return nil, err
	}
	health.Set("pending", pending)
	health.Set("backend", "hc")
	health.Set("note", "Native observations trigger bounded reconciliation. Only matching application receipts close durable obligations.")
	return health, nil
}
func Status(s *store.Store, ctx *ordjson.Object, runtimeRoot, sumctlPath string) (*ordjson.Object, error) {
	row, err := Summary(s)
	if err != nil {
		return nil, err
	}
	observed, observeErr := Compatibility(context.Background(), s)
	if observeErr != nil {
		row.Set("degraded", true)
		row.Set("reason", observeErr.Error())
	} else {
		row.Set("observer_observed", observed)
		if enabled, _ := row.Get("enabled"); enabled == true {
			if running, _ := observed.Get("running"); running != true {
				row.Set("degraded", true)
				row.Set("reason", "no current observer startup receipt and process is confirmed")
			}
		}
	}
	return row, nil
}
