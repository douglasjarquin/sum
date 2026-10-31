package launch

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type nativeProfile struct {
	label, version, versionPattern, submit string
}

var nativeProfiles = map[string]nativeProfile{
	"codex":  {"Codex", "0.166.0", `^codex-cli 0\.166\.0$`, "tab"},
	"grok":   {"Grok Build", "1.0.67", `^grok 1\.0\.67 \([0-9a-f]+\) \[stable\]$`, "enter"},
	"claude": {"Claude Code", "2.1.316", `^2\.1\.316 \(Claude Code\)$`, "enter"},
	"devin":  {"Devin", "3000.11.234", `^devin 3000\.11\.234 \([0-9a-f]+\)$`, "enter"},
}

func (p nativeProfile) matchesVersion(text string) bool {
	return regexp.MustCompile(p.versionPattern).MatchString(text)
}

func nativeInputConfig(harness string, argv []string, options HCTemplateOptions) (map[string]string, string, error) {
	profile, ok := nativeProfiles[harness]
	if !ok {
		return nil, "", fmt.Errorf("no qualified native status and input contract for %s", harness)
	}
	environment, err := nativeEnvironment(harness, options)
	if err != nil {
		return nil, "", err
	}
	options.InputEnv = environment
	if harness == "codex" {
		return codexDefaultKeymap(argv, options)
	}
	home := environment["HOME"]
	var paths []string
	switch harness {
	case "claude":
		config := environment["CLAUDE_CONFIG_DIR"]
		if config == "" {
			config = filepath.Join(home, ".claude")
		}
		paths = []string{filepath.Join(config, "keybindings.json"), filepath.Join(config, "settings.json")}
	case "grok":
		config := environment["GROK_HOME"]
		if config == "" {
			config = filepath.Join(home, ".grok")
		}
		paths = []string{filepath.Join(config, "managed_config.toml"), filepath.Join(config, "config.toml"), filepath.Join(config, "settings.json"), filepath.Join(config, "requirements.toml"), filepath.Join(config, "pager.toml"), "/etc/grok/managed_config.toml", "/etc/grok/requirements.toml"}
		if overlay := environment["GROK_CONFIG_PATH"]; overlay != "" {
			paths = append(paths, overlay)
		}
	case "devin":
		config := environment["XDG_CONFIG_HOME"]
		if config == "" {
			config = filepath.Join(home, ".config")
		}
		paths = []string{filepath.Join(config, "devin/config.json")}
	}
	for current := options.Cwd; ; current = filepath.Dir(current) {
		switch harness {
		case "claude":
			paths = append(paths, filepath.Join(current, ".claude/settings.json"), filepath.Join(current, ".claude/settings.local.json"))
		case "grok":
			paths = append(paths, filepath.Join(current, ".grok/config.toml"), filepath.Join(current, ".grok/settings.json"))
		case "devin":
			paths = append(paths, filepath.Join(current, ".devin/config.json"))
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	hashes := map[string]string{}
	for _, arg := range argv {
		key, _, _ := strings.Cut(arg, "=")
		switch key {
		case "-p", "--print", "--single", "--prompt-file", "--prompt-json", "--cloud", "--bg", "--background", "--help", "-h", "--version", "-v", "--safe-mode", "--bare", "--settings", "--config":
			return nil, "", fmt.Errorf("%s %s does not preserve the qualified interactive input and permission contract", harness, key)
		}
	}
	paths = append(paths, options.ConfigFiles...)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			hashes[path] = ""
			continue
		}
		if err != nil {
			return nil, "", err
		}
		hashes[path] = digest(data)
		if filepath.Base(path) == "keybindings.json" && len(strings.TrimSpace(string(data))) > 0 {
			return nil, "", fmt.Errorf("custom Claude keybindings in %s are not qualified; configuration preserved", path)
		}
		var config map[string]any
		if filepath.Ext(path) == ".toml" {
			err = toml.Unmarshal(data, &config)
		} else {
			err = json.Unmarshal(data, &config)
		}
		if err != nil {
			return nil, "", fmt.Errorf("read effective %s input config %s: %w", harness, path, err)
		}
		if err = checkNativeInputSettings(harness, config); err != nil {
			return nil, "", fmt.Errorf("%s: %w; configuration preserved", path, err)
		}
	}
	return hashes, digest([]byte(harness + "/" + profile.version + "/default-input/" + profile.submit)), nil
}

func checkNativeInputSettings(harness string, value any) error {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			switch key {
			case "keymap", "keybindings":
				return fmt.Errorf("custom %s keymap is not qualified", harness)
			case "simple_mode":
				if child != true && child != nil {
					return fmt.Errorf("simple_mode changes composer submission")
				}
			case "vimMode", "multiline", "multiline_mode":
				if child != false && child != nil {
					return fmt.Errorf("%s changes composer submission", key)
				}
			case "program_status":
				if child == "off" || child == false {
					return fmt.Errorf("native status reporting is disabled")
				}
				if status, ok := child.(map[string]any); ok {
					if status["mode"] == "off" || (status["query"] == false && status["mode"] != "on") {
						return fmt.Errorf("native status reporting is disabled")
					}
				}
			}
			if err := checkNativeInputSettings(harness, child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := checkNativeInputSettings(harness, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func NativeHarnessSupported(harness string) bool { _, ok := nativeProfiles[harness]; return ok }

func nativeEnvironment(harness string, options HCTemplateOptions) (map[string]string, error) {
	environment := maps.Clone(options.InputEnv)
	if environment == nil {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		environment = map[string]string{"HOME": home}
		for key, fallback := range map[string]string{"CODEX_HOME": filepath.Join(home, ".codex"), "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "GROK_HOME": filepath.Join(home, ".grok"), "XDG_CONFIG_HOME": filepath.Join(home, ".config")} {
			value := os.Getenv(key)
			if value == "" {
				value = fallback
			}
			environment[key] = value
		}
		if options.CodexHome != "" {
			environment["CODEX_HOME"] = options.CodexHome
		}
	}
	for _, key := range []string{"HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "GROK_HOME", "XDG_CONFIG_HOME"} {
		value := environment[key]
		if !filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
			return nil, fmt.Errorf("absolute frozen %s required for native input", key)
		}
	}
	if harness == "grok" {
		inline, known := environment["GROK_CONFIG"]
		if !known {
			inline = os.Getenv("GROK_CONFIG")
		}
		if inline != "" {
			return nil, fmt.Errorf("inline GROK_CONFIG is not qualified for managed input")
		}
		environment["GROK_CONFIG"] = ""
		overlay, known := environment["GROK_CONFIG_PATH"]
		if !known {
			overlay = os.Getenv("GROK_CONFIG_PATH")
		}
		if overlay != "" && (!filepath.IsAbs(overlay) || strings.ContainsAny(overlay, "\x00\r\n")) {
			return nil, fmt.Errorf("absolute GROK_CONFIG_PATH required")
		}
		environment["GROK_CONFIG_PATH"] = overlay
	}
	return environment, nil
}

func commandWithEnvironment(argv []string, environment map[string]string) string {
	keys := slices.Sorted(maps.Keys(environment))
	command := []string{"/usr/bin/env"}
	for _, key := range keys {
		command = append(command, key+"="+environment[key])
	}
	return TemplateCommand(append(command, argv...))
}

func (t HCTemplate) Command() string { return commandWithEnvironment(t.Argv, t.InputEnv) }

func (t HCTemplate) BindInputEnvironment(environment map[string]string) {
	for key, value := range t.InputEnv {
		environment[key] = value
	}
}
