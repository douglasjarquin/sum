package updatecmd

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/release"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	activationStatePath = ".local/activation.json"
	approvalsPath       = ".local/approvals.json"
)

var sha40Hex = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ValidateTarget is the single target-validation path for apply, rollback, compensation, and recovery.
// A staged directory is not approval.
func ValidateTarget(s *store.Store, root, target string, current *ordjson.Object) (*ordjson.Object, *ordjson.Object, error) {
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	if eval, evalErr := filepath.EvalSymlinks(resolvedRoot); evalErr == nil {
		resolvedRoot = eval
	}
	candidate := resolvedRoot
	kind := "checkout"
	if target != "" {
		abs, absErr := filepath.Abs(target)
		if absErr != nil {
			return nil, nil, absErr
		}
		candidate = abs
		kind = "release"
	}
	descriptor := ordjson.NewObject()
	descriptor.Set("kind", kind)
	descriptor.Set("path", candidate)
	if kind == "checkout" {
		headOut, headErr := proc.Run([]string{"git", "-C", root, "rev-parse", "HEAD"}, "", 20*time.Second, true, nil)
		if headErr != nil {
			return nil, nil, headErr
		}
		sha := strings.TrimSpace(headOut.Stdout)
		descriptor.Set("sha", sha)
		if !checkoutIsCurrent(resolvedRoot, sha, current) {
			dirtyOut, dirtyErr := proc.Run([]string{"git", "-C", root, "status", "--porcelain", "--untracked-files=all"}, "", 20*time.Second, true, nil)
			if dirtyErr != nil {
				return nil, nil, dirtyErr
			}
			if strings.TrimSpace(dirtyOut.Stdout) != "" {
				return nil, nil, fmt.Errorf("Checkout rollback requires a clean approved checkout; local changes were preserved.")
			}
		}
		if err := approveUpdateTarget(s, resolvedRoot, "", nil); err != nil {
			return nil, nil, err
		}
	} else {
		releases, relErr := filepath.Abs(filepath.Join(root, ".local", "releases"))
		if relErr != nil {
			return nil, nil, relErr
		}
		if filepath.Dir(candidate) != releases {
			return nil, nil, fmt.Errorf("Update target must be an immutable release of this installation.")
		}
		verified, verifyErr := release.VerifyRelease(candidate, filepath.Base(candidate))
		if verifyErr != nil {
			return nil, nil, verifyErr
		}
		descriptor.Set("sha", filepath.Base(candidate))
		if err := approveUpdateTarget(s, resolvedRoot, candidate, verified); err != nil {
			return nil, nil, err
		}
	}
	compat, compatErr := Compatibility(s, root, candidate, current)
	if compatErr != nil {
		return nil, nil, compatErr
	}
	if ok, _ := compat.Get("ok"); ok != true {
		return compat, descriptor, fmt.Errorf("%s", joinBlocking(func() any { v, _ := compat.Get("blocking"); return v }()))
	}
	return compat, descriptor, nil
}

func checkoutIsCurrent(root, sha string, current *ordjson.Object) bool {
	if current == nil {
		return false
	}
	path := asString(func() any { v, _ := current.Get("path"); return v }())
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if eval, err := filepath.EvalSymlinks(path); err == nil {
		path = eval
	}
	return asString(func() any { v, _ := current.Get("kind"); return v }()) == "checkout" &&
		asString(func() any { v, _ := current.Get("sha"); return v }()) == sha &&
		path == root
}

func requireReleaseProvenance(s *store.Store, root string, manifest *ordjson.Object) error {
	identity, err := readStateIdentity(s)
	if err != nil {
		return err
	}
	source := asObject(func() any { v, _ := manifest.Get("source"); return v }())
	staged := asObject(func() any { v, _ := manifest.Get("staged_by"); return v }())
	repo := asString(func() any {
		if source == nil {
			return nil
		}
		v, _ := source.Get("repository")
		return v
	}())
	installation := asString(func() any {
		if staged == nil {
			return nil
		}
		v, _ := staged.Get("installation")
		return v
	}())
	instance := func() any {
		if staged == nil {
			return nil
		}
		v, _ := staged.Get("instance")
		return v
	}()
	if repo != root || installation != root || fmt.Sprint(instance) != fmt.Sprint(instanceValue(identity)) {
		return fmt.Errorf("Release provenance does not match this installation; selection unchanged.")
	}
	return nil
}

