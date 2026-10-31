package launch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
	"github.com/pelletier/go-toml/v2"
)

type HCTemplateOptions struct {
	Cwd         string
	CodexHome   string
	ConfigFiles []string
}
type HCTemplate struct {
	Name          string            `json:"name"`
	Harness       string            `json:"harness"`
	Binary        string            `json:"binary"`
	Version       string            `json:"version"`
	Argv          []string          `json:"argv"`
	SHA256        string            `json:"sha256"`
	CommandSHA256 string            `json:"command_sha256"`
	KeymapSHA256  string            `json:"keymap_sha256"`
	ConfigSHA256  map[string]string `json:"config_sha256"`
	Submit        string            `json:"submit"`
}

func (t HCTemplate) RunArgv(taskPointer string) []string { return []string{t.Name, taskPointer} }
func digest(data []byte) string                          { v := sha256.Sum256(data); return hex.EncodeToString(v[:]) }
func shellArgument(text string) string                   { return "'" + strings.ReplaceAll(text, "'", "'\"'\"'") + "'" }

func TemplateCommand(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = shellArgument(arg)
	}
	return strings.Join(quoted, " ")
}

func binaryDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func PrepareHCTemplate(runtimeRoot, coreConfigHome, harness string, argv []string, options HCTemplateOptions) (HCTemplate, error) {
	var result HCTemplate
	if harness != "codex" {
		return result, fmt.Errorf("no qualified native status and input contract for %s; automatic launch is disabled", harness)
	}
	if !filepath.IsAbs(coreConfigHome) || !filepath.IsAbs(options.Cwd) {
		return result, fmt.Errorf("explicit absolute HC config home and launch directory required")
	}
	binary, err := toolpath.Find(runtimeRoot, "codex")
	if err != nil {
		return result, err
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return result, err
	}
	version, err := proc.Run([]string{binary, "--version"}, options.Cwd, 10*time.Second, true, nil)
	if err != nil {
		return result, err
	}
	if strings.TrimSpace(version.Stdout) != "codex-cli 0.166.0" {
		return result, fmt.Errorf("Codex status/Tab input is qualified only for 0.166.0; found %q", strings.TrimSpace(version.Stdout))
	}
	binaryHash, err := binaryDigest(binary)
	if err != nil {
		return result, err
	}
	configs, keymapHash, err := codexDefaultKeymap(argv, options)
	if err != nil {
		return result, err
	}
	args := append([]string{}, argv...)
	if !slices.Contains(args, "--no-daemon") {
		args = append([]string{"--no-daemon"}, args...)
	}
	command := append([]string{binary}, args...)
	for _, arg := range command {
		if strings.ContainsAny(arg, "\x00\r\n") {
			return result, fmt.Errorf("HC template argv must contain no NUL or line endings")
		}
	}
	commandText := TemplateCommand(command)
	provenance, _ := json.Marshal(struct {
		Command      []string
		BinarySHA256 string
		KeymapSHA256 string
		ConfigSHA256 map[string]string
	}{command, binaryHash, keymapHash, configs})
	commandHash := digest(provenance)
	name := "sum-codex-" + commandHash[:20]
	text := "---\ndescription: Sum qualified Codex 0.166.0\ncommand: " + commandText + "\nsubmit: tab\n---\nRead the assigned Sum task pointer and follow its installed helper and procedure.\n"
	result = HCTemplate{Name: name, Harness: harness, Binary: binary, Version: "0.166.0", Argv: command, SHA256: digest([]byte(text)), CommandSHA256: commandHash, KeymapSHA256: keymapHash, ConfigSHA256: configs, Submit: "tab"}
	coreConfigHome, err = filepath.EvalSymlinks(coreConfigHome)
	if err != nil {
		return HCTemplate{}, fmt.Errorf("resolve explicitly selected HC config home: %w", err)
	}
	directory := filepath.Join(coreConfigHome, "herdr-core", "templates")
	if err = rejectSymlinkAncestors(directory); err != nil {
		return HCTemplate{}, err
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return HCTemplate{}, err
	}
	path := filepath.Join(directory, name+".md")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return HCTemplate{}, statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return HCTemplate{}, fmt.Errorf("HC template ownership conflict at symlink %s", path)
		}
		prior, e := os.ReadFile(path)
		if e != nil {
			return HCTemplate{}, e
		}
		if string(prior) != text {
			return HCTemplate{}, fmt.Errorf("HC template ownership conflict at %s; existing content preserved", path)
		}
		return result, nil
	}
	if err != nil {
		return HCTemplate{}, err
	}
	_, writeErr := file.WriteString(text)
	closeErr := file.Close()
	if writeErr != nil {
		return HCTemplate{}, writeErr
	}
	if closeErr != nil {
		return HCTemplate{}, closeErr
	}
	return result, nil
}
func rejectSymlinkAncestors(path string) error {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("HC template publication refuses symlink %s", current)
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return nil
}
func codexDefaultKeymap(argv []string, options HCTemplateOptions) (map[string]string, string, error) {
	for index, arg := range argv {
		value := ""
		switch {
		case arg == "-c" || arg == "--config":
			if index+1 >= len(argv) {
				return nil, "", fmt.Errorf("missing Codex config override")
			}
			value = argv[index+1]
		case strings.HasPrefix(arg, "--config="):
			value = strings.TrimPrefix(arg, "--config=")
		case strings.HasPrefix(arg, "-c") && len(arg) > 2:
			value = arg[2:]
		}
		if value != "" {
			key, _, found := strings.Cut(value, "=")
			if !found {
				return nil, "", fmt.Errorf("Codex config override must name a value")
			}
			var keyPath map[string]any
			if err := toml.Unmarshal([]byte(strings.TrimSpace(key)+" = true"), &keyPath); err != nil {
				return nil, "", fmt.Errorf("invalid Codex config override key: %w", err)
			}
			var override map[string]any
			_ = toml.Unmarshal([]byte(value), &override)
			if containsKeymap(keyPath) || containsKeymap(override) {
				return nil, "", fmt.Errorf("Codex keymap override is not qualified for Tab queue submission")
			}
		}
	}
	home := options.CodexHome
	if home == "" {
		home = os.Getenv("CODEX_HOME")
	}
	if home == "" {
		user, err := os.UserHomeDir()
		if err != nil {
			return nil, "", err
		}
		home = filepath.Join(user, ".codex")
	}
	paths := []string{"/etc/codex/config.toml", filepath.Join(home, "config.toml")}
	for current := options.Cwd; ; current = filepath.Dir(current) {
		paths = append(paths, filepath.Join(current, ".codex/config.toml"))
		if filepath.Dir(current) == current {
			break
		}
	}
	paths = append(paths, options.ConfigFiles...)
	hashes := map[string]string{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		var config map[string]any
		if err = toml.Unmarshal(data, &config); err != nil {
			return nil, "", fmt.Errorf("read effective Codex config %s: %w", path, err)
		}
		if containsKeymap(config) {
			return nil, "", fmt.Errorf("Codex keymap in %s is not qualified for Tab queue submission; configuration preserved", path)
		}
		hashes[path] = digest(data)
	}

	return hashes, digest([]byte("codex-cli/0.166.0/default-tui-keymap/tab-submit-or-queue")), nil
}
func containsKeymap(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "keymap" {
				return true
			}
			if containsKeymap(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if containsKeymap(child) {
				return true
			}
		}
	}
	return false
}

