package scaffold

import (
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/douglasjarquin/sum/tools/verify/internal/contract"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
)

const (
	defaultMaps = "docs/features/README.md"
	artifacts   = ".artifacts/verification"
	todo        = "TODO(verify)"
)

var (
	readmeGapTasks = []string{"lint"}
	nonSlug        = regexp.MustCompile(`[^a-z0-9]+`)
)

type feature struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Surface string `json:"surface"`
	Entry   string `json:"entry"`
}

type plannedFile struct {
	path, content string
	// kind is "append" for a file the scaffold extends in place and "create" otherwise.
	kind string
}

type planned struct {
	FeatureMaps string    `json:"feature_maps"`
	Commands    []string  `json:"commands"`
	Features    []feature `json:"features"`
	Freshness   []string  `json:"freshness"`
	files       []plannedFile
}

// setFile plans one file; a path planned twice keeps its first position and its last content.
func (p *planned) setFile(file plannedFile) {
	for i := range p.files {
		if p.files[i].path == file.path {
			p.files[i] = file
			return
		}
	}
	p.files = append(p.files, file)
}

type readmeRow struct {
	Task    string  `json:"task"`
	Owner   *string `json:"owner"`
	Example *string `json:"example"`
	Check   *string `json:"check"`
	Kind    string  `json:"kind"`
}

func ownerFor(root string, info inspection, task string) string {
	for _, entry := range info.Mise.Own {
		if entry.Name != task {
			continue
		}
		if entry.Source == nil {
			break
		}
		rel, err := filepath.Rel(root, *entry.Source)
		if err != nil {
			return filepath.Base(*entry.Source)
		}
		return filepath.ToSlash(rel)
	}
	if info.PackageScripts.Has(task) {
		return "package.json"
	}
	for _, t := range info.MakeTargets {
		if t.Name == task {
			return targetFile(t)
		}
	}
	return ""
}

func targetFile(t target) string {
	if t.Runner == "just" {
		return "Justfile"
	}
	return "Makefile"
}

func exampleFor(info inspection, task string) string {
	if !slices.Contains([]string{"test", "tests", "unit", "spec", "e2e", "check"}, task) {
		return ""
	}
	if len(info.Tests) > 0 {
		return info.Tests[0].Examples[0]
	}
	if len(info.CLIEntrypoints) > 0 {
		return info.CLIEntrypoints[0]
	}
	return ""
}

// readmeMap maps each observed task to its owner, an example, and its check. Missing cells stay null. File existence is not enforcement.
func readmeMap(root string, info inspection) []readmeRow {
	rows := []readmeRow{}
	seen := map[string]bool{}
	add := func(task, owner, example, check string) {
		row := readmeRow{Task: task, Kind: "gap"}
		if owner != "" && example != "" {
			row = readmeRow{Task: task, Owner: &owner, Example: &example, Check: &check, Kind: "observed"}
		}
		rows = append(rows, row)
		seen[task] = true
	}
	for _, name := range info.CheckTasks {
		add(name, ownerFor(root, info, name), exampleFor(info, name), "mise run "+name)
	}
	for _, script := range info.PackageScripts.Keys() {
		if slices.Contains(checkTaskNames, script) && !seen[script] {
			add(script, "package.json", exampleFor(info, script), "npm run "+script)
		}
	}
	for _, t := range info.MakeTargets {
		if slices.Contains(checkTaskNames, t.Name) && !seen[t.Name] {
			add(t.Name, targetFile(t), exampleFor(info, t.Name), t.Runner+" "+t.Name)
		}
	}
	for _, name := range readmeGapTasks {
		if !seen[name] {
			add(name, "", "", "")
		}
	}
	return rows
}

// verifyTaskCommands composes the aggregate `verify` task from checks the repository declares. With nothing to reuse there is no task: a command that merely succeeds is refused.
func verifyTaskCommands(info inspection) []string {
	commands := []string{}
	// Stable order: test first, then check, lint, typecheck, ...; build is a prerequisite, not a check.
	for _, name := range checkTaskNames {
		if name != "build" && slices.Contains(info.CheckTasks, name) {
			commands = append(commands, "mise run "+name)
		}
	}
	if len(commands) == 0 {
		for _, name := range []string{"test", "lint", "check", "typecheck"} {
			if info.PackageScripts.Has(name) {
				commands = append(commands, "npm run "+name)
			}
		}
		for _, t := range info.MakeTargets {
			if slices.Contains([]string{"test", "check", "lint"}, t.Name) {
				commands = append(commands, t.Runner+" "+t.Name)
			}
		}
	}
	if len(commands) == 0 {
		for _, location := range info.Tests {
			if slices.ContainsFunc(location.Examples, func(e string) bool { return strings.HasSuffix(e, ".py") }) {
				commands = append(commands, "python3 -m unittest discover -s "+location.Dir+" -p 'test_*.py'")
				break
			}
		}
	}
	return commands
}

