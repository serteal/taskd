// Command testplugin is a minimal raw-gRPC connector plugin for the
// internal/plugin host tests. It deliberately does NOT import pkg/taskplugin
// (built in a parallel workstream): it speaks the wire contract directly.
//
// Behavior, driven by env and config:
//   - TESTPLUGIN_SCRIPT: JSON file {"items":[{external_id,kind,title}...],
//     "poll_seconds":N} — snapshot contents (re-read per call) and the
//     capability poll hint.
//   - TESTPLUGIN_NAME: manifest name (default "testplugin").
//   - TESTPLUGIN_PID_FILE: written with this process's pid at startup.
//   - config {"crash_after_configure": true}: os.Exit(3) shortly after
//     replying to Configure. With config "crash_marker": <path>, crashes
//     only when the marker file is absent (created before exiting), so a
//     supervised restart succeeds.
//   - config {"store_secret": "k=v"}: PutSecret k=v via the corehost
//     socket during Configure, then GetSecret to verify the round trip.
//   - config {"pid_file": <path>}: pid written during Configure.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
)

type script struct {
	Items []struct {
		ExternalID string `json:"external_id"`
		Kind       string `json:"kind"`
		Title      string `json:"title"`
	} `json:"items"`
	PollSeconds int `json:"poll_seconds"`
}

func readScript() script {
	var s script
	path := os.Getenv("TESTPLUGIN_SCRIPT")
	if path == "" {
		return s
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(b, &s)
	return s
}

type pluginServer struct {
	pluginv1.UnimplementedPluginServiceServer
}

func (pluginServer) GetManifest(context.Context, *pluginv1.GetManifestRequest) (*pluginv1.GetManifestResponse, error) {
	name := os.Getenv("TESTPLUGIN_NAME")
	if name == "" {
		name = "testplugin"
	}
	return &pluginv1.GetManifestResponse{Manifest: &pluginv1.Manifest{
		Name:     name,
		Version:  "0.1.0",
		Kinds:    []*pluginv1.KindRegistration{{Kind: "test.item"}},
		Services: []string{"connector"},
	}}, nil
}

func (pluginServer) Configure(ctx context.Context, req *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	cfg := req.GetConfig().AsMap()

	if p, _ := cfg["pid_file"].(string); p != "" {
		if err := os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
			return nil, status.Errorf(codes.Internal, "pid file: %v", err)
		}
	}

	if kv, _ := cfg["store_secret"].(string); kv != "" {
		if err := storeSecret(ctx, kv); err != nil {
			return nil, status.Errorf(codes.Internal, "store secret: %v", err)
		}
	}

	if crash, _ := cfg["crash_after_configure"].(bool); crash {
		doCrash := true
		if marker, _ := cfg["crash_marker"].(string); marker != "" {
			if _, err := os.Stat(marker); err == nil {
				doCrash = false // already crashed once
			} else if err := os.WriteFile(marker, []byte("crashed"), 0o644); err != nil {
				return nil, status.Errorf(codes.Internal, "crash marker: %v", err)
			}
		}
		if doCrash {
			go func() {
				time.Sleep(150 * time.Millisecond) // let the response flush
				os.Exit(3)
			}()
		}
	}

	caps := &pluginv1.Capabilities{Enumeration: pluginv1.Enumeration_ENUMERATION_SNAPSHOT}
	if s := readScript(); s.PollSeconds > 0 {
		caps.PollInterval = durationpb.New(time.Duration(s.PollSeconds) * time.Second)
	}
	return &pluginv1.ConfigureResponse{Capabilities: caps}, nil
}

// storeSecret writes "k=v" through the corehost SecretService and reads it
// back, proving the plugin-side round trip.
func storeSecret(ctx context.Context, kv string) error {
	k, v, ok := strings.Cut(kv, "=")
	if !ok {
		return fmt.Errorf("bad store_secret %q, want k=v", kv)
	}
	sock := os.Getenv("TASKPLUGIN_COREHOST_SOCKET")
	if sock == "" {
		return fmt.Errorf("TASKPLUGIN_COREHOST_SOCKET not set")
	}
	conn, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	sc := pluginv1.NewSecretServiceClient(conn)
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := sc.PutSecret(cctx, &pluginv1.PutSecretRequest{Key: k, Value: []byte(v)}, grpc.WaitForReady(true)); err != nil {
		return fmt.Errorf("put: %w", err)
	}
	got, err := sc.GetSecret(cctx, &pluginv1.GetSecretRequest{Key: k})
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	if !got.GetFound() || string(got.GetValue()) != v {
		return fmt.Errorf("read back found=%v value=%q, want %q", got.GetFound(), got.GetValue(), v)
	}
	return nil
}

type connectorServer struct {
	pluginv1.UnimplementedConnectorServiceServer
}

func (connectorServer) Snapshot(_ *pluginv1.SnapshotRequest, stream pluginv1.ConnectorService_SnapshotServer) error {
	for _, it := range readScript().Items {
		if err := stream.Send(&pluginv1.SnapshotResponse{Item: &pluginv1.RemoteItem{
			ExternalId: it.ExternalID,
			Kind:       it.Kind,
			Title:      it.Title,
		}}); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	if p := os.Getenv("TESTPLUGIN_PID_FILE"); p != "" {
		if err := os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "pid file:", err)
			os.Exit(1)
		}
	}
	sock := os.Getenv("TASKPLUGIN_SOCKET")
	if sock == "" {
		fmt.Fprintln(os.Stderr, "TASKPLUGIN_SOCKET not set")
		os.Exit(1)
	}
	_ = os.Remove(sock)
	lis, err := net.Listen("unix", sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(1)
	}
	srv := grpc.NewServer()
	pluginv1.RegisterPluginServiceServer(srv, pluginServer{})
	pluginv1.RegisterConnectorServiceServer(srv, connectorServer{})
	fmt.Println("testplugin serving on", sock)
	if err := srv.Serve(lis); err != nil {
		fmt.Fprintln(os.Stderr, "serve:", err)
		os.Exit(1)
	}
}
