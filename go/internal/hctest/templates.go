package hctest

import (
	"fmt"
	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (f *Fixture) LoadTemplatesFrom(configHome string) error {
	if !filepath.IsAbs(configHome) {
		return fmt.Errorf("absolute fixture HC configuration home required")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.templateConfigHome = configHome
	return nil
}
func (f *Fixture) nativeTemplatesLocked() ([]hcclient.TemplateInfo, map[string]string) {
	templates := map[string]hcclient.TemplateInfo{}
	submits := map[string]string{}
	if f.templateConfigHome != "" {
		paths, _ := filepath.Glob(filepath.Join(f.templateConfigHome, "herdr-core", "templates", "*.md"))
		sort.Strings(paths)
		for _, path := range paths {
			name := strings.TrimSuffix(filepath.Base(path), ".md")
			template := hcclient.TemplateInfo{Name: name}
			data, err := os.ReadFile(path)
			if err == nil {
				text := strings.ReplaceAll(string(data), "\r\n", "\n")
				parts := strings.SplitN(text, "---\n", 3)
				if len(parts) != 3 || parts[0] != "" {
					err = fmt.Errorf("template has no literal native frontmatter")
				} else {
					for _, line := range strings.Split(parts[1], "\n") {
						if line == "" {
							continue
						}
						key, value, ok := strings.Cut(line, ": ")
						if !ok {
							err = fmt.Errorf("unsupported native frontmatter line %q", line)
							break
						}
						switch key {
						case "description":
							template.Description = value
						case "command":
							template.Command = value
						case "submit":
							submits[name] = value
						default:
							err = fmt.Errorf("unsupported native frontmatter field %q", key)
						}
					}
					if template.Command == "" {
						err = fmt.Errorf("native template has no command")
					}
				}
			}
			if err != nil {
				message := err.Error()
				template.Error = &message
			}
			templates[name] = template
		}
	}
	for _, template := range f.templates {
		templates[template.Name] = template
	}
	for name, key := range f.templateSubmits {
		submits[name] = key
	}
	names := make([]string, 0, len(templates))
	for name := range templates {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]hcclient.TemplateInfo, 0, len(names))
	for _, name := range names {
		out = append(out, templates[name])
	}
	return out, submits
}
