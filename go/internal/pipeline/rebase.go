package pipeline

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const (
	rebaseGate       = "rebase"
	rebaseUnobserved = "Not observed; run `pipeline rebase`"
)

var rebaseOutcomes = map[string]Status{
	"up-to-date":       Pass,
	"clean":            Blocked,
	outcomeUnavailable: Blocked,
	"conflict":         Fail,
	"failed":           Fail,
}

type RebaseArgs struct {
	Task      string
	Candidate string
}

// Rebase only observes. Rewriting the task branch would mean force-pushing over a checkout the worker still holds,
// which is the force-reset the contract forbids, so a branch that is behind stays the worker's to rebase.
func Rebase(s *store.Store, ctx *ordjson.Object, args RebaseArgs) (*ordjson.Object, error) {
	task, candidate, err := gateTask(s, ctx, args.Task, args.Candidate)
	if err != nil {
		return nil, err
	}
	repo := stringField(task, "repository")
	if repo == "" || gitIn(repo, gitBound, "rev-parse", "--git-dir").Code != 0 {
		return nil, fmt.Errorf("the task repository %q is not a Git repository; nothing can be observed against the base branch from here", repo)
	}
	if cat := gitIn(repo, gitBound, "cat-file", "-e", candidate+"^{commit}"); cat.Code != 0 {
		return nil, fmt.Errorf("%s is not a commit in the task repository; verify the SHA the worker reported", candidate)
	}
	dir, err := runDir(s, args.Task, rebaseGate)
	if err != nil {
		return nil, err
	}
	body, summary := observeRebase(repo, dir, candidate, baseBranch(repo, task))
	return recordGate(s, ctx, args.Task, rebaseGate, candidate, body, summary)
}

// The reconciled PR is the base the change is actually going to; only without one does the repository's default apply.
func baseBranch(repo string, task *ordjson.Object) string {
	pr, _ := field(task, "pr").(*ordjson.Object)
	identity, _ := field(pr, "identity").(*ordjson.Object)
	if base := stringField(identity, "base_branch"); base != "" {
		return base
	}
	head := gitIn(repo, gitBound, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if head.Code == 0 {
		if name := strings.TrimPrefix(strings.TrimSpace(head.Stdout), "origin/"); name != "" {
			return name
		}
	}
	return "main"
}

func observeRebase(repo, dir, candidate, base string) (*ordjson.Object, string) {
	body := ordjson.NewObject()
	body.Set("base_branch", base)
	ref := "origin/" + base

	if fetch := gitIn(repo, networkBound, "fetch", "origin", base); fetch.Code != 0 {
		return unobservableRebase(body, fmt.Sprintf("Could not fetch %s: %s", ref, lastLineOf(fetch.Stderr)))
	}
	tip := gitIn(repo, gitBound, "rev-parse", "--verify", ref+"^{commit}")
	if tip.Code != 0 {
		return unobservableRebase(body, fmt.Sprintf("Could not resolve %s after fetching it: %s", ref, lastLineOf(tip.Stderr)))
	}
	baseSHA := strings.TrimSpace(tip.Stdout)
	body.Set("base_sha", baseSHA)

	if ancestor := gitIn(repo, gitBound, "merge-base", "--is-ancestor", baseSHA, candidate); ancestor.Code == 0 {
		body.Set("outcome", "up-to-date")
		body.Set("behind", jsonNumber(0))
		body.Set("conflicts", []any{})
		return body, "Up to date with " + base
	}
	behind := countCommits(repo, candidate+".."+baseSHA)
	body.Set("behind", jsonNumber(behind))

	checkout := filepath.Join(dir, "checkout")
	if add := gitIn(repo, gitBound, "worktree", "add", "--detach", checkout, candidate); add.Code != 0 {
		body.Set("conflicts", []any{})
		return unobservableRebase(body, fmt.Sprintf("Could not check out %s to test the rebase: %s", candidate, lastLineOf(add.Stderr)))
	}
	defer removeCheckout(repo, checkout)

	rebase := gitIn(checkout, networkBound,
		"-c", "core.editor=true", "-c", "commit.gpgsign=false", "-c", "rebase.autoStash=false",
		"rebase", baseSHA)
	if rebase.Code == 0 {
		body.Set("outcome", "clean")
		body.Set("conflicts", []any{})
		return body, fmt.Sprintf("Behind %s by %s; rebases cleanly. The worker rebases; send `repair send` with the rebase instruction",
			base, plural(behind, "commit", "commits"))
	}
	conflicts := unmergedPaths(checkout)
	gitIn(checkout, gitBound, "rebase", "--abort")
	body.Set("conflicts", anyStrings(conflicts))
	if len(conflicts) == 0 {
		body.Set("outcome", "failed")
		return body, fmt.Sprintf("Could not test the rebase onto %s: %s", base, lastLineOf(rebase.Stderr))
	}
	body.Set("outcome", "conflict")
	return body, fmt.Sprintf("Conflicts with %s in: %s", base, strings.Join(conflicts, ", "))
}

func unobservableRebase(body *ordjson.Object, summary string) (*ordjson.Object, string) {
	body.Set("outcome", outcomeUnavailable)
	if _, present := body.Get("conflicts"); !present {
		body.Set("conflicts", []any{})
	}
	return body, summary
}

func countCommits(repo, revisionRange string) int {
	run := gitIn(repo, gitBound, "rev-list", "--count", revisionRange)
	if run.Code != 0 {
		return 0
	}
	count, err := strconv.Atoi(strings.TrimSpace(run.Stdout))
	if err != nil {
		return 0
	}
	return count
}

func unmergedPaths(checkout string) []string {
	run := gitIn(checkout, gitBound, "diff", "--name-only", "--diff-filter=U")
	if run.Code != 0 {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(run.Stdout, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			paths = append(paths, trimmed)
		}
	}
	return paths
}
