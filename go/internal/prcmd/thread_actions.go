package prcmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/maintenance"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/repair"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

type ThreadRevision struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

type ThreadActionArgs struct {
	Task        string
	Action      string
	Head        string
	PRURL       string
	Threads     []ThreadRevision
	All         bool
	Text        string
	Attempt     string
	Class       string
	Reason      string
	RuntimeRoot string
}

func ThreadAction(s *store.Store, ctx *ordjson.Object, args ThreadActionArgs) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	if args.Action != "address" && args.Action != "discuss" && args.Action != "draft-reply" && args.Action != "open-comment" {
		return nil, fmt.Errorf("action must be address, discuss, draft-reply, or open-comment")
	}
	if args.Action != "address" && (args.All || len(args.Threads) != 1) {
		return nil, fmt.Errorf("%s requires exactly one --thread and --revision", args.Action)
	}
	if args.Action == "address" && args.All && len(args.Threads) != 0 {
		return nil, fmt.Errorf("--all cannot be combined with explicit threads")
	}
	if args.Action == "address" && !args.All && len(args.Threads) == 0 {
		return nil, fmt.Errorf("address requires --all or at least one exact thread and revision")
	}
	if args.Action == "address" && strings.TrimSpace(args.Text) == "" {
		return nil, fmt.Errorf("address requires corrective --text")
	}
	if (args.Action == "discuss" || args.Action == "draft-reply") && strings.TrimSpace(args.Text) == "" {
		return nil, fmt.Errorf("%s requires --text", args.Action)
	}
	if err := validateAddressScope(&args); err != nil {
		return nil, err
	}
	if len(args.Text) > app.MaxText {
		return nil, fmt.Errorf("text exceeds %d bytes", app.MaxText)
	}
	task, err := s.ReadTask(args.Task)
	if err != nil {
		return nil, err
	}
	pr, observation, remote, number, url, err := threadActionBinding(task)
	if err != nil {
		return nil, err
	}
	if args.PRURL == "" || args.PRURL != url {
		return nil, fmt.Errorf("saved PR binding changed; reconcile the PR and refresh the thread action")
	}
	if args.Head == "" || args.Head != asString(identityObject(pr), "head_sha") || args.Head != asString(observation, "observed_head_sha") {
		return nil, fmt.Errorf("saved PR head changed; reconcile the PR and refresh the thread action")
	}
	selected, err := threadSelection(observation, args)
	if err != nil {
		return nil, err
	}
	gh, err := toolpath.Find(args.RuntimeRoot, "gh")
	if err != nil {
		return nil, err
	}
	freshPR, err := viewPR(gh, asString(task, "repository"), remote, number)
	if err != nil {
		return nil, err
	}
	if fmt.Sprint(freshPR["url"]) != url || fmt.Sprint(freshPR["headRefOid"]) != args.Head {
		return nil, fmt.Errorf("GitHub PR binding or head changed; reconcile the PR before acting")
	}
	fresh := observeReviewThreads(gh, asString(task, "repository"), remote, number, freshPR)
	if asString(fresh, "status") != "complete" {
		return nil, fmt.Errorf("GitHub review threads could not be revalidated completely; refresh the observation before acting")
	}
	if err := validateLiveSelection(fresh, selected, args.All); err != nil {
		return nil, err
	}
	if args.Action == "open-comment" {
		thread := findThread(fresh, selected[0].ID)
		result := ordjson.NewObject()
		result.Set("task", args.Task)
		result.Set("url", asString(thread, "source_url"))
		result.Set("thread", selected[0].ID)
		result.Set("revision", selected[0].Revision)
		result.Set("note", "Open the saved GitHub comment URL. No reply or resolution was made.")
		return result, nil
	}
	requestKey, requestID := threadRequestIdentity(args, selected, url)
	request := threadRequest(args, selected, url, requestKey, requestID)
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	current, err := s.ReadTask(args.Task)
	if err != nil {
		unlock()
		return nil, err
	}
	if err := maintenance.Refuse(current, "PR thread instruction"); err != nil {
		unlock()
		return nil, err
	}

	_, currentObservation, _, _, currentURL, err := threadActionBinding(current)
	if err != nil || currentURL != url || asString(currentObservation, "observed_head_sha") != args.Head || !sameThreadSelection(currentObservation, selected) {
		unlock()
		return nil, fmt.Errorf("saved PR or thread revisions changed while the action was being checked; reconcile and retry")
	}
	actions := asList(identityValue(current, "github_review_actions"))
	for _, raw := range actions {
		existing := asObject(raw)
		if asString(existing, "key") == requestKey {
			unlock()
			result := ordjson.NewObject()
			result.Set("task", args.Task)
			result.Set("request", existing)
			result.Set("duplicate", true)
			return result, nil
		}
		if args.Action == "address" && addressRequestBlocksRetry(existing, selected) {
			unlock()
			return nil, fmt.Errorf("a prior address request for this thread revision is %s; inspect its saved delivery state before creating another request", asString(existing, "status"))
		}
	}
	current.Set("github_review_actions", append(actions, request))
	if err := s.SaveTask(current); err != nil {
		unlock()
		return nil, err
	}
	unlock()
	if args.Action != "address" {
		result := ordjson.NewObject()
		result.Set("task", args.Task)
		result.Set("request", request)
		result.Set("duplicate", false)
		result.Set("note", "This local discussion or draft does not post to or resolve the GitHub thread.")
		return result, nil
	}
	_, err = repair.Send(s, ctx, repair.SendArgs{TaskID: args.Task, Attempt: args.Attempt, Key: requestKey, Text: args.Text, Class: args.Class, Reason: args.Reason, RuntimeRoot: args.RuntimeRoot})
	if err != nil {
		operationID, operationState := findRepairOperation(s, args.Task, requestKey)
		if operationID == "" {
			operationState = "refused"
		}
		_ = updateThreadRequest(s, args.Task, requestID, operationState, operationID, err.Error())
		return nil, err
	}
	operationID, operationState := findRepairOperation(s, args.Task, requestKey)
	if err := updateThreadRequest(s, args.Task, requestID, operationState, operationID, ""); err != nil {
		return nil, err
	}
	saved, _ := s.ReadTask(args.Task)
	result := ordjson.NewObject()
	result.Set("task", args.Task)
	result.Set("request", findThreadRequest(saved, requestID))
	result.Set("duplicate", false)
	result.Set("note", "Repair delivery and GitHub thread resolution are separate; this action did not post a reply or resolve the thread.")
	return result, nil
}

