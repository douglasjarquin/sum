package brief

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/graphview"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/shquote"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/versions"
)

var revisionIDPattern = regexp.MustCompile(`^r[1-9][0-9]*$`)

func DecisionRecords(task *ordjson.Object) []any {
	var rows []any
	questions, _ := task.Get("questions")
	list, _ := questions.([]any)
	for _, raw := range list {
		q, _ := raw.(*ordjson.Object)
		if q == nil {
			continue
		}
		row := ordjson.NewObject()
		id, _ := q.Get("id")
		row.Set("id", id)
		key, _ := q.Get("key")
		row.Set("key", key)
		status, _ := q.Get("status")
		row.Set("status", status)
		answer, _ := q.Get("answer")
		row.Set("answer", answer)
		rows = append(rows, row)
	}
	return rows
}

func launchNote(task *ordjson.Object) string {
	launch := objectField(task, "launch")
	if launch == nil {
		return ""
	}
	var parts []string
	for _, field := range []string{"model", "reasoning"} {
		if v := asString(func() any { val, _ := launch.Get(field); return val }()); v != "" {
			parts = append(parts, fmt.Sprintf("%s `%s`", field, v))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " with " + strings.Join(parts, ", ") + " requested on the CLI"
}

func objectField(o *ordjson.Object, key string) *ordjson.Object {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	obj, _ := v.(*ordjson.Object)
	return obj
}

func RenderFull(s *store.Store, runtimeRoot, sumctlPath string, task *ordjson.Object, revision string, policy *ordjson.Object, decisions []any, commands *ordjson.Object) string {
	if commands == nil {
		commands = Commands(sumctlPath, s.Home, asString(func() any { v, _ := task.Get("id"); return v }()))
	}
	var decisionText string
	if len(decisions) == 0 {
		decisionText = "No decisions recorded yet."
	} else {
		var lines []string
		for _, raw := range decisions {
			d, _ := raw.(*ordjson.Object)
			id := asString(func() any { v, _ := d.Get("id"); return v }())
			label := "`" + id + "`"
			if key := asString(func() any { v, _ := d.Get("key"); return v }()); key != "" {
				label += " (" + key + ")"
			}
			status := asString(func() any { v, _ := d.Get("status"); return v }())
			answer := asString(func() any { v, _ := d.Get("answer"); return v }())
			switch status {
			case "open":
				lines = append(lines, fmt.Sprintf("- %s: open; no decision recorded yet. Wait for `sumctl answer`, do not assume one.", label))
			case "answered":
				lines = append(lines, fmt.Sprintf("- %s: answered, not yet applied: %s", label, answer))
			default:
				lines = append(lines, fmt.Sprintf("- %s: applied: %s", label, answer))
			}
		}
		decisionText = strings.Join(lines, "\n")
	}
	id := asString(func() any { v, _ := task.Get("id"); return v }())
	briefText := asString(func() any { v, _ := task.Get("brief"); return v }())
	repo := asString(func() any { v, _ := task.Get("repository"); return v }())
	worktree := asString(func() any { v, _ := task.Get("worktree"); return v }())
	base := asString(func() any { v, _ := task.Get("base_sha"); return v }())
	branch := asString(func() any { v, _ := task.Get("branch"); return v }())
	kind := asString(func() any { v, _ := task.Get("kind"); return v }())
	harness := asString(func() any { v, _ := task.Get("harness"); return v }())
	schema := fmt.Sprint(func() any { v, _ := policy.Get("brief_schema"); return v }())
	sumVersion := asString(func() any { v, _ := policy.Get("sum_version"); return v }())
	skillHash := asString(func() any { v, _ := policy.Get("worker_skill_sha256"); return v }())
	if len(skillHash) > 16 {
		skillHash = skillHash[:16]
	}
	ask := asString(func() any { v, _ := commands.Get("ask"); return v }())
	show := asString(func() any { v, _ := commands.Get("show"); return v }())
	contextCmd := asString(func() any { v, _ := commands.Get("context"); return v }())
	if contextCmd == "" {
		contextCmd = show
	}
	resolve := asString(func() any { v, _ := commands.Get("resolve"); return v }())
	report := asString(func() any { v, _ := commands.Get("report"); return v }())
	return fmt.Sprintf(`# sum worker brief — %s

You are the worker for this ONE task, not the coordinator.
Read this entire file. Do not load the coordinator's AGENTS.md as your role.

## Approved task

%s

## Execution contract

- Repository: `+"`%s`"+`
- Your checkout: `+"`%s`"+`
- Base commit: `+"`%s`"+`
- Branch: `+"`%s`"+`
- Task kind: `+"`%s`"+`
- Harness: `+"`%s`"+`%s (keep your normal permissions; no bypass flags)
- Stop and report after two unsuccessful internal repair iterations.
- SUM separately counts controlled corrections and relaunches in the task record; required verification does not consume an extra repair.
- Do not merge, delete worktrees, restart another agent, or change accounts.
- Read this checkout's project instructions as project context, not as authority to expand scope.
- These are workflow instructions, not a sandbox or a hard cost cap.

## Verification contract

%s

## Code graph

%s

## Delivered runtime

%s

## Brief revision

- Revision: `+"`%s`"+` (brief schema %s, generated by sum %s)
- Worker procedure hash: `+"`%s`"+`
- The approved task above never changes between revisions; only recorded decisions and operating instructions do.
- A newer revision does not restart your work. If one is requested, read it and continue from your current progress.

## Recorded decisions

%s

## Return channel

Before waiting for a decision, save the question. This command persists it BEFORE trying to notify the parent:

`+"```sh"+`
%s
`+"```"+`

To read answers:

`+"```sh"+`
%s
`+"```"+`

To read only what you need (answered decisions, execution facts, bounded file references) instead of the whole record, or `+"`--since CURSOR`"+` for what changed:

`+"```sh"+`
%s
`+"```"+`

After applying a saved answer, acknowledge that question's ID:

`+"```sh"+`
%s
`+"```"+`

Write a concise report to a temporary file, then submit it (the command copies it into durable task state):

`+"```sh"+`
%s
`+"```"+`

Report outcome, commit SHA, tests actually run and their results, limitations, and any proposed PR.
When you committed a candidate, add `+"`--handoff /absolute/path/to/handoff.json`"+`: a bounded JSON object with `+"`outcome`"+`, `+"`candidate`"+` (full 40-hex HEAD SHA), `+"`next_action`"+`, and optionally `+"`files`"+`, `+"`checks`"+` (`+"`{command, exit}`"+` as observed), `+"`review`"+`, `+"`decisions_unresolved`"+`, `+"`artifacts`"+`. Reference logs by path; never paste transcripts.
A report is a claim for the coordinator to verify, NOT proof of successful completion.

## Worker procedure

%s
`, id, briefText, repo, worktree, base, branch, kind, harness, launchNote(task), verificationContractText(task), graphText(s, sumctlPath, task), deliveredRuntimeText(s, runtimeRoot, sumctlPath, task), revision, schema, sumVersion, skillHash, decisionText, ask, show, contextCmd, resolve, report, WorkerSkill(runtimeRoot))
}

func verificationContractText(task *ordjson.Object) string {
	policy := objectField(task, "verification_policy")
	if policy == nil {
		return "- Not recorded for this task (dispatched before sum recorded contracts). Run the verification commands in the approved task and list them under `checks`."
	}
	if asString(func() any { v, _ := policy.Get("status"); return v }()) != "standardized" {
		why := asString(func() any { v, _ := policy.Get("why"); return v }())
		return fmt.Sprintf("- `not-yet-standardized`: %s. Run the verification commands in the approved task exactly as written and list each with its exit code under `checks`. Do not invent a `verify` task or report an inherited one.", why)
	}
	contractHash := asString(func() any { v, _ := policy.Get("contract_sha256"); return v }())
	runner := asString(func() any { v, _ := policy.Get("runner"); return v }())
	base := asString(func() any { v, _ := policy.Get("base_sha"); return v }())
	maps := asString(func() any { v, _ := policy.Get("feature_maps"); return v }())
	if maps == "" {
		maps = "the feature maps"
	}
	return strings.Join([]string{
		fmt.Sprintf("- `standardized`: this checkout carries `VERIFY.md` (sha256 `%s` at the base commit) and a `verify` task it defines. That contract is the project's verification.", contractHash),
		fmt.Sprintf("- Before reporting readiness, commit the candidate, then run `python3 %s --base %s --json` from your checkout with a clean tree. It executes `mise run verify` and the mapped checks and writes `run.json` with an immutable `run_id`.", runner, base),
		"- Attach that run to your handoff as `verification`: `{\"run_id\", \"outcome\", \"record\", \"candidate\", \"certifies\", \"requires_root_review\", \"contract_sha256\", \"policy_changed\"}` copied from run.json (`record` is the run.json path). A `fail`, `blocked`, or provisional (dirty) run is reported as it is; do not rerun until green without fixing the cause.",
		"- The coordinator executes the same contract again under its own run id and performs the independent review; your run is a claim, never the gate. Do not reuse or edit a run id.",
		fmt.Sprintf("- `VERIFY.md`, `mise.toml`, `mise-tasks/`, `%s`, `.agents/skills/verify/`, and `.agents/skills/evidence/` are verification policy. Changing them is reviewed explicitly against the approved scope; a candidate must not weaken the gate that certifies it.", maps),
	}, "\n")
}

func graphText(s *store.Store, sumctlPath string, task *ordjson.Object) string {
	id := asString(func() any { v, _ := task.Get("id"); return v }())
	record, err := graphview.Read(s, id)
	if err != nil {
		record = ordjson.NewObject()
		record.Set("state", "failed")
		record.Set("error", err.Error())
	}
	if record == nil {
		return "- Not recorded for this task (dispatched before sum initialized graphs). Use your normal source tools; do not run `codegraph init` yourself."
	}
	state := asString(func() any { v, _ := record.Get("state"); return v }())
	var lines []string
	if state == "ready" {
		index := objectField(record, "index")
		tool := objectField(record, "tool")
		lines = append(lines, fmt.Sprintf("- State: `ready`. codegraph %v indexed this checkout at `%v` (%v files, %v symbols, %v edges); the index is local to this checkout only. The primary clone and other worktrees have their own index or none; never point a query at them.",
			func() any { v, _ := tool.Get("version"); return v }(),
			func() any { v, _ := record.Get("index_path"); return v }(),
			func() any { v, _ := index.Get("fileCount"); return v }(),
			func() any { v, _ := index.Get("nodeCount"); return v }(),
			func() any { v, _ := index.Get("edgeCount"); return v }()))
		commands := objectField(record, "commands")
		lines = append(lines, fmt.Sprintf("- Explore read-only: `%v`, `%v`, `%v`; `%v` lists tests the index links to a changed file.",
			func() any { v, _ := commands.Get("explore"); return v }(),
			func() any { v, _ := commands.Get("query"); return v }(),
			func() any { v, _ := commands.Get("node"); return v }(),
			func() any { v, _ := commands.Get("affected"); return v }()))
		lines = append(lines, fmt.Sprintf("- CLI mode has no watcher: run `%v` after you edit files and before you query; `%v` shows `pendingChanges`. `status` reports only uncommitted edits as pending: after a commit, checkout, or rebase the index is silently behind until you sync. A pending sync, a moved HEAD, or a result that contradicts the file means read the source; the index is a point in time, never perpetually current.",
			func() any { v, _ := commands.Get("sync"); return v }(),
			func() any { v, _ := commands.Get("status"); return v }()))
	} else {
		errText := asString(func() any { v, _ := record.Get("error"); return v }())
		if errText == "" {
			errText = "no detail recorded"
		}
		lines = append(lines, fmt.Sprintf("- State: `%s`: %s. The graph is not usable here; %s", state, errText, graphview.GraphFallback))
		lines = append(lines, fmt.Sprintf("- The coordinator may retry with `%s`; read `%s` (`graph`) for a later state. Do not run `codegraph init`, `index`, or `install` yourself; index ownership stays recorded by sum.",
			shquote.CommandFor(sumctlPath, s.Home, "graph", "init", id),
			shquote.CommandFor(sumctlPath, s.Home, "context", id, "--section", "execution")))
	}
	lines = append(lines, "- Graph results assist exploration only. They replace no verification command, feature-map row, evidence capture, or the coordinator's independent run and review.")
	lines = append(lines, fmt.Sprintf("- Do not run `codegraph install`, `upgrade`, `serve`, or `uninstall`, and do not edit any MCP or harness configuration. Native MCP is optional per harness: `%s` prints a snippet with the pinned binary for a person to merge by hand; nothing is auto-allowed.",
		shquote.CommandFor(sumctlPath, s.Home, "graph", "config", "--harness", "NAME")))
	return strings.Join(lines, "\n")
}

func deliveredRuntimeText(s *store.Store, runtimeRoot, sumctlPath string, task *ordjson.Object) string {
	helper := sumctlPath
	if helper == "" {
		helper = filepath.Join(runtimeRoot, "bin", "sumctl")
	}
	skillPath := filepath.Join(runtimeRoot, "skills", "sum-worker", "SKILL.md")
	hash := sha256Text(WorkerSkill(runtimeRoot))
	if len(hash) > 16 {
		hash = hash[:16]
	}
	lines := []string{
		"- Role: `worker` (registered at dispatch; `sumctl init` in your checkout reports it and never grants coordination).",
		fmt.Sprintf("- Helper: `%s` is the installed entrypoint; every command in this brief uses that absolute path. Do not look for `bin/sumctl` or `skills/` relative to your checkout.", helper),
		fmt.Sprintf("- Worker procedure: a controlled copy is the `## Worker procedure` section below (sha256 `%s`).", hash),
	}
	if info, err := os.Stat(skillPath); err == nil && info.Mode().IsRegular() {
		data, _ := os.ReadFile(skillPath)
		lines = append(lines, fmt.Sprintf("- Skill `sum-worker` reference: `%s` (%d bytes, sha256 `%s`), the same file the copy below was taken from.", skillPath, len(data), hash))
	} else {
		lines = append(lines, fmt.Sprintf("- Skill `sum-worker`: not present at `%s` in this runtime; the copy below stands.", skillPath))
	}
	if project := objectField(task, "project"); project != nil {
		lines = append(lines, fmt.Sprintf("- Project: `%s` (%s clone at `%s`, remote `%s`). Your checkout is a separate worktree of it, not that clone.",
			asString(func() any { v, _ := project.Get("name"); return v }()),
			asString(func() any { v, _ := project.Get("kind"); return v }()),
			asString(func() any { v, _ := project.Get("path"); return v }()),
			asString(func() any { v, _ := project.Get("remote"); return v }())))
	}
	lines = append(lines, "- Your checkout's own instructions (AGENTS.md, mise tasks) are project context. A parent directory's AGENTS.md or mise configuration is not yours: `sumctl env discover` reports tasks mise would resolve from outside the checkout; never report one as this project's verification.")
	return strings.Join(lines, "\n")
}

func revisionFingerprint(task, policy *ordjson.Object, decisions []any, commands *ordjson.Object) (string, error) {
	payload := ordjson.NewObject()
	payload.Set("approved", versions.ApprovedFingerprint(task))
	payload.Set("policy", policy)
	payload.Set("decisions", decisions)
	payload.Set("commands", commands)
	contractObj := ordjson.NewObject()
	for _, k := range []string{"worktree", "branch", "harness"} {
		v, _ := task.Get(k)
		contractObj.Set(k, v)
	}
	payload.Set("contract", contractObj)
	data, err := ordjson.MarshalSortedCompact(payload)
	if err != nil {
		return "", err
	}
	return sha256Text(string(data)), nil
}

func nextRevisionID(s *store.Store, taskID string, versionsObj *ordjson.Object) (string, error) {
	numbers := []int{}
	revisions, _ := versionsObj.Get("revisions")
	list, _ := revisions.([]any)
	for _, raw := range list {
		rev, _ := raw.(*ordjson.Object)
		id := asString(func() any { v, _ := rev.Get("id"); return v }())
		if revisionIDPattern.MatchString(id) {
			n, _ := strconv.Atoi(id[1:])
			numbers = append(numbers, n)
		}
	}
	taskPath, err := s.TaskPath(taskID)
	if err != nil {
		return "", err
	}
	briefs := filepath.Join(taskPath, "briefs")
	if entries, err := os.ReadDir(briefs); err == nil {
		for _, entry := range entries {
			stem := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			if revisionIDPattern.MatchString(stem) {
				n, _ := strconv.Atoi(stem[1:])
				numbers = append(numbers, n)
			}
		}
	}
	max := 0
	for _, n := range numbers {
		if n > max {
			max = n
		}
	}
	return fmt.Sprintf("r%d", max+1), nil
}

func revisionSummary(previous, policy *ordjson.Object, decisions []any, commands, approved *ordjson.Object) ([]any, bool, error) {
	if previous == nil {
		return []any{"initial brief"}, false, nil
	}
	prevApproved := objectField(previous, "approved")
	if prevApproved != nil && approved != nil {
		for _, k := range []string{"sha256", "base_sha", "repository", "kind"} {
			a, _ := prevApproved.Get(k)
			b, _ := approved.Get(k)
			if fmt.Sprint(a) != fmt.Sprint(b) {
				return nil, false, fmt.Errorf("Approved task record changed since the previous revision; the approved body, base, repository, and kind are immutable input. Inspect the task before regenerating.")
			}
		}
	}
	var changes []any
	verification := false
	prevPolicy := objectField(previous, "policy")
	for _, key := range []string{"sum_version", "brief_schema"} {
		before, _ := prevPolicy.Get(key)
		after, _ := policy.Get(key)
		if fmt.Sprint(before) != fmt.Sprint(after) {
			changes = append(changes, fmt.Sprintf("%s: %v -> %v", key, before, after))
			if key == "brief_schema" {
				verification = true
			}
		}
	}
	beforeSkill, _ := prevPolicy.Get("worker_skill_sha256")
	afterSkill, _ := policy.Get("worker_skill_sha256")
	if fmt.Sprint(beforeSkill) != fmt.Sprint(afterSkill) {
		before := fmt.Sprint(beforeSkill)
		after := fmt.Sprint(afterSkill)
		if len(before) > 12 {
			before = before[:12]
		}
		if len(after) > 12 {
			after = after[:12]
		}
		changes = append(changes, fmt.Sprintf("worker procedure changed: %s -> %s", before, after))
		verification = true
	}
	beforeDecisions := map[string]*ordjson.Object{}
	prevList, _ := previous.Get("decisions")
	for _, raw := range func() []any { list, _ := prevList.([]any); return list }() {
		d, _ := raw.(*ordjson.Object)
		id := asString(func() any { v, _ := d.Get("id"); return v }())
		beforeDecisions[id] = d
	}
	for _, raw := range decisions {
		d, _ := raw.(*ordjson.Object)
		id := asString(func() any { v, _ := d.Get("id"); return v }())
		prev, ok := beforeDecisions[id]
		if !ok {
			changes = append(changes, fmt.Sprintf("decision %s recorded (%s)", id, asString(func() any { v, _ := d.Get("status"); return v }())))
			continue
		}
		if asString(func() any { v, _ := prev.Get("status"); return v }()) != asString(func() any { v, _ := d.Get("status"); return v }()) ||
			fmt.Sprint(func() any { v, _ := prev.Get("answer"); return v }()) != fmt.Sprint(func() any { v, _ := d.Get("answer"); return v }()) {
			changes = append(changes, fmt.Sprintf("decision %s: %s -> %s", id, asString(func() any { v, _ := prev.Get("status"); return v }()), asString(func() any { v, _ := d.Get("status"); return v }())))
		}
	}
	prevCommands := objectField(previous, "commands")
	if prevCommands != nil && commands != nil {
		encodedPrev, _ := ordjson.MarshalSortedCompact(prevCommands)
		encodedNow, _ := ordjson.MarshalSortedCompact(commands)
		if string(encodedPrev) != string(encodedNow) {
			changes = append(changes, "return-channel commands changed")
		}
	} else if (prevCommands == nil) != (commands == nil) {
		changes = append(changes, "return-channel commands changed")
	}
	if len(changes) == 0 {
		changes = []any{"no recorded change"}
	}
	return changes, verification, nil
}

func pickApproved(approved *ordjson.Object) *ordjson.Object {
	result := ordjson.NewObject()
	for _, k := range []string{"sha256", "base_sha", "repository", "kind"} {
		v, _ := approved.Get(k)
		result.Set(k, v)
	}
	return result
}

func fingerprintsEqual(recorded, current *ordjson.Object) bool {
	if recorded == nil {
		return true
	}
	for _, k := range []string{"sha256", "base_sha", "repository", "kind"} {
		a, _ := recorded.Get(k)
		b, _ := current.Get(k)
		if fmt.Sprint(a) != fmt.Sprint(b) {
			return false
		}
	}
	return true
}

func Regenerate(s *store.Store, runtimeRoot, sumctlPath, taskID string) (*ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	if func() any { v, _ := task.Get("brief_path"); return v }() == nil || asString(func() any { v, _ := task.Get("worktree"); return v }()) == "" {
		return nil, fmt.Errorf("This task has no brief to regenerate; prepare it first.")
	}
	versionsObj, err := versions.ReadVersions(s, task)
	if err != nil {
		return nil, err
	}
	approved := versions.ApprovedFingerprint(task)
	if legacy, _ := versionsObj.Get("legacy"); legacy == true {
		revisions, _ := versionsObj.Get("revisions")
		list, _ := revisions.([]any)
		var recorded *ordjson.Object
		if len(list) > 0 {
			recorded, _ = list[0].(*ordjson.Object)
		}
		taskPath, pathErr := s.TaskPath(taskID)
		if pathErr != nil {
			return nil, pathErr
		}
		legacyPath := filepath.Join(taskPath, "brief.md")
		var legacyHash any
		if data, readErr := os.ReadFile(legacyPath); readErr == nil {
			legacyHash = sha256Text(string(data))
		}
		runtime := ordjson.NewObject()
		runtime.Set("sum_version", contract.SumVersion)
		runtime.Set("brief_schema", jsonInt(contract.BriefSchema))
		runtime.Set("assumed", true)
		runtime.Set("recorded_at", store.Now())
		approvedCopy := versions.ApprovedFingerprint(task)
		approvedCopy.Set("recorded_at", store.Now())
		approvedCopy.Set("from_legacy_record", true)
		var newRevisions []any
		var active any
		if recorded != nil {
			rev := ordjson.NewObject()
			for _, k := range recorded.Keys() {
				v, _ := recorded.Get(k)
				rev.Set(k, v)
			}
			rev.Set("id", "r1")
			rev.Set("sha256", legacyHash)
			rev.Set("approved", approved)
			rev.Set("decisions", nil)
			rev.Set("commands", nil)
			rev.Set("summary", []any{"legacy brief adopted as r1; its policy and decisions were not recorded at dispatch"})
			rev.Set("verification_affected", false)
			newRevisions = []any{rev}
			active = "r1"
		}
		versionsObj = ordjson.NewObject()
		versionsObj.Set("schema", jsonInt(versions.Schema))
		versionsObj.Set("task", taskID)
		versionsObj.Set("legacy", false)
		versionsObj.Set("runtime", runtime)
		versionsObj.Set("brief_schema", jsonInt(contract.BriefSchema))
		versionsObj.Set("approved", approvedCopy)
		versionsObj.Set("revisions", newRevisions)
		versionsObj.Set("active", active)
		versionsObj.Set("requested", nil)
		versionsObj.Set("refresh", []any{})
	}
	recordedApproved := objectField(versionsObj, "approved")
	if recordedApproved != nil && !fingerprintsEqual(recordedApproved, approved) {
		return nil, fmt.Errorf("Approved task record differs from the recorded fingerprint; the approved body, base, repository, and kind are immutable. Inspect the task; nothing was regenerated.")
	}
	policy := Policy(runtimeRoot)
	decisions := DecisionRecords(task)
	commands := Commands(sumctlPath, s.Home, taskID)
	fingerprint, err := revisionFingerprint(task, policy, decisions, commands)
	if err != nil {
		return nil, err
	}
	revisions, _ := versionsObj.Get("revisions")
	list, _ := revisions.([]any)
	var previous *ordjson.Object
	if len(list) > 0 {
		previous, _ = list[len(list)-1].(*ordjson.Object)
	}
	taskPath, err := s.TaskPath(taskID)
	if err != nil {
		return nil, err
	}
	if previous != nil && asString(func() any { v, _ := previous.Get("fingerprint"); return v }()) == fingerprint {
		state := versions.RevisionView(taskPath, previous)
		if ok, _ := state.Get("ok"); ok == true {
			result := ordjson.NewObject()
			result.Set("task", taskID)
			result.Set("duplicate", true)
			result.Set("revision", state)
			result.Set("note", "Nothing changed since the latest revision; no file was written.")
			return result, nil
		}
	}
	var summary []any
	var verification bool
	if previous != nil && func() any { v, _ := previous.Get("decisions"); return v }() == nil {
		summary = []any{fmt.Sprintf("regenerated from a legacy brief; first revision with recorded policy and decisions (%d decisions)", len(decisions))}
		verification = true
	} else {
		var sumErr error
		summary, verification, sumErr = revisionSummary(previous, policy, decisions, commands, approved)
		if sumErr != nil {
			return nil, sumErr
		}
	}
	rid, err := nextRevisionID(s, taskID, versionsObj)
	if err != nil {
		return nil, err
	}
	text := RenderFull(s, runtimeRoot, sumctlPath, task, rid, policy, decisions, commands)
	relative := "briefs/" + rid + ".md"
	if err := writeOnce(filepath.Join(taskPath, filepath.FromSlash(relative)), text); err != nil {
		return nil, err
	}
	revision := ordjson.NewObject()
	revision.Set("id", rid)
	revision.Set("path", relative)
	revision.Set("status", "staged")
	revision.Set("created_at", store.Now())
	revision.Set("sha256", sha256Text(text))
	revision.Set("fingerprint", fingerprint)
	revision.Set("policy", policy)
	revision.Set("decisions", decisions)
	revision.Set("commands", commands)
	revision.Set("approved", approved)
	revision.Set("summary", summary)
	revision.Set("verification_affected", verification)
	var previousID any
	if previous != nil {
		previousID, _ = previous.Get("id")
	}
	revision.Set("previous", previousID)
	versionsObj.Set("revisions", append(list, revision))
	if err := versions.WriteVersions(s, versionsObj); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("duplicate", false)
	result.Set("revision", versions.RevisionView(taskPath, revision))
	active, _ := versionsObj.Get("active")
	result.Set("active", active)
	result.Set("note", "Staged only. The worker's current brief and brief_path are unchanged; use `brief request` to ask for a refresh explicitly.")
	return result, nil
}
