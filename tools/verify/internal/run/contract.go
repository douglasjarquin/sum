package run

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/douglasjarquin/sum/tools/verify/internal/contract"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
)

const (
	maxVerificationFileBytes = 256 * 1024
	maxContractListItems     = 256
	maxContractItemChars     = 512
)

var (
	requiredHeadings = []string{"Setup", "Readiness", "Teardown", "Automated checks", "Scenarios", "Isolation", "Artifacts"}
	// The skill paths carry no trailing slash: a project may keep a skill behind a link, and Git matches a
	// trailing-slash pathspec against a directory only, never against a link entry.
	policyFilesDefault = []string{
		"VERIFY.md", "mise.toml", ".mise.toml", "mise-tasks/", ".agents/skills/verify", ".agents/skills/evidence",
		".agents/skills/create-verification", ".agents/skills/maintain-verification", "AGENTS.md", "Agents.md",
		"ARCHITECTURE.md", "docs/ARCHITECTURE.md", "CODEOWNERS", ".github/CODEOWNERS", ".github/workflows/",
	}
	heading = regexp.MustCompile(`(?m)^#{2,3}[` + pycompat.SpaceClass + `]+(.+?)[` + pycompat.SpaceClass + `]*$`)
)

// terms is the validated ```verify block of VERIFY.md.
type terms struct {
	sha256           string
	entrypoint       string
	featureMaps      string
	artifacts        string
	evidence         string
	taskOwner        string
	commands         []string
	freshnessInputs  []string
	freshnessOutputs []string
	timeout          int
	policyFiles      []string
}

func loadContract(root string) (*terms, error) {
	data, err := readBounded(root + "/" + contract.File)
	if err != nil {
		return nil, err
	}
	text, err := decodeUTF8(data)
	if err != nil {
		return nil, contract.Blockedf("%s is not valid UTF-8: %v", contract.File, err)
	}
	config, found, err := contract.Config(text)
	if !found {
		return nil, contract.Blockedf("%s has no ```verify configuration block.", contract.File)
	}
	if err != nil {
		return nil, contract.Blockedf("%s ```verify block is not valid TOML: %v", contract.File, err)
	}
	headings := map[string]bool{}
	for _, match := range heading.FindAllStringSubmatch(text, -1) {
		headings[pycompat.Lower(pycompat.Strip(match[1]))] = true
	}
	var missing []string
	for _, required := range requiredHeadings {
		if !headings[strings.ToLower(required)] {
			missing = append(missing, required)
		}
	}
	if len(missing) > 0 {
		return nil, contract.Blockedf("%s lacks required section(s): %s.", contract.File, strings.Join(missing, ", "))
	}

	t := &terms{sha256: tool.SHA256(data)}
	if t.entrypoint, _ = config["entrypoint"].(string); t.entrypoint != "mise run verify" {
		return nil, contract.Blockedf("%s entrypoint must be the literal `mise run verify`, found %s.", contract.File, pycompat.Repr(config["entrypoint"]))
	}
	for _, path := range []struct {
		key, kind string
		fallback  any
		into      *string
	}{
		{"feature_maps", "path", nil, &t.featureMaps},
		{"artifacts", "path", nil, &t.artifacts},
		{"evidence", "path", ".artifacts/evidence", &t.evidence},
		{"task_owner", "directory", ".", &t.taskOwner},
	} {
		value := lookup(config, path.key, path.fallback)
		if !safeRelativePath(value) {
			return nil, contract.Blockedf("%s `%s` must be a relative %s inside the repository, found %s.", contract.File, path.key, path.kind, pycompat.Repr(value))
		}
		*path.into = value.(string)
	}
	requires, ok := lookup(config, "requires", map[string]any{}).(map[string]any)
	if !ok {
		return nil, contract.Blockedf("%s `requires` must be a table.", contract.File)
	}
	freshness, ok := lookup(config, "freshness", map[string]any{}).(map[string]any)
	if !ok {
		return nil, contract.Blockedf("%s `freshness` must be a table.", contract.File)
	}
	if t.commands, err = boundedStringList(lookup(requires, "commands", []any{}), contract.File+" `requires.commands`", false); err != nil {
		return nil, err
	}
	if t.freshnessInputs, err = boundedStringList(lookup(freshness, "inputs", []any{}), contract.File+" `freshness.inputs`", true); err != nil {
		return nil, err
	}
	if t.freshnessOutputs, err = boundedStringList(lookup(freshness, "outputs", []any{}), contract.File+" `freshness.outputs`", true); err != nil {
		return nil, err
	}
	timeout, ok := lookup(config, "timeout_seconds", int64(3600)).(int64)
	if !ok || timeout <= 0 {
		return nil, contract.Blockedf("%s `timeout_seconds` must be a positive integer.", contract.File)
	}
	t.timeout = int(timeout)
	if t.policyFiles, err = policyFileSet(lookup(config, "policy_files", []any{})); err != nil {
		return nil, err
	}
	return t, nil
}

