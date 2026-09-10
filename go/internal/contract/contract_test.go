package contract

import (
	"encoding/json"
	"testing"
)

func TestBuildRelease_matchesThePythonReferenceShape(t *testing.T) {
	release := BuildRelease()
	encoded, err := json.MarshalIndent(release, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{
  "sum_version": "0.1.0",
  "contracts": {
    "herdr_cli": "0.9.0",
    "mcp": {
      "server": "herdr-mesh-sum",
      "version": "0.1.0",
      "tools": 10
    }
  },
  "supports": {
    "state_schema": [
      1
    ],
    "brief_schema": [
      1
    ]
  }
}`
	if string(encoded) != want {
		t.Fatalf("release contract JSON =\n%s\nwant\n%s", encoded, want)
	}
}
