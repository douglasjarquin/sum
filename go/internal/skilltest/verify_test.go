package skilltest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	testFile   = map[string]string{"cli": "tests/test_hello.py", "web": "check.py", "service": "tests/test_app.py"}
	sourceFile = map[string]string{"cli": "hello.py", "web": "serve.py", "service": "app.py"}
)

type verifyLab struct {
	t                   *testing.T
	root, stop, toolkit string
	env                 []string
}

func newVerifyLab(t *testing.T) *verifyLab {
	t.Helper()
	root := repoRoot(t)
	stop := t.TempDir()
	toolkit := filepath.Join(stop, "toolkit/.agents/skills")
	copyTree(t, filepath.Join(root, ".agents/skills"), toolkit)
	return &verifyLab{t: t, root: root, stop: stop, toolkit: toolkit, env: labEnv(t, root, stop)}
}

func (v *verifyLab) rawRepo(fixture, path string) string {
	t := v.t
	copyTree(t, filepath.Join(v.root, "tests/fixtures/verify", fixture), path)
	for _, gone := range []string{"VERIFY.md", "docs/features", ".agents", ".claude"} {
		removeAll(t, filepath.Join(path, gone))
	}
	switch fixture {
	case "cli":
		mustWrite(t, filepath.Join(path, "mise.toml"), "[tasks]\ntest = \"python3 -m unittest discover -s tests -p 'test_*.py'\"\n")
	case "web":
		mustWrite(t, filepath.Join(path, "mise.toml"), "[tasks]\nbuild = \"python3 build.py\"\ncheck = \"python3 check.py\"\nserve = \"python3 serve.py\"\n")
	}
	init := exec.Command("git", "init", "-q", "-b", "main")
	init.Dir = path
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	git(t, path, "config", "user.email", "lab@example.invalid")
	git(t, path, "config", "user.name", "verify lab")
	git(t, path, "add", "-A")
	git(t, path, "commit", "-q", "-m", "ordinary project")
	return path
}

func (v *verifyLab) scaffold(repo string, args ...string) (int, map[string]any, string) {
	script := filepath.Join(v.toolkit, "create-verification/scripts/verify_scaffold.py")
	code, rec, _, stderr := runPy(v.t, v.env, script, v.stop, append([]string{"--root", repo, "--json"}, args...)...)
	return code, rec, stderr
}

func (v *verifyLab) runner(repo string, args ...string) (int, map[string]any, string) {
	script := filepath.Join(repo, ".agents/skills/verify/scripts/verify_run.py")
	code, rec, _, stderr := runPy(v.t, v.env, script, repo, append([]string{"--json"}, args...)...)
	return code, rec, stderr
}

func (v *verifyLab) audit(repo string, args ...string) (int, map[string]any, string) {
	script := filepath.Join(repo, ".agents/skills/maintain-verification/scripts/verify_audit.py")
	code, rec, _, stderr := runPy(v.t, v.env, script, repo, append([]string{"--json"}, args...)...)
	return code, rec, stderr
}

func (v *verifyLab) capture(repo string, args ...string) (int, string, string) {
	script := filepath.Join(repo, ".agents/skills/verify/scripts/verify_capture.py")
	code, _, stdout, stderr := runPy(v.t, v.env, script, repo, args...)
	return code, stdout, stderr
}

func (v *verifyLab) fill(repo, fixture string) {
	t := v.t
	contract := filepath.Join(repo, "VERIFY.md")
	text := readFile(t, contract)
	text = strings.ReplaceAll(text, "TODO(verify): state what this check proves", "the checks in `"+testFile[fixture]+"`")
	text = regexp.MustCompile(`(?m)^TODO\(verify\): how a fresh clone is prepared.*$`).ReplaceAllString(text, "Nothing to install beyond Python 3.")
	text = regexp.MustCompile(`(?m)^TODO\(verify\): name the temporary directories.*$`).ReplaceAllString(text, "Tests use temporary directories and ephemeral loopback ports only.")
	text = regexp.MustCompile(`(?m)^TODO\(verify\): how anything a check started.*$`).ReplaceAllString(text, "Each check stops the process it started; nothing is left listening.")
	mustWrite(t, contract, text)
	indexDir := filepath.Join(repo, "docs/features")
	entries, _ := os.ReadDir(indexDir)
	for _, e := range entries {
		if e.IsDir() || e.Name() == "README.md" {
			continue
		}
		path := filepath.Join(indexDir, e.Name())
		body := readFile(t, path)
		body = strings.ReplaceAll(body, "automated: TODO(verify): name the test that exercises it", "automated: "+testFile[fixture])
		body = strings.ReplaceAll(body, "TODO(verify): the success path a user sees", "the success path answers as documented")
		body = regexp.MustCompile(`TODO\(verify\):[^|\n]*`).ReplaceAllString(body, "observed in `"+sourceFile[fixture]+"` and `"+testFile[fixture]+"`")
		mustWrite(t, path, body)
	}
}