func approveUpdateTarget(s *store.Store, root, target string, manifest *ordjson.Object) error {
	var sha, manifestTree string
	if target == "" {
		headOut, err := proc.Run([]string{"git", "-C", root, "rev-parse", "HEAD"}, "", 20*time.Second, true, nil)
		if err != nil {
			return err
		}
		sha = strings.TrimSpace(headOut.Stdout)
	} else {
		if manifest == nil {
			verified, err := release.VerifyRelease(target, filepath.Base(target))
			if err != nil {
				return err
			}
			manifest = verified
		}
		source := asObject(func() any { v, _ := manifest.Get("source"); return v }())
		sha = asString(func() any {
			if source == nil {
				return nil
			}
			v, _ := source.Get("sha")
			return v
		}())
		manifestTree = asString(func() any {
			if source == nil {
				return nil
			}
			v, _ := source.Get("tree")
			return v
		}())
		if err := requireReleaseProvenance(s, root, manifest); err != nil {
			return err
		}
	}
	path := filepath.Join(root, approvalsPath)
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Update approval records must not be a symlink.")
	}
	identity, err := approvalIdentity(s, root)
	if err != nil {
		return err
	}
	approvals := ordjson.NewObject()
	if _, err := os.Stat(path); err == nil {
		value, readErr := ordjson.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("Update approval records are malformed or belong to another installation.")
		}
		obj := asObject(value)
		if obj == nil {
			return fmt.Errorf("Update approval records are malformed or belong to another installation.")
		}
		approvals = obj
	} else if !os.IsNotExist(err) {
		return err
	} else {
		approvals.Set("schema", json.Number("1"))
		for _, key := range []string{"installation", "instance", "origin"} {
			v, _ := identity.Get(key)
			approvals.Set(key, v)
		}
		approvals.Set("revisions", ordjson.NewObject())
	}
	if fmt.Sprint(func() any { v, _ := approvals.Get("schema"); return v }()) != "1" {
		return fmt.Errorf("Update approval records are malformed or belong to another installation.")
	}
	for _, key := range []string{"installation", "instance", "origin"} {
		want, _ := identity.Get(key)
		got, _ := approvals.Get(key)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			return fmt.Errorf("Update approval records are malformed or belong to another installation.")
		}
	}
	revisions := asObject(func() any { v, _ := approvals.Get("revisions"); return v }())
	if revisions == nil {
		return fmt.Errorf("Update approval records are malformed or belong to another installation.")
	}
	receipt := asObject(func() any { v, _ := revisions.Get(sha); return v }())
	if receipt != nil {
		expectedTree := manifestTree
		if target == "" {
			treeOut, treeErr := proc.Run([]string{"git", "-C", root, "rev-parse", "--verify", sha + "^{tree}", "--"}, "", 20*time.Second, true, nil)
			if treeErr != nil {
				return treeErr
			}
			expectedTree = strings.TrimSpace(treeOut.Stdout)
		}
		if err := receiptMatches(receipt, sha, expectedTree); err != nil {
			return err
		}
		return nil
	}
	treeOut, treeErr := proc.Run([]string{"git", "-C", root, "rev-parse", "--verify", sha + "^{tree}", "--"}, "", 20*time.Second, true, nil)
	if treeErr != nil {
		return treeErr
	}
	tree := strings.TrimSpace(treeOut.Stdout)
	if manifestTree != "" && manifestTree != tree {
		return fmt.Errorf("Release source tree does not match its approved Git revision.")
	}
	source, authErr := resolveAuthorized(root, sha, false)
	if authErr != nil {
		return authErr
	}
	receipt = ordjson.NewObject()
	receipt.Set("sha", sha)
	receipt.Set("tree", tree)
	receipt.Set("branch", asString(func() any { v, _ := source.Get("branch"); return v }()))
	receipt.Set("tip", asString(func() any { v, _ := source.Get("tip"); return v }()))
	receipt.Set("approved_at", store.Now())
	revisions.Set(sha, receipt)
	approvals.Set("revisions", revisions)
	return ordjson.WriteFile(path, approvals)
}

