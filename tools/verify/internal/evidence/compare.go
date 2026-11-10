package evidence

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/douglasjarquin/sum/tools/verify/internal/evidence/args"
	"github.com/douglasjarquin/sum/tools/verify/internal/evidence/value"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
)

var compareSpec = args.Spec{Options: []args.Option{
	{Name: "--scenario", Values: 1, Required: true},
	{Name: "--run", Values: 1, Required: true},
	{Name: "--base", Values: 1},
	{Name: "--candidate", Values: 1},
	{Name: "--json"},
}}

// Settings both captures must share unless a difference was declared intentional.
var environmentCompared = []string{"recipe", "kind", "viewport", "theme", "locale", "timezone"}

// Media fields a comparison carries over from each capture.
var comparedMedia = []string{"file", "type", "sha256", "bytes", "width", "height", "frames", "duration_ms", "role", "derived"}

// captured is one role's capture as the comparison reads it: its directory name under the scenario and its record.
type captured struct {
	dir    string
	record *value.Object
}

func readRecord(path string) (*value.Object, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parsed, err := value.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	record := value.Obj(parsed)
	if record == nil {
		return nil, fmt.Errorf("%s: not a JSON object", path)
	}
	return record, nil
}

// loadCaptures groups a scenario directory's captures by role, in directory-name order.
func loadCaptures(directory string) (map[string][]captured, error) {
	found := map[string][]captured{}
	entries, _ := os.ReadDir(directory)
	for _, entry := range entries {
		manifest := filepath.Join(directory, entry.Name(), "capture.json")
		if !isFile(manifest) {
			continue
		}
		record, err := readRecord(manifest)
		if err != nil {
			return nil, err
		}
		role := value.Str(record.Get("role"))
		found[role] = append(found[role], captured{entry.Name(), record})
	}
	return found, nil
}

// verifyHashes names every recorded file that is missing or no longer has the content hash its capture recorded.
func verifyHashes(directory string, record *value.Object) []string {
	var mismatched []string
	hashes := value.Obj(record.Get("content_hashes"))
	for _, name := range hashes.Keys() {
		path := filepath.Join(directory, name)
		if !isFile(path) {
			path = filepath.Join(directory, "frames", name)
		}
		data, err := os.ReadFile(path)
		if err != nil || tool.SHA256(data) != value.Str(hashes.Get(name)) {
			mismatched = append(mismatched, name)
		}
	}
	return mismatched
}

