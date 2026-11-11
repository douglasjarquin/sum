package publish

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/douglasjarquin/sum/tools/verify/internal/args"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
	"github.com/douglasjarquin/sum/tools/verify/internal/value"
)

const (
	bodyLimit   = 65536 // GitHub pull request body character limit.
	attachBatch = 50    // `gh --attach` accepts at most 50 files per command.
)

var exitCodes = map[string]int{"published": 0, "unchanged": 0, "planned": 0, "refused": 1, "failed": 1, "deferred": 2, "uncertain": 4}

var publishSpec = args.Spec{Options: []args.Option{
	{Name: "--plan", Values: 1, Required: true},
	{Name: "--receipts", Values: 1},
	{Name: "--visibility", Values: 1, Required: true, Choices: []string{"public", "private", "internal"}},
	{Name: "--gh", Values: 1},
	{Name: "--timeout", Values: 1, Int: true, Default: "300"},
	{Name: "--allow-head-mismatch"},
	{Name: "--replace-foreign-block"},
	{Name: "--dry-run"},
	{Name: "--json"},
}}

type publisher struct {
	opts         args.Parsed
	plan         *value.Object
	destination  *value.Object
	repo         string
	number       int
	target       string // repo#number, as receipts name the destination.
	candidate    string
	publishDir   string
	mediaDir     string
	receiptsPath string
	receipts     *value.Object
	gh           *gh
	timeout      int
	result       *value.Object
}

// editFailure is why a `gh pr edit` may not have been applied: it exited non-zero ("partial") or did not finish ("timeout").
type editFailure struct{ kind, message string }

func newPublisher(opts args.Parsed) (*publisher, error) {
	planPath := opts.String("--plan")
	plan, err := loadJSON(planPath, "publish plan")
	if err != nil {
		return nil, err
	}
	if !value.Equal(plan.Get("schema"), schema) {
		return nil, refused(fmt.Sprintf("plan schema %s is not %d", value.Repr(plan.Get("schema")), schema))
	}
	p := &publisher{opts: opts, plan: plan, destination: value.Obj(plan.Get("destination")), candidate: value.Str(plan.Get("candidate")),
		publishDir: value.Str(plan.Get("publish_dir")), mediaDir: value.Str(plan.Get("media_dir")), timeout: opts.Int("--timeout")}
	number, _ := value.Number(p.destination.Get("number"))
	p.repo, p.number = value.Str(p.destination.Get("repository")), int(number)
	p.target = fmt.Sprintf("%s#%d", p.repo, p.number)
	p.receiptsPath = filepath.Join(p.publishDir, "receipts.json")
	if given := opts.String("--receipts"); given != "" {
		p.receiptsPath = pycompat.ResolveExistingPrefix(pycompat.ExpandUser(given))
	}
	if p.receipts, err = loadReceipts(p.receiptsPath); err != nil {
		return nil, err
	}
	p.gh = newGh(opts.String("--gh"), p.timeout)
	unpublished := value.NewObject()
	if refusedScenarios := value.Obj(plan.Get("refused_scenarios")); refusedScenarios != nil {
		unpublished = refusedScenarios.Clone()
	}
	p.result = value.NewObject("schema", schema, "at", tool.UTCNow(), "destination", p.destination, "candidate", plan.Get("candidate"), "run", plan.Get("run"), "outcome", nil, "reason", nil,
		"uploaded", []any{}, "reused", []any{}, "unpublished", unpublished, "edits", 0, "video_table", nil, "pr_url", nil,
		"receipts", p.receiptsPath, "plan", pycompat.ResolveExistingPrefix(planPath), "dry_run", opts.Bool("--dry-run"))
	return p, nil
}

func (p *publisher) urlFor(sha string) string {
	return recordedURL(p.receipts, sha, p.destination)
}

func (p *publisher) saveReceipts() error {
	return writeJSON(p.receiptsPath, p.receipts)
}

func (p *publisher) event(fields ...any) error {
	recordEvent(p.receipts, fields...)
	return p.saveReceipts()
}

// fetch reads the pull request as GitHub has it now.
func (p *publisher) fetch() (*value.Object, error) {
	return p.gh.json([]string{"pr", "view", strconv.Itoa(p.number), "--repo", p.repo, "--json", "body,headRefOid,state,url,number"}, 60)
}

func (p *publisher) edit(bodyFile, dir string, check bool, attach ...string) (int, string, error) {
	arguments := append([]string{"pr", "edit", strconv.Itoa(p.number), "--repo", p.repo, "--body-file", bodyFile}, attach...)
	result, err := p.gh.run(ghCall{args: arguments, dir: dir, timeout: p.timeout, check: check})
	return result.Code, output(result), err
}

