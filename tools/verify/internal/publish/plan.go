package publish

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/douglasjarquin/sum/tools/verify/internal/args"
	"github.com/douglasjarquin/sum/tools/verify/internal/evidence"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
	"github.com/douglasjarquin/sum/tools/verify/internal/value"
)

const (
	imageLimit = 10 * 1024 * 1024  // GitHub rejects larger images and GIFs.
	videoLimit = 100 * 1024 * 1024 // GitHub's video attachment ceiling.
)

var (
	imageTypes = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}
	videoTypes = map[string]string{"video/mp4": ".mp4", "video/quicktime": ".mov", "video/webm": ".webm"}

	fullSHA   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	ownerName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

var planSpec = args.Spec{Options: []args.Option{
	{Name: "--run", Values: 1, Required: true},
	{Name: "--scenario", Values: 1},
	{Name: "--repo", Values: 1, Required: true},
	{Name: "--pr", Values: 1, Int: true, Required: true},
	{Name: "--candidate", Values: 1, Required: true},
	{Name: "--base", Values: 1},
	{Name: "--verification-run", Values: 1},
	{Name: "--captured-by", Values: 1},
	{Name: "--evidence-root", Values: 1},
	{Name: "--publish-dir", Values: 1},
	{Name: "--redact", Values: 1},
	{Name: "--json"},
}}

func loadJSON(path, what string) (*value.Object, error) {
	unreadable := func(reason any) error {
		return refused(fmt.Sprintf("%s unreadable at %s: %v", what, path, reason))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, unreadable(err)
	}
	parsed, err := value.Decode(data)
	if err != nil {
		return nil, unreadable(err)
	}
	if value.Obj(parsed) == nil {
		return nil, unreadable("not a JSON object")
	}
	return value.Obj(parsed), nil
}

