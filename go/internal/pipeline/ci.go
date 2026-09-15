package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/settings"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

// CI is observed, never watched. sum runs no daemon and polls nothing, so the checks are read whenever sum already
// talks to GitHub about the PR, and every row says when that was.
const (
	ciGate       = "ci"
	ciUnobserved = "Not observed; `pr reconcile` or `pipeline ci` reads the checks"
	ciFields     = "name,state,bucket,link,workflow,startedAt,completedAt"
	ciNoRequired = "; GitHub reports no required checks"
)

var ciOutcomes = map[string]Status{
	"pass":             Pass,
	"fail":             Fail,
	"pending":          Pending,
	"not-declared":     NotDeclared,
	outcomeUnavailable: Blocked,
}

// check is one GitHub check as this gate records it. bucket is GitHub's own classification of state and is what the
// outcome is decided from; state is kept verbatim so a row can quote what GitHub actually said.
type check struct {
	Name     string `json:"name"`
	Bucket   string `json:"bucket"`
	State    string `json:"state"`
	Link     string `json:"link"`
	Workflow string `json:"workflow"`
}

// buckets is GitHub's documented set, in the order the counts are reported.
var buckets = []string{"pass", "fail", "pending", "skipping", "cancel"}

// severity ranks the buckets so the outcome is the worst bucket present, read from one table instead of a chain of
// conditions. A bucket this release does not know ranks with a failure, never a silent pass.
var severity = map[string]int{"skipping": 0, "pass": 1, "pending": 2, "cancel": 3, "fail": 3}

var headlines = map[string]string{"fail": "Failed", "cancel": "Cancelled"}

type CIArgs struct {
	Task      string
	NoPublish bool
	Timeout   int
}

// CI re-observes the checks on a task's reconciled PR without a full reconcile, and republishes the table.
func CI(s *store.Store, ctx *ordjson.Object, runtimeRoot string, args CIArgs) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	row, err := ObserveCI(s, ctx, runtimeRoot, args.Task, nil, args.Timeout)
	if err != nil {
		return nil, err
	}
	record, err := Load(s, args.Task)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", args.Task)
	result.Set("ci", row)
	result.Set("publication", republish(s, ctx, runtimeRoot, args))
	result.Set("pipeline", View(record))
	result.Set("note", "The checks as GitHub reported them at this instant. A green row means green at the last observation; nothing watches them afterwards.")
	return result, nil
}

// ObserveCI records one CI observation for the task's reconciled PR and returns the row its callers print.
// rollup is the statusCheckRollup a caller has already read, used only when the installed gh cannot answer
// `pr checks --json`; pass nil to let the fallback read it for itself.
func ObserveCI(s *store.Store, ctx *ordjson.Object, runtimeRoot, taskID string, rollup []any, timeout int) (*ordjson.Object, error) {
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	dest, err := destination(task)
	if err != nil {
		return nil, err
	}
	gh, err := toolpath.Find(runtimeRoot, "gh")
	if err != nil {
		return nil, err
	}
	body, summary := observeChecks(gh, stringField(task, "repository"), dest, timeout, rollup)
	result, err := recordGate(s, ctx, taskID, ciGate, dest.HeadSHA, body, summary)
	if err != nil {
		return nil, err
	}
	row := ordjson.NewObject()
	row.Set("gate", string(StageCI))
	for _, key := range []string{"outcome", "summary", "observed_at", "head_sha", "scope", "required_only", "counts"} {
		value, _ := body.Get(key)
		row.Set(key, value)
	}
	evidence, _ := result.Get("evidence")
	if record, isObject := evidence.(*ordjson.Object); isObject {
		id, _ := record.Get("id")
		row.Set("evidence", id)
	}
	return row, nil
}

