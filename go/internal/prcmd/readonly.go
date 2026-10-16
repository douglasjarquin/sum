package prcmd

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/proc"
)

var (
	TransientDelay = time.Second
	RateLimitDelay = 5 * time.Second
)

const detailBound = 300

type readOutcome struct {
	Result   proc.Result
	Attempts int
	Category string
	Deferred bool
}

func readOnlyGH(argv []string, dir string, bound time.Duration) (readOutcome, error) {
	deadline := time.Now().Add(bound)
	res, err := proc.RunContext(context.Background(), proc.Cmd{Argv: argv, Dir: dir, Timeout: bound})
	out := readOutcome{Result: res, Attempts: 1}
	if err != nil || res.Code == 0 {
		return out, err
	}
	out.Category = transientCategory(res.Stderr)
	if out.Category == "" {
		return out, nil
	}
	delay := TransientDelay
	if out.Category == "rate-limit" {
		delay = RateLimitDelay
	}
	if time.Until(deadline) <= delay+TransientDelay {
		out.Deferred = true
		return out, nil
	}
	time.Sleep(delay)
	res, err = proc.RunContext(context.Background(), proc.Cmd{Argv: argv, Dir: dir, Timeout: time.Until(deadline)})
	out.Result, out.Attempts = res, 2
	if err == nil && res.Code != 0 {
		if category := transientCategory(res.Stderr); category != "" {
			out.Category = category
		}
	}
	return out, err
}

func (o readOutcome) attemptNote() string {
	switch {
	case o.Deferred:
		return fmt.Sprintf(" after 1 attempt (%s, retry deferred for budget)", o.Category)
	case o.Attempts > 1:
		return fmt.Sprintf(" after %d attempts (%s)", o.Attempts, o.Category)
	}
	return ""
}

var httpStatusLine = regexp.MustCompile(`\bHTTP ([1-5][0-9]{2})\b`)

func transientCategory(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "secondary rate limit") {
			return "rate-limit"
		}
		if m := httpStatusLine.FindStringSubmatch(line); m != nil {
			switch {
			case m[1] == "429":
				return "rate-limit"
			case m[1][0] == '5':
				return "server-error"
			}
			continue
		}
		if strings.HasPrefix(line, "error connecting to ") ||
			strings.Contains(line, "connection refused") ||
			strings.Contains(line, "tls: ") ||
			strings.Contains(line, "x509: ") {
			return "transport"
		}
	}
	return ""
}

var secretShapes = regexp.MustCompile(`(?i)\b(?:gh[pousr]_[A-Za-z0-9]+|github_pat_[A-Za-z0-9_]+|bearer\s+\S+)`)

func sanitizedDetail(res proc.Result) string {
	detail := secretShapes.ReplaceAllString(res.Detail(), "[redacted]")
	if len(detail) > detailBound {
		detail = detail[:detailBound]
	}
	return detail
}