func writeJSON(path string, record any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := tool.EncodeJSON(record)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// sameSHA accepts a full SHA and an abbreviation of it, but nothing shorter than seven characters.
func sameSHA(a, b string) bool {
	if len(a) < 7 || len(b) < 7 {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

func isSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// containedFile is a path built only from single, non-dot components under the run directory, resolved without following any symlink out of it.
func containedFile(runDir string, parts ...string) (string, error) {
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, `/\`) || strings.ContainsFunc(part, isSpace) {
			return "", refused("manifest names an unsafe path component " + value.Repr(part))
		}
	}
	path := filepath.Join(append([]string{runDir}, parts...)...)
	real := pycompat.ResolveExistingPrefix(path)
	absolute, _ := filepath.Abs(path)
	if real != absolute || !strings.HasPrefix(real, runDir+string(filepath.Separator)) {
		return "", refused(path + " escapes the evidence run directory (symlink or relative component); nothing outside it is ever read")
	}
	if info, err := os.Stat(real); err != nil || !info.Mode().IsRegular() {
		return "", refused(path + " is not a regular file")
	}
	return real, nil
}

func publishName(scenario, role, sha, ext string) string {
	return fmt.Sprintf("%s--%s--%s%s", evidence.SafeName(scenario), role, sha[:12], ext)
}

// stageCopy writes an approved publish copy once, verifies it after the write, and never replaces it with different content.
func stageCopy(mediaDir, name string, data []byte) error {
	digest := tool.SHA256(data)
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		return err
	}
	copy := filepath.Join(mediaDir, name)
	if existing, err := os.ReadFile(copy); err == nil {
		if tool.SHA256(existing) != digest {
			return refused("publish copy " + name + " exists with different content; remove it deliberately")
		}
		return nil
	}
	tmp := copy + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if written, err := os.ReadFile(tmp); err != nil || tool.SHA256(written) != digest {
		_ = os.Remove(tmp)
		return refused("publish copy " + name + " did not verify after copying")
	}
	return os.Rename(tmp, copy)
}

// selected is one file of a comparison that can be published, and how: as an image, a video, or text.
type selected struct {
	kind     string
	manifest *value.Object
}

func hasOutcome(entry *value.Object) bool {
	outcome := entry.Get("outcome")
	return entry.Len() > 0 && outcome != nil && outcome != "unavailable" && outcome != "missing"
}

// selectMedia picks the renderable media and text of one role; AVI originals are named in findings, never uploaded.
func selectMedia(comparison *value.Object, role string, findings *[]string) []selected {
	entry := value.Obj(comparison.Get(role))
	if !hasOutcome(entry) {
		return nil
	}
	media := value.List(entry.Get("media"))
	first := func(as string, types map[string]string) *value.Object {
		for _, m := range media {
			item := value.Obj(m)
			if _, known := types[value.Str(item.Get("type"))]; item.Get("role") == as && (known || types == nil) {
				return item
			}
		}
		return nil
	}
	var chosen []selected
	if shot := first("screenshot", imageTypes); shot != nil {
		chosen = append(chosen, selected{"image", shot})
	}
	if converted := first("screencast-converted", videoTypes); converted != nil {
		chosen = append(chosen, selected{"video", converted})
	} else if original := first("screencast", nil); original != nil {
		container := "an AVI"
		if value.Truthy(original.Get("type")) {
			container = value.Text(original.Get("type"))
		}
		*findings = append(*findings, fmt.Sprintf("%s: screencast %s is %s; GitHub renders mp4, mov, and webm only. "+
			"Recapture with --convert (ffmpeg) to publish the video; the screenshot is published without it.", role, value.Text(original.Get("file")), container))
	}
	for _, m := range media {
		item := value.Obj(m)
		if as := item.Get("role"); (as == "transcript" || as == "response") && item.Get("type") == "text/plain" {
			chosen = append(chosen, selected{"text", item})
		}
	}
	return chosen
}

// textOrEmpty is the value as text, with null and other empty values as "".
func textOrEmpty(v any) string {
	if !value.Truthy(v) {
		return ""
	}
	return value.Text(v)
}

// scenarioPlanner validates the scenarios of one run against the destination being published to.
type scenarioPlanner struct {
	runDir, mediaDir string
	candidate, base  string
	redactor         *evidence.Redactor
}

type stagedCopy struct {
	name string
	data []byte
}

func (p *scenarioPlanner) plan(scenario string) (*value.Object, error) {
	safe := evidence.SafeName(scenario)
	comparison, err := loadJSON(filepath.Join(p.runDir, safe, "comparison.json"), "comparison manifest for "+scenario)
	if err != nil {
		return nil, err
	}
	row := value.NewObject("id", scenario, "feature", nil, "verdict", comparison.Get("verdict"), "label", comparison.Get("label"), "kind", comparison.Get("kind"),
		"proves_claim", comparison.Get("proves_claim"), "candidate", value.Obj(comparison.Get("candidate")).Get("sha"), "base", value.Obj(comparison.Get("base")).Get("sha"),
		"publishable", false, "findings", []any{}, "media", []any{}, "unpublished", []any{})
	var findings []string
	media := []any{}
	done := func() (*value.Object, error) {
		row.Set("findings", value.Strings(findings))
		row.Set("media", media)
		return row, nil
	}
	if !value.Equal(comparison.Get("scenario"), scenario) {
		findings = append(findings, fmt.Sprintf("manifest is for scenario %s, not %s", value.Repr(comparison.Get("scenario")), value.Repr(scenario)))
	}
	if !sameSHA(value.Str(row.Get("candidate")), p.candidate) {
		findings = append(findings, fmt.Sprintf("comparison candidate %s is not the candidate %s being published; evidence from another build is never attached", value.Repr(row.Get("candidate")), p.candidate[:12]))
	}
	if p.base != "" && value.Truthy(row.Get("base")) && !sameSHA(value.Str(row.Get("base")), p.base) {
		findings = append(findings, fmt.Sprintf("comparison base %s differs from --base %s", value.Repr(row.Get("base")), firstChars(p.base, 12)))
	}
	switch row.Get("verdict") {
	case "mismatch", "capture-failed", "after-fails", nil:
		label := "nothing"
		if value.Truthy(comparison.Get("label")) {
			label = value.Text(comparison.Get("label"))
		}
		findings = append(findings, fmt.Sprintf("verdict %s does not show a candidate passing; %s is not published as evidence", value.Repr(row.Get("verdict")), label))
	}
	outcomes := value.Obj(comparison.Get("outcomes"))
	texts := []string{scenario, textOrEmpty(comparison.Get("label"))}
	for _, finding := range value.List(comparison.Get("findings")) {
		texts = append(texts, value.Text(finding))
	}
	for _, role := range evidence.Roles {
		entry := value.Obj(comparison.Get(role))
		for _, limitation := range value.List(entry.Get("limitations")) {
			texts = append(texts, value.Text(limitation))
		}
		texts = append(texts, textOrEmpty(entry.Get("blocked_reason")))
	}
	mark := p.redactor.Count()
	for _, text := range texts {
		p.redactor.Redact(text)
	}
	if found := p.redactor.Count() - mark; found > 0 {
		findings = append(findings, fmt.Sprintf("%d secret-shaped span(s) in the manifest text that would reach the PR body; publish nothing from this scenario until the text is clean", found))
	}
	if findings != nil {
		return done()
	}
	var pending []stagedCopy
	for _, role := range evidence.Roles {
		entry := value.Obj(comparison.Get(role))
		if !hasOutcome(entry) {
			continue
		}
		dir := textOrEmpty(entry.Get("dir"))
		manifestPath, err := containedFile(p.runDir, safe, dir, "capture.json")
		var record *value.Object
		if err == nil {
			record, err = loadJSON(manifestPath, role+" capture manifest")
		}
		if err != nil {
			findings = append(findings, err.Error())
			continue
		}
		if !value.Truthy(row.Get("feature")) {
			row.Set("feature", record.Get("feature"))
		}
		redaction := value.Obj(record.Get("redaction")).Get("count")
		for _, item := range selectMedia(comparison, role, &findings) {
			if item.kind == "image" && value.Truthy(redaction) {
				findings = append(findings, fmt.Sprintf("%s: %s secret-shaped span(s) were redacted from this capture's text; a screenshot cannot be redacted, so it is treated as containing them and is not published", role, value.Text(redaction)))
				continue
			}
			entry, copy, err := p.validate(scenario, role, dir, item, record)
			if err != nil {
				findings = append(findings, err.Error())
				continue
			}
			pending = append(pending, copy)
			media = append(media, entry)
		}
	}
	row.Set("outcomes", value.NewObject("before", outcomes.Get("before"), "after", outcomes.Get("after")))
	publishable := false
	for _, m := range media {
		publishable = publishable || value.Obj(m).Get("role") == "after"
	}
	publishable = publishable && findings == nil
	if publishable {
		for _, copy := range pending {
			if err := stageCopy(p.mediaDir, copy.name, copy.data); err != nil {
				if _, ok := err.(refused); !ok {
					return nil, err
				}
				publishable = false
				findings = append(findings, err.Error())
				break
			}
		}
	}
	row.Set("publishable", publishable)
	if !publishable && findings == nil {
		findings = append(findings, "no publishable after evidence (screenshot, converted screencast, or transcript) in this comparison")
	}
	return done()
}

// validate re-reads one selected file and proves it is the file both manifests describe before it becomes a plan entry and a publish copy.
func (p *scenarioPlanner) validate(scenario, role, dir string, item selected, record *value.Object) (*value.Object, stagedCopy, error) {
	manifest := item.manifest
	file := textOrEmpty(manifest.Get("file"))
	real, err := containedFile(p.runDir, evidence.SafeName(scenario), dir, file)
	if err != nil {
		return nil, stagedCopy{}, err
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return nil, stagedCopy{}, err
	}
	digest := tool.SHA256(data)
	declared, recorded := manifest.Get("sha256"), value.Obj(record.Get("content_hashes")).Get(file)
	if declared != digest || recorded != digest {
		return nil, stagedCopy{}, refused(fmt.Sprintf("%s: %s hash %s does not match the comparison (%s) and capture (%s) manifests; the original was altered",
			role, file, digest[:12], firstChars(value.Text(declared), 12), firstChars(value.Text(recorded), 12)))
	}
	limit := videoLimit
	if item.kind == "image" {
		limit = imageLimit
	}
	if item.kind != "text" && len(data) > limit {
		return nil, stagedCopy{}, refused(fmt.Sprintf("%s: %s is %d bytes, over GitHub's %d byte limit for %ss", role, file, len(data), limit, item.kind))
	}
	info, err := evidence.InspectMedia(real)
	if err != nil {
		return nil, stagedCopy{}, err
	}
	if item.kind == "image" && (!value.Equal(info.Get("width"), manifest.Get("width")) || !value.Equal(info.Get("height"), manifest.Get("height"))) {
		return nil, stagedCopy{}, refused(fmt.Sprintf("%s: %s reads as %sx%s, manifest says %sx%s", role, file,
			value.Text(info.Get("width")), value.Text(info.Get("height")), value.Text(manifest.Get("width")), value.Text(manifest.Get("height"))))
	}
	ext := ".txt"
	var textRole any
	if item.kind == "text" {
		if !utf8.Valid(data) {
			return nil, stagedCopy{}, refused(fmt.Sprintf("%s: %s is not UTF-8 text", role, file))
		}
		if text := string(data); p.redactor.Redact(text) != text {
			return nil, stagedCopy{}, refused(role + ": secret-shaped text in transcript; publish only a redacted capture")
		}
		textRole = manifest.Get("role")
	} else {
		mime := value.Str(manifest.Get("type"))
		if ext = imageTypes[mime]; ext == "" {
			ext = videoTypes[mime]
		}
	}
	duration := info.Get("duration_ms")
	if !value.Truthy(duration) {
		duration = manifest.Get("duration_ms")
	}
	source, _ := filepath.Rel(p.runDir, real)
	name := publishName(scenario, role, digest, ext)
	return value.NewObject("role", role, "kind", item.kind, "sha256", digest, "bytes", len(data), "type", manifest.Get("type"), "width", info.Get("width"),
		"height", info.Get("height"), "duration_ms", duration, "source", source, "publish_copy", name, "text_role", textRole), stagedCopy{name, data}, nil
}

func firstChars(text string, n int) string {
	if runes := []rune(text); len(runes) > n {
		return string(runes[:n])
	}
	return text
}

func cmdPlan(argv []string) (int, error) {
	opts, err := args.Parse(planSpec, argv)
	if err != nil {
		return 0, err
	}
	run, candidate, repo := opts.String("--run"), opts.String("--candidate"), opts.String("--repo")
	var rootDir string
	if given := opts.String("--evidence-root"); given != "" {
		rootDir = pycompat.ResolveExistingPrefix(pycompat.ExpandUser(given))
	} else {
		root, err := evidence.ProjectRoot()
		if err != nil {
			return 0, err
		}
		rootDir, _ = evidence.EvidenceRoot(root)
	}
	runDir := pycompat.ResolveExistingPrefix(filepath.Join(rootDir, evidence.SafeName(run)))
	if info, err := os.Stat(runDir); err != nil || !info.IsDir() {
		return 0, refused(fmt.Sprintf("run %s not found under %s", run, rootDir))
	}
	if !fullSHA.MatchString(candidate) {
		return 0, refused("--candidate must be the full 40-hex candidate SHA recorded for the PR head")
	}
	if !ownerName.MatchString(repo) {
		return 0, refused("--repo must be owner/name")
	}
	publishDir := filepath.Join(runDir, "publish")
	if given := opts.String("--publish-dir"); given != "" {
		publishDir = pycompat.ResolveExistingPrefix(pycompat.ExpandUser(given))
	}
	mediaDir := filepath.Join(publishDir, "media")
	scenarios := opts.Strings("--scenario")
	if len(scenarios) == 0 {
		manifests, _ := filepath.Glob(filepath.Join(runDir, "*", "comparison.json"))
		for _, manifest := range manifests {
			scenarios = append(scenarios, filepath.Base(filepath.Dir(manifest)))
		}
		sort.Strings(scenarios)
	}
	if len(scenarios) == 0 {
		return 0, refused(fmt.Sprintf("run %s has no comparison manifests", run))
	}
	redactor, err := evidence.NewRedactor(opts.Strings("--redact"))
	if err != nil {
		return 0, refused(err.Error())
	}
	planner := scenarioPlanner{runDir: runDir, mediaDir: mediaDir, candidate: candidate, base: opts.String("--base"), redactor: redactor}
	rows := []any{}
	for _, scenario := range scenarios {
		row, err := planner.plan(scenario)
		if err != nil {
			return 0, err
		}
		rows = append(rows, row)
	}
	plan := value.NewObject("schema", schema, "created_at", tool.UTCNow(), "run", run, "evidence_root", rootDir, "run_dir", runDir, "publish_dir", publishDir, "media_dir", mediaDir,
		"destination", value.NewObject("repository", repo, "number", opts.Int("--pr")), "candidate", candidate, "base", opts.Optional("--base"),
		"verification_runs", value.Strings(opts.Strings("--verification-run")), "captured_by", opts.Optional("--captured-by"), "scenarios", rows)
	summarise := func() (publishable []string) {
		media := 0
		refusedRows := value.NewObject()
		for _, r := range rows {
			row := value.Obj(r)
			if row.Get("publishable") == true {
				publishable = append(publishable, value.Str(row.Get("id")))
				media += len(value.List(row.Get("media")))
			} else {
				refusedRows.Set(value.Str(row.Get("id")), row.Get("findings"))
			}
		}
		plan.Set("publishable_media", media)
		plan.Set("publishable_scenarios", value.Strings(publishable))
		plan.Set("refused_scenarios", refusedRows)
		return publishable
	}
	publishable := summarise()
	plan.Set("block_preview", nil)
	if publishable != nil {
		preview, err := renderBlock(plan, func(string) string { return "" }, false)
		if err != nil {
			return 0, err
		}
		mark := redactor.Count()
		redactor.Redact(preview)
		if redactor.Count() > mark {
			for _, r := range rows {
				if row := value.Obj(r); row.Get("publishable") == true {
					row.Set("publishable", false)
					row.Set("findings", append(value.List(row.Get("findings")), "the rendered block contained secret-shaped text"))
				}
			}
			publishable = summarise()
		} else {
			plan.Set("block_preview", preview)
		}
	}
	planPath := filepath.Join(publishDir, "plan.json")
	if err := writeJSON(planPath, plan); err != nil {
		return 0, err
	}
	if opts.Bool("--json") {
		if err := tool.WriteJSON(os.Stdout, plan); err != nil {
			return 0, err
		}
	} else {
		fmt.Printf("plan: %s publishable=%s media=%s\n", planPath, value.Repr(plan.Get("publishable_scenarios")), value.Text(plan.Get("publishable_media")))
		refusedRows := value.Obj(plan.Get("refused_scenarios"))
		for _, id := range refusedRows.Keys() {
			findings := make([]string, 0)
			for _, finding := range value.List(refusedRows.Get(id)) {
				findings = append(findings, value.Text(finding))
			}
			fmt.Printf("  %s: not published: %s\n", id, strings.Join(findings, "; "))
		}
	}
	if publishable == nil {
		return 1, nil
	}
	return 0, nil
}