func validateAddressScope(args *ThreadActionArgs) error {
	if args.Action != "address" {
		return nil
	}
	if args.Attempt == "" {
		return fmt.Errorf("address requires the exact worker --attempt")
	}
	if args.Class == "" {
		args.Class = repair.ClassInScope
	}
	if args.Class != repair.ClassInScope && args.Class != repair.ClassExpansion {
		return fmt.Errorf("--class must be %s or %s", repair.ClassInScope, repair.ClassExpansion)
	}
	if args.Class == repair.ClassExpansion && strings.TrimSpace(args.Reason) == "" {
		return fmt.Errorf("expansion address requires --reason")
	}
	return nil
}

func threadActionBinding(task *ordjson.Object) (*ordjson.Object, *ordjson.Object, string, int, string, error) {
	pr := asObject(identityValue(task, "pr"))
	identity := asObject(identityValue(pr, "identity"))
	observation := asObject(identityValue(pr, "review_threads"))
	if identity == nil || observation == nil {
		return nil, nil, "", 0, "", fmt.Errorf("task has no saved GitHub review thread observation")
	}
	remote := asString(observation, "repository")
	number := intOf(identityValue(identity, "number"))
	url := asString(identity, "url")
	if remote == "" || number < 1 || url == "" {
		return nil, nil, "", 0, "", fmt.Errorf("saved PR identity is incomplete")
	}
	return pr, observation, remote, number, url, nil
}

func identityObject(pr *ordjson.Object) *ordjson.Object {
	return asObject(identityValue(pr, "identity"))
}

