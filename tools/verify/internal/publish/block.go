package publish

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/douglasjarquin/sum/tools/verify/internal/evidence/value"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
)

// The block markup (before/after tables, Preview labels, own-line videos, HTML video tables, and these markers) follows
// vercel-labs/before-and-after `skill/scripts/format.mjs` at 8306d34f459b6704e08e6adb5829fcddb0dc3557 (PolyForm Shield 1.0.0,
// copyright (c) 2026 James Clements); see references/ATTRIBUTION.md and references/LICENSE-before-and-after.txt in the evidence skill.
const (
	markerStart = "<!-- before-and-after:start -->"
	markerEnd   = "<!-- before-and-after:end -->"
)

var (
	mediaMark     = regexp.MustCompile(`<!-- sum-media: ([^>]*?) -->`)
	attachmentURL = regexp.MustCompile(`https://github\.com/user-attachments/assets/[A-Za-z0-9._-]+`)
	localRef      = regexp.MustCompile(`\]\(\./[^)\s]+\)`)
	backticks     = regexp.MustCompile("`+")
)

func trimRight(text string) string {
	return strings.TrimRightFunc(text, isSpace)
}

// normalize is the form two blocks are compared in: line endings, trailing spaces, and surrounding blank lines do not count.
func normalize(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = trimRight(line)
	}
	return strings.TrimFunc(strings.Join(lines, "\n"), isSpace)
}

func blockHash(text string) string {
	return tool.SHA256([]byte(normalize(text)))
}

func mediaRef(entry *value.Object, url, alt string) string {
	if url == "" {
		url = "./" + value.Str(entry.Get("publish_copy"))
	}
	return fmt.Sprintf("![%s](%s)", alt, url)
}

