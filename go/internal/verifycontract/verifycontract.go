package verifycontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/douglasjarquin/sum/go/internal/environment"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	ContractFile = "VERIFY.md"
	RunnerPath   = ".agents/skills/verify/scripts/verify_run.py"
)

var (
	requiredHeadings = []string{"Setup", "Readiness", "Teardown", "Automated checks", "Scenarios", "Isolation", "Artifacts"}
	defaultPolicy    = []string{"VERIFY.md", "mise.toml", ".mise.toml", "mise-tasks/", ".agents/skills/verify/", ".agents/skills/evidence/", ".agents/skills/create-verification/", ".agents/skills/maintain-verification/"}

	fence          = regexp.MustCompile("(?sm)^```verify[ \t]*\n(.*?)^```[ \t]*$")
	heading        = regexp.MustCompile(`(?m)^#{2,3}\s+(.+?)\s*$`)
	link           = regexp.MustCompile(`\]\(([^)\s]+\.md)\)`)
	row            = regexp.MustCompile("^\\|\\s*`?([A-Za-z0-9][A-Za-z0-9._:/-]{0,79})`?\\s*\\|(.*)\\|\\s*$")
	evidenceWanted = regexp.MustCompile(`(?i)\b(screenshot|screencast|red/green|before/after)\b`)
	driverDeclared = regexp.MustCompile(`(?i)^(automated|manual)\b`)
)

const maxContractListItems = 256

// Scenario is a feature-map row whose Evidence cell names visual proof.
type Scenario struct {
	ID      string
	Feature string
	Map     string
}

type FileHash struct {
	Path   string
	SHA256 string
}

type Contract struct {
	SHA256           string
	Entrypoint       string
	TaskOwner        string
	FeatureMapsIndex string
	FeatureMaps      []string
	FeatureMapHashes []FileHash
	RequiredChecks   []string
	ScenarioIDs      []string
	FreshnessInputs  []string
	FreshnessOutputs []string
	TimeoutSeconds   int64
	PolicyFiles      []string
	EvidenceRequired []Scenario
}

