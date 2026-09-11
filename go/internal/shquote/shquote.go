package shquote

import (
	"regexp"
	"strings"
)

var unsafe = regexp.MustCompile(`[^\w@%+=:,./-]`)

func Quote(s string) string {
	if s == "" {
		return "''"
	}
	if !unsafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func Join(parts []string) string {
	quoted := make([]string, len(parts))
	for i, p := range parts {
		quoted[i] = Quote(p)
	}
	return strings.Join(quoted, " ")
}

func CommandFor(sumctlPath, home string, args ...string) string {
	return Join(append([]string{sumctlPath, "--home", home}, args...))
}
