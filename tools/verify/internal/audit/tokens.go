package audit

import (
	"regexp"
	"strings"

	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
)

var (
	code        = regexp.MustCompile("`([^`\\n]+)`")
	taskRef     = regexp.MustCompile(`mise run ([A-Za-z0-9_:.-]+)`)
	envRelative = regexp.MustCompile(`^[A-Z][A-Z0-9_]+/`)

	notPathPrefixes = []string{"-", "<", "$", "/", "http://", "https://", "mise ", "python", "npm", "node", "git", "curl"}
	pathSuffixes    = []string{".py", ".md", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".toml", ".json", ".sh", ".yaml", ".yml", ".html", ".go", ".rs", ".rb", ".txt", ".css"}
	testExtensions  = []string{"py", "js", "mjs", "ts", "tsx", "go", "rs", "rb", "sh"}
)

// pathTokens returns the backticked tokens that look like repository paths. The first word of a command is taken,
// so `hello.py NAME` checks `hello.py`.
func pathTokens(text string) []string {
	var tokens []string
	for _, match := range code.FindAllStringSubmatch(text, -1) {
		words := pycompat.Fields(match[1])
		if len(words) == 0 {
			continue
		}
		word := words[0]
		if hasAnyPrefix(word, notPathPrefixes) || strings.ContainsAny(word, "*<>{}$|;&") {
			continue // Flags, placeholders, URLs, commands, and absolute or route paths (`/health`) are not repository paths.
		}
		if envRelative.MatchString(word) {
			continue // `ITEMS_DATA_DIR/items.json`: an environment-variable-relative path, not a repository path.
		}
		if strings.Contains(word, "/") || hasAnySuffix(word, pathSuffixes) {
			tokens = append(tokens, strings.TrimRight(word, ":,"))
		}
	}
	return tokens
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func hasAnySuffix(s string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}

// taskRefs returns the NAME of every `mise run NAME` that starts at a word boundary. RE2's `\b` knows only ASCII
// words, so the boundary is checked here; a candidate glued to a preceding word is skipped and the search resumes
// just past its first character, where a backtracking search would look next.
func taskRefs(text string) []string {
	var names []string
	for at := 0; at < len(text); {
		match := taskRef.FindStringSubmatchIndex(text[at:])
		if match == nil {
			break
		}
		if pycompat.WordBefore(text, at+match[0]) {
			at += match[0] + 1
			continue
		}
		names = append(names, text[at+match[2]:at+match[3]])
		at += match[1]
	}
	return names
}

// coveragePaths returns the bare test-file paths a Driver cell names, as the pattern
//
//	(?<![\w/])((?:[\w.-]+/)*[\w.-]+\.(?:py|js|mjs|ts|tsx|go|rs|rb|sh))\b
//
// finds them. RE2 has no lookbehind and prefers a different match when the first candidate fails the trailing
// boundary, so the pattern is matched by hand in the order a backtracking engine tries it.
func coveragePaths(text string) []string {
	chars := []rune(text)
	var paths []string
	for start := 0; start < len(chars); {
		end := -1
		if start == 0 || !(pycompat.IsWord(chars[start-1]) || chars[start-1] == '/') {
			end = coveragePathEnd(chars, start)
		}
		if end < 0 {
			start++
			continue
		}
		paths = append(paths, string(chars[start:end]))
		start = end
	}
	return paths
}

// coveragePathEnd returns where the coverage path starting at start ends, or -1 when none starts there.
func coveragePathEnd(chars []rune, start int) int {
	// The text from start is split into the `/`-separated runs of path characters the pattern can consume.
	var segments [][2]int
	for at := start; ; {
		end := at
		for end < len(chars) && isPathChar(chars[end]) {
			end++
		}
		if end == at {
			break
		}
		segments = append(segments, [2]int{at, end})
		if end == len(chars) || chars[end] != '/' {
			break
		}
		at = end + 1
	}
	// The directory group is greedy, so the last run is tried as the file name first, then each earlier one.
	// The name is greedy too: within a run, the latest `.ext` that ends at a word boundary wins.
	for i := len(segments) - 1; i >= 0; i-- {
		for dot := segments[i][1] - 1; dot > segments[i][0]; dot-- {
			if chars[dot] != '.' {
				continue
			}
			for _, extension := range testExtensions {
				end := dot + 1 + len(extension)
				if end <= len(chars) && string(chars[dot+1:end]) == extension && (end == len(chars) || !pycompat.IsWord(chars[end])) {
					return end
				}
			}
		}
	}
	return -1
}

func isPathChar(r rune) bool {
	return pycompat.IsWord(r) || r == '.' || r == '-'
}