func featureID(text string) string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(text), "-"), "-")
	if len(slug) > 40 {
		slug = slug[:40]
	}
	if slug == "" {
		return "feature"
	}
	return slug
}

// stem is the last path component of a command without its extension.
func stem(command string) string {
	name := path.Base(strings.TrimRight(command, "/"))
	if name == "." || name == "/" {
		return ""
	}
	return strings.TrimSuffix(name, suffix(name))
}

// seedFeatures picks the top real user-facing features the inspection found, at most five; every other surface stays listed as unmapped inventory.
func seedFeatures(info inspection) []feature {
	features := []feature{}
	for _, route := range info.Routes {
		if len(features) >= 5 {
			break
		}
		features = append(features, feature{ID: "service." + featureID(route), Title: "HTTP `" + route + "`", Surface: "service", Entry: route})
	}
	for _, entry := range info.CLIEntrypoints {
		if len(features) >= 5 {
			break
		}
		command, _, _ := strings.Cut(entry, " ")
		features = append(features, feature{ID: "cli." + featureID(stem(command)), Title: "Command `" + entry + "`", Surface: "cli", Entry: entry})
	}
	if slices.Contains(info.Surfaces, "web") && len(features) < 5 {
		features = append(features, feature{ID: "web.home", Title: "Web page", Surface: "web", Entry: "the served index page"})
	}
	return features
}

// miseTomlWithVerify returns mise.toml with the aggregate task added, or ok=false when the file already defines one.
func miseTomlWithVerify(root string, commands []string) (content string, ok bool) {
	text := read(filepath.Join(root, "mise.toml"))
	run := tomlString(commands[0])
	if len(commands) > 1 {
		quoted := make([]string, len(commands))
		for i, command := range commands {
			quoted[i] = "  " + tomlString(command)
		}
		run = "[\n" + strings.Join(quoted, ",\n") + "\n]"
	}
	block := "[tasks.verify]\ndescription = \"Canonical aggregate verification (see VERIFY.md)\"\nrun = " + run + "\n"
	if text == "" {
		return block, true
	}
	if definesVerifyTask(text) {
		return "", false
	}
	return strings.TrimRight(text, "\n") + "\n\n" + block, true
}

func definesVerifyTask(miseToml string) bool {
	return strings.Contains(miseToml, "[tasks.verify]") || verifyAssignment.MatchString(miseToml)
}

// tomlString quotes a command as a TOML basic string, which shares its escapes with JSON.
func tomlString(command string) string {
	data, _ := tool.EncodeJSON(command)
	return strings.TrimSuffix(string(data), "\n")
}

// plan lists every file the scaffold would produce, with its content. Existing files are compared, never rewritten.
func plan(root string, info inspection) planned {
	featureMaps := defaultMaps
	if info.ExistingContract != nil {
		// A declared location is kept only when it stays inside the repository.
		if declared, _ := info.ExistingContract.FeatureMaps.(string); declared != "" && !strings.HasPrefix(declared, "/") && !slices.Contains(strings.Split(declared, "/"), "..") {
			featureMaps = declared
		}
	}
	p := planned{FeatureMaps: featureMaps, Commands: verifyTaskCommands(info), Features: seedFeatures(info)}
	hasBuild := slices.ContainsFunc(info.Mise.Own, func(t miseTask) bool { return t.Name == "build" })
	if slices.Contains(info.Surfaces, "web") && pycompat.IsDir(filepath.Join(root, "src")) && hasBuild && (pycompat.Exists(filepath.Join(root, "dist")) || info.MentionsDist) {
		p.Freshness = []string{"src", "dist"}
	}
	if len(p.Commands) > 0 && !info.HasVerifyTask {
		if content, ok := miseTomlWithVerify(root, p.Commands); ok {
			kind := "create"
			if pycompat.IsFile(filepath.Join(root, "mise.toml")) {
				kind = "append"
			}
			p.setFile(plannedFile{path: "mise.toml", content: content, kind: kind})
		}
	}
	p.setFile(plannedFile{path: contract.File, content: renderVerifyMD(root, info, p), kind: "create"})
	p.setFile(plannedFile{path: featureMaps, content: renderIndex(p.Features, info), kind: "create"})
	mapsDir := path.Dir(path.Clean(featureMaps))
	for _, f := range p.Features {
		p.setFile(plannedFile{path: path.Join(mapsDir, f.ID+".md"), content: renderFeature(info, f), kind: "create"})
	}
	gitignore := read(filepath.Join(root, ".gitignore"))
	if !strings.Contains(gitignore, ".artifacts/") && !slices.Contains(strings.Fields(gitignore), ".artifacts") {
		file := plannedFile{path: ".gitignore", content: "\n# Verification run records written by .agents/skills/verify (see VERIFY.md).\n.artifacts/\n", kind: "create"}
		if gitignore != "" {
			file = plannedFile{path: ".gitignore", content: strings.TrimRight(gitignore, "\n") + "\n" + file.content, kind: "append"}
		}
		p.setFile(file)
	}
	return p
}