// media is every image and video of the publishable scenarios; text travels inside the block and is never attached.
func (p *publisher) media() []*value.Object {
	var media []*value.Object
	for _, r := range value.List(p.plan.Get("scenarios")) {
		if row := value.Obj(r); row.Get("publishable") == true {
			for _, m := range value.List(row.Get("media")) {
				if value.Obj(m).Get("kind") != "text" {
					media = append(media, value.Obj(m))
				}
			}
		}
	}
	return media
}

func (p *publisher) bumpEdits() {
	edits, _ := value.Number(p.result.Get("edits"))
	p.result.Set("edits", int(edits)+1)
}

func (p *publisher) write(name, text string) (string, error) {
	path := filepath.Join(p.publishDir, name)
	return path, os.WriteFile(path, []byte(text), 0o644)
}

// finish records how the publication ended, in the result and as a durable file under results/.
func (p *publisher) finish(outcome string, reason any, extra ...any) (*value.Object, error) {
	p.result.Set("outcome", outcome)
	p.result.Set("reason", reason)
	for i := 0; i+1 < len(extra); i += 2 {
		p.result.Set(extra[i].(string), extra[i+1])
	}
	path := filepath.Join(p.publishDir, "results", strings.ReplaceAll(tool.UTCNow(), ":", "")+"-"+outcome+".json")
	if err := writeJSON(path, p.result); err != nil {
		return nil, err
	}
	p.result.Set("record", path)
	return p.result, nil
}

func sha256Of(m *value.Object) string {
	return value.Str(m.Get("sha256"))
}

func shas(media []*value.Object) []any {
	list := []any{}
	for _, m := range media {
		list = append(list, sha256Of(m))
	}
	return list
}

func body(pr *value.Object) string {
	return value.Str(pr.Get("body"))
}

// isRefusal reports whether err is a refusal or a gh timeout, the two failures a publication step absorbs into its result.
func isRefusal(err error) bool {
	switch err.(type) {
	case refused, ghTimeout:
		return true
	}
	return false
}

