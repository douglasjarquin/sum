package machine

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const rawA = "0123456789abcdef0123456789abcdef"
const rawB = "fedcba9876543210fedcba9876543210"

func runAs(t *testing.T, raw, hostname string) {
	t.Helper()
	t.Cleanup(Pin(raw, hostname))
}

func initializedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte(`{"schema": 1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func local(t *testing.T, home string) Identity {
	t.Helper()
	identity, err := Local(home)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func TestID_survivesARenameAndDiffersBetweenHosts(t *testing.T) {
	runAs(t, rawA, "dev")
	before, err := ID()
	if err != nil {
		t.Fatal(err)
	}
	runAs(t, rawA, "host-development.example")
	after, _ := ID()
	runAs(t, rawB, "dev")
	other, _ := ID()
	if before != after {
		t.Fatalf("ID changed with the hostname: %s -> %s", before, after)
	}
	if other == before {
		t.Fatalf("two hosts with different machine IDs share identity %s", before)
	}
	if !regexp.MustCompile(`^m-[0-9a-f]{32}$`).MatchString(before) {
		t.Fatalf("ID = %q, want m- and 32 hex digits", before)
	}
	if strings.Contains(before, rawA) || strings.Contains(before, rawA[:16]) {
		t.Fatalf("ID %q exposes the raw machine ID", before)
	}
}

func TestID_readsTheOperatingSystemIdentifier(t *testing.T) {
	first, err := ID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := ID()
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !strings.HasPrefix(first, "m-") {
		t.Fatalf("unpinned ID = %q then %q, want one stable m- value", first, second)
	}
}

func TestLocal_readsTheCurrentHostnameAsThisHostWithoutWriting(t *testing.T) {
	home := initializedHome(t)
	runAs(t, rawA, "dev")
	host := local(t, home)
	if !host.Is(host.ID) || !host.Is("dev") || host.Is("elsewhere") || host.Is("") {
		t.Fatalf("Is: ID=%v dev=%v elsewhere=%v empty=%v", host.Is(host.ID), host.Is("dev"), host.Is("elsewhere"), host.Is(""))
	}
	if !host.Same("dev", host.ID) || host.Same("elsewhere", host.ID) || !host.Same("elsewhere", "elsewhere") {
		t.Fatal("Same must fold only this host's legacy name onto its ID")
	}
	if _, err := os.Stat(filepath.Join(home, File)); !os.IsNotExist(err) {
		t.Fatalf("Local wrote %s: %v", File, err)
	}
}

func TestRecord_keepsAnObservedNameAfterARename(t *testing.T) {
	home := initializedHome(t)
	runAs(t, rawA, "dev")
	if err := Record(home); err != nil {
		t.Fatal(err)
	}
	runAs(t, rawA, "renamed")
	host := local(t, home)
	if !host.Is("dev") || !host.Is("renamed") {
		t.Fatalf("after rename: dev=%v renamed=%v, want both this host", host.Is("dev"), host.Is("renamed"))
	}
	if host.Is("never-seen") {
		t.Fatal("a name this host never carried must not resolve to it")
	}
}

func TestRecord_writesNothingIntoAnUninitialisedHome(t *testing.T) {
	home := t.TempDir()
	runAs(t, rawA, "dev")
	if err := Record(home); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("Record wrote into an uninitialised home: %v", entries)
	}
}

func TestLocal_aNameRecordedByAnotherHostIsNeverThisHost(t *testing.T) {
	home := initializedHome(t)
	runAs(t, rawA, "dev")
	if err := Record(home); err != nil {
		t.Fatal(err)
	}
	original := local(t, home).ID

	// A clone with a regenerated machine ID that kept the hostname.
	runAs(t, rawB, "dev")
	if err := Record(home); err != nil {
		t.Fatal(err)
	}
	clone := local(t, home)
	if clone.Is("dev") || clone.Is(original) {
		t.Fatalf("clone adopted the original's identity: dev=%v original=%v", clone.Is("dev"), clone.Is(original))
	}
	if data, _ := os.ReadFile(filepath.Join(home, File)); strings.Count(string(data), `"dev"`) != 1 {
		t.Fatalf("clone recorded a name another host owns:\n%s", data)
	}
}

func TestLocal_refusesAMalformedAliasTable(t *testing.T) {
	home := initializedHome(t)
	if err := os.WriteFile(filepath.Join(home, File), []byte(`{"schema": 1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runAs(t, rawA, "dev")
	if _, err := Local(home); err == nil {
		t.Fatal("a malformed alias table must fail instead of silently dropping aliases")
	}
}
