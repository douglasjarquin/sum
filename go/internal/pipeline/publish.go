package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/evidence"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

type Outcome string

const (
	OutcomePublished Outcome = "published"
	OutcomeUnchanged Outcome = "unchanged"
	OutcomePlanned   Outcome = "planned"
	OutcomeRefused   Outcome = "refused"
	OutcomeFailed    Outcome = "failed"
	OutcomeUncertain Outcome = "uncertain"
	OutcomeSkipped   Outcome = "skipped"
)

const (
	ReceiptsFile    = "pipeline-receipts.json"
	receiptsSchema  = 1
	publishSubdir   = "pipeline"
	defaultGhBound  = 120 * time.Second
	rebaseAttempts  = 3
	receiptsHistory = 200
)

var (
	repoName = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	sha40    = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type Destination struct {
	Repository string
	Number     int
	HeadSHA    string
	State      string
}

func (d Destination) key() string {
	return fmt.Sprintf("%s#%d", d.Repository, d.Number)
}

type Publication struct {
	Outcome     Outcome
	Reason      string
	Destination Destination
	BlockSHA256 string
}

type PublishArgs struct {
	Task                string
	DryRun              bool
	ReplaceForeignBlock bool
	Timeout             int
	Trigger             string
}

type receipts struct {
	Schema int            `json:"schema"`
	Blocks []receiptBlock `json:"blocks"`
	Events []receiptEvent `json:"events"`
}

type receiptBlock struct {
	Destination string `json:"destination"`
	SHA256      string `json:"sha256"`
	At          string `json:"at"`
	Candidate   string `json:"candidate"`
}

type receiptEvent struct {
	At          string `json:"at"`
	Kind        string `json:"kind"`
	Destination string `json:"destination"`
	Reason      string `json:"reason,omitempty"`
}

// Publish writes the pipeline block into the reconciled PR and records the outcome as one publication record.
// It owns exactly one marked span; prose outside it, and the evidence block, are never touched.
func Publish(s *store.Store, ctx *ordjson.Object, runtimeRoot string, args PublishArgs) (Publication, error) {
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return Publication{}, err
	}
	dest, err := destination(task)
	if err != nil {
		return Publication{}, err
	}
	if dest.State != "open" {
		return record(s, ctx, args, dest, Publication{Outcome: OutcomeSkipped, Destination: dest,
			Reason: fmt.Sprintf("PR #%d is %s; the pipeline block is written only into an open PR", dest.Number, dest.State)})
	}
	gh, err := toolpath.Find(runtimeRoot, "gh")
	if err != nil {
		return Publication{}, err
	}
	taskDir, err := s.TaskPath(args.Task)
	if err != nil {
		return Publication{}, err
	}
	publication := publish(gh, taskDir, task, dest, args)
	return record(s, ctx, args, dest, publication)
}

func publish(gh, taskDir string, task *ordjson.Object, dest Destination, args PublishArgs) Publication {
	result := Publication{Destination: dest}
	publishDir := filepath.Join(taskDir, "publish", publishSubdir)
	receiptsPath := filepath.Join(taskDir, "publish", ReceiptsFile)
	known, err := loadReceipts(receiptsPath)
	if err != nil {
		return refuse(result, err.Error())
	}
	repoDir := stringField(task, "repository")
	block := Render(Derive(task), task)

	// The block is spliced onto the body as it was read immediately before the edit, so a reviewer's
	// concurrent paragraph is kept instead of clobbered. A body that will not hold still is refused.
	var body string
	stable := false
	for attempt := 0; attempt < rebaseAttempts; attempt++ {
		observed, viewErr := viewPR(gh, repoDir, dest, args.Timeout)
		if viewErr != nil {
			result.Outcome = OutcomeUncertain
			result.Reason = viewErr.Error()
			return result
		}
		if attempt > 0 && observed.Body == body {
			stable = true
			break
		}
		body = observed.Body
	}
	if !stable {
		return refuse(result, "the PR body changed on every read while preparing the edit; retry when it is quiet")
	}
	at, err := findBlock(body)
	if err != nil {
		return refuse(result, err.Error())
	}
	if at != nil {
		existing := body[at.start:at.end]
		if !known.owns(dest, blockHash(existing)) && !args.ReplaceForeignBlock {
			result.BlockSHA256 = blockHash(existing)
			return refuse(result, "the PR already carries a sum-pipeline block this coordinator did not write (someone edited it, or another tool wrote it); "+
				"inspect it and pass --replace-foreign-block to take it over. Prose outside the block is never touched either way.")
		}
		if normalize(existing) == normalize(block) {
			result.Outcome = OutcomeUnchanged
			result.BlockSHA256 = blockHash(block)
			result.Reason = "the PR already carries this exact pipeline block; nothing was edited"
			return result
		}
	}
	next := splice(body, at, block)
	if args.DryRun {
		result.Outcome = OutcomePlanned
		result.Reason = "dry run: nothing was edited"
		result.BlockSHA256 = blockHash(block)
		return result
	}
	if err := os.MkdirAll(publishDir, 0o700); err != nil {
		result.Outcome = OutcomeFailed
		result.Reason = err.Error()
		return result
	}
	bodyFile := filepath.Join(publishDir, "body-next.md")
	if err := os.WriteFile(bodyFile, []byte(next), 0o600); err != nil {
		result.Outcome = OutcomeFailed
		result.Reason = err.Error()
		return result
	}
	if err := os.WriteFile(filepath.Join(publishDir, "body-previous.md"), []byte(body), 0o600); err != nil {
		result.Outcome = OutcomeFailed
		result.Reason = err.Error()
		return result
	}
	known.event("edit-attempt", dest, "")
	if err := saveReceipts(receiptsPath, known); err != nil {
		result.Outcome = OutcomeFailed
		result.Reason = err.Error()
		return result
	}
	editErr := editBody(gh, repoDir, dest, bodyFile, args.Timeout)
	return reconcile(gh, repoDir, publishDir, dest, body, block, known, receiptsPath, Candidate(task), editErr, result, args.Timeout)
}

