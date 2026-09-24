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

// machineIdentityCompatibility reports whether the candidate can read the
// machine values already recorded, and the blocking text when it cannot and
// the override was not given.
func machineIdentityCompatibility(s *store.Store, tasks []*ordjson.Object, offered []any, allow PreIdentity) (*ordjson.Object, string, error) {
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
	if offered == nil {
		offered = []any{}
	}
	row.Set("candidate", offered)
	var blocking string
	switch {
	case containsNumber(offered, contract.MachineIdentity):
		row.Set("result", "supported")
	case evidence == "":
		row.Set("result", "unused")
	case allow == AllowPreIdentity:
		row.Set("result", "overridden")
		row.Set("flag", PreIdentityFlag)
	default:
		row.Set("result", "refused")
		blocking = fmt.Sprintf("the target release predates the stable machine identity (its release.json does not offer supports.machine_identity %d), but this installation's records already carry it (%s). That release compares each recorded machine to the raw hostname, so it sees every record as another machine: the coordinator pane is demoted to developer, `init --role coordinator --reclaim` is refused as other-machine, and recovery means rewriting records by hand. For a deliberate emergency rollback, repeat the command with %s; the override is recorded in the update history", contract.MachineIdentity, evidence, PreIdentityFlag)
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
