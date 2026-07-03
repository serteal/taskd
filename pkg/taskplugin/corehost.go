package taskplugin

import (
	"context"
	"fmt"
	"io"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
)

// DialCoreHost connects to the core's per-instance services on the unix
// socket named by TASKPLUGIN_COREHOST_SOCKET and returns the secret client —
// the least-privilege plugin surface (plugins never get the client API).
// Close the returned closer when done. Connecting is lazy (gRPC dials on
// first RPC), so a dead host surfaces on the first call, not here.
func DialCoreHost(ctx context.Context) (pluginv1.SecretServiceClient, io.Closer, error) {
	_ = ctx // connection is lazy; ctx kept for API stability
	path := os.Getenv(CoreHostSocketEnv)
	if path == "" {
		return nil, nil, fmt.Errorf("taskplugin: %s is not set; plugins are launched by the task host, which provides it", CoreHostSocketEnv)
	}
	conn, err := grpc.NewClient("unix://"+path,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("taskplugin: dial corehost %s: %w", path, err)
	}
	return pluginv1.NewSecretServiceClient(conn), conn, nil
}
