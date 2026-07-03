package taskclient

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// SyncTypes fetches every registered kind's extension-type descriptors from
// the daemon and registers them as dynamic types in this process's global
// proto registry. Call it once after Dial: from then on, protojson of items
// carrying plugin Any payloads (mirror.data) resolves without the client
// ever linking plugin code — descriptors are the schema registry, for
// clients exactly as for the daemon. Idempotent; already-linked or
// previously-registered types are skipped.
func (c *Client) SyncTypes(ctx context.Context) error {
	resp, err := c.Schema().ListKinds(ctx, &taskcorev1.ListKindsRequest{})
	if err != nil {
		return fmt.Errorf("taskclient: listing kinds: %w", err)
	}
	for _, k := range resp.GetKinds() {
		fds := k.GetTypes()
		if fds == nil || len(fds.GetFile()) == 0 {
			continue
		}
		files, err := protodesc.NewFiles(fds)
		if err != nil {
			return fmt.Errorf("taskclient: kind %q descriptors: %w", k.GetKind(), err)
		}
		var regErr error
		files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
			if regErr = registerDynamic(fd.Messages()); regErr != nil {
				return false
			}
			return true
		})
		if regErr != nil {
			return fmt.Errorf("taskclient: kind %q: %w", k.GetKind(), regErr)
		}
	}
	return nil
}

func registerDynamic(msgs protoreflect.MessageDescriptors) error {
	for i := 0; i < msgs.Len(); i++ {
		md := msgs.Get(i)
		if _, err := protoregistry.GlobalTypes.FindMessageByName(md.FullName()); err == nil {
			continue // linked or previously registered
		}
		if err := protoregistry.GlobalTypes.RegisterMessage(dynamicpb.NewMessageType(md)); err != nil {
			return err
		}
		if err := registerDynamic(md.Messages()); err != nil {
			return err
		}
	}
	return nil
}
