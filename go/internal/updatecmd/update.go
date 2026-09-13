package updatecmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/release"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

var shaPrefix = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func jsonNumber(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asObject(v any) *ordjson.Object {
	o, _ := v.(*ordjson.Object)
	return o
}

func origin(root string) (string, error) {
	out, err := proc.Run([]string{"git", "-C", root, "remote", "get-url", "origin"}, "", 20*time.Second, false, nil)
	if err != nil || out.Code != 0 {
		return "", fmt.Errorf("The installation has no `origin` remote. Add one yourself; sum never changes remotes.")
	}
	return strings.TrimSpace(out.Stdout), nil
}

func defaultBranch(root string) string {
	out, err := proc.Run([]string{"git", "-C", root, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"}, "", 20*time.Second, false, nil)
	if err == nil {
		ref := strings.TrimSpace(out.Stdout)
		if strings.HasPrefix(ref, "refs/remotes/origin/") {
			return strings.TrimPrefix(ref, "refs/remotes/origin/")
		}
	}
	return "main"
}

func resolveAuthorized(root, ref string, fetch bool) (*ordjson.Object, error) {
	remote, err := origin(root)
	if err != nil {
		return nil, err
	}
	branch := defaultBranch(root)
	fetched := false
	if fetch {
		if _, ferr := proc.Run([]string{"git", "-C", root, "fetch", "--quiet", "origin", branch}, "", 300*time.Second, true, nil); ferr != nil {
			return nil, ferr
		}
		fetched = true
	}
	upstream := "refs/remotes/origin/" + branch
	if out, err := proc.Run([]string{"git", "-C", root, "show-ref", "--verify", "--quiet", upstream}, "", 20*time.Second, false, nil); err != nil || out.Code != 0 {
		return nil, fmt.Errorf("%s is unknown here. Fetch origin first (omit --no-fetch).", upstream)
	}
	tipOut, err := proc.Run([]string{"git", "-C", root, "rev-parse", upstream}, "", 20*time.Second, true, nil)
	if err != nil {
		return nil, err
	}
	tip := strings.TrimSpace(tipOut.Stdout)
	target := ref
	if target == "" {
		target = upstream
	}
	resolved, err := proc.Run([]string{"git", "-C", root, "rev-parse", "--verify", target + "^{commit}", "--"}, "", 20*time.Second, false, nil)
	if err != nil || resolved.Code != 0 {
		return nil, fmt.Errorf("Unknown revision %q.", target)
	}
	sha := strings.TrimSpace(resolved.Stdout)
	if anc, err := proc.Run([]string{"git", "-C", root, "merge-base", "--is-ancestor", sha, tip}, "", 20*time.Second, false, nil); err != nil || anc.Code != 0 {
		return nil, fmt.Errorf("%s is not merged on origin/%s (%s). sum activates only merged revisions of its own origin; a development or task branch is never executed as an update.", sha, branch, tip)
	}
	headOut, _ := proc.Run([]string{"git", "-C", root, "rev-parse", "HEAD"}, "", 20*time.Second, true, nil)
	dirtyOut, _ := proc.Run([]string{"git", "-C", root, "status", "--porcelain", "--untracked-files=no"}, "", 20*time.Second, true, nil)
	checkout := ordjson.NewObject()
	checkout.Set("head", strings.TrimSpace(headOut.Stdout))
	checkout.Set("dirty", strings.TrimSpace(dirtyOut.Stdout) != "")
	checkout.Set("note", "check and stage leave the checkout unchanged. apply fast-forwards a clean installation clone when the selected SHA is a fast-forward.")
	result := ordjson.NewObject()
	result.Set("sha", sha)
	result.Set("ref", target)
	result.Set("origin", remote)
	result.Set("branch", branch)
	result.Set("tip", tip)
	result.Set("fetched", fetched)
	result.Set("checkout", checkout)
	return result, nil
}

func DefaultRuntime(root string) *ordjson.Object {
	link := filepath.Join(root, ".local", "current")
	info, err := os.Lstat(link)
	headOut, _ := proc.Run([]string{"git", "-C", root, "rev-parse", "HEAD"}, "", 20*time.Second, true, nil)
	head := strings.TrimSpace(headOut.Stdout)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		result := ordjson.NewObject()
		result.Set("kind", "checkout")
		result.Set("path", root)
		result.Set("sha", head)
		result.Set("manifest", nil)
		result.Set("ok", true)
		return result
	}
	target, _ := os.Readlink(link)
	path := target
	if !filepath.IsAbs(target) {
		path = filepath.Join(filepath.Dir(link), target)
	}
	resolved, _ := filepath.Abs(path)
	result := ordjson.NewObject()
	result.Set("kind", "release")
	result.Set("path", resolved)
	result.Set("link", target)
	result.Set("sha", filepath.Base(resolved))
	result.Set("manifest", nil)
	result.Set("ok", false)
	manifest, verErr := release.VerifyRelease(resolved, filepath.Base(resolved))
	if verErr != nil {
		result.Set("error", verErr.Error())
		return result
	}
	result.Set("manifest", manifest)
	result.Set("ok", true)
	return result
}

func selectionDescriptor(runtimeObj *ordjson.Object) *ordjson.Object {
	row := ordjson.NewObject()
	kind, _ := runtimeObj.Get("kind")
	sha, _ := runtimeObj.Get("sha")
	path, _ := runtimeObj.Get("path")
	if p, ok := path.(string); ok && p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			path = abs
		}
	}
	row.Set("kind", kind)
	row.Set("sha", sha)
	row.Set("path", path)
	return row
}

