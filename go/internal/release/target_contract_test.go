package release

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/contract"
)

func targetHelper(t *testing.T, offered contract.Release) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, ".local", "bin", "sumctl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(offered)
	if err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\ncat <<'SUM_CONTRACT'\n" + string(raw) + "\nSUM_CONTRACT\n"
	if err = os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	return root
}
func TestTargetContractRequiresNativeBinaryCapabilities(t *testing.T) {
	offered := contract.BuildRelease()
	root := targetHelper(t, offered)
	got, err := ReadTargetContract(context.Background(), root)
	if err != nil || got.Contracts.HCCore != "0.1.14" {
		t.Fatalf("%#v %v", got, err)
	}
	for _, field := range []string{"state", "endpoint", "notifications", "observer", "core"} {
		t.Run(field, func(t *testing.T) {
			v := contract.BuildRelease()
			switch field {
			case "state":
				v.Supports.StateSchema = []int{1}
			case "endpoint":
				v.Supports.EndpointSchema = nil
			case "notifications":
				v.Supports.NotificationProtocol = nil
			case "observer":
				v.Supports.ObserverProtocol = nil
			case "core":
				v.Contracts.HCCore = "0.1.13"
			}
			root := targetHelper(t, v)
			if _, err := ReadTargetContract(context.Background(), root); err == nil || !strings.Contains(err.Error(), "HC") {
				t.Fatalf("incapable target accepted: %v", err)
			}
		})
	}
}
