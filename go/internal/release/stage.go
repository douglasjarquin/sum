package release

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/skills"
	"github.com/douglasjarquin/sum/go/internal/store"
)

var installTools = []string{"python3", "node", "herdr", "gh", "quota-axi", "codegraph", "skills"}

func Stage(s *store.Store, ref string) (*ordjson.Object, error) {
	root, err := InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	if ref == "" {
		ref = "HEAD"
	}
	shaOut, err := proc.Run([]string{"git", "-C", root, "rev-parse", "--verify", ref + "^{commit}", "--"}, "", 30*time.Second, true, nil)
	if err != nil {
		return nil, err
	}
	sha := strings.TrimSpace(shaOut.Stdout)
	releases := filepath.Join(root, ".local", "releases")
	if err := os.MkdirAll(releases, 0o700); err != nil {
		return nil, err
	}
	final := filepath.Join(releases, sha)
	if info, statErr := os.Stat(final); statErr == nil && info.IsDir() {
		manifest, verErr := VerifyRelease(final, sha)
		if verErr != nil {
			return nil, verErr
		}
		return ReleaseSummary(final, manifest, false)
	}
	staging, err := os.MkdirTemp(releases, ".staging-"+sha[:12]+"-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	cmd := exec.Command("git", "-C", root, "archive", "--format=tar", sha)
	archiveOut, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git archive: %s", err)
	}
	tar := exec.Command("tar", "-xf", "-", "-C", staging)
	tar.Stdin = strings.NewReader(string(archiveOut))
	if err := tar.Run(); err != nil {
		return nil, fmt.Errorf("extract archive: %s", err)
	}
	inventory, err := skills.Check(staging)
	if err != nil {
		return nil, err
	}
	if ok, _ := inventory.Get("ok"); ok != true {
		errors, _ := inventory.Get("errors")
		return nil, fmt.Errorf("Skill inventory refused release staging: %v", errors)
	}
	if _, err := os.Stat(filepath.Join(staging, ".sum")); err == nil {
		return nil, fmt.Errorf("The committed tree must not contain .sum or a release manifest.")
	}
	if err := installRuntime(staging); err != nil {
		return nil, fmt.Errorf("Staging %s failed and its partial bundle was removed; existing releases and the current setup are unchanged. %s", sha, err)
	}
	manifest, err := BuildManifest(s, root, sha, staging)
	if err != nil {
		return nil, fmt.Errorf("Staging %s failed and its partial bundle was removed; existing releases and the current setup are unchanged. %s", sha, err)
	}
	if err := ordjson.WriteFile(filepath.Join(staging, Manifest), manifest); err != nil {
		return nil, err
	}
	if _, err := VerifyRelease(staging, sha); err != nil {
		return nil, fmt.Errorf("Staging %s failed and its partial bundle was removed; existing releases and the current setup are unchanged. %s", sha, err)
	}
	if err := os.Rename(staging, final); err != nil {
		if _, statErr := os.Stat(final); statErr == nil {
			verified, verErr := VerifyRelease(final, sha)
			if verErr != nil {
				return nil, verErr
			}
			return ReleaseSummary(final, verified, false)
		}
		return nil, err
	}
	verified, err := VerifyRelease(final, sha)
	if err != nil {
		return nil, err
	}
	return ReleaseSummary(final, verified, true)
}

func installRuntime(target string) error {
	if os.Getenv("SUM_STAGE_OFFLINE") != "" {
		return installOffline(target)
	}
	miseEnv := append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+filepath.Join(target, "mise.toml"))
	if _, err := proc.Run([]string{"mise", "install"}, target, 900*time.Second, true, miseEnv); err != nil {
		return err
	}
	localBin := filepath.Join(target, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		return err
	}
	for _, name := range installTools {
		which, err := proc.Run([]string{"mise", "which", name}, target, 60*time.Second, true, miseEnv)
		if err != nil {
			return err
		}
		resolved := strings.TrimSpace(which.Stdout)
		if resolved == "" {
			return fmt.Errorf("mise resolved %s to an empty path", name)
		}
		if err := linkTool(filepath.Join(localBin, name), resolved); err != nil {
			return err
		}
	}
	if err := buildNative(target); err != nil {
		return err
	}
	return writeHerdrSkill(target)
}

func lookPathOr(name, fallback string) string {
	if found, err := exec.LookPath(name); err == nil && found != "" {
		return found
	}
	return fallback
}

func installOffline(target string) error {
	localBin := filepath.Join(target, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		return err
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		return fmt.Errorf("offline stage needs python3 on PATH")
	}
	herdr := os.Getenv("SUM_HERDR_BIN")
	if herdr == "" {
		herdr = python
	}
	gh := os.Getenv("SUM_GH_BIN")
	if gh == "" {
		gh = lookPathOr("gh", herdr)
	}
	codegraph := os.Getenv("SUM_CODEGRAPH_BIN")
	if codegraph == "" {
		codegraph = herdr
	}
	links := map[string]string{
		"python3":   python,
		"node":      lookPathOr("node", python),
		"herdr":     herdr,
		"gh":        gh,
		"quota-axi": python,
		"codegraph": codegraph,
		"skills":    python,
	}
	for name, dest := range links {
		if err := linkTool(filepath.Join(localBin, name), dest); err != nil {
			return err
		}
	}
	if err := buildNative(target); err != nil {
		return err
	}
	skillDir := filepath.Join(target, ".local", "skills", "herdr")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("offline herdr skill\n"), 0o644); err != nil {
		return err
	}
	for _, parent := range []string{filepath.Join(target, ".agents", "skills"), filepath.Join(target, ".claude", "skills")} {
		_ = linkTool(filepath.Join(parent, "herdr"), "../../.local/skills/herdr")
	}
	return nil
}

