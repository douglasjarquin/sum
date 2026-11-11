package pycompat

import (
	"os"
	"path/filepath"
	"strings"
)

// A chain of symbolic links longer than this is treated as a path that does not exist.
const maxSymlinks = 40

// Join joins rel onto base: an absolute rel replaces base, empty and `.` components are dropped,
// and `..` is kept for the filesystem to resolve rather than collapsed lexically.
func Join(base, rel string) string {
	path := rel
	if !strings.HasPrefix(rel, "/") {
		path = base + "/" + rel
	}
	var parts []string
	for _, part := range strings.Split(path, "/") {
		if part != "" && part != "." {
			parts = append(parts, part)
		}
	}
	joined := strings.Join(parts, "/")
	if strings.HasPrefix(path, "/") {
		return "/" + joined
	}
	if joined == "" {
		return "."
	}
	return joined
}

// Resolve is the absolute path with symbolic links followed for as far as the path exists
// and the remainder kept lexically.
func Resolve(path string) string {
	if !strings.HasPrefix(path, "/") {
		cwd, _ := os.Getwd()
		path = cwd + "/" + path
	}
	pending := strings.Split(path, "/")
	resolved := ""
	links := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			resolved = resolved[:max(strings.LastIndex(resolved, "/"), 0)]
			continue
		}
		next := resolved + "/" + part
		info, err := os.Lstat(next)
		if err != nil || info.Mode()&os.ModeSymlink == 0 || links >= maxSymlinks {
			resolved = next
			continue
		}
		target, err := os.Readlink(next)
		if err != nil {
			resolved = next
			continue
		}
		links++
		if strings.HasPrefix(target, "/") {
			resolved = ""
		}
		pending = append(strings.Split(target, "/"), pending...)
	}
	if resolved == "" {
		return "/"
	}
	return resolved
}

// ResolveExistingPrefix is the absolute path with symbolic links resolved as far as the path exists; whatever does not exist yet is kept as written.
func ResolveExistingPrefix(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	rest := ""
	for current := abs; ; {
		if real, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return abs
		}
		current, rest = parent, filepath.Join(filepath.Base(current), rest)
	}
}

// ExpandUser replaces a leading "~" with the home directory.
func ExpandUser(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return home + path[1:]
}

// Rel is path relative to root: a lexical comparison that never touches the filesystem. ok is false when path is not under root.
func Rel(path, root string) (rel string, ok bool) {
	if path == root {
		return ".", true
	}
	prefix := strings.TrimSuffix(root, "/") + "/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	return path[len(prefix):], true
}

// Within reports whether path is root or lies under it, lexically.
func Within(path, root string) bool {
	_, ok := Rel(path, root)
	return ok
}

// IsFile reports a regular file, following symbolic links.
func IsFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// IsDir reports a directory, following symbolic links.
func IsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// Exists reports that the path exists, following symbolic links.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
