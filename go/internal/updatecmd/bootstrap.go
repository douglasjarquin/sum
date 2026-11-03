package updatecmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/douglasjarquin/sum/go/internal/execution"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/release"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
)

var TestBootstrapBoundary func(string) error

func bootstrapStep(phase string) error {
	if TestBootstrapBoundary != nil {
		return TestBootstrapBoundary(phase)
	}
	return nil
}

func Bootstrap(s *store.Store, ref string, noFetch, approve bool, operation string) (*ordjson.Object, error) {
	root, err := release.InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	for _, member := range []string{filepath.Join(root, ".local"), filepath.Join(root, ".local", "releases"), filepath.Join(root, ".local", "update.lock")} {
		if info, e := os.Lstat(member); e == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("bootstrap path must not be a symlink: %s", member)
		} else if e != nil && !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
	}
	unlock, err := activationLock(root)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err = requireNoPending(s, root); err != nil {
		return nil, err
	}
	source, err := resolveAuthorized(root, ref, !noFetch)
	if err != nil {
		return nil, err
	}
	sha := effectField(source, "sha")
	path := filepath.Join(root, ".local", "bootstrap.json")
	journal, err := readBootstrap(path)
	if err != nil {
		return nil, err
	}
	owner, err := s.Owner()
	if err != nil {
		return nil, err
	}
	current := DefaultRuntime(root)
	view := ordjson.NewObject()
	view.Set("installation", root)
	view.Set("old_runtime", selectionDescriptor(current))
	view.Set("recorded_owner", owner)
	view.Set("source", source)
	view.Set("journal", journal)
	operationID := effectField(journal, "operation")
	if !approve {
		inspection, _, inspectErr := bootstrapInspect(s, owner, operationID, journal)
		view.Set("inspection", inspection)
		if inspectErr != nil {
			view.Set("blocker", inspectErr.Error())
		}
		view.Set("next", "After explicit user authorization, repeat with --approve; resume an interrupted operation with its exact --operation and --ref SHA.")
		return view, nil
	}
	if journal != nil {
		if operation == "" || operation != effectField(journal, "operation") || sha != effectField(journal, "sha") || root != effectField(journal, "installation") || s.Home != effectField(journal, "home") || s.Generation != effectField(journal, "state_generation") {
			return nil, fmt.Errorf("bootstrap journal exists; inspect it and resume its exact --operation %s --ref %s", effectField(journal, "operation"), effectField(journal, "sha"))
		}
		owner = asObject(func() any { v, _ := journal.Get("owner"); return v }())
		before := asObject(func() any { v, _ := journal.Get("old_runtime"); return v }())
		selected := selectionDescriptor(current)
		if !descriptorsEqual(before, selected) && !(effectField(selected, "kind") == "release" && effectField(selected, "sha") == sha && effectField(selected, "path") == filepath.Join(root, ".local/releases", sha)) {
			return nil, fmt.Errorf("bootstrap selection changed outside this operation; recovery refused")
		}
	} else {
		if operation != "" {
			return nil, fmt.Errorf("no bootstrap operation %s exists", operation)
		}
		if session, e := s.CoordinatorSession(); e != nil {
			return nil, e
		} else if session != nil {
			return nil, fmt.Errorf("logical ownership already exists; use its claim and ordinary update apply")
		}
		if owner == nil {
			return nil, fmt.Errorf("bootstrap requires a recorded native coordinator")
		}
		if ok, _ := current.Get("ok"); ok != true {
			return nil, fmt.Errorf("old runtime cannot be verified; selection preserved")
		}
	}
	_, plan, err := bootstrapInspect(s, owner, operationID, journal)
	if err != nil {
		return nil, err
	}
	staged, err := release.Stage(s, sha)
	if err != nil {
		return nil, err
	}
	target := effectField(staged, "release")
	offered, err := release.ReadTargetContract(context.Background(), target)
	if err != nil {
		return nil, err
	}
	if !hasInt(offered.Supports.StateSchema, 3) || !hasInt(offered.Supports.OwnerSchema, 2) {
		return nil, fmt.Errorf("bootstrap target must support state schema 3 and logical owner schema 2")
	}
	if _, _, err = validateTargetForTasks(s, root, target, current, plan.tasks); err != nil {
		return nil, err
	}
	if _, _, err = settings.NativeRetirementPreferences(s, owner); err != nil {
		return nil, err
	}
	manifestHash, err := release.Sha256File(filepath.Join(target, release.Manifest))
	if err != nil {
		return nil, err
	}
	if journal == nil {
		id, err := newGeneration()
		if err != nil {
			return nil, err
		}
		journal = ordjson.NewObject()
		journal.Set("schema", json.Number("1"))
		journal.Set("operation", id)
		journal.Set("session_id", "c-"+id)
		journal.Set("installation", root)
		journal.Set("home", s.Home)
		journal.Set("state_generation", s.Generation)
		journal.Set("sha", sha)
		journal.Set("manifest_sha256", manifestHash)
		journal.Set("owner", owner)
		journal.Set("old_runtime", selectionDescriptor(current))
		checkout, _ := source.Get("checkout")
		journal.Set("checkout_head", effectField(asObject(checkout), "head"))
		journal.Set("phase", "prepared")
		journal.Set("reconciliation", plan.releases)
		if err = ordjson.WriteFile(path, journal); err != nil {
			return nil, err
		}
	} else if manifestHash != effectField(journal, "manifest_sha256") {
		return nil, fmt.Errorf("bootstrap target manifest changed; recovery refused")
	}
	resumeError := func(err error) error {
		return fmt.Errorf("%w; resume bootstrap --approve --operation %s --ref %s", err, effectField(journal, "operation"), sha)
	}
	if err = bootstrapCheckout(root, journal); err != nil {
		return nil, resumeError(err)
	}
	if err = bootstrapStep("prepared"); err != nil {
		return nil, resumeError(err)
	}
	session, err := s.RetireForUpgrade(effectField(journal, "session_id"), owner, func() error {
		if err := bootstrapCheckout(root, journal); err != nil {
			return err
		}
		inspection, freshPlan, inspectErr := bootstrapInspect(s, owner, effectField(journal, "operation"), journal)
		if inspectErr != nil {
			return inspectErr
		}
		if _, _, err := validateTargetForTasks(s, root, target, DefaultRuntime(root), freshPlan.tasks); err != nil {
			return err
		}
		journal.Set("inspection", inspection)
		if err := ordjson.WriteFile(path, journal); err != nil {
			return err
		}
		for _, task := range freshPlan.changed {
			if err := s.SaveTask(task); err != nil {
				return err
			}
			if err := bootstrapStep("reconciled:" + effectField(task, "id")); err != nil {
				return err
			}
		}
		return nil
	}, func() error { return bootstrapStep("fenced") })
	if err != nil {
		return nil, resumeError(err)
	}
	if err = bootstrapStep("owned"); err != nil {
		return nil, resumeError(err)
	}
	if effectField(journal, "phase") == "complete" {
		if effectField(current, "sha") != sha || effectField(current, "kind") != "release" {
			return nil, fmt.Errorf("completed bootstrap selection no longer matches its target")
		}
		check := postCheck(s, root)
		if ok, _ := check.Get("ok"); ok != true {
			return nil, fmt.Errorf("completed bootstrap entrypoint changed")
		}
		return bootstrapResult(root, journal, session, check), nil
	}
	if err = retireBootstrapPreferences(s, session.Token()); err != nil {
		return nil, resumeError(err)
	}
	journal.Set("phase", "retired")
	if err = ordjson.WriteFile(path, journal); err != nil {
		return nil, resumeError(err)
	}
	if err = bootstrapStep("retired"); err != nil {
		return nil, resumeError(err)
	}
	if err = bootstrapCheckout(root, journal); err != nil {
		return nil, resumeError(err)
	}
	if _, err = SelectDefault(root, target); err != nil {
		return nil, resumeError(err)
	}
	if err = bootstrapStep("selected"); err != nil {
		return nil, resumeError(err)
	}
	if err = bootstrapCheckout(root, journal); err != nil {
		return nil, resumeError(err)
	}
	alignment := syncInstallationCheckout(root, sha)
	if effectField(alignment, "result") == "refused" {
		return nil, resumeError(fmt.Errorf("checkout alignment refused: %s", effectField(alignment, "reason")))
	}
	if err = bootstrapStep("aligned"); err != nil {
		return nil, resumeError(err)
	}
	check := postCheck(s, root)
	if ok, _ := check.Get("ok"); ok != true {
		return nil, resumeError(fmt.Errorf("bootstrap entrypoint verification failed: %v", check))
	}
	journal.Set("phase", "complete")
	if err = ordjson.WriteFile(path, journal); err != nil {
		return nil, resumeError(err)
	}
	return bootstrapResult(root, journal, session, check), nil
}