func lookup(table map[string]any, key string, fallback any) any {
	if value, ok := table[key]; ok {
		return value
	}
	return fallback
}

// policyFileSet is the default policy files plus the contract's additions. The defaults always count: a candidate's
// VERIFY.md may add paths but can never remove or shrink the set that governs it.
func policyFileSet(extra any) ([]string, error) {
	added, err := boundedStringList(extra, contract.File+" `policy_files`", true)
	if err != nil {
		return nil, err
	}
	return sortedSet(policyFilesDefault, added), nil
}

func sortedSet(lists ...[]string) []string {
	set := slices.Concat(lists...)
	slices.Sort(set)
	return slices.Compact(set)
}

// safeRelativePath accepts only a plain relative path: nothing absolute, no `..` component, no backslash,
// no control character, and none of the characters Git reads as pathspec magic or a glob.
func safeRelativePath(value any) bool {
	path, ok := value.(string)
	if !ok || path == "" || strings.HasPrefix(path, "/") || strings.ContainsAny(path, `\:*?[]`) {
		return false
	}
	for _, character := range path {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return !slices.Contains(strings.Split(path, "/"), "..")
}

func boundedStringList(value any, name string, paths bool) ([]string, error) {
	list, ok := value.([]any)
	if !ok {
		return nil, contract.Blockedf("%s must be a list of strings.", name)
	}
	items := make([]string, len(list))
	for i, item := range list {
		if items[i], ok = item.(string); !ok {
			return nil, contract.Blockedf("%s must be a list of strings.", name)
		}
	}
	if len(items) > maxContractListItems {
		return nil, contract.Blockedf("%s has more than %d items.", name, maxContractListItems)
	}
	for _, item := range items {
		if utf8.RuneCountInString(item) > maxContractItemChars {
			return nil, contract.Blockedf("%s contains an item longer than %d characters.", name, maxContractItemChars)
		}
		if paths && !safeRelativePath(item) {
			return nil, contract.Blockedf("%s must contain only relative paths inside the repository.", name)
		}
	}
	return items, nil
}

// readBounded reads a regular file no larger than the verification-file limit; a symlink is refused, not followed.
func readBounded(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, contract.Blockedf("verification file %s is missing: %v", path, err)
	}
	oversized := contract.Blockedf("verification file %s exceeds %d bytes or is not regular", path, maxVerificationFileBytes)
	if !info.Mode().IsRegular() || info.Size() > maxVerificationFileBytes {
		return nil, oversized
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, contract.Blockedf("verification file %s is unreadable: %v", path, err)
	}
	defer file.Close()
	// The limit is enforced on what is read too, so a file that grows after the size check stays bounded.
	data, err := io.ReadAll(io.LimitReader(file, maxVerificationFileBytes+1))
	if err != nil {
		return nil, contract.Blockedf("verification file %s is unreadable: %v", path, err)
	}
	if len(data) > maxVerificationFileBytes {
		return nil, oversized
	}
	return data, nil
}

func decodeUTF8(data []byte) (string, error) {
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			return "", fmt.Errorf("invalid byte 0x%02x at offset %d", data[i], i)
		}
		i += size
	}
	return string(data), nil
}

// containsSymlink reports whether any component of path below root is a symbolic link.
func containsSymlink(path, root string) bool {
	relative, ok := pycompat.Rel(path, root)
	if !ok || relative == "." {
		return false
	}
	current := root
	for _, part := range strings.Split(relative, "/") {
		current += "/" + part
		info, err := os.Lstat(current)
		if err != nil {
			return false
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}
