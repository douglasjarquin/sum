// Package run is the portable verification runner for the project-root VERIFY.md contract.
//
// It needs nothing but Git and mise in an ordinary clone: no sum installation, no Herdr, no absolute path to anything
// outside the repository. It reads VERIFY.md, checks that the canonical `mise run verify` task belongs to this project
// (not a parent directory's configuration), runs it, and writes one evidence record per run under the Git-ignored
// artifact directory. A run's outcome is one of blocked / fail / provisional / uncompared / needs-root-review /
// certified, and `--check` reports checked. A missing or malformed contract is `blocked`, never a passing outcome,
// and only `certified` binds the result to a commit SHA.
package run

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/douglasjarquin/sum/tools/verify/internal/contract"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
	"github.com/douglasjarquin/sum/tools/verify/internal/value"
)

const usage = `usage: sum-verify run [--root DIR] [--check] [--base REF] [--scenario ID=STATUS[:reason]]... [--timeout SECONDS] [--json]

Portable verification runner for the project-root VERIFY.md contract.

  --root DIR                     any directory inside the repository; the Git top level is resolved from it (default .)
  --check                        validate the contract, maps, task ownership, and requirements without running anything
  --base REF                     commit/ref to compare policy files against; changes to VERIFY.md, maps, or tasks are flagged for root review
  --scenario ID=STATUS[:reason]  outcome of a manual/interactive scenario you actually exercised
  --timeout SECONDS              override the contract's timeout_seconds for the entrypoint
  --json                         print the full run record instead of a summary

Exit codes: 0 checked or any passing outcome, 1 fail, 2 blocked (configuration, dependency, or freshness prevented a trustworthy run), 3 usage.
`

const schema = 2

var exitCodes = map[string]int{"checked": 0, "provisional": 0, "uncompared": 0, "needs-root-review": 0, "certified": 0, "fail": 1, "blocked": 2}

type options struct {
	root      string
	check     bool
	base      string
	scenarios []string
	timeout   int
	json      bool
}

// reported is the outcome the operator gave for one manual scenario.
type reported struct {
	status, reason string
}

type runnerRecord struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
}

type candidate struct {
	SHA          string  `json:"sha"`
	Dirty        bool    `json:"dirty"`
	Branch       *string `json:"branch"`
	GitDirIsFile bool    `json:"git_dir_is_file"`
}

type contractRecord struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Entrypoint string `json:"entrypoint"`
	TaskOwner  string `json:"task_owner"`
}

type artifacts struct {
	Dir        string `json:"dir"`
	GitIgnored bool   `json:"git_ignored"`
	RunDir     string `json:"run_dir,omitempty"`
}