func bootstrapCheckout(root string, journal *ordjson.Object) error {
	sha := effectField(journal, "sha")
	alignment := checkoutAlignment(root, sha, false)
	if effectField(alignment, "result") == "refused" {
		return fmt.Errorf("bootstrap checkout refused: %s", effectField(alignment, "reason"))
	}
	head := effectField(alignment, "head")
	if !sha40Hex.MatchString(effectField(journal, "checkout_head")) || (head != sha && (effectField(journal, "phase") == "complete" || head != effectField(journal, "checkout_head"))) {
		return fmt.Errorf("bootstrap checkout HEAD changed outside its recorded endpoints; checkout preserved")
	}
	return nil
}

func bootstrapResult(root string, journal *ordjson.Object, session *store.CoordinatorSession, check *ordjson.Object) *ordjson.Object {
	result := ordjson.NewObject()
	result.Set("action", "bootstrap")
	result.Set("operation", effectField(journal, "operation"))
	result.Set("role", "coordinator")
	result.Set("claim", session.Token())
	result.Set("default", selectionDescriptor(DefaultRuntime(root)))
	result.Set("post_check", check)
	result.Set("next", "Continue in this conversation: run the installation's bin/sumctl init --claim TOKEN and read its validated coordinator procedure.")
	return result
}

