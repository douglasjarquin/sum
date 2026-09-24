package updatecmd

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type preIdentityCase struct {
	name string
	// arrange leaves a pre-identity target ready and returns the SHA serving now
	// and the SHA the operation would select.
	arrange func(t *testing.T, lab *applyLab) (serving, target string)
	run     func(lab *applyLab, allow PreIdentity) (*ordjson.Object, error)
}

func preIdentityCases() []preIdentityCase {
	servingNewRollingBackToOld := func(t *testing.T, lab *applyLab) (string, string) {
		selectReleaseAllowing(t, lab, lab.oldSHA)
		selectWorkingRelease(t, lab, lab.newSHA)
		return lab.newSHA, lab.oldSHA
	}
	return []preIdentityCase{
		{
			name:    "rollback",
			arrange: servingNewRollingBackToOld,
			run: func(lab *applyLab, allow PreIdentity) (*ordjson.Object, error) {
				return Rollback(lab.store, lab.ctx, "", allow)
			},
		},
		{
			name:    "rollback --to SHA",
			arrange: servingNewRollingBackToOld,
			run: func(lab *applyLab, allow PreIdentity) (*ordjson.Object, error) {
				return Rollback(lab.store, lab.ctx, lab.oldSHA, allow)
			},
		},
		{
			name: "rollback --to checkout",
			arrange: func(t *testing.T, lab *applyLab) (string, string) {
				selectReleaseAllowing(t, lab, lab.oldSHA)
				return lab.oldSHA, lab.oldSHA
			},
			run: func(lab *applyLab, allow PreIdentity) (*ordjson.Object, error) {
				return Rollback(lab.store, lab.ctx, "checkout", allow)
			},
		},
		{
			name:    "apply --ref",
			arrange: servingNewRollingBackToOld,
			run: func(lab *applyLab, allow PreIdentity) (*ordjson.Object, error) {
				return Apply(lab.store, lab.ctx, lab.oldSHA, true, allow)
			},
		},
	}
}

func TestPreIdentityTarget_refusedWithoutFlag(t *testing.T) {
	for _, tc := range preIdentityCases() {
		t.Run(tc.name, func(t *testing.T) {
			lab := newPreIdentityLab(t)
			serving, _ := tc.arrange(t, lab)
			before := DefaultRuntime(lab.root)
			knownGood := recordedKnownGood(t, lab.root)

			_, err := tc.run(lab, RefusePreIdentity)
			if err == nil {
				t.Fatal("selecting a pre-identity release over stable records succeeded without the flag")
			}
			for _, want := range []string{"predates the stable machine identity", "demoted to developer", "--allow-pre-machine-identity"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal = %v, want it to name %q", err, want)
				}
			}
			if !descriptorsEqual(selectionDescriptor(DefaultRuntime(lab.root)), selectionDescriptor(before)) {
				t.Fatalf("selection changed to %s, want %s", dump(DefaultRuntime(lab.root)), dump(before))
			}
			if got := currentSHA(t, lab.root); serving != "" && got != serving {
				t.Fatalf("serving %s, want unchanged %s", got, serving)
			}
			if got := recordedKnownGood(t, lab.root); got != knownGood {
				t.Fatalf("known-good = %s, want unchanged %s", got, knownGood)
			}
			last := lastUpdateLog(t, lab.root)
			if strField(last, "result") != "refused" || !strings.Contains(fmtBlocking(func() any { v, _ := last.Get("blocking"); return v }()), "predates the stable machine identity") {
				t.Fatalf("history does not record the refusal: %s", dump(last))
			}
		})
	}
}

func TestPreIdentityTarget_flagProceedsAndRecordsOverride(t *testing.T) {
	for _, tc := range preIdentityCases() {
		t.Run(tc.name, func(t *testing.T) {
			lab := newPreIdentityLab(t)
			_, target := tc.arrange(t, lab)

			view, err := tc.run(lab, AllowPreIdentity)
			if err != nil {
				t.Fatalf("override refused: %v", err)
			}
			if got := strField(DefaultRuntime(lab.root), "sha"); got != target {
				t.Fatalf("selected %s, want %s\n%s", got, target, dump(view))
			}
			assertOverride(t, asObject(func() any { v, _ := view.Get("machine_identity_override"); return v }()))
			generation := strField(view, "generation")
			var phases []string
			for _, raw := range readUpdateLog(lab.root, 100) {
				entry := asObject(raw)
				if entry == nil || strField(entry, "generation") != generation {
					continue
				}
				assertOverride(t, asObject(func() any { v, _ := entry.Get("machine_identity_override"); return v }()))
				phases = append(phases, strField(entry, "phase")+strField(entry, "result"))
			}
			if strings.Join(phases, ",") != "selecting,selected" {
				t.Fatalf("history entries with the override = %v, want selecting and selected", phases)
			}
		})
	}
}

