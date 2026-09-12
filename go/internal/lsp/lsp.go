package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

const InstallTimeout = 120 * time.Second

type Pin struct {
	Tool    string
	Version string
	Depends []string
}

var Allowlist = map[string]Pin{
	"basedpyright-langserver": {Tool: "pipx:basedpyright", Version: "1.40.1", Depends: []string{"python", "uv"}},
	"gopls":                   {Tool: "go:golang.org/x/tools/gopls", Version: "0.23.0", Depends: []string{"go"}},
}

var commandNotFound = regexp.MustCompile(`Command not found:\s+([A-Za-z0-9._+-]+)`)

func Lookup(binary string) (Pin, bool) {
	pin, ok := Allowlist[binary]
	return pin, ok
}

func MissingBinaries(payload []byte) []string {
	text := collectText(payload)
	seen := map[string]struct{}{}
	var out []string
	add := func(name string) {
		if name == "" || strings.ContainsAny(name, `/\:`) {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	for _, match := range commandNotFound.FindAllStringSubmatch(text, -1) {
		add(match[1])
	}
	if strings.Contains(text, "NOT INSTALLED") {
		for binary := range Allowlist {
			if containsWord(text, binary) {
				add(binary)
			}
		}
	}
	sort.Strings(out)
	return out
}

func Ensure(root string, payload []byte) error {
	if root == "" {
		return nil
	}
	var first error
	for _, binary := range MissingBinaries(payload) {
		if err := ensureBinary(root, binary); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func ensureBinary(root, binary string) error {
	pin, ok := Lookup(binary)
	if !ok {
		return fmt.Errorf("unknown LSP binary %s", binary)
	}
	link := filepath.Join(root, ".local", "bin", binary)
	if info, err := os.Lstat(link); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	mise, err := toolpath.Find(root, "mise")
	if err != nil {
		return err
	}
	spec := pin.Tool + "@" + pin.Version
	if _, err := runMise(root, mise, InstallTimeout, "install", spec); err != nil {
		return err
	}
	resolved, err := resolveBinary(root, mise, binary, pin)
	if err != nil {
		return err
	}
	_, err = LinkTool(link, resolved)
	return err
}

func resolveBinary(root, mise, binary string, pin Pin) (string, error) {
	out, err := runMise(root, mise, 60*time.Second, "which", binary)
	if err == nil {
		if path := strings.TrimSpace(out); path != "" {
			if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
				return path, nil
			}
			if sibling := filepath.Join(filepath.Dir(path), binary); sibling != path {
				if info, statErr := os.Stat(sibling); statErr == nil && !info.IsDir() {
					return sibling, nil
				}
			}
		}
	}
	toolName := pin.Tool
	if i := strings.LastIndex(toolName, ":"); i >= 0 {
		toolName = toolName[i+1:]
	}
	if toolName != "" && toolName != binary {
		out, err = runMise(root, mise, 60*time.Second, "which", toolName)
		if err == nil {
			dir := filepath.Dir(strings.TrimSpace(out))
			candidate := filepath.Join(dir, binary)
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("mise did not resolve %s", binary)
}

type LinkResult struct {
	Target  string
	Created bool
	Differs bool
}

func LinkTool(link, target string) (LinkResult, error) {
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return LinkResult{}, err
	}
	dest := linkDestination(link, target)
	info, err := os.Lstat(link)
	if err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return LinkResult{}, fmt.Errorf("Refusing to replace non-symlink %s", link)
		}
		current, readErr := os.Readlink(link)
		if readErr != nil {
			return LinkResult{}, readErr
		}
		return LinkResult{Target: current, Created: false, Differs: current != dest}, nil
	}
	if !os.IsNotExist(err) {
		return LinkResult{}, err
	}
	if err := os.Symlink(dest, link); err != nil {
		return LinkResult{}, err
	}
	return LinkResult{Target: dest, Created: true, Differs: false}, nil
}

func linkDestination(link, target string) string {
	rel, err := filepath.Rel(filepath.Dir(link), target)
	if err != nil {
		return target
	}
	return rel
}

func runMise(root, mise string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, mise, args...)
	cmd.Dir = root
	env := append([]string{}, os.Environ()...)
	trusted := filepath.Join(root, "mise.toml")
	if _, err := os.Stat(trusted); err == nil {
		env = append(env, "MISE_TRUSTED_CONFIG_PATHS="+trusted)
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return stdout.String(), fmt.Errorf("mise: timed out after %s; its effect is unknown", timeout)
	}
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail == "" {
			detail = err.Error()
		}
		return stdout.String(), fmt.Errorf("mise %s: %s", strings.Join(args, " "), detail)
	}
	return stdout.String(), nil
}

func collectText(payload []byte) string {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return ""
	}
	var decoded any
	if json.Unmarshal(trimmed, &decoded) != nil {
		return string(payload)
	}
	var b strings.Builder
	collectStrings(&b, decoded)
	if b.Len() == 0 {
		return string(payload)
	}
	return b.String()
}

func collectStrings(b *strings.Builder, value any) {
	switch v := value.(type) {
	case string:
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(v)
	case []any:
		for _, item := range v {
			collectStrings(b, item)
		}
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			collectStrings(b, v[key])
		}
	}
}

func containsWord(text, word string) bool {
	start := 0
	for {
		i := strings.Index(text[start:], word)
		if i < 0 {
			return false
		}
		i += start
		before := i == 0 || !isWordChar(text[i-1])
		after := i+len(word) == len(text) || !isWordChar(text[i+len(word)])
		if before && after {
			return true
		}
		start = i + len(word)
	}
}

func isWordChar(b byte) bool {
	return b == '-' || b == '_' || b == '.' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
