package main

import (
	"context"
	"fmt"
	"jakegodsall/knowledge-tree/src/mcpserver"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// runMCP serves the knowledge trees to an MCP client over stdin/stdout. The
// client starts and stops the process, so nothing else may write to stdout.
func (a *app) runMCP(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: ktree mcp")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := mcpserver.New(mcpserver.Repositories{
		Trees:         a.trees,
		Nodes:         a.nodes,
		Prerequisites: a.prerequisites,
		Tags:          a.tags,
	})

	return server.Run(ctx, &mcp.StdioTransport{})
}
