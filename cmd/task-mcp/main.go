// Command task-mcp is the agent frontend: a thin stdio bridge from MCP tools
// and resources to the taskd daemon's gRPC API. It holds no state of its own —
// every tool is a typed wrapper over an ItemService / IntentService /
// ViewService / SchemaService / RuleService RPC, reusing the taskclient SDK
// (client name "task-mcp", so agent writes are attributed in the audit trail,
// DESIGN §8). The one policy it enforces locally is the agent guardrail
// (DESIGN §14): agents are a distinct principal class with no confirm dialog,
// so destructive intents are refused unless the user opts in per action via
// the TASKMCP_ALLOW_DESTRUCTIVE allowlist. See DESIGN §10 ("MCP falls out for
// free"): the daemon's typed API is the whole product surface, and this
// process is just a protocol adapter onto it.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/daemon"
	"todoapp/pkg/taskclient"
)

// clientName is this frontend's provenance identity: every write it makes is
// attributed to "task-mcp" in the daemon's audit trail.
const clientName = "task-mcp"

// clientHeader mirrors taskclient's private provenance header; the extra
// connection dialed for IntentService/RuleService must carry it too.
const clientHeader = "x-task-client"

func main() {
	dir := flag.String("dir", daemon.DefaultDir(), "taskd data directory (honors TASKD_DIR)")
	flag.Parse()

	if err := run(context.Background(), *dir); err != nil {
		fmt.Fprintln(os.Stderr, "task-mcp:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dir string) error {
	sock := daemon.SocketPath(dir)
	cl, err := taskclient.Dial(ctx, sock, clientName)
	if err != nil {
		return err
	}
	defer cl.Close()
	// Dialing is lazy, so Ping is the first real RPC: an unreachable daemon
	// surfaces here with a clear message instead of on the first tool call.
	if _, err := cl.Ping(ctx); err != nil {
		return fmt.Errorf("taskd unreachable at %s (is the daemon running? try `task daemon start`): %w", sock, err)
	}
	// Register plugin extension types so protojson of mirror.data Any
	// payloads resolves — agents get full structured items for plugin kinds
	// without this process linking any plugin code.
	if err := cl.SyncTypes(ctx); err != nil {
		return fmt.Errorf("syncing plugin types: %w", err)
	}
	// The taskclient SDK does not yet expose IntentService or RuleService, so
	// dial one extra connection for them carrying the same provenance header
	// (mirrors cmd/task's withIntents/withRules).
	conn, err := dialExtra(sock)
	if err != nil {
		return err
	}
	defer conn.Close()

	b := &bridge{
		cl:      cl,
		intents: taskcorev1.NewIntentServiceClient(conn),
		rules:   taskcorev1.NewRuleServiceClient(conn),
	}
	return b.server().Run(ctx, &mcp.StdioTransport{})
}

// bridge holds the daemon clients every tool and resource handler shares.
type bridge struct {
	cl      *taskclient.Client
	intents taskcorev1.IntentServiceClient
	rules   taskcorev1.RuleServiceClient
}

// server builds the MCP server and registers every tool and resource.
func (b *bridge) server() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: clientName, Version: "0.1.0"}, nil)
	b.registerTools(s)
	b.registerResources(s)
	return s
}

// dialExtra opens a gRPC connection to the daemon socket carrying the same
// x-task-client provenance header taskclient.Dial attaches, for the services
// the SDK does not yet wrap.
func dialExtra(sock string) (*grpc.ClientConn, error) {
	unary := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(metadata.AppendToOutgoingContext(ctx, clientHeader, clientName), method, req, reply, cc, opts...)
	}
	return grpc.NewClient("unix://"+sock,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(unary),
	)
}