func offeredContracts(manifest *ordjson.Object) (sumVersion, herdrCLI string, mcp *ordjson.Object, stateSchema, briefSchema []any) {
	if manifest == nil {
		return
	}
	sumVersion = asString(func() any { v, _ := manifest.Get("sum_version"); return v }())
	contracts := asObject(func() any { v, _ := manifest.Get("contracts"); return v }())
	if contracts != nil {
		herdrCLI = asString(func() any { v, _ := contracts.Get("herdr_cli"); return v }())
		mcp = asObject(func() any { v, _ := contracts.Get("mcp"); return v }())
	}
	supports := asObject(func() any { v, _ := manifest.Get("supports"); return v }())
	if supports != nil {
		if v, ok := supports.Get("state_schema"); ok {
			stateSchema, _ = v.([]any)
		}
		if v, ok := supports.Get("brief_schema"); ok {
			briefSchema, _ = v.([]any)
		}
	}
	return
}

func checkoutContract(candidatePath string) (*ordjson.Object, error) {
	helpers := []string{
		filepath.Join(candidatePath, ".local", "bin", "sumctl"),
		filepath.Join(candidatePath, ".local", "bin", "sumctl-go"),
	}
	var last string
	for _, helper := range helpers {
		info, err := os.Stat(helper)
		if err != nil || info.Mode()&0o111 == 0 {
			continue
		}
		out, runErr := proc.Run([]string{helper, "release-contract"}, candidatePath, 60*time.Second, false, append(os.Environ(), "SUM_INSTALL_ROOT="+candidatePath))
		if runErr != nil || out.Code != 0 {
			detail := strings.TrimSpace(out.Stderr)
			if detail == "" {
				detail = strings.TrimSpace(out.Stdout)
			}
			if runErr != nil && detail == "" {
				detail = runErr.Error()
			}
			last = detail
			continue
		}
		value, decErr := ordjson.Decode([]byte(strings.TrimSpace(out.Stdout)))
		if decErr != nil {
			return nil, fmt.Errorf("Candidate release contract is not JSON: %s", trimForError(out.Stdout))
		}
		obj := asObject(value)
		if obj == nil {
			return nil, fmt.Errorf("Candidate release contract is incomplete")
		}
		if _, hasSum := obj.Get("sum_version"); !hasSum {
			return nil, fmt.Errorf("Candidate release contract is incomplete")
		}
		if asObject(func() any { v, _ := obj.Get("contracts"); return v }()) == nil {
			return nil, fmt.Errorf("Candidate release contract is incomplete")
		}
		if asObject(func() any { v, _ := obj.Get("supports"); return v }()) == nil {
			return nil, fmt.Errorf("Candidate release contract is incomplete")
		}
		return obj, nil
	}
	if last != "" {
		return nil, fmt.Errorf("%s", last)
	}
	return nil, fmt.Errorf("checkout has no release manifest or contract evidence")
}

func trimForError(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300]
	}
	return s
}

func containsNumber(list []any, n int) bool {
	want := fmt.Sprint(n)
	for _, v := range list {
		switch t := v.(type) {
		case json.Number:
			if string(t) == want {
				return true
			}
		case float64:
			if int(t) == n {
				return true
			}
		}
	}
	return false
}

