package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/graph"
	"github.com/douglasjarquin/sum/go/internal/herdrclient"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/release"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

const herdrVersionPin = "0.9.0"

var toolNames = []string{"python3", "node", "git", "gh", "herdr", "quota-axi", "remainder", "lsof"}

var harnessExecutables = []struct {
	Kind string
	Exe  string
}{
	{"codex", "codex"},
	{"claude", "claude"},
	{"grok", "grok"},
	{"cursor", "cursor-agent"},
	{"pi", "pi"},
	{"opencode", "opencode"},
	{"gemini", "gemini"},
	{"omp", "omp"},
	{"copilot", "copilot"},
}

func Doctor(runtimeRoot, installRoot string, s *store.Store) *ordjson.Object {
	rows := make([]any, 0, 12)

	for _, name := range toolNames {
		row := ordjson.NewObject()
		row.Set("tool", name)
		if path, err := toolpath.Find(runtimeRoot, name); err == nil {
			row.Set("path", path)
			row.Set("ok", true)
		} else {
			row.Set("ok", false)
			row.Set("detail", err.Error())
		}
		rows = append(rows, row)
	}

	herdrVersionRow := ordjson.NewObject()
	herdrVersionRow.Set("tool", "herdr-version")
	herdrPath, herdrPathErr := toolpath.Find(runtimeRoot, "herdr")
	if herdrPathErr == nil {
		if found, err := herdrclient.EnsureVersion(herdrPath, herdrVersionPin); err == nil {
			herdrVersionRow.Set("ok", true)
			herdrVersionRow.Set("detail", found)
		} else {
			herdrVersionRow.Set("ok", false)
			herdrVersionRow.Set("detail", err.Error())
		}
	} else {
		herdrVersionRow.Set("ok", false)
		herdrVersionRow.Set("detail", herdrPathErr.Error())
	}
	rows = append(rows, herdrVersionRow)

	rows = append(rows, ghAttachCheck(runtimeRoot))

	rows = append(rows, runtimeRevisionCheck(runtimeRoot, installRoot))

	role := ordjson.NewObject()
	role.Set("tool", "role")
	role.Set("ok", true)
	designated := s.Designated()
	role.Set("installation", designated)
	if designated {
		owner, _ := s.Owner()
		if owner != nil {
			role.Set("coordinator", owner)
		} else {
			role.Set("coordinator", nil)
		}
	} else {
		role.Set("coordinator", nil)
	}

	herdrContextRow := ordjson.NewObject()
	herdrContextRow.Set("tool", "herdr-context")
	ctx, ctxErr := store.Context(runtimeRoot)
	if ctxErr == nil {
		if herdrPathErr == nil {
			pane, _ := ctx.Get("pane")
			session, _ := ctx.Get("session")
			paneStr, _ := pane.(string)
			sessionStr, _ := session.(string)
			_, callErr := herdrclient.Call(herdrPath, sessionStr, 10*time.Second, "pane", "get", paneStr)
			if callErr == nil {
				herdrContextRow.Set("ok", true)
				herdrContextRow.Set("detail", ctx)
				var registration *ordjson.Object
				if designated {
					registration, _ = s.Registration(store.EndpointFromContext(ctx))
				}
				if registration != nil {
					roleValue, _ := registration.Get("role")
					roleStr, _ := roleValue.(string)
					role.Set("registered", roleStr)
					role.Set("detail", "Registered as "+roleStr+".")
				} else {
					role.Set("registered", nil)
					role.Set("detail", "This pane is not registered. Run ./bin/sumctl init to register explicitly; doctor never binds.")
				}
			} else {
				herdrContextRow.Set("ok", false)
				herdrContextRow.Set("detail", callErr.Error())
			}
		} else {
			herdrContextRow.Set("ok", false)
			herdrContextRow.Set("detail", herdrPathErr.Error())
		}
	} else {
		herdrContextRow.Set("ok", false)
		herdrContextRow.Set("detail", ctxErr.Error())
	}
	rows = append(rows, herdrContextRow)
	rows = append(rows, role)

	installed := ordjson.NewObject()
	anyInstalled := false
	for _, h := range harnessExecutables {
		if path := findExecutable(runtimeRoot, h.Exe); path != "" {
			installed.Set(h.Kind, path)
			anyInstalled = true
		}
	}
	harnessRow := ordjson.NewObject()
	harnessRow.Set("tool", "harness")
	harnessRow.Set("ok", anyInstalled)
	harnessRow.Set("installed", installed)
	rows = append(rows, harnessRow)

	meshRow := ordjson.NewObject()
	meshRow.Set("tool", "mesh")
	meshRow.Set("ok", isExecutable(filepath.Join(runtimeRoot, ".local", "bin", "herdr-mesh")) ||
		isExecutable(filepath.Join(runtimeRoot, ".local", "bin", "herdr-mesh-go")))
	rows = append(rows, meshRow)

	graphTool := graph.Tool(runtimeRoot)
	available, _ := graphTool.Get("available")
	pinned, _ := graphTool.Get("pinned")
	version, _ := graphTool.Get("version")
	path, _ := graphTool.Get("path")
	reason, _ := graphTool.Get("reason")
	codegraphRow := ordjson.NewObject()
	codegraphRow.Set("tool", "codegraph")
	codegraphRow.Set("ok", true)
	codegraphRow.Set("available", available)
	codegraphRow.Set("pinned", pinned)
	codegraphRow.Set("version", version)
	codegraphRow.Set("path", path)
	if available == true {
		codegraphRow.Set("detail", "pinned codegraph available; new checkouts get a local index")
	} else {
		reasonStr, _ := reason.(string)
		codegraphRow.Set("detail", "graph optional and unavailable: "+reasonStr)
	}
	rows = append(rows, codegraphRow)

	allOK := true
	for _, r := range rows {
		obj := r.(*ordjson.Object)
		if ok, _ := obj.Get("ok"); ok != true {
			allOK = false
			break
		}
	}

	result := ordjson.NewObject()
	result.Set("version", contract.SumVersion)
	result.Set("home", s.Home)
	result.Set("runtime", runtimeRoot)
	if installRoot == "" {
		installRoot = runtimeRoot
	}
	result.Set("installation", installRoot)
	result.Set("checks", rows)
	result.Set("ok", allOK)
	result.Set("note", "Observation only: nothing was bound or written. No auth changes or permission bypasses. Authenticate the chosen harness and gh separately.")
	return result
}

