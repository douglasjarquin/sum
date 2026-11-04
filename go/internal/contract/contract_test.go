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
    "notification_protocol": 2
  },
  "supports": {
    "hc_template_schema": [
      0,
      2
    ],
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
      2,
      3
    ],
    "brief_schema": [
      1
    ],
    "machine_identity": [
      1
    ],
    "wake_protocol": [
      1
    ],
    "owner_schema": [
      2
    ]
  }
}`
	if string(encoded) != want {
		t.Fatalf("release contract JSON =\n%s\nwant\n%s", encoded, want)
	}
}

func TestHCCompatible(t *testing.T) {
	for _, tc := range []struct {
		version    string
		generation int
		want       bool
	}{
		{"0.1.14", 1, true}, {"0.1.17", 1, true}, {"0.1.9", 1, false}, {"0.1.10", 1, false}, {"0.10.0", 1, true}, {"0.1.13", 1, false}, {"0.1.17", 2, false}, {"", 1, false}, {"0.1.x", 1, false}, {"0.1.+14", 1, false}, {"0..14", 1, false}, {"18446744073709551616", 1, false},
	} {
		if got := HCCompatible(tc.version, tc.generation); got != tc.want {
			t.Errorf("%q generation %d: %v", tc.version, tc.generation, got)
		}
	}
}
