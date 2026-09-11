package graphview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pyrepr"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	GraphFile        = "graph.json"
	GraphSchema      = 1
	CodegraphVersion = "1.5.0"

	GraphFallback = "Read and search the source with your normal tools; a graph that is not `ready` or a result that contradicts a file is never a structural conclusion."
)

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func intEquals(v any, n int) bool {
	num, ok := v.(json.Number)
	if !ok {
		return false
	}
	i, err := num.Int64()
	return err == nil && i == int64(n)
}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case json.Number:
		f, err := t.Float64()
		return err != nil || f != 0
	case []any:
		return len(t) > 0
	case *ordjson.Object:
		return t != nil && t.Len() > 0
	default:
		return v != nil
	}
}

func asObject(v any) *ordjson.Object {
	obj, _ := v.(*ordjson.Object)
	return obj
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func getField(o *ordjson.Object, key string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	return v
}

func listField(o *ordjson.Object, key string) []any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	list, _ := v.([]any)
	return list
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

// Path ports `graph_path`.
func Path(s *store.Store, taskID string) (string, error) {
	taskPath, err := s.TaskPath(taskID)
	if err != nil {
		return "", err
	}
	return filepath.Join(taskPath, GraphFile), nil
}

// Read ports `read_graph`.
func Read(s *store.Store, taskID string) (*ordjson.Object, error) {
	path, err := Path(s, taskID)
	if err != nil {
		return nil, err
	}
	if isSymlink(path) {
		return nil, fmt.Errorf("%s must not be a symlink", path)
	}
	info, statErr := os.Stat(path)
	if statErr != nil || info.IsDir() {
		return nil, nil
	}
	value, err := ordjson.ReadFile(path)
	if err != nil {
		return nil, err
	}
	obj := asObject(value)
	schemaValue := getField(obj, "schema")
	if !intEquals(schemaValue, GraphSchema) {
		return nil, fmt.Errorf("%s has graph schema %s; this release reads schema %d", path, pyrepr.Repr(schemaValue), GraphSchema)
	}
	return obj, nil
}

func graphFailures(record *ordjson.Object) []any {
	var failures []any
	for _, av := range listField(record, "attempts") {
		a := asObject(av)
		if !truthy(getField(a, "ok")) && asString(getField(a, "action")) != "deferred" {
			failures = append(failures, a)
		}
	}
	return failures
}

// Summary ports `graph_summary`: the bounded view kept in task.json, dev.json, and run records.
func Summary(record *ordjson.Object) *ordjson.Object {
	if !truthy(record) {
		return nil
	}
	attempts := listField(record, "attempts")
	var last *ordjson.Object
	if len(attempts) > 0 {
		last = asObject(attempts[len(attempts)-1])
	}
	result := ordjson.NewObject()
	result.Set("state", getField(record, "state"))
	result.Set("index_path", getField(record, "index_path"))
	result.Set("tool_version", getField(asObject(getField(record, "tool")), "version"))
	result.Set("indexed_head", getField(record, "indexed_head"))
	result.Set("pinned", CodegraphVersion)
	result.Set("attempts", jsonInt(len(attempts)))
	result.Set("failures", jsonInt(len(graphFailures(record))))
	result.Set("last_action", getField(last, "action"))
	result.Set("seconds", getField(last, "seconds"))
	index := asObject(getField(record, "index"))
	result.Set("files", getField(index, "fileCount"))
	result.Set("nodes", getField(index, "nodeCount"))
	var errOut any
	if errValue := getField(record, "error"); truthy(errValue) {
		runes := []rune(asString(errValue))
		if len(runes) > 300 {
			runes = runes[:300]
		}
		errOut = string(runes)
	}
	result.Set("error", errOut)
	result.Set("updated_at", getField(record, "updated_at"))
	return result
}

// View ports `graph_view`: the `graph` field of `context --section execution`. Reads no checkout.
func View(s *store.Store, task *ordjson.Object) (*ordjson.Object, error) {
	taskID := asString(getField(task, "id"))
	record, err := Read(s, taskID)
	if err != nil {
		result := ordjson.NewObject()
		result.Set("present", false)
		result.Set("ok", false)
		result.Set("error", err.Error())
		return result, nil
	}
	if record == nil {
		result := ordjson.NewObject()
		result.Set("present", false)
		result.Set("ok", true)
		result.Set("note", "No graph record; the task was dispatched before sum initialized graphs, or the checkout was never created.")
		return result, nil
	}
	path, err := Path(s, taskID)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("present", true)
	result.Set("ok", true)
	result.Set("path", path)
	summary := Summary(record)
	for _, k := range summary.Keys() {
		result.Set(k, getField(summary, k))
	}
	result.Set("commands", getField(record, "commands"))
	result.Set("freshness", getField(record, "freshness"))
	result.Set("fallback", GraphFallback)
	result.Set("authority", "Tool observation recorded by sum; a graph result is never verification evidence.")
	return result, nil
}
