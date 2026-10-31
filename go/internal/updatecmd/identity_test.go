package updatecmd

import (
	"path/filepath"
	"testing"
)

const preIdentityContractJSON = `{"sum_version":"0.1.0","contracts":{"herdr_cli":"0.9.5","mcp":{"server":"herdr-mesh-sum","version":"0.1.0","tools":10}},"supports":{"state_schema":[1],"brief_schema":[1]}}`

func TestNativeIdentityComesFromArtifactInsteadOfManifestOrSource(t *testing.T) {
	lab := newApplyLab(t, applyLabOpts{})
	dir := filepath.Join(lab.root, ".local/releases", lab.newSHA)

	plantNativeHelper(t, dir, "#!/bin/sh\nprintf '%s\n' '"+preIdentityContractJSON+"'\n")
	current := DefaultRuntime(lab.root)
	compat, err := Compatibility(lab.store, lab.root, dir, current)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := compat.Get("ok"); ok != false {
		t.Fatal("manifest claims authorized an incompatible artifact")
	}
}