func hasInt(values []int, n int) bool {
	for _, v := range values {
		if v == n {
			return true
		}
	}
	return false
}

func readBootstrap(path string) (*ordjson.Object, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("bootstrap journal must not be a symlink")
	}
	raw, err := ordjson.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	obj := asObject(raw)
	if obj == nil || fmt.Sprint(func() any { v, _ := obj.Get("schema"); return v }()) != "1" || !sha40Hex.MatchString(effectField(obj, "sha")) || effectField(obj, "operation") == "" {
		return nil, fmt.Errorf("malformed bootstrap journal")
	}
	return obj, nil
}

func bootstrapInspect(s *store.Store, owner *ordjson.Object, operation string, journal *ordjson.Object) (*ordjson.Object, *bootstrapPlan, error) {
	view := ordjson.NewObject()
	tasks, err := s.AllTasks()
	if err != nil {
		return view, nil, err
	}
	plan, blocked := planBootstrapReleases(s, filepath.Dir(s.Root), operation, tasks)
	if journal != nil {
		if err := plan.matches(journal); err != nil {
			blocked = append(blocked, err.Error())
		}
	}
	view.Set("reconciliation", plan.releases)
	blocked = append(blocked, unresolvedEffects(s, tasks)...)
	registrations, err := s.Registrations()
	if err != nil {
		return view, plan, err
	}
	var observations []any
	observe := func(record *ordjson.Object, label string, observer bool) {
		if record == nil {
			return
		}
		e := store.EndpointFromContext(record)
		row := ordjson.NewObject()
		row.Set("kind", label)
		row.Set("endpoint", record)
		observations = append(observations, row)
		if err := e.Validate(); err != nil {
			blocked = append(blocked, label+": "+err.Error())
			return
		}
		if observer && e.Role != "observer" {
			blocked = append(blocked, label+": endpoint is not an observer")
			return
		}
		if !observer {
			proof := execution.NativeProgramStopped(s, e)
			row.Set("state", proof.Outcome)
			row.Set("stop_evidence", proof.Observation)
			if proof.Outcome != execution.OutcomeStopped {
				blocked = append(blocked, label+": "+proof.Reason)
			}
			if e.Role != "coordinator" {
				if e.Cwd == "" || filepath.Clean(e.Cwd) == filepath.Dir(s.Root) {
					blocked = append(blocked, label+": isolated task checkout identity is missing")
				} else if inside, err := proc.ProcessesBoundTo(e.Cwd, nil); err != nil {
					blocked = append(blocked, label+": "+err.Error())
				} else if len(inside) != 0 {
					blocked = append(blocked, fmt.Sprintf("%s: checkout still has process %d", label, inside[0].PID))
				}
			}
			return
		}
		client := hcclient.Client{Socket: e.Socket, CoreID: e.CoreID}
		terminal, err := client.Get(context.Background(), e.Address)
		if err != nil {
			var rpc *hcclient.RPCError
			var detail struct {
				Kind string `json:"kind"`
			}
			if errors.As(err, &rpc) && json.Unmarshal(rpc.Data, &detail) == nil && detail.Kind == "not_found" {
				row.Set("state", "absent")
				return
			}
			blocked = append(blocked, label+": "+err.Error())
			return
		}
		row.Set("state", terminal.State)
		if terminal.Address != e.Address || terminal.CreatedMS != e.CreatedMS {
			blocked = append(blocked, label+": terminal identity changed")
			return
		}
		if terminal.State != "finished" && !(observer && terminal.State == "live") {
			blocked = append(blocked, label+": terminal is still live or uncertain")
		}
	}
	observe(owner, "native coordinator", false)
	for _, registration := range registrations {
		role := effectField(registration, "role")
		if role == "worker" || role == "reviewer" {
			observe(registration, role, false)
		}
	}
	for _, task := range tasks {
		if proof := execution.ServicesStopped(s, task); proof.Outcome != execution.OutcomeStopped {
			blocked = append(blocked, "task "+effectField(task, "id")+": "+proof.Reason)
		}
		for _, field := range []string{"worker_endpoint", "reviewer"} {
			observe(asObject(func() any { v, _ := task.Get(field); return v }()), "task "+effectField(task, "id")+" "+field, false)
		}
	}
	healthPath := filepath.Join(s.Home, "hook", "health.json")
	if raw, e := ordjson.ReadFile(healthPath); e == nil {
		health := asObject(raw)
		view.Set("observer_health", health)
		if health == nil {
			blocked = append(blocked, "malformed observer health")
		} else {
			schema, _ := health.Get("schema")
			effectValue, hasEffect := health.Get("launch_effect")
			_, effectString := effectValue.(string)
			observerValue, _ := health.Get("observer")
			observer := asObject(observerValue)
			if fmt.Sprint(schema) != "2" || (hasEffect && !effectString) || (observerValue != nil && observer == nil) {
				blocked = append(blocked, "malformed observer health schema or launch fields")
			}
			effect := effectField(health, "launch_effect")
			if (effect != "" && effect != "submitted" && effect != "refused" && effect != "not-submitted") || (effect == "submitted" && observer == nil) || (observer != nil && effect != "submitted") || (effect == "" && effectField(health, "launch_attempt") != "") {
				blocked = append(blocked, "unresolved observer launch")
			}
			observe(observer, "observer (writer and sender fenced at retirement)", true)
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		blocked = append(blocked, "observer health: "+e.Error())
	}
	view.Set("terminals", observations)
	view.Set("blocking", blocked)
	if len(blocked) != 0 {
		return view, plan, fmt.Errorf("bootstrap refused: %s", joinBlocking(blocked))
	}
	return view, plan, nil
}

func retireBootstrapPreferences(s *store.Store, token string) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	session, err := s.CheckClaim(token)
	if err != nil {
		return err
	}
	prefs, changed, err := settings.NativeRetirementPreferences(s, session.RetiredNative)
	if err != nil {
		return err
	}
	if changed {
		if err = ordjson.WriteFile(filepath.Join(s.Home, settings.File), prefs); err != nil {
			return err
		}
	}
	path := filepath.Join(s.Home, "hook", "health.json")
	if _, err = os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	archive := filepath.Join(s.Home, "hook", "health.retired-native.json")
	if _, err = os.Lstat(archive); err == nil {
		return fmt.Errorf("observer archive already exists; preserve both health records and inspect")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(path, archive)
}
