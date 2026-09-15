package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

const prGate = "pr"

// The PR stage's action is what this run did about the branch, and is the one field the derived row reads.
// A refusal is a decision for the user, never something the next run quietly retries.
const (
	prCreated  = "created"
	prExisting = "existing"
	prRefused  = "refused"
	prFailed   = "failed"
	prPlanned  = "planned"
)

var prURL = regexp.MustCompile(`https://[^\s]*/pull/([0-9]+)`)

type PRArgs struct {
	Task                string
	Draft               bool
	Title               string
	BodyFile            string
	DryRun              bool
	AllowNewAfterClosed bool
	Timeout             int
}

// listedPR is one row of `gh pr list --json`, which is how sum learns whether this branch already has a PR.
type listedPR struct {
	Number  int    `json:"number"`
	State   string `json:"state"`
	HeadOID string `json:"headRefOid"`
	Base    string `json:"baseRefName"`
	URL     string `json:"url"`
}

func (p listedPR) open() bool { return strings.EqualFold(p.State, "open") }

// PR creates the pull request for a pushed candidate, or adopts the one that is already there. It never merges and
// never reviews. Reconciling the number it returns is the caller's next step, and is what records the PR identity.
func PR(s *store.Store, ctx *ordjson.Object, runtimeRoot string, args PRArgs) (*ordjson.Object, error) {
	task, candidate, err := gateTask(s, ctx, args.Task, "")
	if err != nil {
		return nil, err
	}
	if status := stringField(task, "status"); status != "reported" {
		return nil, fmt.Errorf("this task is %q; the PR is opened on the worker's reported candidate", status)
	}
	if push := Derive(task).Get(StagePush); push.Status != Pass {
		return nil, fmt.Errorf("the Push gate is %s (%s); the branch must be on origin at the candidate before a PR can point at it",
			push.Status, push.Result)
	}
	branch := stringField(task, "branch")
	if branch == "" {
		return nil, fmt.Errorf("this task records no branch to open a PR for")
	}
	repoDir := stringField(task, "repository")
	gh, err := toolpath.Find(runtimeRoot, "gh")
	if err != nil {
		return nil, err
	}
	remote := RemoteRepository(gh, repoDir)
	if remote == "" {
		return nil, fmt.Errorf("`gh repo view` could not name the GitHub repository for %q; check `gh auth status` and the remote", repoDir)
	}

	prRecord, _ := field(task, "pr").(*ordjson.Object)
	if identity, _ := field(prRecord, "identity").(*ordjson.Object); identity != nil {
		return recordPR(s, ctx, args, candidate, prBody(prExisting, branch, remote, args.Draft,
			numberOf(identity), stringField(identity, "url"), stringField(identity, "base_branch")),
			fmt.Sprintf("Already recorded as #%d; this run reconciles it", numberOf(identity)))
	}

	listed, listErr := listPRs(gh, repoDir, remote, branch, args.Timeout)
	if listErr != nil {
		return nil, fmt.Errorf("could not read the pull requests for %s: %w; nothing was created", branch, listErr)
	}
	if adopted := adoptable(listed, candidate); adopted != nil {
		return recordPR(s, ctx, args, candidate, prBody(prExisting, branch, remote, args.Draft, adopted.Number, adopted.URL, adopted.Base),
			fmt.Sprintf("Adopted the existing #%d (%s): %s", adopted.Number, strings.ToLower(adopted.State), adopted.URL))
	}
	if len(listed) > 0 && !args.AllowNewAfterClosed {
		return recordPR(s, ctx, args, candidate, prBody(prRefused, branch, remote, args.Draft, 0, "", ""),
			fmt.Sprintf("Refused: %s already has %s and none is open at this candidate. A closed PR is not permission to create another; reopen one, or pass --allow-new-after-closed.",
				branch, describe(listed)))
	}
	return create(s, ctx, gh, repoDir, remote, branch, candidate, task, args)
}

func create(s *store.Store, ctx *ordjson.Object, gh, repoDir, remote, branch, candidate string, task *ordjson.Object, args PRArgs) (*ordjson.Object, error) {
	base := BaseBranch(repoDir, task)
	title := args.Title
	if title == "" {
		title = Title(task)
	}
	bodyFile := args.BodyFile
	if bodyFile == "" {
		var err error
		if bodyFile, err = writeBody(s, args.Task, RenderBody(task)); err != nil {
			return nil, err
		}
	}
	if args.DryRun {
		planned := prBody(prPlanned, branch, remote, args.Draft, 0, "", base)
		planned.Set("title", title)
		planned.Set("body_file", bodyFile)
		result := ordjson.NewObject()
		result.Set("task", args.Task)
		result.Set("pr", planned)
		result.Set("summary", fmt.Sprintf("Would open %s against %s as %q; nothing was created", branch, base, title))
		return result, nil
	}

	cmd := []string{"pr", "create", "--repo", remote, "--head", branch, "--base", base, "--title", title, "--body-file", bodyFile}
	if args.Draft {
		cmd = append(cmd, "--draft")
	}
	stdout, runErr := runGH(gh, repoDir, args.Timeout, cmd...)
	number, url := parseCreated(stdout)

	// A timeout or unreadable output does not prove nothing was created, so GitHub is asked again before this is
	// called a failure. Retrying the create blind is how a branch ends up with two pull requests.
	if runErr != nil || number == 0 {
		listed, listErr := listPRs(gh, repoDir, remote, branch, args.Timeout)
		if listErr == nil {
			if adopted := adoptable(listed, candidate); adopted != nil {
				body := prBody(prCreated, branch, remote, args.Draft, adopted.Number, adopted.URL, adopted.Base)
				body.Set("title", title)
				body.Set("body_file", bodyFile)
				return recordPR(s, ctx, args, candidate, body,
					fmt.Sprintf("Created #%d: %s (`gh pr create` did not report it; GitHub did)", adopted.Number, adopted.URL))
			}
		}
		body := prBody(prFailed, branch, remote, args.Draft, 0, "", base)
		body.Set("title", title)
		body.Set("body_file", bodyFile)
		return recordPR(s, ctx, args, candidate, body, "Failed to open the PR: "+createReason(runErr, stdout))
	}

	body := prBody(prCreated, branch, remote, args.Draft, number, url, base)
	body.Set("title", title)
	body.Set("body_file", bodyFile)
	return recordPR(s, ctx, args, candidate, body, fmt.Sprintf("Created #%d: %s", number, url))
}