func Compatibility(s *store.Store, root, candidatePath string, current *ordjson.Object) (*ordjson.Object, error) {
	resolvedRoot, _ := filepath.Abs(root)
	resolvedCandidate, _ := filepath.Abs(candidatePath)
	checkout := resolvedRoot == resolvedCandidate
	var blocking []any
	var deferred []any
	var manifest *ordjson.Object
	var candidateSHA string
	if checkout {
		headOut, err := proc.Run([]string{"git", "-C", root, "rev-parse", "HEAD"}, "", 20*time.Second, true, nil)
		if err != nil {
			return nil, err
		}
		candidateSHA = strings.TrimSpace(headOut.Stdout)
		m, contractErr := checkoutContract(candidatePath)
		if contractErr != nil {
			result := ordjson.NewObject()
			result.Set("ok", false)
			result.Set("blocking", []any{"checkout contract: " + contractErr.Error()})
			result.Set("deferred", []any{})
			result.Set("probes", []any{})
			result.Set("tasks", []any{})
			return result, nil
		}
		manifest = m
	} else {
		m, err := release.VerifyRelease(candidatePath, filepath.Base(candidatePath))
		if err != nil {
			result := ordjson.NewObject()
			result.Set("ok", false)
			result.Set("blocking", []any{"candidate bundle: " + err.Error()})
			result.Set("deferred", []any{})
			result.Set("probes", []any{})
			result.Set("tasks", []any{})
			return result, nil
		}
		manifest = m
		candidateSHA = asString(func() any {
			src := asObject(func() any { v, _ := m.Get("source"); return v }())
			if src == nil {
				return nil
			}
			v, _ := src.Get("sha")
			return v
		}())
	}
	sumVersion, _, mcp, stateSchema, briefSchema := offeredContracts(manifest)
	stateObj, _ := ordjson.ReadFile(filepath.Join(s.Home, "state.json"))
	stateSchemaVal := 1
	if so := asObject(stateObj); so != nil {
		if v, ok := so.Get("schema"); ok {
			if num, isNum := v.(json.Number); isNum {
				if n, convErr := num.Int64(); convErr == nil {
					stateSchemaVal = int(n)
				}
			}
		}
	}
	if !containsNumber(stateSchema, stateSchemaVal) {
		blocking = append(blocking, fmt.Sprintf("state schema %d is not supported by the candidate (%v)", stateSchemaVal, stateSchema))
	}
	var tasks []any
	all, err := s.AllTasks()
	if err != nil {
		return nil, err
	}
	for _, task := range all {
		status, _ := task.Get("status")
		if status == "archived" {
			continue
		}
		id, _ := task.Get("id")
		idStr, _ := id.(string)
		row := ordjson.NewObject()
		row.Set("task", idStr)
		vers, vErr := versions.ReadVersions(s, task)
		if vErr != nil {
			row.Set("brief_schema", nil)
			row.Set("error", vErr.Error())
			blocking = append(blocking, fmt.Sprintf("task %s: version sidecar unreadable: %s", idStr, vErr))
		} else {
			brief := 1
			if v, ok := vers.Get("brief_schema"); ok {
				if num, isNum := v.(json.Number); isNum {
					if n, convErr := num.Int64(); convErr == nil {
						brief = int(n)
					}
				}
			}
			row.Set("brief_schema", jsonNumber(brief))
			st, _ := task.Get("status")
			row.Set("status", st)
			if !containsNumber(briefSchema, brief) {
				blocking = append(blocking, fmt.Sprintf("task %s uses brief schema %d, which the candidate does not support (%v); already-adopted task contracts are never downgraded implicitly", idStr, brief, briefSchema))
			}
		}
		tasks = append(tasks, row)
	}
	currentMCP := asObject(func() any {
		if current == nil {
			return nil
		}
		m := asObject(func() any { v, _ := current.Get("manifest"); return v }())
		_, _, mcpObj, _, _ := offeredContracts(m)
		return mcpObj
	}())
	if currentMCP != nil && mcp != nil {
		curEnc, _ := ordjson.MarshalCompact(currentMCP)
		newEnc, _ := ordjson.MarshalCompact(mcp)
		note := ordjson.NewObject()
		note.Set("what", "mcp")
		if string(curEnc) != string(newEnc) {
			note.Set("from", currentMCP)
			note.Set("to", mcp)
			note.Set("note", "Already-connected MCP clients keep the server and tool set they started; they see the new tools only after the client itself restarts. Nothing is reloaded for them.")
		} else {
			note.Set("note", "Already-running MCP servers keep their start tree until their client restarts; the tool contract is unchanged, so nothing is lost meanwhile.")
		}
		deferred = append(deferred, note)
	}
	headOut, _ := proc.Run([]string{"git", "-C", root, "rev-parse", "HEAD"}, "", 20*time.Second, true, nil)
	head := strings.TrimSpace(headOut.Stdout)
	kind, _ := current.Get("kind")
	curSHA, _ := current.Get("sha")
	if kind == "checkout" || curSHA != head {
		row := ordjson.NewObject()
		row.Set("what", "checkout-instructions")
		row.Set("checkout_head", head)
		row.Set("note", "AGENTS.md and skills read by a plain harness come from the checkout. apply fast-forwards a clean installation clone; this command does not.")
		deferred = append(deferred, row)
	}
	helper := filepath.Join(candidatePath, ".local", "bin", "sumctl")
	if info, err := os.Stat(helper); err != nil || info.Mode()&0o111 == 0 {
		helper = filepath.Join(candidatePath, "bin", "sumctl")
	}
	var probes []any
	if len(blocking) == 0 {
		argvs := [][]string{{"--version"}, {"--home", s.Home, "status"}}
		for _, argv := range argvs {
			cmd := append([]string{helper}, argv...)
			out, runErr := proc.Run(cmd, "", 60*time.Second, false, append(os.Environ(), "SUM_INSTALL_ROOT="+root))
			probe := ordjson.NewObject()
			shown := argv
			if len(argv) > 0 && argv[0] == "--home" && len(argv) >= 3 {
				shown = argv[len(argv)-2:]
			}
			var shownAny []any
			for _, a := range shown {
				shownAny = append(shownAny, a)
			}
			probe.Set("argv", shownAny)
			ok := runErr == nil && out.Code == 0
			probe.Set("ok", ok)
			if ok {
				probe.Set("detail", nil)
			} else {
				detail := strings.TrimSpace(out.Stderr)
				if detail == "" {
					detail = strings.TrimSpace(out.Stdout)
				}
				if len(detail) > 400 {
					detail = detail[len(detail)-400:]
				}
				if runErr != nil && detail == "" {
					detail = runErr.Error()
				}
				probe.Set("detail", detail)
				blocking = append(blocking, fmt.Sprintf("candidate helper failed `%s`: %s", strings.Join(shown, " "), detail))
			}
			probes = append(probes, probe)
		}
	}
	currentSum, currentHerdr, currentMCPObj, _, _ := offeredContracts(asObject(func() any { v, _ := current.Get("manifest"); return v }()))
	candidateObj := ordjson.NewObject()
	candidateObj.Set("sha", candidateSHA)
	candidateObj.Set("sum_version", sumVersion)
	candidateObj.Set("herdr_cli", currentHerdr)
	if mcp != nil {
		candidateObj.Set("mcp", mcp)
	}
	currentObj := ordjson.NewObject()
	currentObj.Set("kind", kind)
	currentObj.Set("sha", curSHA)
	currentObj.Set("sum_version", currentSum)
	currentObj.Set("herdr_cli", currentHerdr)
	if currentMCPObj != nil {
		currentObj.Set("mcp", currentMCPObj)
	}
	result := ordjson.NewObject()
	result.Set("ok", len(blocking) == 0)
	result.Set("blocking", blocking)
	result.Set("deferred", deferred)
	result.Set("probes", probes)
	result.Set("tasks", tasks)
	result.Set("candidate", candidateObj)
	result.Set("current", currentObj)
	return result, nil
}

