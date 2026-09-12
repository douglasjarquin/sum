package evidence

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

const Schema = 1

func newID() (string, error) {
	buf := make([]byte, 5)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "e-" + hex.EncodeToString(buf), nil
}

func endpointIdentity(endpoint *ordjson.Object) *ordjson.Object {
	if endpoint == nil {
		return nil
	}
	row := ordjson.NewObject()
	for _, key := range []string{"machine", "session", "pane"} {
		value, _ := endpoint.Get(key)
		row.Set(key, value)
	}
	return row
}

func Append(task *ordjson.Object, kind, source string, body *ordjson.Object, candidate any, endpoint *ordjson.Object) (*ordjson.Object, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	record := ordjson.NewObject()
	record.Set("schema", json.Number(fmt.Sprint(Schema)))
	record.Set("id", id)
	record.Set("kind", kind)
	record.Set("source", source)
	record.Set("at", store.Now())
	record.Set("candidate", candidate)
	record.Set("brief_revision", nil)
	record.Set("sum_version", contract.SumVersion)
	record.Set("endpoint", endpointIdentity(endpoint))
	if body != nil {
		for _, key := range body.Keys() {
			value, _ := body.Get(key)
			record.Set(key, value)
		}
	}
	list, _ := task.Get("evidence")
	items, _ := list.([]any)
	task.Set("evidence", append(items, record))
	return record, nil
}

func ActiveRevision(s *store.Store, task *ordjson.Object) any {
	versionsObj, err := versions.ReadVersions(s, task)
	if err != nil || versionsObj == nil {
		return nil
	}
	active, _ := versionsObj.Get("active")
	return active
}