func sha256Text(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func relativeInside(value string) bool {
	if value == "" || strings.ContainsAny(value, `\:*?[]`) || path.IsAbs(value) || filepath.IsAbs(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return false
		}
	}
	for _, part := range strings.Split(path.Clean(value), "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func pathContainsSymlink(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

func stringAt(config map[string]any, key, fallback string) (string, bool) {
	value, present := config[key]
	if !present {
		return fallback, true
	}
	text, ok := value.(string)
	return text, ok
}

// Read parses VERIFY.md and its feature maps the way .agents/skills/verify/scripts/verify_run.py
// does, running nothing. A missing file, an unreadable fence, or a value the runner would refuse
// comes back as an error naming the cause. The runner stays the authority at verification time.
func Read(worktree string) (*Contract, error) {
	raw, err := readBounded(filepath.Join(worktree, ContractFile))
	if err != nil {
		return nil, fmt.Errorf("%s is missing at the checkout root; this project is not yet standardized", ContractFile)
	}
	text := string(raw)
	block := fence.FindStringSubmatch(text)
	if block == nil {
		return nil, fmt.Errorf("%s has no ```verify configuration block", ContractFile)
	}
	var config map[string]any
	if err := toml.Unmarshal([]byte(block[1]), &config); err != nil {
		return nil, fmt.Errorf("%s ```verify block is not valid TOML: %s", ContractFile, err)
	}
	present := map[string]bool{}
	for _, found := range heading.FindAllStringSubmatch(text, -1) {
		present[strings.ToLower(strings.TrimSpace(found[1]))] = true
	}
	var missing []string
	for _, want := range requiredHeadings {
		if !present[strings.ToLower(want)] {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%s lacks required section(s): %s", ContractFile, strings.Join(missing, ", "))
	}
	entrypoint, _ := config["entrypoint"].(string)
	if entrypoint != "mise run verify" {
		return nil, fmt.Errorf("%s entrypoint must be the literal `mise run verify`, found %q", ContractFile, config["entrypoint"])
	}
	maps, ok := config["feature_maps"].(string)
	if !ok || !relativeInside(maps) {
		return nil, fmt.Errorf("%s `feature_maps` must be a relative path inside the repository, found %v", ContractFile, config["feature_maps"])
	}
	if artifacts, ok := config["artifacts"].(string); !ok || !relativeInside(artifacts) {
		return nil, fmt.Errorf("%s `artifacts` must be a relative path inside the repository, found %v", ContractFile, config["artifacts"])
	}
	evidence, ok := stringAt(config, "evidence", ".artifacts/evidence")
	if !ok || !relativeInside(evidence) {
		return nil, fmt.Errorf("%s `evidence` must be a relative path inside the repository, found %v", ContractFile, config["evidence"])
	}
	owner, ok := stringAt(config, "task_owner", ".")
	if !ok || !relativeInside(owner) {
		return nil, fmt.Errorf("%s `task_owner` must be a relative directory inside the repository, found %v", ContractFile, config["task_owner"])
	}
	requires, ok := config["requires"].(map[string]any)
	if config["requires"] != nil && !ok {
		return nil, fmt.Errorf("%s `requires` must be a table", ContractFile)
	}
	if !ok {
		requires = map[string]any{}
	}
	requiredChecks, err := stringList(requires["commands"], "requires.commands")
	if err != nil {
		return nil, err
	}
	freshness, ok := config["freshness"].(map[string]any)
	if config["freshness"] != nil && !ok {
		return nil, fmt.Errorf("%s `freshness` must be a table", ContractFile)
	}
	if !ok {
		freshness = map[string]any{}
	}
	freshnessInputs, err := relativeStringList(freshness["inputs"], "freshness.inputs")
	if err != nil {
		return nil, err
	}
	freshnessOutputs, err := relativeStringList(freshness["outputs"], "freshness.outputs")
	if err != nil {
		return nil, err
	}
	timeoutSeconds := int64(3600)
	if rawTimeout, present := config["timeout_seconds"]; present {
		timeoutSeconds, ok = rawTimeout.(int64)
		if !ok || timeoutSeconds <= 0 {
			return nil, fmt.Errorf("%s `timeout_seconds` must be a positive integer", ContractFile)
		}
	}
	policyFiles, err := policyFileSet(config["policy_files"])
	if err != nil {
		return nil, err
	}
	paths, hashes, scenarioIDs, scenarios, err := readFeatureMaps(worktree, maps)
	if err != nil {
		return nil, err
	}
	ownerPath := filepath.Join(worktree, filepath.FromSlash(owner))
	if pathContainsSymlink(worktree, ownerPath) {
		return nil, fmt.Errorf("%s `task_owner` traverses a symlink", ContractFile)
	}
	ownerInfo, err := os.Stat(ownerPath)
	if err != nil || !ownerInfo.IsDir() {
		return nil, fmt.Errorf("%s `task_owner` must name an existing directory, found %q", ContractFile, owner)
	}
	return &Contract{SHA256: sha256Text(text), Entrypoint: entrypoint, TaskOwner: owner, FeatureMapsIndex: maps,
		FeatureMaps: paths, FeatureMapHashes: hashes, RequiredChecks: requiredChecks, ScenarioIDs: scenarioIDs,
		FreshnessInputs: freshnessInputs, FreshnessOutputs: freshnessOutputs, TimeoutSeconds: timeoutSeconds,
		PolicyFiles: policyFiles, EvidenceRequired: scenarios}, nil
}

func stringList(value any, name string) ([]string, error) {
	if value == nil {
		return []string{}, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a list of strings", name)
	}
	if len(list) > maxContractListItems {
		return nil, fmt.Errorf("%s has more than %d items", name, maxContractListItems)
	}
	result := make([]string, 0, len(list))
	for _, item := range list {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be a list of strings", name)
		}
		if len([]rune(text)) > 512 {
			return nil, fmt.Errorf("%s contains an item longer than 512 characters", name)
		}
		result = append(result, text)
	}
	return result, nil
}

func relativeStringList(value any, name string) ([]string, error) {
	values, err := stringList(value, name)
	if err != nil {
		return nil, err
	}
	for _, value := range values {
		if !relativeInside(value) {
			return nil, fmt.Errorf("%s must contain only relative paths inside the repository", name)
		}
	}
	return values, nil
}

func ValidateCommittedPolicy(repo string, expected *ordjson.Object) error {
	base := stringField(expected, "base_sha")
	root, cleanup, err := MaterializeCommit(repo, base)
	if err != nil {
		if stringField(expected, "status") == "not-yet-standardized" && unavailableSnapshotError(err) {
			return nil
		}
		return err
	}
	defer cleanup()
	contract, readErr := Read(root)
	if readErr != nil {
		if stringField(expected, "status") == "standardized" {
			return fmt.Errorf("committed policy is no longer standardized: %w", readErr)
		}
		reason := stringField(expected, "reason")
		if reason != "" && !strings.Contains(reason, readErr.Error()) {
			return fmt.Errorf("committed policy reason does not match the base contract")
		}
		return nil
	}
	actualStatus := stringField(environment.VerificationContractStatusAtBase(root), "status")
	expectedStatus := stringField(expected, "status")
	requirements, _ := field(expected, "requirements").(*ordjson.Object)
	if !jsonEqual(field(requirements, "missing"), strings2any(missingCommands(contract.RequiredChecks))) {
		return fmt.Errorf("unavailable verification requirements differ from the dispatch snapshot")
	}
	if actualStatus != expectedStatus && !(actualStatus == "standardized" && expectedStatus == "not-yet-standardized" && hasMissingRequirements(expected)) {
		return fmt.Errorf("verification status differs from the dispatch snapshot")
	}
	if expectedHash := stringField(expected, "contract_sha256"); expectedHash != "" && expectedHash != contract.SHA256 {
		return fmt.Errorf("contract hash differs from the dispatch snapshot")
	}
	for key, actual := range map[string]string{"entrypoint": contract.Entrypoint, "task_owner": contract.TaskOwner, "feature_maps_index": contract.FeatureMapsIndex} {
		if expectedValue := stringField(expected, key); expectedValue != "" && expectedValue != actual {
			return fmt.Errorf("%s differs from the dispatch snapshot", key)
		}
	}
	freshness := ordjson.NewObject()
	freshness.Set("inputs", strings2any(contract.FreshnessInputs))
	freshness.Set("outputs", strings2any(contract.FreshnessOutputs))
	freshness.Set("timeout_seconds", json.Number(fmt.Sprint(contract.TimeoutSeconds)))
	if !jsonEqual(field(expected, "feature_maps"), strings2any(contract.FeatureMaps)) ||
		!jsonEqual(field(expected, "feature_map_hashes"), fileHashes2any(contract.FeatureMapHashes)) ||
		!jsonEqual(field(expected, "required_checks"), strings2any(contract.RequiredChecks)) ||
		!jsonEqual(field(expected, "scenario_ids"), strings2any(contract.ScenarioIDs)) ||
		!jsonEqual(field(expected, "freshness"), freshness) ||
		!jsonEqual(field(expected, "policy_files"), strings2any(contract.PolicyFiles)) ||
		!jsonEqual(field(expected, "evidence_required"), evidence2any(contract.EvidenceRequired)) {
		return fmt.Errorf("committed verification policy differs from the dispatch snapshot")
	}
	return nil
}

func unavailableSnapshotError(err error) bool {
	text := err.Error()
	return strings.Contains(text, "committed path") || strings.Contains(text, "contract snapshot references more than") || strings.Contains(text, "contract snapshot exceeds")
}

func SealPolicy(policy *ordjson.Object) {
	policy.Set("snapshot_sha256", policyDigest(policy))
}

func ValidatePolicySeal(policy *ordjson.Object) error {
	want := stringField(policy, "snapshot_sha256")
	if want == "" || want != policyDigest(policy) {
		return fmt.Errorf("snapshot seal does not match its coordinator-owned contents")
	}
	return nil
}

func policyDigest(policy *ordjson.Object) string {
	contents := ordjson.NewObject()
	for _, key := range policy.Keys() {
		if key == "snapshot_sha256" {
			continue
		}
		value, _ := policy.Get(key)
		contents.Set(key, value)
	}
	encoded, _ := ordjson.MarshalSortedCompact(contents)
	return sha256Text(string(encoded))
}

func field(value *ordjson.Object, key string) any {
	if value == nil {
		return nil
	}
	field, _ := value.Get(key)
	return field
}

func stringField(value *ordjson.Object, key string) string {
	text, _ := field(value, key).(string)
	return text
}

func jsonEqual(left, right any) bool {
	leftJSON, leftErr := ordjson.MarshalSortedCompact(left)
	rightJSON, rightErr := ordjson.MarshalSortedCompact(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func hasMissingRequirements(policy *ordjson.Object) bool {
	requirements, _ := field(policy, "requirements").(*ordjson.Object)
	missing, _ := field(requirements, "missing").([]any)
	return len(missing) > 0
}

// policyFileSet adds the contract's declared paths to the defaults; a candidate may widen the set that governs it, never shrink it.
func policyFileSet(declared any) ([]string, error) {
	set := map[string]bool{}
	for _, value := range defaultPolicy {
		set[value] = true
	}
	if declared != nil {
		list, ok := declared.([]any)
		if !ok {
			return nil, fmt.Errorf("%s `policy_files` must be a list of relative paths to add to the defaults", ContractFile)
		}
		if len(list) > maxContractListItems {
			return nil, fmt.Errorf("%s `policy_files` has more than %d items", ContractFile, maxContractListItems)
		}
		for _, item := range list {
			value, ok := item.(string)
			if !ok || !relativeInside(value) {
				return nil, fmt.Errorf("%s `policy_files` must be a list of relative paths to add to the defaults", ContractFile)
			}
			if len([]rune(value)) > 512 {
				return nil, fmt.Errorf("%s `policy_files` contains an item longer than 512 characters", ContractFile)
			}
			set[value] = true
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out, nil
}

func readFeatureMaps(worktree, indexRelative string) ([]string, []FileHash, []string, []Scenario, error) {
	root, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		root = worktree
	}
	index := filepath.Join(root, filepath.FromSlash(indexRelative))
	if pathContainsSymlink(root, index) {
		return nil, nil, nil, nil, fmt.Errorf("Feature-map index %s traverses a symlink", indexRelative)
	}
	body, err := readBounded(index)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("Feature-map index %s is missing", indexRelative)
	}
	paths := []string{indexRelative}
	hashes := []FileHash{{Path: indexRelative, SHA256: sha256Text(string(body))}}
	snapshotBytes := len(body)
	var scenarioIDs []string
	var scenarios []Scenario
	seen := map[string]bool{}
	for _, found := range link.FindAllStringSubmatch(string(body), -1) {
		target := found[1]
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
			continue
		}
		if !relativeInside(target) {
			return nil, nil, nil, nil, fmt.Errorf("Feature map link %s in %s is not a safe repository path", target, indexRelative)
		}
		rawTarget := filepath.Join(filepath.Dir(index), filepath.FromSlash(target))
		if pathContainsSymlink(root, rawTarget) {
			return nil, nil, nil, nil, fmt.Errorf("Feature map link %s in %s traverses a symlink", target, indexRelative)
		}
		resolved, err := filepath.EvalSymlinks(rawTarget)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("Feature map %s linked from %s is missing", target, indexRelative)
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil || strings.HasPrefix(relative, "..") {
			return nil, nil, nil, nil, fmt.Errorf("Feature map link %s in %s leaves the repository", target, indexRelative)
		}
		relative = filepath.ToSlash(relative)
		mapBody, err := readBounded(resolved)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("Feature map %s linked from %s is missing", relative, indexRelative)
		}
		if len(paths)+1 > commitSnapshotMaxFiles {
			return nil, nil, nil, nil, fmt.Errorf("verification contract snapshot references more than %d files", commitSnapshotMaxFiles)
		}
		if snapshotBytes+len(mapBody) > commitSnapshotMaxSize {
			return nil, nil, nil, nil, fmt.Errorf("verification contract snapshot exceeds %d bytes", commitSnapshotMaxSize)
		}
		paths = append(paths, relative)
		snapshotBytes += len(mapBody)
		hashes = append(hashes, FileHash{Path: relative, SHA256: sha256Text(string(mapBody))})
		ids, rows, err := mapScenarios(relative, string(mapBody), seen)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		scenarioIDs = append(scenarioIDs, ids...)
		scenarios = append(scenarios, rows...)
	}
	return paths, hashes, scenarioIDs, scenarios, nil
}

func mapScenarios(relative, body string, seen map[string]bool) ([]string, []Scenario, error) {
	driverColumn := -1
	var scenarioIDs []string
	var scenarios []Scenario
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(strings.TrimLeft(line, " \t"), "|") {
			driverColumn = -1
			continue
		}
		header := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if driverColumn < 0 {
			for i, cell := range header {
				if strings.ToLower(strings.TrimSpace(cell)) == "driver" {
					driverColumn = i - 1
					break
				}
			}
			if driverColumn >= 0 {
				continue
			}
		}
		cells := row.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if cells == nil || driverColumn < 0 {
			continue
		}
		columns := strings.Split(cells[2], "|")
		for i := range columns {
			columns[i] = strings.TrimSpace(columns[i])
		}
		if driverColumn >= len(columns) || strings.Trim(columns[driverColumn], "-:") == "" {
			continue
		}
		if !driverDeclared.MatchString(columns[driverColumn]) {
			return nil, nil, fmt.Errorf("Scenario %s in %s has driver %q; it must start with `automated` or `manual`", cells[1], relative, columns[driverColumn])
		}
		if seen[cells[1]] {
			return nil, nil, fmt.Errorf("Scenario id %s is defined twice across the feature maps", cells[1])
		}
		seen[cells[1]] = true
		scenarioIDs = append(scenarioIDs, cells[1])
		evidence := ""
		if driverColumn+1 < len(columns) {
			evidence = columns[driverColumn+1]
		}
		if evidenceWanted.MatchString(evidence) {
			scenarios = append(scenarios, Scenario{ID: cells[1], Feature: strings.TrimSuffix(path.Base(relative), ".md"), Map: relative})
		}
	}
	return scenarioIDs, scenarios, nil
}

// PolicyAtDispatch is the task's `verification_policy`: the contract as it stands at
// baseSHA, for the worker's candidate to be compared against later. status comes from
// environment.VerificationContractStatus, which is what decides whether mise resolves a
// `verify` task this checkout owns. Nothing is run and no error escapes.
func PolicyAtDispatch(worktree, baseSHA string, status *ordjson.Object) *ordjson.Object {
	state, why, runner := "not-yet-standardized", "no VERIFY.md at the checkout root; the project keeps its current verification path", any(nil)
	var reason any
	if status != nil {
		if value, ok := status.Get("status"); ok {
			state, _ = value.(string)
		}
		if value, ok := status.Get("why"); ok {
			why, _ = value.(string)
		}
		runner, _ = status.Get("runner")
		if value, ok := status.Get("error"); ok {
			reason, _ = value.(string)
		}
	}
	var contract *Contract
	var err error
	if worktree == "" {
		err = fmt.Errorf("verification contract snapshot is unavailable")
	} else {
		contract, err = Read(worktree)
	}
	if err != nil {
		reason = err.Error()
		if state == "standardized" {
			state = "not-yet-standardized"
			why = why + "; " + err.Error()
		}
		contract = &Contract{PolicyFiles: append([]string{}, defaultPolicy...), TimeoutSeconds: 3600}
		sort.Strings(contract.PolicyFiles)
	}
	policy := ordjson.NewObject()
	policy.Set("status", state)
	policy.Set("why", why)
	policy.Set("reason", reason)
	policy.Set("runner", runner)
	policy.Set("base_sha", baseSHA)
	policy.Set("observed_at", store.Now())
	policy.Set("contract_path", ContractFile)
	policy.Set("contract_sha256", optional(contract.SHA256))
	policy.Set("entrypoint", optional(contract.Entrypoint))
	policy.Set("task_owner", optional(contract.TaskOwner))
	policy.Set("feature_maps_index", optional(contract.FeatureMapsIndex))
	policy.Set("feature_maps", strings2any(contract.FeatureMaps))
	policy.Set("feature_map_hashes", fileHashes2any(contract.FeatureMapHashes))
	policy.Set("required_checks", strings2any(contract.RequiredChecks))
	policy.Set("scenario_ids", strings2any(contract.ScenarioIDs))
	freshness := ordjson.NewObject()
	freshness.Set("inputs", strings2any(contract.FreshnessInputs))
	freshness.Set("outputs", strings2any(contract.FreshnessOutputs))
	freshness.Set("timeout_seconds", json.Number(fmt.Sprint(contract.TimeoutSeconds)))
	policy.Set("freshness", freshness)
	missing := missingCommands(contract.RequiredChecks)
	requirements := ordjson.NewObject()
	requirements.Set("commands", strings2any(contract.RequiredChecks))
	requirements.Set("missing", strings2any(missing))
	policy.Set("requirements", requirements)
	policy.Set("policy_files", strings2any(contract.PolicyFiles))
	policy.Set("evidence_required", evidence2any(contract.EvidenceRequired))
	if len(missing) > 0 {
		state = "not-yet-standardized"
		policy.Set("status", state)
		message := fmt.Sprintf("required command(s) are unavailable: %s", strings.Join(missing, ", "))
		policy.Set("reason", message)
		policy.Set("why", why+"; "+message)
	}
	return policy
}

func PolicyAtDispatchUnavailable(baseSHA, reason string) *ordjson.Object {
	policy := PolicyAtDispatch("", baseSHA, nil)
	policy.Set("status", "not-yet-standardized")
	policy.Set("why", "verification contract snapshot unavailable at dispatch")
	policy.Set("reason", reason)
	policy.Set("snapshot_error", reason)
	return policy
}

func missingCommands(commands []string) []string {
	missing := make([]string, 0)
	for _, command := range commands {
		if _, err := exec.LookPath(command); err != nil {
			missing = append(missing, command)
		}
	}
	return missing
}

func optional(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func strings2any(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func fileHashes2any(values []FileHash) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		item := ordjson.NewObject()
		item.Set("path", value.Path)
		item.Set("sha256", value.SHA256)
		out = append(out, item)
	}
	return out
}

func evidence2any(values []Scenario) []any {
	out := make([]any, 0, len(values))
	for _, scenario := range values {
		item := ordjson.NewObject()
		item.Set("scenario", scenario.ID)
		item.Set("feature", scenario.Feature)
		item.Set("map", scenario.Map)
		out = append(out, item)
	}
	return out
}
