package proc

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// directExec lists every non-test source outside this runner that still starts a process itself, with the
// number of call sites and why it keeps its own lifecycle. Owned helper commands go through RunContext; a new
// direct call fails this test until it is migrated or listed here with a reason.
var directExec = map[string]struct {
	sites  int
	reason string
}{
	"internal/proc/proc.go":               {1, "the bounded runner itself"},
	"internal/verifycmd/execute.go":       {1, "project verification: records the runner pid while it runs, bounded by the contract timeout, output parsed from its own record"},
	"internal/pipeline/lint.go":           {1, "project lint runs candidate code under the contract timeout and tees its transcript to lint.log"},
	"internal/pipeline/document.go":       {2, "VERIFY.md audit and its detached-checkout git operations under the pipeline's own bounds; follow-up to move the git calls"},
	"internal/pipeline/ci.go":             {2, "gh check reads under the pipeline bound; follow-up migration"},
	"internal/pipeline/prcreate.go":       {1, "gh pr create is a mutation under the pipeline bound; follow-up migration"},
	"internal/pipeline/publish.go":        {2, "gh PR body read and edit under the pipeline bound; follow-up migration"},
	"internal/prcmd/publish.go":           {2, "evidence publisher uploads media under 5 and 30 minute bounds and reports through result files"},
	"internal/release/stage.go":           {2, "git archive piped into tar needs stdin, which the helper runner does not take"},
	"internal/project/project.go":         {2, "project enrollment git, including a network clone that needs its own bound; follow-up migration"},
	"internal/devcmd/dev.go":              {3, "development checkout git worktree operations; follow-up migration"},
	"internal/doctor/doctor.go":           {2, "doctor observations of gh and git; follow-up migration"},
	"internal/lsp/lsp.go":                 {1, "mise install and which under the LSP install bounds; follow-up migration"},
	"internal/machine/machine.go":         {1, "macOS ioreg read with its own 5 second bound"},
	"internal/verifycontract/archive.go":  {3, "git reads with an existing output bound; follow-up migration"},
	"internal/verifycontract/dispatch.go": {1, "git rev-parse; follow-up migration"},
	"internal/skilltest/lab.go":           {3, "test-only lab helpers that run under testing.T"},
}

var execCall = regexp.MustCompile(`exec\.Command(Context)?\(`)

func TestDirectExecInventory(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]int{}
	for _, dir := range []string{"internal", "cmd"} {
		walkErr := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if n := len(execCall.FindAll(data, -1)); n > 0 {
				rel, _ := filepath.Rel(root, path)
				found[filepath.ToSlash(rel)] = n
			}
			return nil
		})
		if walkErr != nil {
			t.Fatal(walkErr)
		}
	}
	var problems []string
	for file, n := range found {
		entry, ok := directExec[file]
		switch {
		case !ok:
			problems = append(problems, file+": starts processes directly; run it through proc.RunContext or list it with a reason")
		case entry.sites != n:
			problems = append(problems, file+": listed with a different number of direct call sites than it has")
		}
	}
	for file := range directExec {
		if _, ok := found[file]; !ok {
			problems = append(problems, file+": listed but no longer starts processes directly; remove the entry")
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}
