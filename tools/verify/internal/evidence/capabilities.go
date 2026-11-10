package evidence

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/tools/verify/internal/proc"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
	"github.com/douglasjarquin/sum/tools/verify/internal/value"
)

var (
	browserNames = []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome", "microsoft-edge", "brave-browser"}
	browserMac   = []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge", "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
	}
)

// findBrowser returns "" when no Chromium-family browser is available. EVIDENCE_BROWSER, when set, is the only candidate.
func findBrowser() string {
	if explicit := os.Getenv("EVIDENCE_BROWSER"); explicit != "" {
		if info, err := os.Stat(explicit); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return explicit
		}
		return ""
	}
	for _, name := range browserNames {
		if found, err := exec.LookPath(name); err == nil {
			return found
		}
	}
	for _, candidate := range browserMac {
		if pycompat.IsFile(candidate) {
			return candidate
		}
	}
	return ""
}

// nodeVersion returns the node binary on PATH and what it reports as its version; without node the path is "" and the version null.
func nodeVersion() (node string, version any) {
	node, err := exec.LookPath("node")
	if err != nil {
		return "", nil
	}
	result, _ := proc.Run([]string{node, "--version"}, proc.Options{})
	return node, strings.TrimSpace(result.Stdout)
}

// ffmpegBinary only counts ffmpeg when it actually runs here; a version-manager shim without an active version is not a converter.
func ffmpegBinary() string {
	found, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ""
	}
	if result, err := proc.Run([]string{found, "-version"}, proc.Options{Timeout: 10 * time.Second}); err != nil || result.Code != 0 {
		return ""
	}
	return found
}

// orNull records an absent string as null.
func orNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func capabilities() *value.Object {
	_, version := nodeVersion()
	major := 0
	if text := value.Str(version); strings.HasPrefix(text, "v") {
		major, _ = strconv.Atoi(strings.Split(text[1:], ".")[0])
	}
	browser := findBrowser()
	ffmpeg := ffmpegBinary()
	supported := browser != "" && major >= 22
	var reason any
	switch {
	case browser == "":
		reason = "no Chromium-family browser found; set EVIDENCE_BROWSER"
	case !supported:
		reason = "node 22+ with global WebSocket required, found " + value.Text(version)
	}
	return value.NewObject(
		"browser", value.NewObject("supported", supported, "binary", orNull(browser), "node", version, "reason", reason),
		"cli", value.NewObject("supported", true, "reason", nil),
		"http", value.NewObject("supported", true, "reason", nil),
		"desktop", value.NewObject("supported", false, "reason", "no desktop automation driver is shipped; report such scenarios manually"),
		"video", value.NewObject("native", "mjpeg-avi (original JPEG frames in an AVI container)", "ffmpeg", orNull(ffmpeg), "convert", ffmpeg != ""),
	)
}

// capabilitiesText is one line per recipe: supported, the reason it is not, or the whole entry when it has neither.
func capabilitiesText(info *value.Object) string {
	lines := make([]string, 0, info.Len())
	for _, key := range info.Keys() {
		entry := value.Obj(info.Get(key))
		shown := "supported"
		if !value.Truthy(entry.Get("supported")) {
			if entry.Has("reason") {
				shown = value.Text(entry.Get("reason"))
			} else {
				shown = value.Text(entry)
			}
		}
		lines = append(lines, key+": "+shown)
	}
	return strings.Join(lines, "\n")
}
