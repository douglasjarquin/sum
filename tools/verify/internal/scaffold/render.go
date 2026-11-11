package scaffold

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/douglasjarquin/sum/tools/verify/internal/contract"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
)

// wrapper is how a project runs its pinned sum-verify binary.
const wrapper = ".agents/skills/verify/scripts/sum-verify"

func quoteAll(values []string, quote string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = quote + value + quote
	}
	return out
}

func fence(p planned, requires []string) string {
	lines := []string{
		`entrypoint = "mise run verify"`, `feature_maps = "` + p.FeatureMaps + `"`, `artifacts = "` + artifacts + `"`, `evidence = ".artifacts/evidence"`, "", "[requires]",
		"commands = [" + strings.Join(quoteAll(requires, `"`), ", ") + "]",
	}
	if p.Freshness != nil {
		lines = append(lines, "", "[freshness]", `inputs = ["`+p.Freshness[0]+`"]`, `outputs = ["`+p.Freshness[1]+`"]`)
	}
	return strings.Join(lines, "\n")
}

func renderVerifyMD(root string, info inspection, p planned) string {
	var policy []string
	for _, name := range []string{"mise.toml", ".mise.toml", "mise-tasks/"} {
		if pycompat.Exists(filepath.Join(root, name)) {
			policy = append(policy, name)
		}
	}
	if policy == nil {
		policy = []string{"mise.toml"}
	}
	// curl is the wrapper's own need: it downloads the pinned binary on first use.
	requires := []string{"git", "mise", "curl"}
	if slices.ContainsFunc(p.Commands, func(c string) bool { return strings.Contains(c, "python") }) ||
		slices.ContainsFunc(info.CLIEntrypoints, func(e string) bool { return strings.HasSuffix(e, ".py") }) {
		requires = append(requires, "python3")
	}
	if slices.ContainsFunc(p.Commands, func(c string) bool { return strings.HasPrefix(c, "npm") || strings.HasPrefix(c, "node") }) {
		requires = append(requires, "node")
	}
	var rows []string
	for _, c := range p.Commands {
		rows = append(rows, "| `"+c+"` | `"+c+"` | "+todo+": state what this check proves | ")
	}
	if rows == nil {
		rows = []string{"| " + todo + ": no existing check was found | - | - |"}
	}
	var setup []string
	for _, g := range info.Guidance {
		if g != contract.File {
			setup = append(setup, "- `"+g+"` describes setup; follow it.")
		}
	}
	if setup == nil {
		setup = []string{todo + ": how a fresh clone is prepared (no setup guidance file was found)."}
	}
	var ready []string
	for _, task := range info.ServeTasks {
		ready = append(ready, "- `mise run "+task+"` starts the service; it is not part of `mise run verify`.")
	}
	for _, endpoint := range info.Readiness.Endpoints {
		ready = append(ready, "- `"+endpoint+"` is a readiness endpoint the repository mentions.")
	}
	ready = append(ready,
		"- `mise tasks ls` lists `verify` with a source inside this repository.",
		"- `"+wrapper+" run --check` validates this contract without running anything.")
	var containers []string
	for _, c := range info.Containers {
		containers = append(containers, "- `"+c+"` is declared; start it only through its own task, task-scoped.")
	}
	return strings.Join([]string{
		"# Verification contract",
		"",
		"This file is the repository-local verification convention for `" + filepath.Base(root) + "`.",
		"It works in an ordinary clone with Git, mise, and the commands listed below; no sum installation, Herdr session, or absolute path outside this checkout is required.",
		"The portable procedure and the runner that records evidence live in `.agents/skills/verify/`; `.agents/skills/maintain-verification/` keeps this contract and the feature maps honest.",
		"A harness without skill discovery follows this file directly.",
		"",
		"```verify",
		fence(p, requires),
		"```",
		"",
		"## Setup",
		"",
		strings.Join(setup, "\n"),
		"",
		"## Readiness",
		"",
		strings.Join(ready, "\n"),
		strings.Join(containers, "\n"),
		"",
		"## Automated checks",
		"",
		"`mise run verify` is the canonical aggregate entrypoint. It runs the checks this repository already declares and stops at the first failure:",
		"",
		"| Check | Command | Proves |",
		"| --- | --- | --- |",
		strings.Join(rows, "\n"),
		"",
		"## Scenarios",
		"",
		"The feature maps linked from `" + p.FeatureMaps + "` describe the user-facing features and which scenarios `mise run verify` exercises.",
		"Rows marked `automated` are covered by the aggregate; rows marked `manual` need a person or an interactive driver and are recorded `not-run` unless reported with `--scenario`.",
		"A green `mise run verify` is evidence for the automated rows only, never for a scenario that was not exercised.",
		"",
		"## Isolation",
		"",
		todo + ": name the temporary directories, ephemeral ports, or lab data this repository's checks use, and what they must never touch.",
		"Never point a check at production data, a personal browser profile, or an application instance you did not start.",
		"",
		"## Artifacts",
		"",
		"Each run writes `" + artifacts + "/<run-id>/run.json` and `verify.log`; `" + artifacts + "/latest.json` mirrors the newest record.",
		"Evidence captured with `" + wrapper + " capture` lands under the same run directory and survives cleanup.",
		"Before/after proof of one scenario (`" + wrapper + " evidence`) lands under `.artifacts/evidence/<run-id>/<scenario>/` with a comparison manifest; its `capabilities` command says whether this machine can drive a browser.",
		"The directory is Git-ignored; reference records by path in reports.",
		"",
		"## Teardown",
		"",
		todo + ": how anything a check started is stopped. Stop only what the run started; never kill by process name.",
		"",
		"## Policy",
		"",
		"Edits to this file, " + strings.Join(quoteAll(policy, "`"), ", ") + ", the feature maps, or the skills under `.agents/skills/verify/` and `.agents/skills/evidence/` are policy changes.",
		"Run the runner with `--base <merge-base>` so such a candidate reports `needs-root-review`; it cannot certify its own new standard.",
		"",
	}, "\n")
}

