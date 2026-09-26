package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestReleaseContract_printsRuntimeContract(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	root := NewRoot("", &stdout, &stderr)
	root.SetArgs([]string{"--home", home, "release-contract"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("release-contract failed: %v (stderr=%s)", err, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("not JSON: %v (%s)", err, stdout.String())
	}
	if payload["sum_version"] != "0.1.0" {
		t.Fatalf("sum_version = %v", payload["sum_version"])
	}
	// The wake protocol (#240a) is reported here so an update or rollback can read the target's capability from
	// its own helper, exactly like machine_identity.
	supports, _ := payload["supports"].(map[string]any)
	if got := fmt.Sprint(supports["wake_protocol"]); got != "[1]" || fmt.Sprint(supports["machine_identity"]) != "[1]" {
		t.Fatalf("supports = %v, want wake_protocol [1] beside machine_identity [1]", supports)
	}
}
