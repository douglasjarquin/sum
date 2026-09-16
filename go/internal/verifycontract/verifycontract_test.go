package verifycontract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

const contractBody = "# Verification contract\n\n" +
	"```verify\n" +
	"entrypoint = \"mise run verify\"\n" +
	"feature_maps = \"docs/index.md\"\n" +
	"artifacts = \".artifacts/verification\"\n" +
	"```\n\n" +
	"## Setup\n\nNone.\n\n## Readiness\n\nNone.\n\n## Automated checks\n\n`mise run verify`.\n\n" +
	"## Scenarios\n\nSee the maps.\n\n## Isolation\n\nTemp dirs.\n\n## Artifacts\n\n`.artifacts/verification/`.\n\n## Teardown\n\nNone.\n"

func checkout(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for relative, body := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func standardizedStatus() *ordjson.Object {
	status := ordjson.NewObject()
	status.Set("status", "standardized")
	status.Set("why", "VERIFY.md at the root and a `verify` task this checkout defines")
	status.Set("runner", nil)
	return status
}

func TestReadCollectsMapsAndEvidenceRequirements(t *testing.T) {
	root := checkout(t, map[string]string{
		"VERIFY.md":     contractBody,
		"docs/index.md": "- [Alpha](features/alpha.md)\n- [Beta](features/beta.md)\n- [Upstream](https://example.invalid/x.md)\n",
		"docs/features/alpha.md": "| ID | Scenario | Driver | Evidence |\n| --- | --- | --- | --- |\n" +
			"| `alpha.paint` | Repaints | manual | before/after screencast |\n" +
			"| `alpha.parse` | Parses | automated: `go test` | offline suite |\n",
		"docs/features/beta.md": "| ID | Scenario | Driver | Evidence |\n| --- | --- | --- | --- |\n" +
			"| `beta.diff` | Diffs | manual | red/green pair |\n",
	})
	contract, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	wantMaps := []string{"docs/index.md", "docs/features/alpha.md", "docs/features/beta.md"}
	if strings.Join(contract.FeatureMaps, ",") != strings.Join(wantMaps, ",") {
		t.Fatalf("FeatureMaps = %v, want %v", contract.FeatureMaps, wantMaps)
	}
	if len(contract.EvidenceRequired) != 2 {
		t.Fatalf("EvidenceRequired = %v, want two rows", contract.EvidenceRequired)
	}
	if got := contract.EvidenceRequired[0]; got != (Scenario{ID: "alpha.paint", Feature: "alpha", Map: "docs/features/alpha.md"}) {
		t.Fatalf("EvidenceRequired[0] = %+v", got)
	}
	if got := contract.EvidenceRequired[1]; got != (Scenario{ID: "beta.diff", Feature: "beta", Map: "docs/features/beta.md"}) {
		t.Fatalf("EvidenceRequired[1] = %+v", got)
	}
	if contract.TaskOwner != "." || contract.Entrypoint != "mise run verify" {
		t.Fatalf("contract = %+v", contract)
	}
}

func TestReadRecordsCommittedRequirementsAndMapHashes(t *testing.T) {
	body := strings.Replace(contractBody, "artifacts = \".artifacts/verification\"\n", "artifacts = \".artifacts/verification\"\n[requires]\ncommands = [\"go\", \"python3\"]\n", 1)
	root := checkout(t, map[string]string{
		"VERIFY.md":     body,
		"docs/index.md": "- [Alpha](features/alpha.md)\n",
		"docs/features/alpha.md": "| ID | Scenario | Driver | Evidence |\n| --- | --- | --- | --- |\n" +
			"| `alpha.paint` | Repaints | manual | before/after screenshot |\n",
	})
	contract, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(contract.RequiredChecks, ",") != "go,python3" {
		t.Fatalf("RequiredChecks = %v, want the declared commands", contract.RequiredChecks)
	}
	if strings.Join(contract.ScenarioIDs, ",") != "alpha.paint" {
		t.Fatalf("ScenarioIDs = %v, want every mapped scenario", contract.ScenarioIDs)
	}
	if len(contract.FeatureMapHashes) != 2 || contract.FeatureMapHashes[0].SHA256 == "" || contract.FeatureMapHashes[1].SHA256 == "" {
		t.Fatalf("FeatureMapHashes = %v, want hashes for the index and linked map", contract.FeatureMapHashes)
	}
}

func TestReadRefusesMalformedContracts(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{name: "absent", files: map[string]string{}, want: "VERIFY.md is missing"},
		{name: "no fence", files: map[string]string{"VERIFY.md": "# Contract\n\nNo block.\n"}, want: "no ```verify configuration block"},
		{
			name:  "bad toml",
			files: map[string]string{"VERIFY.md": "```verify\nentrypoint = \n```\n"},
			want:  "not valid TOML",
		},
		{
			name:  "missing headings",
			files: map[string]string{"VERIFY.md": "```verify\nentrypoint = \"mise run verify\"\nfeature_maps = \"docs/index.md\"\nartifacts = \".a\"\n```\n"},
			want:  "lacks required section(s)",
		},
		{
			name:  "foreign entrypoint",
			files: map[string]string{"VERIFY.md": strings.Replace(contractBody, "mise run verify", "make check", 1)},
			want:  "entrypoint must be the literal",
		},
		{
			name:  "escaping feature maps",
			files: map[string]string{"VERIFY.md": strings.Replace(contractBody, "docs/index.md", "../elsewhere/index.md", 1)},
			want:  "`feature_maps` must be a relative path",
		},
		{
			name:  "backslash escaping feature maps",
			files: map[string]string{"VERIFY.md": strings.Replace(contractBody, "feature_maps = \"docs/index.md\"", "feature_maps = '..\\elsewhere/index.md'", 1)},
			want:  "`feature_maps` must be a relative path",
		},
		{
			name:  "wildcard feature maps",
			files: map[string]string{"VERIFY.md": strings.Replace(contractBody, "feature_maps = \"docs/index.md\"", "feature_maps = \"docs/*.md\"", 1)},
			want:  "`feature_maps` must be a relative path",
		},
		{
			name:  "embedded traversal feature maps",
			files: map[string]string{"VERIFY.md": strings.Replace(contractBody, "feature_maps = \"docs/index.md\"", "feature_maps = \"safe/../other.md\"", 1)},
			want:  "`feature_maps` must be a relative path",
		},
		{
			name:  "unsafe linked map",
			files: map[string]string{"VERIFY.md": contractBody, "docs/index.md": "- [Unsafe](safe/../outside.md)\n"},
			want:  "is not a safe repository path",
		},
		{
			name:  "embedded task owner traversal",
			files: map[string]string{"VERIFY.md": strings.Replace(contractBody, "artifacts = \".artifacts/verification\"", "artifacts = \".artifacts/verification\"\ntask_owner = \"safe/../owner\"", 1)},
			want:  "`task_owner` must be a relative directory",
		},
		{
			name:  "missing index",
			files: map[string]string{"VERIFY.md": contractBody},
			want:  "Feature-map index docs/index.md is missing",
		},
		{
			name:  "missing map",
			files: map[string]string{"VERIFY.md": contractBody, "docs/index.md": "- [Gone](features/gone.md)\n"},
			want:  "linked from docs/index.md is missing",
		},
		{
			name: "bad driver",
			files: map[string]string{"VERIFY.md": contractBody, "docs/index.md": "- [Alpha](features/alpha.md)\n",
				"docs/features/alpha.md": "| ID | Scenario | Driver | Evidence |\n| --- | --- | --- | --- |\n| `alpha.x` | X | somehow | screenshot |\n"},
			want: "it must start with `automated` or `manual`",
		},
		{
			name: "duplicate id",
			files: map[string]string{"VERIFY.md": contractBody, "docs/index.md": "- [Alpha](features/alpha.md)\n- [Beta](features/beta.md)\n",
				"docs/features/alpha.md": "| ID | Scenario | Driver | Evidence |\n| --- | --- | --- | --- |\n| `shared.id` | X | manual | screenshot |\n",
				"docs/features/beta.md":  "| ID | Scenario | Driver | Evidence |\n| --- | --- | --- | --- |\n| `shared.id` | Y | manual | screenshot |\n"},
			want: "defined twice across the feature maps",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Read(checkout(t, tc.files))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestReadRefusesSymlinkedFeatureMapPaths(t *testing.T) {
	tests := []struct {
		name       string
		contract   string
		files      map[string]string
		link, dest string
	}{
		{
			name:     "index directory",
			contract: strings.Replace(contractBody, "docs/index.md", "docs/linked/index.md", 1),
			files: map[string]string{
				"docs/real/index.md": "- [Map](map.md)\n",
				"docs/real/map.md":   "| ID | Scenario | Driver | Evidence |\n| --- | --- | --- | --- |\n| `map.x` | X | manual | - |\n",
			},
			link: "docs/linked", dest: "real",
		},
		{
			name:     "linked map",
			contract: contractBody,
			files: map[string]string{
				"docs/index.md": "- [Map](linked.md)\n",
				"docs/real.md":  "| ID | Scenario | Driver | Evidence |\n| --- | --- | --- | --- |\n| `map.x` | X | manual | - |\n",
			},
			link: "docs/linked.md", dest: "real.md",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"VERIFY.md": tc.contract}
			for path, body := range tc.files {
				files[path] = body
			}
			root := checkout(t, files)
			if err := os.Symlink(tc.dest, filepath.Join(root, filepath.FromSlash(tc.link))); err != nil {
				t.Fatal(err)
			}
			_, err := Read(root)
			if err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("err = %v, want a symlink refusal", err)
			}
		})
	}
}

