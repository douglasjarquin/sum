package prcmd

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/douglasjarquin/sum/go/internal/proc"
)

func shortBounds(t *testing.T, bound, transient, rateLimit time.Duration) {
	t.Helper()
	prevBound, prevTransient, prevRate := GHBound, TransientDelay, RateLimitDelay
	GHBound, TransientDelay, RateLimitDelay = bound, transient, rateLimit
	t.Cleanup(func() { GHBound, TransientDelay, RateLimitDelay = prevBound, prevTransient, prevRate })
}

type countedGh struct {
	path  string
	count string
}

func countingGh(t *testing.T, firstBody, laterBody string) countedGh {
	t.Helper()
	count := filepath.Join(t.TempDir(), "count")
	body := fmt.Sprintf(`n=$(cat %q 2>/dev/null || echo 0); n=$((n+1)); echo $n > %q
if [ "$n" -eq 1 ]; then
%s
fi
%s`, count, count, firstBody, laterBody)
	return countedGh{path: fakeGh(t, body), count: count}
}

func (c countedGh) calls(t *testing.T) int {
	t.Helper()
	raw, err := os.ReadFile(c.count)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("count file %q: %v", raw, err)
	}
	return n
}

const (
	stderr502     = `echo 'HTTP 502: Bad Gateway (https://api.github.com/graphql)' >&2; exit 1`
	stderr503     = `echo 'HTTP 503: Service Unavailable (https://api.github.com/graphql)' >&2; exit 1`
	stderr429     = `echo 'HTTP 429: Too Many Requests (https://api.github.com/graphql)' >&2; exit 1`
	validPRJSON   = `printf '{"number":7,"state":"OPEN","headRefOid":"abc"}'`
	validRepoJSON = `printf '{"visibility":"PUBLIC"}'`
)