func ValidateHCTemplate(coreConfigHome string, spec HCTemplate, options HCTemplateOptions) error {
	if spec.Harness != "codex" || spec.Version != "0.166.0" || spec.Submit != "tab" || len(spec.CommandSHA256) != 64 || len(spec.Argv) == 0 || spec.Argv[0] != spec.Binary {
		return fmt.Errorf("unqualified HC template record")
	}
	binaryHash, err := binaryDigest(spec.Binary)
	if err != nil {
		return err
	}
	configs, keymapHash, err := codexDefaultKeymap(spec.Argv[1:], options)
	if err != nil {
		return err
	}
	provenance, _ := json.Marshal(struct {
		Command      []string
		BinarySHA256 string
		KeymapSHA256 string
		ConfigSHA256 map[string]string
	}{spec.Argv, binaryHash, keymapHash, configs})
	if digest(provenance) != spec.CommandSHA256 || keymapHash != spec.KeymapSHA256 || spec.Name != "sum-codex-"+spec.CommandSHA256[:20] {
		return fmt.Errorf("HC command or effective keymap configuration changed after qualification")
	}
	path := filepath.Join(coreConfigHome, "herdr-core/templates", spec.Name+".md")
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("HC template was replaced by a symlink")
	}
	template, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if digest(template) != spec.SHA256 {
		return fmt.Errorf("HC template changed after qualification")
	}
	return nil
}

func ValidateHCInputConfig(harness string, argv []string, options HCTemplateOptions) error {
	if harness != "codex" {
		return fmt.Errorf("no qualified native status and input contract for %s", harness)
	}
	_, _, err := codexDefaultKeymap(argv, options)
	return err
}
