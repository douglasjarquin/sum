package updatecmd

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
)

func TestFirstUpdateRecoveryRecordedCheckoutEndpoints(t *testing.T) {
	for _, position := range []string{"before-alignment", "after-alignment", "dirty", "unrelated"} {
		t.Run(position, func(t *testing.T) {
			lab := newActivationLab(t)
			plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", lab.newSHA), workingHelper(""))
			afterSelect = func() error {
				if position != "before-alignment" {
					if alignment := syncInstallationCheckout(lab.root, lab.newSHA); effectField(alignment, "result") == "refused" {
						t.Fatalf("alignment: %s", dump(alignment))
					}
				}
				return errTestInterrupt
			}
			if _, err := Apply(lab.store, lab.ctx, lab.newSHA, true); !errors.Is(err, errTestInterrupt) {
				t.Fatalf("interrupt: %v", err)
			}
			afterSelect = nil
			generation := pendingGeneration(t, lab.root)
			if position == "dirty" {
				writeFile(t, filepath.Join(lab.root, "local-note"), "preserve\n")
			}
			if position == "unrelated" {
				git(t, lab.root, "merge", "--ff-only", stageNextRelease(t, lab))
			}
			head := git(t, lab.root, "rev-parse", "HEAD")
			journal := readActivationBytes(t, lab.root)
			view, err := independentRecover(t, lab, generation)
			if position == "dirty" || position == "unrelated" {
				want := "local changes"
				if position == "unrelated" {
					want = "checkout HEAD matches neither"
				}
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("recover = %v, want %s", err, want)
				}
				assertJournalUnchanged(t, lab.root, journal)
				if currentSHA(t, lab.root) != lab.newSHA || git(t, lab.root, "rev-parse", "HEAD") != head {
					t.Fatal("refused recovery mutated selection or checkout")
				}
				if position == "dirty" {
					body, _ := os.ReadFile(filepath.Join(lab.root, "local-note"))
					if string(body) != "preserve\n" {
						t.Fatal("dirty work changed")
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("recover: %v", err)
			}
			if restored := asObject(func() any { v, _ := view.Get("default"); return v }()); strField(restored, "sha") != lab.oldSHA {
				t.Fatalf("recovered result reports wrong checkout revision: %s", dump(view))
			}
			if hasCurrent(t, lab.root) || git(t, lab.root, "rev-parse", "HEAD") != lab.oldSHA || pendingGeneration(t, lab.root) != "" {
				t.Fatal("recovery did not restore checkout-backed known good")
			}
		})
	}
}

func TestFirstUpdatePendingInstructionCarriesClaim(t *testing.T) {
	lab := newActivationLab(t)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", lab.newSHA), workingHelper(""))
	afterPendingWrite = func() error { return errTestInterrupt }
	if _, err := Apply(lab.store, lab.ctx, lab.newSHA, true); !errors.Is(err, errTestInterrupt) {
		t.Fatal(err)
	}
	afterPendingWrite = nil
	if _, err := Apply(lab.store, lab.ctx, lab.newSHA, true); err == nil || !strings.Contains(err.Error(), "--claim "+strField(lab.ctx, "claim")) || !strings.Contains(err.Error(), filepath.Join(lab.root, ".local", "bin", "sumctl")) {
		t.Fatalf("pending instruction: %v", err)
	}
}

func TestApplyHandMovedCheckoutStillRefuses(t *testing.T) {
	lab := newActivationLab(t)
	afterPendingWrite = func() error { return errTestInterrupt }
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", lab.newSHA), workingHelper(""))
	if _, err := Apply(lab.store, lab.ctx, lab.newSHA, true); !errors.Is(err, errTestInterrupt) {
		t.Fatal(err)
	}
	afterPendingWrite = nil
	state := activationState(t, lab.root)
	state.Set("pending", nil)
	if err := writeActivationState(lab.store, lab.root, state); err != nil {
		t.Fatal(err)
	}
	git(t, lab.root, "merge", "--ff-only", lab.newSHA)
	if _, err := Apply(lab.store, lab.ctx, lab.newSHA, true); err == nil || !strings.Contains(err.Error(), "known-good checkout no longer matches") {
		t.Fatalf("hand moved apply: %v", err)
	}
	if hasCurrent(t, lab.root) {
		t.Fatal("hand moved apply selected a runtime")
	}
}

func TestFirstUpdateCommitFailurePrintsStagedRecovery(t *testing.T) {
	lab := newActivationLab(t)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", lab.newSHA), workingHelper(""))
	beforeActivationCommit = func() error { return errTestInterrupt }
	t.Cleanup(func() { beforeActivationCommit = nil })
	_, err := Apply(lab.store, lab.ctx, lab.newSHA, true)
	beforeActivationCommit = nil
	if err == nil || !strings.Contains(err.Error(), "activation record could not be committed") {
		t.Fatalf("commit failure: %v", err)
	}
	pending := asObject(func() any { v, _ := activationState(t, lab.root).Get("pending"); return v }())
	recovery := asObject(func() any { v, _ := pending.Get("recovery"); return v }())
	if !strings.Contains(err.Error(), "Run `"+argvJoin(recovery)+"`") || !strings.Contains(err.Error(), "--claim "+strField(lab.ctx, "claim")) {
		t.Fatalf("instruction: %v", err)
	}
	if git(t, lab.root, "rev-parse", "HEAD") != lab.newSHA {
		t.Fatal("did not fail after alignment")
	}
	if _, err := independentRecover(t, lab, pendingGeneration(t, lab.root)); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryStagesImmutablePreclaimHelperThroughInstallationEntrypoint(t *testing.T) {
	lab := newActivationLab(t)
	helper := buildPreclaimRecoveryHelper(t)
	body, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	plantNativeHelper(t, lab.root, string(body))
	prior := selectionDescriptor(DefaultRuntime(lab.root))
	rec, err := stageRecovery(lab.store, lab.ctx, lab.root, "0123456789abcdef0123456789abcdef", prior)
	if err != nil {
		t.Fatal(err)
	}
	if strField(rec, "helper") != filepath.Join(lab.root, "bin", "sumctl") || strField(rec, "route") != "installation-entrypoint" {
		t.Fatalf("route: %s", dump(rec))
	}
	raw, _ := rec.Get("argv")
	argv := raw.([]any)
	if argv[len(argv)-2] != "--claim" || argv[len(argv)-1] != strField(lab.ctx, "claim") {
		t.Fatalf("argv: %v", argv)
	}
	cmd := exec.Command(helper, "--home", lab.home, "update", "recover", "--generation", "0123456789abcdef0123456789abcdef")
	cmd.Env = proc.ScrubbedEnv()
	if output, err := cmd.CombinedOutput(); err == nil || (!strings.Contains(string(output), "unsupported state schema") && !strings.Contains(string(output), "inactive state generation")) {
		t.Fatalf("preclaim helper crossed state floor: %v %s", err, output)
	}
}

func TestRecoveryProbeFailureLeavesSelectionUnchanged(t *testing.T) {
	lab := newActivationLab(t)
	plantNativeHelper(t, lab.root, "#!/bin/sh\necho 'probe failed' >&2\nexit 19\n")
	_, err := stageRecovery(lab.store, lab.ctx, lab.root, "0123456789abcdef0123456789abcdef", selectionDescriptor(DefaultRuntime(lab.root)))
	if err == nil || !strings.Contains(err.Error(), "probe failed") {
		t.Fatalf("probe: %v", err)
	}
	if hasCurrent(t, lab.root) {
		t.Fatal("failed probe changed selection")
	}
}

func TestRecoveryArgvQuotesShellMetacharacters(t *testing.T) {
	rec := ordjson.NewObject()
	rec.Set("argv", []any{"/tmp/space dir/helper", "--claim", "a'b;$HOME", ""})
	cmd := exec.Command("sh", "-c", "set -- "+argvJoin(rec)+"; printf '%s\\n' \"$@\"")
	output, err := cmd.Output()
	if err != nil || string(output) != "/tmp/space dir/helper\n--claim\na'b;$HOME\n\n" {
		t.Fatalf("argv: %v %q", err, output)
	}
}

func buildPreclaimRecoveryHelper(t *testing.T) string {
	t.Helper()
	const sha = "8a4300c646cb6dcea8ca6f19f9199268b3adb6e3"
	repo, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	archive, err := exec.Command("git", "-C", strings.TrimSpace(string(repo)), "archive", "--format=tar", sha, "go").Output()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		path := filepath.Join(root, header.Name)
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(path, 0700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if header.Typeflag != tar.TypeReg {
			t.Fatalf("unexpected archive type: %s", header.Name)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, os.FileMode(header.Mode)); err != nil {
			t.Fatal(err)
		}
	}
	helper := filepath.Join(root, "sumctl")
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-trimpath", "-buildvcs=false", "-o", helper, "./cmd/sumctl")
	cmd.Dir = filepath.Join(root, "go")
	cmd.Env = append(proc.ScrubbedEnv(), "GOTOOLCHAIN=local", "CGO_ENABLED=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build preclaim helper: %v %s", err, output)
	}
	return helper
}
