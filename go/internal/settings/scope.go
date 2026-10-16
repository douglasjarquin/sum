package settings

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/project"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type Scope struct {
	Project    string
	Repository string
	Path       string
	Limit      int
	Source     string
	Enrolled   any
	index      registryIndex
}

func (sc Scope) Label() string {
	if sc.Project != "" {
		return sc.Project
	}
	return sc.Repository
}

func (sc Scope) RaiseKey() string {
	if strings.HasPrefix(sc.Source, "projects.") {
		return "capacity." + sc.Source
	}
	return "capacity.per_repository"
}

func projectName(projectObj *ordjson.Object) string {
	if projectObj == nil {
		return ""
	}
	v, _ := projectObj.Get("name")
	name, _ := v.(string)
	return name
}

func taskProject(task *ordjson.Object) *ordjson.Object {
	v, _ := task.Get("project")
	obj, _ := v.(*ordjson.Object)
	return obj
}

type registryIndex struct {
	byName map[string]string
	byPath map[string]string
	err    error
}

func readRegistry(s *store.Store) registryIndex {
	index := registryIndex{byName: map[string]string{}, byPath: map[string]string{}}
	registry, err := project.ReadProjects(s)
	if err != nil {
		index.err = err
		return index
	}
	projectsValue, _ := registry.Get("projects")
	projectsObj, _ := projectsValue.(*ordjson.Object)
	if projectsObj == nil {
		return index
	}
	for _, name := range projectsObj.Keys() {
		recordValue, _ := projectsObj.Get(name)
		record, _ := recordValue.(*ordjson.Object)
		if record == nil {
			continue
		}
		pathValue, _ := record.Get("path")
		path, _ := pathValue.(string)
		if path == "" {
			continue
		}
		path = project.ResolvePathOrSelf(path)
		index.byName[name] = path
		index.byPath[path] = name
	}
	return index
}

func (index registryIndex) scopeOf(capacity *ordjson.Object, projectObj *ordjson.Object, repository string) Scope {
	scope := Scope{Project: projectName(projectObj), Repository: project.ResolvePathOrSelf(repository), Source: "per_repository", index: index}
	if scope.Project == "" && index.err == nil {
		scope.Project = index.byPath[scope.Repository]
	}
	if scope.Project != "" {
		if index.err != nil {
			scope.Enrolled = nil
		} else if path, enrolled := index.byName[scope.Project]; enrolled {
			scope.Enrolled = true
			scope.Path = path
		} else {
			scope.Enrolled = false
		}
	}
	if capacity == nil {
		scope.Source = "unlimited"
		return scope
	}
	scope.Limit, _ = asInt(func() any { v, _ := capacity.Get("per_repository"); return v }())
	if scope.Project == "" {
		return scope
	}
	if projectsValue, has := capacity.Get("projects"); has {
		if projects, ok := projectsValue.(*ordjson.Object); ok {
			if raw, declared := projects.Get(scope.Project); declared {
				if n, ok := asInt(raw); ok {
					scope.Limit = n
					scope.Source = "projects." + scope.Project
				}
			}
		}
	}
	return scope
}

func ResolveScope(s *store.Store, capacity *ordjson.Object, projectObj *ordjson.Object, repository string) Scope {
	return readRegistry(s).scopeOf(capacity, projectObj, repository)
}

func (sc Scope) covers(task *ordjson.Object, index registryIndex) bool {
	repoValue, _ := task.Get("repository")
	repo, _ := repoValue.(string)
	repo = project.ResolvePathOrSelf(repo)
	if repo == sc.Repository || (sc.Path != "" && repo == sc.Path) {
		return true
	}
	if sc.Project == "" {
		return false
	}
	name := projectName(taskProject(task))
	if name == "" && index.err == nil {
		name = index.byPath[repo]
	}
	return name == sc.Project
}

func HeldFor(s *store.Store, tasks []*ordjson.Object, sc Scope) ([]any, error) {
	index := sc.index
	if index.byName == nil {
		index = readRegistry(s)
	}
	held := []any{}
	for _, t := range tasks {
		reserved, err := reservations.Held(t)
		if err != nil {
			idValue, _ := t.Get("id")
			return nil, fmt.Errorf("Malformed execution reservation for %v: %s. Admission and release are refused.", idValue, err)
		}
		if len(reserved) == 0 || !sc.covers(t, index) {
			continue
		}
		idValue, _ := t.Get("id")
		for range reserved {
			held = append(held, idValue)
		}
	}
	return held, nil
}

func effectiveView(s *store.Store, capacity *ordjson.Object, tasks []*ordjson.Object) []any {
	index := readRegistry(s)
	scopes := map[string]*Scope{}
	var order []string
	add := func(sc Scope) string {
		key := sc.Label()
		if _, seen := scopes[key]; !seen {
			scopes[key] = &sc
			order = append(order, key)
		}
		return key
	}
	if projectsValue, has := capacity.Get("projects"); has {
		if projects, ok := projectsValue.(*ordjson.Object); ok {
			for _, name := range projects.Keys() {
				projectObj := ordjson.NewObject()
				projectObj.Set("name", name)
				add(index.scopeOf(capacity, projectObj, index.byName[name]))
			}
		}
	}
	for _, t := range tasks {
		reserved, err := reservations.Held(t)
		if err != nil || len(reserved) == 0 {
			continue
		}
		repoValue, _ := t.Get("repository")
		repo, _ := repoValue.(string)
		add(index.scopeOf(capacity, taskProject(t), repo))
	}
	result := make([]any, 0, len(order))
	for _, key := range order {
		sc := scopes[key]
		entry := ordjson.NewObject()
		entry.Set("scope", key)
		if sc.Project != "" {
			entry.Set("project", sc.Project)
			entry.Set("enrolled", sc.Enrolled)
		} else {
			entry.Set("project", nil)
			entry.Set("enrolled", nil)
		}
		entry.Set("limit", json.Number(fmt.Sprint(sc.Limit)))
		entry.Set("source", sc.Source)
		held := []any{}
		for _, t := range tasks {
			reserved, err := reservations.Held(t)
			if err != nil || len(reserved) == 0 || !sc.covers(t, index) {
				continue
			}
			idValue, _ := t.Get("id")
			for range reserved {
				held = append(held, idValue)
			}
		}
		entry.Set("held", held)
		result = append(result, entry)
	}
	return result
}
