package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/machine"
)

func TestStartRefusesPreparedTaskWithoutVerificationSnapshot(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	if _, err := runCLI(t, home, "init"); err != nil {
		t.Fatal(err)
	}
	host, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "prepared", "machine": "`+host+`", "repository": "owner/repo", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief": "do the thing"}`)
	_, _, err = runStartCLI(t, home, "start", "t-aaaaaaaaaaaa")
	if err == nil || !strings.Contains(err.Error(), "verification snapshot") {
		t.Fatalf("err = %v, want the missing snapshot refusal", err)
	}
}

func TestStartRefusesEmptyVerificationSnapshot(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	if _, err := runCLI(t, home, "init"); err != nil {
		t.Fatal(err)
	}
	host, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	writeTaskFixture(t, home, "t-aaaaaaaaaaab", `{"schema": 1, "id": "t-aaaaaaaaaaab", "status": "prepared", "machine": "`+host+`", "repository": "owner/repo", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief": "do the thing", "verification_policy": {}}`)
	_, _, err = runStartCLI(t, home, "start", "t-aaaaaaaaaaab")
	if err == nil || !strings.Contains(err.Error(), "verification snapshot") {
		t.Fatalf("err = %v, want the invalid snapshot refusal", err)
	}
}

func TestStartRefusesVerificationSnapshotForDifferentBase(t *testing.T) {
	home := writeDesignatedHome(t)
	herdrEnv(t, home)
	if _, err := runCLI(t, home, "init"); err != nil {
		t.Fatal(err)
	}
	host, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	writeTaskFixture(t, home, "t-aaaaaaaaaaac", `{"schema": 1, "id": "t-aaaaaaaaaaac", "status": "prepared", "machine": "`+host+`", "repository": "owner/repo", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief": "do the thing", "verification_policy": {"status":"not-yet-standardized","base_sha":"fedcba9876543210fedcba9876543210fedcba98","contract_path":"VERIFY.md","feature_maps":[],"feature_map_hashes":[],"required_checks":[],"scenario_ids":[],"requirements":{},"freshness":{},"policy_files":[],"evidence_required":[],"repository_path":"/repo","project_identity":{},"source_runtime":{},"delivery":{}}}`)
	_, _, err = runStartCLI(t, home, "start", "t-aaaaaaaaaaac")
	if err == nil || !strings.Contains(err.Error(), "verification snapshot") {
		t.Fatalf("err = %v, want the wrong-base snapshot refusal", err)
	}
}

func TestStartUnknownFlagsAreUsageErrorsBeforeStart(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		unknown bool
	}{
		{name: "unknown flag", args: []string{"start", "t-aaaaaaaaaaaa", "--unexpected"}, unknown: true},
		{name: "unknown flag before task", args: []string{"start", "--unexpected", "t-aaaaaaaaaaaa"}, unknown: true},
		{name: "unknown flag after --arg", args: []string{"start", "t-aaaaaaaaaaaa", "--arg", "dev", "--unexpected"}, unknown: true},
		{name: "extra positional", args: []string{"start", "t-aaaaaaaaaaaa", "extra"}},
		{name: "extra after dashdash", args: []string{"start", "t-aaaaaaaaaaaa", "--", "extra"}},
		{name: "extra after --arg", args: []string{"start", "t-aaaaaaaaaaaa", "--arg", "dev", "extra"}},
		{name: "missing --arg value", args: []string{"start", "t-aaaaaaaaaaaa", "--arg"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearHerdrEnv(t)
			home, before := startUsageLab(t)
			stdout, stderr, err := runStartCLI(t, home, tc.args...)
			if err == nil {
				t.Fatalf("expected usage failure, stdout=%s stderr=%s", stdout, stderr)
			}
			assertStartUsageError(t, err)
			if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
				t.Fatalf("err = %v, want unknown flag", err)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %s", stdout)
			}
			assertStartTaskUnchanged(t, home, before)
		})
	}
}