func linkTool(link, target string) error {
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	info, err := os.Lstat(link)
	if err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("Refusing to replace non-symlink %s", link)
		}
		return nil
	}
	return os.Symlink(target, link)
}

func nativePlatform() string {
	system := runtime.GOOS
	machine := runtime.GOARCH
	switch system {
	case "darwin", "linux":
	default:
		system = runtime.GOOS
	}
	switch machine {
	case "amd64", "arm64":
	default:
		machine = runtime.GOARCH
	}
	return system + "-" + machine
}

func buildNative(target string) error {
	source := filepath.Join(target, "go")
	if _, err := os.Stat(filepath.Join(source, "go.mod")); err != nil {
		return fmt.Errorf("Native Go source is missing from %s", source)
	}
	goBin := os.Getenv("SUM_GO_BIN")
	if goBin == "" {
		which, err := proc.Run([]string{"mise", "which", "go"}, target, 60*time.Second, false, nil)
		if err == nil && strings.TrimSpace(which.Stdout) != "" {
			goBin = strings.TrimSpace(which.Stdout)
		}
	}
	if goBin == "" {
		if found, err := exec.LookPath("go"); err == nil {
			goBin = found
		}
	}
	if goBin == "" {
		return fmt.Errorf("Missing Go 1.25+; install the pinned build tool with mise before staging native artifacts.")
	}
	outDir := filepath.Join(target, ".local", "bin")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	platform := nativePlatform()
	parts := strings.SplitN(platform, "-", 2)
	env := append(os.Environ(), "CGO_ENABLED=0", "GOENV=off", "GOOS="+parts[0], "GOARCH="+parts[1])
	outputs := []struct{ name, pkg string }{{"sumctl", "./cmd/sumctl"}, {"herdr-mesh", "./cmd/herdr-mesh"}}
	for _, item := range outputs {
		dest := filepath.Join(outDir, item.name)
		if info, err := os.Stat(dest); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			continue
		}
		if _, err := proc.Run([]string{goBin, "build", "-trimpath", "-buildvcs=false", "-o", dest, item.pkg}, source, 900*time.Second, true, env); err != nil {
			return err
		}
	}
	return nil
}

func writeHerdrSkill(target string) error {
	herdr := filepath.Join(target, ".local", "bin", "herdr")
	out, err := proc.Run([]string{herdr, "--skill"}, "", 30*time.Second, true, nil)
	if err != nil {
		return err
	}
	path := filepath.Join(target, ".local", "skills", "herdr", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	text := out.Stdout
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return err
	}
	for _, parent := range []string{filepath.Join(target, ".agents", "skills"), filepath.Join(target, ".claude", "skills")} {
		_ = linkTool(filepath.Join(parent, "herdr"), "../../.local/skills/herdr")
	}
	return nil
}

