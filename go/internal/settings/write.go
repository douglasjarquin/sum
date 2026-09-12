package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pyrepr"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const note = "Applies to future admissions and dispatches only. No worker was stopped, relaunched, or switched; the coordinator's own harness and model are untouched."

type WriteArgs struct {
	Global            *int
	PerRepository     *int
	ClearCapacity     bool
	WorkerHarness     string
	WorkerModel       string
	WorkerReasoning   string
	WorkerPreset      string
	ClearWorker       bool
	ReviewerPreset    string
	ClearReviewer     bool
	WorkerPresetSet   bool
	ReviewerPresetSet bool
}

func Write(s *store.Store, args WriteArgs) (*ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := LoadSettings(s)
	if err != nil {
		return nil, err
	}
	var mergedCapacity *ordjson.Object
	switch {
	case args.ClearCapacity:
		mergedCapacity = nil
	case args.Global != nil || args.PerRepository != nil:
		base := ordjson.NewObject()
		if current.Capacity != nil {
			for _, k := range current.Capacity.Keys() {
				v, _ := current.Capacity.Get(k)
				base.Set(k, v)
			}
		}
		if args.Global != nil {
			base.Set("global", json.Number(fmt.Sprint(*args.Global)))
		}
		if args.PerRepository != nil {
			base.Set("per_repository", json.Number(fmt.Sprint(*args.PerRepository)))
		}
		mergedCapacity, err = validateCapacity(base)
		if err != nil {
			return nil, err
		}
	default:
		mergedCapacity = current.Capacity
	}

	mergedWorker := current.Worker
	switch {
	case args.ClearWorker:
		mergedWorker = nil
	case args.WorkerPresetSet:
		obj := ordjson.NewObject()
		obj.Set("preset", args.WorkerPreset)
		mergedWorker, err = validateWorker(obj, current.Presets)
		if err != nil {
			return nil, err
		}
	case args.WorkerHarness != "" || args.WorkerModel != "" || args.WorkerReasoning != "":
		saved := current.Worker
		if saved != nil {
			if _, hasPreset := saved.Get("preset"); hasPreset {
				saved = nil
			}
		}
		harness := args.WorkerHarness
		if harness == "" && saved != nil {
			if v, ok := saved.Get("harness"); ok {
				if str, is := v.(string); is {
					harness = str
				}
			}
		}
		if harness == "" {
			return nil, fmt.Errorf("Give --worker-harness when saving a worker model or reasoning default; a model belongs to one harness.")
		}
		base := ordjson.NewObject()
		if saved != nil {
			savedHarness, _ := saved.Get("harness")
			if savedHarness == harness {
				for _, k := range saved.Keys() {
					v, _ := saved.Get(k)
					base.Set(k, v)
				}
			}
		}
		if args.WorkerHarness != "" {
			base.Set("harness", args.WorkerHarness)
		}
		if args.WorkerModel != "" {
			base.Set("model", args.WorkerModel)
		}
		if args.WorkerReasoning != "" {
			base.Set("reasoning", args.WorkerReasoning)
		}
		base.Set("harness", harness)
		mergedWorker, err = validateWorker(base, nil)
		if err != nil {
			return nil, err
		}
	}

	mergedReviewer := current.Reviewer
	switch {
	case args.ClearReviewer:
		mergedReviewer = nil
	case args.ReviewerPresetSet:
		obj := ordjson.NewObject()
		obj.Set("preset", args.ReviewerPreset)
		mergedReviewer, err = validateReviewer(obj, current.Presets)
		if err != nil {
			return nil, err
		}
	}

	previousCapacity := current.Capacity
	previousWorker := current.Worker
	previousReviewer := current.Reviewer
	path, err := save(s, mergedCapacity, mergedWorker, current.Presets, mergedReviewer)
	if err != nil {
		return nil, err
	}
	tasks, err := s.AllTasks()
	if err != nil {
		return nil, err
	}
	occupied, err := occupancy(tasks)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("path", path)
	result.Set("previous", orNil(previousCapacity))
	result.Set("capacity", orNil(mergedCapacity))
	result.Set("previous_worker", orNil(previousWorker))
	result.Set("worker", orNil(mergedWorker))
	result.Set("previous_reviewer", orNil(previousReviewer))
	result.Set("reviewer", orNil(mergedReviewer))
	result.Set("occupied", occupied)
	result.Set("note", note)
	return result, nil
}

type PresetWriteArgs struct {
	Name      string
	Harness   string
	Model     string
	Reasoning string
	Args      []string
	ArgsSet   bool
	Clear     []string
}

