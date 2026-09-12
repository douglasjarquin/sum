package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/douglasjarquin/go-toon"
)

func decodeCLIMap(t *testing.T, raw string) map[string]any {
	t.Helper()
	raw = strings.TrimSpace(raw)
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err == nil {
		return value
	}
	if err := toon.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("decode CLI output: %v\n%s", err, raw)
	}
	return value
}

func decodeCLIError(t *testing.T, raw []byte) string {
	t.Helper()
	value := decodeCLIMap(t, string(raw))
	msg, _ := value["error"].(string)
	if msg == "" {
		t.Fatalf("missing error field in %s", raw)
	}
	return msg
}
