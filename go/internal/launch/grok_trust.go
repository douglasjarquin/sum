package launch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/douglasjarquin/sum/go/internal/proc"
)

// TrustGrokRepository records the directory Grok will ask about before that worker starts.
// Grok names the parent of the checkout's common .git, not the task worktree.
func TrustGrokRepository(ctx context.Context, grokHome, checkout string) error {
	if !filepath.IsAbs(grokHome) || strings.ContainsAny(grokHome, "\x00\r\n") {
		return fmt.Errorf("absolute GROK_HOME required before a grok worker starts")
	}
	common, err := gitCommonDir(ctx, checkout)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(common) || filepath.Base(common) != ".git" {
		return fmt.Errorf("grok asks about the parent of a common .git; got %q", common)
	}
	folder := filepath.Dir(common)
	if folder == string(filepath.Separator) {
		return fmt.Errorf("refusing to trust the filesystem root")
	}
	return appendGrokTrust(grokHome, folder)
}

func gitCommonDir(ctx context.Context, checkout string) (string, error) {
	out, err := proc.RunContext(ctx, proc.Cmd{
		Argv:    []string{"git", "-C", checkout, "rev-parse", "--path-format=absolute", "--git-common-dir"},
		Timeout: 90 * time.Second,
		Check:   true,
	})
	if err != nil {
		return "", fmt.Errorf("grok trust directory: %w", err)
	}
	common := strings.TrimSpace(out.Stdout)
	if common == "" {
		return "", fmt.Errorf("grok trust directory: git returned an empty common dir")
	}
	return common, nil
}

type grokTrustedFolder struct {
	Trusted bool `toml:"trusted"`
}

type grokTrustedFolders struct {
	Folders map[string]grokTrustedFolder `toml:"folders"`
}

func appendGrokTrust(grokHome, folder string) error {
	path := filepath.Join(grokHome, "trusted_folders.toml")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read grok trusted folders: %w", err)
	}
	if len(bytes.TrimSpace(data)) > 0 {
		var doc grokTrustedFolders
		if err := toml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("read grok trusted folders: %w", err)
		}
		if current, ok := doc.Folders[folder]; ok {
			if current.Trusted {
				return nil
			}
			return fmt.Errorf("grok trust for %s is false", folder)
		}
	}
	quoted, err := tomlBasicString(folder)
	if err != nil {
		return err
	}
	suffix := fmt.Appendf(nil, "[folders.%s]\ntrusted = true\ndecided_at = %d\n", quoted, time.Now().Unix())
	if len(data) > 0 {
		if !bytes.HasSuffix(data, []byte("\n")) {
			suffix = append([]byte("\n"), suffix...)
		}
		suffix = append([]byte("\n"), suffix...)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("record grok trust: %w", err)
	}
	_, err = file.Write(suffix)
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("record grok trust: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("record grok trust: %w", closeErr)
	}
	return nil
}

func tomlBasicString(value string) (string, error) {
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("grok trust path contains a control character")
	}
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('"')
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' || value[i] == '"' {
			b.WriteByte('\\')
		}
		b.WriteByte(value[i])
	}
	b.WriteByte('"')
	return b.String(), nil
}