func approvalIdentity(s *store.Store, root string) (*ordjson.Object, error) {
	state, err := readStateIdentity(s)
	if err != nil {
		return nil, err
	}
	remote, err := origin(root)
	if err != nil {
		return nil, err
	}
	row := ordjson.NewObject()
	row.Set("installation", root)
	row.Set("instance", instanceValue(state))
	row.Set("origin", remote)
	return row, nil
}

func receiptMatches(receipt *ordjson.Object, sha, expectedTree string) error {
	gotSHA := asString(func() any { v, _ := receipt.Get("sha"); return v }())
	gotTree := asString(func() any { v, _ := receipt.Get("tree"); return v }())
	branch := asString(func() any { v, _ := receipt.Get("branch"); return v }())
	tip := asString(func() any { v, _ := receipt.Get("tip"); return v }())
	approvedAt := asString(func() any { v, _ := receipt.Get("approved_at"); return v }())
	if _, err := time.Parse(time.RFC3339, approvedAt); err != nil || gotSHA != sha || gotTree != expectedTree || branch == "" || !sha40Hex.MatchString(tip) {
		return fmt.Errorf("Update approval receipt does not match the selected revision and tree.")
	}
	return nil
}

func joinBlocking(v any) string {
	list, _ := v.([]any)
	parts := make([]string, 0, len(list))
	for _, item := range list {
		parts = append(parts, fmt.Sprint(item))
	}
	return strings.Join(parts, "; ")
}

func newGeneration() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func activationPath(root string) string {
	return filepath.Join(root, activationStatePath)
}

func readStateIdentity(s *store.Store) (*ordjson.Object, error) {
	value, err := ordjson.ReadFile(filepath.Join(s.Home, "state.json"))
	if err != nil {
		return nil, err
	}
	obj := asObject(value)
	if obj == nil {
		return nil, fmt.Errorf("state.json is not a JSON object")
	}
	return obj, nil
}

func instanceValue(obj *ordjson.Object) any {
	if obj == nil {
		return nil
	}
	v, ok := obj.Get("instance")
	if !ok {
		return nil
	}
	return v
}

func readActivationState(s *store.Store, root string) (*ordjson.Object, error) {
	path := activationPath(root)
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("Activation state must not be a symlink.")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}
	value, err := ordjson.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Activation state is malformed or belongs to another installation.")
	}
	state := asObject(value)
	identity, idErr := readStateIdentity(s)
	if idErr != nil {
		return nil, idErr
	}
	resolvedRoot, _ := filepath.Abs(root)
	if state == nil ||
		fmt.Sprint(func() any { v, _ := state.Get("schema"); return v }()) != "1" ||
		asString(func() any { v, _ := state.Get("installation"); return v }()) != resolvedRoot ||
		asString(func() any { v, _ := state.Get("home"); return v }()) != s.Home ||
		fmt.Sprint(instanceValue(state)) != fmt.Sprint(instanceValue(identity)) {
		return nil, fmt.Errorf("Activation state is malformed or belongs to another installation.")
	}
	known := asObject(func() any { v, _ := state.Get("known_good"); return v }())
	if known == nil {
		return nil, fmt.Errorf("Activation state is malformed or belongs to another installation.")
	}
	kind := asString(func() any { v, _ := known.Get("kind"); return v }())
	if kind != "checkout" && kind != "release" {
		return nil, fmt.Errorf("Activation state is malformed or belongs to another installation.")
	}
	if asString(func() any { v, _ := known.Get("sha"); return v }()) == "" || asString(func() any { v, _ := known.Get("path"); return v }()) == "" {
		return nil, fmt.Errorf("Activation state is malformed or belongs to another installation.")
	}
	if pending, ok := state.Get("pending"); ok && pending != nil {
		if asObject(pending) == nil {
			return nil, fmt.Errorf("Activation state is malformed or belongs to another installation.")
		}
	}
	return state, nil
}

