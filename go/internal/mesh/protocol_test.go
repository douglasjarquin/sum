package mesh

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPServer_negotiatesListsAndCallsThroughTheSharedService(t *testing.T) {
	runner := &fakeRunner{results: []commandResult{{stdout: `{"result":{"agents":[]}}`}}}
	service := newTestService(t, "coordinator", runner)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- NewMCPServer(service).Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 10 {
		t.Fatalf("tool count = %d, want 10", len(tools.Tools))
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "herdr_agent_list", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if _, ok := result.Content[0].(*mcp.TextContent); !ok {
		t.Fatalf("content type = %T", result.Content[0])
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("MCP server did not stop after context cancellation")
	}
}
