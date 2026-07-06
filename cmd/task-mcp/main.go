// Command task-mcp is the agent frontend for taskd: a stdio MCP server that
// bridges MCP tool calls to the TaskService API. It holds no state of its
// own — every tool is a thin, typed wrapper over one or two RPCs, and the
// tool descriptions are the API documentation an agent sees. The one policy
// enforced locally is the destructive-action guard (see guard.go): agents
// have no confirm dialog, so delete_task is refused unless the user opts in
// via TASKMCP_ALLOW_DESTRUCTIVE=1.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/serteal/taskd/gen/task/taskconnect"
	"github.com/serteal/taskd/internal/version"
	"github.com/serteal/taskd/pkg/client"
)

func main() {
	addr := flag.String("addr", client.Target(),
		"taskd address: http://host:port or unix:///path/to/taskd.sock (default honors TASKD_ADDR)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("task-mcp %s\n", version.Version)
		return
	}

	b := &bridge{tc: client.New(*addr)}
	if err := b.server().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "task-mcp:", err)
		os.Exit(1)
	}
}

// bridge holds the TaskService client every tool handler shares. Tests
// construct one against an httptest server; main points it at a real daemon.
type bridge struct {
	tc taskconnect.TaskServiceClient
}

// server builds the MCP server and registers every tool.
func (b *bridge) server() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "taskd", Version: version.Version}, nil)
	b.registerTools(s)
	return s
}