func postCheck(s *store.Store, root string) *ordjson.Object {
	if TestPostCheck != nil {
		return TestPostCheck(s, root)
	}
	return PostCheck(s, root)
}

func PostCheck(s *store.Store, root string) *ordjson.Object {
	helper := filepath.Join(root, "bin", "sumctl")
	out, err := proc.Run([]string{helper, "--home", s.Home, "status"}, "", 60*time.Second, false, append(os.Environ(), "SUM_INSTALL_ROOT="+root))
	result := ordjson.NewObject()
	ok := err == nil && out.Code == 0
	result.Set("ok", ok)
	if ok {
		result.Set("detail", nil)
	} else {
		detail := strings.TrimSpace(out.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(out.Stdout)
		}
		if err != nil && detail == "" {
			detail = err.Error()
		}
		if len(detail) > 400 {
			detail = detail[len(detail)-400:]
		}
		result.Set("detail", detail)
	}
	return result
}

func activationLock(root string) (func() error, error) {
	path := filepath.Join(root, ".local", "update.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(handle.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		handle.Close()
		return nil, fmt.Errorf("Another update or rollback holds the activation lock; the current selection is unchanged. Retry after it finishes.")
	}
	return func() error {
		_ = syscall.Flock(int(handle.Fd()), syscall.LOCK_UN)
		return handle.Close()
	}, nil
}

func SelectDefault(root, target string) (string, error) {
	link := filepath.Join(root, ".local", "current")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		return "", err
	}
	if target == "" {
		if info, err := os.Lstat(link); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(link); err != nil {
				return "", err
			}
		}
		return "", nil
	}
	relative, err := filepath.Rel(filepath.Dir(link), target)
	if err != nil {
		return "", err
	}
	tmp := filepath.Join(filepath.Dir(link), ".current-tmp")
	_ = os.Remove(tmp)
	if err := os.Symlink(relative, tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return relative, nil
}