// textCopy reads a text publish copy back, refusing one that is no longer the content the plan approved.
func textCopy(plan, entry *value.Object) (string, error) {
	name := value.Str(entry.Get("publish_copy"))
	path, err := containedFile(value.Str(plan.Get("media_dir")), name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if tool.SHA256(data) != value.Str(entry.Get("sha256")) {
		return "", refused("publish copy " + name + " is altered; run plan again")
	}
	if !utf8.Valid(data) {
		return "", refused("publish copy " + name + " is not UTF-8 text")
	}
	return string(data), nil
}

// withoutText drops every fenced text section the block renders: a line of three or more backticks followed by "text", through the next
// line that is exactly the same run of backticks. What is left is the markup the publisher wrote, without captured text that could imitate it.
func withoutText(block string) string {
	var out strings.Builder
	lines := strings.SplitAfter(block, "\n")
	for i := 0; i < len(lines); i++ {
		fence, opens := strings.CutSuffix(lines[i], "text\n")
		if opens && len(fence) >= 3 && strings.Trim(fence, "`") == "" {
			closing := -1
			for j := i + 1; j < len(lines); j++ {
				if strings.TrimSuffix(lines[j], "\n") == fence {
					closing = j
					break
				}
			}
			if closing >= 0 {
				i = closing
				continue
			}
		}
		out.WriteString(lines[i])
	}
	return out.String()
}

func shortSHA(entry *value.Object) string {
	return value.Str(entry.Get("sha256"))[:12]
}

// renderBlock renders the marked block. urlFor gives a known attachment URL or "" (then a local publish-copy reference `gh --attach`
// rewrites). final renders video pairs as an HTML table once every URL is known; before that videos stay on their own lines so `gh` can
// upload them.
func renderBlock(plan *value.Object, urlFor func(sha string) string, final bool) (string, error) {
	candidate, base, run := value.Str(plan.Get("candidate")), value.Str(plan.Get("base")), value.Text(plan.Get("run"))
	shownBase := "unknown"
	if base != "" {
		shownBase = base
	}
	lines := []string{markerStart, fmt.Sprintf("<!-- sum-evidence: run=%s candidate=%s base=%s scenarios=%d -->", run, candidate, shownBase, len(value.List(plan.Get("publishable_scenarios")))),
		"### Before/after evidence", ""}
	who := "the task worker"
	if value.Truthy(plan.Get("captured_by")) {
		who = value.Text(plan.Get("captured_by"))
	}
	var cited []string
	for _, id := range value.List(plan.Get("verification_runs")) {
		cited = append(cited, "`"+value.Text(id)+"`")
	}
	runs := "none recorded"
	if cited != nil {
		runs = strings.Join(cited, ", ")
	}
	against := ""
	if base != "" {
		against = fmt.Sprintf(" against base `%s`", firstChars(base, 12))
	}
	lines = append(lines, fmt.Sprintf("Captured by %s from candidate `%s`%s; evidence run `%s`; verification run(s) %s. "+
		"Worker evidence is a claim about the candidate build; it is not independent verification.", who, firstChars(candidate, 12), against, run, runs), "")
	for _, r := range value.List(plan.Get("scenarios")) {
		row := value.Obj(r)
		if row.Get("publishable") != true {
			continue
		}
		label := "`" + value.Text(row.Get("id")) + "`"
		if value.Truthy(row.Get("feature")) {
			label += fmt.Sprintf(" (feature `%s`)", value.Text(row.Get("feature")))
		}
		lines = append(lines, fmt.Sprintf("#### %s: %s", label, value.Text(row.Get("verdict"))), "_"+value.Text(row.Get("label"))+"_", "")
		media := value.List(row.Get("media"))
		find := func(role, kind string) *value.Object {
			var found *value.Object
			for _, m := range media {
				if entry := value.Obj(m); entry.Get("role") == role && entry.Get("kind") == kind {
					found = entry
				}
			}
			return found
		}
		for _, kind := range []string{"image", "video"} {
			before, after := find("before", kind), find("after", kind)
			if after == nil {
				continue
			}
			afterSHA := value.Str(after.Get("sha256"))
			afterURL, beforeURL := urlFor(afterSHA), ""
			marks := "after=" + afterSHA
			if before != nil {
				beforeSHA := value.Str(before.Get("sha256"))
				beforeURL = urlFor(beforeSHA)
				marks = "before=" + beforeSHA + " " + marks
			}
			pair := "<!-- sum-media: " + marks + " -->"
			switch {
			case kind == "image" && before != nil:
				lines = append(lines, pair, "| Before | After |", "|:---:|:---:|", fmt.Sprintf("| %s | %s |",
					mediaRef(before, beforeURL, "before screenshot "+shortSHA(before)), mediaRef(after, afterURL, "after screenshot "+shortSHA(after))), "")
			case kind == "image":
				lines = append(lines, pair, "| Preview (after only) |", "|:---:|", fmt.Sprintf("| %s |", mediaRef(after, afterURL, "preview screenshot "+shortSHA(after))), "")
			case final && afterURL != "" && (before == nil || beforeURL != ""):
				lines = append(lines, pair, "<table>", "  <tr>")
				if before != nil {
					lines = append(lines, "    <th>Before (screencast)</th>", "    <th>After (screencast)</th>", "  </tr>", "  <tr>",
						fmt.Sprintf(`    <td><video src="%s" width="100%%" controls></video></td>`, beforeURL))
				} else {
					lines = append(lines, "    <th>Preview (screencast)</th>", "  </tr>", "  <tr>")
				}
				lines = append(lines, fmt.Sprintf(`    <td><video src="%s" width="100%%" controls></video></td>`, afterURL), "  </tr>", "</table>", "")
			case before != nil:
				lines = append(lines, "**Before (screencast)**", "<!-- sum-media: before="+value.Str(before.Get("sha256"))+" -->", mediaRef(before, beforeURL, "before screencast "+shortSHA(before)), "",
					"**After (screencast)**", "<!-- sum-media: after="+afterSHA+" -->", mediaRef(after, afterURL, "after screencast "+shortSHA(after)), "")
			default:
				lines = append(lines, "**Preview (after only) (screencast)**", "<!-- sum-media: after="+afterSHA+" -->", mediaRef(after, afterURL, "preview screencast "+shortSHA(after)), "")
			}
		}
		for _, m := range media {
			entry := value.Obj(m)
			if entry.Get("kind") != "text" {
				continue
			}
			text, err := textCopy(plan, entry)
			if err != nil {
				return "", err
			}
			longest := 0
			for _, run := range backticks.FindAllString(text, -1) {
				longest = max(longest, len(run))
			}
			fence := strings.Repeat("`", max(3, 1+longest))
			title := "Before"
			if entry.Get("role") == "after" {
				title = "After"
				if find("before", "text") == nil {
					title = "Preview (after only)"
				}
			}
			lines = append(lines, fmt.Sprintf("**%s (%s)**", title, value.Text(entry.Get("text_role"))),
				fmt.Sprintf("Capture SHA-256: `%s`; HTML comment openers are escaped for publication.", value.Text(entry.Get("sha256"))),
				fence+"text", strings.TrimSuffix(strings.ReplaceAll(text, "<!--", "&lt;!--"), "\n"), fence, "")
		}
		for _, note := range value.List(row.Get("unpublished")) {
			lines = append(lines, "_"+value.Text(note)+"_", "")
		}
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(append(lines, markerEnd), "\n") + "\n", nil
}

// span is where the marked block sits in a PR body, as byte offsets.
type span struct{ start, end int }

// findBlock locates exactly one marked block, or none. Two starts, a missing end, or an inverted pair is malformed and refused.
func findBlock(body string) (*span, error) {
	start, end := strings.Index(body, markerStart), strings.Index(body, markerEnd)
	if start == -1 && end == -1 {
		return nil, nil
	}
	if start == -1 || end == -1 || end < start {
		return nil, refused("the PR body has an incomplete before-and-after marker block; fix it by hand before publishing")
	}
	if strings.Contains(body[start+len(markerStart):], markerStart) || strings.Contains(body[end+len(markerEnd):], markerEnd) {
		return nil, refused("the PR body has more than one before-and-after marker block; publication owns exactly one")
	}
	return &span{start, end + len(markerEnd)}, nil
}

// splice replaces only the marked span (or appends); every other byte of the body is kept.
func splice(body string, at *span, block string) string {
	if at == nil {
		if prefix := trimRight(body); prefix != "" {
			return prefix + "\n\n" + block
		}
		return block
	}
	return body[:at.start] + strings.TrimRight(block, "\n") + body[at.end:]
}

// outside is the body with the marked span cut out.
func outside(body string, at *span) string {
	if at == nil {
		return body
	}
	return body[:at.start] + body[at.end:]
}

// harvestURLs reads the attachment URLs GitHub wrote for each `<!-- sum-media: role=sha ... -->` marker, from the lines that follow it in order.
func harvestURLs(block string) map[string]string {
	found := map[string]string{}
	lines := strings.Split(strings.ReplaceAll(block, "\r\n", "\n"), "\n")
	for index, line := range lines {
		match := mediaMark.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		var shas []string
		for _, pair := range strings.Fields(match[1]) {
			if _, sha, ok := strings.Cut(pair, "="); ok {
				shas = append(shas, sha)
			}
		}
		var urls []string
		for _, following := range lines[index+1:] {
			if mediaMark.MatchString(following) || strings.HasPrefix(following, "#### ") || following == markerEnd {
				break
			}
			urls = append(urls, attachmentURL.FindAllString(following, -1)...)
			if len(urls) >= len(shas) {
				break
			}
		}
		for i := 0; i < len(shas) && i < len(urls); i++ {
			if _, seen := found[shas[i]]; !seen {
				found[shas[i]] = urls[i]
			}
		}
	}
	return found
}