func WritePreset(s *store.Store, args PresetWriteArgs) (*ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := LoadSettings(s)
	if err != nil {
		return nil, err
	}
	if !presetNamePattern.MatchString(args.Name) {
		return nil, fmt.Errorf("preset name must be lowercase letters, digits, _ or - (up to 32 characters), got %s", pyrepr.Repr(args.Name))
	}
	previous := current.Presets[args.Name]
	base := ordjson.NewObject()
	if previous != nil {
		clear := map[string]bool{}
		for _, c := range args.Clear {
			clear[c] = true
		}
		for _, k := range previous.Keys() {
			if k == "revision" || clear[k] {
				continue
			}
			v, _ := previous.Get(k)
			base.Set(k, v)
		}
		if args.Harness != "" {
			old, _ := previous.Get("harness")
			if old != args.Harness {
				base = ordjson.NewObject()
			}
		}
	}
	if args.Harness != "" {
		base.Set("harness", args.Harness)
	}
	if args.Model != "" {
		base.Set("model", args.Model)
	}
	if args.Reasoning != "" {
		base.Set("reasoning", args.Reasoning)
	}
	if args.ArgsSet {
		list := make([]any, len(args.Args))
		for i, a := range args.Args {
			list[i] = a
		}
		base.Set("args", list)
	}
	if _, has := base.Get("harness"); !has {
		return nil, fmt.Errorf("Give --harness when creating preset %s; a preset is a shortcut for one harness.", pyrepr.Repr(args.Name))
	}
	revision := 1
	if previous != nil {
		if v, ok := previous.Get("revision"); ok {
			if n, ok := asInt(v); ok {
				revision = n + 1
			}
		}
	}
	base.Set("revision", json.Number(fmt.Sprint(revision)))
	spec, err := validatePreset(args.Name, base)
	if err != nil {
		return nil, err
	}
	if previous == nil && len(current.Presets) >= presetMax {
		return nil, fmt.Errorf("At most %d presets; delete one first.", presetMax)
	}
	presets := map[string]*ordjson.Object{}
	for k, v := range current.Presets {
		presets[k] = v
	}
	presets[args.Name] = spec
	path, err := save(s, current.Capacity, current.Worker, presets, current.Reviewer)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("path", path)
	result.Set("name", args.Name)
	result.Set("previous", orNil(previous))
	result.Set("preset", spec)
	result.Set("launch", presetLaunch(spec))
	result.Set("note", "Future dispatches that select this preset expand this revision. Prepared or running tasks keep the specification they were prepared with. "+note)
	return result, nil
}

func DeletePreset(s *store.Store, name string) (*ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := LoadSettings(s)
	if err != nil {
		return nil, err
	}
	if err := validatePresetReference("preset", name, current.Presets); err != nil {
		return nil, err
	}
	refs := presetReferences(current.Worker, current.Reviewer, name)
	if len(refs) > 0 {
		return nil, fmt.Errorf("Preset %s is still the %s. Repoint or clear that default first; nothing was deleted.", pyrepr.Repr(name), strings.Join(refs, " and the "))
	}
	presets := map[string]*ordjson.Object{}
	for k, v := range current.Presets {
		if k != name {
			presets[k] = v
		}
	}
	path, err := save(s, current.Capacity, current.Worker, presets, current.Reviewer)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(presets))
	for k := range presets {
		names = append(names, k)
	}
	sort.Strings(names)
	remaining := make([]any, len(names))
	for i, n := range names {
		remaining[i] = n
	}
	result := ordjson.NewObject()
	result.Set("path", path)
	result.Set("deleted", name)
	result.Set("remaining", remaining)
	result.Set("note", "Tasks prepared with this preset keep their persisted specification; nothing running was touched.")
	return result, nil
}

func save(s *store.Store, capacity, worker *ordjson.Object, presets map[string]*ordjson.Object, reviewer *ordjson.Object) (string, error) {
	path := filepath.Join(s.Home, File)
	doc := ordjson.NewObject()
	doc.Set("schema", json.Number(fmt.Sprint(Schema)))
	if capacity != nil {
		doc.Set("capacity", capacity)
	}
	if worker != nil {
		doc.Set("worker", worker)
	}
	if len(presets) > 0 {
		obj := ordjson.NewObject()
		names := make([]string, 0, len(presets))
		for name := range presets {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			obj.Set(name, presets[name])
		}
		doc.Set("presets", obj)
	}
	if reviewer != nil {
		doc.Set("reviewer", reviewer)
	}
	if err := ordjson.WriteFile(path, doc); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