func cmdCompare(argv []string) (int, error) {
	opts, err := args.Parse(compareSpec, argv)
	if err != nil {
		return 0, err
	}
	root, err := ProjectRoot()
	if err != nil {
		return 0, err
	}
	rootDir, _ := EvidenceRoot(root)
	run, scenario, base, candidate := opts.String("--run"), opts.String("--scenario"), opts.String("--base"), opts.String("--candidate")
	directory := scenarioDir(rootDir, run, scenario)
	if !isDir(directory) {
		return 0, usage(fmt.Sprintf("no captures for scenario %s in run %s under %s", scenario, run, rootDir))
	}
	captures, err := loadCaptures(directory)
	if err != nil {
		return 0, err
	}
	comparison := value.NewObject("schema", schema, "run", run, "scenario", scenario, "compared_at", tool.UTCNow(), "base", value.NewObject("sha", orNull(base)),
		"candidate", value.NewObject("sha", orNull(candidate)), "before", nil, "after", nil, "findings", []any{}, "verdict", nil, "label", nil, "visual_proof", nil, "derived", []any{})
	var findings []string
	entries := map[string]*captured{}
	for _, role := range Roles {
		found := captures[role]
		if len(found) > 1 {
			findings = append(findings, fmt.Sprintf("%d %s captures in this run; a comparison needs exactly one per role", len(found), role))
			continue
		}
		if len(found) == 0 {
			continue
		}
		entry := &found[0]
		record := entry.record
		if mismatched := verifyHashes(filepath.Join(directory, entry.dir), record); mismatched != nil {
			findings = append(findings, fmt.Sprintf("%s: content hash mismatch for %s; the originals were altered after capture", role, strings.Join(mismatched, ", ")))
		}
		entries[role] = entry
		media := []any{}
		for _, m := range value.List(record.Get("media")) {
			projected := value.NewObject()
			for _, key := range comparedMedia {
				projected.Set(key, value.Obj(m).Get(key))
			}
			media = append(media, projected)
		}
		comparison.Set(role, value.NewObject("dir", entry.dir, "outcome", record.Get("outcome"), "sha", value.Obj(record.Get("checkout")).Get("sha"), "recipe", record.Get("recipe"),
			"kind", record.Get("kind"), "assertions", listOrEmpty(record.Get("assertions")), "limitations", listOrEmpty(record.Get("limitations")),
			"blocked_reason", record.Get("blocked_reason"), "media", media, "started_at", record.Get("started_at"), "ended_at", record.Get("ended_at")))
	}
	before, after := entries["before"], entries["after"]
	if after == nil {
		findings = append(findings, "no after capture; nothing to compare")
	}
	var kind any
	if after != nil {
		kind = after.record.Get("kind")
	} else if before != nil {
		kind = before.record.Get("kind")
	}
	comparison.Set("kind", kind)
	outcome := func(role string) string {
		if entries[role] == nil {
			return "missing"
		}
		return value.Str(entries[role].record.Get("outcome"))
	}
	// Build identity: the candidate capture must come from the declared candidate SHA, the before from the base.
	for _, role := range Roles {
		expected, declared := base, "base"
		if role == "after" {
			expected, declared = candidate, "candidate"
		}
		if entries[role] == nil || expected == "" || outcome(role) == "unavailable" {
			continue
		}
		sha := value.Str(value.Obj(entries[role].record.Get("checkout")).Get("sha"))
		if sha == "" {
			findings = append(findings, fmt.Sprintf("%s: no --checkout was recorded, so the build cannot be tied to %s", role, head(expected, 12)))
		} else if !strings.HasPrefix(sha, expected) {
			findings = append(findings, fmt.Sprintf("%s: captured from %s, not the declared %s %s (stale or wrong build)", role, head(sha, 12), declared, head(expected, 12)))
		}
	}
	// Environment consistency, unless the difference is declared intentional in either capture.
	if before != nil && after != nil && outcome("before") != "unavailable" {
		intentional := value.NewObject()
		for _, entry := range []*captured{before, after} {
			declared := value.Obj(entry.record.Get("intentional_differences"))
			for _, key := range declared.Keys() {
				intentional.Set(key, declared.Get(key))
			}
		}
		setting := func(entry *captured, key string) any {
			if key == "recipe" || key == "kind" {
				return entry.record.Get(key)
			}
			return value.Obj(entry.record.Get("environment")).Get(key)
		}
		differs := func(key string) string {
			return fmt.Sprintf("%s differs: before %s, after %s", key, value.Repr(setting(before, key)), value.Repr(setting(after, key)))
		}
		for _, key := range environmentCompared {
			if !value.Equal(setting(before, key), setting(after, key)) && !intentional.Has(key) {
				findings = append(findings, differs(key)+fmt.Sprintf("; declare it with --intentional %s=... or recapture consistently", key))
			}
		}
		comparison.Set("intentional_differences", intentional)
		var warnings []string
		for _, key := range []string{"browser_version", "platform"} {
			if !value.Equal(setting(before, key), setting(after, key)) {
				warnings = append(warnings, differs(key))
			}
		}
		if warnings != nil {
			comparison.Set("warnings", value.Strings(warnings))
		}
	}
	comparison.Set("findings", value.Strings(findings))
	visual := false
	for _, entry := range []*captured{before, after} {
		if entry == nil {
			continue
		}
		for _, m := range value.List(entry.record.Get("media")) {
			visual = visual || value.Obj(m).Get("role") == "screenshot"
		}
	}
	switch {
	case visual:
		comparison.Set("visual_proof", "captured")
	case kind == "nonvisual":
		comparison.Set("visual_proof", "not-applicable")
	default:
		comparison.Set("visual_proof", "not-captured")
	}
	comparison.Set("outcomes", value.NewObject("before", outcome("before"), "after", outcome("after")))
	var blockedRoles, blockedReasons []string
	for _, role := range Roles {
		if outcome(role) == "blocked" {
			blockedRoles = append(blockedRoles, role)
			blockedReasons = append(blockedReasons, value.Str(entries[role].record.Get("blocked_reason")))
		}
	}
	var verdict, label string
	switch {
	case findings != nil:
		verdict, label = "mismatch", "the captures cannot be compared as taken: "+strings.Join(findings, "; ")
	case blockedRoles != nil:
		verdict, label = "capture-failed", strings.Join(blockedRoles, ", ")+" capture blocked: "+strings.Join(blockedReasons, "; ")
	case outcome("after") != "pass":
		verdict, label = "after-fails", "the candidate does not pass the scenario's assertions"
	case outcome("before") == "missing" || outcome("before") == "unavailable":
		reason := "no before capture was recorded"
		if before != nil {
			reason = value.Str(before.record.Get("reason"))
		}
		verdict, label = "after-only", "before-unavailable: "+reason+"; this shows the candidate only, not a red/green pair"
	case outcome("before") == "fail":
		verdict, label = "red-green", "the base fails the user path and the candidate passes it"
		if kind == "nonvisual" {
			label += " (transcripts; visual proof not applicable)"
		}
	case kind == "feature" || kind == "visual":
		verdict, label = "before-after", fmt.Sprintf("both states captured (before %s, after pass); for a new feature the before state shows the absence, not a defect", outcome("before"))
	default:
		verdict, label = "before-also-passes", "the base already passes the same path; this is not evidence that a defect was fixed"
	}
	comparison.Set("verdict", verdict)
	comparison.Set("label", label)
	proves := verdict == "red-green" || verdict == "before-after" || (verdict == "after-only" && (kind == "feature" || kind == "visual"))
	comparison.Set("proves_claim", proves)
	page := []byte(sideBySideHTML(comparison, before, after))
	if err := os.WriteFile(filepath.Join(directory, "comparison.html"), page, 0o644); err != nil {
		return 0, err
	}
	comparison.Set("derived", []any{value.NewObject("file", "comparison.html", "type", "text/html",
		"transformation", "labelled two-up page referencing the original files; CSS scaling only", "sha256", tool.SHA256(page))})
	manifest := filepath.Join(directory, "comparison.json")
	if err := writeRecord(manifest, comparison); err != nil {
		return 0, err
	}
	if opts.Bool("--json") {
		if err := tool.WriteJSON(os.Stdout, comparison); err != nil {
			return 0, err
		}
	} else {
		fmt.Printf("comparison: %s verdict=%s visual_proof=%s\n%s\n", manifest, verdict, value.Str(comparison.Get("visual_proof")), label)
	}
	switch {
	case proves:
		return 0, nil
	case slices.Contains([]string{"mismatch", "capture-failed"}, verdict):
		return 2, nil
	}
	return 1, nil
}