// reconcile reads the PR back after a write that may or may not have landed, and restores the previous body when the block did not land.
func reconcile(gh, repoDir, publishDir string, dest Destination, previous, block string, known *receipts, receiptsPath, candidate string,
	editErr error, result Publication, timeout int) Publication {
	observed, viewErr := viewPR(gh, repoDir, dest, timeout)
	if viewErr != nil {
		known.event("uncertain", dest, viewErr.Error())
		saveReceipts(receiptsPath, known)
		result.Outcome = OutcomeUncertain
		result.Reason = fmt.Sprintf("the edit was sent and the PR could not be read back: %s. Inspect the PR; do not retry blindly.", viewErr)
		return result
	}
	if observed.Body == previous {
		known.event("edit-not-applied", dest, reasonOf(editErr, "gh reported success but the PR body did not change"))
		saveReceipts(receiptsPath, known)
		result.Outcome = OutcomeFailed
		result.Reason = reasonOf(editErr, "gh reported success but the PR body did not change") + ". The PR body is intact."
		return result
	}
	landed, findErr := findBlock(observed.Body)
	if findErr != nil || landed == nil || normalize(observed.Body[landed.start:landed.end]) != normalize(block) {
		reason := "the PR body changed but does not carry the pipeline block that was written"
		if findErr != nil {
			reason = findErr.Error()
		}
		restored := restore(gh, repoDir, publishDir, dest, previous, timeout)
		known.event("restored", dest, reason)
		saveReceipts(receiptsPath, known)
		if restored {
			result.Outcome = OutcomeFailed
			result.Reason = reason + ". The previous PR body was restored."
			return result
		}
		result.Outcome = OutcomeUncertain
		result.Reason = reason + ". The PR body could NOT be restored; inspect it."
		return result
	}
	hash := blockHash(observed.Body[landed.start:landed.end])
	known.Blocks = append(known.Blocks, receiptBlock{Destination: dest.key(), SHA256: hash, At: store.Now(), Candidate: candidate})
	known.event("published", dest, "")
	if err := saveReceipts(receiptsPath, known); err != nil {
		result.Outcome = OutcomeUncertain
		result.Reason = "the block landed but its receipt could not be saved: " + err.Error()
		result.BlockSHA256 = hash
		return result
	}
	result.Outcome = OutcomePublished
	result.BlockSHA256 = hash
	if editErr != nil {
		result.Reason = "gh reported " + editErr.Error() + ", but the block landed and was read back"
	}
	return result
}

func restore(gh, repoDir, publishDir string, dest Destination, previous string, timeout int) bool {
	path := filepath.Join(publishDir, "body-restore.md")
	if err := os.WriteFile(path, []byte(previous), 0o600); err != nil {
		return false
	}
	if err := editBody(gh, repoDir, dest, path, timeout); err != nil {
		return false
	}
	observed, err := viewPR(gh, repoDir, dest, timeout)
	return err == nil && observed.Body == previous
}

func refuse(result Publication, reason string) Publication {
	result.Outcome = OutcomeRefused
	result.Reason = reason
	return result
}

func reasonOf(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	return err.Error()
}

type observation struct {
	Body    string `json:"body"`
	State   string `json:"state"`
	HeadOID string `json:"headRefOid"`
}

func viewPR(gh, repoDir string, dest Destination, timeout int) (observation, error) {
	runCtx, cancel := context.WithTimeout(context.Background(), bound(timeout))
	defer cancel()
	cmd := exec.CommandContext(runCtx, gh, "pr", "view", fmt.Sprint(dest.Number), "--repo", dest.Repository, "--json", "body,state,headRefOid")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return observation{}, fmt.Errorf("gh could not read PR #%d of %s: %s", dest.Number, dest.Repository, firstLine(err.Error()))
	}
	var observed observation
	if err := json.Unmarshal(out, &observed); err != nil {
		return observation{}, fmt.Errorf("gh did not return JSON for PR #%d of %s", dest.Number, dest.Repository)
	}
	return observed, nil
}