func ghAttachCheck(runtimeRoot string) *ordjson.Object {
	row := ordjson.NewObject()
	row.Set("tool", "gh-attach")
	ghPath, err := toolpath.Find(runtimeRoot, "gh")
	if err != nil {
		row.Set("ok", true)
		row.Set("supported", false)
		row.Set("detail", err.Error())
		return row
	}
	out, _ := exec.Command(ghPath, "pr", "edit", "--help").Output()
	attach := strings.Contains(string(out), "--attach")
	row.Set("ok", true)
	row.Set("supported", attach)
	if attach {
		row.Set("detail", "gh pr edit --attach available; `pr evidence` can publish")
	} else {
		row.Set("detail", "this runtime's gh has no --attach (GitHub CLI 2.99+); evidence publication defers until a release with the current pin is active")
	}
	return row
}

func findExecutable(runtimeRoot, exe string) string {
	if path, err := exec.LookPath(exe); err == nil {
		return path
	}
	local := filepath.Join(runtimeRoot, ".local", "bin", exe)
	if isFile(local) {
		return local
	}
	return ""
}

func isFile(path string) bool {
	if info, err := os.Stat(path); err == nil {
		return !info.IsDir()
	}
	return false
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// runtimeRevisionCheck reports which revision the active runtime was staged from
// and how far the installation checkout has moved past it. bin/sumctl is always
// the checkout's file while the binary it execs belongs to the staged release,
// so the two can drift apart silently; this row is the only place that says so.
// A runtime that is an ancestor of the checkout is the ordinary "pulled but not
// updated yet" state and stays ok. A runtime that cannot be identified, or that
// is not on the checkout's history at all, is not.
func runtimeRevisionCheck(runtimeRoot, installRoot string) *ordjson.Object {
	row := ordjson.NewObject()
	row.Set("tool", "runtime-revision")
	// Claim the key order every branch below reports in; Set keeps a key's
	// position when it is overwritten, and an unreached branch stays not-ok.
	row.Set("ok", false)
	if installRoot == "" {
		installRoot = runtimeRoot
	}
	if sameDirectory(runtimeRoot, installRoot) {
		row.Set("ok", true)
		row.Set("detail", "no staged release is active; the runtime is the checkout itself")
		return row
	}

	runtimeSHA, err := stagedSourceSHA(runtimeRoot)
	if err != nil {
		row.Set("ok", false)
		row.Set("detail", "cannot identify the active runtime's revision: "+err.Error())
		return row
	}
	row.Set("runtime_sha", runtimeSHA)

	checkoutSHA, headErr := gitOutput(installRoot, "rev-parse", "HEAD")
	if headErr != nil {
		row.Set("ok", true)
		row.Set("detail", "the installation is not a readable Git checkout; revision drift cannot be compared")
		return row
	}
	row.Set("checkout_sha", checkoutSHA)

	if runtimeSHA == checkoutSHA {
		row.Set("behind", 0)
		row.Set("ok", true)
		row.Set("detail", "the active runtime was staged from the checkout's current HEAD")
		return row
	}

	if _, ancestorErr := gitOutput(installRoot, "merge-base", "--is-ancestor", runtimeSHA, checkoutSHA); ancestorErr != nil {
		row.Set("ok", false)
		row.Set("detail", "the active runtime was staged from "+shortSHA(runtimeSHA)+", which is not on this checkout's history; stage and activate a release from HEAD")
		return row
	}

	count, countErr := gitOutput(installRoot, "rev-list", "--count", runtimeSHA+".."+checkoutSHA)
	behind, convErr := strconv.Atoi(count)
	if countErr != nil || convErr != nil {
		// The revisions differ and one is an ancestor of the other, so never
		// report a distance of zero just because counting them failed.
		row.Set("behind", nil)
		row.Set("ok", true)
		row.Set("detail", "the active runtime was staged from "+shortSHA(runtimeSHA)+", an unknown distance behind the checkout; `update` activates a release from HEAD")
		return row
	}
	row.Set("behind", behind)
	row.Set("ok", true)
	row.Set("detail", fmt.Sprintf("the active runtime was staged from %s, %d commit(s) behind the checkout; `update` activates a release from HEAD", shortSHA(runtimeSHA), behind))
	return row
}

func stagedSourceSHA(runtimeRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(runtimeRoot, release.Manifest))
	if err != nil {
		return "", err
	}
	var manifest struct {
		Source struct {
			SHA string `json:"sha"`
		} `json:"source"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", err
	}
	if manifest.Source.SHA == "" {
		return "", fmt.Errorf("%s has no source.sha", release.Manifest)
	}
	return manifest.Source.SHA, nil
}

func gitOutput(root string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func sameDirectory(a, b string) bool {
	return resolveDirectory(a) == resolveDirectory(b)
}

func resolveDirectory(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
