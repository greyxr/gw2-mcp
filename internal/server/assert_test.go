package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func check(t testing.TB, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(format, args...)
	}
}

func connectTest(t testing.TB, server *mcp.Server) (context.Context, *mcp.ClientSession) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	check(t, err == nil, "%v", err)
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	check(t, err == nil, "%v", err)
	t.Cleanup(func() { cs.Close() })
	return ctx, cs
}

func callTool(t testing.TB, ctx context.Context, session *mcp.ClientSession, name, args string) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(args)})
	check(t, err == nil, "%s: %v", name, err)
	return result
}

func toolData[T any](t testing.TB, result *mcp.CallToolResult) T {
	t.Helper()
	check(t, result != nil && result.StructuredContent != nil, "tool result has no structured content")
	encoded, err := json.Marshal(result.StructuredContent)
	check(t, err == nil, "encode tool result: %v", err)
	var wrapped struct {
		Data T `json:"data"`
	}
	check(t, json.Unmarshal(encoded, &wrapped) == nil, "decode tool result: %s", encoded)
	return wrapped.Data
}