func syncInstallationCheckout(root, sha string) *ordjson.Object {
	result := ordjson.NewObject()
	headOut, err := proc.Run([]string{"git", "-C", root, "rev-parse", "HEAD"}, "", 20*time.Second, false, nil)
	head := strings.TrimSpace(headOut.Stdout)
	result.Set("head", head)
	if err != nil || headOut.Code != 0 || head == "" {
		result.Set("result", "refused")
		result.Set("reason", "installation checkout HEAD is unreadable")
		return result
	}
	if sha == "" || sha == head {
		result.Set("result", "already-aligned")
		return result
	}
	branch := defaultBranch(root)
	upstream := "refs/remotes/origin/" + branch
	tipOut, tipErr := proc.Run([]string{"git", "-C", root, "rev-parse", "--verify", upstream}, "", 20*time.Second, false, nil)
	tip := strings.TrimSpace(tipOut.Stdout)
	if tipErr != nil || tipOut.Code != 0 || tip == "" {
		result.Set("result", "refused")
		result.Set("reason", fmt.Sprintf("%s is unknown here", upstream))
		return result
	}
	anc, _ := proc.Run([]string{"git", "-C", root, "merge-base", "--is-ancestor", sha, tip}, "", 20*time.Second, false, nil)
	if anc.Code != 0 {
		result.Set("result", "refused")
		result.Set("reason", fmt.Sprintf("%s is not an ancestor of origin/%s", sha, branch))
		return result
	}
	dirtyOut, _ := proc.Run([]string{"git", "-C", root, "status", "--porcelain", "--untracked-files=no"}, "", 20*time.Second, false, nil)
	if strings.TrimSpace(dirtyOut.Stdout) != "" {
		result.Set("result", "refused")
		result.Set("reason", "checkout has tracked changes")
		return result
	}
	ff, _ := proc.Run([]string{"git", "-C", root, "merge-base", "--is-ancestor", head, sha}, "", 20*time.Second, false, nil)
	if ff.Code != 0 {
		result.Set("result", "refused")
		result.Set("reason", fmt.Sprintf("HEAD %s is not a fast-forward to %s", head, sha))
		return result
	}
	merge, mergeErr := proc.Run([]string{"git", "-C", root, "merge", "--ff-only", sha}, "", 20*time.Second, false, nil)
	if mergeErr != nil || merge.Code != 0 {
		reason := strings.TrimSpace(merge.Stderr)
		if reason == "" {
			reason = strings.TrimSpace(merge.Stdout)
		}
		if reason == "" && mergeErr != nil {
			reason = mergeErr.Error()
		}
		if reason == "" {
			reason = "git merge --ff-only refused"
		}
		result.Set("result", "refused")
		result.Set("reason", reason)
		return result
	}
	newHeadOut, _ := proc.Run([]string{"git", "-C", root, "rev-parse", "HEAD"}, "", 20*time.Second, false, nil)
	result.Set("head", strings.TrimSpace(newHeadOut.Stdout))
	result.Set("result", "fast-forwarded")
	return result
}

func applyCheckoutOutcome(compat, checkout *ordjson.Object) {
	if compat == nil || checkout == nil {
		return
	}
	raw, _ := compat.Get("deferred")
	list, _ := raw.([]any)
	kept := make([]any, 0, len(list))
	for _, item := range list {
		row := asObject(item)
		if row != nil && asString(func() any { v, _ := row.Get("what"); return v }()) == "checkout-instructions" {
			continue
		}
		kept = append(kept, item)
	}
	outcome, _ := checkout.Get("result")
	if asString(outcome) == "refused" {
		head, _ := checkout.Get("head")
		reason, _ := checkout.Get("reason")
		row := ordjson.NewObject()
		row.Set("what", "checkout-instructions")
		row.Set("checkout_head", head)
		row.Set("reason", reason)
		row.Set("note", "AGENTS.md and skills read by a plain harness come from the checkout. The default runtime is the new release. The checkout was not reset, stashed, or force-updated.")
		kept = append(kept, row)
	}
	compat.Set("deferred", kept)
}

func deferredWhats(compat *ordjson.Object) []any {
	var deferredWhat []any
	if compat == nil {
		return deferredWhat
	}
	if d, ok := compat.Get("deferred"); ok {
		if list, isList := d.([]any); isList {
			for _, raw := range list {
				if obj := asObject(raw); obj != nil {
					if w, has := obj.Get("what"); has {
						deferredWhat = append(deferredWhat, w)
					}
				}
			}
		}
	}
	return deferredWhat
}

func updateLog(root string, entry *ordjson.Object) {
	path := filepath.Join(root, ".local", "updates.jsonl")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	entry.Set("at", store.Now())
	encoded, err := ordjson.MarshalCompact(entry)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(encoded, '\n'))
	_ = f.Sync()
}

func readUpdateLog(root string, limit int) []any {
	data, err := os.ReadFile(filepath.Join(root, ".local", "updates.jsonl"))
	if err != nil {
		return []any{}
	}
	var rows []any
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		value, decErr := ordjson.Decode([]byte(line))
		if decErr != nil {
			row := ordjson.NewObject()
			if len(line) > 200 {
				line = line[:200]
			}
			row.Set("unparsed", line)
			rows = append(rows, row)
			continue
		}
		rows = append(rows, value)
	}
	if len(rows) > limit {
		rows = rows[len(rows)-limit:]
	}
	return rows
}

func descriptorsEqual(a, b *ordjson.Object) bool {
	if a == nil || b == nil {
		return a == b
	}
	return asString(func() any { v, _ := a.Get("kind"); return v }()) == asString(func() any { v, _ := b.Get("kind"); return v }()) &&
		asString(func() any { v, _ := a.Get("sha"); return v }()) == asString(func() any { v, _ := b.Get("sha"); return v }()) &&
		asString(func() any { v, _ := a.Get("path"); return v }()) == asString(func() any { v, _ := b.Get("path"); return v }())
}

func activate(s *store.Store, root, target, action string, source *ordjson.Object) (*ordjson.Object, error) {
	unlock, err := activationLock(root)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return activateLocked(s, root, target, action, source)
}

