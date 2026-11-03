package roleinit

import (
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func InitCoordinator(root, helper string, s *store.Store, token, label string, transfer bool) (*ordjson.Object, error) {
	core, err := coordinatorCore(root)
	if err != nil {
		return nil, err
	}
	session, err := s.ClaimCoordinator(token, label, transfer)
	if err != nil {
		view := ordjson.NewObject()
		if session != nil {
			view.Set("session", coordinatorSessionView(session))
		}
		return view, err
	}
	tasks, err := s.AllTasks()
	if err != nil {
		return nil, err
	}
	items := make([]any, 0)
	counts := ordjson.NewObject()
	total := 0
	for _, task := range tasks {
		obligations, err := returns.OpenObligations(s, task)
		if err != nil {
			return nil, err
		}
		for _, obligation := range obligations {
			kind, _ := obligation.Get("kind")
			name, _ := kind.(string)
			raw, _ := counts.Get(name)
			count, _ := raw.(int)
			counts.Set(name, count+1)
			total++
			if len(items) < 20 {
				item := ordjson.NewObject()
				id, _ := task.Get("id")
				item.Set("task", id)
				for _, key := range obligation.Keys() {
					value, _ := obligation.Get(key)
					item.Set(key, value)
				}
				items = append(items, item)
			}
		}
	}
	outstanding := ordjson.NewObject()
	outstanding.Set("total", total)
	outstanding.Set("counts", counts)
	outstanding.Set("items", items)
	view := ordjson.NewObject()
	view.Set("role", "coordinator")
	view.Set("home", s.Home)
	view.Set("claim", session.Token())
	view.Set("session", coordinatorSessionView(session))
	view.Set("procedure", roleProcedure(root, s, "coordinator", nil, core))
	view.Set("helper", helper)
	view.Set("outstanding", outstanding)
	view.Set("next", "Read the validated coordinator procedure, then run collect --claim "+session.Token()+".")
	return view, nil
}

func TaskCheckout(root string, current *store.Store) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	currentTasks, err := current.AllTasks()
	if err != nil {
		return "", err
	}
	if task := taskCheckoutAt(currentTasks, cwd); task != "" {
		return task, nil
	}
	hint, err := InstallationHint(root)
	if err != nil || hint == "" {
		return "", err
	}
	s, err := store.Open(hint)
	if err != nil {
		return "", err
	}
	tasks, err := s.AllTasks()
	if err != nil {
		return "", err
	}
	return taskCheckoutAt(tasks, root), nil
}

func taskCheckoutAt(tasks []*ordjson.Object, root string) string {
	for _, task := range tasks {
		raw, _ := task.Get("worktree")
		path, _ := raw.(string)
		if path == "" {
			continue
		}
		relative, err := filepath.Rel(resolveOrSelf(path), resolveOrSelf(root))
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
			id, _ := task.Get("id")
			name, _ := id.(string)
			return name
		}
	}
	return ""
}

func coordinatorSessionView(session *store.CoordinatorSession) *ordjson.Object {
	view := session.Object()
	if claimed, err := time.Parse(time.RFC3339, session.ClaimedAt); err == nil {
		view.Set("age_seconds", max(int64(0), int64(store.NowTime().Sub(claimed).Seconds())))
	}
	return view
}
