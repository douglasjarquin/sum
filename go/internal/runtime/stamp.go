package runtime

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
)

func SourceSHA(runtimeRoot string) string {
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

func Identity(runtimeRoot string) string {
	if sha := SourceSHA(runtimeRoot); sha != "" {
		return contract.SumVersion + "@" + sha
	}
	return contract.SumVersion
}

func Marker(runtimeRoot string) string {
	return "<!-- sum:" + Identity(runtimeRoot) + " -->"
}
