package updatecmd

import (
	"fmt"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/machine"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// PreIdentity says whether a selection may move to a release that predates the
// stable machine identity while this installation's records already carry it.
type PreIdentity bool

const (
	RefusePreIdentity PreIdentity = false
	AllowPreIdentity  PreIdentity = true
)

const PreIdentityFlag = "--allow-pre-machine-identity"

// stableIdentityRecord names the first record whose machine value is a stable
// identity, or "" when every record still carries a hostname. These are the
// values a release that predates the stable identity compares to the raw
// hostname: the coordinator's context.json and session file, which init
// rewrites, and each task's machine, which every task command checks.
// .sum/machine.json is not the evidence because backups exclude it, so a
// restored home would carry stable records without it.
func stableIdentityRecord(s *store.Store, tasks []*ordjson.Object) (string, error) {
	owner, err := s.Owner()
	if err != nil {
		return "", err
	}
	if owner != nil {
		if v, _ := owner.Get("machine"); machine.IsStable(v) {
			return "context.json", nil
		}
	}
	registrations, err := s.Registrations()
	if err != nil {
		return "", err
	}
	for _, reg := range registrations {
		if v, _ := reg.Get("machine"); machine.IsStable(v) {
			return fmt.Sprintf("session %s", asString(func() any { k, _ := reg.Get("key"); return k }())), nil
		}
	}
	for _, task := range tasks {
		if v, _ := task.Get("machine"); machine.IsStable(v) {
			return fmt.Sprintf("task %s", asString(func() any { id, _ := task.Get("id"); return id }())), nil
		}
	}
	return "", nil
}

// identityMarker is the file 4eb8591 (#202) added with the stable identity. A
// release tree that contains it resolves recorded machine values through
// internal/machine; one without it compares them to the raw hostname. The
// evidence is the release's own tree, never a field the staging runtime stamps
// into release.json, so a release staged by any runtime classifies the same.
const identityMarker = "go/internal/machine/machine.go"

// targetIdentity is what the target itself says about the stable identity.
type targetIdentity struct {
	offers bool
	// evidence names where that was read; lacking is the refusal's reason.
	evidence, lacking string
}

// releaseIdentity reads the marker from a verified release manifest, whose
// files list every tracked path of the staged tree with its content ID. The
// release's native helper is built from that same tree.
func releaseIdentity(manifest *ordjson.Object) targetIdentity {
	files := asObject(func() any {
		if manifest == nil {
			return nil
		}
		v, _ := manifest.Get("files")
		return v
	}())
	offers := false
	if files != nil {
		_, offers = files.Get(identityMarker)
	}
	return targetIdentity{
		offers:   offers,
		evidence: "release files: " + identityMarker,
		lacking:  fmt.Sprintf("its tree has no %s, which #202 added", identityMarker),
	}
}

// checkoutIdentity asks the checkout's own helper. Selecting the checkout runs
// its prebuilt .local/bin/sumctl, which nothing binds to HEAD, so HEAD's tree
// says nothing about the code that will run. A helper built before
// release-contract reported machine_identity is refused conservatively.
func checkoutIdentity(helperContract *ordjson.Object) targetIdentity {
	var offered []any
	if supports := asObject(func() any {
		if helperContract == nil {
			return nil
		}
		v, _ := helperContract.Get("supports")
		return v
	}()); supports != nil {
		v, _ := supports.Get("machine_identity")
		offered, _ = v.([]any)
	}
	return targetIdentity{
		offers:   containsNumber(offered, contract.MachineIdentity),
		evidence: "checkout helper release-contract: supports.machine_identity",
		lacking:  fmt.Sprintf("the checkout's .local/bin/sumctl does not report supports.machine_identity %d in its release-contract; if HEAD is newer than that build, rebuild it with `mise run test` and retry", contract.MachineIdentity),
	}
}

// machineIdentityCompatibility reports whether the target can read the
// machine values already recorded, and the blocking text when it cannot and
// the override was not given.
func machineIdentityCompatibility(s *store.Store, tasks []*ordjson.Object, target targetIdentity, allow PreIdentity) (*ordjson.Object, string, error) {
	evidence, err := stableIdentityRecord(s, tasks)
	if err != nil {
		return nil, "", err
	}
	row := ordjson.NewObject()
	if evidence == "" {
		row.Set("records", "hostname")
		row.Set("evidence", nil)
	} else {
		row.Set("records", "stable")
		row.Set("evidence", evidence)
	}
	row.Set("target_evidence", target.evidence)
	row.Set("target_offers", target.offers)
	var blocking string
	switch {
	case target.offers:
		row.Set("result", "supported")
	case evidence == "":
		row.Set("result", "unused")
	case allow == AllowPreIdentity:
		row.Set("result", "overridden")
		row.Set("flag", PreIdentityFlag)
	default:
		row.Set("result", "refused")
		blocking = fmt.Sprintf("the target predates the stable machine identity (%s), but this installation's records already carry it (%s). That code compares each recorded machine to the raw hostname, so it sees every record as another machine: the coordinator pane is demoted to developer, `init --role coordinator --reclaim` is refused as other-machine, and recovery means rewriting records by hand. To select it anyway, repeat the command with %s; the override is recorded in the update history", target.lacking, evidence, PreIdentityFlag)
	}
	return row, blocking, nil
}

// identityOverride is the machine_identity row when the override waived a
// refusal, for the update history; nil otherwise.
func identityOverride(compat *ordjson.Object) *ordjson.Object {
	if compat == nil {
		return nil
	}
	row := asObject(func() any { v, _ := compat.Get("machine_identity"); return v }())
	if row == nil || asString(func() any { v, _ := row.Get("result"); return v }()) != "overridden" {
		return nil
	}
	return row
}