func editBody(gh, repoDir string, dest Destination, bodyFile string, timeout int) error {
	runCtx, cancel := context.WithTimeout(context.Background(), bound(timeout))
	defer cancel()
	cmd := exec.CommandContext(runCtx, gh, "pr", "edit", fmt.Sprint(dest.Number), "--repo", dest.Repository, "--body-file", bodyFile)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if runCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("gh pr edit did not finish in time")
	}
	if err != nil {
		return fmt.Errorf("gh pr edit failed: %s", firstLine(string(out)))
	}
	return nil
}

func bound(timeout int) time.Duration {
	if timeout > 0 {
		return time.Duration(timeout) * time.Second
	}
	return defaultGhBound
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return strings.TrimSpace(text)
}

func destination(task *ordjson.Object) (Destination, error) {
	pr, _ := field(task, "pr").(*ordjson.Object)
	identity, _ := field(pr, "identity").(*ordjson.Object)
	if identity == nil {
		return Destination{}, fmt.Errorf("the pipeline block needs a reconciled PR; record it with `pr reconcile` first.")
	}
	number := 0
	if raw, isNumber := field(identity, "number").(json.Number); isNumber {
		value, err := raw.Int64()
		if err != nil {
			return Destination{}, fmt.Errorf("the recorded PR identity has no usable number; run `pr reconcile` again.")
		}
		number = int(value)
	}
	head := stringField(identity, "head_sha")
	if number == 0 || !sha40.MatchString(head) {
		return Destination{}, fmt.Errorf("the recorded PR identity has no number and head SHA to publish into; run `pr reconcile` again.")
	}
	repo := repositoryFromURL(stringField(identity, "url"))
	if !repoName.MatchString(repo) {
		return Destination{}, fmt.Errorf("the recorded PR URL does not name an owner/repo destination; run `pr reconcile` again.")
	}
	return Destination{Repository: repo, Number: number, HeadSHA: head, State: stringField(pr, "state")}, nil
}

func repositoryFromURL(url string) string {
	parts := strings.Split(strings.TrimPrefix(url, "https://"), "/")
	if len(parts) < 3 {
		return ""
	}
	return parts[1] + "/" + parts[2]
}

func loadReceipts(path string) (*receipts, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return &receipts{Schema: receiptsSchema}, nil
	}
	var loaded receipts
	if err := json.Unmarshal(data, &loaded); err != nil {
		return nil, fmt.Errorf("pipeline receipts at %s are not readable JSON; inspect them by hand", path)
	}
	if loaded.Schema != receiptsSchema {
		return nil, fmt.Errorf("pipeline receipts at %s have schema %d, expected %d", path, loaded.Schema, receiptsSchema)
	}
	return &loaded, nil
}

func saveReceipts(path string, value *receipts) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func (r *receipts) owns(dest Destination, hash string) bool {
	for _, block := range r.Blocks {
		if block.Destination == dest.key() && block.SHA256 == hash {
			return true
		}
	}
	return false
}

func (r *receipts) event(kind string, dest Destination, reason string) {
	r.Events = append(r.Events, receiptEvent{At: store.Now(), Kind: kind, Destination: dest.key(), Reason: reason})
	if len(r.Events) > receiptsHistory {
		r.Events = r.Events[len(r.Events)-receiptsHistory:]
	}
}

func record(s *store.Store, ctx *ordjson.Object, args PublishArgs, dest Destination, publication Publication) (Publication, error) {
	unlock, err := s.Lock()
	if err != nil {
		return publication, err
	}
	defer unlock()
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return publication, err
	}
	body := ordjson.NewObject()
	body.Set("block", "pipeline")
	body.Set("outcome", string(publication.Outcome))
	body.Set("reason", nilIfEmpty(publication.Reason))
	body.Set("destination", destinationObject(dest))
	body.Set("block_sha256", nilIfEmpty(publication.BlockSHA256))
	body.Set("trigger", args.Trigger)
	body.Set("dry_run", args.DryRun)
	if _, err := evidence.Append(task, "publication", "coordinator", body, dest.HeadSHA, ctx); err != nil {
		return publication, err
	}
	if err := RefreshLocked(s, task); err != nil {
		return publication, err
	}
	return publication, s.SaveTask(task)
}

func destinationObject(dest Destination) *ordjson.Object {
	row := ordjson.NewObject()
	row.Set("repository", dest.Repository)
	row.Set("number", json.Number(fmt.Sprint(dest.Number)))
	row.Set("head_sha", dest.HeadSHA)
	row.Set("state", dest.State)
	return row
}

func (p Publication) Row() *ordjson.Object {
	row := ordjson.NewObject()
	row.Set("block", "pipeline")
	row.Set("outcome", string(p.Outcome))
	row.Set("reason", nilIfEmpty(p.Reason))
	row.Set("destination", destinationObject(p.Destination))
	row.Set("block_sha256", nilIfEmpty(p.BlockSHA256))
	return row
}
