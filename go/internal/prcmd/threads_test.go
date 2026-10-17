package prcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func quoteShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func TestObserveReviewThreadsPreservesIdentityAndLatestComment(t *testing.T) {
	payload := `{"data":{"repository":{"pullRequest":{"id":"PR_kwDO","number":7,"url":"https://github.com/o/r/pull/7","headRefOid":"HEAD","reviewDecision":"CHANGES_REQUESTED","reviewThreads":{"nodes":[{"id":"PRRT_1","isOutdated":false,"isResolved":false,"path":"go/main.go","line":12,"comments":{"nodes":[{"id":"PRRC_1","databaseId":31,"url":"https://github.com/o/r/pull/7#discussion_r31","body":"first","createdAt":"2026-10-16T00:00:00Z","updatedAt":"2026-10-16T00:00:00Z","author":{"login":"reviewer"},"replyTo":null},{"id":"PRRC_2","databaseId":32,"url":"https://github.com/o/r/pull/7#discussion_r32","body":"follow-up","createdAt":"2026-10-16T01:00:00Z","updatedAt":"2026-10-16T02:00:00Z","author":{"login":"author"},"replyTo":{"id":"PRRC_1"}}]}}],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}}`
	gh := fakeGh(t, `printf '%s' `+quoteShell(payload))
	pr := map[string]any{"url": "https://github.com/o/r/pull/7", "headRefOid": "HEAD"}
	got := observeReviewThreads(gh, t.TempDir(), "o/r", 7, pr)
	if asString(got, "status") != "complete" || asString(got, "review_decision") != "CHANGES_REQUESTED" {
		t.Fatalf("observation = %v", got)
	}
	threads := asList(func() any { v, _ := got.Get("threads"); return v }())
	if len(threads) != 1 {
		t.Fatalf("threads = %v", threads)
	}
	thread := asObject(threads[0])
	if asString(thread, "id") != "PRRT_1" || asString(thread, "source_url") != "https://github.com/o/r/pull/7#discussion_r32" || asString(thread, "latest_comment_id") != "PRRC_2" {
		t.Fatalf("thread identity/latest = %v", thread)
	}
	comments := asList(func() any { v, _ := thread.Get("comments"); return v }())
	if len(comments) != 2 || asString(asObject(comments[1]), "author") != "reviewer" {
		t.Fatalf("comments = %v", comments)
	}
}

func TestObserveReviewThreadsMarksNonAdvancingPaginationIncomplete(t *testing.T) {
	payload := `{"data":{"repository":{"pullRequest":{"number":7,"url":"https://github.com/o/r/pull/7","headRefOid":"HEAD","reviewDecision":"APPROVED","reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}}}}}`
	gh := fakeGh(t, `printf '%s' `+quoteShell(payload))
	old := ordjson.NewObject()
	old.Set("id", "PRRT_OLD")
	previous := ordjson.NewObject()
	previous.Set("status", "complete")
	previous.Set("threads", []any{old})
	got := observeReviewThreads(gh, t.TempDir(), "o/r", 7, map[string]any{"url": "https://github.com/o/r/pull/7", "headRefOid": "HEAD"})
	retainObservedThreads(got, previous)
	if asString(got, "status") != "incomplete" || !strings.Contains(asString(got, "detail"), "no advancing cursor") {
		t.Fatalf("observation = %v", got)
	}
	rows := asList(func() any { v, _ := got.Get("threads"); return v }())
	if len(rows) != 1 || asString(asObject(rows[0]), "id") != "PRRT_OLD" {
		t.Fatalf("retained rows = %v", rows)
	}
	retained := asList(func() any { v, _ := previous.Get("threads"); return v }())
	if len(retained) != 1 || asString(asObject(retained[0]), "id") != "PRRT_OLD" {
		t.Fatalf("source record changed: %v", retained)
	}
}

func TestObserveReviewThreadsReadsEveryPage(t *testing.T) {
	first := `{"data":{"repository":{"pullRequest":{"number":7,"url":"https://github.com/o/r/pull/7","headRefOid":"HEAD","reviewDecision":"APPROVED","reviewThreads":{"nodes":[{"id":"PRRT_1","comments":{"nodes":[]}}],"pageInfo":{"hasNextPage":true,"endCursor":"cursor-1"}}}}}}`
	last := `{"data":{"repository":{"pullRequest":{"number":7,"url":"https://github.com/o/r/pull/7","headRefOid":"HEAD","reviewDecision":"APPROVED","reviewThreads":{"nodes":[{"id":"PRRT_2","comments":{"nodes":[]}}],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}}`
	seen := filepath.Join(t.TempDir(), "seen")
	gh := fakeGh(t, `if [ -f `+quoteShell(seen)+` ]; then printf '%s' `+quoteShell(last)+`; else touch `+quoteShell(seen)+`; printf '%s' `+quoteShell(first)+`; fi`)
	got := observeReviewThreads(gh, t.TempDir(), "o/r", 7, map[string]any{"url": "https://github.com/o/r/pull/7", "headRefOid": "HEAD"})
	rows := asList(func() any { v, _ := got.Get("threads"); return v }())
	if asString(got, "status") != "complete" || len(rows) != 2 || asString(asObject(rows[1]), "id") != "PRRT_2" {
		t.Fatalf("observation = %v", got)
	}
	if _, err := os.Stat(seen); err != nil {
		t.Fatalf("second page was not requested: %v", err)
	}
}

