package cli

import (
	"strings"
	"testing"
)

func TestSettingsShowReadsTheEvidenceBlock(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		contains []string
	}{
		{name: "absent block publishes by default", file: `{"schema": 1, "capacity": {"global": 2}}`, contains: []string{"auto_publish: true", "source: default"}},
		{name: "opted out", file: `{"schema": 1, "evidence": {"auto_publish": false}}`, contains: []string{"auto_publish: false", "source: settings"}},
		{name: "opted in explicitly", file: `{"schema": 1, "evidence": {"auto_publish": true}}`, contains: []string{"auto_publish: true", "source: settings"}},
		{name: "unknown key", file: `{"schema": 1, "evidence": {"auto_publish": true, "bogus": 1}}`, contains: []string{"source: invalid", "unknown evidence keys ['bogus']"}},
		{name: "non-bool auto_publish", file: `{"schema": 1, "evidence": {"auto_publish": "yes"}}`, contains: []string{"source: invalid", "evidence.auto_publish must be true or false"}},
		{name: "missing auto_publish", file: `{"schema": 1, "evidence": {}}`, contains: []string{"source: invalid", "evidence.auto_publish is required"}},
		{name: "block is not an object", file: `{"schema": 1, "evidence": true}`, contains: []string{"source: invalid", "evidence must be an object"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home := writeDesignatedHome(t)
			writeSettingsFile(t, home, tc.file)
			stdout, stderr, err := runSettings(t, home, "settings", "show")
			if err != nil {
				t.Fatalf("settings show: %v stderr=%s", err, stderr)
			}
			for _, want := range tc.contains {
				if !strings.Contains(stdout, want) {
					t.Fatalf("settings show =\n%s\nwant substring %q", stdout, want)
				}
			}
		})
	}
}

func TestSettingsSetWritesAndClearsTheEvidenceBlock(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	writeSettingsFile(t, home, `{"schema": 1, "capacity": {"global": 2, "per_repository": 1}}`)
	if _, err := runCLI(t, home, "init"); err != nil {
		t.Fatalf("coordinator init: %v", err)
	}

	if _, _, err := runSettings(t, home, "settings", "set", "--auto-publish-evidence", "false"); err != nil {
		t.Fatalf("set --auto-publish-evidence false: %v", err)
	}
	saved := readSettingsFile(t, home)
	if !strings.Contains(saved, `"auto_publish": false`) || !strings.Contains(saved, `"global": 2`) {
		t.Fatalf("settings.json =\n%s\nwant auto_publish false beside the existing capacity", saved)
	}

	if _, _, err := runSettings(t, home, "settings", "set", "--auto-publish-evidence=true"); err != nil {
		t.Fatalf("set --auto-publish-evidence=true: %v", err)
	}
	if saved = readSettingsFile(t, home); !strings.Contains(saved, `"auto_publish": true`) {
		t.Fatalf("settings.json =\n%s\nwant auto_publish true", saved)
	}

	if _, _, err := runSettings(t, home, "settings", "set", "--clear-evidence"); err != nil {
		t.Fatalf("set --clear-evidence: %v", err)
	}
	if saved = readSettingsFile(t, home); strings.Contains(saved, "auto_publish") {
		t.Fatalf("settings.json =\n%s\nwant no evidence block", saved)
	}
	if !strings.Contains(saved, `"global": 2`) {
		t.Fatalf("settings.json =\n%s\nwant the capacity block untouched", saved)
	}

	_, _, err := runSettings(t, home, "settings", "set", "--clear-evidence", "--auto-publish-evidence", "false")
	if err == nil || !strings.Contains(err.Error(), "--clear-evidence conflicts with --auto-publish-evidence") {
		t.Fatalf("err = %v, want the conflict refused", err)
	}

	_, _, err = runSettings(t, home, "settings", "set", "--auto-publish-evidence", "maybe")
	if err == nil || !strings.Contains(err.Error(), "takes true or false") {
		t.Fatalf("err = %v, want a value error", err)
	}
}
