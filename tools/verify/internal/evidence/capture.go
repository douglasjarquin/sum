package evidence

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/tools/verify/internal/evidence/args"
	"github.com/douglasjarquin/sum/tools/verify/internal/evidence/value"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
)

const schema = 1

var (
	kinds   = []string{"bugfix", "feature", "visual", "nonvisual"}
	recipes = []string{"browser", "cli", "http"}
)

var captureSpec = args.Spec{Trailing: true, Options: []args.Option{
	{Name: "--scenario", Values: 1, Required: true},
	{Name: "--feature", Values: 1},
	{Name: "--role", Values: 1, Required: true, Choices: Roles},
	{Name: "--kind", Values: 1, Required: true, Choices: kinds},
	{Name: "--run", Values: 1},
	{Name: "--checkout", Values: 1},
	{Name: "--expect-sha", Values: 1},
	{Name: "--build-id", Values: 1},
	{Name: "--note", Values: 1},
	{Name: "--redact", Values: 1},
	{Name: "--intentional", Values: 1},
}}

var unavailableSpec = args.Spec{Options: []args.Option{
	{Name: "--scenario", Values: 1, Required: true},
	{Name: "--role", Values: 1, Choices: Roles, Default: "before"},
	{Name: "--kind", Values: 1, Choices: kinds, Default: "bugfix"},
	{Name: "--reason", Values: 1, Required: true},
	{Name: "--run", Values: 1},
}}

// capture is one capture in progress: where its files go, the record being built, and the redactor every recorded text passes through.
type capture struct {
	dir    string
	kind   string
	record *value.Object
	redact *Redactor
}

func (c *capture) list(key string) []any {
	return value.List(c.record.Get(key))
}

func (c *capture) appendTo(key string, items ...any) {
	c.record.Set(key, append(c.list(key), items...))
}

func (c *capture) assert(expectation string, met bool, observed ...any) {
	assertion := value.NewObject("expectation", expectation, "met", met)
	if len(observed) > 0 {
		assertion.Set("observed", observed[0])
	}
	c.appendTo("assertions", assertion)
}

// withRole labels an inspected file with what it is in this capture.
func withRole(info *value.Object, role string, more ...any) *value.Object {
	info.Set("role", role)
	info.Set("derived", false)
	for i := 0; i+1 < len(more); i += 2 {
		info.Set(more[i].(string), more[i+1])
	}
	return info
}

