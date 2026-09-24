package graph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/proc"
)

const (
	CodegraphVersion = "1.5.0"
	CodegraphPackage = "@colbymchenry/codegraph"
)

var HarnessChoices = []string{"claude", "codex", "cursor", "opencode"}

func IsValidHarness(harness string) bool {
	for _, h := range HarnessChoices {
		if h == harness {
			return true
		}
	}
	return false
}

func binPath(runtimeRoot string) string {
	if override := os.Getenv("SUM_CODEGRAPH_BIN"); override != "" {
		return override
	}
	return filepath.Join(runtimeRoot, ".local", "bin", "codegraph")
}

func Tool(runtimeRoot string) *ordjson.Object {
	path := binPath(runtimeRoot)
	row := ordjson.NewObject()
	row.Set("pinned", CodegraphVersion)
	row.Set("path", path)
	row.Set("available", false)
	row.Set("version", nil)
	row.Set("reason", nil)

	info, statErr := os.Stat(path)
	if statErr != nil || info.IsDir() {
		row.Set("reason", fmt.Sprintf("codegraph is not installed in this runtime (%s). `mise run setup` or a staged release links the pinned %s@%s; a global or floating installation is never used.", path, CodegraphPackage, CodegraphVersion))
		return row
	}

	result, runErr := proc.RunContext(context.Background(), proc.Cmd{
		Argv:    []string{path, "--version"},
		Env:     codegraphEnv(),
		Timeout: 60 * time.Second,
	})
	if runErr != nil {
		row.Set("reason", fmt.Sprintf("codegraph did not answer `--version`: %s", runErr))
		return row
	}

	var lines []string
	for _, line := range strings.Split(result.Stdout, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	var version string
	if len(lines) > 0 {
		version = lines[len(lines)-1]
		row.Set("version", version)
	}

	exitCode := result.Code
	if exitCode != 0 || version == "" {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(result.Stdout)
		}
		if len(detail) > 300 {
			detail = detail[len(detail)-300:]
		}
		row.Set("reason", fmt.Sprintf("codegraph at %s exited %d without a version: %s", path, exitCode, detail))
		return row
	}
	if version != CodegraphVersion {
		row.Set("reason", fmt.Sprintf("codegraph %s at %s is not the tested pin %s; only the pinned release is used, nothing is upgraded or downgraded", version, path, CodegraphVersion))
		return row
	}
	row.Set("available", true)
	return row
}

func Config(runtimeRoot, harness string) (*ordjson.Object, error) {
	tool := Tool(runtimeRoot)
	available, _ := tool.Get("available")
	if available != true {
		reasonValue, _ := tool.Get("reason")
		reason, _ := reasonValue.(string)
		return nil, fmt.Errorf("No snippet: %s", reason)
	}
	commandValue, _ := tool.Get("path")
	command, _ := commandValue.(string)

	var target, format, snippet string
	switch harness {
	case "claude":
		target, format = ".mcp.json in the checkout (project scope)", "json"
		snippet = mcpServersSnippet(command, []any{"serve", "--mcp"})
	case "codex":
		target, format = ".codex/config.toml in the checkout", "toml"
		snippet = fmt.Sprintf("[mcp_servers.codegraph]\ncommand = %s\nargs = [\"serve\", \"--mcp\"]\n", ordjson.QuoteString(command))
	case "cursor":
		target, format = ".cursor/mcp.json in the checkout", "json"
		snippet = mcpServersSnippet(command, []any{"serve", "--mcp", "--path", "${workspaceFolder}"})
	default:
		target, format = "opencode.json in the checkout", "json"
		snippet = opencodeSnippet(command)
	}

	result := ordjson.NewObject()
	result.Set("harness", harness)
	result.Set("format", format)
	result.Set("target", target)
	result.Set("snippet", snippet)
	result.Set("tool", tool)
	result.Set("note", "Printed only; sum wrote no file, changed no permission list, and did not run `codegraph install`. The server this starts watches only the project it is started in; it is that harness session's process, and cleanup reports it as an occupant of the checkout until the session exits.")
	return result, nil
}

func mcpServersSnippet(command string, args []any) string {
	inner := ordjson.NewObject()
	inner.Set("type", "stdio")
	inner.Set("command", command)
	inner.Set("args", args)
	servers := ordjson.NewObject()
	servers.Set("codegraph", inner)
	root := ordjson.NewObject()
	root.Set("mcpServers", servers)
	encoded, _ := ordjson.MarshalIndent(root)
	return string(encoded)
}

func opencodeSnippet(command string) string {
	inner := ordjson.NewObject()
	inner.Set("type", "local")
	inner.Set("command", []any{command, "serve", "--mcp"})
	inner.Set("enabled", true)
	mcp := ordjson.NewObject()
	mcp.Set("codegraph", inner)
	root := ordjson.NewObject()
	root.Set("mcp", mcp)
	encoded, _ := ordjson.MarshalIndent(root)
	return string(encoded)
}