func (p *publisher) run() (*value.Object, error) {
	if !value.Truthy(p.plan.Get("publishable_scenarios")) {
		return p.finish("refused", "the plan has nothing publishable; see unpublished")
	}
	caps := p.gh.capabilities()
	p.result.Set("gh", caps)
	reason := func(fallback string) any {
		if value.Truthy(caps.Get("reason")) {
			return caps.Get("reason")
		}
		return fallback
	}
	if !value.Truthy(caps.Get("gh")) || caps.Get("authenticated") == nil {
		return p.finish("deferred", reason("GitHub CLI capability probe unavailable"))
	}
	if len(p.media()) > 0 && caps.Get("attach") != true {
		return p.finish("deferred", reason("gh --attach unavailable"))
	}
	if caps.Get("authenticated") == false {
		return p.finish("refused", "gh is not authenticated; nothing was uploaded or edited")
	}
	repo, err := p.gh.json([]string{"repo", "view", p.repo, "--json", "nameWithOwner,visibility,viewerPermission"}, 60)
	if err != nil {
		return nil, err
	}
	if repo.Get("nameWithOwner") != p.repo {
		return p.finish("refused", fmt.Sprintf("gh resolved %s, not the planned destination %s", value.Repr(repo.Get("nameWithOwner")), p.repo))
	}
	visibility := strings.ToLower(textOrEmpty(repo.Get("visibility")))
	p.result.Set("visibility", visibility)
	if declared := p.opts.String("--visibility"); visibility != declared {
		return p.finish("refused", fmt.Sprintf("destination %s is %s; --visibility %s was declared. Nothing was uploaded.", p.repo, visibility, declared))
	}
	switch strings.ToUpper(textOrEmpty(repo.Get("viewerPermission"))) {
	case "WRITE", "MAINTAIN", "ADMIN":
	default:
		permission := "no"
		if value.Truthy(repo.Get("viewerPermission")) {
			permission = value.Text(repo.Get("viewerPermission"))
		}
		return p.finish("refused", fmt.Sprintf("gh has %s permission on %s; editing the PR needs write access. Nothing was uploaded.", permission, p.repo))
	}
	pr, err := p.fetch()
	if err != nil {
		return nil, err
	}
	p.result.Set("pr_url", pr.Get("url"))
	if state := pr.Get("state"); strings.ToUpper(value.Text(state)) != "OPEN" {
		return p.finish("refused", fmt.Sprintf("PR #%d is %s; evidence is published only to an open PR", p.number, value.Text(state)))
	}
	if head := pr.Get("headRefOid"); head != p.candidate {
		p.result.Set("head_sha", head)
		if !p.opts.Bool("--allow-head-mismatch") {
			return p.finish("refused", fmt.Sprintf("PR head %s is not the candidate %s the evidence was captured from; pass --allow-head-mismatch to publish it labelled as such",
				firstChars(value.Text(head), 12), firstChars(p.candidate, 12)))
		}
	}
	// Rebase onto the latest body: compute, refetch, and only proceed when nothing moved in between (bounded).
	known := map[string]bool{}
	for _, b := range value.List(p.receipts.Get("blocks")) {
		if block := value.Obj(b); block.Get("destination") == p.target {
			known[value.Str(block.Get("sha256"))] = true
		}
	}
	current := body(pr)
	var at *span
	var newBody string
	settled := false
	for attempt := 0; attempt < 3 && !settled; attempt++ {
		at, err = findBlock(current)
		if err != nil {
			return p.finish("refused", err.Error()+". Nothing was uploaded or edited.")
		}
		if at != nil {
			if existing := blockHash(current[at.start:at.end]); !known[existing] && !p.opts.Bool("--replace-foreign-block") {
				p.result.Set("existing_block_sha256", existing)
				return p.finish("refused", "the PR already carries a before-and-after block this publisher did not write (someone edited it, or another tool wrote it); "+
					"inspect it and pass --replace-foreign-block to take it over. Prose outside the block is never touched either way.")
			}
		}
		videosKnown := true
		for _, m := range p.media() {
			videosKnown = videosKnown && (m.Get("kind") != "video" || p.urlFor(sha256Of(m)) != "")
		}
		block, err := renderBlock(p.plan, p.urlFor, videosKnown)
		if err != nil {
			return p.finish("refused", err.Error())
		}
		newBody = splice(current, at, block)
		if length := utf8.RuneCountInString(newBody); length > bodyLimit {
			return p.finish("refused", fmt.Sprintf("PR body is %d characters, over the %d character limit; retain evidence and publish a smaller scenario set", length, bodyLimit))
		}
		again, err := p.fetch()
		if err != nil {
			return nil, err
		}
		settled = body(again) == current
		current = body(again)
	}
	if !settled {
		return p.finish("refused", "the PR body changed three times while preparing the edit; retry when it is quiet")
	}
	var toUpload, reused []*value.Object
	for _, m := range p.media() {
		if p.urlFor(sha256Of(m)) == "" {
			toUpload = append(toUpload, m)
		} else {
			reused = append(reused, m)
		}
	}
	p.result.Set("reused", shas(reused))
	if toUpload == nil && at != nil {
		final, err := renderBlock(p.plan, p.urlFor, true)
		if err != nil {
			return nil, err
		}
		if normalize(current[at.start:at.end]) == normalize(final) {
			return p.finish("unchanged", "the PR already carries this exact block with every attachment; nothing to upload or edit")
		}
	}
	if len(toUpload) > attachBatch {
		return p.finish("refused", fmt.Sprintf("%d files to attach exceeds gh's %d per command; publish fewer scenarios per run", len(toUpload), attachBatch))
	}
	var attach []string
	for _, m := range toUpload {
		name := value.Str(m.Get("publish_copy"))
		if data, err := os.ReadFile(filepath.Join(p.mediaDir, name)); err != nil || tool.SHA256(data) != sha256Of(m) {
			return p.finish("refused", "publish copy "+name+" is missing or altered; run plan again")
		}
		attach = append(attach, "--attach", "./"+name)
	}
	p.result.Set("to_upload", shas(toUpload))
	if p.opts.Bool("--dry-run") {
		p.result.Set("body_preview", newBody)
		return p.finish("planned", "dry run: nothing uploaded or edited")
	}
	bodyFile, err := p.write("body-next.md", newBody)
	if err != nil {
		return nil, err
	}
	if _, err := p.write("body-previous.md", current); err != nil {
		return nil, err
	}
	if err := p.event("kind", "edit-attempt", "destination", p.target, "attachments", shas(toUpload), "previous_body_sha256", tool.SHA256([]byte(current))); err != nil {
		return nil, err
	}
	var failure *editFailure
	code, said, err := p.edit(bodyFile, p.mediaDir, false, attach...)
	if timeout, timedOut := err.(ghTimeout); timedOut {
		failure = &editFailure{"timeout", string(timeout)}
	} else if err != nil {
		return nil, err
	} else if code != 0 {
		failure = &editFailure{"partial", fmt.Sprintf("gh pr edit exited %d: %s", code, lastChars(said, 600))}
	}
	p.bumpEdits()
	return p.reconcile(current, at, toUpload, failure)
}

