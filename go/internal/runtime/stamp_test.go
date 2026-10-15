package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/contract"
)

const stampTestSHA = "0123456789abcdef0123456789abcdef01234567"

func TestSourceSHA_releaseManifestWins(t *testing.T) {
	root := t.TempDir()
	manifest := `{"schema": 1, "source": {"sha": "` + stampTestSHA + `"}}`
	if err := os.WriteFile(filepath.Join(root, "release.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := SourceSHA(root); got != stampTestSHA {
		t.Fatalf("SourceSHA = %q, want the manifest source sha %q", got, stampTestSHA)
	}
	if got := Identity(root); got != contract.SumVersion+"@"+stampTestSHA {
		t.Fatalf("Identity = %q", got)
	}
	if got := Marker(root); got != "<!-- sum:"+contract.SumVersion+"@"+stampTestSHA+" -->" {
		t.Fatalf("Marker = %q", got)
	}
}

func TestSourceSHA_developmentCheckoutReadsHEAD(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "--quiet")
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "README")
	git("commit", "--quiet", "-m", "init")
	head := git("rev-parse", "HEAD")
	if got := SourceSHA(root); got != head {
		t.Fatalf("SourceSHA = %q, want HEAD %q", got, head)
	}
}

func TestMarker_noManifestNoGitIsTheBareVersion(t *testing.T) {
	root := t.TempDir()
	if got := SourceSHA(root); got != "" {
		t.Fatalf("SourceSHA = %q, want empty", got)
	}
	if got := Marker(root); got != "<!-- sum:"+contract.SumVersion+" -->" {
		t.Fatalf("Marker = %q, want the bare version stamp", got)
	}
}
