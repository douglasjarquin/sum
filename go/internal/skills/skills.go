package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/pyrepr"
)

const Version = "1.5.25"

var (
	sumSkillNames      = []string{"sum-delivery", "sum-develop", "sum-dispatch", "sum-rundown", "sum-update", "sum-worker"}
	portableSkillNames = []string{"create-verification", "evidence", "maintain-verification", "verify"}
	skillArgument      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

func Check(root string) (*ordjson.Object, error) {
	skillsDir := filepath.Join(root, "skills")
	info, err := os.Stat(skillsDir)
	if err != nil || !info.IsDir() {
		result := ordjson.NewObject()
		result.Set("ok", true)
		result.Set("active", []any{})
		result.Set("routes", ordjson.NewObject())
		result.Set("compatibility", []any{})
		result.Set("errors", []any{})
		return result, nil
	}
	return inventory(root)
}

func inventory(root string) (*ordjson.Object, error) {
	var errors []any
	var compatibility []any
	active := make([]any, len(sumSkillNames))
	for i, name := range sumSkillNames {
		active[i] = name
	}
	canonicalRoot := filepath.Join(root, "skills")
	entries, err := os.ReadDir(canonicalRoot)
	if err != nil {
		errors = append(errors, fmt.Sprintf("missing skill source directory: %s", canonicalRoot))
	} else {
		for _, child := range entries {
			name := child.Name()
			if strings.HasPrefix(name, "sum-") && !contains(sumSkillNames, name) {
				errors = append(errors, fmt.Sprintf("namespace collision: unexpected Sum skill %s", name))
			}
		}
		for _, name := range sumSkillNames {
			path := filepath.Join(canonicalRoot, name)
			skillFile := filepath.Join(path, "SKILL.md")
			info, statErr := os.Lstat(path)
			if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				errors = append(errors, fmt.Sprintf("missing canonical skill directory: %s", path))
				continue
			}
			if !isFile(skillFile) {
				errors = append(errors, fmt.Sprintf("missing skill resource: %s", skillFile))
				continue
			}
			if found := frontmatterName(skillFile); found != name {
				errors = append(errors, fmt.Sprintf("skill name mismatch: %s is %s, expected %s", skillFile, pyrepr.Repr(foundName(skillFile)), pyrepr.Repr(name)))
			}
		}
		for _, canonical := range sumSkillNames {
			legacy := strings.TrimPrefix(canonical, "sum-")
			path := filepath.Join(canonicalRoot, legacy)
			target, linkErr := os.Readlink(path)
			if linkErr == nil && target == canonical {
				compatibility = append(compatibility, fmt.Sprintf("skills/%s->skills/%s", legacy, canonical))
			} else if exists(path) {
				errors = append(errors, fmt.Sprintf("legacy compatibility reference mismatch: %s must point to %s", path, canonical))
			}
		}
	}
	sort.Slice(compatibility, func(i, j int) bool {
		return compatibility[i].(string) < compatibility[j].(string)
	})

	routes := ordjson.NewObject()
	for _, route := range []struct {
		name         string
		directory    string
		nativeTarget *string
	}{
		{"agents", filepath.Join(root, ".agents/skills"), nil},
		{"claude", filepath.Join(root, ".claude/skills"), strPtr(".agents/skills")},
	} {
		var discovered []string
		info, statErr := os.Stat(route.directory)
		if statErr != nil || !info.IsDir() {
			errors = append(errors, fmt.Sprintf("missing discovery route: %s", route.directory))
			routes.Set(route.name, []any{})
			continue
		}
		children, readErr := os.ReadDir(route.directory)
		if readErr != nil {
			errors = append(errors, fmt.Sprintf("missing discovery route: %s", route.directory))
			routes.Set(route.name, []any{})
			continue
		}
		for _, child := range children {
			name := child.Name()
			if strings.HasPrefix(name, "sum-") {
				if !contains(sumSkillNames, name) {
					errors = append(errors, fmt.Sprintf("namespace collision: unexpected projected skill %s in %s", name, route.directory))
				} else {
					discovered = append(discovered, name)
				}
			}
		}
		for _, name := range sumSkillNames {
			path := filepath.Join(route.directory, name)
			target := "../../skills/" + name
			if link, linkErr := os.Readlink(path); linkErr != nil || link != target {
				errors = append(errors, fmt.Sprintf("projection mismatch: %s must point to %s", path, target))
			}
		}
		for _, name := range portableSkillNames {
			path := filepath.Join(route.directory, name)
			if route.nativeTarget == nil {
				info, err := os.Lstat(path)
				if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || frontmatterName(filepath.Join(path, "SKILL.md")) != name {
					errors = append(errors, fmt.Sprintf("portable skill mismatch: %s must remain a native %s skill", path, name))
				}
			} else {
				target := "../../.agents/skills/" + name
				if link, linkErr := os.Readlink(path); linkErr != nil || link != target {
					errors = append(errors, fmt.Sprintf("portable projection mismatch: %s must point to %s", path, target))
				}
			}
		}
		seen := map[string]bool{}
		var combined []any
		for _, name := range append(discovered, portableSkillNames...) {
			if seen[name] {
				continue
			}
			seen[name] = true
			combined = append(combined, name)
		}
		sort.Slice(combined, func(i, j int) bool { return combined[i].(string) < combined[j].(string) })
		routes.Set(route.name, combined)
	}

	result := ordjson.NewObject()
	result.Set("ok", len(errors) == 0)
	result.Set("active", active)
	result.Set("routes", routes)
	result.Set("compatibility", compatibility)
	result.Set("errors", errors)
	return result, nil
}