// observeChecks reads the PR's checks once, preferring the required ones, and writes both the record body and the
// sentence its row shows. It never returns an error: an unreadable check list is an `unavailable` observation.
func observeChecks(gh, repoDir string, dest Destination, timeout int, rollup []any) (*ordjson.Object, string) {
	observedAt := store.Now()
	body := ordjson.NewObject()
	body.Set("observed_at", observedAt)
	body.Set("head_sha", dest.HeadSHA)

	checks, requiredOnly, reason := readChecks(gh, repoDir, dest, timeout, rollup)
	scope := "all"
	if requiredOnly {
		scope = "required"
	}
	body.Set("scope", scope)
	body.Set("required_only", requiredOnly)
	body.Set("checks", checkRows(checks))
	body.Set("counts", countRows(checks))
	if reason != "" {
		body.Set("outcome", outcomeUnavailable)
		return body, fmt.Sprintf("Could not read the checks: %s (observed %s)", reason, observedStamp(observedAt))
	}
	if len(checks) == 0 {
		body.Set("outcome", "not-declared")
		return body, fmt.Sprintf("No checks reported for this PR (observed %s)", observedStamp(observedAt))
	}
	outcome := ciOutcome(checks)
	body.Set("outcome", outcome)
	return body, ciSummary(outcome, checks, requiredOnly, observedAt)
}

// readChecks prefers the required checks, which is the scope GitHub itself gates a merge on. A repository that marks
// none falls back to every check, and the row says so rather than reading as a narrower pass than it is.
func readChecks(gh, repoDir string, dest Destination, timeout int, rollup []any) ([]check, bool, string) {
	required, requiredErr := runChecks(gh, repoDir, dest, timeout, true)
	if requiredErr == nil && len(required) > 0 {
		return required, true, ""
	}
	if unsupported(requiredErr) {
		return fromRollup(gh, repoDir, dest, timeout, rollup)
	}
	all, allErr := runChecks(gh, repoDir, dest, timeout, false)
	if allErr == nil {
		return all, false, ""
	}
	if unsupported(allErr) {
		return fromRollup(gh, repoDir, dest, timeout, rollup)
	}
	return nil, false, allErr.Error()
}

// runChecks reports no error for a nonzero exit: `gh pr checks` exits 1 on a failing check and 8 on a pending one,
// and prints the JSON either way. Only output that is not a check list is a failure to read.
func runChecks(gh, repoDir string, dest Destination, timeout int, requiredOnly bool) ([]check, error) {
	args := []string{"pr", "checks", fmt.Sprint(dest.Number), "--repo", dest.Repository, "--json", ciFields}
	if requiredOnly {
		args = append(args, "--required")
	}
	runCtx, cancel := context.WithTimeout(context.Background(), bound(timeout))
	defer cancel()
	cmd := exec.CommandContext(runCtx, gh, args...)
	cmd.Dir = repoDir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()
	if runCtx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("gh pr checks did not finish in time")
	}
	var parsed []check
	if json.Unmarshal(out, &parsed) == nil {
		return parsed, nil
	}
	detail := firstLine(stderr.String())
	if detail == "" {
		detail = reasonOf(runErr, "gh printed no check list")
	}
	if noChecks(detail) {
		return nil, nil
	}
	return nil, fmt.Errorf("%s", detail)
}

// noChecks is gh's own wording for a PR with nothing to report, which is an answer, not a failure to read.
func noChecks(detail string) bool {
	return strings.Contains(detail, "no checks reported") || strings.Contains(detail, "no required checks reported")
}

func unsupported(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "unknown flag") || strings.Contains(text, "unknown command")
}

// fromRollup is the fallback for a gh too old to answer `pr checks --json`. The rollup carries GitHub's required
// flag when the API sets one; without it every check is the scope and the row says so.
func fromRollup(gh, repoDir string, dest Destination, timeout int, rollup []any) ([]check, bool, string) {
	if rollup == nil {
		read, err := readRollup(gh, repoDir, dest, timeout)
		if err != nil {
			return nil, false, err.Error()
		}
		rollup = read
	}
	var all, required []check
	for _, raw := range rollup {
		node, isObject := raw.(map[string]any)
		if !isObject {
			continue
		}
		one := rollupCheck(node)
		all = append(all, one)
		if flag, isBool := node["isRequired"].(bool); isBool && flag {
			required = append(required, one)
		}
	}
	if len(required) > 0 {
		return required, true, ""
	}
	return all, false, ""
}

