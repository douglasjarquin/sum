package redact

import "regexp"

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{16,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)\b(?:api[_-]?key|access[_-]?token|secret|password|passwd)\s*[=:]\s*['"]?[^\s'"]{8,}`),
}

func Redact(text string) (string, int) {
	count := 0
	for _, pattern := range secretPatterns {
		matches := pattern.FindAllString(text, -1)
		if len(matches) == 0 {
			continue
		}
		count += len(matches)
		text = pattern.ReplaceAllString(text, "[redacted]")
	}
	return text, count
}
