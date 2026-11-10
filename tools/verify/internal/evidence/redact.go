package evidence

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
)

const textLimit = 20000

var redactDefault = []string{
	`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`,
	`(?i)\b((?:api[_-]?key|token|secret|password|passwd|authorization)\b["']?\s*[:=]\s*["']?)[^\s"',;]+`,
	`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`,
	`\b(?:gh[pousr]|sk|xox[abp])[_-][A-Za-z0-9_-]{10,}\b`,
}

// Redactor replaces secret-shaped text with [REDACTED], keeping a pattern's first group (the label before the secret) when it has one.
type Redactor struct {
	patterns []*regexp.Regexp
	spans    map[string]bool // Distinct redacted values; the same secret in a transcript and in the manifest counts once.
}

// NewRedactor compiles the built-in patterns followed by the extra ones.
func NewRedactor(extra []string) (*Redactor, error) {
	r := &Redactor{spans: map[string]bool{}}
	for _, source := range append(append([]string{}, redactDefault...), extra...) {
		pattern, err := regexp.Compile(source)
		if err != nil {
			return nil, fmt.Errorf("--redact %q is not a usable pattern: %w", source, err)
		}
		r.patterns = append(r.patterns, pattern)
	}
	return r, nil
}

// Count is how many distinct values have been redacted so far.
func (r *Redactor) Count() int {
	return len(r.spans)
}

func (r *Redactor) Redact(text string) string {
	for _, pattern := range r.patterns {
		var out strings.Builder
		last := 0
		for _, m := range pattern.FindAllStringSubmatchIndex(text, -1) {
			r.spans[text[m[0]:m[1]]] = true
			out.WriteString(text[last:m[0]])
			if len(m) > 2 && m[2] >= 0 {
				out.WriteString(text[m[2]:m[3]])
			}
			out.WriteString("[REDACTED]")
			last = m[1]
		}
		if last > 0 {
			out.WriteString(text[last:])
			text = out.String()
		}
	}
	return text
}

func clip(text string) string {
	return pycompat.ClipNoting(text, textLimit)
}