type scenarioResult struct {
	scenario
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// session is one invocation: the record being assembled and the parts of it the summary and exit code read back.
type session struct {
	opts      options
	outcomes  map[string]reported
	record    *value.Object
	runID     string
	root      string
	outcome   string
	blocked   string
	candidate *candidate
	artifacts *artifacts
	policy    *policy
	evidence  *evidence
	rows      []scenarioResult
	ran       bool
}

// Main runs the subcommand with its arguments and returns the process exit code.
func Main(args []string) int {
	var opts options
	fs := flag.NewFlagSet("sum-verify run", flag.ContinueOnError)
	fs.StringVar(&opts.root, "root", ".", "")
	fs.BoolVar(&opts.check, "check", false, "")
	fs.StringVar(&opts.base, "base", "", "")
	fs.Func("scenario", "", func(value string) error {
		opts.scenarios = append(opts.scenarios, value)
		return nil
	})
	fs.IntVar(&opts.timeout, "timeout", 0, "")
	fs.BoolVar(&opts.json, "json", false, "")
	rest, exit, done := pycompat.ParseFlags(fs, args, usage, false)
	if done {
		return exit
	}
	if len(rest) > 0 {
		return pycompat.UsageError(fs, usage, "unrecognized arguments: %s", strings.Join(rest, " "))
	}
	outcomes, err := parseScenarios(opts.scenarios)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}

	s := &session{opts: opts, outcomes: outcomes, record: value.NewObject(), runID: tool.Stamp(4)}
	mode := "run"
	if opts.check {
		mode = "check"
	}
	executable, _ := os.Executable()
	s.record.Set("schema", schema)
	s.record.Set("run_id", s.runID)
	s.record.Set("started_at", tool.UTCNow())
	s.record.Set("runner", runnerRecord{Path: pycompat.Resolve(executable), Mode: mode})
	s.record.Set("outcome", nil)
	s.record.Set("blocked_reason", nil)

	var blocked *contract.Blocked
	switch err := s.verify(); {
	case errors.As(err, &blocked):
		s.outcome, s.blocked = "blocked", blocked.Reason
	case err != nil:
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	s.record.Set("outcome", s.outcome)
	if s.blocked != "" {
		s.record.Set("blocked_reason", s.blocked)
	}
	s.record.Set("ended_at", tool.UTCNow())

	if s.artifacts != nil && s.artifacts.RunDir != "" {
		if err := s.write(); err != nil {
			fmt.Fprintf(os.Stderr, "sum-verify run: the run record could not be written: %v\n", err)
			return 2
		}
	}
	if opts.json {
		if err := tool.WriteJSON(os.Stdout, s.record); err != nil {
			fmt.Fprintf(os.Stderr, "sum-verify run: %v\n", err)
			return 2
		}
	} else {
		s.summarize()
	}
	return exitCodes[s.outcome]
}

func parseScenarios(values []string) (map[string]reported, error) {
	outcomes := map[string]reported{}
	for _, value := range values {
		id, rest, ok := strings.Cut(value, "=")
		if !ok {
			return nil, fmt.Errorf("--scenario expects ID=STATUS[:reason], got %s", pycompat.Repr(value))
		}
		status, reason, _ := strings.Cut(rest, ":")
		reason = pycompat.Strip(reason)
		switch status {
		case "pass", "fail":
		case "blocked", "not-applicable":
			if reason == "" {
				return nil, fmt.Errorf("--scenario %s=%s needs a reason after ':'", id, status)
			}
		default:
			return nil, fmt.Errorf("--scenario status must be one of pass, fail, blocked, not-applicable; got %s", pycompat.Repr(status))
		}
		outcomes[id] = reported{status: status, reason: reason}
	}
	return outcomes, nil
}

// verify fills the record. A *contract.Blocked error is a blocked run; any other error is a usage mistake.
func (s *session) verify() error {
	root, err := contract.Root(pycompat.Resolve(s.opts.root))
	if err != nil {
		return contract.Blockedf("%s is not inside a Git work tree: %s", pycompat.Resolve(s.opts.root), pycompat.Clip(err.Error(), 200))
	}
	s.root = root
	s.record.Set("root", root)

	head, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	head = pycompat.Strip(head)
	status, err := git(root, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return err
	}
	s.candidate = &candidate{SHA: head, Dirty: pycompat.Strip(status) != "", GitDirIsFile: pycompat.IsFile(root + "/.git")}
	if branch, _, _ := contract.Git(root, "rev-parse", "--abbrev-ref", "HEAD"); pycompat.Strip(branch) != "" {
		branch = pycompat.Strip(branch)
		s.candidate.Branch = &branch
	}
	s.record.Set("candidate", s.candidate)

	terms, err := loadContract(root)
	if err != nil {
		return err
	}
	s.record.Set("contract", contractRecord{Path: contract.File, SHA256: terms.sha256, Entrypoint: terms.entrypoint, TaskOwner: terms.taskOwner})
	maps, scenarios, err := loadFeatureMaps(root, terms.featureMaps, terms.taskOwner)
	if err != nil {
		return err
	}
	s.record.Set("feature_maps", maps)
	if unknown := unknownScenarios(s.outcomes, scenarios); len(unknown) > 0 {
		return fmt.Errorf("--scenario names id(s) not present in any feature map: %s", strings.Join(unknown, ", "))
	}
	requirements, err := checkRequirements(terms.commands)
	if err != nil {
		return err
	}
	s.record.Set("requirements", requirements)
	task, err := miseTask(root, terms.taskOwner, "verify")
	if err != nil {
		return err
	}
	s.record.Set("task", task)
	// Maps are policy too. Base extras stay protected even if HEAD drops them.
	protected := terms.policyFiles
	for _, featureMap := range maps {
		protected = append(protected, featureMap.Path)
	}
	if s.policy, err = policyChange(root, s.opts.base, protected); err != nil {
		return err
	}
	s.record.Set("policy", s.policy)
	artifactDir := pycompat.Join(root, terms.artifacts)
	_, _, ignored := contract.Git(root, "check-ignore", "-q", artifactDir)
	s.artifacts = &artifacts{Dir: terms.artifacts, GitIgnored: ignored}
	s.record.Set("artifacts", s.artifacts)

	if s.opts.check {
		s.outcome = "checked"
		s.record.Set("scenarios", scenarios)
		return nil
	}

	runDir := artifactDir + "/" + s.runID
	if err := os.MkdirAll(artifactDir, 0o777); err != nil {
		return contract.Blockedf("the run directory could not be created: %v", err)
	}
	if err := os.Mkdir(runDir, 0o777); err != nil {
		return contract.Blockedf("the run directory could not be created: %v", err)
	}
	s.artifacts.RunDir, _ = pycompat.Rel(runDir, root)
	timeout := s.opts.timeout
	if timeout == 0 {
		timeout = terms.timeout
	}
	execution, err := runEntrypoint(root, runDir, timeout, task.Binary)
	if err != nil {
		return err
	}
	s.record.Set("execution", execution)
	fresh, err := freshnessState(root, terms.freshnessInputs, terms.freshnessOutputs)
	if err != nil {
		return err
	}
	s.record.Set("freshness", fresh)

	s.rows = s.scenarioResults(scenarios, execution)
	s.record.Set("scenarios", s.rows)
	s.evidence = evidenceState(root, terms.evidence, head, scenarios)
	s.record.Set("evidence", s.evidence)
	notExercised := []string{}
	for i := range s.rows {
		row := &s.rows[i]
		if slices.Contains(s.evidence.Missing, row.ID) {
			row.Reason += "; required visual evidence (screenshot/screencast comparison for this candidate) is missing"
		}
		switch row.Status {
		case "not-run", "blocked", "not-applicable":
			notExercised = append(notExercised, row.ID)
		}
	}
	s.outcome = runOutcome(execution, fresh, s.rows, s.candidate.Dirty, s.policy)
	switch {
	case execution.TimedOut:
		s.blocked = fmt.Sprintf("`mise run verify` exceeded %ds and was stopped", timeout)
	case execution.passed() && fresh != nil && !fresh.Fresh:
		s.blocked = *fresh.Reason
	}
	s.record.Set("not_exercised", notExercised)
	s.ran = true
	return nil
}

func unknownScenarios(outcomes map[string]reported, scenarios []scenario) []string {
	mapped := map[string]bool{}
	for _, scenario := range scenarios {
		mapped[scenario.ID] = true
	}
	var unknown []string
	for id := range outcomes {
		if !mapped[id] {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	return unknown
}

func (s *session) scenarioResults(scenarios []scenario, execution *execution) []scenarioResult {
	automated := "fail"
	switch {
	case execution.passed():
		automated = "pass"
	case execution.TimedOut:
		automated = "blocked"
	}
	exit := "None"
	if execution.Exit != nil {
		exit = fmt.Sprint(*execution.Exit)
	}
	rows := []scenarioResult{}
	for _, scenario := range scenarios {
		row := scenarioResult{scenario: scenario, Status: "not-run", Reason: "manual/interactive scenario was not exercised in this run"}
		if scenario.Driver == "automated" {
			row.Status, row.Reason = automated, fmt.Sprintf("covered by `mise run verify` (exit %s)", exit)
		} else if given, ok := s.outcomes[scenario.ID]; ok {
			row.Status, row.Reason = given.status, given.reason
			if row.Reason == "" {
				row.Reason = "reported by the operator for this run"
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// runOutcome names what the run established. Precedence: blocked, fail, provisional, uncompared, needs-root-review, certified.
func runOutcome(execution *execution, fresh *freshness, rows []scenarioResult, dirty bool, policy *policy) string {
	if execution.TimedOut {
		return "blocked"
	}
	failed := !execution.passed() || (fresh != nil && !fresh.Fresh)
	for _, row := range rows {
		failed = failed || row.Status == "fail"
	}
	switch {
	case failed:
		return "fail"
	case dirty:
		return "provisional" // Nothing about a dirty tree is bound to a SHA, whatever the policy comparison says.
	case !policy.Checked:
		return "uncompared" // Unchecked policy is unreviewed, never certified.
	case len(policy.Changed) > 0:
		return "needs-root-review"
	}
	return "certified"
}

// write stores the record in the run directory and as the artifact directory's latest.json.
func (s *session) write() error {
	data, err := tool.EncodeJSON(s.record)
	if err != nil {
		return err
	}
	if err := os.WriteFile(pycompat.Join(s.root, s.artifacts.RunDir)+"/run.json", data, 0o666); err != nil {
		return err
	}
	return os.WriteFile(pycompat.Join(s.root, s.artifacts.Dir)+"/latest.json", data, 0o666)
}

func (s *session) summarize() {
	line := "verify: " + s.outcome
	if s.blocked != "" {
		line += " (" + s.blocked + ")"
	}
	fmt.Println(line)
	if s.candidate != nil {
		fmt.Printf("candidate: %s dirty=%s\n", s.candidate.SHA, pycompat.Repr(s.candidate.Dirty))
	}
	if s.rows != nil {
		counts := map[string]int{}
		for _, row := range s.rows {
			counts[row.Status]++
		}
		statuses := make([]string, 0, len(counts))
		for status := range counts {
			statuses = append(statuses, status)
		}
		sort.Strings(statuses)
		for i, status := range statuses {
			statuses[i] = fmt.Sprintf("%s=%d", status, counts[status])
		}
		if len(statuses) == 0 {
			fmt.Println("scenarios: none mapped")
		} else {
			fmt.Println("scenarios: " + strings.Join(statuses, ", "))
		}
	}
	switch {
	case s.ran && len(s.policy.Changed) > 0:
		fmt.Println("policy files changed since base; this run cannot certify its own new standard: " + strings.Join(s.policy.Changed, ", "))
	case s.ran && !s.policy.Checked:
		fmt.Println("policy not compared (no --base); the run cannot certify a SHA until VERIFY.md, tasks, and maps are reviewed against a base")
	}
	if s.evidence != nil && len(s.evidence.Missing) > 0 {
		fmt.Printf("required evidence missing for: %s (no comparison for this candidate under %s)\n", strings.Join(s.evidence.Missing, ", "), s.evidence.Dir)
	}
	if s.artifacts != nil && s.artifacts.RunDir != "" {
		fmt.Printf("record: %s/run.json\n", s.artifacts.RunDir)
	}
}
