package verifycontract

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMatchesThePythonRunnerOnThisRepository(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	out, runErr := exec.Command("python3", filepath.Join(root, ".agents/skills/verify/scripts/verify_run.py"), "--root", root, "--check", "--json").Output()
	if len(out) == 0 {
		t.Skipf("runner --check produced no record: %v", runErr)
	}
	var record struct {
		Contract struct {
			SHA256     string `json:"sha256"`
			Entrypoint string `json:"entrypoint"`
			TaskOwner  string `json:"task_owner"`
		} `json:"contract"`
		FeatureMaps []struct {
			Path string `json:"path"`
		} `json:"feature_maps"`
		Scenarios []struct {
			ID               string `json:"id"`
			Feature          string `json:"feature"`
			Map              string `json:"map"`
			RequiresEvidence bool   `json:"requires_evidence"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal(out, &record); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if record.Contract.SHA256 == "" {
		t.Skipf("runner blocked before parsing the contract: %v", runErr)
	}
	got, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA256 != record.Contract.SHA256 || got.Entrypoint != record.Contract.Entrypoint || got.TaskOwner != record.Contract.TaskOwner {
		t.Fatalf("contract = %+v, runner = %+v", got, record.Contract)
	}
	if len(got.FeatureMaps) != len(record.FeatureMaps) {
		t.Fatalf("FeatureMaps = %v, runner = %v", got.FeatureMaps, record.FeatureMaps)
	}
	for i, want := range record.FeatureMaps {
		if got.FeatureMaps[i] != want.Path {
			t.Fatalf("FeatureMaps[%d] = %q, runner = %q", i, got.FeatureMaps[i], want.Path)
		}
	}
	var wantEvidence []Scenario
	for _, s := range record.Scenarios {
		if s.RequiresEvidence {
			wantEvidence = append(wantEvidence, Scenario{ID: s.ID, Feature: s.Feature, Map: s.Map})
		}
	}
	if len(got.EvidenceRequired) != len(wantEvidence) {
		t.Fatalf("EvidenceRequired = %v, runner = %v", got.EvidenceRequired, wantEvidence)
	}
	for i, want := range wantEvidence {
		if got.EvidenceRequired[i] != want {
			t.Fatalf("EvidenceRequired[%d] = %+v, runner = %+v", i, got.EvidenceRequired[i], want)
		}
	}
}
