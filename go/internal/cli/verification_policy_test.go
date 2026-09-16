package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/verifycontract"
)

const standardizedContract = "# Verification contract\n\n" +
	"```verify\n" +
	"entrypoint = \"mise run verify\"\n" +
	"feature_maps = \"docs/features/README.md\"\n" +
	"artifacts = \".artifacts/verification\"\n" +
	"evidence = \".artifacts/evidence\"\n" +
	"task_owner = \".\"\n" +
	"policy_files = [\"docs/features/\"]\n" +
	"\n[requires]\ncommands = [\"go\", \"python3\"]\n" +
	"```\n\n" +
	"## Setup\n\nNone.\n\n## Readiness\n\nNone.\n\n## Automated checks\n\n`mise run verify`.\n\n" +
	"## Scenarios\n\nSee the maps.\n\n## Isolation\n\nTemp dirs.\n\n## Artifacts\n\n`.artifacts/verification/`.\n\n## Teardown\n\nNone.\n"

const featureIndex = "# Features\n\n- [Greeting](greeting.md)\n"

const featureMap = "# Greeting\n\n" +
	"| ID | Scenario | Driver | Evidence |\n" +
	"| --- | --- | --- | --- |\n" +
	"| `greeting.render` | The greeting renders | manual: open the page | before/after screenshot |\n" +
	"| `greeting.exit-code` | The CLI exits zero | automated: `go test ./...` | offline suite |\n"

