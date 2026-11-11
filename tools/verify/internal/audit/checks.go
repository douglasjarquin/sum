package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/douglasjarquin/sum/tools/verify/internal/contract"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
)

const todo = "TODO(verify)"

type scenario struct {
	ID         string `json:"id"`
	Map        string `json:"map"`
	Driver     string `json:"driver"`
	DriverText string `json:"driver_text"`
	Line       int    `json:"line"`
}

// ownTasks is the set of mise tasks defined inside this repository. known is false when mise is absent or
// does not answer in JSON, and then no task claim can be checked.
func ownTasks(root string) (names map[string]bool, known bool) {
	mise := contract.ListMiseTasks(root)
	if mise.Binary == "" {
		return nil, false
	}
	var rows []map[string]any
	if len(mise.Stdout) > 0 && json.Unmarshal(mise.Stdout, &rows) != nil {
		return nil, false
	}
	names = map[string]bool{}
	for _, row := range rows {
		source, _ := row["source"].(string)
		if source == "" {
			source, _ = row["file"].(string)
		}
		name, named := row["name"].(string)
		if named && source != "" && pycompat.Within(pycompat.Resolve(source), root) {
			names[name] = true
		}
	}
	return names, true
}

// checkText runs the checks every contract and map gets: placeholders, stale paths, and stale tasks.
func (r *record) checkText(root string, doc *document, tasks map[string]bool, known bool) {
	for number, line := range pycompat.SplitLines(doc.text) {
		if strings.Contains(line, todo) {
			r.add(finding{Kind: "placeholder", File: doc.relative, Line: number + 1, Detail: pycompat.Clip(pycompat.Strip(line), 160)})
		}
	}
	r.checkPaths(root, doc)
	if !known {
		return
	}
	names := taskRefs(doc.text)
	slices.Sort(names)
	for _, name := range slices.Compact(names) {
		if !tasks[name] {
			r.add(finding{Kind: "stale-task", File: doc.relative, Detail: fmt.Sprintf("`mise run %s` is mentioned but this repository defines no `%s` task", name, name)})
		}
	}
}

func (r *record) checkPaths(root string, doc *document) {
	for _, word := range dedupe(pathTokens(doc.text)) {
		if existing(root, doc, word) == "" && !gitIgnored(root, word) {
			r.add(finding{Kind: "stale-path", File: doc.relative, Detail: fmt.Sprintf("`%s` does not exist in the repository", word)})
		}
	}
}

// existing resolves a path a document mentions against the repository root, then the document's own directory.
// It is empty when neither exists.
func existing(root string, doc *document, word string) string {
	for _, base := range []string{root, doc.dir()} {
		if path := pycompat.Join(base, word); pycompat.Exists(path) {
			return path
		}
	}
	return ""
}

// gitIgnored reports whether the repository deliberately ignores the path: runtime paths (state, run records)
// are not stale when absent.
func gitIgnored(root, word string) bool {
	for _, path := range []string{word, strings.TrimRight(word, "/") + "/"} {
		if _, _, ignored := contract.Git(root, "check-ignore", "-q", "--no-index", path); ignored {
			return true
		}
	}
	return false
}

// referencesOf lists the repository paths a map or contract mentions, resolved relative to the repository root
// or to the file itself.
func referencesOf(root string, doc *document) []string {
	found := []string{}
	for _, word := range append(pathTokens(doc.text), contract.Links(doc.text)...) {
		for _, base := range []string{root, doc.dir()} {
			candidate := pycompat.Resolve(pycompat.Join(base, word))
			if !pycompat.Exists(candidate) {
				continue
			}
			if relative, ok := pycompat.Rel(candidate, root); ok {
				found = append(found, strings.TrimRight(relative, "/"))
			}
			break
		}
	}
	slices.Sort(found)
	return slices.Compact(found)
}

// scenariosOf lists a map's scenario rows and reports the rows whose driver or coverage claim does not hold.
func (r *record) scenariosOf(root string, doc *document, seen map[string]string) []scenario {
	var rows []scenario
	for _, row := range contract.Rows(doc.text) {
		automated, ok := contract.Automated(row.Driver)
		if !ok {
			r.add(finding{Kind: "bad-driver", File: doc.relative, Line: row.Line, Detail: fmt.Sprintf("%s: driver %s must start with `automated` or `manual`", row.ID, pycompat.Repr(row.Driver))})
			continue
		}
		if first, defined := seen[row.ID]; defined {
			r.add(finding{Kind: "duplicate-id", File: doc.relative, Line: row.Line, Detail: fmt.Sprintf("%s is also defined in %s", row.ID, first)})
		}
		seen[row.ID] = doc.relative
		driver := "manual"
		if automated {
			driver = "automated"
		}
		if automated && !strings.Contains(row.Driver, todo) { // A placeholder row is already reported as `placeholder`.
			r.checkCoverage(root, doc, row)
		}
		rows = append(rows, scenario{ID: row.ID, Map: doc.relative, Driver: driver, DriverText: pycompat.Clip(row.Driver, 200), Line: row.Line})
	}
	return rows
}

// checkCoverage reports an automated row that names no test or command path, or one that is missing or holds no test.
func (r *record) checkCoverage(root string, doc *document, row contract.Row) {
	claim := func(format string, args ...any) {
		r.add(finding{Kind: "coverage-claim", File: doc.relative, Line: row.Line, Detail: row.ID + " " + fmt.Sprintf(format, args...)})
	}
	named := append(pathTokens(row.Driver), coveragePaths(row.Driver)...)
	if len(named) == 0 {
		claim("is `automated` but names no test or command path that covers it")
	}
	for _, word := range dedupe(named) {
		target := existing(root, doc, word)
		switch {
		case target == "":
			claim("claims coverage by `%s`, which does not exist", word)
		case pycompat.IsFile(target) && isPythonFile(target) && !holdsTest(target):
			claim("claims coverage by `%s`, which contains no test or assertion", word)
		}
	}
}

func isPythonFile(path string) bool {
	name := path[strings.LastIndex(path, "/")+1:]
	return strings.HasSuffix(name, ".py") && name != ".py"
}

func holdsTest(path string) bool {
	data, err := os.ReadFile(path)
	return err != nil || bytes.Contains(data, []byte("def test")) || bytes.Contains(data, []byte("unittest")) || bytes.Contains(data, []byte("assert"))
}
