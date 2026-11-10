package contract

import (
	"regexp"
	"slices"
	"strings"

	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
)

var (
	link       = regexp.MustCompile(`\]\(([^)` + pycompat.SpaceClass + `]+\.md)\)`)
	row        = regexp.MustCompile("^\\|[" + pycompat.SpaceClass + "]*`?([A-Za-z0-9][A-Za-z0-9._:/-]{0,79})`?[" + pycompat.SpaceClass + "]*\\|(.*)\\|[" + pycompat.SpaceClass + "]*$")
	driverKind = regexp.MustCompile(`(?i)^(?:automated|manual)`)
)

// Links returns the targets of a document's Markdown links to `.md` files, in order.
func Links(text string) []string {
	var targets []string
	for _, match := range link.FindAllStringSubmatch(text, -1) {
		targets = append(targets, match[1])
	}
	return targets
}

// Row is one scenario row of a feature map: `| id | description | ... | driver | evidence |`.
type Row struct {
	Line        int // 1-based line in the map.
	ID          string
	Description string
	Driver      string
	Evidence    string
}

// Rows returns the scenario rows of every table in a map that declares a Driver column.
func Rows(text string) []Row {
	var rows []Row
	driverColumn, declared := 0, false
	for number, line := range pycompat.SplitLines(text) {
		if !strings.HasPrefix(strings.TrimLeftFunc(line, pycompat.IsSpace), "|") {
			declared = false // A table ended; the next one declares its own columns.
			continue
		}
		if !declared {
			header := strings.Split(strings.Trim(pycompat.Strip(line), "|"), "|")
			for i, cell := range header {
				header[i] = pycompat.Lower(pycompat.Strip(cell))
			}
			if at := slices.Index(header, "driver"); at >= 0 {
				driverColumn, declared = at-1, true // Cells after the id column.
				continue
			}
		}
		match := row.FindStringSubmatch(line)
		if match == nil || !declared {
			continue
		}
		cells := strings.Split(match[2], "|")
		for i, cell := range cells {
			cells[i] = pycompat.Strip(cell)
		}
		if driverColumn >= len(cells) {
			continue // A short row.
		}
		// A Driver header in the id column counts from the end.
		driver := cells[(driverColumn+len(cells))%len(cells)]
		if strings.Trim(driver, "-:") == "" {
			continue // The `| --- |` separator.
		}
		found := Row{Line: number + 1, ID: match[1], Description: cells[0], Driver: driver}
		if driverColumn+1 < len(cells) {
			found.Evidence = cells[driverColumn+1]
		}
		rows = append(rows, found)
	}
	return rows
}

// Automated reports whether a Driver cell starts with the word `automated`.
// ok is false when it starts with neither `automated` nor `manual`.
func Automated(driver string) (automated, ok bool) {
	end := driverKind.FindStringIndex(driver)
	if end == nil || pycompat.WordAfter(driver, end[1]) {
		return false, false
	}
	return strings.HasPrefix(pycompat.Lower(driver), "automated"), true
}
