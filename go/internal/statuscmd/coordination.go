package statuscmd

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func coordination(s *store.Store, view *ordjson.Object, tasks []*ordjson.Object) error {
	session, err := s.CoordinatorSession()
	if err != nil {
		return err
	}
	view.Set("session", nil)
	view.Set("claim_generation", nil)
	if session != nil {
		view.Set("session", session.Object())
		generation, _ := session.Object().Get("generation")
		view.Set("claim_generation", generation)
	}
	route, err := ordjson.ReadFile(filepath.Join(s.Root, "runtime-route.json"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	view.Set("runtime_route", route)
	total := 0
	kinds := map[string]int{}
	for _, task := range tasks {
		obligations, err := returns.OpenObligations(s, task)
		if err != nil {
			return err
		}
		total += len(obligations)
		for _, item := range obligations {
			value, _ := item.Get("kind")
			kind, _ := value.(string)
			kinds[kind]++
		}
	}
	counts := ordjson.NewObject()
	keys := make([]string, 0, len(kinds))
	for kind := range kinds {
		keys = append(keys, kind)
	}
	sort.Strings(keys)
	for _, kind := range keys {
		counts.Set(kind, jsonInt(kinds[kind]))
	}
	outstanding := ordjson.NewObject()
	outstanding.Set("total", jsonInt(total))
	outstanding.Set("by_kind", counts)
	view.Set("outstanding", outstanding)
	return nil
}