func listOrEmpty(v any) any {
	if v == nil {
		return []any{}
	}
	return v
}

func htmlEscape(v any) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(value.Text(v))
}

// sideBySideHTML follows the labelled two-up convention of vercel-labs/before-and-after (see references/ATTRIBUTION.md): originals are
// referenced, never rewritten; only CSS scaling for display is applied.
func sideBySideHTML(comparison *value.Object, before, after *captured) string {
	panel := func(label string, entry *captured) string {
		if entry == nil {
			return "<section><h2>" + label + "</h2><p class='missing'>not available</p></section>"
		}
		record := entry.record
		first := func(roles ...string) *value.Object {
			for _, m := range value.List(record.Get("media")) {
				if slices.Contains(roles, value.Str(value.Obj(m).Get("role"))) {
					return value.Obj(m)
				}
			}
			return nil
		}
		firstSet := func(values ...any) any {
			for _, v := range values {
				if value.Truthy(v) {
					return v
				}
			}
			return ""
		}
		var parts strings.Builder
		fmt.Fprintf(&parts, "<h2>%s <small>%s</small></h2>", label, htmlEscape(record.Get("outcome")))
		fmt.Fprintf(&parts, "<p>sha <code>%s</code></p>", htmlEscape(value.Obj(record.Get("checkout")).Get("sha")))
		if shot := first("screenshot"); shot != nil {
			file, width, height := value.Text(shot.Get("file")), value.Text(shot.Get("width")), value.Text(shot.Get("height"))
			fmt.Fprintf(&parts, "<img src='%s/%s' alt='%s screenshot %sx%s' width='%s' height='%s'>", entry.dir, file, label, width, height, width, height)
		}
		if clip := first("screencast-converted", "screencast"); clip != nil {
			fmt.Fprintf(&parts, "<video controls muted src='%s/%s'></video><p><small>%s</small></p>", entry.dir, value.Text(clip.Get("file")),
				htmlEscape(firstSet(clip.Get("encoding"), clip.Get("tool"))))
		}
		if first("transcript", "response") != nil {
			observations := value.Obj(record.Get("observations"))
			fmt.Fprintf(&parts, "<pre>%s</pre>", htmlEscape(firstSet(observations.Get("stdout"), observations.Get("body"))))
		}
		if record.Get("outcome") == "unavailable" {
			fmt.Fprintf(&parts, "<p class='missing'>%s</p>", htmlEscape(record.Get("reason")))
		}
		for _, a := range value.List(record.Get("assertions")) {
			mark := "✗"
			if value.Truthy(value.Obj(a).Get("met")) {
				mark = "✓"
			}
			fmt.Fprintf(&parts, "<p>%s %s</p>", mark, htmlEscape(value.Obj(a).Get("expectation")))
		}
		return "<section>" + parts.String() + "</section>"
	}
	return "<!doctype html><meta charset='utf-8'><title>" + htmlEscape(comparison.Get("scenario")) + " before/after</title>" +
		"<style>body{font-family:system-ui;margin:1rem}main{display:grid;grid-template-columns:1fr 1fr;gap:1rem}img,video{max-width:100%;height:auto;border:1px solid #888}" +
		"pre{white-space:pre-wrap;background:#f4f4f4;padding:.5rem}.missing{color:#a00}</style>" +
		"<h1>" + htmlEscape(comparison.Get("scenario")) + ": " + htmlEscape(comparison.Get("verdict")) + "</h1>" +
		"<p>" + htmlEscape(comparison.Get("label")) + ". Derived view: originals are referenced unchanged and scaled by CSS for display only.</p>" +
		"<main>" + panel("Before", before) + panel("After", after) + "</main>"
}
