package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/project"
	"github.com/douglasjarquin/sum/go/internal/pyrepr"
	"github.com/douglasjarquin/sum/go/internal/reservations"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	File                 = "settings.json"
	Schema               = 1
	CapacityMax          = 64
	presetMax            = 32
	defaultGlobal        = 2
	defaultPerRepository = 1
)

var (
	presetNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	harnessKindPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	launchValuePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@,=\[\]-]{0,127}$`)
	settingsKeys       = map[string]bool{"schema": true, "capacity": true, "worker": true, "presets": true, "reviewer": true, "evidence": true, "hook": true, "usage": true}
	defaultCapacity    = map[string]int{"global": defaultGlobal, "per_repository": defaultPerRepository}
	capacityKeys       = []string{"global", "per_repository", "projects"}
)

type adapter struct {
	Model      []string
	Reasoning  []string
	Permission []string
}

var adapters = map[string]adapter{
	"codex": {
		Model:     []string{"-m"},
		Reasoning: []string{"-c", "model_reasoning_effort="},
		Permission: []string{
			"-a=", "--ask-for-approval=", "-s=", "--sandbox=",
			"--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust", "--approve-for-me",
			"-c approval_policy=", "-c sandbox_mode=", "-c sandbox_permissions=",
			"--config approval_policy=", "--config sandbox_mode=", "--config sandbox_permissions=",
		},
	},
	"claude": {
		Model:     []string{"--model"},
		Reasoning: []string{"--effort"},
		Permission: []string{
			"--permission-mode=", "--permission-prompts=", "--permission-prompt-tool=",
			"--allowedTools=", "--allowed-tools=", "--disallowedTools=", "--disallowed-tools=", "--tools=",
			"--dangerously-skip-permissions", "--allow-dangerously-skip-permissions", "--restricted",
		},
	},
	"grok": {Model: []string{"-m"}, Reasoning: []string{"--reasoning-effort"}},
	"copilot": {
		Model:     []string{"--model"},
		Reasoning: []string{"--effort"},
		Permission: []string{
			"--allow-tool=", "--allow-url=", "--deny-tool=", "--deny-url=", "--mode=",
			"--allow-all", "--allow-all-tools", "--allow-all-paths", "--allow-all-urls",
			"--yolo", "--autopilot", "--disallow-temp-dir",
		},
	},
	"cursor": {Model: []string{"--model"}},
	"pi":     {Model: []string{"--model"}},
	"omp":    {Model: []string{"--model="}},
	"devin":  {Permission: []string{"--permission-mode=", "--respect-workspace-trust="}},
}

func PermissionFlags(harness string) ([]string, bool) {
	a, ok := adapters[harness]
	if !ok || len(a.Permission) == 0 {
		return nil, false
	}
	flags := make([]string, 0, len(a.Permission))
	for _, entry := range a.Permission {
		flags = append(flags, strings.TrimSuffix(entry, "="))
	}
	return flags, true
}

func PermissionHarnesses() []string {
	var names []string
	for name, a := range adapters {
		if len(a.Permission) > 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func PermissionOverrides(harness string, argv []string) []string {
	a, ok := adapters[harness]
	if !ok || len(a.Permission) == 0 {
		return nil
	}
	var out []string
	for i := 0; i < len(argv); i++ {
		for _, entry := range a.Permission {
			matched, consumed := matchPermission(entry, argv, i)
			if matched == "" {
				continue
			}
			out = append(out, matched)
			i += consumed
			break
		}
	}
	return out
}

func matchPermission(entry string, argv []string, i int) (string, int) {
	arg := argv[i]
	if flag, key, isConfig := strings.Cut(entry, " "); isConfig {
		if arg == flag && i+1 < len(argv) && strings.HasPrefix(argv[i+1], key) {
			return flag + " " + argv[i+1], 1
		}
		if strings.HasPrefix(arg, flag+"="+key) || (shortFlag(flag) && strings.HasPrefix(arg, flag+key)) {
			return arg, 0
		}
		return "", 0
	}
	if flag, takesValue := strings.CutSuffix(entry, "="); takesValue {
		if strings.HasPrefix(arg, flag+"=") || (shortFlag(flag) && len(arg) > len(flag) && strings.HasPrefix(arg, flag)) {
			return arg, 0
		}
		if arg != flag {
			return "", 0
		}
		if i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "-") {
			return arg + " " + argv[i+1], 1
		}
		return arg, 0
	}
	if arg == entry {
		return arg, 0
	}
	return "", 0
}

func shortFlag(flag string) bool {
	return len(flag) == 2 && flag[0] == '-' && flag[1] != '-'
}

func adapterPrefix(harness, field string) ([]string, bool) {
	a, ok := adapters[harness]
	if !ok {
		return nil, false
	}
	switch field {
	case "model":
		if a.Model == nil {
			return nil, false
		}
		return a.Model, true
	case "reasoning":
		if a.Reasoning == nil {
			return nil, false
		}
		return a.Reasoning, true
	}
	return nil, false
}

func knownHarnessesForField(field string) []string {
	var names []string
	for name := range adapters {
		if _, ok := adapterPrefix(name, field); ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func argvConflicts(harness, field string, extra []string) bool {
	prefix, ok := adapterPrefix(harness, field)
	if !ok || len(prefix) == 0 {
		return false
	}
	head := prefix[0]
	flag := strings.TrimSuffix(head, "=")
	if len(prefix) == 2 && strings.HasSuffix(prefix[1], "=") {
		key := prefix[1]
		for _, a := range extra {
			if strings.HasPrefix(a, key) || a == flag+"="+key || strings.HasPrefix(a, flag+"="+key) {
				return true
			}
		}
		return false
	}
	for _, a := range extra {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

type Settings struct {
	Capacity *ordjson.Object
	Worker   *ordjson.Object
	Presets  map[string]*ordjson.Object
	Reviewer *ordjson.Object
	Evidence *ordjson.Object
	Hook     *ordjson.Object
	Usage    *ordjson.Object
	Source   string
	Path     string
}

// AutoPublishEvidence is on unless an explicit evidence block turns it off.
func (s *Settings) AutoPublishEvidence() bool {
	if s == nil {
		return true
	}
	return autoPublish(s.Evidence)
}

func autoPublish(block *ordjson.Object) bool {
	if block == nil {
		return true
	}
	value, _ := block.Get("auto_publish")
	enabled, _ := value.(bool)
	return enabled
}

// EvidenceView renders the effective setting and where it came from, the way an absent capacity block reads as unlimited.
func EvidenceView(block *ordjson.Object) *ordjson.Object {
	view := ordjson.NewObject()
	view.Set("auto_publish", autoPublish(block))
	if block == nil {
		view.Set("source", "default")
	} else {
		view.Set("source", "settings")
	}
	return view
}

func LoadSettings(s *store.Store) (*Settings, error) {
	path := filepath.Join(s.Home, File)
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must not be a symlink", path)
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return &Settings{Presets: map[string]*ordjson.Object{}, Source: "unlimited", Path: path}, nil
	}
	settings, err := loadSettingsFile(path)
	if err != nil {
		return nil, fmt.Errorf("Invalid %s: %s. Fix or remove the file; nothing was admitted or changed, and existing tasks keep running. Only an explicit capacity block configures admission.", path, err)
	}
	return settings, nil
}

func loadSettingsFile(path string) (*Settings, error) {
	raw, err := ordjson.ReadFile(path)
	if err != nil {
		return nil, err
	}
	obj, ok := raw.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("top level must be an object")
	}
	schemaValue, _ := obj.Get("schema")
	if !isSchema(schemaValue, Schema) {
		return nil, fmt.Errorf("schema must be %d", Schema)
	}
	var unknown []string
	for _, k := range obj.Keys() {
		if !settingsKeys[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		allowed := []string{"capacity", "evidence", "hook", "presets", "reviewer", "schema", "usage", "worker"}
		return nil, fmt.Errorf("unknown keys %s; allowed: %s", pyrepr.StrList(unknown), pyrepr.StrList(allowed))
	}
	var capacity *ordjson.Object
	if capacityValue, has := obj.Get("capacity"); has {
		validated, err := validateCapacity(capacityValue)
		if err != nil {
			return nil, err
		}
		capacity = validated
	}
	presetsValue, _ := obj.Get("presets")
	presets, err := validatePresets(presetsValue)
	if err != nil {
		return nil, err
	}
	workerValue, _ := obj.Get("worker")
	worker, err := validateWorker(workerValue, presets)
	if err != nil {
		return nil, err
	}
	reviewerValue, _ := obj.Get("reviewer")
	reviewer, err := validateReviewer(reviewerValue, presets)
	if err != nil {
		return nil, err
	}
	var evidenceBlock *ordjson.Object
	if evidenceValue, has := obj.Get("evidence"); has {
		validated, err := validateEvidence(evidenceValue)
		if err != nil {
			return nil, err
		}
		evidenceBlock = validated
	}
	var hookBlock *ordjson.Object
	if hookValue, has := obj.Get("hook"); has {
		validated, err := validateHook(hookValue)
		if err != nil {
			return nil, err
		}
		hookBlock = validated
	}
	var usageBlock *ordjson.Object
	if usageValue, has := obj.Get("usage"); has {
		validated, err := validateEnabledBlock("usage", usageValue)
		if err != nil {
			return nil, err
		}
		usageBlock = validated
	}
	return &Settings{Capacity: capacity, Worker: worker, Presets: presets, Reviewer: reviewer, Evidence: evidenceBlock, Hook: hookBlock, Usage: usageBlock, Source: "settings.json", Path: path}, nil
}

func validateHook(value any) (*ordjson.Object, error) {
	return validateEnabledBlock("hook", value)
}

func validateEnabledBlock(name string, value any) (*ordjson.Object, error) {
	obj, _ := value.(*ordjson.Object)
	if obj == nil {
		return nil, fmt.Errorf("%s must be an object", name)
	}
	for _, key := range obj.Keys() {
		if key != "enabled" {
			return nil, fmt.Errorf("unknown %s keys %s; allowed: %s", name, pyrepr.StrList([]string{key}), pyrepr.StrList([]string{"enabled"}))
		}
	}
	enabled, has := obj.Get("enabled")
	if !has {
		return nil, fmt.Errorf("%s.enabled is required when a %s block is present", name, name)
	}
	if _, ok := enabled.(bool); !ok {
		return nil, fmt.Errorf("%s.enabled must be true or false, got %s", name, pyrepr.Repr(enabled))
	}
	canonical := ordjson.NewObject()
	canonical.Set("enabled", enabled)
	return canonical, nil
}

func UsageEnabled(s *store.Store) (bool, error) {
	loaded, err := LoadSettings(s)
	if err != nil {
		return false, err
	}
	return loaded.UsageEnabled(), nil
}

func (s *Settings) UsageEnabled() bool {
	if s == nil || s.Usage == nil {
		return false
	}
	enabled, _ := s.Usage.Get("enabled")
	value, _ := enabled.(bool)
	return value
}

func UsageView(block *ordjson.Object) *ordjson.Object {
	view := ordjson.NewObject()
	enabled := false
	if block != nil {
		value, _ := block.Get("enabled")
		enabled, _ = value.(bool)
	}
	view.Set("enabled", enabled)
	if block == nil {
		view.Set("source", "default")
	} else {
		view.Set("source", "settings")
	}
	return view
}

func NativeEventsEnabled(s *store.Store) (bool, error) {
	enabled, _, err := NativeEventsPreference(s)
	return enabled, err
}

func NativeEventsPreference(s *store.Store) (bool, bool, error) {
	loaded, err := LoadSettings(s)
	if err != nil {
		return false, false, err
	}
	if loaded.Hook == nil {
		return true, false, nil
	}
	enabled, _ := loaded.Hook.Get("enabled")
	value, ok := enabled.(bool)
	if !ok {
		return false, true, fmt.Errorf("Invalid %s: hook.enabled must be true or false", loaded.Path)
	}
	return value, true, nil
}

func isSchema(value any, want int) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	n, err := number.Int64()
	return err == nil && n == int64(want)
}

func asInt(value any) (int, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	n, err := number.Int64()
	if err != nil {
		return 0, false
	}
	return int(n), true
}

func validateCapacity(value any) (*ordjson.Object, error) {
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("capacity must be an object")
	}
	var unknown []string
	for _, k := range obj.Keys() {
		if !slices.Contains(capacityKeys, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown capacity keys %s; allowed: %s", pyrepr.StrList(unknown), pyrepr.StrList(capacityKeys))
	}
	result := map[string]int{"global": defaultCapacity["global"], "per_repository": defaultCapacity["per_repository"]}
	for _, key := range obj.Keys() {
		if key == "projects" {
			continue
		}
		raw, _ := obj.Get(key)
		n, ok := asInt(raw)
		if !ok || n < 1 || n > CapacityMax {
			return nil, fmt.Errorf("capacity.%s must be an integer between 1 and %d, got %s", key, CapacityMax, pyrepr.Repr(raw))
		}
		result[key] = n
	}
	if result["per_repository"] > result["global"] {
		return nil, fmt.Errorf("capacity.per_repository (%d) exceeds capacity.global (%d)", result["per_repository"], result["global"])
	}
	canonical := ordjson.NewObject()
	canonical.Set("global", json.Number(fmt.Sprint(result["global"])))
	canonical.Set("per_repository", json.Number(fmt.Sprint(result["per_repository"])))
	if projectsValue, has := obj.Get("projects"); has {
		projects, err := validateProjectLimits(projectsValue, result["global"])
		if err != nil {
			return nil, err
		}
		if projects != nil {
			canonical.Set("projects", projects)
		}
	}
	return canonical, nil
}

func validateProjectLimits(value any, global int) (*ordjson.Object, error) {
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("capacity.projects must be an object keyed by enrolled project name, got %s", pyrepr.Repr(value))
	}
	names := obj.Keys()
	if len(names) == 0 {
		return nil, nil
	}
	sort.Strings(names)
	canonical := ordjson.NewObject()
	for _, name := range names {
		if !project.ValidName(name) {
			return nil, fmt.Errorf("capacity.projects key %s is not a project name (owner/repo, or host/owner/repo)", pyrepr.Repr(name))
		}
		raw, _ := obj.Get(name)
		n, ok := asInt(raw)
		if !ok || n < 1 || n > CapacityMax {
			return nil, fmt.Errorf("capacity.projects.%s must be an integer between 1 and %d, got %s", name, CapacityMax, pyrepr.Repr(raw))
		}
		if n > global {
			return nil, fmt.Errorf("capacity.projects.%s (%d) exceeds capacity.global (%d)", name, n, global)
		}
		canonical.Set(name, json.Number(fmt.Sprint(n)))
	}
	return canonical, nil
}

func validateEvidence(value any) (*ordjson.Object, error) {
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("evidence must be an object")
	}
	var unknown []string
	for _, k := range obj.Keys() {
		if k != "auto_publish" {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown evidence keys %s; allowed: %s", pyrepr.StrList(unknown), pyrepr.StrList([]string{"auto_publish"}))
	}
	raw, has := obj.Get("auto_publish")
	if !has {
		return nil, fmt.Errorf("evidence.auto_publish is required when an evidence block is present")
	}
	enabled, ok := raw.(bool)
	if !ok {
		return nil, fmt.Errorf("evidence.auto_publish must be true or false, got %s", pyrepr.Repr(raw))
	}
	canonical := ordjson.NewObject()
	canonical.Set("auto_publish", enabled)
	return canonical, nil
}

func validateLaunchValue(harness, field string, value any) (string, error) {
	str, ok := value.(string)
	if !ok || !launchValuePattern.MatchString(str) {
		return "", fmt.Errorf("%s must be one plain CLI value (letters, digits, . _ : / @ , = [ ] -), got %s", field, pyrepr.Repr(value))
	}
	if _, hasAdapter := adapterPrefix(harness, field); !hasAdapter {
		known := knownHarnessesForField(field)
		return "", fmt.Errorf("No verified %s flag for harness %s; sum passes only mappings confirmed from an installed CLI's help (%s). Pass the native argument yourself with --arg, or choose a supported harness.", field, pyrepr.Repr(harness), strings.Join(known, ", "))
	}
	return str, nil
}

func validatePresetReference(field string, name any, presets map[string]*ordjson.Object) error {
	str, ok := name.(string)
	if !ok || !presetNamePattern.MatchString(str) {
		return fmt.Errorf("%s must name a preset (lowercase letters, digits, _ -), got %s", field, pyrepr.Repr(name))
	}
	if presets != nil {
		if _, exists := presets[str]; !exists {
			keys := make([]string, 0, len(presets))
			for k := range presets {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			list := "none"
			if len(keys) > 0 {
				list = pyrepr.StrList(keys)
			}
			return fmt.Errorf("%s names unknown preset %s; saved presets: %s. Create it with `preset set %s --harness ...` or point the default elsewhere.", field, pyrepr.Repr(str), list, str)
		}
	}
	return nil
}

func validateWorker(value any, presets map[string]*ordjson.Object) (*ordjson.Object, error) {
	if value == nil {
		return nil, nil
	}
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("worker must be an object")
	}
	if presetValue, hasPreset := obj.Get("preset"); hasPreset {
		if obj.Len() != 1 {
			return nil, fmt.Errorf("worker is either {'preset': NAME} or a harness/model/reasoning block, not both")
		}
		if err := validatePresetReference("worker.preset", presetValue, presets); err != nil {
			return nil, err
		}
		result := ordjson.NewObject()
		result.Set("preset", presetValue)
		return result, nil
	}
	allowed := map[string]bool{"harness": true, "model": true, "reasoning": true}
	var unknown []string
	for _, k := range obj.Keys() {
		if !allowed[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown worker keys %s; allowed: ['harness', 'model', 'reasoning'] or ['preset']", pyrepr.StrList(unknown))
	}
	harnessValue, _ := obj.Get("harness")
	harness, ok := harnessValue.(string)
	if !ok || !harnessKindPattern.MatchString(harness) {
		return nil, fmt.Errorf("worker.harness must be a Herdr integration kind such as codex or claude")
	}
	result := ordjson.NewObject()
	result.Set("harness", harness)
	for _, field := range []string{"model", "reasoning"} {
		if fieldValue, has := obj.Get(field); has && fieldValue != nil {
			validated, err := validateLaunchValue(harness, field, fieldValue)
			if err != nil {
				return nil, err
			}
			result.Set(field, validated)
		}
	}
	return result, nil
}

func validateReviewer(value any, presets map[string]*ordjson.Object) (*ordjson.Object, error) {
	if value == nil {
		return nil, nil
	}
	obj, ok := value.(*ordjson.Object)
	if !ok || obj.Len() != 1 {
		return nil, fmt.Errorf("reviewer must be {'preset': NAME}")
	}
	presetValue, hasPreset := obj.Get("preset")
	if !hasPreset {
		return nil, fmt.Errorf("reviewer must be {'preset': NAME}")
	}
	if err := validatePresetReference("reviewer.preset", presetValue, presets); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("preset", presetValue)
	return result, nil
}

func validatePreset(name string, value any) (*ordjson.Object, error) {
	if !presetNamePattern.MatchString(name) {
		return nil, fmt.Errorf("preset name must be lowercase letters, digits, _ or - (up to 32 characters), got %s", pyrepr.Repr(name))
	}
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("presets.%s must be an object", name)
	}
	allowed := map[string]bool{"harness": true, "model": true, "reasoning": true, "args": true, "revision": true}
	var unknown []string
	for _, k := range obj.Keys() {
		if !allowed[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown keys %s in presets.%s; allowed: %s", pyrepr.StrList(unknown), name, pyrepr.StrList([]string{"harness", "model", "reasoning", "args", "revision"}))
	}
	harnessValue, _ := obj.Get("harness")
	harness, ok := harnessValue.(string)
	if !ok || !harnessKindPattern.MatchString(harness) {
		return nil, fmt.Errorf("presets.%s.harness must be a Herdr integration kind such as codex or claude", name)
	}
	result := ordjson.NewObject()
	result.Set("harness", harness)
	for _, field := range []string{"model", "reasoning"} {
		if fieldValue, has := obj.Get(field); has && fieldValue != nil {
			validated, err := validateLaunchValue(harness, field, fieldValue)
			if err != nil {
				return nil, err
			}
			result.Set(field, validated)
		}
	}
	var args []string
	if rawArgs, has := obj.Get("args"); has && rawArgs != nil {
		list, isList := rawArgs.([]any)
		if !isList {
			return nil, fmt.Errorf("presets.%s.args must be a list of plain non-empty strings", name)
		}
		for _, item := range list {
			s, ok := item.(string)
			if !ok || s == "" || strings.Contains(s, "\x00") {
				return nil, fmt.Errorf("presets.%s.args must be a list of plain non-empty strings", name)
			}
			args = append(args, s)
		}
	}
	for _, field := range []string{"model", "reasoning"} {
		if fieldValue, has := result.Get(field); has {
			if s, isStr := fieldValue.(string); isStr && argvConflicts(harness, field, args) {
				return nil, fmt.Errorf("presets.%s: %s %s and an entry of args both set the %s %s flag. Give one.", name, field, pyrepr.Repr(s), harness, field)
			}
		}
	}
	if len(args) > 0 {
		argsAny := make([]any, len(args))
		for i, a := range args {
			argsAny[i] = a
		}
		result.Set("args", argsAny)
	}
	revision := 1
	if revisionValue, has := obj.Get("revision"); has {
		n, ok := asInt(revisionValue)
		if !ok || n < 1 {
			return nil, fmt.Errorf("presets.%s.revision must be a positive integer", name)
		}
		revision = n
	}
	result.Set("revision", json.Number(fmt.Sprint(revision)))
	return result, nil
}

func validatePresets(value any) (map[string]*ordjson.Object, error) {
	if value == nil {
		return map[string]*ordjson.Object{}, nil
	}
	obj, ok := value.(*ordjson.Object)
	if !ok {
		return nil, fmt.Errorf("presets must be an object keyed by preset name")
	}
	if obj.Len() > presetMax {
		return nil, fmt.Errorf("at most %d presets are supported", presetMax)
	}
	result := map[string]*ordjson.Object{}
	for _, name := range obj.Keys() {
		spec, _ := obj.Get(name)
		validated, err := validatePreset(name, spec)
		if err != nil {
			return nil, err
		}
		result[name] = validated
	}
	return result, nil
}

func presetSummary(presets map[string]*ordjson.Object) *ordjson.Object {
	names := make([]string, 0, len(presets))
	for name := range presets {
		names = append(names, name)
	}
	sort.Strings(names)
	result := ordjson.NewObject()
	for _, name := range names {
		spec := presets[name]
		entry := ordjson.NewObject()
		harness, _ := spec.Get("harness")
		entry.Set("harness", harness)
		model, hasModel := spec.Get("model")
		if !hasModel {
			model = nil
		}
		entry.Set("model", model)
		reasoning, hasReasoning := spec.Get("reasoning")
		if !hasReasoning {
			reasoning = nil
		}
		entry.Set("reasoning", reasoning)
		args, hasArgs := spec.Get("args")
		if !hasArgs {
			args = []any{}
		}
		entry.Set("args", args)
		revision, _ := spec.Get("revision")
		entry.Set("revision", revision)
		result.Set(name, entry)
	}
	return result
}

func occupancy(tasks []*ordjson.Object) (*ordjson.Object, error) {
	byRepo := ordjson.NewObject()
	count := 0
	for _, t := range tasks {
		held, err := reservations.Held(t)
		if err != nil {
			idValue, _ := t.Get("id")
			return nil, fmt.Errorf("Malformed execution reservation for %v: %s. Admission and release are refused.", idValue, err)
		}
		count += len(held)
		if len(held) == 0 {
			continue
		}
		repoValue, _ := t.Get("repository")
		repo, _ := repoValue.(string)
		idValue, _ := t.Get("id")
		var list []any
		if existing, has := byRepo.Get(repo); has {
			list, _ = existing.([]any)
		}
		for range held {
			list = append(list, idValue)
		}
		byRepo.Set(repo, list)
	}
	result := ordjson.NewObject()
	result.Set("global", json.Number(fmt.Sprint(count)))
	result.Set("by_repository", byRepo)
	return result, nil
}

func orNil(obj *ordjson.Object) any {
	if obj == nil {
		return nil
	}
	return obj
}

func CapacityView(s *store.Store) (*ordjson.Object, error) {
	tasks, err := s.AllTasks()
	if err != nil {
		return nil, err
	}
	return CapacityViewOf(s, tasks)
}

// CapacityViewOf is CapacityView over tasks the caller already read in this operation.
func CapacityViewOf(s *store.Store, tasks []*ordjson.Object) (*ordjson.Object, error) {
	loaded, err := LoadSettings(s)
	if err != nil {
		occupied, occErr := occupancy(tasks)
		if occErr != nil {
			return nil, occErr
		}
		result := ordjson.NewObject()
		result.Set("limits", nil)
		result.Set("worker", nil)
		result.Set("source", "invalid")
		result.Set("error", err.Error())
		result.Set("occupied", occupied)
		result.Set("note", "Admission is refused until settings.json is fixed; every recorded task keeps its slot and callbacks.")
		return result, nil
	}
	occupied, occErr := occupancy(tasks)
	if occErr != nil {
		result := ordjson.NewObject()
		result.Set("limits", orNil(loaded.Capacity))
		result.Set("worker", orNil(loaded.Worker))
		result.Set("source", "invalid")
		result.Set("error", occErr.Error())
		result.Set("occupied", nil)
		result.Set("note", "Admission and release are refused until the malformed execution record is repaired from exact ownership evidence.")
		return result, nil
	}
	result := ordjson.NewObject()
	result.Set("limits", orNil(loaded.Capacity))
	result.Set("worker", orNil(loaded.Worker))
	result.Set("source", loaded.Source)
	result.Set("occupied", occupied)
	if loaded.Capacity != nil {
		result.Set("effective", effectiveView(s, loaded.Capacity, tasks))
	}
	result.Set("presets", presetSummary(loaded.Presets))
	result.Set("reviewer", orNil(loaded.Reviewer))
	result.Set("evidence", EvidenceView(loaded.Evidence))
	result.Set("worker_note", "Saved worker defaults apply to future dispatches only; absent means the worker runs the coordinator's harness. A task prompt overrides them without changing them.")
	result.Set("note", "Each recorded execution reservation holds a slot until a conclusive stop observation releases it; legacy non-archived tasks remain conservatively held.")
	return result, nil
}

func AdapterArgv(harness, field string, value string) []string {
	prefix, _ := adapterPrefix(harness, field)
	if len(prefix) == 0 {
		return nil
	}
	last := prefix[len(prefix)-1]
	if strings.HasSuffix(last, "=") {
		argv := append([]string{}, prefix[:len(prefix)-1]...)
		return append(argv, last+value)
	}
	return append(append([]string{}, prefix...), value)
}

func presetLaunch(spec *ordjson.Object) *ordjson.Object {
	harnessValue, _ := spec.Get("harness")
	harness, _ := harnessValue.(string)
	var argv []string
	for _, field := range []string{"model", "reasoning"} {
		if value, has := spec.Get(field); has {
			if s, ok := value.(string); ok {
				argv = append(argv, AdapterArgv(harness, field, s)...)
			}
		}
	}
	if rawArgs, has := spec.Get("args"); has {
		if list, ok := rawArgs.([]any); ok {
			for _, a := range list {
				if s, ok := a.(string); ok {
					argv = append(argv, s)
				}
			}
		}
	}
	result := ordjson.NewObject()
	result.Set("harness", harness)
	model, hasModel := spec.Get("model")
	if !hasModel {
		model = nil
	}
	result.Set("model", model)
	reasoning, hasReasoning := spec.Get("reasoning")
	if !hasReasoning {
		reasoning = nil
	}
	result.Set("reasoning", reasoning)
	result.Set("argv", anyStrings(argv))
	if overrides := PermissionOverrides(harness, argv); len(overrides) > 0 {
		result.Set("permission_overrides", anyStrings(overrides))
	}
	return result
}

func anyStrings(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

func presetReferences(worker, reviewer *ordjson.Object, name string) []string {
	var refs []string
	if worker != nil {
		if presetValue, has := worker.Get("preset"); has && presetValue == name {
			refs = append(refs, "worker default")
		}
	}
	if reviewer != nil {
		if presetValue, has := reviewer.Get("preset"); has && presetValue == name {
			refs = append(refs, "reviewer default")
		}
	}
	return refs
}

func PresetList(s *store.Store) (*ordjson.Object, error) {
	loaded, err := LoadSettings(s)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("presets", presetSummary(loaded.Presets))
	result.Set("worker", orNil(loaded.Worker))
	result.Set("reviewer", orNil(loaded.Reviewer))
	result.Set("source", loaded.Source)
	result.Set("path", loaded.Path)
	result.Set("note", "Presets are dispatch shortcuts expanded at prepare; each task keeps the specification it was prepared with. Nothing here is a running agent, a role, or a default until you say so.")
	return result, nil
}

func PresetShow(s *store.Store, name string) (*ordjson.Object, error) {
	loaded, err := LoadSettings(s)
	if err != nil {
		return nil, err
	}
	if err := validatePresetReference("preset", name, loaded.Presets); err != nil {
		return nil, err
	}
	spec := loaded.Presets[name]
	revision, _ := spec.Get("revision")
	usedBy := presetReferences(loaded.Worker, loaded.Reviewer, name)
	usedByAny := make([]any, len(usedBy))
	for i, u := range usedBy {
		usedByAny[i] = u
	}
	result := ordjson.NewObject()
	result.Set("name", name)
	result.Set("revision", revision)
	result.Set("preset", spec)
	result.Set("launch", presetLaunch(spec))
	result.Set("used_by", usedByAny)
	result.Set("note", "`launch.argv` is exactly what `dispatch --preset` appends after the harness executable; a model here is CLI-requested, never runtime-verified.")
	return result, nil
}
