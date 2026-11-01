package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBareInitEnsuresTheLocalHCHolder(t *testing.T) {
	clearFallbackIdentity(t)
	runtimeRoot, st, f, _ := developerFallbackLab(t)
	log := filepath.Join(t.TempDir(), "hc.log")
	path := filepath.Join(runtimeRoot, ".local", "bin", "hc")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HCBOOT_LOG\"\nprintf '%s\\n' '{\"live\":0,\"holder\":{\"current\":true,\"pid\":4}}'\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HCBOOT_LOG", log)
	before := snapshotHome(t, st.Root)
	var out, errOut bytes.Buffer
	command := NewRoot(filepath.Join(runtimeRoot, "bin", "sumctl"), &out, &errOut)
	command.SetArgs([]string{"--home", st.Root, "--format", "json", "init"})
	if err := command.Execute(); err != nil {
		t.Fatalf("init failed: %v %s", err, errOut.String())
	}
	view := decodeObject(t, out.String())
	if view["role"] != "developer" || view["registered"] != false {
		t.Fatalf("bare init claimed a role: %v", view)
	}
	services, _ := view["services"].(map[string]any)
	if services["ok"] != true || services["action"] != "ready" {
		t.Fatalf("services = %#v", view["services"])
	}
	if len(f.Calls()) != 0 {
		t.Fatalf("ensure used the fixture core: %v", f.Calls())
	}
	if snapshotHome(t, st.Root) != before {
		t.Fatal("service ensure changed installation state")
	}
}