func threadSelection(observation *ordjson.Object, args ThreadActionArgs) ([]ThreadRevision, error) {
	if args.All {
		if asString(observation, "status") != "complete" {
			return nil, fmt.Errorf("Address all requires a complete saved observation; reconcile the PR first")
		}
		selection := []ThreadRevision{}
		for _, raw := range asList(identityValue(observation, "threads")) {
			thread := asObject(raw)
			if !objectBool(thread, "resolved") && asString(thread, "latest_comment_id") != "" {
				selection = append(selection, ThreadRevision{ID: asString(thread, "id"), Revision: asString(thread, "latest_comment_id")})
			}
		}
		if len(selection) == 0 {
			return nil, fmt.Errorf("there are no actionable GitHub review threads")
		}
		return normalizeThreadSelection(selection)
	}
	selection, err := normalizeThreadSelection(args.Threads)
	if err != nil {
		return nil, err
	}
	for _, selected := range selection {
		thread := findThread(observation, selected.ID)
		if thread == nil || objectBool(thread, "resolved") || asString(thread, "latest_comment_id") != selected.Revision {
			return nil, fmt.Errorf("selected thread %s is stale or no longer actionable; reconcile the PR before acting", selected.ID)
		}
	}
	return selection, nil
}

func normalizeThreadSelection(selection []ThreadRevision) ([]ThreadRevision, error) {
	if len(selection) == 0 {
		return nil, fmt.Errorf("thread selection is empty")
	}
	if len(selection) > reviewThreadPageSize*reviewThreadPageLimit {
		return nil, fmt.Errorf("thread selection exceeds the bounded %d-thread observation limit", reviewThreadPageSize*reviewThreadPageLimit)
	}
	out := append([]ThreadRevision(nil), selection...)
	for _, row := range out {
		if row.ID == "" || row.Revision == "" || len(row.ID) > 512 || len(row.Revision) > 512 {
			return nil, fmt.Errorf("each thread selection needs an exact thread ID and current comment revision")
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	for i := 1; i < len(out); i++ {
		if out[i-1].ID == out[i].ID {
			return nil, fmt.Errorf("thread %s appears more than once", out[i].ID)
		}
	}
	return out, nil
}

func validateLiveSelection(fresh *ordjson.Object, selected []ThreadRevision, all bool) error {
	if all {
		current := []ThreadRevision{}
		for _, raw := range asList(identityValue(fresh, "threads")) {
			thread := asObject(raw)
			if !objectBool(thread, "resolved") && asString(thread, "latest_comment_id") != "" {
				current = append(current, ThreadRevision{ID: asString(thread, "id"), Revision: asString(thread, "latest_comment_id")})
			}
		}
		current, _ = normalizeThreadSelection(current)
		if !equalThreadSelection(current, selected) {
			return fmt.Errorf("the actionable thread set changed; reconcile and review the current inbox rows before using Address all")
		}
		return nil
	}
	for _, row := range selected {
		thread := findThread(fresh, row.ID)
		if thread == nil || objectBool(thread, "resolved") || asString(thread, "latest_comment_id") != row.Revision {
			return fmt.Errorf("thread %s changed or resolved; reconcile and refresh the inbox row", row.ID)
		}
	}
	return nil
}

func sameThreadSelection(observation *ordjson.Object, selected []ThreadRevision) bool {
	for _, row := range selected {
		thread := findThread(observation, row.ID)
		if thread == nil || objectBool(thread, "resolved") || asString(thread, "latest_comment_id") != row.Revision {
			return false
		}
	}
	return true
}

func equalThreadSelection(a, b []ThreadRevision) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func findThread(observation *ordjson.Object, id string) *ordjson.Object {
	for _, raw := range asList(identityValue(observation, "threads")) {
		thread := asObject(raw)
		if asString(thread, "id") == id {
			return thread
		}
	}
	return nil
}

func threadRequestIdentity(args ThreadActionArgs, selected []ThreadRevision, url string) (string, string) {
	canonical := append([]ThreadRevision(nil), selected...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].ID < canonical[j].ID })
	identity := struct {
		Task    string           `json:"task"`
		Action  string           `json:"action"`
		Head    string           `json:"head"`
		PR      string           `json:"pr"`
		Threads []ThreadRevision `json:"threads"`
		Text    string           `json:"text"`
		Attempt string           `json:"attempt"`
		Class   string           `json:"class"`
		Reason  string           `json:"reason"`
	}{args.Task, args.Action, args.Head, url, canonical, args.Text, args.Attempt, args.Class, args.Reason}
	data, _ := json.Marshal(identity)
	hash := sha256.Sum256(data)
	suffix := hex.EncodeToString(hash[:])[:20]
	return "github-review-" + suffix, "ghra-" + suffix
}