func TestObserveReviewThreadsRejectsWrongHeadAndRecordsReadFailure(t *testing.T) {
	payload := `{"data":{"repository":{"pullRequest":{"number":7,"url":"https://github.com/o/r/pull/7","headRefOid":"OTHER","reviewDecision":"APPROVED","reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}}`
	gh := fakeGh(t, `printf '%s' `+quoteShell(payload))
	wrongHead := observeReviewThreads(gh, t.TempDir(), "o/r", 7, map[string]any{"url": "https://github.com/o/r/pull/7", "headRefOid": "HEAD"})
	if asString(wrongHead, "status") != "mismatched" || len(asList(func() any { v, _ := wrongHead.Get("threads"); return v }())) != 0 {
		t.Fatalf("wrong-head observation = %v", wrongHead)
	}
	failing := fakeGh(t, `echo 'GraphQL: permission denied' >&2; exit 1`)
	failed := observeReviewThreads(failing, t.TempDir(), "o/r", 7, map[string]any{"url": "https://github.com/o/r/pull/7", "headRefOid": "HEAD"})
	if asString(failed, "status") != "incomplete" || !strings.Contains(asString(failed, "detail"), "permission denied") {
		t.Fatalf("failed observation = %v", failed)
	}
}

func TestRetainObservedThreadsOnIncompletePage(t *testing.T) {
	old := ordjson.NewObject()
	old.Set("id", "PRRT_OLD")
	currentRow := ordjson.NewObject()
	currentRow.Set("id", "PRRT_NEW")
	previous := ordjson.NewObject()
	previous.Set("review_decision", "CHANGES_REQUESTED")
	previous.Set("threads", []any{old})
	current := ordjson.NewObject()
	current.Set("status", "incomplete")
	current.Set("threads", []any{currentRow})
	retainObservedThreads(current, previous)
	rows := asList(func() any { v, _ := current.Get("threads"); return v }())
	if len(rows) != 2 || asString(asObject(rows[1]), "id") != "PRRT_OLD" {
		t.Fatalf("threads = %v", rows)
	}
	if value, _ := previous.Get("review_decision"); value != "CHANGES_REQUESTED" {
		t.Fatalf("previous was mutated: %v", value)
	}
}

func TestRecordObservationUpdatesAndRetainsThreadRevisions(t *testing.T) {
	head := strings.Repeat("a", 40)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	taskValue, err := ordjson.Decode([]byte(observationTask(head, "")))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTask(taskValue.(*ordjson.Object)); err != nil {
		t.Fatal(err)
	}
	ctx := ordjson.NewObject()
	ctx.Set("machine", "m-test")
	ctx.Set("session", "sum-test")
	ctx.Set("pane", "w-parent:p1")
	base, err := ordjson.Decode([]byte(ghView("OPEN", head, "")))
	if err != nil {
		t.Fatal(err)
	}
	data := map[string]any{}
	for _, key := range base.(*ordjson.Object).Keys() {
		value, _ := base.(*ordjson.Object).Get(key)
		data[key] = value
	}
	thread := ordjson.NewObject()
	thread.Set("id", "PRRT_1")
	thread.Set("resolved", true)
	thread.Set("latest_comment_id", "PRRC_1")
	complete := ordjson.NewObject()
	complete.Set("status", "complete")
	complete.Set("review_decision", "CHANGES_REQUESTED")
	complete.Set("threads", []any{thread})
	data["review_threads"] = complete
	for range 2 {
		if _, _, err := recordObservationWith(st, ctx, "t-aaaaaaaaaaaa", data, false); err != nil {
			t.Fatal(err)
		}
	}
	thread.Set("resolved", false)
	thread.Set("latest_comment_id", "PRRC_2")
	if _, _, err := recordObservationWith(st, ctx, "t-aaaaaaaaaaaa", data, false); err != nil {
		t.Fatal(err)
	}
	newThread := ordjson.NewObject()
	newThread.Set("id", "PRRT_2")
	incomplete := ordjson.NewObject()
	incomplete.Set("status", "incomplete")
	incomplete.Set("threads", []any{newThread})
	data["review_threads"] = incomplete
	if _, _, err := recordObservationWith(st, ctx, "t-aaaaaaaaaaaa", data, false); err != nil {
		t.Fatal(err)
	}
	saved, err := st.ReadTask("t-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	prValue, _ := saved.Get("pr")
	pr := asObject(prValue)
	reviewValue, _ := pr.Get("review_threads")
	review := asObject(reviewValue)
	rows := asList(func() any { v, _ := review.Get("threads"); return v }())
	if len(rows) != 2 || asString(asObject(rows[0]), "id") != "PRRT_2" || asString(asObject(rows[1]), "id") != "PRRT_1" {
		t.Fatalf("saved thread rows = %v", rows)
	}
	previous := asObject(rows[1])
	if asString(previous, "latest_comment_id") != "PRRC_2" || asString(review, "review_decision") != "CHANGES_REQUESTED" || asString(review, "status") != "incomplete" {
		t.Fatalf("saved review observation = %v", review)
	}
}