func sourceFiles(root, sha string) ([]string, error) {
	out, err := proc.Run([]string{"git", "-C", root, "ls-tree", "-r", "-z", "--name-only", sha}, "", 60*time.Second, true, nil)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, name := range strings.Split(out.Stdout, "\x00") {
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

func toolPins(target string) (*ordjson.Object, error) {
	data, err := os.ReadFile(filepath.Join(target, "mise.toml"))
	if err != nil {
		return nil, fmt.Errorf("Cannot read bundled mise.toml: %s", err)
	}
	var parsed struct {
		Tools map[string]any `toml:"tools"`
	}
	if err := toml.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("Cannot read bundled mise.toml: %s", err)
	}
	pins := ordjson.NewObject()
	for k, v := range parsed.Tools {
		pins.Set(k, fmt.Sprint(v))
	}
	return pins, nil
}

func nativeArtifactRecord(inventory *ordjson.Object, target, name string) (*ordjson.Object, error) {
	var entry *ordjson.Object
	deps := asList(func() any {
		if inventory == nil {
			return nil
		}
		v, _ := inventory.Get("dependencies")
		return v
	}())
	for _, raw := range deps {
		item := asObject(raw)
		id, _ := item.Get("id")
		if asString(id) == name {
			entry = item
			break
		}
	}
	path := filepath.Join(target, ".local", "bin", name)
	if entry == nil || !isExecutable(path) {
		return nil, fmt.Errorf("Release is missing the staged native %s artifact", name)
	}
	hash, err := Sha256File(path)
	if err != nil {
		return nil, err
	}
	record := ordjson.NewObject()
	for _, k := range entry.Keys() {
		v, _ := entry.Get(k)
		record.Set(k, v)
	}
	record.Set("path", ".local/bin/"+name)
	record.Set("sha256", hash)
	record.Set("platform", nativePlatform())
	build := ordjson.NewObject()
	build.Set("cgo", false)
	build.Set("requires", []any{"go >= 1.25"})
	record.Set("build", build)
	runtimeReq := ordjson.NewObject()
	runtimeReq.Set("requires", []any{})
	record.Set("runtime", runtimeReq)
	return record, nil
}

func BuildManifest(s *store.Store, root, sha, target string) (*ordjson.Object, error) {
	names, err := sourceFiles(root, sha)
	if err != nil {
		return nil, err
	}
	files := ordjson.NewObject()
	for _, name := range names {
		id, idErr := ContentID(filepath.Join(target, name))
		if idErr != nil {
			return nil, idErr
		}
		files.Set(name, id)
	}
	tools := ordjson.NewObject()
	for _, name := range installTools {
		link := filepath.Join(target, ".local", "bin", name)
		info, statErr := os.Lstat(link)
		if statErr != nil || info.Mode()&os.ModeSymlink == 0 {
			return nil, fmt.Errorf("Release is missing the pinned tool link %s", link)
		}
		dest, readErr := os.Readlink(link)
		if readErr != nil {
			return nil, readErr
		}
		tools.Set(name, dest)
	}
	if remainder := filepath.Join(target, ".local", "bin", "remainder"); isSymlink(remainder) {
		if dest, readErr := os.Readlink(remainder); readErr == nil {
			tools.Set("remainder", dest)
		}
	}
	rawInventory, err := ordjson.ReadFile(filepath.Join(target, "docs", "dependency-inventory.json"))
	if err != nil {
		return nil, err
	}
	inventory := asObject(rawInventory)
	if err := ValidateDependencyInventory(inventory); err != nil {
		return nil, err
	}
	native := ordjson.NewObject()
	for _, name := range []string{"sumctl", "herdr-mesh"} {
		record, recErr := nativeArtifactRecord(inventory, target, name)
		if recErr != nil {
			return nil, recErr
		}
		native.Set(name, record)
	}
	pins, err := toolPins(target)
	if err != nil {
		return nil, err
	}
	offered := contract.BuildRelease()
	contracts := ordjson.NewObject()
	contracts.Set("herdr_cli", offered.Contracts.HerdrCLI)
	mcp := ordjson.NewObject()
	mcp.Set("server", offered.Contracts.MCP.Server)
	mcp.Set("version", offered.Contracts.MCP.Version)
	mcp.Set("tools", jsonNumber(offered.Contracts.MCP.Tools))
	contracts.Set("mcp", mcp)
	supports := ordjson.NewObject()
	var state []any
	for _, n := range offered.Supports.StateSchema {
		state = append(state, jsonNumber(n))
	}
	var brief []any
	for _, n := range offered.Supports.BriefSchema {
		brief = append(brief, jsonNumber(n))
	}
	supports.Set("state_schema", state)
	supports.Set("brief_schema", brief)
	treeOut, err := proc.Run([]string{"git", "-C", root, "rev-parse", sha + "^{tree}"}, "", 30*time.Second, true, nil)
	if err != nil {
		return nil, err
	}
	source := ordjson.NewObject()
	source.Set("sha", sha)
	source.Set("tree", strings.TrimSpace(treeOut.Stdout))
	source.Set("repository", root)
	host, _ := os.Hostname()
	statePath := filepath.Join(s.Home, "state.json")
	var instance any
	if raw, readErr := ordjson.ReadFile(statePath); readErr == nil {
		if obj := asObject(raw); obj != nil {
			instance, _ = obj.Get("instance")
		}
	}
	stagedBy := ordjson.NewObject()
	stagedBy.Set("machine", host)
	stagedBy.Set("installation", root)
	stagedBy.Set("instance", instance)
	depsTools := ordjson.NewObject()
	depsTools.Set("pins", pins)
	depsTools.Set("paths", tools)
	codegraph := ordjson.NewObject()
	codegraph.Set("package", "@colbymchenry/codegraph")
	codegraph.Set("version", "1.5.0")
	codegraph.Set("license", "MIT")
	if pins != nil {
		if pin, ok := pins.Get("npm:@colbymchenry/codegraph"); ok {
			codegraph.Set("pin", pin)
		}
	}
	codegraph.Set("path", ".local/bin/codegraph")
	dependencies := ordjson.NewObject()
	dependencies.Set("tools", depsTools)
	dependencies.Set("codegraph", codegraph)
	dependencies.Set("inventory", inventory)
	dependencies.Set("native", native)
	manifest := ordjson.NewObject()
	manifest.Set("schema", jsonNumber(Schema))
	manifest.Set("kind", "sum-release")
	manifest.Set("sum_version", offered.SumVersion)
	manifest.Set("source", source)
	manifest.Set("files", files)
	manifest.Set("dependencies", dependencies)
	manifest.Set("contracts", contracts)
	manifest.Set("supports", supports)
	manifest.Set("staged_at", store.Now())
	manifest.Set("staged_by", stagedBy)
	return manifest, nil
}

func jsonNumber(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}