func TestTransientCategory(t *testing.T) {
	cases := map[string]string{
		"HTTP 502: Bad Gateway (https://api.github.com/graphql)":                                         "server-error",
		"HTTP 503: Service Unavailable (https://api.github.com/graphql)":                                 "server-error",
		"HTTP 500: Internal Server Error":                                                                "server-error",
		"HTTP 429: Too Many Requests (https://api.github.com/graphql)":                                   "rate-limit",
		"HTTP 403: You have exceeded a secondary rate limit. Please wait a few minutes":                  "rate-limit",
		"error connecting to api.github.com\ncheck your internet connection or https://githubstatus.com": "transport",
		`Post "https://127.0.0.1:1/api/graphql": dial tcp 127.0.0.1:1: connect: connection refused`:      "transport",
		"Get https://api.github.com: tls: handshake failure":                                             "transport",
		"x509: certificate signed by unknown authority":                                                  "transport",
		"A new release of gh is available: 2.0 -> 3.0\nHTTP 502: Bad Gateway":                            "server-error",
		"HTTP 401: Bad credentials (https://api.github.com/graphql)":                                     "",
		"HTTP 403: Resource not accessible by integration":                                               "",
		"HTTP 403: API rate limit exceeded for 1.2.3.4":                                                  "",
		"HTTP 404: Not Found (https://api.github.com/repos/o/r/pulls/7)":                                 "",
		"HTTP 422: Validation Failed":                                                                    "",
		"GraphQL: Could not resolve to a PullRequest with the number of 7.":                              "",
		"Post https://api.github.com/graphql: dial tcp: i/o timeout":                                     "",
		"":               "",
		"unexpected EOF": "",
	}
	for input, want := range cases {
		if got := transientCategory(input); got != want {
			t.Errorf("transientCategory(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSanitizedDetail(t *testing.T) {
	res := proc.Result{Stderr: "HTTP 502: Bad Gateway token ghp_abcdefghijklmnopqrstuvwxyz0123456789 Authorization: Bearer abc.def github_pat_11AAA_bbb"}
	got := sanitizedDetail(res)
	for _, leaked := range []string{"ghp_abcdefghijklmnopqrstuvwxyz0123456789", "Bearer abc.def", "github_pat_11AAA_bbb"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("detail leaked %q: %q", leaked, got)
		}
	}
	if !strings.HasPrefix(got, "HTTP 502: Bad Gateway") {
		t.Fatalf("detail = %q", got)
	}
	long := proc.Result{Stderr: strings.Repeat("x", 2000)}
	if n := len(sanitizedDetail(long)); n > 300 {
		t.Fatalf("detail length %d exceeds the bound", n)
	}
}

func TestViewPRRetriesTransientFailureOnce(t *testing.T) {
	shortBounds(t, 5*time.Second, 10*time.Millisecond, 20*time.Millisecond)
	gh := countingGh(t, stderr502, validPRJSON)
	data, err := viewPR(gh.path, t.TempDir(), "owner/repo", 7)
	if err != nil {
		t.Fatalf("second attempt should have succeeded: %v", err)
	}
	if data["number"] != float64(7) {
		t.Fatalf("data=%v", data)
	}
	if calls := gh.calls(t); calls != 2 {
		t.Fatalf("gh was invoked %d times, want 2", calls)
	}
}

func TestViewPRPersistentTransientFailureIsUncertainAfterTwoAttempts(t *testing.T) {
	shortBounds(t, 5*time.Second, 10*time.Millisecond, 20*time.Millisecond)
	gh := countingGh(t, stderr503, stderr503)
	data, err := viewPR(gh.path, t.TempDir(), "", 7)
	if err == nil || data != nil {
		t.Fatalf("data=%v err=%v, want an uncertain observation", data, err)
	}
	want := "PR observation for #7 is uncertain after 2 attempts (server-error): HTTP 503: Service Unavailable (https://api.github.com/graphql)"
	if err.Error() != want {
		t.Fatalf("err=%q\nwant %q", err, want)
	}
	if calls := gh.calls(t); calls != 2 {
		t.Fatalf("gh was invoked %d times, want 2", calls)
	}
}

func TestViewPRNeverRetriesNonTransientFailures(t *testing.T) {
	shortBounds(t, 5*time.Second, 10*time.Millisecond, 20*time.Millisecond)
	cases := map[string]string{
		"auth":          `echo 'HTTP 401: Bad credentials (https://api.github.com/graphql)' >&2; exit 1`,
		"forbidden":     `echo 'HTTP 403: Resource not accessible by integration' >&2; exit 1`,
		"not found":     `echo 'HTTP 404: Not Found (https://api.github.com/repos/o/r/pulls/7)' >&2; exit 1`,
		"graphql":       `echo 'GraphQL: Could not resolve to a PullRequest' >&2; exit 1`,
		"malformed":     `printf '{"number":7,'`,
		"oversized":     `printf '{"url":"'; head -c 9437184 /dev/zero | tr '\0' 'a'; printf '"}'`,
		"empty failure": `exit 1`,
	}
	for name, first := range cases {
		t.Run(name, func(t *testing.T) {
			gh := countingGh(t, first, validPRJSON)
			data, err := viewPR(gh.path, t.TempDir(), "", 7)
			if err == nil || data != nil {
				t.Fatalf("data=%v err=%v, want a failure", data, err)
			}
			if strings.Contains(err.Error(), "attempts") {
				t.Fatalf("err=%q names a retry", err)
			}
			if calls := gh.calls(t); calls != 1 {
				t.Fatalf("gh was invoked %d times, want 1", calls)
			}
		})
	}
}

func TestViewPRTimeoutIsNotRetriedAndKeepsOneBound(t *testing.T) {
	shortBounds(t, time.Second, 10*time.Millisecond, 20*time.Millisecond)
	gh := countingGh(t, `sleep 5`, validPRJSON)
	start := time.Now()
	data, err := viewPR(gh.path, t.TempDir(), "", 7)
	elapsed := time.Since(start)
	if err == nil || data != nil || !errors.Is(err, proc.ErrUncertain) {
		t.Fatalf("data=%v err=%v, want the runner's uncertain timeout", data, err)
	}
	if !strings.Contains(err.Error(), "gh: timed out after 1s; its effect is unknown") {
		t.Fatalf("err=%q", err)
	}
	if calls := gh.calls(t); calls != 1 {
		t.Fatalf("gh was invoked %d times, want 1", calls)
	}
	if elapsed > 4*time.Second {
		t.Fatalf("elapsed %s exceeds one bound", elapsed)
	}
}

func TestViewPRSlowFirstAttemptLeavesNoBudgetForASecond(t *testing.T) {
	shortBounds(t, time.Second, 150*time.Millisecond, 20*time.Millisecond)
	gh := countingGh(t, `sleep 0.8; `+stderr502, validPRJSON)
	start := time.Now()
	data, err := viewPR(gh.path, t.TempDir(), "", 7)
	elapsed := time.Since(start)
	if err == nil || data != nil {
		t.Fatalf("data=%v err=%v, want a deferred failure", data, err)
	}
	if !strings.Contains(err.Error(), "after 1 attempt (server-error, retry deferred for budget)") {
		t.Fatalf("err=%q", err)
	}
	if calls := gh.calls(t); calls != 1 {
		t.Fatalf("gh was invoked %d times, want 1", calls)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("elapsed %s exceeds one bound", elapsed)
	}
}

func TestViewPRRateLimitDefersWhenBudgetIsShort(t *testing.T) {
	shortBounds(t, 500*time.Millisecond, 10*time.Millisecond, 5*time.Second)
	gh := countingGh(t, stderr429, validPRJSON)
	data, err := viewPR(gh.path, t.TempDir(), "", 7)
	if err == nil || data != nil {
		t.Fatalf("data=%v err=%v, want a deferred failure", data, err)
	}
	want := "PR observation for #7 is uncertain after 1 attempt (rate-limit, retry deferred for budget): HTTP 429: Too Many Requests (https://api.github.com/graphql)"
	if err.Error() != want {
		t.Fatalf("err=%q\nwant %q", err, want)
	}
	if calls := gh.calls(t); calls != 1 {
		t.Fatalf("gh was invoked %d times, want 1", calls)
	}
}

func TestViewPRRateLimitRetriesWhenBudgetAllows(t *testing.T) {
	shortBounds(t, 5*time.Second, 10*time.Millisecond, 20*time.Millisecond)
	gh := countingGh(t, stderr429, validPRJSON)
	if _, err := viewPR(gh.path, t.TempDir(), "", 7); err != nil {
		t.Fatalf("second attempt should have succeeded: %v", err)
	}
	if calls := gh.calls(t); calls != 2 {
		t.Fatalf("gh was invoked %d times, want 2", calls)
	}
}

func TestViewPRGenericForbiddenIsNotARateLimit(t *testing.T) {
	shortBounds(t, 5*time.Second, 10*time.Millisecond, 20*time.Millisecond)
	gh := countingGh(t, `echo 'HTTP 403: Resource not accessible by integration' >&2; exit 1`, validPRJSON)
	_, err := viewPR(gh.path, t.TempDir(), "", 7)
	if err == nil || err.Error() != "PR observation for #7 is uncertain: HTTP 403: Resource not accessible by integration" {
		t.Fatalf("err=%v", err)
	}
	if calls := gh.calls(t); calls != 1 {
		t.Fatalf("gh was invoked %d times, want 1", calls)
	}
}

func TestObservedVisibilityRetriesTransientFailureOnce(t *testing.T) {
	shortBounds(t, 5*time.Second, 10*time.Millisecond, 20*time.Millisecond)
	gh := countingGh(t, stderr502, validRepoJSON)
	got, err := observedVisibility(gh.path, t.TempDir(), "owner/repo")
	if err != nil || got != "public" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if calls := gh.calls(t); calls != 2 {
		t.Fatalf("gh was invoked %d times, want 2", calls)
	}
	failing := countingGh(t, stderr503, stderr503)
	_, err = observedVisibility(failing.path, t.TempDir(), "owner/repo")
	if err == nil || !strings.Contains(err.Error(), "could not report the visibility of owner/repo after 2 attempts (server-error)") {
		t.Fatalf("err=%v", err)
	}
}

func TestProcessRunnerNeverRetries(t *testing.T) {
	gh := countingGh(t, stderr502, validPRJSON)
	res, err := proc.RunContext(context.Background(), proc.Cmd{Argv: []string{gh.path, "pr", "view"}, Timeout: 5 * time.Second})
	if err != nil || res.Code != 1 {
		t.Fatalf("res=%+v err=%v, want the single failed exit", res, err)
	}
	if calls := gh.calls(t); calls != 1 {
		t.Fatalf("runner invoked the helper %d times, want 1", calls)
	}
}

func TestRetryBoundaryIsExactlyTheTwoReadOnlyObservations(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	callers := map[string]bool{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "readOnlyGH" {
						callers[fn.Name.Name] = true
					}
					return true
				})
			}
		}
	}
	var got []string
	for name := range callers {
		got = append(got, name)
	}
	sort.Strings(got)
	want := []string{"observedVisibility", "viewPR"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("readOnlyGH callers = %v, want %v", got, want)
	}
}