func writeRecord(path string, record any) error {
	data, err := tool.EncodeJSON(record)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func runID(given string) string {
	if given != "" {
		return given
	}
	if fromEnv := os.Getenv("EVIDENCE_RUN"); fromEnv != "" {
		return fromEnv
	}
	return newRunID()
}

// checkoutIdentity records which build a capture was driven from: the checkout's HEAD, whether it was dirty, and whether it is the expected SHA.
func checkoutIdentity(checkout, expectSHA string, buildID any) (*value.Object, error) {
	record := value.NewObject("path", nil, "sha", nil, "dirty", nil, "branch", nil, "build_id", buildID, "expected_sha", orNull(expectSHA), "matches_expected", nil)
	sha := ""
	if checkout != "" {
		path := ResolvePath(checkout)
		code, top, stderr := git(path, "rev-parse", "--show-toplevel")
		if code != 0 {
			return nil, blocked(fmt.Sprintf("--checkout %s is not a Git work tree: %s", checkout, head(stderr, 200)))
		}
		_, sha, _ = git(path, "rev-parse", "HEAD")
		_, status, _ := git(path, "status", "--porcelain", "--untracked-files=normal")
		_, branch, _ := git(path, "rev-parse", "--abbrev-ref", "HEAD")
		record.Set("path", top)
		record.Set("sha", sha)
		record.Set("dirty", status != "")
		record.Set("branch", orNull(branch))
		if expectSHA != "" {
			record.Set("matches_expected", strings.HasPrefix(sha, expectSHA) || strings.HasPrefix(expectSHA, sha))
		}
	}
	return record, nil
}

func environmentFacts() *value.Object {
	_, node := nodeVersion()
	return value.NewObject("platform", runtime.GOOS+"-"+runtime.GOARCH, "node", node, "hostname_recorded", false, "user_profile", "none (ephemeral)")
}

func feature(scenario string) string {
	name, _, _ := strings.Cut(scenario, ".")
	return name
}

func cmdCapture(argv []string) (int, error) {
	top, err := args.Parse(captureSpec, argv)
	if err != nil {
		return 0, err
	}
	if len(top.Rest) == 0 {
		return 0, args.UsageError("the following arguments are required: recipe")
	}
	recipe := top.Rest[0]
	spec, ok := map[string]args.Spec{"browser": browserSpec, "cli": cliSpec, "http": httpSpec}[recipe]
	if !ok {
		return 0, args.UsageError(fmt.Sprintf("argument recipe: invalid choice: '%s' (choose from %s)", recipe, strings.Join(recipes, ", ")))
	}
	opts, err := args.Parse(spec, top.Rest[1:])
	if err != nil {
		return 0, err
	}
	root, err := ProjectRoot()
	if err != nil {
		return 0, err
	}
	rootDir, source := EvidenceRoot(root)
	run := runID(top.String("--run"))
	redact, err := NewRedactor(top.Strings("--redact"))
	if err != nil {
		return 0, usage(err.Error())
	}
	scenario, role, kind := top.String("--scenario"), top.String("--role"), top.String("--kind")
	build, err := checkoutIdentity(top.String("--checkout"), top.String("--expect-sha"), top.Optional("--build-id"))
	if err != nil {
		return 0, err
	}
	sha := value.Str(build.Get("sha"))
	stem := "nobuild"
	if sha != "" {
		stem = head(sha, 12)
	}
	dir := filepath.Join(scenarioDir(rootDir, run, scenario), role+"-"+stem)
	if _, err := os.Lstat(dir); err == nil {
		return 0, usage(dir + " already exists; a capture is never overwritten. Use a new --run or remove it deliberately.")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	featureID := top.String("--feature")
	if featureID == "" {
		featureID = feature(scenario)
	}
	intentional := value.NewObject()
	for _, pair := range top.Strings("--intentional") {
		if key, val, ok := strings.Cut(pair, "="); ok {
			intentional.Set(key, val)
		}
	}
	record := value.NewObject("schema", schema, "run", run, "scenario", scenario, "feature", featureID, "role", role, "kind", kind,
		"recipe", recipe, "note", top.Optional("--note"), "started_at", tool.UTCNow(), "checkout", build, "environment", environmentFacts(), "steps", []any{},
		"assertions", []any{}, "limitations", []any{}, "intentional_differences", intentional,
		"media", []any{}, "outcome", nil, "blocked_reason", nil, "evidence_root", value.NewObject("path", rootDir, "declared_by", source))
	c := &capture{dir: dir, kind: kind, record: record, redact: redact}
	clock := time.Now()
	var media []any
	if build.Get("matches_expected") == false {
		err = blocked(fmt.Sprintf("build mismatch: --checkout HEAD %s is not the expected %s; this is a stale or wrong build", sha, top.String("--expect-sha")))
	} else {
		switch recipe {
		case "browser":
			media, err = c.browser(opts)
		case "cli":
			media, err = c.cli(opts)
		default:
			media, err = c.http(opts)
		}
	}
	if reason, isBlocked := err.(blocked); isBlocked {
		record.Set("outcome", "blocked")
		record.Set("blocked_reason", string(reason))
		c.keepDiagnostics()
	} else if err != nil {
		return 0, err
	} else {
		record.Set("media", media)
		outcome := "pass"
		for _, assertion := range c.list("assertions") {
			if value.Obj(assertion).Get("met") != true {
				outcome = "fail"
			}
		}
		record.Set("outcome", outcome)
	}
	if build.Get("dirty") == true {
		c.appendTo("limitations", "the driven checkout had uncommitted changes; the capture is bound to its HEAD but the running code may differ")
	}
	if redact.Count() > 0 {
		c.appendTo("limitations", fmt.Sprintf("%d text span(s) redacted; screenshots cannot be redacted, use synthetic data", redact.Count()))
	}
	record.Set("redaction", value.NewObject("patterns", len(redact.patterns), "count", redact.Count(), "labelled", redact.Count() > 0))
	record.Set("ended_at", tool.UTCNow())
	record.Set("seconds", round3(time.Since(clock).Seconds()))
	hashes := value.NewObject()
	for _, m := range c.list("media") {
		hashes.Set(value.Str(value.Obj(m).Get("file")), value.Obj(m).Get("sha256"))
	}
	record.Set("content_hashes", hashes)
	if err := writeRecord(filepath.Join(dir, "capture.json"), record); err != nil {
		return 0, err
	}
	shown := dir
	if rel, err := filepath.Rel(root, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
		shown = rel
	}
	outcome := value.Str(record.Get("outcome"))
	line := fmt.Sprintf("evidence: %s/capture.json run=%s outcome=%s", shown, run, outcome)
	if reason := value.Str(record.Get("blocked_reason")); reason != "" {
		line += " (" + reason + ")"
	}
	var unmet []string
	for _, assertion := range c.list("assertions") {
		if value.Obj(assertion).Get("met") != true {
			unmet = append(unmet, value.Str(value.Obj(assertion).Get("expectation")))
		}
	}
	if unmet != nil {
		line += " UNMET: " + strings.Join(unmet, "; ")
	}
	fmt.Println(line)
	return map[string]int{"pass": 0, "fail": 1, "blocked": 2}[outcome], nil
}

// keepDiagnostics lists whatever a blocked capture left behind as diagnostic media, so the reason it blocked stays inspectable.
func (c *capture) keepDiagnostics() {
	entries, _ := os.ReadDir(c.dir)
	for _, entry := range entries {
		path := filepath.Join(c.dir, entry.Name())
		if entry.Name() == "capture.json" || !isFile(path) {
			continue
		}
		info, err := InspectMedia(path)
		if err != nil {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				continue
			}
			info = value.NewObject("file", entry.Name(), "bytes", len(data), "role", "diagnostic", "derived", false, "sha256", tool.SHA256(data))
		} else {
			withRole(info, "diagnostic")
		}
		c.appendTo("media", info)
	}
}

func cmdUnavailable(argv []string) (int, error) {
	opts, err := args.Parse(unavailableSpec, argv)
	if err != nil {
		return 0, err
	}
	root, err := ProjectRoot()
	if err != nil {
		return 0, err
	}
	rootDir, source := EvidenceRoot(root)
	run := runID(opts.String("--run"))
	scenario, role, reason := opts.String("--scenario"), opts.String("--role"), opts.String("--reason")
	dir := filepath.Join(scenarioDir(rootDir, run, scenario), role+"-unavailable")
	if _, err := os.Lstat(dir); err == nil {
		return 0, usage(dir + " already exists")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	record := value.NewObject("schema", schema, "run", run, "scenario", scenario, "feature", feature(scenario), "role", role, "kind", opts.String("--kind"), "recipe", nil,
		"started_at", tool.UTCNow(), "ended_at", tool.UTCNow(), "checkout", value.NewObject("sha", nil), "outcome", "unavailable", "blocked_reason", nil, "reason", reason,
		"assertions", []any{}, "media", []any{}, "limitations", []any{fmt.Sprintf("%s state not captured: %s", role, reason)}, "content_hashes", value.NewObject(),
		"evidence_root", value.NewObject("path", rootDir, "declared_by", source))
	if err := writeRecord(filepath.Join(dir, "capture.json"), record); err != nil {
		return 0, err
	}
	fmt.Printf("evidence: %s/capture.json run=%s outcome=unavailable (%s)\n", dir, run, reason)
	return 0, nil
}
