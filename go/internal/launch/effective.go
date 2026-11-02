package launch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

func effectiveInputDigest(configs map[string]string) (string, error) {
	effective := map[string]any{}
	for path, hash := range configs {
		if hash == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		var config map[string]any
		if filepath.Ext(path) == ".toml" {
			err = toml.Unmarshal(data, &config)
		} else {
			err = json.Unmarshal(data, &config)
		}
		if err != nil {
			return "", fmt.Errorf("effective input configuration %s: %w", path, err)
		}
		if selected := inputSettings(config); len(selected) > 0 {
			effective[path] = selected
		}
	}
	encoded, err := json.Marshal(effective)
	if err != nil {
		return "", err
	}
	return digest(encoded), nil
}

func inputSettings(config map[string]any) map[string]any {
	result := map[string]any{}
	for key, value := range config {
		switch key {
		case "profile", "trust_level", "keymap", "keybindings", "vimMode", "multiline", "multiline_mode", "simple_mode", "program_status", "sandbox", "sandbox_mode", "sandbox_workspace_write", "approval_policy", "approval_mode", "permissions", "permission_mode", "permissionMode", "defaultMode", "allowed_tools", "allowedTools", "disallowedTools", "trust", "disableBypassPermissionsMode", "skipDangerousModePermissionPrompt", "env":
			result[key] = value
		default:
			if child, ok := value.(map[string]any); ok {
				if selected := inputSettings(child); len(selected) > 0 {
					result[key] = selected
				}
			}
		}
	}
	return result
}
