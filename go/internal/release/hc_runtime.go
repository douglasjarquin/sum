package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/proc"
)

// VerifyHCRuntime verifies the executable and its matched public contract without
// starting a core. A helper's capability report alone cannot qualify its runtime.
func VerifyHCRuntime(ctx context.Context, target string) error {
	read := func(path string, value any) error {
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return json.Unmarshal(body, value)
	}
	var releaseManifest map[string]any
	if body, err := os.ReadFile(filepath.Join(target, Manifest)); err == nil {
		if err = json.Unmarshal(body, &releaseManifest); err != nil {
			return fmt.Errorf("HC release manifest: %w", err)
		}
		dependencies, _ := releaseManifest["dependencies"].(map[string]any)
		hc, _ := dependencies["hc"].(map[string]any)
		if qualified, present := hc["qualified"]; present && qualified == false {
			return fmt.Errorf("offline HC test fixture cannot qualify an installation runtime")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	var pinned, archived map[string]any
	if err := read(filepath.Join(target, "config", "hc-release.json"), &pinned); err != nil {
		return fmt.Errorf("HC release pin: %w", err)
	}
	docs, err := filepath.EvalSymlinks(filepath.Join(target, ".local", "share", "hc"))
	if err != nil {
		return fmt.Errorf("HC matched contract: %w", err)
	}
	if err = read(filepath.Join(docs, "manifest.json"), &archived); err != nil || !reflect.DeepEqual(pinned, archived) {
		return fmt.Errorf("HC matched manifest differs from release pin")
	}
	var receipt struct {
		Version    string `json:"version"`
		Generation int    `json:"generation"`
		Source     string `json:"source_commit"`
		Platform   string `json:"platform"`
		Archive    string `json:"archive_sha256"`
		Binary     string `json:"binary"`
		BinaryHash string `json:"binary_sha256"`
		SchemaHash string `json:"schema_sha256"`
		ManualHash string `json:"manual_sha256"`
	}
	if err = read(filepath.Join(docs, "provenance.json"), &receipt); err != nil {
		return fmt.Errorf("HC provenance: %w", err)
	}
	platforms := map[string]string{"darwin-arm64": "macos-aarch64", "darwin-amd64": "macos-x86_64", "linux-arm64": "linux-aarch64", "linux-amd64": "linux-x86_64"}
	key := platforms[receipt.Platform]
	targets, _ := pinned["targets"].(map[string]any)
	pin, _ := targets[key].(map[string]any)
	if receipt.Version != contract.HCVersion || receipt.Generation != contract.HCGeneration || receipt.Source != "29e6d375623de40cb4820e0894aa131ba057b796" || key == "" || receipt.Platform != nativePlatform() || !sha256Hex.MatchString(receipt.Archive) || pin["sha256"] != receipt.Archive || pin["url"] != "https://releases.herdr.dev/core/"+contract.HCVersion+"/herdr-core-"+key+".tar.gz" || pinned["version"] != contract.HCVersion || pinned["generation"] != float64(contract.HCGeneration) {
		return fmt.Errorf("HC provenance does not identify the selected published artifact")
	}
	if releaseManifest != nil {
		dependencies, _ := releaseManifest["dependencies"].(map[string]any)
		savedReceipt, _ := dependencies["hc"].(map[string]any)
		var actualReceipt map[string]any
		if err = read(filepath.Join(docs, "provenance.json"), &actualReceipt); err != nil || !reflect.DeepEqual(savedReceipt, actualReceipt) {
			return fmt.Errorf("HC release dependency metadata differs from matched provenance")
		}
	}
	if receipt.Binary == "" || filepath.IsAbs(receipt.Binary) || containsDotDot(receipt.Binary) {
		return fmt.Errorf("HC provenance binary path is invalid")
	}
	binary, err := filepath.EvalSymlinks(filepath.Join(target, ".local", "bin", "hc"))
	if err != nil {
		return fmt.Errorf("HC executable: %w", err)
	}
	expected, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(docs), receipt.Binary))
	if err != nil || binary != expected || !isExecutable(binary) {
		return fmt.Errorf("HC executable differs from matched contract")
	}
	for _, member := range []struct{ path, hash string }{{binary, receipt.BinaryHash}, {filepath.Join(docs, "openrpc.json"), receipt.SchemaHash}, {filepath.Join(docs, "manual.md"), receipt.ManualHash}} {
		hash, err := Sha256File(member.path)
		if err != nil || hash != member.hash || len(hash) != 64 {
			return fmt.Errorf("HC artifact or matched contract checksum differs: %s", member.path)
		}
	}
	for _, check := range []struct {
		args     []string
		expected string
		hash     bool
	}{{[]string{"--version"}, "hc " + contract.HCVersion, false}, {[]string{"schema"}, receipt.SchemaHash, true}, {[]string{"--skill"}, receipt.ManualHash, true}} {
		result, err := proc.RunContext(ctx, proc.Cmd{Argv: append([]string{binary}, check.args...), Dir: target, Env: proc.ScrubbedEnv(), Timeout: 20 * time.Second})
		if err != nil || result.Code != 0 {
			return fmt.Errorf("HC artifact contract command failed: %v", err)
		}
		actual := strings.TrimSpace(result.Stdout)
		if check.hash {
			sum := sha256.Sum256([]byte(result.Stdout))
			actual = hex.EncodeToString(sum[:])
		}
		if actual != check.expected {
			return fmt.Errorf("HC executable contract differs for %s", check.args[0])
		}
	}
	return nil
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)
