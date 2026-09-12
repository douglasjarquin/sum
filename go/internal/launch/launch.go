package launch

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

const Schema = 1

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asObject(v any) *ordjson.Object {
	obj, _ := v.(*ordjson.Object)
	return obj
}

func asList(v any) []any {
	list, _ := v.([]any)
	return list
}

func Occupancy(tasks []*ordjson.Object) (*ordjson.Object, error) {
	byRepo := ordjson.NewObject()
	count := 0
	for _, t := range tasks {
		held, err := reservations.Held(t)
		if err != nil {
			id, _ := t.Get("id")
			return nil, fmt.Errorf("Malformed execution reservation for %v: %s. Admission and release are refused.", id, err)
		}
		count += len(held)
		if len(held) == 0 {
			continue
		}
		repo, _ := t.Get("repository")
		repoKey := fmt.Sprint(repo)
		existing, _ := byRepo.Get(repoKey)
		list, _ := existing.([]any)
		id, _ := t.Get("id")
		for range held {
			list = append(list, id)
		}
		byRepo.Set(repoKey, list)
	}
	result := ordjson.NewObject()
	result.Set("global", jsonInt(count))
	result.Set("by_repository", byRepo)
	return result, nil
}

func Admit(s *store.Store, tasks []*ordjson.Object, repository string) (*ordjson.Object, error) {
	loaded, err := settings.LoadSettings(s)
	if err != nil {
		return nil, err
	}
	occupied, err := Occupancy(tasks)
	if err != nil {
		return nil, err
	}
	limits := loaded.Capacity
	globalN := 0
	if v, ok := occupied.Get("global"); ok {
		if n, is := v.(json.Number); is {
			g, _ := n.Int64()
			globalN = int(g)
		}
	}
	same := []any{}
	if byRepo := asObject(func() any { v, _ := occupied.Get("by_repository"); return v }()); byRepo != nil {
		if v, ok := byRepo.Get(repository); ok {
			if list, is := v.([]any); is {
				same = list
			}
		}
	}
	if limits != nil {
		gLimit, _ := intField(limits, "global")
		if globalN >= gLimit {
			return nil, fmt.Errorf("Capacity: %d of %d global execution slots are held (%s). Park a conclusively stopped attempt with `execution park`, or raise capacity.global in .sum/settings.json; nothing was dispatched.", globalN, gLimit, loaded.Source)
		}
		pLimit, _ := intField(limits, "per_repository")
		if len(same) >= pLimit {
			return nil, fmt.Errorf("Capacity: %d of %d slots for %s are held by %v (%s). Park a conclusively stopped attempt, or raise capacity.per_repository in .sum/settings.json if this checkout needs another writer.", len(same), pLimit, repository, same, loaded.Source)
		}
	}
	result := ordjson.NewObject()
	result.Set("at", store.Now())
	result.Set("limits", limits)
	result.Set("source", loaded.Source)
	before := ordjson.NewObject()
	before.Set("global", jsonInt(globalN))
	before.Set("repository", jsonInt(len(same)))
	result.Set("occupied_before", before)
	return result, nil
}

func intField(o *ordjson.Object, key string) (int, bool) {
	v, _ := o.Get(key)
	switch t := v.(type) {
	case json.Number:
		n, err := t.Int64()
		return int(n), err == nil
	case int:
		return t, true
	}
	return 0, false
}

type ResolveArgs struct {
	Harness    string
	Model      string
	Reasoning  string
	SameAsRoot bool
	Extra      []string
	Preset     string
	PresetSet  bool
	RuntimeRoot string
}

