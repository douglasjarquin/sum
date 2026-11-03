package runtime

import (
	"os"
	"path/filepath"
	"strings"
)

const Releases = ".local/releases"

func RootFromHelper(path string) string {
	if path == "" {
		return ""
	}
	binDir := filepath.Dir(path)
	parent := filepath.Dir(binDir)
	if filepath.Base(parent) == ".local" {
		return filepath.Dir(parent)
	}
	return parent
}

func ResolveInstallation(runtimeRoot, envInstallRoot string) string {
	if envInstallRoot == "" {
		return runtimeRoot
	}
	candidate := resolve(envInstallRoot)
	if samePath(candidate, runtimeRoot) {
		return candidate
	}
	releases := resolve(filepath.Join(candidate, ".local", "releases"))
	p := resolve(runtimeRoot)
	for {
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		if samePath(parent, releases) {
			return candidate
		}
		p = parent
	}
	return runtimeRoot
}

func DefaultHome(installRoot string) string {
	if value := os.Getenv("SUM_HOME"); value != "" {
		relative, err := filepath.Rel(resolve(installRoot), resolve(value))
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
			return resolve(value)
		}
	}
	return filepath.Join(installRoot, ".sum")
}

func resolve(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	ancestor := absolute
	var tail []string
	for {
		if resolved, err := filepath.EvalSymlinks(ancestor); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return absolute
		}
		tail = append(tail, filepath.Base(ancestor))
		ancestor = parent
	}
}

func samePath(a, b string) bool {
	return resolve(a) == resolve(b)
}