func TestBootstrapThreeFixtures(t *testing.T) {
	v := newVerifyLab(t)
	for _, fixture := range []string{"cli", "web", "service"} {
		t.Run(fixture, func(t *testing.T) {
			v.t = t
			repo := v.rawRepo(fixture, filepath.Join(v.stop, "raw-"+fixture))
			code, plan, stderr := v.scaffold(repo)
			if code != 0 {
				t.Fatal(stderr)
			}
			if asString(plan["mode"]) != "inspect" {
				t.Fatalf("mode %v", plan["mode"])
			}
			if _, err := os.Stat(filepath.Join(repo, "VERIFY.md")); err == nil {
				t.Fatal("VERIFY.md exists after inspect")
			}
			code, written, stderr := v.scaffold(repo, "--write")
			if code != 0 {
				t.Fatal(stderr)
			}
			statuses := map[string]string{}
			for _, r := range asSlice(written["results"]) {
				row := asMap(r)
				statuses[asString(row["path"])] = asString(row["status"])
			}
			if statuses["VERIFY.md"] != "created" || statuses[".agents/skills/verify"] != "vendored" {
				t.Fatalf("statuses %v", statuses)
			}
			v.fill(repo, fixture)
			git(t, repo, "add", "-A")
			git(t, repo, "commit", "-q", "-m", "verification contract and seed maps")
			head := git(t, repo, "rev-parse", "HEAD")
			code, audit, stderr := v.audit(repo)
			if code != 0 || asString(audit["outcome"]) != "clean" {
				t.Fatalf("audit %v %s", audit, stderr)
			}
			code, record, stderr := v.runner(repo, "--base", head)
			if code != 0 || asString(record["outcome"]) != "pass" {
				t.Fatalf("runner %v %s", record, stderr)
			}
			if asString(record["certifies"]) != head {
				t.Fatalf("certifies %v", record["certifies"])
			}
		})
	}
}

func TestBrokenBaseExposed(t *testing.T) {
	v := newVerifyLab(t)
	repo := v.rawRepo("service", filepath.Join(v.stop, "broken"))
	testPath := filepath.Join(repo, "tests/test_app.py")
	mustWrite(t, testPath, strings.ReplaceAll(readFile(t, testPath), `(200, {"ok": True})`, `(200, {"ok": "yes"})`))
	git(t, repo, "commit", "-qam", "regression on the base")
	code, _, _ := v.scaffold(repo, "--write")
	if code != 0 {
		t.Fatal(code)
	}
	v.fill(repo, "service")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "contract")
	code, record, _ := v.runner(repo, "--base", "HEAD")
	if code != 1 || asString(record["outcome"]) != "fail" {
		t.Fatalf("want fail got %v", record)
	}
	empty := filepath.Join(v.stop, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(empty, "README.md"), "# empty\n")
	mustWrite(t, filepath.Join(empty, "tool.py"), "#!/usr/bin/env python3\nprint('hi')\n")
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = empty
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	git(t, empty, "add", "-A")
	git(t, empty, "-c", "user.email=l@x", "-c", "user.name=l", "commit", "-q", "-m", "x")
	code, written, _ := v.scaffold(empty, "--write")
	if code != 1 {
		t.Fatalf("empty scaffold code %d", code)
	}
	ok := false
	for _, p := range asSlice(written["problems"]) {
		if strings.Contains(asString(p), "no existing check was found") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("problems %v", written["problems"])
	}
}