func TestPreIdentityRecover_refusedWithoutFlagThenRecordsOverride(t *testing.T) {
	lab := newPreIdentityLab(t)
	selectReleaseAllowing(t, lab, lab.oldSHA)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", lab.newSHA), workingHelper(""))
	afterSelect = func() error { return errTestInterrupt }
	if _, err := Apply(lab.store, lab.ctx, lab.newSHA, true, RefusePreIdentity); !errors.Is(err, errTestInterrupt) {
		t.Fatalf("apply interrupt: %v", err)
	}
	afterSelect = nil
	generation := pendingGeneration(t, lab.root)
	if generation == "" {
		t.Fatal("pending activation was not recorded")
	}

	_, err := independentRecoverWith(t, lab, generation, RefusePreIdentity)
	if err == nil || !strings.Contains(err.Error(), "predates the stable machine identity") || !strings.Contains(err.Error(), "--allow-pre-machine-identity") {
		t.Fatalf("recover to a pre-identity release = %v, want the machine-identity refusal", err)
	}
	if got := currentSHA(t, lab.root); got != lab.newSHA {
		t.Fatalf("refused recovery selected %s, want the interrupted candidate %s", got, lab.newSHA)
	}
	if pendingGeneration(t, lab.root) != generation {
		t.Fatal("refused recovery dropped the pending generation")
	}

	view, err := independentRecoverWith(t, lab, generation, AllowPreIdentity)
	if err != nil {
		t.Fatalf("recover with the override: %v", err)
	}
	if got := currentSHA(t, lab.root); got != lab.oldSHA {
		t.Fatalf("recovered %s, want %s\n%s", got, lab.oldSHA, dump(view))
	}
	last := lastUpdateLog(t, lab.root)
	if strField(last, "action") != "recover" || strField(last, "generation") != generation {
		t.Fatalf("last history entry is not this recovery: %s", dump(last))
	}
	assertOverride(t, asObject(func() any { v, _ := last.Get("machine_identity_override"); return v }()))
}

func TestPreIdentity_compensationRestoresServingReleaseWithoutFlag(t *testing.T) {
	lab := newPreIdentityLab(t)
	selectReleaseAllowing(t, lab, lab.oldSHA)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", lab.newSHA), workingHelper(""))
	candidate := filepath.Join(lab.root, ".local", "releases", lab.newSHA)
	TestPostCheck = func(_ *store.Store, root string) *ordjson.Object {
		row := ordjson.NewObject()
		ok := strField(DefaultRuntime(root), "path") != candidate
		row.Set("ok", ok)
		row.Set("detail", map[bool]any{true: nil, false: "candidate fails its entrypoint check"}[ok])
		return row
	}
	t.Cleanup(func() { TestPostCheck = nil })

	_, err := Apply(lab.store, lab.ctx, lab.newSHA, true, RefusePreIdentity)
	if err == nil || !strings.Contains(err.Error(), "restored and verified") {
		t.Fatalf("failed apply = %v, want verified restoration of the serving release", err)
	}
	if got := currentSHA(t, lab.root); got != lab.oldSHA {
		t.Fatalf("compensation left %s selected, want the serving release %s", got, lab.oldSHA)
	}
	if pendingGeneration(t, lab.root) != "" {
		t.Fatal("compensation left a pending generation")
	}
	last := lastUpdateLog(t, lab.root)
	if strField(last, "action") != "recover" {
		t.Fatalf("last history entry = %s, want the compensation", dump(last))
	}
	assertOverride(t, asObject(func() any { v, _ := last.Get("machine_identity_override"); return v }()))
}

// A release that 4eb8591 stages writes no identity field into release.json; its
// tree alone decides.
func TestPreIdentity_targetOfferingTheIdentityIsUnaffected(t *testing.T) {
	lab := newRollbackLab(t)
	selectWorkingRelease(t, lab, lab.oldSHA)
	selectWorkingRelease(t, lab, lab.newSHA)
	supports := asObject(func() any {
		manifest, err := ordjson.ReadFile(filepath.Join(lab.root, ".local", "releases", lab.oldSHA, "release.json"))
		if err != nil {
			t.Fatal(err)
		}
		v, _ := asObject(manifest).Get("supports")
		return v
	}())
	if _, has := supports.Get("machine_identity"); has {
		t.Fatal("fixture manifest carries an identity field; it must look like one 4eb8591 staged")
	}

	view, err := Rollback(lab.store, lab.ctx, "", RefusePreIdentity)
	if err != nil {
		t.Fatalf("rollback to a release offering the stable identity: %v", err)
	}
	if got := currentSHA(t, lab.root); got != lab.oldSHA {
		t.Fatalf("selected %s, want %s", got, lab.oldSHA)
	}
	row := compatIdentity(t, view)
	if strField(row, "result") != "supported" || strField(row, "records") != "stable" {
		t.Fatalf("machine_identity = %s, want supported over stable records", dump(row))
	}
	if _, has := view.Get("machine_identity_override"); has {
		t.Fatalf("a supported target recorded an override\n%s", dump(view))
	}
}