// reconcile runs after a write that may or may not have landed: read GitHub, record whatever attachments exist, and either finish the
// block or restore the body.
func (p *publisher) reconcile(previousBody string, previousSpan *span, toUpload []*value.Object, failure *editFailure) (*value.Object, error) {
	now, err := p.fetch()
	if isRefusal(err) {
		if err := p.event("kind", "uncertain", "reason", err.Error()); err != nil {
			return nil, err
		}
		sent := "was sent"
		if failure != nil {
			sent = "failed"
		}
		return p.finish("uncertain", fmt.Sprintf("the edit %s and the PR could not be read back: %v. Inspect the PR; receipts may be incomplete; do not retry blindly.", sent, err))
	}
	if err != nil {
		return nil, err
	}
	current := body(now)
	if current == previousBody {
		logged, why, tail := "body unchanged", "gh reported success but the PR body did not change", "."
		if failure != nil {
			logged, why = failure.message, failure.message
			if failure.kind == "timeout" {
				tail = "; an upload may have completed without being referenced, which GitHub keeps harmlessly."
			}
		}
		if err := p.event("kind", "edit-not-applied", "reason", logged); err != nil {
			return nil, err
		}
		return p.finish("failed", why+". The PR body is intact; local evidence is untouched"+tail)
	}
	at, err := findBlock(current)
	if err != nil {
		p.result.Set("malformed", err.Error())
	}
	block := ""
	if at != nil {
		block = current[at.start:at.end]
	}
	harvested := harvestURLs(withoutText(block))
	attachments := value.Obj(p.receipts.Get("attachments"))
	for _, m := range toUpload {
		if url := harvested[sha256Of(m)]; url != "" {
			attachments.Set(receiptKey(sha256Of(m), p.destination), value.NewObject("url", url, "sha256", sha256Of(m), "kind", m.Get("kind"), "file", m.Get("publish_copy"),
				"published_at", tool.UTCNow(), "destination", p.target, "candidate", p.plan.Get("candidate"), "run", p.plan.Get("run")))
			p.result.Set("uploaded", append(value.List(p.result.Get("uploaded")), sha256Of(m)))
		}
	}
	if err := p.saveReceipts(); err != nil {
		return nil, err
	}
	missing := 0
	for _, m := range toUpload {
		if p.urlFor(sha256Of(m)) == "" {
			missing++
		}
	}
	proseOK := at != nil && normalize(outside(current, at)) == normalize(outside(previousBody, previousSpan))
	leftovers := localRef.FindAllString(withoutText(block), -1)
	contentOK := false
	if expected, err := renderBlock(p.plan, p.urlFor, false); err == nil {
		if expectedFinal, err := renderBlock(p.plan, p.urlFor, true); err == nil {
			contentOK = normalize(block) == normalize(expected) || normalize(block) == normalize(expectedFinal)
		}
	}
	if failure != nil && missing == 0 && leftovers == nil && proseOK {
		p.result.Set("gh_warning", failure.message)
	}
	if missing > 0 || leftovers != nil || !proseOK || !contentOK {
		var reasons []string
		if failure != nil {
			reasons = append(reasons, failure.message)
		}
		if missing > 0 {
			reasons = append(reasons, fmt.Sprintf("%d attachment(s) have no URL", missing))
		}
		if leftovers != nil {
			reasons = append(reasons, "local references remain in the block: "+strings.Join(leftovers[:min(5, len(leftovers))], ", "))
		}
		if !contentOK {
			reasons = append(reasons, "published evidence block differs from the planned content")
		}
		if !proseOK {
			reasons = append(reasons, "prose outside the block differs from the body read before the edit (concurrent edit or a malformed block)")
		}
		reason := strings.Join(reasons, "; ")
		if !proseOK || !contentOK {
			if _, err := p.write("body-observed.md", current); err != nil {
				return nil, err
			}
			if err := p.event("kind", "uncertain", "reason", reason); err != nil {
				return nil, err
			}
			return p.finish("uncertain", reason+". The observed PR body was preserved; reconcile it with body-next.md and body-previous.md before retrying.", "restored", false)
		}
		restored, err := p.restore(previousBody)
		if err != nil {
			return nil, err
		}
		outcome, what := "uncertain", ". The PR body could NOT be restored; inspect it"
		if restored {
			outcome, what = "failed", ". The previous PR body was restored"
		}
		return p.finish(outcome, reason+what+"; uploaded attachments are kept in receipts for reuse.", "restored", restored)
	}
	p.appendBlock(block)
	if err := p.event("kind", "published", "destination", p.target, "uploaded", p.result.Get("uploaded"), "reused", p.result.Get("reused")); err != nil {
		return nil, err
	}
	// Second step: video pairs become an HTML table now that every URL is known (own-line players stay when this step cannot be applied).
	final, err := renderBlock(p.plan, p.urlFor, true)
	if err != nil {
		return nil, err
	}
	hasVideo := false
	for _, m := range p.media() {
		hasVideo = hasVideo || m.Get("kind") == "video"
	}
	if hasVideo && normalize(final) != normalize(block) {
		table, err := p.upgrade(current, at, final)
		if err != nil {
			return nil, err
		}
		p.result.Set("video_table", table)
	}
	how := "block appended"
	if previousSpan != nil {
		how = "block replaced in place"
	}
	return p.finish("published", fmt.Sprintf("%d uploaded, %d reused, %s", len(value.List(p.result.Get("uploaded"))), len(value.List(p.result.Get("reused"))), how))
}