func TestSumRepositoryMapsAuditClean(t *testing.T) {
	v := newVerifyLab(t)
	script := filepath.Join(v.root, ".agents/skills/maintain-verification/scripts/verify_audit.py")
	env := append(append([]string{}, v.env...), "FAKE_MISE_STOP="+v.root)
	code, audit, _, stderr := runPy(t, env, script, v.root, "--json", "--no-record")
	if code != 0 || asString(audit["outcome"]) != "clean" {
		t.Fatalf("sum audit %v %s", audit, stderr)
	}
	ids := map[string]bool{}
	for _, s := range asSlice(asMap(audit["authored"])["scenarios"]) {
		ids[asString(asMap(s)["id"])] = true
	}
	if !ids["verify.three-checkouts"] {
		t.Fatalf("missing scenario ids %v", ids)
	}
}

func TestAuditDetectsStaleClaims(t *testing.T) {
	v := newVerifyLab(t)
	repo := v.rawRepo("cli", filepath.Join(v.stop, "stale"))
	v.scaffold(repo, "--write")
	v.fill(repo, "cli")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "contract")
	code, audit, _ := v.audit(repo)
	if code != 0 || asString(audit["outcome"]) != "clean" {
		t.Fatalf("clean %v", audit)
	}
	mustWrite(t, filepath.Join(repo, "mise.toml"), "[tasks]\nunit = \"python3 -m unittest discover -s tests -p 'test_*.py'\"\n\n[tasks.verify]\nrun = \"mise run unit\"\n")
	if err := os.Rename(filepath.Join(repo, "tests/test_hello.py"), filepath.Join(repo, "tests/test_greeting.py")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, "docs/features/orphan.md"), "# Orphan\n\n| ID | Scenario | Driver | Evidence |\n| --- | --- | --- | --- |\n| `cli.hello` | dup | sometimes | - |\n| `cli.extra` | claims | automated | - |\n")
	index := filepath.Join(repo, "docs/features/README.md")
	mustWrite(t, index, readFile(t, index)+"- [Gone](gone.md)\n")
	code, audit, _ = v.audit(repo)
	if code != 1 || asString(audit["outcome"]) != "findings" {
		t.Fatalf("findings %v", audit)
	}
	kinds := map[string]bool{}
	for _, f := range asSlice(audit["findings"]) {
		kinds[asString(asMap(f)["kind"])] = true
	}
	for _, want := range []string{"stale-task", "stale-path", "coverage-claim", "unlinked-map", "missing-link"} {
		if !kinds[want] {
			t.Fatalf("missing kind %s in %v", want, kinds)
		}
	}
}

func TestExternalCloneFollowsGeneratedInstructions(t *testing.T) {
	v := newVerifyLab(t)
	origin := v.rawRepo("service", filepath.Join(v.stop, "origin"))
	v.scaffold(origin, "--write")
	v.fill(origin, "service")
	git(t, origin, "add", "-A")
	git(t, origin, "commit", "-q", "-m", "contract")
	external := filepath.Join(v.stop, "elsewhere/service")
	if err := os.MkdirAll(filepath.Dir(external), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "clone", "-q", origin, external)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	removeAll(t, filepath.Dir(v.toolkit))
	code, record, stderr := v.runner(external, "--check")
	if code != 0 || asString(record["outcome"]) != "checked" {
		t.Fatalf("check %v %s", record, stderr)
	}
	head := git(t, external, "rev-parse", "HEAD")
	code, record, stderr = v.runner(external, "--base", head)
	if code != 0 || asString(record["outcome"]) != "pass" {
		t.Fatalf("run %v %s", record, stderr)
	}
}