func TestStartCommands(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		herdr     bool
		usage     bool
		unknown   bool
		domainErr string
	}{
		{name: "valid task", args: []string{"start", "t-aaaaaaaaaaaa"}, domainErr: "Herdr pane"},
		{name: "valid task after dashdash", args: []string{"start", "--", "t-aaaaaaaaaaaa"}, domainErr: "Herdr pane"},
		{name: "valid --arg", args: []string{"start", "t-aaaaaaaaaaaa", "--arg", "dev", "--arg", "serve"}, domainErr: "Herdr pane"},
		{name: "valid --arg=", args: []string{"start", "t-aaaaaaaaaaaa", "--arg=dev"}, domainErr: "Herdr pane"},
		{name: "valid with herdr", args: []string{"start", "t-aaaaaaaaaaaa", "--arg", "dev"}, herdr: true, domainErr: "registered coordinator"},
		{name: "unknown flag", args: []string{"start", "t-aaaaaaaaaaaa", "--unexpected"}, usage: true, unknown: true},
		{name: "unknown flag before task", args: []string{"start", "--unexpected", "t-aaaaaaaaaaaa"}, usage: true, unknown: true},
		{name: "extra positional", args: []string{"start", "t-aaaaaaaaaaaa", "extra"}, usage: true},
		{name: "missing task", args: []string{"start"}, usage: true},
		{name: "missing --arg value", args: []string{"start", "t-aaaaaaaaaaaa", "--arg"}, usage: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, before := startUsageLab(t)
			if tc.herdr {
				herdrEnv(t, home)
			} else {
				clearHerdrEnv(t)
			}
			stdout, stderr, err := runStartCLI(t, home, tc.args...)
			switch {
			case tc.usage:
				assertStartUsageError(t, err)
				if tc.unknown && !strings.Contains(err.Error(), "unknown flag") {
					t.Fatalf("err = %v, want unknown flag", err)
				}
				if stdout != "" {
					t.Fatalf("usage failure wrote stdout: %s", stdout)
				}
				assertStartTaskUnchanged(t, home, before)
			default:
				if err == nil {
					t.Fatalf("expected domain error, stdout=%s stderr=%s", stdout, stderr)
				}
				if !strings.Contains(err.Error(), tc.domainErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.domainErr)
				}
				assertStartTaskUnchanged(t, home, before)
			}
		})
	}
}

func startUsageLab(t *testing.T) (home, before string) {
	t.Helper()
	home = writeDesignatedHome(t)
	writeTaskFixture(t, home, "t-aaaaaaaaaaaa", `{"schema": 1, "id": "t-aaaaaaaaaaaa", "status": "running", "repository": "owner/repo",
"questions": [], "evidence": [], "report": null, "notice": null, "attention": [], "brief": "do the thing", "base_sha": "0123456789abcdef0123456789abcdef01234567", "kind": "ship", "brief_path": "brief.md"}`)
	before = readNamedTask(t, home, "t-aaaaaaaaaaaa")
	return home, before
}

func runStartCLI(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root := NewRoot(filepath.Join(repoRoot(t), "bin", "sumctl"), &outBuf, &errBuf)
	root.SetArgs(append([]string{"--home", home}, args...))
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

func assertStartUsageError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected usage failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "registered coordinator") || strings.Contains(msg, "Herdr pane") {
		t.Fatalf("err = %v, want a usage failure", err)
	}
	usage := strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "accepts 1 arg") ||
		strings.Contains(msg, "flag needs an argument") ||
		strings.Contains(msg, "required flag") ||
		strings.Contains(msg, "unrecognized arguments") ||
		strings.Contains(msg, "invalid start")
	if !usage {
		t.Fatalf("err = %v, want a usage failure", err)
	}
}

func assertStartTaskUnchanged(t *testing.T, home, before string) {
	t.Helper()
	if got := readNamedTask(t, home, "t-aaaaaaaaaaaa"); got != before {
		t.Fatalf("task.json changed:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}
