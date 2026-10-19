package prcmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const reviewThreadPageSize = 50
const reviewThreadPageLimit = 20

const reviewThreadsQuery = `query($owner:String!,$name:String!,$number:Int!,$cursor:String){repository(owner:$owner,name:$name){pullRequest(number:$number){id number url headRefOid reviewDecision reviewThreads(first:%d,after:$cursor){nodes{id isOutdated isResolved path line originalLine startLine originalStartLine comments(last:100){nodes{id databaseId url body createdAt updatedAt author{login} replyTo{id}}}} pageInfo{hasNextPage endCursor}}}}}`

func observeReviewThreads(gh, dir, remote string, number int, pr map[string]any) *ordjson.Object {
	record := ordjson.NewObject()
	observedAt := store.Now()
	record.Set("observed_at", observedAt)
	record.Set("observed_head_sha", fmt.Sprint(pr["headRefOid"]))
	record.Set("status", "unavailable")
	record.Set("threads", []any{})
	owner, name, ok := strings.Cut(remote, "/")
	if !ok || owner == "" || name == "" {
		record.Set("detail", "repository identity is unavailable")
		return record
	}
	record.Set("repository", remote)
	record.Set("pr_number", number)
	var rows []any
	cursor := ""
	decision := any(nil)
	deadline := time.Now().Add(GHBound)
	for page := 0; page < reviewThreadPageLimit; page++ {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			record.Set("status", "incomplete")
			record.Set("detail", "review thread observation time bound reached")
			record.Set("threads", rows)
			return record
		}
		argv := []string{gh, "api", "graphql", "-f", "query=" + fmt.Sprintf(reviewThreadsQuery, reviewThreadPageSize), "-F", "owner=" + owner, "-F", "name=" + name, "-F", fmt.Sprintf("number=%d", number)}
		if cursor != "" {
			argv = append(argv, "-F", "cursor="+cursor)
		}
		out, err := readOnlyGH(argv, dir, remaining)
		if err != nil || out.Result.Code != 0 {
			record.Set("status", "incomplete")
			detail := sanitizedDetail(out.Result)
			if err != nil {
				detail = err.Error()
			}
			if detail == "" {
				detail = "GitHub returned an unsuccessful response"
			}
			record.Set("detail", boundedThreadDetail(fmt.Sprintf("review thread page %d unavailable: %s", page+1, detail)))
			record.Set("threads", rows)
			return record
		}
		var payload struct {
			Data struct {
				Repository *struct {
					PullRequest *struct {
						ID             string `json:"id"`
						Number         int    `json:"number"`
						URL            string `json:"url"`
						HeadRefOID     string `json:"headRefOid"`
						ReviewDecision string `json:"reviewDecision"`
						ReviewThreads  *struct {
							Nodes    *[]reviewThreadNode `json:"nodes"`
							PageInfo *struct {
								HasNextPage bool   `json:"hasNextPage"`
								EndCursor   string `json:"endCursor"`
							} `json:"pageInfo"`
						} `json:"reviewThreads"`
					} `json:"pullRequest"`
				} `json:"repository"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.Unmarshal([]byte(out.Result.Stdout), &payload); err != nil {
			record.Set("status", "incomplete")
			record.Set("detail", "review thread response was not valid JSON")
			record.Set("threads", rows)
			return record
		}
		if len(payload.Errors) > 0 || payload.Data.Repository == nil || payload.Data.Repository.PullRequest == nil {
			detail := "review thread response was incomplete"
			if len(payload.Errors) > 0 {
				detail = secretShapes.ReplaceAllString(payload.Errors[0].Message, "[redacted]")
			}
			record.Set("status", "incomplete")
			record.Set("detail", boundedThreadDetail(detail))
			record.Set("threads", rows)
			return record
		}
		got := payload.Data.Repository.PullRequest
		if got.Number != number || got.URL != fmt.Sprint(pr["url"]) || got.HeadRefOID != fmt.Sprint(pr["headRefOid"]) {
			record.Set("status", "mismatched")
			record.Set("detail", "review threads did not match the observed repository, PR, and head")
			record.Set("threads", []any{})
			return record
		}
		if got.ReviewThreads == nil || got.ReviewThreads.Nodes == nil || got.ReviewThreads.PageInfo == nil {
			record.Set("status", "incomplete")
			record.Set("detail", "review thread response omitted its nodes or pagination metadata")
			record.Set("threads", rows)
			return record
		}
		decision = got.ReviewDecision
		for _, node := range *got.ReviewThreads.Nodes {
			if !validReviewThreadNode(node) {
				record.Set("status", "incomplete")
				record.Set("detail", "review thread response contained a thread without stable comment identity")
				record.Set("threads", rows)
				return record
			}
			thread := node.record()
			thread.Set("observed_at", observedAt)
			thread.Set("observed_head_sha", fmt.Sprint(pr["headRefOid"]))
			rows = append(rows, thread)
		}
		if !got.ReviewThreads.PageInfo.HasNextPage {
			record.Set("status", "complete")
			record.Set("threads", rows)
			record.Set("review_decision", decision)
			return record
		}
		next := got.ReviewThreads.PageInfo.EndCursor
		if next == "" || next == cursor {
			record.Set("status", "incomplete")
			record.Set("detail", "review thread pagination returned no advancing cursor")
			record.Set("threads", rows)
			return record
		}
		cursor = next
	}
	record.Set("status", "incomplete")
	record.Set("detail", "review thread page limit reached")
	record.Set("threads", rows)
	return record
}

type reviewThreadNode struct {
	ID                string  `json:"id"`
	IsOutdated        *bool   `json:"isOutdated"`
	IsResolved        *bool   `json:"isResolved"`
	Path              *string `json:"path"`
	Line              *int    `json:"line"`
	OriginalLine      *int    `json:"originalLine"`
	StartLine         *int    `json:"startLine"`
	OriginalStartLine *int    `json:"originalStartLine"`
	Comments          struct {
		Nodes *[]struct {
			ID        string `json:"id"`
			Database  int64  `json:"databaseId"`
			URL       string `json:"url"`
			Body      string `json:"body"`
			CreatedAt string `json:"createdAt"`
			UpdatedAt string `json:"updatedAt"`
			Author    *struct {
				Login string `json:"login"`
			} `json:"author"`
			ReplyTo *struct {
				ID string `json:"id"`
			} `json:"replyTo"`
		} `json:"nodes"`
	} `json:"comments"`
}

func validReviewThreadNode(node reviewThreadNode) bool {
	if node.ID == "" || node.IsOutdated == nil || node.IsResolved == nil || node.Path == nil || node.Comments.Nodes == nil {
		return false
	}
	for _, comment := range *node.Comments.Nodes {
		if comment.ID == "" {
			return false
		}
	}
	return true
}

func (n reviewThreadNode) record() *ordjson.Object {
	thread := ordjson.NewObject()
	thread.Set("id", n.ID)
	thread.Set("path", *n.Path)
	thread.Set("line", n.Line)
	thread.Set("original_line", n.OriginalLine)
	thread.Set("start_line", n.StartLine)
	thread.Set("original_start_line", n.OriginalStartLine)
	thread.Set("resolved", *n.IsResolved)
	thread.Set("outdated", *n.IsOutdated)
	sort.Slice(*n.Comments.Nodes, func(i, j int) bool { return (*n.Comments.Nodes)[i].UpdatedAt > (*n.Comments.Nodes)[j].UpdatedAt })
	commentRows := make([]any, 0, len(*n.Comments.Nodes))
	for _, c := range *n.Comments.Nodes {
		row := ordjson.NewObject()
		row.Set("id", c.ID)
		row.Set("database_id", c.Database)
		row.Set("url", c.URL)
		row.Set("author", nil)
		if c.Author != nil {
			row.Set("author", c.Author.Login)
		}
		row.Set("created_at", c.CreatedAt)
		row.Set("updated_at", c.UpdatedAt)
		row.Set("reply_to", nil)
		if c.ReplyTo != nil {
			row.Set("reply_to", c.ReplyTo.ID)
		}
		body := c.Body
		if len(body) > 4096 {
			body = body[:4096]
		}
		row.Set("body", body)
		commentRows = append(commentRows, row)
	}
	thread.Set("comments", commentRows)
	if len(*n.Comments.Nodes) > 0 {
		latest := (*n.Comments.Nodes)[0]
		thread.Set("latest_comment_id", latest.ID)
		thread.Set("latest_updated_at", latest.UpdatedAt)
		thread.Set("source_url", latest.URL)
		if latest.Author != nil {
			thread.Set("latest_author", latest.Author.Login)
		}
	}
	return thread
}

func boundedThreadDetail(detail string) string {
	detail = secretShapes.ReplaceAllString(detail, "[redacted]")
	if len(detail) > detailBound {
		return detail[:detailBound]
	}
	return detail
}

func retainObservedThreads(current, previous *ordjson.Object) {
	if current == nil || previous == nil || asString(current, "status") == "complete" {
		return
	}
	seen := map[string]bool{}
	rows := asList(func() any { v, _ := current.Get("threads"); return v }())
	for _, raw := range rows {
		seen[asString(asObject(raw), "id")] = true
	}
	for _, raw := range asList(func() any { v, _ := previous.Get("threads"); return v }()) {
		thread := asObject(raw)
		if thread != nil && !seen[asString(thread, "id")] {
			rows = append(rows, thread)
		}
	}
	current.Set("threads", rows)
}