func readRollup(gh, repoDir string, dest Destination, timeout int) ([]any, error) {
	runCtx, cancel := context.WithTimeout(context.Background(), bound(timeout))
	defer cancel()
	cmd := exec.CommandContext(runCtx, gh, "pr", "view", fmt.Sprint(dest.Number), "--repo", dest.Repository, "--json", "statusCheckRollup")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh could not read the checks of PR #%d: %s", dest.Number, firstLine(err.Error()))
	}
	var payload struct {
		StatusCheckRollup []any `json:"statusCheckRollup"`
	}
	if json.Unmarshal(out, &payload) != nil {
		return nil, fmt.Errorf("gh did not return a check rollup for PR #%d", dest.Number)
	}
	return payload.StatusCheckRollup, nil
}

// rollupCheck maps a rollup node onto the same shape `pr checks` gives, so one outcome table decides either source.
// A rollup states conclusion and status instead of a bucket, so the bucket is derived here and nowhere else.
func rollupCheck(node map[string]any) check {
	text := func(keys ...string) string {
		for _, key := range keys {
			if value, isText := node[key].(string); isText && value != "" {
				return value
			}
		}
		return ""
	}
	one := check{
		Name:     text("name", "context"),
		Link:     text("detailsUrl", "targetUrl"),
		Workflow: text("workflowName"),
	}
	conclusion := text("conclusion")
	status := text("status", "state")
	one.State = strings.ToLower(conclusion)
	if one.State == "" {
		one.State = strings.ToLower(status)
	}
	one.Bucket = rollupBucket(strings.ToUpper(conclusion), strings.ToUpper(status))
	return one
}

var rollupBuckets = map[string]string{
	"SUCCESS": "pass", "NEUTRAL": "pass", "SKIPPED": "skipping", "CANCELLED": "cancel",
	"FAILURE": "fail", "TIMED_OUT": "fail", "ACTION_REQUIRED": "fail", "STARTUP_FAILURE": "fail", "STALE": "fail",
	"ERROR": "fail", "PENDING": "pending", "EXPECTED": "pending",
}

func rollupBucket(conclusion, status string) string {
	if bucket, known := rollupBuckets[conclusion]; known {
		return bucket
	}
	if conclusion != "" {
		return "fail"
	}
	if bucket, known := rollupBuckets[status]; known {
		return bucket
	}
	return "pending"
}

func ciOutcome(checks []check) string {
	worst := 0
	for _, one := range checks {
		if rank := severityOf(one.Bucket); rank > worst {
			worst = rank
		}
	}
	switch worst {
	case 3:
		return "fail"
	case 2:
		return "pending"
	default:
		return "pass"
	}
}

func severityOf(bucket string) int {
	if rank, known := severity[bucket]; known {
		return rank
	}
	return severity["fail"]
}

func ciSummary(outcome string, checks []check, requiredOnly bool, observedAt string) string {
	noun := "checks"
	suffix := ciNoRequired
	if requiredOnly {
		noun = "required checks"
		suffix = ""
	}
	counts := map[string]int{}
	for _, one := range checks {
		counts[one.Bucket]++
	}
	stamp := observedStamp(observedAt)
	switch outcome {
	case "pass":
		return fmt.Sprintf("Passed %d/%d %s (observed %s)%s", counts["pass"], len(checks), noun, stamp, suffix)
	case "pending":
		return fmt.Sprintf("%d pending, %d passed of %d %s (observed %s)%s",
			counts["pending"], counts["pass"], len(checks), noun, stamp, suffix)
	default:
		failing := firstFailing(checks)
		headline, known := headlines[failing.Bucket]
		if !known {
			headline = "Failed on " + failing.State
		}
		return fmt.Sprintf("%s: %s; %d of %d %s passed (observed %s)%s",
			headline, checkLabel(failing), counts["pass"], len(checks), noun, stamp, suffix)
	}
}