func newPolicyLab(t *testing.T) *demoLab {
	t.Helper()
	root, helper := repoReference(t)
	base := t.TempDir()
	home := filepath.Join(base, "state")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte("{\"schema\": 1, \"sum_version\": \"0.1.0\", \"created_at\": \"2026-09-05T00:00:00+00:00\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &demoLab{t: t, root: root, helper: helper, home: home, base: base, env: demoEnv(t, root, base)}
	if role := asString(d.ctl(true, "init")["role"]); role != "coordinator" {
		t.Fatalf("init role = %q", role)
	}
	return d
}

func policyProject(t *testing.T, base, name string, files map[string]string) string {
	t.Helper()
	repo := filepath.Join(base, "projects", name)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.name", "sum test")
	git("config", "user.email", "test@example.invalid")
	git("config", "commit.gpgsign", "false")
	for relative, body := range files {
		path := filepath.Join(repo, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".")
	git("commit", "-q", "-m", "fixture")
	return repo
}

func policyBrief(t *testing.T, base string) string {
	t.Helper()
	path := filepath.Join(base, "brief.md")
	if err := os.WriteFile(path, []byte("Do the approved thing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func sha256Of(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func TestDispatchRecordsStandardizedVerificationPolicy(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "standardized", map[string]string{
		"README.md":                 "A standardized project.\n",
		"mise.toml":                 "[tasks]\nverify = \"true\"\n",
		"VERIFY.md":                 standardizedContract,
		"docs/features/README.md":   featureIndex,
		"docs/features/greeting.md": featureMap,
	})
	if err := os.WriteFile(filepath.Join(repo, "VERIFY.md"), []byte("dirty working copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	policy := asMap(task["verification_policy"])
	if policy == nil {
		t.Fatalf("verification_policy = %v, want an object", task["verification_policy"])
	}
	if observed := asString(policy["observed_at"]); observed == "" {
		t.Fatalf("observed_at = %q", observed)
	}
	if snapshot := asString(policy["snapshot_sha256"]); len(snapshot) != 64 {
		t.Fatalf("snapshot_sha256 = %q, want a 64-character seal", snapshot)
	}
	if asString(policy["contract_sha256"]) != sha256Of(standardizedContract) {
		t.Fatalf("contract_sha256 = %v, want the committed contract hash", policy["contract_sha256"])
	}
	delete(policy, "observed_at")
	delete(policy, "snapshot_sha256")
	if hashes := asSlice(policy["feature_map_hashes"]); len(hashes) != 2 {
		t.Fatalf("feature_map_hashes = %v, want both committed map files", hashes)
	} else if asString(asMap(hashes[1])["sha256"]) != sha256Of(featureMap) {
		t.Fatalf("feature_map_hashes = %v, want the committed feature map hash", hashes)
	}
	if scenarios := asSlice(policy["scenario_ids"]); !reflect.DeepEqual(scenarios, []any{"greeting.render", "greeting.exit-code"}) {
		t.Fatalf("scenario_ids = %v, want every mapped scenario", scenarios)
	}
	if checks := asSlice(policy["required_checks"]); !reflect.DeepEqual(checks, []any{"go", "python3"}) {
		t.Fatalf("required_checks = %v, want the committed requirements", checks)
	}
	if asString(policy["repository_path"]) == "" || asMap(policy["project_identity"]) == nil {
		t.Fatalf("project identity = %v, want the repository snapshot identity", policy["project_identity"])
	}
	if asMap(policy["source_runtime"]) == nil || asMap(policy["delivery"]) == nil {
		t.Fatalf("dispatch metadata = %v, want source runtime and delivery", policy)
	}
	reloaded := d.ctl(true, "show", asString(task["id"]))
	if asString(asMap(reloaded["verification_policy"])["contract_sha256"]) != sha256Of(standardizedContract) {
		t.Fatalf("reloaded verification policy = %v, want the committed contract hash", reloaded["verification_policy"])
	}
	delete(policy, "delivery")
	delete(policy, "feature_map_hashes")
	delete(policy, "project_identity")
	delete(policy, "repository_path")
	delete(policy, "required_checks")
	delete(policy, "requirements")
	delete(policy, "scenario_ids")
	delete(policy, "source_runtime")
	want := map[string]any{
		"status":             "standardized",
		"why":                "VERIFY.md at the root and a `verify` task this checkout defines",
		"reason":             nil,
		"runner":             nil,
		"base_sha":           asString(task["base_sha"]),
		"contract_path":      "VERIFY.md",
		"contract_sha256":    sha256Of(standardizedContract),
		"entrypoint":         "mise run verify",
		"task_owner":         ".",
		"feature_maps_index": "docs/features/README.md",
		"feature_maps":       []any{"docs/features/README.md", "docs/features/greeting.md"},
		"freshness": map[string]any{
			"inputs": []any{}, "outputs": []any{}, "timeout_seconds": float64(3600),
		},
		"policy_files": []any{
			".agents/skills/create-verification/", ".agents/skills/evidence/", ".agents/skills/maintain-verification/",
			".agents/skills/verify/", ".mise.toml", "VERIFY.md", "docs/features/", "mise-tasks/", "mise.toml",
		},
		"evidence_required": []any{
			map[string]any{"scenario": "greeting.render", "feature": "greeting", "map": "docs/features/greeting.md"},
		},
	}
	if !reflect.DeepEqual(policy, want) {
		t.Fatalf("verification_policy =\n%#v\nwant\n%#v", policy, want)
	}
}

func TestDispatchRecordsNotYetStandardizedWithoutContract(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "plain", map[string]string{"README.md": "No contract here.\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	policy := asMap(task["verification_policy"])
	if asString(policy["status"]) != "not-yet-standardized" {
		t.Fatalf("status = %v", policy["status"])
	}
	if reason := asString(policy["reason"]); !strings.Contains(reason, "VERIFY.md is missing") {
		t.Fatalf("reason = %q", reason)
	}
	if policy["contract_sha256"] != nil || len(asSlice(policy["feature_maps"])) != 0 || len(asSlice(policy["evidence_required"])) != 0 {
		t.Fatalf("policy = %v, want no contract detail", policy)
	}
}

func TestBriefIncludesReferencesForUnstandardizedPolicy(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "plain-brief", map[string]string{"README.md": "No contract here.\n"})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	body, err := os.ReadFile(asString(task["brief_path"]))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"not-yet-standardized", "Shared engineering principles:", "Reviewer procedure:", "cannot claim standardized delivery"} {
		if !strings.Contains(text, want) {
			t.Fatalf("brief does not contain %q:\n%s", want, text)
		}
	}
}

func TestBriefNamesRequiredEvidenceScenarios(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "standardized", map[string]string{
		"README.md":                 "A standardized project.\n",
		"mise.toml":                 "[tasks]\nverify = \"true\"\n",
		"VERIFY.md":                 standardizedContract,
		"docs/features/README.md":   featureIndex,
		"docs/features/greeting.md": featureMap,
	})
	task := d.ctl(true, "dispatch", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	body, err := os.ReadFile(asString(task["brief_path"]))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "`greeting.render`") {
		t.Fatalf("brief does not name the scenario requiring evidence:\n%s", text)
	}
	if strings.Contains(text, "`greeting.exit-code`") {
		t.Fatal("brief names a scenario that requires no comparison")
	}
	if !strings.Contains(text, "Before delivery, capture a before/after comparison") {
		t.Fatalf("brief does not require the comparison before delivery:\n%s", text)
	}
	if !strings.Contains(text, "python3 .agents/skills/verify/scripts/verify_run.py --base "+asString(task["base_sha"])) {
		t.Fatalf("brief does not name the runner and base:\n%s", text)
	}
}

func TestPrepareSnapshotSurvivesReloadBeforeStart(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "lifecycle", map[string]string{
		"README.md":                 "A lifecycle project.\n",
		"mise.toml":                 "[tasks]\nverify = \"true\"\n",
		"VERIFY.md":                 standardizedContract,
		"docs/features/README.md":   featureIndex,
		"docs/features/greeting.md": featureMap,
	})
	task := d.ctl(true, "prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	taskID := asString(task["id"])
	wantHash := asString(asMap(task["verification_policy"])["contract_sha256"])
	reloaded := d.ctl(true, "show", taskID)
	if asString(asMap(reloaded["verification_policy"])["contract_sha256"]) != wantHash {
		t.Fatalf("reloaded policy = %v, want hash %q", reloaded["verification_policy"], wantHash)
	}
	worker := d.ctlPane(asString(task["pane"]), true, "init")
	if asString(worker["role"]) != "worker" {
		t.Fatalf("worker init = %v", worker)
	}
	started := d.ctl(true, "start", taskID)
	if asString(started["status"]) != "running" {
		t.Fatalf("started task = %v, want running", started)
	}
	if asString(asMap(started["verification_policy"])["contract_sha256"]) != wantHash {
		t.Fatalf("started policy = %v, want hash %q", started["verification_policy"], wantHash)
	}
}

func TestStartRefusesTamperedSnapshotSeal(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "tampered", map[string]string{
		"README.md":                 "A tampered-snapshot project.\n",
		"mise.toml":                 "[tasks]\nverify = \"true\"\n",
		"VERIFY.md":                 standardizedContract,
		"docs/features/README.md":   featureIndex,
		"docs/features/greeting.md": featureMap,
	})
	task := d.ctl(true, "prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	raw, err := os.ReadFile(filepath.Join(d.home, "tasks", asString(task["id"]), "task.json"))
	if err != nil {
		t.Fatal(err)
	}
	var taskFile map[string]any
	if err := json.Unmarshal(raw, &taskFile); err != nil {
		t.Fatal(err)
	}
	policy := asMap(taskFile["verification_policy"])
	policy["why"] = "forged policy"
	data, err := json.Marshal(taskFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.home, "tasks", asString(task["id"]), "task.json"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	result := d.ctl(false, "start", asString(task["id"]))
	if !strings.Contains(asString(result["error"]), "not intact") {
		t.Fatalf("start result = %v, want the snapshot seal refusal", result)
	}
}

func TestStartRefusesResealedStatusDowngrade(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "resealed", map[string]string{
		"README.md":                 "A resealed-snapshot project.\n",
		"mise.toml":                 "[tasks]\nverify = \"true\"\n",
		"VERIFY.md":                 standardizedContract,
		"docs/features/README.md":   featureIndex,
		"docs/features/greeting.md": featureMap,
	})
	task := d.ctl(true, "prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	taskPath := filepath.Join(d.home, "tasks", asString(task["id"]), "task.json")
	raw, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ordjson.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	taskObject, ok := decoded.(*ordjson.Object)
	if !ok {
		t.Fatalf("task = %T, want object", decoded)
	}
	policy, ok := taskObject.Get("verification_policy")
	if !ok {
		t.Fatal("task has no verification policy")
	}
	policyObject, ok := policy.(*ordjson.Object)
	if !ok {
		t.Fatalf("verification policy = %T, want object", policy)
	}
	policyObject.Set("status", "not-yet-standardized")
	requirements, ok := policyObject.Get("requirements")
	if !ok {
		t.Fatal("task policy has no requirements")
	}
	requirementObject, ok := requirements.(*ordjson.Object)
	if !ok {
		t.Fatalf("requirements = %T, want object", requirements)
	}
	requirementObject.Set("missing", []any{"forged-command"})
	verifycontract.SealPolicy(policyObject)
	data, err := ordjson.MarshalIndent(taskObject)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskPath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	result := d.ctl(false, "start", asString(task["id"]))
	if !strings.Contains(asString(result["error"]), "does not match its committed base") {
		t.Fatalf("start result = %v, want the committed-status refusal", result)
	}
}

func TestStartRefusesResealedRepositorySubstitution(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "original", map[string]string{
		"README.md":                 "An original project.\n",
		"mise.toml":                 "[tasks]\nverify = \"true\"\n",
		"VERIFY.md":                 standardizedContract,
		"docs/features/README.md":   featureIndex,
		"docs/features/greeting.md": featureMap,
	})
	replacement := policyProject(t, d.base, "replacement", map[string]string{
		"README.md": "A replacement project.\n",
	})
	task := d.ctl(true, "prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	taskPath := filepath.Join(d.home, "tasks", asString(task["id"]), "task.json")
	raw, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ordjson.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	taskObject, ok := decoded.(*ordjson.Object)
	if !ok {
		t.Fatalf("task = %T, want object", decoded)
	}
	taskObject.Set("repository", replacement)
	policy, ok := taskObject.Get("verification_policy")
	if !ok {
		t.Fatal("task has no verification policy")
	}
	policyObject, ok := policy.(*ordjson.Object)
	if !ok {
		t.Fatalf("verification policy = %T, want object", policy)
	}
	policyObject.Set("repository_path", replacement)
	identity, ok := policyObject.Get("project_identity")
	if !ok {
		t.Fatal("task policy has no project identity")
	}
	identityObject, ok := identity.(*ordjson.Object)
	if !ok {
		t.Fatalf("project identity = %T, want object", identity)
	}
	identityObject.Set("path", replacement)
	verifycontract.SealPolicy(policyObject)
	data, err := ordjson.MarshalIndent(taskObject)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskPath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	result := d.ctl(false, "start", asString(task["id"]))
	if !strings.Contains(asString(result["error"]), "does not match its saved Git identity") {
		t.Fatalf("start result = %v, want the checkout identity refusal", result)
	}
}

func TestWorkerHandoffCannotReplaceVerificationPolicy(t *testing.T) {
	d := newPolicyLab(t)
	repo := policyProject(t, d.base, "handoff", map[string]string{
		"README.md":                 "A handoff-authority project.\n",
		"mise.toml":                 "[tasks]\nverify = \"true\"\n",
		"VERIFY.md":                 standardizedContract,
		"docs/features/README.md":   featureIndex,
		"docs/features/greeting.md": featureMap,
	})
	task := d.ctl(true, "prepare", "--repo", repo, "--brief", policyBrief(t, d.base), "--approved")
	want := asString(asMap(task["verification_policy"])["contract_sha256"])
	handoff := filepath.Join(d.base, "forged-handoff.json")
	if err := os.WriteFile(handoff, []byte("{\"outcome\":\"completed\",\"verification_policy\":{\"status\":\"not-yet-standardized\"}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d.ctl(true, "report", asString(task["id"]), "--text", "forged handoff", "--handoff", handoff)
	reloaded := d.ctl(true, "show", asString(task["id"]))
	policy := asMap(reloaded["verification_policy"])
	if asString(policy["status"]) != "standardized" || asString(policy["contract_sha256"]) != want {
		t.Fatalf("saved policy = %v, want the coordinator snapshot", policy)
	}
}