func writeActivationState(s *store.Store, root string, payload *ordjson.Object) error {
	path := activationPath(root)
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Activation state must not be a symlink.")
	}
	identity, err := readStateIdentity(s)
	if err != nil {
		return err
	}
	resolvedRoot, absErr := filepath.Abs(root)
	if absErr != nil {
		return absErr
	}
	doc := ordjson.NewObject()
	doc.Set("schema", json.Number("1"))
	doc.Set("installation", resolvedRoot)
	doc.Set("home", s.Home)
	doc.Set("instance", instanceValue(identity))
	for _, key := range payload.Keys() {
		v, _ := payload.Get(key)
		doc.Set(key, v)
	}
	return ordjson.WriteFile(path, doc)
}

func requireNoPending(s *store.Store, root string) error {
	state, err := readActivationState(s, root)
	if err != nil {
		return err
	}
	if state == nil {
		return nil
	}
	pending := asObject(func() any { v, _ := state.Get("pending"); return v }())
	if pending == nil {
		return nil
	}
	generation := asString(func() any { v, _ := pending.Get("generation"); return v }())
	return fmt.Errorf("An activation is pending; run `update recover --generation %s` before another update or rollback.", generation)
}

func resolveDescriptor(root string, descriptor *ordjson.Object) (string, error) {
	if descriptor == nil {
		return "", fmt.Errorf("Activation state is malformed or belongs to another installation.")
	}
	kind := asString(func() any { v, _ := descriptor.Get("kind"); return v }())
	sha := asString(func() any { v, _ := descriptor.Get("sha"); return v }())
	path := asString(func() any { v, _ := descriptor.Get("path"); return v }())
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if kind == "checkout" {
		headOut, headErr := proc.Run([]string{"git", "-C", root, "rev-parse", "HEAD"}, "", 20*time.Second, true, nil)
		if headErr != nil {
			return "", headErr
		}
		if sha != strings.TrimSpace(headOut.Stdout) || path != resolvedRoot {
			return "", fmt.Errorf("The known-good checkout no longer matches its recorded revision and path.")
		}
		return "", nil
	}
	abs, absErr := filepath.Abs(path)
	if absErr != nil {
		return "", absErr
	}
	releases, relErr := filepath.Abs(filepath.Join(root, ".local", "releases"))
	if relErr != nil {
		return "", relErr
	}
	if filepath.Dir(abs) != releases || filepath.Base(abs) != sha {
		return "", fmt.Errorf("Activation state names a release outside this installation.")
	}
	if _, verifyErr := release.VerifyRelease(abs, sha); verifyErr != nil {
		return "", verifyErr
	}
	return abs, nil
}