func activateLocked(s *store.Store, root, target, action string, source *ordjson.Object) (*ordjson.Object, error) {
	if err := requireNoPending(s, root); err != nil {
		return nil, err
	}
	current := DefaultRuntime(root)
	state, err := ensureActivationState(s, root, current)
	if err != nil {
		return nil, err
	}
	compat, intended, err := ValidateTarget(s, root, target, current)
	newSHA := asString(func() any {
		if intended != nil {
			v, _ := intended.Get("sha")
			return v
		}
		return filepath.Base(target)
	}())
	if err != nil {
		if compat != nil {
			blocking, _ := compat.Get("blocking")
			log := ordjson.NewObject()
			log.Set("action", action)
			log.Set("result", "refused")
			log.Set("from", func() any { v, _ := current.Get("sha"); return v }())
			log.Set("to", newSHA)
			log.Set("blocking", blocking)
			updateLog(root, log)
			kind, _ := current.Get("kind")
			sha, _ := current.Get("sha")
			return nil, fmt.Errorf("%s refused; the current selection (%v %v) still serves. Exact incompatibilities: %s", action, kind, sha, joinBlocking(blocking))
		}
		return nil, err
	}
	curKind, _ := current.Get("kind")
	curSHA, _ := current.Get("sha")
	wantKind := "release"
	if target == "" {
		wantKind = "checkout"
	}
	if curKind == wantKind && curSHA == newSHA {
		checkout := syncInstallationCheckout(root, newSHA)
		applyCheckoutOutcome(compat, checkout)
		note := "Already the default; nothing changed."
		checkoutResult, _ := checkout.Get("result")
		if asString(checkoutResult) == "fast-forwarded" {
			note = "Already the default runtime; the installation checkout was fast-forwarded to that SHA."
		}
		result := ordjson.NewObject()
		result.Set("action", action)
		result.Set("changed", false)
		result.Set("default", current)
		result.Set("compatibility", compat)
		result.Set("checkout", checkout)
		result.Set("source", source)
		result.Set("note", note)
		return result, nil
	}
	knownGood := asObject(func() any { v, _ := state.Get("known_good"); return v }())
	if _, priorErr := resolveDescriptor(root, knownGood); priorErr != nil {
		return nil, priorErr
	}
	generation, genErr := newGeneration()
	if genErr != nil {
		return nil, genErr
	}
	recovery, recErr := stageRecovery(s, root, generation, knownGood)
	if recErr != nil {
		return nil, recErr
	}
	before := selectionDescriptor(current)
	pending := ordjson.NewObject()
	pending.Set("generation", generation)
	pending.Set("action", action)
	pending.Set("from", knownGood)
	pending.Set("to", intended)
	pending.Set("source", source)
	pending.Set("recovery", recovery)
	pending.Set("status", "prepared")
	state.Set("pending", pending)
	if err := writeActivationState(s, root, state); err != nil {
		return nil, err
	}
	log := ordjson.NewObject()
	log.Set("action", action)
	log.Set("phase", "selecting")
	log.Set("generation", generation)
	log.Set("from", before)
	log.Set("to", intended)
	log.Set("source", source)
	updateLog(root, log)
	if afterPendingWrite != nil {
		if hookErr := afterPendingWrite(); hookErr != nil {
			return nil, hookErr
		}
	}
	if _, err := SelectDefault(root, target); err != nil {
		if _, recErr := recoverPendingLocked(s, root, generation); recErr != nil {
			return nil, fmt.Errorf("%s could not replace the selection: %v. Recovery also failed: %v. Run `%s`; records are untouched.", action, err, recErr, argvJoin(recovery))
		}
		return nil, err
	}
	if afterSelect != nil {
		if hookErr := afterSelect(); hookErr != nil {
			return nil, hookErr
		}
	}
	after := DefaultRuntime(root)
	check := postCheck(s, root)
	if ok, _ := check.Get("ok"); ok != true {
		recovered, recErr := recoverPendingLocked(s, root, generation)
		detail, _ := check.Get("detail")
		if recErr != nil {
			failLog := ordjson.NewObject()
			failLog.Set("action", action)
			failLog.Set("result", "selected-but-entrypoint-check-failed")
			failLog.Set("generation", generation)
			failLog.Set("from", before)
			failLog.Set("to", selectionDescriptor(after))
			failLog.Set("post_check", check)
			failLog.Set("recovery", "failed")
			updateLog(root, failLog)
			return nil, fmt.Errorf("%s switched the default to %v but the entrypoint check failed: %v. Recovery also failed: %v. Run `%s`; records are untouched.", action, func() any { v, _ := after.Get("sha"); return v }(), detail, recErr, argvJoin(recovery))
		}
		restoredSHA := asString(func() any {
			def := asObject(func() any { v, _ := recovered.Get("default"); return v }())
			if def == nil {
				return nil
			}
			v, _ := def.Get("sha")
			return v
		}())
		return nil, fmt.Errorf("%s candidate entrypoint check failed: %v. The prior known-good runtime %s was restored and verified; records are untouched.", action, detail, restoredSHA)
	}
	checkout := syncInstallationCheckout(root, newSHA)
	applyCheckoutOutcome(compat, checkout)
	committed := ordjson.NewObject()
	committed.Set("generation", generation)
	committed.Set("from", before)
	committed.Set("to", selectionDescriptor(after))
	committed.Set("known_good", selectionDescriptor(after))
	committed.Set("pending", nil)
	if err := writeActivationState(s, root, committed); err != nil {
		return nil, fmt.Errorf("%s selected %v but the activation record could not be committed: %v. Run `update recover --generation %s`; records are untouched.", action, func() any { v, _ := after.Get("sha"); return v }(), err, generation)
	}
	okLog := ordjson.NewObject()
	okLog.Set("action", action)
	okLog.Set("result", "selected")
	okLog.Set("generation", generation)
	okLog.Set("from", before)
	okLog.Set("to", selectionDescriptor(after))
	okLog.Set("deferred", deferredWhats(compat))
	okLog.Set("checkout", checkout)
	okLog.Set("post_check", check)
	updateLog(root, okLog)
	result := ordjson.NewObject()
	result.Set("action", action)
	result.Set("changed", true)
	result.Set("previous", before)
	result.Set("default", after)
	result.Set("compatibility", compat)
	result.Set("checkout", checkout)
	result.Set("post_check", check)
	result.Set("source", source)
	result.Set("generation", generation)
	result.Set("recovery", recovery)
	result.Set("note", "New entrypoint invocations and new dispatches use this default. Commands already running finish on the runtime they resolved; connected MCP servers keep their start tree; task records, worktrees, and .sum were not touched.")
	return result, nil
}

