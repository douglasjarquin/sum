package roleinit

import (
	"strings"

	"github.com/douglasjarquin/sum/go/internal/launch"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/pelletier/go-toml/v2"
)

func existingCoordinatorLaunch(spec launch.HCTemplate) *ordjson.Object {
	argv := append([]string(nil), spec.Argv[1:]...)
	model, reasoning := explicitNativeSelection(spec.Harness, argv)
	result := ordjson.NewObject()
	result.Set("schema", launch.Schema)
	result.Set("harness", spec.Harness)
	result.Set("model", model)
	result.Set("reasoning", reasoning)
	argumentsValue := make([]any, len(argv))
	for index, arg := range argv {
		argumentsValue[index] = arg
	}
	result.Set("argv", argumentsValue)
	source := ordjson.NewObject()
	source.Set("harness", "qualified-existing-command")
	for key, value := range map[string]any{"model": model, "reasoning": reasoning} {
		if value == nil {
			source.Set(key, "not-exposed")
		} else {
			source.Set(key, "qualified-existing-command")
		}
	}
	result.Set("source", source)
	permissions := ordjson.NewObject()
	permissions.Set("override", false)
	permissions.Set("authorization", nil)
	permissions.Set("status", "not-exposed")
	arguments := []any{}
	for _, argument := range settings.PermissionOverrides(spec.Harness, argv) {
		row := ordjson.NewObject()
		row.Set("argument", argument)
		row.Set("source", "qualified-existing-command")
		arguments = append(arguments, row)
	}
	permissions.Set("arguments", arguments)
	result.Set("permissions", permissions)
	observed := ordjson.NewObject()
	observed.Set("harness", spec.Harness)
	observed.Set("model", "not-exposed")
	observed.Set("permissions", "not-exposed")
	result.Set("observed", observed)
	result.Set("adopted_at", store.Now())
	return result
}

func explicitCodexSelection(argv []string) (any, any) {
	candidates := map[string][]string{"model": {}, "model_reasoning_effort": {}}
	add := func(key, value string) { candidates[key] = append(candidates[key], value) }
	for index := 0; index < len(argv); index++ {
		arg := argv[index]
		value := ""
		switch {
		case arg == "--":
			index = len(argv)
			continue
		case arg == "-m" || arg == "--model":
			if index+1 < len(argv) {
				index++
				add("model", argv[index])
			}
		case strings.HasPrefix(arg, "--model="):
			add("model", strings.TrimPrefix(arg, "--model="))
		case strings.HasPrefix(arg, "-m") && len(arg) > 2:
			add("model", arg[2:])
		case arg == "-c" || arg == "--config":
			if index+1 < len(argv) {
				index++
				value = argv[index]
			}
		case strings.HasPrefix(arg, "--config="):
			value = strings.TrimPrefix(arg, "--config=")
		case strings.HasPrefix(arg, "-c") && len(arg) > 2:
			value = arg[2:]
		}
		if value != "" {
			var config map[string]any
			if toml.Unmarshal([]byte(value), &config) == nil {
				for _, key := range []string{"model", "model_reasoning_effort"} {
					if raw, present := config[key]; present {
						if text, ok := raw.(string); ok {
							add(key, text)
						} else {
							add(key, "")
						}
					}
				}
			}
		}
	}
	known := func(key string) any {
		values := candidates[key]
		if len(values) == 0 || values[0] == "" {
			return nil
		}
		for _, value := range values[1:] {
			if value != values[0] {
				return nil
			}
		}
		return values[0]
	}
	return known("model"), known("model_reasoning_effort")
}

func explicitNativeSelection(harness string, argv []string) (any, any) {
	if harness == "codex" {
		return explicitCodexSelection(argv)
	}
	values := map[string][]string{"model": {}, "reasoning": {}}
	for index := 0; index < len(argv); index++ {
		key, value, inline := strings.Cut(argv[index], "=")
		if key == "--" {
			break
		}
		field := ""
		switch key {
		case "--model", "-m":
			field = "model"
		case "--effort", "--reasoning-effort":
			field = "reasoning"
		}
		if field == "" {
			continue
		}
		if !inline {
			if index+1 >= len(argv) {
				values[field] = append(values[field], "")
				continue
			}
			index++
			value = argv[index]
		}
		values[field] = append(values[field], value)
	}
	known := func(field string) any {
		selections := values[field]
		if len(selections) == 0 || selections[0] == "" {
			return nil
		}
		for _, value := range selections[1:] {
			if value != selections[0] {
				return nil
			}
		}
		return selections[0]
	}
	return known("model"), known("reasoning")
}