func TestChangeDetectionAndFailingBranch(t *testing.T) {
	v := newVerifyLab(t)
	repo := v.rawRepo("service", filepath.Join(v.stop, "maint"))
	v.scaffold(repo, "--write")
	v.fill(repo, "service")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "contract")
	base := git(t, repo, "rev-parse", "HEAD")
	code, record, _ := v.runner(repo, "--base", base)
	if asString(record["certifies"]) != base {
		t.Fatalf("certifies %v", record["certifies"])
	}
	git(t, repo, "checkout", "-q", "-b", "feature")
	app := filepath.Join(repo, "app.py")
	mustWrite(t, app, strings.ReplaceAll(readFile(t, app), `{"ok": True}`, `{"ok": True, "version": 2}`))
	testp := filepath.Join(repo, "tests/test_app.py")
	mustWrite(t, testp, strings.ReplaceAll(readFile(t, testp), `(200, {"ok": True})`, `(200, {"ok": True, "version": 2})`))
	code, audit, _ := v.audit(repo, "--base", base)
	if code != 0 {
		t.Fatalf("audit after health change %v", audit)
	}
	mustWrite(t, filepath.Join(repo, "util.py"), "def helper():\n    return 1\n")
	code, audit, _ = v.audit(repo, "--base", base)
	if code != 1 {
		t.Fatalf("unmapped %v", audit)
	}
	code, audit, _ = v.audit(repo, "--base", base, "--rationale", "util.py is an internal helper with no user-facing path")
	if code != 0 {
		t.Fatalf("rationale %v", audit)
	}
}

func TestRunnerRejectsTraversalInFreshnessPaths(t *testing.T) {
	v := newVerifyLab(t)
	for _, tc := range []struct {
		name string
		line string
	}{
		{name: "slash", line: "inputs = [\"../outside\"]\n"},
		{name: "embedded-slash", line: "inputs = [\"inside/../outside\"]\n"},
		{name: "backslash", line: `inputs = ['..\outside']` + "\n"},
		{name: "wildcard", line: "inputs = [\"inside/*.txt\"]\n"},
		{name: "pathspec", line: "inputs = [\":(glob)outside\"]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := v.rawRepo("cli", filepath.Join(v.stop, "freshness-traversal-"+tc.name))
			if code, _, stderr := v.scaffold(repo, "--write"); code != 0 {
				t.Fatal(stderr)
			}
			contract := filepath.Join(repo, "VERIFY.md")
			body := strings.Replace(readFile(t, contract), "artifacts = \".artifacts/verification\"\n", "artifacts = \".artifacts/verification\"\n[freshness]\n"+tc.line, 1)
			mustWrite(t, contract, body)
			git(t, repo, "add", "VERIFY.md")
			git(t, repo, "commit", "-q", "-m", "reject traversal freshness path")
			head := git(t, repo, "rev-parse", "HEAD")
			code, record, stderr := v.runner(repo, "--base", head)
			if code != 2 || asString(record["outcome"]) != "blocked" || !strings.Contains(asString(record["blocked_reason"]), "freshness.inputs") {
				t.Fatalf("runner %v code=%d stderr=%s, want a blocked traversal path", record, code, stderr)
			}
		})
	}
}

