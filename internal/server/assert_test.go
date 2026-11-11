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

func toolText(t testing.TB, result *mcp.CallToolResult) string {
	t.Helper()
	check(t, result != nil && len(result.Content) == 1 && result.StructuredContent == nil, "tool result is not a single text block: %+v", result)
	content, ok := result.Content[0].(*mcp.TextContent)
	check(t, ok, "tool content is %T", result.Content[0])
	return content.Text
}

func toolData[T any](t testing.TB, result *mcp.CallToolResult) T {
	t.Helper()
	var data T
	text := toolText(t, result)
	check(t, json.Unmarshal([]byte(text), &data) == nil, "decode tool result: %s", text)
	return data
}