func firstFailing(checks []check) check {
	for _, one := range checks {
		if severityOf(one.Bucket) == severity["fail"] {
			return one
		}
	}
	return check{}
}

// checkLabel links the failing check so a reviewer reaches its log from the table cell itself.
func checkLabel(one check) string {
	name := one.Name
	if name == "" {
		name = "an unnamed check"
	}
	if one.Link == "" {
		return name
	}
	return fmt.Sprintf("[%s](%s)", name, one.Link)
}

// observedStamp is the recorded instant to the minute, which is the precision a row needs to be read as a snapshot.
func observedStamp(at string) string {
	if len(at) < 16 {
		return at
	}
	return at[:16] + "Z"
}

func checkRows(checks []check) []any {
	rows := make([]any, 0, len(checks))
	for _, one := range checks {
		row := ordjson.NewObject()
		row.Set("name", one.Name)
		row.Set("bucket", one.Bucket)
		row.Set("state", nilIfEmpty(one.State))
		row.Set("link", nilIfEmpty(one.Link))
		row.Set("workflow", nilIfEmpty(one.Workflow))
		rows = append(rows, row)
	}
	return rows
}

func countRows(checks []check) *ordjson.Object {
	counts := map[string]int{}
	order := append([]string{}, buckets...)
	for _, one := range checks {
		if _, known := severity[one.Bucket]; !known {
			if counts[one.Bucket] == 0 {
				order = append(order, one.Bucket)
			}
		}
		counts[one.Bucket]++
	}
	rows := ordjson.NewObject()
	for _, bucket := range order {
		rows.Set(bucket, jsonNumber(counts[bucket]))
	}
	rows.Set("total", jsonNumber(len(checks)))
	return rows
}

// deriveCI reads the observation for the SHA the PR actually carries. CI runs on what was pushed, so an observation
// of an earlier head says nothing about this one and is ignored until the checks are read again.
func deriveCI(task *ordjson.Object) Row {
	row := Row{Stage: StageCI, Status: Pending, Result: ciUnobserved}
	pr, _ := field(task, "pr").(*ordjson.Object)
	identity, _ := field(pr, "identity").(*ordjson.Object)
	head := stringField(identity, "head_sha")
	if head == "" {
		return row
	}
	var latest *ordjson.Object
	for _, record := range records(task) {
		if stringField(record, "kind") != ciGate || stringField(record, "source") != "coordinator" {
			continue
		}
		if stringField(record, "head_sha") != head {
			continue
		}
		latest = record
	}
	if latest == nil {
		return row
	}
	status, known := ciOutcomes[stringField(latest, "outcome")]
	if !known {
		status = Fail
	}
	return Row{
		Stage:    StageCI,
		Status:   status,
		Result:   stringField(latest, "summary"),
		At:       stringField(latest, "at"),
		Evidence: []string{stringField(latest, "id")},
	}
}

// republish never fails the observation that was already recorded: a publication failure is its own record.
func republish(s *store.Store, ctx *ordjson.Object, runtimeRoot string, args CIArgs) any {
	if args.NoPublish {
		return nil
	}
	loaded, err := settings.LoadSettings(s)
	if err != nil {
		return Publication{Outcome: OutcomeSkipped, Reason: err.Error()}.Row()
	}
	if !loaded.AutoPublishEvidence() {
		return nil
	}
	publication, err := Publish(s, ctx, runtimeRoot, PublishArgs{Task: args.Task, Timeout: args.Timeout, Trigger: "ci"})
	if err != nil {
		return Publication{Outcome: OutcomeFailed, Reason: err.Error()}.Row()
	}
	return publication.Row()
}
