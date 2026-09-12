package runtime

import (
	"os"
	"path/filepath"
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
		return value
	}
	return filepath.Join(installRoot, ".sum")
}

func resolve(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved
	}
	return absolute
}

func samePath(a, b string) bool {
	return resolve(a) == resolve(b)
}
