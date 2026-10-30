package contract

import (
	"encoding/json"
	"testing"
)

func TestBuildRelease_reportsNativeBinaryCapabilities(t *testing.T) {
	release := BuildRelease()
	encoded, err := json.MarshalIndent(release, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{
  "sum_version": "0.1.0",
  "contracts": {
    "hc_core": "0.1.14",
    "hc_generation": 1,
    "endpoint_schema": 2,
    "notification_protocol": 2,
    "herdr_cli": "0.9.5",
    "mcp": {
      "server": "herdr-mesh-sum",
      "version": "0.1.0",
      "tools": 10
    }
  },
  "supports": {
    "endpoint_schema": [
      2
    ],
    "notification_protocol": [
      2
    ],
    "observer_protocol": [
      1
    ],
    "state_schema": [
      2
    ],
    "brief_schema": [
      1
    ],
    "machine_identity": [
      1
    ],
    "wake_protocol": [
      1
    ]
  }
}`
	if string(encoded) != want {
		t.Fatalf("release contract JSON =\n%s\nwant\n%s", encoded, want)
	}
}