func TestPreIdentity_installationWithoutStableRecordsIsUnaffected(t *testing.T) {
	lab := newPreIdentityLab(t)
	selectReleaseAllowing(t, lab, lab.oldSHA)
	selectWorkingRelease(t, lab, lab.newSHA)
	rewriteRecordsToHostname(t, lab)

	view, err := Rollback(lab.store, lab.ctx, "", RefusePreIdentity)
	if err != nil {
		t.Fatalf("rollback with hostname-only records: %v", err)
	}
	if got := currentSHA(t, lab.root); got != lab.oldSHA {
		t.Fatalf("selected %s, want %s", got, lab.oldSHA)
	}
	row := compatIdentity(t, view)
	if strField(row, "result") != "unused" || strField(row, "records") != "hostname" {
		t.Fatalf("machine_identity = %s, want unused over hostname records", dump(row))
	}
	if _, has := view.Get("machine_identity_override"); has {
		t.Fatalf("hostname-only records recorded an override\n%s", dump(view))
	}
}

func TestPreIdentity_staleTaskRecordIsEvidence(t *testing.T) {
	lab := newPreIdentityLab(t)
	selectReleaseAllowing(t, lab, lab.oldSHA)
	selectWorkingRelease(t, lab, lab.newSHA)
	rewriteRecordsToHostname(t, lab)
	id, err := machine.ID()
	if err != nil {
		t.Fatal(err)
	}
	task, err := lab.store.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	task.Set("machine", id)
	if err := lab.store.SaveTask(task); err != nil {
		t.Fatal(err)
	}

	_, err = Rollback(lab.store, lab.ctx, "", RefusePreIdentity)
	if err == nil || !strings.Contains(err.Error(), "task t-aaaaaaaaaaaa") {
		t.Fatalf("rollback over a task carrying the stable identity = %v, want a refusal naming that task", err)
	}
}

// A runtime from 9fb9256 stamped its own compiled support into every manifest
// it staged, including one for a tree that predates the identity.
func TestPreIdentity_manifestClaimingSupportDoesNotHideAPreIdentityTree(t *testing.T) {
	lab := newPreIdentityLab(t)
	selectReleaseAllowing(t, lab, lab.oldSHA)
	selectWorkingRelease(t, lab, lab.newSHA)
	path := filepath.Join(lab.root, ".local", "releases", lab.oldSHA, "release.json")
	raw, err := ordjson.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest := asObject(raw)
	supports := asObject(func() any { v, _ := manifest.Get("supports"); return v }())
	supports.Set("machine_identity", []any{json.Number("1")})
	if err := ordjson.WriteFile(path, manifest); err != nil {
		t.Fatal(err)
	}

	_, err = Rollback(lab.store, lab.ctx, "", RefusePreIdentity)
	if err == nil || !strings.Contains(err.Error(), "predates the stable machine identity") {
		t.Fatalf("rollback to a pre-identity tree whose manifest claims support = %v, want the refusal", err)
	}
	if got := currentSHA(t, lab.root); got != lab.newSHA {
		t.Fatalf("selection changed to %s", got)
	}
}

// The marker must classify this repository's own history: 1f2806d is the
// parent of #202, 4eb8591 is #202, and HEAD is everything since.
func TestPreIdentity_markerClassifiesThisRepositorysHistory(t *testing.T) {
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("this test needs the sum repository with its history: %v", err)
	}
	repo := strings.TrimSpace(string(top))
	for _, tc := range []struct {
		rev  string
		want bool
	}{
		{"1f2806db4669efdfab7be90c1e4efd4e21aac0e3", false},
		{"4eb8591bd9372f96a3322f35ad1be0e70f75987b", true},
		{"HEAD", true},
	} {
		names, err := exec.Command("git", "-C", repo, "ls-tree", "-r", "--name-only", tc.rev).Output()
		if err != nil {
			t.Fatalf("%s is not in this clone's history (fetch full history): %v", tc.rev, err)
		}
		files := ordjson.NewObject()
		for _, name := range strings.Split(strings.TrimSpace(string(names)), "\n") {
			files.Set(name, "sha256:unused")
		}
		manifest := ordjson.NewObject()
		manifest.Set("files", files)
		if got := releaseOffersIdentity(manifest); got != tc.want {
			t.Fatalf("release of %s offers the identity = %v, want %v", tc.rev, got, tc.want)
		}
		got, err := checkoutOffersIdentity(repo, tc.rev)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("checkout at %s offers the identity = %v, want %v", tc.rev, got, tc.want)
		}
	}
}

