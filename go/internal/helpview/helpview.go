package helpview

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pyrepr"
)

//go:embed catalog.json
var catalogJSON []byte

var (
	catalogOnce sync.Once
	catalogErr  error
	catalogRaw  []byte
)

func loadCatalog() error {
	catalogOnce.Do(func() {
		catalogRaw = catalogJSON
		_, catalogErr = ordjson.Decode(catalogJSON)
	})
	return catalogErr
}

func View(runtimeRoot, topic string) (*ordjson.Object, error) {
	if err := loadCatalog(); err != nil {
		return nil, err
	}
	quotedRuntime, err := json.Marshal(runtimeRoot)
	if err != nil {
		return nil, err
	}
	data := []byte(strings.ReplaceAll(string(catalogRaw), `"{{RUNTIME}}"`, string(quotedRuntime)))
	decoded, err := ordjson.Decode(data)
	if err != nil {
		return nil, err
	}
	catalog, ok := decoded.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("help catalog is not a JSON object")
	}
	topValue, _ := catalog.Get("")
	top, _ := topValue.(*ordjson.Object)
	commandsValue, _ := top.Get("commands")
	commands, _ := commandsValue.(*ordjson.Object)
	names := commands.Keys()
	sortedNames := append([]string(nil), names...)
	sort.Strings(sortedNames)

	if topic == "" {
		return top, nil
	}

	parts := strings.SplitN(topic, "-", 2)
	if !contains(names, parts[0]) {
		return nil, fmt.Errorf("Unknown topic %s; topics: %s", pyrepr.Repr(topic), pyrepr.StrList(sortedNames))
	}
	if len(parts) == 2 {
		parentValue, has := catalog.Get(parts[0])
		parent, _ := parentValue.(*ordjson.Object)
		var available []string
		if has && parent != nil {
			if subs, ok := parent.Get("subcommands"); ok {
				if subObj, isObj := subs.(*ordjson.Object); isObj {
					available = subObj.Keys()
				}
			}
		}
		nestedKey := parts[0] + "-" + parts[1]
		if _, hasNested := catalog.Get(nestedKey); !hasNested {
			sort.Strings(available)
			return nil, fmt.Errorf("Unknown subcommand %s of %s; available: %s", pyrepr.Repr(parts[1]), parts[0], pyrepr.StrList(available))
		}
	}
	viewValue, has := catalog.Get(topic)
	if !has {
		return nil, fmt.Errorf("Unknown topic %s; topics: %s", pyrepr.Repr(topic), pyrepr.StrList(sortedNames))
	}
	view, ok := viewValue.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("help topic %s is not an object", topic)
	}
	return view, nil
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