func runtimeHelper(root string, descriptor *ordjson.Object) (string, error) {
	base := asString(func() any { v, _ := descriptor.Get("path"); return v }())
	kind := asString(func() any { v, _ := descriptor.Get("kind"); return v }())
	if kind == "checkout" {
		base = root
	}
	candidates := []string{
		filepath.Join(base, ".local", "bin", "sumctl"),
		filepath.Join(base, ".local", "bin", "sumctl-go"),
	}
	if kind == "checkout" {
		candidates = append(candidates, filepath.Join(root, "bin", "sumctl"))
	}
	for _, path := range candidates {
		info, err := os.Stat(path)
		if err == nil && info.Mode()&0o111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("The prior known-good runtime has no recovery helper; selection unchanged.")
}

func stageRecovery(s *store.Store, root, generation string, prior *ordjson.Object) (*ordjson.Object, error) {
	helper, err := runtimeHelper(root, prior)
	if err != nil {
		return nil, err
	}
	hash, hashErr := release.Sha256File(helper)
	if hashErr != nil {
		return nil, hashErr
	}
	out, runErr := proc.Run([]string{helper, "--version"}, "", 60*time.Second, false, append(os.Environ(), "SUM_INSTALL_ROOT="+root))
	if runErr != nil || out.Code != 0 {
		detail := strings.TrimSpace(out.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(out.Stdout)
		}
		if runErr != nil && detail == "" {
			detail = runErr.Error()
		}
		if len(detail) > 400 {
			detail = detail[len(detail)-400:]
		}
		return nil, fmt.Errorf("The prior known-good runtime cannot execute recovery; selection unchanged: %s", detail)
	}
	argv := []any{helper, "--home", s.Home, "update", "recover", "--generation", generation}
	rec := ordjson.NewObject()
	rec.Set("helper", helper)
	rec.Set("sha256", hash)
	rec.Set("home", s.Home)
	rec.Set("runtime", asString(func() any { v, _ := prior.Get("path"); return v }()))
	rec.Set("argv", argv)
	return rec, nil
}

func ensureActivationState(s *store.Store, root string, current *ordjson.Object) (*ordjson.Object, error) {
	state, err := readActivationState(s, root)
	if err != nil {
		return nil, err
	}
	currentDesc := selectionDescriptor(current)
	if state != nil {
		pending := asObject(func() any { v, _ := state.Get("pending"); return v }())
		if pending == nil && !descriptorsEqual(asObject(func() any { v, _ := state.Get("known_good"); return v }()), currentDesc) {
			return nil, fmt.Errorf("The selected runtime differs from committed known-good activation state; refusing to overwrite recovery history.")
		}
		return state, nil
	}
	target, resolveErr := resolveDescriptor(root, currentDesc)
	if resolveErr != nil {
		return nil, resolveErr
	}
	compat, _, validErr := ValidateTarget(s, root, target, current)
	if validErr != nil {
		blocking := ""
		if compat != nil {
			blocking = joinBlocking(func() any { v, _ := compat.Get("blocking"); return v }())
		}
		if blocking == "" {
			blocking = validErr.Error()
		}
		return nil, fmt.Errorf("The current runtime cannot be established as known-good: %s", blocking)
	}
	check := postCheck(s, root)
	if ok, _ := check.Get("ok"); ok != true {
		return nil, fmt.Errorf("The current stable entrypoint cannot be established as known-good: %v", func() any { v, _ := check.Get("detail"); return v }())
	}
	payload := ordjson.NewObject()
	payload.Set("generation", nil)
	payload.Set("from", currentDesc)
	payload.Set("to", currentDesc)
	payload.Set("known_good", currentDesc)
	payload.Set("pending", nil)
	if writeErr := writeActivationState(s, root, payload); writeErr != nil {
		return nil, writeErr
	}
	return readActivationState(s, root)
}

func recoverPendingLocked(s *store.Store, root, generation string) (*ordjson.Object, error) {
	if TestRecoverPending != nil {
		return TestRecoverPending(s, root, generation)
	}
	state, err := readActivationState(s, root)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("No interrupted activation is recorded for generation %s.", generation)
	}
	pending := asObject(func() any { v, _ := state.Get("pending"); return v }())
	if pending == nil {
		return nil, fmt.Errorf("No interrupted activation is recorded for generation %s.", generation)
	}
	pendingGen := asString(func() any { v, _ := pending.Get("generation"); return v }())
	if pendingGen != generation {
		return nil, fmt.Errorf("Recovery generation %s is stale; pending generation is %s.", generation, pendingGen)
	}
	recovery := asObject(func() any { v, _ := pending.Get("recovery"); return v }())
	helper := asString(func() any {
		if recovery == nil {
			return nil
		}
		v, _ := recovery.Get("helper")
		return v
	}())
	wantHash := asString(func() any {
		if recovery == nil {
			return nil
		}
		v, _ := recovery.Get("sha256")
		return v
	}())
	gotHash, hashErr := release.Sha256File(helper)
	if hashErr != nil || gotHash != wantHash {
		return nil, fmt.Errorf("The generation recovery helper is missing or does not match its recorded hash.")
	}
	from := asObject(func() any { v, _ := pending.Get("from"); return v }())
	to := asObject(func() any { v, _ := pending.Get("to"); return v }())
	current := selectionDescriptor(DefaultRuntime(root))
	target, resolveErr := resolveDescriptor(root, from)
	if resolveErr != nil {
		return nil, resolveErr
	}
	compat, _, validErr := ValidateTarget(s, root, target, DefaultRuntime(root))
	if validErr != nil {
		blocking := validErr.Error()
		if compat != nil {
			if joined := joinBlocking(func() any { v, _ := compat.Get("blocking"); return v }()); joined != "" {
				blocking = joined
			}
		}
		return nil, fmt.Errorf("The prior known-good runtime is no longer compatible: %s", blocking)
	}
	if descriptorsEqual(current, from) {
		check := postCheck(s, root)
		if ok, _ := check.Get("ok"); ok != true {
			pending.Set("recovery_status", "failed")
			pending.Set("recovery_check", check)
			state.Set("pending", pending)
			_ = writeActivationState(s, root, state)
			return nil, fmt.Errorf("Recovery generation %s found the prior selection but its stable entrypoint check failed: %v", generation, func() any { v, _ := check.Get("detail"); return v }())
		}
		log := ordjson.NewObject()
		log.Set("action", "recover")
		log.Set("result", "recovered")
		log.Set("generation", generation)
		log.Set("changed", false)
		log.Set("from", current)
		log.Set("to", current)
		log.Set("post_check", check)
		updateLog(root, log)
		state.Set("pending", nil)
		if writeErr := writeActivationState(s, root, state); writeErr != nil {
			return nil, writeErr
		}
		result := ordjson.NewObject()
		result.Set("action", "recover")
		result.Set("generation", generation)
		result.Set("changed", false)
		result.Set("default", current)
		result.Set("post_check", check)
		result.Set("note", "The prior known-good runtime was already selected and has been verified; no pointer change was needed.")
		return result, nil
	}
	if !descriptorsEqual(current, to) {
		return nil, fmt.Errorf("The current selection matches neither endpoint of the pending generation; recovery refused without mutation.")
	}
	if _, selectErr := SelectDefault(root, target); selectErr != nil {
		return nil, selectErr
	}
	restored := selectionDescriptor(DefaultRuntime(root))
	check := postCheck(s, root)
	if ok, _ := check.Get("ok"); ok != true {
		pending.Set("recovery_status", "failed")
		pending.Set("recovery_check", check)
		state.Set("pending", pending)
		_ = writeActivationState(s, root, state)
		return nil, fmt.Errorf("Recovery generation %s restored %s but its stable entrypoint check failed: %v", generation, asString(func() any { v, _ := restored.Get("sha"); return v }()), func() any { v, _ := check.Get("detail"); return v }())
	}
	log := ordjson.NewObject()
	log.Set("action", "recover")
	log.Set("result", "recovered")
	log.Set("generation", generation)
	log.Set("changed", true)
	log.Set("from", to)
	log.Set("to", restored)
	log.Set("post_check", check)
	updateLog(root, log)
	state.Set("pending", nil)
	if writeErr := writeActivationState(s, root, state); writeErr != nil {
		return nil, writeErr
	}
	result := ordjson.NewObject()
	result.Set("action", "recover")
	result.Set("generation", generation)
	result.Set("changed", true)
	result.Set("default", restored)
	result.Set("post_check", check)
	result.Set("note", "The prior known-good runtime was restored and verified; records are untouched.")
	return result, nil
}

func argvJoin(recovery *ordjson.Object) string {
	if recovery == nil {
		return "update recover --generation GENERATION"
	}
	raw, _ := recovery.Get("argv")
	list, _ := raw.([]any)
	parts := make([]string, 0, len(list))
	for _, item := range list {
		parts = append(parts, fmt.Sprint(item))
	}
	if len(parts) == 0 {
		return "update recover --generation GENERATION"
	}
	return strings.Join(parts, " ")
}