// driverText names a driver only when its capability is installed; otherwise the row is explicitly manual.
func driverText(info inspection, f feature) string {
	automated := "automated: " + todo + ": name the test that exercises it"
	switch f.Surface {
	case "service":
		if len(info.Tests) > 0 {
			return automated
		}
		if info.Capabilities.Get("curl") == true {
			return "manual: `curl` against a service you started"
		}
		return "manual: no HTTP driver installed"
	case "cli":
		if len(info.Tests) > 0 {
			return automated
		}
		return "manual: run the command in a terminal"
	}
	for _, browser := range browserDrivers {
		if info.Capabilities.Get(browser) == true {
			return "manual: browser via `" + browser + "`"
		}
	}
	return "manual: no browser driver installed; open the page by hand"
}

func renderFeature(info inspection, f feature) string {
	var drive string
	switch f.Surface {
	case "service":
		serve := todo + ": no serve task found"
		if len(info.ServeTasks) > 0 {
			serve = strings.Join(quoteAll(prefixAll("mise run ", info.ServeTasks), "`"), ", ")
		}
		drive = fmt.Sprintf("Start the service with its own task (%s), then request `%s` on the URL it prints. Capture with `%s capture --feature %s http GET <url>%s`.", serve, f.Entry, wrapper, f.ID, f.Entry)
	case "cli":
		command, _, _ := strings.Cut(f.Entry, " ")
		drive = fmt.Sprintf("Run `%s` with a real argument. Capture with `%s capture --feature %s run -- %s <args>`.", f.Entry, wrapper, f.ID, command)
	case "web":
		drive = "Serve the built page and open it. " + todo + ": name the exact URL and what the page must show."
	}
	return strings.Join([]string{
		"# " + f.Title,
		"",
		todo + ": one paragraph on what this feature does for the user and why it matters. Seeded from `" + f.Entry + "` found in the repository.",
		"",
		"## Entry points",
		"",
		"- `" + f.Entry + "`",
		"- " + todo + ": every other way a user reaches this feature (UI control, keyboard, CLI flag, API route), or state that this is the only one.",
		"",
		"## Scenarios",
		"",
		"| ID | Scenario | Driver | Evidence |",
		"| --- | --- | --- | --- |",
		"| `" + f.ID + "` | " + todo + ": the success path a user sees | " + driverText(info, f) + " | run record |",
		"",
		"Add one row per variant and per applicable case: error, cancel, empty, persistence. Mark a case not applicable in prose rather than silently omitting it.",
		"",
		"## Driving it",
		"",
		drive,
		"",
		"## Expected states and side effects",
		"",
		"- " + todo + ": what is observable when it works (output, status code, rendered text, file written, row inserted).",
		"- " + todo + ": what the error path shows.",
		"",
		"## Gotchas and manual gaps",
		"",
		"- " + todo + ": traps that waste or invalidate a run, and anything only a person can check.",
		"",
	}, "\n")
}

func prefixAll(prefix string, values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = prefix + value
	}
	return out
}

func renderIndex(features []feature, info inspection) string {
	lines := []string{
		"# Feature maps", "",
		"Each file maps one user-facing feature: its entry points, scenarios with stable IDs, how to drive it, the observable states that prove it, and the manual gaps.",
		"The runner in `.agents/skills/verify/` reads the scenario tables from the files linked here; a scenario ID must be unique across maps.",
		"A row's Driver column starts with `automated` (covered by `mise run verify`) or `manual`; anything after that names the test or the procedure.", "",
		fmt.Sprintf("Inventory: **incomplete**. %d feature(s) seeded from the repository inspection on %s; the rest of the product is not mapped yet.", len(features), tool.UTCNow()[:10]),
		"Do not read this index as full coverage until the incomplete label is removed by a person who inventoried the product.", "",
	}
	mapped := map[string]bool{}
	for _, f := range features {
		lines = append(lines, "- ["+f.Title+"]("+f.ID+".md)")
		mapped[f.Entry] = true
	}
	var unmapped []string
	for _, entry := range slices.Concat(info.Routes, info.CLIEntrypoints) {
		if !mapped[entry] {
			unmapped = append(unmapped, "- `"+entry+"`")
		}
	}
	if unmapped != nil {
		lines = append(lines, "", "Found but not mapped yet:")
		lines = append(lines, unmapped...)
	}
	return strings.Join(lines, "\n") + "\n"
}