func TestPolicyAtDispatchDowngradesAMalformedContract(t *testing.T) {
	root := checkout(t, map[string]string{"VERIFY.md": "# Contract\n\nNo block.\n"})
	policy := PolicyAtDispatch(root, "abc123", standardizedStatus())
	status, _ := policy.Get("status")
	if status != "not-yet-standardized" {
		t.Fatalf("status = %v, want not-yet-standardized", status)
	}
	reason, _ := policy.Get("reason")
	if text, _ := reason.(string); !strings.Contains(text, "no ```verify configuration block") {
		t.Fatalf("reason = %v", reason)
	}
	why, _ := policy.Get("why")
	if text, _ := why.(string); !strings.Contains(text, "no ```verify configuration block") {
		t.Fatalf("why = %v", why)
	}
	if sha, _ := policy.Get("contract_sha256"); sha != nil {
		t.Fatalf("contract_sha256 = %v, want nil", sha)
	}
	files, _ := policy.Get("policy_files")
	if list, _ := files.([]any); len(list) != len(defaultPolicy) {
		t.Fatalf("policy_files = %v, want the defaults", files)
	}
}

func TestPolicyAtDispatchKeepsStatusForAnUnstandardizedCheckout(t *testing.T) {
	status := ordjson.NewObject()
	status.Set("status", "not-yet-standardized")
	status.Set("why", "VERIFY.md exists but mise resolves no `verify` task owned by this checkout")
	status.Set("runner", ".agents/skills/verify/scripts/verify_run.py")
	root := checkout(t, map[string]string{
		"VERIFY.md":              contractBody,
		"docs/index.md":          "- [Alpha](features/alpha.md)\n",
		"docs/features/alpha.md": "| ID | Scenario | Driver | Evidence |\n| --- | --- | --- | --- |\n| `alpha.x` | X | manual | screenshot |\n",
	})
	policy := PolicyAtDispatch(root, "abc123", status)
	if state, _ := policy.Get("status"); state != "not-yet-standardized" {
		t.Fatalf("status = %v", state)
	}
	if reason, _ := policy.Get("reason"); reason != nil {
		t.Fatalf("reason = %v, want nil for a contract that parsed", reason)
	}
	if runner, _ := policy.Get("runner"); runner != ".agents/skills/verify/scripts/verify_run.py" {
		t.Fatalf("runner = %v", runner)
	}
	if sha, _ := policy.Get("contract_sha256"); sha == nil {
		t.Fatal("contract_sha256 = nil, want the hash of a contract that parsed")
	}
}

