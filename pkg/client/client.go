// Package client dials a taskd and returns the generated TaskService
// client. Every consumer — CLI, TUI, MCP server, syncers, tests — connects
// through here, so target resolution lives in exactly one place.
package client

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/serteal/taskd/gen/task/taskconnect"
)

// DefaultTarget is where clients look for the daemon when TASKD_ADDR is
// unset; it matches the daemon's default listen address.
const DefaultTarget = "http://127.0.0.1:8888"

// Target resolves the daemon address: TASKD_ADDR env var, else the default.
// Accepted forms: "http://host:port" or "unix:///path/to/taskd.sock".
func Target() string {
	if v := os.Getenv("TASKD_ADDR"); v != "" {
		return v
	}
	return DefaultTarget
}

// New returns a TaskService client for the target. For unix targets the
// returned client tunnels HTTP over the socket.
func New(target string) taskconnect.TaskServiceClient {
	httpClient := http.DefaultClient
	if path, ok := strings.CutPrefix(target, "unix://"); ok {
		httpClient = &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", path)
				},
			},
		}
		// The host is ignored by the socket dialer but a URL is required.
		target = "http://taskd"
	}
	return taskconnect.NewTaskServiceClient(httpClient, target)
}