func threadRequest(args ThreadActionArgs, selected []ThreadRevision, url, key, id string) *ordjson.Object {
	rows := make([]any, 0, len(selected))
	for _, selectedThread := range selected {
		row := ordjson.NewObject()
		row.Set("id", selectedThread.ID)
		row.Set("revision", selectedThread.Revision)
		rows = append(rows, row)
	}
	request := ordjson.NewObject()
	request.Set("id", id)
	request.Set("key", key)
	request.Set("action", args.Action)
	request.Set("head_sha", args.Head)
	request.Set("pr_url", url)
	request.Set("threads", rows)
	request.Set("all", args.All)
	request.Set("text", args.Text)
	request.Set("status", "recorded")
	request.Set("created_at", store.Now())
	if args.Action == "address" {
		request.Set("status", "in-flight")
		request.Set("repair_key", key)
		request.Set("attempt", args.Attempt)
		request.Set("class", args.Class)
		if args.Reason != "" {
			request.Set("reason", args.Reason)
		}
	}
	return request
}

func updateThreadRequest(s *store.Store, taskID, requestID, state, operationID, detail string) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	task, err := s.ReadTask(taskID)
	if err != nil {
		return err
	}
	for _, raw := range asList(identityValue(task, "github_review_actions")) {
		request := asObject(raw)
		if asString(request, "id") == requestID {
			request.Set("status", state)
			request.Set("updated_at", store.Now())
			if operationID != "" {
				request.Set("repair_operation_id", operationID)
			}
			if detail != "" {
				request.Set("detail", detail)
			}
			return s.SaveTask(task)
		}
	}
	return fmt.Errorf("saved review action request %s disappeared", requestID)
}

func findRepairOperation(s *store.Store, taskID, key string) (string, string) {
	task, err := s.ReadTask(taskID)
	if err != nil {
		return "", "uncertain"
	}
	ledger, err := repair.Ledger(task)
	if err != nil {
		return "", "uncertain"
	}
	for _, raw := range asList(identityValue(ledger, "operations")) {
		operation := asObject(raw)
		if asString(operation, "key") == key {
			return asString(operation, "id"), asString(operation, "state")
		}
	}
	return "", "uncertain"
}

func findThreadRequest(task *ordjson.Object, id string) *ordjson.Object {
	for _, raw := range asList(identityValue(task, "github_review_actions")) {
		request := asObject(raw)
		if asString(request, "id") == id {
			return request
		}
	}
	return nil
}

func addressStateUncertain(status string) bool {
	return status == "in-flight" || status == "pending-resume" || status == "submitted" || status == "uncertain"
}

func addressRequestBlocksRetry(request *ordjson.Object, selected []ThreadRevision) bool {
	return asString(request, "action") == "address" && addressStateUncertain(asString(request, "status")) && selectionOverlaps(asList(identityValue(request, "threads")), selected)
}

func selectionOverlaps(existing []any, selected []ThreadRevision) bool {
	for _, raw := range existing {
		thread := asObject(raw)
		for _, row := range selected {
			if asString(thread, "id") == row.ID && asString(thread, "revision") == row.Revision {
				return true
			}
		}
	}
	return false
}

func OpenCommentURL(task *ordjson.Object, threadID string) string {
	_, observation, _, _, _, err := threadActionBinding(task)
	if err != nil {
		return ""
	}
	return asString(findThread(observation, threadID), "source_url")
}
