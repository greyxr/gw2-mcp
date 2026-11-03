package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gw2mcp/internal/gw2"
	"gw2mcp/internal/server"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	api := gw2.NewClient(os.Getenv("GW2_API_KEY"))
	err := server.New(api).Run(ctx, &mcp.StdioTransport{})
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
