package cli

import (
	"bytes"
	"context"
	"encoding/json"
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
}