func RootLaunch(runtimeRoot string, ctx *ordjson.Object) *ordjson.Object {
	result := ordjson.NewObject()
	result.Set("harness", nil)
	result.Set("model", nil)
	herdrPath, err := toolpath.Find(runtimeRoot, "herdr")
	if err != nil {
		result.Set("error", err.Error())
		return result
	}
	session := asString(func() any { v, _ := ctx.Get("session"); return v }())
	pane := asString(func() any { v, _ := ctx.Get("pane"); return v }())
	agent, err := herdrclient.Call(herdrPath, session, 5*time.Second, "agent", "get", pane)
	if err != nil {
		result.Set("error", err.Error())
		return result
	}
	agentObj := asObject(agent)
	if nested, ok := agentObj.Get("agent"); ok {
		if inner := asObject(nested); inner != nil && asString(func() any { v, _ := inner.Get("agent"); return v }()) != "" {
			agentObj = inner
		}
	}
	kind := asString(func() any { v, _ := agentObj.Get("agent"); return v }())
	if kind == "" || !harnessKindOK(kind) {
		result.Set("error", fmt.Sprintf("Herdr reports no agent kind for pane %s", pane))
		return result
	}
	result.Set("harness", kind)
	result.Set("reasoning", nil)
	result.Set("observed_at", store.Now())
	result.Set("error", nil)
	return result
}