func TestRunnerRejectsGitPathspecContractPaths(t *testing.T) {
	v := newVerifyLab(t)
	for _, tc := range []struct {
		name  string
		field string
		line  string
	}{
		{name: "feature-maps", field: "feature_maps", line: `feature_maps = "docs/*.md"`},
		{name: "artifacts", field: "artifacts", line: `artifacts = "artifacts/*.log"`},
		{name: "evidence", field: "evidence", line: `evidence = "evidence:(glob)"`},
		{name: "task-owner", field: "task_owner", line: `task_owner = '..\outside'`},
		{name: "policy-files", field: "policy_files", line: `policy_files = ["docs/*.md"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := v.rawRepo("cli", filepath.Join(v.stop, "pathspec-"+tc.name))
			if code, _, stderr := v.scaffold(repo, "--write"); code != 0 {
				t.Fatal(stderr)
			}
			contract := filepath.Join(repo, "VERIFY.md")
			body := readFile(t, contract)
			fieldPattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(tc.field) + ` = .*$`)
			if fieldPattern.MatchString(body) {
				body = fieldPattern.ReplaceAllString(body, tc.line)
			} else {
				body = strings.Replace(body, "artifacts = \".artifacts/verification\"\n", "artifacts = \".artifacts/verification\"\n"+tc.line+"\n", 1)
			}
			mustWrite(t, contract, body)
			git(t, repo, "add", "VERIFY.md")
			git(t, repo, "commit", "-q", "-m", "reject contract pathspec")
			head := git(t, repo, "rev-parse", "HEAD")
			code, record, stderr := v.runner(repo, "--base", head)
			if code != 2 || asString(record["outcome"]) != "blocked" || !strings.Contains(asString(record["blocked_reason"]), tc.field) {
				t.Fatalf("runner %v code=%d stderr=%s, want a blocked %s path", record, code, stderr, tc.field)
			}
		})
	}
}

func TestRunnerRejectsUnsafeLinkedFeatureMapPath(t *testing.T) {
	v := newVerifyLab(t)
	repo := v.rawRepo("cli", filepath.Join(v.stop, "linked-pathspec"))
	if code, _, stderr := v.scaffold(repo, "--write"); code != 0 {
		t.Fatal(stderr)
	}
	index := filepath.Join(repo, "docs/features/README.md")
	body := readFile(t, index)
	pattern := regexp.MustCompile(`\]\([^)]*\.md\)`)
	if !pattern.MatchString(body) {
		t.Fatal("generated feature index has no linked map")
	}
	mustWrite(t, index, pattern.ReplaceAllString(body, `](safe/../outside.md)`))
	git(t, repo, "add", "docs/features/README.md")
	git(t, repo, "commit", "-q", "-m", "reject linked map traversal")
	head := git(t, repo, "rev-parse", "HEAD")
	code, record, stderr := v.runner(repo, "--base", head)
	if code != 2 || asString(record["outcome"]) != "blocked" || !strings.Contains(asString(record["blocked_reason"]), "Feature map link") {
		t.Fatalf("runner %v code=%d stderr=%s, want a blocked linked-map path", record, code, stderr)
	}
}

func TestRunnerRejectsScalarContractLists(t *testing.T) {
	v := newVerifyLab(t)
	for _, tc := range []struct {
		name string
		body func(string) string
		want string
	}{
		{name: "requires-commands", body: func(body string) string {
			return regexp.MustCompile(`(?m)^commands = .*$`).ReplaceAllString(body, `commands = "python3"`)
		}, want: "requires.commands"},
		{name: "freshness-inputs", body: func(body string) string {
			return strings.Replace(body, "artifacts = \".artifacts/verification\"\n", "artifacts = \".artifacts/verification\"\n[freshness]\ninputs = \"inside\"\n", 1)
		}, want: "freshness.inputs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := v.rawRepo("cli", filepath.Join(v.stop, "scalar-"+tc.name))
			if code, _, stderr := v.scaffold(repo, "--write"); code != 0 {
				t.Fatal(stderr)
			}
			contract := filepath.Join(repo, "VERIFY.md")
			mustWrite(t, contract, tc.body(readFile(t, contract)))
			git(t, repo, "add", "VERIFY.md")
			git(t, repo, "commit", "-q", "-m", "reject scalar contract list")
			head := git(t, repo, "rev-parse", "HEAD")
			code, record, stderr := v.runner(repo, "--base", head)
			if code != 2 || asString(record["outcome"]) != "blocked" || !strings.Contains(asString(record["blocked_reason"]), tc.want) {
				t.Fatalf("runner %v code=%d stderr=%s, want a blocked %s value", record, code, stderr, tc.want)
			}
		})
	}
}

func TestRunnerRejectsOversizedContractLists(t *testing.T) {
	v := newVerifyLab(t)
	items := strings.Repeat(`"python3",`, 256) + `"python3"`
	for _, tc := range []struct {
		name string
		line string
		want string
	}{
		{name: "too-many", line: "commands = [" + items + "]\n", want: "more than 256 items"},
		{name: "too-long", line: "commands = [\"" + strings.Repeat("x", 513) + "\"]\n", want: "longer than 512 characters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := v.rawRepo("cli", filepath.Join(v.stop, "oversized-"+tc.name))
			if code, _, stderr := v.scaffold(repo, "--write"); code != 0 {
				t.Fatal(stderr)
			}
			contract := filepath.Join(repo, "VERIFY.md")
			body := regexp.MustCompile(`(?m)^commands = .*$`).ReplaceAllString(readFile(t, contract), strings.TrimSuffix(tc.line, "\n"))
			mustWrite(t, contract, body)
			git(t, repo, "add", "VERIFY.md")
			git(t, repo, "commit", "-q", "-m", "reject oversized contract list")
			head := git(t, repo, "rev-parse", "HEAD")
			code, record, stderr := v.runner(repo, "--base", head)
			if code != 2 || asString(record["outcome"]) != "blocked" || !strings.Contains(asString(record["blocked_reason"]), tc.want) {
				t.Fatalf("runner %v code=%d stderr=%s, want a blocked oversized list", record, code, stderr)
			}
		})
	}
}

func TestRunnerRejectsInvalidUTF8Contract(t *testing.T) {
	v := newVerifyLab(t)
	repo := v.rawRepo("cli", filepath.Join(v.stop, "invalid-utf8"))
	if code, _, stderr := v.scaffold(repo, "--write"); code != 0 {
		t.Fatal(stderr)
	}
	if err := os.WriteFile(filepath.Join(repo, "VERIFY.md"), []byte("# invalid\xff\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "VERIFY.md")
	git(t, repo, "commit", "-q", "-m", "reject invalid contract text")
	head := git(t, repo, "rev-parse", "HEAD")
	code, record, stderr := v.runner(repo, "--base", head)
	if code != 2 || asString(record["outcome"]) != "blocked" || !strings.Contains(asString(record["blocked_reason"]), "UTF-8") {
		t.Fatalf("runner %v code=%d stderr=%s, want a blocked invalid UTF-8 contract", record, code, stderr)
	}
}

func TestRunnerRejectsNULContractPath(t *testing.T) {
	v := newVerifyLab(t)
	repo := v.rawRepo("cli", filepath.Join(v.stop, "nul-path"))
	if code, _, stderr := v.scaffold(repo, "--write"); code != 0 {
		t.Fatal(stderr)
	}
	contract := filepath.Join(repo, "VERIFY.md")
	body := strings.Replace(readFile(t, contract), "artifacts = \".artifacts/verification\"", "artifacts = \".artifacts/\\u0000verification\"", 1)
	mustWrite(t, contract, body)
	git(t, repo, "add", "VERIFY.md")
	git(t, repo, "commit", "-q", "-m", "reject NUL contract path")
	head := git(t, repo, "rev-parse", "HEAD")
	code, record, stderr := v.runner(repo, "--base", head)
	if code != 2 || asString(record["outcome"]) != "blocked" || !strings.Contains(asString(record["blocked_reason"]), "artifacts") {
		t.Fatalf("runner %v code=%d stderr=%s, want a blocked NUL path", record, code, stderr)
	}
}

func TestRunnerRejectsLinkedMapSymlink(t *testing.T) {
	v := newVerifyLab(t)
	repo := v.rawRepo("cli", filepath.Join(v.stop, "linked-symlink"))
	if code, _, stderr := v.scaffold(repo, "--write"); code != 0 {
		t.Fatal(stderr)
	}
	mapDir := filepath.Join(repo, "docs/features")
	index := filepath.Join(mapDir, "README.md")
	body := readFile(t, index)
	pattern := regexp.MustCompile(`\]\(([^)\s]+\.md)\)`)
	match := pattern.FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatal("generated feature index has no linked map")
	}
	mustWrite(t, filepath.Join(mapDir, "real.md"), readFile(t, filepath.Join(mapDir, match[1])))
	body = pattern.ReplaceAllString(body, `](linked.md)`)
	mustWrite(t, index, body)
	if err := os.Symlink("real.md", filepath.Join(mapDir, "linked.md")); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "reject linked map symlink")
	head := git(t, repo, "rev-parse", "HEAD")
	code, record, stderr := v.runner(repo, "--base", head)
	if code != 2 || asString(record["outcome"]) != "blocked" || !strings.Contains(asString(record["blocked_reason"]), "symlink") {
		t.Fatalf("runner %v code=%d stderr=%s, want a blocked linked-map symlink", record, code, stderr)
	}
}

func TestRunnerRejectsLinkedMapDirectorySymlink(t *testing.T) {
	v := newVerifyLab(t)
	repo := v.rawRepo("cli", filepath.Join(v.stop, "linked-directory-symlink"))
	if code, _, stderr := v.scaffold(repo, "--write"); code != 0 {
		t.Fatal(stderr)
	}
	features := filepath.Join(repo, "docs/features")
	realFeatures := filepath.Join(repo, "docs/real-features")
	if err := os.MkdirAll(realFeatures, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(features)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		mustWrite(t, filepath.Join(realFeatures, entry.Name()), readFile(t, filepath.Join(features, entry.Name())))
	}
	if err := os.Symlink("real-features", filepath.Join(repo, "docs/linked-features")); err != nil {
		t.Fatal(err)
	}
	contract := filepath.Join(repo, "VERIFY.md")
	body := strings.Replace(readFile(t, contract), "docs/features/README.md", "docs/linked-features/README.md", 1)
	mustWrite(t, contract, body)
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "reject linked feature directory symlink")
	head := git(t, repo, "rev-parse", "HEAD")
	code, record, stderr := v.runner(repo, "--base", head)
	if code != 2 || asString(record["outcome"]) != "blocked" || !strings.Contains(asString(record["blocked_reason"]), "symlink") {
		t.Fatalf("runner %v code=%d stderr=%s, want a blocked linked-directory symlink", record, code, stderr)
	}
}

func TestRunnerRejectsSymlinkedTaskOwner(t *testing.T) {
	v := newVerifyLab(t)
	repo := v.rawRepo("cli", filepath.Join(v.stop, "symlinked-task-owner"))
	if code, _, stderr := v.scaffold(repo, "--write"); code != 0 {
		t.Fatal(stderr)
	}
	if err := os.Symlink("..", filepath.Join(repo, "linked-owner")); err != nil {
		t.Fatal(err)
	}
	contract := filepath.Join(repo, "VERIFY.md")
	body := strings.Replace(readFile(t, contract), "artifacts = \".artifacts/verification\"", "artifacts = \".artifacts/verification\"\ntask_owner = \"linked-owner\"", 1)
	mustWrite(t, contract, body)
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "reject symlinked task owner")
	head := git(t, repo, "rev-parse", "HEAD")
	code, record, stderr := v.runner(repo, "--base", head)
	if code != 2 || asString(record["outcome"]) != "blocked" || !strings.Contains(asString(record["blocked_reason"]), "symlink") {
		t.Fatalf("runner %v code=%d stderr=%s, want a blocked symlinked task owner", record, code, stderr)
	}
}

func TestGenerationKeepsUserEdits(t *testing.T) {
	v := newVerifyLab(t)
	repo := v.rawRepo("service", filepath.Join(v.stop, "custom"))
	mustWrite(t, filepath.Join(repo, "mise.toml"), readFile(t, filepath.Join(repo, "mise.toml"))+"lint = \"python3 -m py_compile app.py\"\n")
	mustWrite(t, filepath.Join(repo, "docs/verification/index.md"), "# Our maps\n\nInventory: incomplete. Custom location, hand written.\n\n- [Health](health.md)\n")
	mustWrite(t, filepath.Join(repo, "docs/verification/health.md"), "# Health\n\nHand written by the team.\n\n| ID | Scenario | Driver | Evidence |\n| --- | --- | --- | --- |\n| `svc.health` | `/health` answers ok | automated: tests/test_app.py | suite |\n")
	mustWrite(t, filepath.Join(repo, "VERIFY.md"), "# Verification contract\n\n```verify\nentrypoint = \"mise run verify\"\nfeature_maps = \"docs/verification/index.md\"\nartifacts = \".artifacts/verification\"\n\n[requires]\ncommands = [\"python3\"]\n```\n\n## Setup\n\nNone.\n\n## Readiness\n\nNone.\n\n## Automated checks\n\n`mise run test`.\n\n## Scenarios\n\nSee `docs/verification/index.md`.\n\n## Isolation\n\nTemp dirs.\n\n## Artifacts\n\n`.artifacts/verification/`.\n\n## Teardown\n\nNone.\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "team's own contract")
	code, written, stderr := v.scaffold(repo, "--write")
	if code != 0 {
		t.Fatal(stderr)
	}
	if asString(asMap(written["plan"])["feature_maps"]) != "docs/verification/index.md" {
		t.Fatalf("maps %v", written["plan"])
	}
	statuses := map[string]string{}
	for _, r := range asSlice(written["results"]) {
		row := asMap(r)
		statuses[asString(row["path"])] = asString(row["status"])
	}
	if statuses["VERIFY.md"] != "kept" {
		t.Fatalf("VERIFY.md %v", statuses)
	}
}

