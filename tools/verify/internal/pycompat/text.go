// Package pycompat reproduces the Python behaviors that the verification records and checks were defined by,
// so the same contract, feature map, or command line gives the same result it always did.
package pycompat

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SpaceClass is Python's `\s` for use inside a regular-expression bracket; RE2's own `\s` is ASCII only.
const SpaceClass = `\t-\r\x1c-\x20\x85\p{Z}`

// WordClass is Python's `\w` for use inside a regular-expression bracket; RE2's own `\w` is ASCII only.
const WordClass = `\p{L}\p{N}_`

// IsSpace is str.isspace for one character.
func IsSpace(r rune) bool {
	return (r >= '\t' && r <= '\r') || (r >= 0x1c && r <= 0x20) || r == 0x85 || unicode.Is(unicode.Z, r)
}

// IsWord reports whether Python's `\w` matches the character.
func IsWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// WordBefore reports whether the character ending at byte offset i is a word character.
func WordBefore(s string, i int) bool {
	r, size := utf8.DecodeLastRuneInString(s[:i])
	return size > 0 && IsWord(r)
}

// WordAfter reports whether the character starting at byte offset i is a word character.
func WordAfter(s string, i int) bool {
	r, size := utf8.DecodeRuneInString(s[i:])
	return size > 0 && IsWord(r)
}

// Strip is str.strip().
func Strip(s string) string {
	return strings.TrimFunc(s, IsSpace)
}

// Fields is str.split() with no separator.
func Fields(s string) []string {
	return strings.FieldsFunc(s, IsSpace)
}

// Lower is str.lower(). Only the dotted capital I lowers to two characters; Python's final-sigma rule is not reproduced
// because every caller compares the result with ASCII words.
func Lower(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "İ", "i̇"))
}

// Clip is s[:n]: the first n characters, not bytes.
func Clip(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}

// Tail is s[-n:]: the last n characters, not bytes.
func Tail(s string, n int) string {
	i := len(s)
	for ; n > 0 && i > 0; n-- {
		_, size := utf8.DecodeLastRuneInString(s[:i])
		i -= size
	}
	return s[i:]
}

// UniversalNewlines is what reading in text mode does: `\r\n` and a lone `\r` both become `\n`.
func UniversalNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// SplitLines is str.splitlines(), which breaks on more than `\n`.
func SplitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !isLineBreak(r) {
			i += size
			continue
		}
		lines = append(lines, s[start:i])
		i += size
		if r == '\r' && i < len(s) && s[i] == '\n' {
			i++
		}
		start = i
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func isLineBreak(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}

// Repr is repr() for the values a TOML table or a command line can hold; messages quote values with it.
func Repr(value any) string {
	switch v := value.(type) {
	case nil:
		return "None"
	case bool:
		if v {
			return "True"
		}
		return "False"
	case string:
		return reprString(v)
	case float64:
		text := strconv.FormatFloat(v, 'g', -1, 64)
		if !strings.ContainsAny(text, ".eIN") {
			text += ".0"
		}
		return text
	case []string:
		items := make([]string, len(v))
		for i, item := range v {
			items[i] = reprString(item)
		}
		return "[" + strings.Join(items, ", ") + "]"
	case []any:
		items := make([]string, len(v))
		for i, item := range v {
			items[i] = Repr(item)
		}
		return "[" + strings.Join(items, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		items := make([]string, len(keys))
		for i, key := range keys {
			items[i] = reprString(key) + ": " + Repr(v[key])
		}
		return "{" + strings.Join(items, ", ") + "}"
	}
	return fmt.Sprint(value)
}

func reprString(s string) string {
	quote := '\''
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		quote = '"'
	}
	var b strings.Builder
	b.WriteRune(quote)
	for _, r := range s {
		switch {
		case r == quote || r == '\\':
			b.WriteRune('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case unicode.IsPrint(r):
			b.WriteRune(r)
		case r <= 0xff:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r <= 0xffff:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	b.WriteRune(quote)
	return b.String()
}