func Install(runtimeRoot, target, source string, skillNames, agentNames []string) (*ordjson.Object, error) {
	expanded := expandUser(target)
	info, err := os.Lstat(expanded)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("Skill target must be an existing project directory, not a symlink: %s", expanded)
	}
	resolved, err := filepath.Abs(expanded)
	if err != nil {
		return nil, err
	}
	if evaluated, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil {
		resolved = evaluated
	}
	project, err := proc.Run([]string{"git", "-C", resolved, "rev-parse", "--show-toplevel"}, "", 0, false, nil)
	if err != nil {
		return nil, err
	}
	top := strings.TrimSpace(project.Stdout)
	topResolved := top
	if abs, absErr := filepath.Abs(top); absErr == nil {
		if evaluated, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
			topResolved = evaluated
		} else {
			topResolved = abs
		}
	}
	if project.Code != 0 || topResolved != resolved {
		return nil, fmt.Errorf("Skill target must be the root of a Git project: %s. Nothing was installed globally.", resolved)
	}
	if source == "" || strings.HasPrefix(source, "-") {
		return nil, fmt.Errorf("Skill source must be one explicit package, repository URL, or local path and cannot look like an option.")
	}
	resolvedSource := source
	sourceExpanded := expandUser(source)
	if _, statErr := os.Stat(sourceExpanded); statErr == nil {
		if abs, absErr := filepath.Abs(sourceExpanded); absErr == nil {
			if evaluated, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
				resolvedSource = evaluated
			} else {
				resolvedSource = abs
			}
		}
	}
	for _, name := range skillNames {
		if !skillArgument.MatchString(name) || strings.HasPrefix(strings.ToLower(name), "sum-") {
			return nil, fmt.Errorf("Every selected skill must be an explicit safe name outside Sum's reserved sum-* namespace; wildcards are refused.")
		}
	}
	for _, agent := range agentNames {
		if !skillArgument.MatchString(agent) {
			return nil, fmt.Errorf("Every agent must be an explicit safe name; wildcards are refused.")
		}
	}
	bin := filepath.Join(runtimeRoot, ".local", "bin", "skills")
	binInfo, binErr := os.Stat(bin)
	if binErr != nil || binInfo.IsDir() || binInfo.Mode()&0o111 == 0 {
		return nil, fmt.Errorf("Pinned Vercel Skills CLI is missing from this runtime: %s. Run mise run setup or stage a release.", bin)
	}
	foundRun, err := proc.Run([]string{bin, "--version"}, "", 30*time.Second, true, nil)
	if err != nil {
		return nil, err
	}
	found := strings.TrimSpace(foundRun.Stdout)
	if found != Version {
		return nil, fmt.Errorf("Vercel Skills CLI at %s is %s, not the tested pin %s. Nothing was installed.", bin, pyrepr.Repr(found), Version)
	}
	command := []string{bin, "add", resolvedSource, "--skill"}
	command = append(command, skillNames...)
	command = append(command, "--agent")
	command = append(command, agentNames...)
	command = append(command, "--copy", "--yes")
	env := append([]string{}, os.Environ()...)
	env = append(env, "NO_COLOR=1", "DO_NOT_TRACK=1", "DISABLE_TELEMETRY=1")
	if _, err := proc.Run(command, resolved, 300*time.Second, true, env); err != nil {
		return nil, err
	}
	skillsAny := make([]any, len(skillNames))
	for i, n := range skillNames {
		skillsAny[i] = n
	}
	agentsAny := make([]any, len(agentNames))
	for i, n := range agentNames {
		agentsAny[i] = n
	}
	tool := ordjson.NewObject()
	tool.Set("path", bin)
	tool.Set("version", found)
	result := ordjson.NewObject()
	result.Set("target", resolved)
	result.Set("source", resolvedSource)
	result.Set("skills", skillsAny)
	result.Set("agents", agentsAny)
	result.Set("scope", "project")
	result.Set("mode", "copy")
	result.Set("tool", tool)
	result.Set("note", "Vercel Skills installed explicit project-local copies. Review the copied skill before use.")
	return result, nil
}

func foundName(path string) any {
	name := frontmatterName(path)
	if name == "" {
		if _, err := os.ReadFile(path); err != nil {
			return nil
		}
		return nil
	}
	return name
}

func frontmatterName(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	text := string(data)
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(key) == "name" {
			return strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return ""
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func strPtr(s string) *string { return &s }

func expandUser(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		if path == "~" {
			return home
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
