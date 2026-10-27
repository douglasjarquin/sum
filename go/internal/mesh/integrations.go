package mesh

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type integrationStatusEntry struct {
	Name            string  `json:"name"`
	Status          string  `json:"status"`
	Version         *string `json:"version"`
	ExpectedVersion *string `json:"expected_version"`
	Path            string  `json:"path"`
}

var integrationStatusLine = regexp.MustCompile(`^(.+): (not installed|current \((v[0-9]+|legacy)\)|outdated \((v[0-9]+|legacy) < (v[0-9]+)\)) \((.*)\)$`)

func (s Service) integrationStatus(ctx context.Context) (string, error) {
	args := []string{"integration", "status"}
	if err := s.authorize(ctx, args); err != nil {
		return "", err
	}
	result, err := s.runner.Run(ctx, args, 10*time.Second, false)
	if err != nil {
		return "", err
	}
	integrations, err := parseIntegrationStatus(result.stdout)
	if err != nil {
		return "", err
	}
	return jsonText(map[string]any{"integrations": integrations})
}

func parseIntegrationStatus(output string) ([]integrationStatusEntry, error) {
	integrations := []integrationStatusEntry{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		match := integrationStatusLine.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("Herdr returned unrecognized integration status line: %s", trim(line))
		}
		entry := integrationStatusEntry{Name: match[1], Path: match[6]}
		switch {
		case match[2] == "not installed":
			entry.Status = "not_installed"
		case match[3] != "":
			entry.Status = "current"
			entry.Version = &match[3]
		default:
			entry.Status = "outdated"
			entry.Version = &match[4]
			entry.ExpectedVersion = &match[5]
		}
		integrations = append(integrations, entry)
	}
	return integrations, nil
}