// The serving checkout predates the identity and is recorded as known-good
// out of band; history says why that was allowed.
func TestPreIdentity_knownGoodRecordedOverStableRecordsIsLogged(t *testing.T) {
	lab := newPreIdentityLab(t)
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", lab.newSHA), workingHelper(""))
	if _, err := Apply(lab.store, lab.ctx, lab.newSHA, true, RefusePreIdentity); err != nil {
		t.Fatalf("apply from a pre-identity checkout: %v", err)
	}
	var entry *ordjson.Object
	for _, raw := range readUpdateLog(lab.root, 100) {
		if row := asObject(raw); row != nil && strField(row, "action") == "known-good" {
			entry = row
		}
	}
	if entry == nil {
		t.Fatal("recording the pre-identity checkout as known-good left no history entry")
	}
	if known := asObject(func() any { v, _ := entry.Get("known_good"); return v }()); strField(known, "kind") != "checkout" || strField(known, "sha") != lab.oldSHA {
		t.Fatalf("known-good entry = %s, want the checkout at %s", dump(entry), lab.oldSHA)
	}
	assertOverride(t, asObject(func() any { v, _ := entry.Get("machine_identity_override"); return v }()))
}

func newPreIdentityLab(t *testing.T) *applyLab {
	t.Helper()
	return newRollbackLabWith(t, applyLabOpts{preIdentityOld: true})
}

// selectReleaseAllowing selects a release as an operator who already chose the
// override would, so a pre-identity release can serve before a test starts.
func selectReleaseAllowing(t *testing.T, lab *applyLab, sha string) {
	t.Helper()
	plantNativeHelper(t, filepath.Join(lab.root, ".local", "releases", sha), workingHelper(""))
	if _, err := Apply(lab.store, lab.ctx, sha, true, AllowPreIdentity); err != nil {
		t.Fatalf("setup apply %s: %v", sha, err)
	}
}

// rewriteRecordsToHostname leaves the coordinator's context.json and session
// file as a release before the stable identity wrote them.
func rewriteRecordsToHostname(t *testing.T, lab *applyLab) {
	t.Helper()
	hostname, err := machine.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	owner, err := lab.store.Owner()
	if err != nil {
		t.Fatal(err)
	}
	owner.Set("machine", hostname)
	if err := ordjson.WriteFile(filepath.Join(lab.home, "context.json"), owner); err != nil {
		t.Fatal(err)
	}
	registrations, err := lab.store.Registrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, reg := range registrations {
		if err := os.Remove(filepath.Join(lab.store.Sessions, strField(reg, "key")+".json")); err != nil {
			t.Fatal(err)
		}
		key := store.RegistrationKey(store.Endpoint{Machine: hostname, Session: strField(reg, "session"), Pane: strField(reg, "pane")})
		reg.Set("machine", hostname)
		reg.Set("key", key)
		if err := ordjson.WriteFile(filepath.Join(lab.store.Sessions, key+".json"), reg); err != nil {
			t.Fatal(err)
		}
	}
	if evidence, err := stableIdentityRecord(lab.store, nil); err != nil || evidence != "" {
		t.Fatalf("records still carry the stable identity (%q, %v)", evidence, err)
	}
}

func independentRecoverWith(t *testing.T, lab *applyLab, generation string, allow PreIdentity) (*ordjson.Object, error) {
	t.Helper()
	st, err := store.Open(lab.home)
	if err != nil {
		t.Fatal(err)
	}
	return Recover(st, lab.ctx, generation, allow)
}

func assertOverride(t *testing.T, row *ordjson.Object) {
	t.Helper()
	if row == nil || strField(row, "result") != "overridden" || strField(row, "flag") != "--allow-pre-machine-identity" || strField(row, "records") != "stable" || strField(row, "evidence") == "" {
		t.Fatalf("machine_identity_override = %s, want the overridden row with its flag and evidence", dump(row))
	}
}

func compatIdentity(t *testing.T, view *ordjson.Object) *ordjson.Object {
	t.Helper()
	compat := asObject(func() any { v, _ := view.Get("compatibility"); return v }())
	row := asObject(func() any {
		if compat == nil {
			return nil
		}
		v, _ := compat.Get("machine_identity")
		return v
	}())
	if row == nil {
		t.Fatalf("compatibility has no machine_identity row\n%s", dump(view))
	}
	return row
}

func lastUpdateLog(t *testing.T, root string) *ordjson.Object {
	t.Helper()
	rows := readUpdateLog(root, 1)
	if len(rows) == 0 {
		t.Fatal("update history is empty")
	}
	return asObject(rows[0])
}
