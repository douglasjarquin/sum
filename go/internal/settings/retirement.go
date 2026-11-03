package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func NativeRetirementPreferences(s *store.Store, native *ordjson.Object) (*ordjson.Object, bool, error) {
	if _, err := LoadSettings(s); err != nil {
		return nil, false, err
	}
	raw, err := ordjson.ReadFile(filepath.Join(s.Home, File))
	var doc *ordjson.Object
	if errors.Is(err, os.ErrNotExist) {
		doc = ordjson.NewObject()
		doc.Set("schema", json.Number(fmt.Sprint(Schema)))
	} else if err != nil {
		return nil, false, err
	} else {
		var ok bool
		doc, ok = raw.(*ordjson.Object)
		if !ok {
			return nil, false, fmt.Errorf("settings.json must be an object before retirement")
		}
	}
	_, changed := doc.Get("hook")
	doc.Delete("hook")
	if _, explicit := doc.Get("template"); explicit || native == nil {
		return doc, changed, nil
	}
	value, present := native.Get("native_template")
	if !present {
		return doc, changed, nil
	}
	provenance, ok := value.(*ordjson.Object)
	if !ok {
		return nil, false, fmt.Errorf("native_template must be an object before retirement")
	}
	var decoded struct {
		Options *struct {
			CodexHome   string
			ConfigFiles []string
		}
		Spec *struct {
			InputEnv map[string]string `json:"input_env"`
		}
	}
	data, err := ordjson.MarshalCompact(provenance)
	if err != nil {
		return nil, false, err
	}
	if err = json.Unmarshal(data, &decoded); err != nil {
		return nil, false, fmt.Errorf("invalid native template retirement preferences: %w", err)
	}
	if decoded.Options == nil || decoded.Spec == nil {
		return nil, false, fmt.Errorf("native_template requires options and spec before retirement")
	}
	template := TemplateOptions{CodexHome: decoded.Options.CodexHome, ConfigFiles: decoded.Options.ConfigFiles, InputEnv: decoded.Spec.InputEnv}
	if template.CodexHome == "" && len(template.ConfigFiles) == 0 && len(template.InputEnv) == 0 {
		return doc, changed, nil
	}
	block, err := ordjson.FromValue(template)
	if err != nil {
		return nil, false, err
	}
	doc.Set("template", block)
	return doc, true, nil
}
