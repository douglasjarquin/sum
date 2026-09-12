package release

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/skills"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func Stage(s *store.Store, ref string) (*ordjson.Object, error) {
	root, err := InstallationRoot(s)
	if err != nil {
		return nil, err
	}
	if ref == "" {
		ref = "HEAD"
	}
	shaOut, err := proc.Run([]string{"git", "-C", root, "rev-parse", "--verify", ref + "^{commit}", "--"}, "", 30*time.Second, true, nil)
	if err != nil {
		return nil, err
	}
	sha := strings.TrimSpace(shaOut.Stdout)
	releases := filepath.Join(root, ".local", "releases")
	if err := os.MkdirAll(releases, 0o700); err != nil {
		return nil, err
	}
	final := filepath.Join(releases, sha)
	if info, statErr := os.Stat(final); statErr == nil && info.IsDir() {
		manifest, verErr := VerifyRelease(final, sha)
		if verErr != nil {
			return nil, verErr
		}
		return ReleaseSummary(final, manifest, false)
	}
	staging, err := os.MkdirTemp(releases, ".staging-"+sha[:12]+"-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	cmd := exec.Command("git", "-C", root, "archive", "--format=tar", sha)
	archiveOut, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git archive: %s", err)
	}
	tar := exec.Command("tar", "-xf", "-", "-C", staging)
	tar.Stdin = strings.NewReader(string(archiveOut))
	if err := tar.Run(); err != nil {
		return nil, fmt.Errorf("extract archive: %s", err)
	}
	inventory, err := skills.Check(staging)
	if err != nil {
		return nil, err
	}
	if ok, _ := inventory.Get("ok"); ok != true {
		errors, _ := inventory.Get("errors")
		return nil, fmt.Errorf("Skill inventory refused release staging: %v", errors)
	}
	if _, err := os.Stat(filepath.Join(staging, ".sum")); err == nil {
		return nil, fmt.Errorf("The committed tree must not contain .sum or a release manifest.")
	}
	if err := installRuntime(staging); err != nil {
		return nil, fmt.Errorf("Staging %s failed and its partial bundle was removed; existing releases and the current setup are unchanged. %s", sha, err)
	}
	if err := os.Rename(staging, final); err != nil {
		if _, statErr := os.Stat(final); statErr == nil {
			manifest, verErr := VerifyRelease(final, sha)
			if verErr != nil {
				return nil, verErr
			}
			return ReleaseSummary(final, manifest, false)
		}
		return nil, err
	}
	manifest, err := VerifyRelease(final, sha)
	if err != nil {
		return nil, err
	}
	return ReleaseSummary(final, manifest, true)
}

func installRuntime(target string) error {
	if _, err := proc.Run([]string{"mise", "install"}, target, 900*time.Second, true, nil); err != nil {
		return err
	}
	goBin := os.Getenv("SUM_GO_BIN")
	if goBin == "" {
		which, err := proc.Run([]string{"mise", "which", "go"}, target, 60*time.Second, false, nil)
		if err == nil && strings.TrimSpace(which.Stdout) != "" {
			goBin = strings.TrimSpace(which.Stdout)
		}
	}
	if goBin == "" {
		if found, err := exec.LookPath("go"); err == nil {
			goBin = found
		}
	}
	if goBin == "" {
		return fmt.Errorf("Missing Go 1.25+; install the pinned build tool with mise before staging native artifacts.")
	}
	outDir := filepath.Join(target, ".local", "bin")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	env := append(os.Environ(), "CGO_ENABLED=0", "GOENV=off")
	outputs := map[string]string{"sumctl": "./cmd/sumctl", "sumctl-go": "./cmd/sumctl-go", "herdr-mesh": "./cmd/herdr-mesh"}
	for name, pkg := range outputs {
		dest := filepath.Join(outDir, name)
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(target, "go", strings.TrimPrefix(pkg, "./"))); err != nil {
			continue
		}
		if _, err := proc.Run([]string{goBin, "build", "-trimpath", "-buildvcs=false", "-o", dest, pkg}, filepath.Join(target, "go"), 900*time.Second, true, env); err != nil {
			if name == "sumctl-go" {
				continue
			}
			return err
		}
	}
	return nil
}