func prBody(action, branch, remote string, draft bool, number int, url, base string) *ordjson.Object {
	body := ordjson.NewObject()
	body.Set("action", action)
	body.Set("repository", remote)
	body.Set("head_branch", branch)
	body.Set("base_branch", nilIfEmpty(base))
	body.Set("draft", draft)
	if number == 0 {
		body.Set("number", nil)
	} else {
		body.Set("number", jsonNumber(number))
	}
	body.Set("url", nilIfEmpty(url))
	return body
}

func recordPR(s *store.Store, ctx *ordjson.Object, args PRArgs, candidate string, body *ordjson.Object, summary string) (*ordjson.Object, error) {
	result, err := recordGate(s, ctx, args.Task, prGate, candidate, body, summary)
	if err != nil {
		return nil, err
	}
	result.Set("pr", body)
	result.Set("summary", summary)
	return result, nil
}

// adoptable is the PR this branch already has: one at the candidate first, then any that is still open. Creating a
// second PR for a branch GitHub already has one for is the duplicate this whole step exists to prevent.
func adoptable(listed []listedPR, candidate string) *listedPR {
	for _, pr := range listed {
		if pr.HeadOID == candidate && pr.open() {
			return &pr
		}
	}
	for _, pr := range listed {
		if pr.HeadOID == candidate {
			return &pr
		}
	}
	for _, pr := range listed {
		if pr.open() {
			return &pr
		}
	}
	return nil
}

func describe(listed []listedPR) string {
	names := make([]string, 0, len(listed))
	for _, pr := range listed {
		names = append(names, fmt.Sprintf("#%d (%s)", pr.Number, strings.ToLower(pr.State)))
	}
	return strings.Join(names, ", ")
}

func listPRs(gh, repoDir, remote, branch string, timeout int) ([]listedPR, error) {
	stdout, err := runGH(gh, repoDir, timeout, "pr", "list", "--repo", remote, "--head", branch,
		"--state", "all", "--json", "number,state,headRefOid,baseRefName,url")
	if err != nil {
		return nil, err
	}
	var listed []listedPR
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil {
		return nil, fmt.Errorf("gh did not return a pull request list: %s", boundText(strings.TrimSpace(stdout), 300))
	}
	return listed, nil
}

// RemoteRepository is the owner/name `gh` resolves for a checkout, or "" when it cannot say.
func RemoteRepository(gh, repoDir string) string {
	stdout, err := runGH(gh, repoDir, 0, "repo", "view", "--json", "nameWithOwner")
	if err != nil {
		return ""
	}
	var payload struct {
		NameWithOwner string `json:"nameWithOwner"`
	}
	if json.Unmarshal([]byte(stdout), &payload) != nil {
		return ""
	}
	return payload.NameWithOwner
}

func runGH(gh, dir string, timeout int, args ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(context.Background(), bound(timeout))
	defer cancel()
	cmd := exec.CommandContext(runCtx, gh, args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if runCtx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("gh %s did not finish in time", strings.Join(args[:2], " "))
	}
	if err != nil {
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		return string(out), fmt.Errorf("%s", boundText(lastLineOf(reason), 300))
	}
	return string(out), nil
}

func parseCreated(stdout string) (int, string) {
	match := prURL.FindStringSubmatch(stdout)
	if match == nil {
		return 0, ""
	}
	number, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, ""
	}
	return number, match[0]
}

func createReason(err error, stdout string) string {
	if err != nil {
		return err.Error()
	}
	if trimmed := strings.TrimSpace(stdout); trimmed != "" {
		return "no PR URL in `gh pr create` output: " + boundText(lastLineOf(trimmed), 300)
	}
	return "`gh pr create` reported nothing and GitHub shows no PR for this branch"
}

func writeBody(s *store.Store, taskID, text string) (string, error) {
	taskPath, err := s.TaskPath(taskID)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(taskPath, pipelineDir, prGate)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "body.md")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func numberOf(identity *ordjson.Object) int {
	raw, isNumber := field(identity, "number").(json.Number)
	if !isNumber {
		return 0
	}
	value, err := raw.Int64()
	if err != nil {
		return 0
	}
	return int(value)
}
