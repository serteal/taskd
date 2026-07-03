// Package taskclient is the Go SDK for talking to taskd. The CLI, TUI, and
// MCP server all build on it, so the two things every client must get right
// live here, once: client-identity metadata on every call (it feeds write
// provenance and the audit trail) and the watch-with-resync loop.
package taskclient

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// clientHeader carries the client's self-chosen name to the daemon, which
// records it as write provenance. An audit trail, not a security boundary.
const clientHeader = "x-task-client"

// queryPageSize is QueryAll's page size: big enough to keep round trips rare
// at personal scale, small enough to bound per-page memory.
const queryPageSize = 200

// Client is a connection to taskd. Safe for concurrent use.
type Client struct {
	conn   *grpc.ClientConn
	name   string
	items  taskcorev1.ItemServiceClient
	views  taskcorev1.ViewServiceClient
	schema taskcorev1.SchemaServiceClient
	admin  taskcorev1.AdminServiceClient
}

// Dial connects to the daemon's unix socket. clientName identifies this
// client in provenance ("task-cli", "task-tui", ...); it is attached as
// metadata to every unary and streaming call. Connecting is lazy (gRPC
// dials on first RPC), so a missing daemon surfaces on the first call —
// typically Ping — not here.
func Dial(ctx context.Context, socketPath, clientName string) (*Client, error) {
	_ = ctx // connection is lazy; ctx kept for API stability
	unary := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(metadata.AppendToOutgoingContext(ctx, clientHeader, clientName), method, req, reply, cc, opts...)
	}
	stream := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(metadata.AppendToOutgoingContext(ctx, clientHeader, clientName), desc, cc, method, opts...)
	}
	conn, err := grpc.NewClient("unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(unary),
		grpc.WithChainStreamInterceptor(stream),
	)
	if err != nil {
		return nil, fmt.Errorf("taskclient: dial %s: %w", socketPath, err)
	}
	return &Client{
		conn:   conn,
		name:   clientName,
		items:  taskcorev1.NewItemServiceClient(conn),
		views:  taskcorev1.NewViewServiceClient(conn),
		schema: taskcorev1.NewSchemaServiceClient(conn),
		admin:  taskcorev1.NewAdminServiceClient(conn),
	}, nil
}

// Items returns the typed ItemService client.
func (c *Client) Items() taskcorev1.ItemServiceClient { return c.items }

// Views returns the typed ViewService client.
func (c *Client) Views() taskcorev1.ViewServiceClient { return c.views }

// Schema returns the typed SchemaService client.
func (c *Client) Schema() taskcorev1.SchemaServiceClient { return c.schema }

// Admin returns the typed AdminService client.
func (c *Client) Admin() taskcorev1.AdminServiceClient { return c.admin }

// Ping performs the daemon handshake: liveness, version (callers warn on
// skew), and the retained cursor range.
func (c *Client) Ping(ctx context.Context) (*taskcorev1.PingResponse, error) {
	return c.admin.Ping(ctx, &taskcorev1.PingRequest{ClientName: c.name})
}

// Close tears down the connection.
func (c *Client) Close() error { return c.conn.Close() }

// QueryAll pages through QueryItems until exhausted, streaming items through
// fn to bound memory. It returns the FIRST page's snapshot cursor — the
// position to resume Watch from for a gapless snapshot-then-follow. A
// non-nil error from fn stops the iteration and is returned.
func (c *Client) QueryAll(ctx context.Context, filter, orderBy string, fn func(*taskcorev1.Item) error) (cursor uint64, err error) {
	pageToken := ""
	for first := true; ; first = false {
		resp, err := c.items.QueryItems(ctx, &taskcorev1.QueryItemsRequest{
			Filter:    filter,
			OrderBy:   orderBy,
			PageSize:  queryPageSize,
			PageToken: pageToken,
		})
		if err != nil {
			return cursor, err
		}
		if first {
			cursor = resp.GetCursor()
		}
		for _, it := range resp.GetItems() {
			if err := fn(it); err != nil {
				return cursor, err
			}
		}
		pageToken = resp.GetNextPageToken()
		if pageToken == "" {
			return cursor, nil
		}
	}
}