// The runner reports a map row that names visual proof as missing until a comparison for this candidate exists,
// and it reads VERIFY_EVIDENCE_ROOT so a capture written outside a disposable checkout is still found.
func TestRunnerReportsRequiredEvidence(t *testing.T) {
	v := newVerifyLab(t)
	repo := v.rawRepo("cli", filepath.Join(v.stop, "evidence-required"))
	if code, _, stderr := v.scaffold(repo, "--write"); code != 0 {
		t.Fatal(stderr)
	}
	v.fill(repo, "cli")
	relative := seededMap(t, repo)
	feature := strings.TrimSuffix(filepath.Base(relative), ".md")
	scenario := feature + ".banner"
	mapPath := filepath.Join(repo, filepath.FromSlash(relative))
	mustWrite(t, mapPath, addScenarioRow(t, readFile(t, mapPath),
		"| `"+scenario+"` | The banner renders in colour | manual: run it and look | screenshot before/after |"))
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "a row that needs visual proof")
	head := git(t, repo, "rev-parse", "HEAD")

	_, record, stderr := v.runner(repo, "--base", head)
	evidence := asMap(record["evidence"])
	if got := asSlice(evidence["required"]); len(got) != 1 || asString(got[0]) != scenario {
		t.Fatalf("required %v %s", evidence["required"], stderr)
	}
	if got := asSlice(evidence["missing"]); len(got) != 1 || asString(got[0]) != scenario {
		t.Fatalf("missing %v", evidence["missing"])
	}
	detail := asMap(asSlice(evidence["missing_details"])[0])
	if asString(detail["scenario"]) != scenario || asString(detail["feature"]) != feature || asString(detail["map"]) != relative {
		t.Fatalf("missing detail %v", detail)
	}
	if asString(record["outcome"]) != "pass" {
		t.Fatalf("outcome %v: the runner reports missing evidence, it never fails the run for it", record["outcome"])
	}

	stop, err := filepath.EvalSymlinks(v.stop)
	if err != nil {
		t.Fatal(err)
	}
	promoted := filepath.Join(stop, "promoted-evidence")
	writeComparison(t, filepath.Join(promoted, "run-1", scenario, "comparison.json"), scenario, head)
	_, found, stderr := v.runnerWithEnv(repo, append(v.env, "VERIFY_EVIDENCE_ROOT="+promoted), "--base", head)
	evidence = asMap(found["evidence"])
	if len(asSlice(evidence["missing"])) != 0 {
		t.Fatalf("missing after promoting the capture %v %s", evidence["missing"], stderr)
	}
	if asString(evidence["root_source"]) != "VERIFY_EVIDENCE_ROOT" || asString(evidence["root"]) != promoted {
		t.Fatalf("root %v %v", evidence["root_source"], evidence["root"])
	}
}

func (v *verifyLab) runnerWithEnv(repo string, env []string, args ...string) (int, map[string]any, string) {
	script := filepath.Join(repo, ".agents/skills/verify/scripts/verify_run.py")
	code, rec, _, stderr := runPy(v.t, env, script, repo, append([]string{"--json"}, args...)...)
	return code, rec, stderr
}

func writeComparison(t *testing.T, path, scenario, head string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"schema": 1, "scenario": scenario, "verdict": "red-green", "visual_proof": "captured",
		"after": map[string]any{"sha": head}, "candidate": map[string]any{"sha": head},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func seededMap(t *testing.T, repo string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repo, "docs/features"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() && e.Name() != "README.md" && strings.HasSuffix(e.Name(), ".md") {
			return "docs/features/" + e.Name()
		}
	}
	t.Fatal("the scaffold seeded no feature map")
	return ""
}

func addScenarioRow(t *testing.T, body, row string) string {
	t.Helper()
	lines := strings.Split(body, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
			return strings.Join(append(lines[:i+1:i+1], append([]string{row}, lines[i+1:]...)...), "\n")
		}
	}
	t.Fatal("the seeded map carries no scenario table")
	return body
}
