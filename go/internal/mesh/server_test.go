package mesh

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServerNegotiatesToolsAndUsesMCPCallErrors(t *testing.T) {
	t.Setenv("SUM_SESSION", "sum-test")
	t.Setenv("CALLS", filepath.Join(t.TempDir(), "calls"))
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := NewServer(NewOperations(NewRunner(fakeHerdr(t))))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mesh-test", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 10 {
		t.Fatalf("tool count = %d", len(tools.Tools))
	}
	var paneRead *mcp.Tool
	for _, tool := range tools.Tools {
		if tool.Name == "herdr_pane_read" {
			paneRead = tool
		}
	}
	if paneRead == nil {
		t.Fatal("herdr_pane_read is missing")
	}
	schema, ok := paneRead.InputSchema.(map[string]any)
	if !ok {
		encoded, marshalErr := json.Marshal(paneRead.InputSchema)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
	}
	properties := schema["properties"].(map[string]any)
	if _, ok := properties["pane_id"]; !ok {
		t.Fatalf("pane schema = %#v", schema)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "herdr_agent_list", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("agent list error: %#v", result)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content = %#v", result.Content)
	}
	if _, err := os.Stat(filepath.Dir(os.Getenv("CALLS"))); err != nil {
		t.Fatal(err)
	}
	invalid, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "herdr_agent_get", Arguments: map[string]any{"target": "-bad"}})
	if err != nil {
		t.Fatal(err)
	}
	if !invalid.IsError {
		t.Fatal("invalid target was accepted")
	}
}

func TestServerRequestCancellationStopsOperation(t *testing.T) {
	t.Setenv("SUM_SESSION", "sum-test")
	t.Setenv("CALLS", filepath.Join(t.TempDir(), "calls"))
	t.Setenv("FAKE_HANG", "1")
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := NewServer(NewOperations(NewRunner(fakeHerdr(t))))
	ctx := context.Background()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mesh-cancel-test", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	callCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := session.CallTool(callCtx, &mcp.CallToolParams{Name: "herdr_agent_list", Arguments: map[string]any{}}); err == nil {
		t.Fatal("cancelled call unexpectedly succeeded")
	}
}