func harnessKindOK(kind string) bool {
	if kind == "" {
		return false
	}
	if kind[0] < 'a' || kind[0] > 'z' {
		return false
	}
	for _, r := range kind[1:] {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return len(kind) <= 32
}

func Resolve(s *store.Store, ctx *ordjson.Object, args ResolveArgs) (*ordjson.Object, error) {
	if args.SameAsRoot && (args.Harness != "" || args.Model != "" || args.Reasoning != "" || args.PresetSet) {
		return nil, fmt.Errorf("--same-as-you conflicts with --harness/--model/--reasoning/--preset: same-as-you means the coordinator's own harness and native model.")
	}
	if args.Harness != "" && !harnessKindOK(args.Harness) {
		return nil, fmt.Errorf("Harness must be a Herdr integration kind, such as codex, claude, grok, or cursor.")
	}
	for _, a := range args.Extra {
		if strings.ContainsRune(a, 0) {
			return nil, fmt.Errorf("Harness arguments must be plain strings.")
		}
	}
	loaded, err := settings.LoadSettings(s)
	if err != nil {
		return nil, err
	}
	presets := loaded.Presets
	saved := loaded.Worker
	if saved == nil {
		saved = ordjson.NewObject()
	}
	var chosen *ordjson.Object
	var chosenSource string
	if args.PresetSet {
		spec, ok := presets[args.Preset]
		if !ok {
			names := make([]string, 0, len(presets))
			for n := range presets {
				names = append(names, n)
			}
			listed := "none"
			if len(names) > 0 {
				listed = strings.Join(names, ", ")
			}
			return nil, fmt.Errorf("Unknown preset %q; saved presets: %s. Run `preset list`, or create it with `preset set %s --harness ...`. Nothing was created.", args.Preset, listed, args.Preset)
		}
		chosen = spec
		chosenSource = "preset"
		h := asString(func() any { v, _ := spec.Get("harness"); return v }())
		if args.Harness != "" && args.Harness != h {
			return nil, fmt.Errorf("Preset %q runs on %s but --harness %s was requested. Choose one: drop --harness, pick another preset, or dispatch without --preset. Nothing was created.", args.Preset, h, args.Harness)
		}
	} else if name := asString(func() any { v, _ := saved.Get("preset"); return v }()); name != "" && !args.SameAsRoot {
		spec, ok := presets[name]
		if !ok {
			return nil, fmt.Errorf("The saved worker default names unknown preset %q. Fix .sum/settings.json (`preset set` or `settings set --clear-worker`), or pass --preset/--harness explicitly.", name)
		}
		h := asString(func() any { v, _ := spec.Get("harness"); return v }())
		if args.Harness != "" && args.Harness != h {
			chosen = nil
		} else {
			chosen = spec
			chosenSource = "saved-default"
			args.Preset = name
		}
	}
	plainSaved := saved
	if _, has := saved.Get("preset"); has {
		plainSaved = ordjson.NewObject()
	}
	source := ordjson.NewObject()
	harness := args.Harness
	var root *ordjson.Object
	switch {
	case harness != "":
		source.Set("harness", "explicit")
	case chosen != nil:
		harness = asString(func() any { v, _ := chosen.Get("harness"); return v }())
		source.Set("harness", chosenSource)
	case args.SameAsRoot || plainSaved.Len() == 0:
		root = RootLaunch(args.RuntimeRoot, ctx)
		h := asString(func() any { v, _ := root.Get("harness"); return v }())
		if h == "" {
			errText := asString(func() any { v, _ := root.Get("error"); return v }())
			return nil, fmt.Errorf("Cannot determine the coordinator's own harness (%s). Pass --harness explicitly or save a worker default with `settings set --worker-harness`.", errText)
		}
		harness = h
		if args.SameAsRoot {
			source.Set("harness", "same-as-you")
		} else {
			source.Set("harness", "root")
		}
	default:
		harness = asString(func() any { v, _ := plainSaved.Get("harness"); return v }())
		source.Set("harness", "saved-default")
	}
	savedHarness := asString(func() any { v, _ := plainSaved.Get("harness"); return v }())
	inheritsSaved := plainSaved.Len() > 0 && !args.SameAsRoot && harness == savedHarness
	presetArgs := []string{}
	if chosen != nil {
		for _, a := range asList(func() any { v, _ := chosen.Get("args"); return v }()) {
			if s, ok := a.(string); ok {
				presetArgs = append(presetArgs, s)
			}
		}
	}
	allExtra := append(append([]string{}, presetArgs...), args.Extra...)
	values := map[string]any{"model": nil, "reasoning": nil}
	explicit := map[string]string{"model": args.Model, "reasoning": args.Reasoning}
	for _, field := range []string{"model", "reasoning"} {
		switch {
		case explicit[field] != "":
			values[field] = explicit[field]
			source.Set(field, "explicit")
		case chosen != nil && asString(func() any { v, _ := chosen.Get(field); return v }()) != "":
			values[field] = asString(func() any { v, _ := chosen.Get(field); return v }())
			source.Set(field, chosenSource)
		case inheritsSaved && asString(func() any { v, _ := plainSaved.Get(field); return v }()) != "":
			values[field] = asString(func() any { v, _ := plainSaved.Get(field); return v }())
			source.Set(field, "saved-default")
		default:
			source.Set(field, "native-default")
		}
	}
	argv := []any{}
	if model, ok := values["model"].(string); ok && model != "" {
		argv = append(argv, anySlice(adapterArgv(harness, "model", model))...)
	}
	if reasoning, ok := values["reasoning"].(string); ok && reasoning != "" {
		argv = append(argv, anySlice(adapterArgv(harness, "reasoning", reasoning))...)
	}
	for _, a := range allExtra {
		argv = append(argv, a)
	}
	observed := ordjson.NewObject()
	observed.Set("status", "not-started")
	observed.Set("harness", nil)
	observed.Set("model", "not-exposed")
	result := ordjson.NewObject()
	result.Set("schema", jsonInt(Schema))
	result.Set("harness", harness)
	result.Set("model", values["model"])
	result.Set("reasoning", values["reasoning"])
	result.Set("argv", argv)
	result.Set("source", source)
	extraAny := make([]any, len(args.Extra))
	for i, a := range args.Extra {
		extraAny[i] = a
	}
	result.Set("explicit_args", extraAny)
	result.Set("same_as_root", args.SameAsRoot)
	result.Set("root", root)
	if chosen != nil {
		preset := ordjson.NewObject()
		preset.Set("name", args.Preset)
		rev, _ := chosen.Get("revision")
		preset.Set("revision", rev)
		preset.Set("source", chosenSource)
		preset.Set("harness", asString(func() any { v, _ := chosen.Get("harness"); return v }()))
		model, _ := chosen.Get("model")
		preset.Set("model", model)
		reasoning, _ := chosen.Get("reasoning")
		preset.Set("reasoning", reasoning)
		pa := make([]any, len(presetArgs))
		for i, a := range presetArgs {
			pa[i] = a
		}
		preset.Set("args", pa)
		result.Set("preset", preset)
	} else {
		result.Set("preset", nil)
	}
	if loaded.Worker != nil && loaded.Worker.Len() > 0 {
		result.Set("saved_default", loaded.Worker)
	} else {
		result.Set("saved_default", nil)
	}
	result.Set("resolved_at", store.Now())
	result.Set("observed", observed)
	return result, nil
}

func adapterArgv(harness, field, value string) []string {
	return settings.AdapterArgv(harness, field, value)
}

func anySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func Confirmation(launch *ordjson.Object) string {
	harness := asString(func() any { v, _ := launch.Get("harness"); return v }())
	source := asObject(func() any { v, _ := launch.Get("source"); return v }())
	model := asString(func() any { v, _ := launch.Get("model"); return v }())
	if model == "" {
		model = "native default"
	}
	parts := []string{
		fmt.Sprintf("harness %s (%s)", harness, asString(func() any { v, _ := source.Get("harness"); return v }())),
		fmt.Sprintf("model %s (%s)", model, asString(func() any { v, _ := source.Get("model"); return v }())),
	}
	if reasoning := asString(func() any { v, _ := launch.Get("reasoning"); return v }()); reasoning != "" {
		parts = append(parts, fmt.Sprintf("reasoning %s (%s)", reasoning, asString(func() any { v, _ := source.Get("reasoning"); return v }())))
	}
	if extra := asList(func() any { v, _ := launch.Get("explicit_args"); return v }()); len(extra) > 0 {
		parts = append(parts, fmt.Sprintf("explicit args %v", extra))
	}
	if preset := asObject(func() any { v, _ := launch.Get("preset"); return v }()); preset != nil {
		parts = append(parts, fmt.Sprintf("preset %s r%v (%s)", asString(func() any { v, _ := preset.Get("name"); return v }()), func() any { v, _ := preset.Get("revision"); return v }(), asString(func() any { v, _ := preset.Get("source"); return v }())))
	}
	observed := asObject(func() any { v, _ := launch.Get("observed"); return v }())
	status := asString(func() any { v, _ := observed.Get("status"); return v }())
	verification := map[string]string{
		"not-started":       "not started yet",
		"harness-observed":  "Herdr confirmed the harness kind; a CLI-requested model is not runtime-verified because no harness exposes it",
		"harness-mismatch":  "Herdr reports a different agent kind than requested; inspect the pane",
	}[status]
	if verification == "" {
		verification = status
	}
	argv, _ := launch.Get("argv")
	return fmt.Sprintf("Launch: %s; argv %v; %s.", strings.Join(parts, ", "), argv, verification)
}

func TaskLaunch(task *ordjson.Object) *ordjson.Object {
	if v, ok := task.Get("launch"); ok {
		if obj := asObject(v); obj != nil {
			return obj
		}
	}
	source := ordjson.NewObject()
	source.Set("harness", "legacy-record")
	source.Set("model", "native-default")
	source.Set("reasoning", "native-default")
	observed := ordjson.NewObject()
	observed.Set("status", "not-started")
	observed.Set("harness", nil)
	observed.Set("model", "not-exposed")
	result := ordjson.NewObject()
	result.Set("schema", jsonInt(Schema))
	harness, _ := task.Get("harness")
	result.Set("harness", harness)
	result.Set("model", nil)
	result.Set("reasoning", nil)
	result.Set("argv", []any{})
	result.Set("source", source)
	result.Set("explicit_args", []any{})
	result.Set("same_as_root", false)
	result.Set("root", nil)
	result.Set("preset", nil)
	result.Set("saved_default", nil)
	result.Set("resolved_at", nil)
	result.Set("observed", observed)
	return result
}