func Status(s *store.Store) (*ordjson.Object, error) {
	root, err := release.InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	current := DefaultRuntime(root)
	listed, listErr := release.List(s)
	if listErr != nil {
		return nil, listErr
	}
	headOut, _ := proc.Run([]string{"git", "-C", root, "rev-parse", "HEAD"}, "", 20*time.Second, true, nil)
	dirtyOut, _ := proc.Run([]string{"git", "-C", root, "status", "--porcelain", "--untracked-files=no"}, "", 20*time.Second, true, nil)
	checkout := ordjson.NewObject()
	checkout.Set("head", strings.TrimSpace(headOut.Stdout))
	checkout.Set("dirty", strings.TrimSpace(dirtyOut.Stdout) != "")
	exe, _ := os.Executable()
	activePath := filepath.Dir(filepath.Dir(exe))
	if filepath.Base(filepath.Dir(exe)) == "bin" && filepath.Base(filepath.Dir(filepath.Dir(exe))) == ".local" {
		activePath = filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	} else if filepath.Base(filepath.Dir(exe)) == "bin" {
		activePath = filepath.Dir(filepath.Dir(exe))
	}
	curPath := asString(func() any { v, _ := current.Get("path"); return v }())
	active := ordjson.NewObject()
	active.Set("runtime", activePath)
	active.Set("sum_version", contract.SumVersion)
	active.Set("is_default", activePath == curPath)
	active.Set("note", "The runtime this very command resolved. A command started before a switch keeps its own runtime until it exits.")
	result := ordjson.NewObject()
	result.Set("installation", root)
	result.Set("default", current)
	result.Set("active", active)
	result.Set("checkout", checkout)
	if listed != nil {
		if v, ok := listed.Get("releases"); ok {
			result.Set("releases", v)
		}
		if v, ok := listed.Get("in_progress"); ok {
			result.Set("in_progress", v)
		}
	}
	result.Set("history", readUpdateLog(root, 20))
	activation, actErr := readActivationState(s, root)
	if actErr != nil {
		return nil, actErr
	}
	result.Set("activation", activation)
	result.Set("note", "Selection is the .local/current symlink; the history is a local log, not the source of truth. Running MCP servers and helpers are not enumerated: they keep the tree they started from.")
	return result, nil
}

func Check(s *store.Store, runtimeRoot, ref string, noFetch bool) (*ordjson.Object, error) {
	root, err := release.InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	source, err := resolveAuthorized(root, ref, !noFetch)
	if err != nil {
		return nil, err
	}
	sha := asString(func() any { v, _ := source.Get("sha"); return v }())
	current := DefaultRuntime(root)
	stagedPath := filepath.Join(root, ".local", "releases", sha)
	info, statErr := os.Stat(stagedPath)
	staged := statErr == nil && info.IsDir()
	curKind, _ := current.Get("kind")
	curSHA, _ := current.Get("sha")
	result := ordjson.NewObject()
	result.Set("installation", root)
	result.Set("source", source)
	def := ordjson.NewObject()
	for _, k := range []string{"kind", "sha", "path", "ok", "error"} {
		if v, ok := current.Get(k); ok {
			def.Set(k, v)
		}
	}
	result.Set("default", def)
	active := ordjson.NewObject()
	exe, _ := os.Executable()
	active.Set("runtime", exe)
	result.Set("active", active)
	result.Set("staged", staged)
	result.Set("up_to_date", curSHA == sha && curKind == "release")
	if staged {
		compat, cErr := Compatibility(s, root, stagedPath, current)
		if cErr != nil {
			return nil, cErr
		}
		result.Set("compatibility", compat)
		result.Set("note", "Read-only apart from refs/remotes/origin. Compatibility was evaluated against the staged bundle.")
	} else {
		result.Set("note", "Read-only apart from refs/remotes/origin. Not staged yet: `update stage` builds it without changing the default; `update apply` stages and activates.")
	}
	return result, nil
}