// appendBlock records a block as written by this publisher, so the next publication recognises it as its own.
func (p *publisher) appendBlock(block string) {
	p.receipts.Set("blocks", append(value.List(p.receipts.Get("blocks")), value.NewObject("destination", p.target, "sha256", blockHash(block), "at", tool.UTCNow(),
		"candidate", p.plan.Get("candidate"), "run", p.plan.Get("run"))))
}

// upgrade replaces the own-line videos with the final table and says whether that edit was applied.
func (p *publisher) upgrade(current string, at *span, final string) (string, error) {
	bodyFile, err := p.write("body-final.md", splice(current, at, final))
	if err != nil {
		return "", err
	}
	uncertain := func(err error) (string, error) {
		if isRefusal(err) {
			return "uncertain: " + err.Error(), nil
		}
		return "", err
	}
	code, said, err := p.edit(bodyFile, p.mediaDir, false)
	if err != nil {
		return uncertain(err)
	}
	p.bumpEdits()
	after, err := p.fetch()
	if err != nil {
		return uncertain(err)
	}
	landed := body(after)
	landedAt, err := findBlock(landed)
	if err != nil {
		return uncertain(err)
	}
	if code != 0 {
		return "not-applied: own-line videos stay (" + lastChars(said, 200) + ")", nil
	}
	if landedAt == nil || normalize(landed[landedAt.start:landedAt.end]) != normalize(final) {
		return "not-applied: own-line videos stay (block differs after the edit)", nil
	}
	p.appendBlock(landed[landedAt.start:landedAt.end])
	return "applied", p.saveReceipts()
}

// restore puts the previous body back and reports whether GitHub now shows exactly that body.
func (p *publisher) restore(previousBody string) (bool, error) {
	path, err := p.write("body-restore.md", previousBody)
	if err != nil {
		return false, err
	}
	_, _, err = p.edit(path, "", true)
	var now *value.Object
	if err == nil {
		if err = p.event("kind", "restored", "destination", p.target); err != nil {
			return false, err
		}
		now, err = p.fetch()
	}
	if isRefusal(err) {
		return false, p.event("kind", "restore-failed", "reason", err.Error())
	}
	if err != nil {
		return false, err
	}
	return body(now) == previousBody, nil
}

func cmdPublish(argv []string) (int, error) {
	opts, err := args.Parse(publishSpec, argv)
	if err != nil {
		return 0, err
	}
	p, err := newPublisher(opts)
	if err != nil {
		return 0, err
	}
	result, err := p.run()
	if err != nil {
		return 0, err
	}
	outcome := value.Str(result.Get("outcome"))
	if opts.Bool("--json") {
		if err := tool.WriteJSON(os.Stdout, result); err != nil {
			return 0, err
		}
	} else {
		fmt.Printf("publish: %s %s: %s\n", outcome, p.target, value.Text(result.Get("reason")))
		unpublished := value.Obj(result.Get("unpublished"))
		for _, id := range unpublished.Keys() {
			var findings []string
			for _, finding := range value.List(unpublished.Get(id)) {
				findings = append(findings, value.Text(finding))
			}
			fmt.Printf("  unpublished %s: %s\n", id, strings.Join(findings, "; "))
		}
	}
	if code, known := exitCodes[outcome]; known {
		return code, nil
	}
	return 1, nil
}
