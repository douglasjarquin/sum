package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
)

func SelectedSourceSHA(runtimeRoot string) string {
	if raw, err := ordjson.ReadFile(filepath.Join(runtimeRoot, "release.json")); err == nil {
		if obj, is := raw.(*ordjson.Object); is {
			if source, ok := obj.Get("source"); ok {
				if src, is := source.(*ordjson.Object); is {
					if sha, ok := src.Get("sha"); ok {
						if text, is := sha.(string); is {
							return text
						}
					}
				}
			}
		}
	}
	out, err := proc.Run([]string{"git", "-C", runtimeRoot, "rev-parse", "HEAD"}, "", 20*time.Second, false, nil)
	if err == nil && out.Code == 0 {
		return strings.TrimSpace(out.Stdout)
	}
	return ""
}

func Identity() string {
	info := buildMetadata()
	if sha, ok := info["source_sha"].(string); ok && sha != "" {
		identity := contract.SumVersion + "@" + sha
		if dirty, _ := info["source_modified"].(bool); dirty {
			identity += "+dirty"
		}
		return identity
	}
	return contract.SumVersion + "@unverified"
}

func Marker() string { return "<!-- sum:" + Identity() + " -->" }

var BuildSourceRevision string
var BuildSourceDigest string
var BuildSourceModified string

func buildMetadata() map[string]any {
	result := map[string]any{"verified_source": false}
	if BuildSourceRevision != "" {
		result["source_sha"] = BuildSourceRevision
		result["verified_source"] = true
	}
	if BuildSourceDigest != "" {
		result["source_digest"] = BuildSourceDigest
	}
	if BuildSourceModified != "" {
		result["source_modified"] = BuildSourceModified == "true"
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		result["go_version"] = info.GoVersion
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				result["source_sha"] = setting.Value
				result["verified_source"] = true
			}
			if setting.Key == "vcs.modified" {
				result["source_modified"] = setting.Value == "true"
			}
		}
	}
	if result["verified_source"] == false {
		result["detail"] = "Build source is unverifiable; checkout HEAD and release metadata do not identify this executable. Stage a verified release with the installed helper."
	}
	return result
}

func BuildIdentity() map[string]any {
	path, err := os.Executable()
	result := map[string]any{"executable": path, "verified_source": false}
	if BuildSourceRevision != "" {
		result["source_sha"] = BuildSourceRevision
		result["verified_source"] = true
	}
	if BuildSourceDigest != "" {
		result["source_digest"] = BuildSourceDigest
	}

	if err != nil {
		result["error"] = err.Error()
		return result
	}
	if file, err := os.Open(path); err == nil {
		defer file.Close()
		hash := sha256.New()
		if _, err = io.Copy(hash, file); err == nil {
			result["sha256"] = hex.EncodeToString(hash.Sum(nil))
		}
	}
	metadata := buildMetadata()
	for key, value := range metadata {
		result[key] = value
	}
	return result
}