func TestPolicyAtDispatchBlocksUnavailableRequiredCommands(t *testing.T) {
	body := strings.Replace(contractBody, "artifacts = \".artifacts/verification\"\n", "artifacts = \".artifacts/verification\"\n[requires]\ncommands = [\"sum-command-does-not-exist\"]\n", 1)
	root := checkout(t, map[string]string{
		"VERIFY.md":     body,
		"docs/index.md": "- [Alpha](features/alpha.md)\n",
		"docs/features/alpha.md": "| ID | Scenario | Driver | Evidence |\n| --- | --- | --- | --- |\n" +
			"| `alpha.x` | X | automated | offline suite |\n",
	})
	policy := PolicyAtDispatch(root, "abc123", standardizedStatus())
	if status, _ := policy.Get("status"); status != "not-yet-standardized" {
		t.Fatalf("status = %v, want not-yet-standardized", status)
	}
	reason, _ := policy.Get("reason")
	reasonText, _ := reason.(string)
	if !strings.Contains(reasonText, "sum-command-does-not-exist") {
		t.Fatalf("reason = %v, want the missing command", reason)
	}
	requirements, _ := policy.Get("requirements")
	requirementObject, ok := requirements.(*ordjson.Object)
	if !ok {
		t.Fatalf("requirements = %v, want a structured availability record", requirements)
	}
	missing, _ := requirementObject.Get("missing")
	missingCommands, _ := missing.([]any)
	if len(missingCommands) != 1 || missingCommands[0] != "sum-command-does-not-exist" {
		t.Fatalf("requirements.missing = %v, want the unavailable command", missing)
	}
}