func Stage(s *store.Store, ctx *ordjson.Object, ref string, noFetch bool) (*ordjson.Object, error) {
	root, err := release.InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	source, err := resolveAuthorized(root, ref, !noFetch)
	if err != nil {
		return nil, err
	}
	sha := asString(func() any { v, _ := source.Get("sha"); return v }())
	staged, err := release.Stage(s, sha)
	if err != nil {
		return nil, err
	}
	current := DefaultRuntime(root)
	relPath := asString(func() any { v, _ := staged.Get("release"); return v }())
	unlock, lockErr := activationLock(root)
	if lockErr != nil {
		return nil, lockErr
	}
	if err := approveUpdateTarget(s, root, relPath, nil); err != nil {
		_ = unlock()
		return nil, err
	}
	if err := unlock(); err != nil {
		return nil, err
	}
	compat, err := Compatibility(s, root, relPath, current)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	for _, k := range staged.Keys() {
		v, _ := staged.Get(k)
		result.Set(k, v)
	}
	result.Set("source", source)
	result.Set("compatibility", compat)
	result.Set("note", "Staged and evaluated; the default is unchanged. `update apply` activates it.")
	return result, nil
}

func Apply(s *store.Store, ctx *ordjson.Object, ref string, noFetch bool) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	root, err := release.InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	if err := requireNoPending(s, root); err != nil {
		return nil, err
	}
	source, err := resolveAuthorized(root, ref, !noFetch)
	if err != nil {
		return nil, err
	}
	sha := asString(func() any { v, _ := source.Get("sha"); return v }())
	staged, err := release.Stage(s, sha)
	if err != nil {
		return nil, err
	}
	relPath := asString(func() any { v, _ := staged.Get("release"); return v }())
	src := ordjson.NewObject()
	for _, k := range []string{"sha", "ref", "origin", "branch", "fetched"} {
		if v, ok := source.Get(k); ok {
			src.Set(k, v)
		}
	}
	return activate(s, root, relPath, "apply", src)
}

func Rollback(s *store.Store, ctx *ordjson.Object, to string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	root, err := release.InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	unlock, lockErr := activationLock(root)
	if lockErr != nil {
		return nil, lockErr
	}
	defer unlock()
	if err := requireNoPending(s, root); err != nil {
		return nil, err
	}
	current := DefaultRuntime(root)
	state, err := ensureActivationState(s, root, current)
	if err != nil {
		return nil, err
	}
	target, src, err := resolveRollbackTarget(root, to, state, current)
	if err != nil {
		return nil, err
	}
	return activateLocked(s, root, target, "rollback", src)
}

func resolveRollbackTarget(root, to string, state, current *ordjson.Object) (string, *ordjson.Object, error) {
	src := ordjson.NewObject()
	requested := to
	if requested == "" {
		previous := asObject(func() any { v, _ := state.Get("from"); return v }())
		if previous == nil || descriptorsEqual(previous, selectionDescriptor(current)) {
			return "", nil, fmt.Errorf("No recorded previous known-good selection. Name the target: `update rollback --to SHA` or `--to checkout`.")
		}
		if asString(func() any { v, _ := previous.Get("kind"); return v }()) == "checkout" {
			requested = "checkout"
		} else {
			requested = asString(func() any { v, _ := previous.Get("sha"); return v }())
		}
	}
	if requested == "checkout" {
		src.Set("to", "checkout")
		return "", src, nil
	}
	if !shaPrefix.MatchString(requested) {
		return "", nil, fmt.Errorf("Roll back to a staged release SHA or `checkout`.")
	}
	releases := filepath.Join(root, ".local", "releases")
	entries, _ := os.ReadDir(releases)
	var matches []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if strings.HasPrefix(e.Name(), requested) {
			matches = append(matches, filepath.Join(releases, e.Name()))
		}
	}
	if len(matches) != 1 {
		return "", nil, fmt.Errorf("%d staged releases match %s; rollback uses only bundles that are already staged.", len(matches), requested)
	}
	src.Set("to", filepath.Base(matches[0]))
	return matches[0], src, nil
}

func Recover(s *store.Store, ctx *ordjson.Object, generation string) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	root, err := release.InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	unlock, lockErr := activationLock(root)
	if lockErr != nil {
		return nil, lockErr
	}
	defer unlock()
	return recoverPendingLocked(s, root, generation)
}

// Test seams for crash injection. Production leaves these nil.
var afterPendingWrite func() error
var afterSelect func() error

// TestPostCheck, if set, replaces PostCheck. Tests only.
var TestPostCheck func(s *store.Store, root string) *ordjson.Object

// TestRecoverPending, if set, replaces recoverPendingLocked. Tests only.
var TestRecoverPending func(s *store.Store, root, generation string) (*ordjson.Object, error)
