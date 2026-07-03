package server

import (
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// cursorExpiredMsg is the token clients detect (alongside FailedPrecondition)
// to trigger the resync path: QueryItems, then Watch from the snapshot's
// cursor. Kept as a stable string prefix; the client SDK owns the loop.
const cursorExpiredMsg = "CURSOR_EXPIRED"

// IsCursorExpired reports whether err is the watch-expiry signal. Shared with
// pkg/taskclient so server and SDK can never drift.
func IsCursorExpired(err error) bool {
	s, ok := status.FromError(err)
	return ok && s.Code() == codes.FailedPrecondition && strings.HasPrefix(s.Message(), cursorExpiredMsg)
}

const (
	watchBuffer        = 256
	catchupBatch       = 500
	checkpointInterval = 30 * time.Second
)

func (i *itemService) Watch(req *taskcorev1.WatchRequest, stream taskcorev1.ItemService_WatchServer) error {
	ctx := stream.Context()
	match, err := i.s.eng.Matcher(req.GetFilter())
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "filter: %v", err)
	}

	latest, err := i.s.st.LatestCursor(ctx)
	if err != nil {
		return storeErr(err)
	}
	last := req.GetSinceCursor()
	if last == 0 {
		// From now: anchor the client with the current position first.
		if err := stream.Send(&taskcorev1.WatchResponse{Msg: &taskcorev1.WatchResponse_Checkpoint{
			Checkpoint: &taskcorev1.Checkpoint{Cursor: latest},
		}}); err != nil {
			return err
		}
		last = latest
	} else {
		oldest, err := i.s.st.OldestCursor(ctx)
		if err != nil {
			return storeErr(err)
		}
		// The log holds (oldest..latest]. A client at cursor c needs c+1
		// onward; anything trimmed away — or a cursor from a future/foreign
		// log — means resync.
		if last+1 < oldest || last > latest {
			return status.Errorf(codes.FailedPrecondition,
				"%s: cursor %d outside retained log [%d, %d]; re-query and resume from the snapshot cursor",
				cursorExpiredMsg, last, oldest, latest)
		}
	}

	// Subscribe before catching up: events published while we read history
	// wait in the buffer, and the cursor dedup below drops the overlap.
	sub := i.s.hub.Subscribe(watchBuffer)
	defer sub.Close()

	for {
		evs, err := i.s.st.ListEvents(ctx, last, catchupBatch)
		if err != nil {
			return storeErr(err)
		}
		for _, ev := range evs {
			if err := i.sendIfMatch(stream, match, ev); err != nil {
				return err
			}
			last = ev.GetCursor()
		}
		if len(evs) < catchupBatch {
			break
		}
	}

	ticker := time.NewTicker(checkpointInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-sub.C():
			if !ok {
				if sub.Lagged() {
					return status.Error(codes.ResourceExhausted,
						"watch lagged behind the feed; resume from your last cursor")
				}
				return nil // daemon shutting down
			}
			if ev.GetCursor() <= last {
				continue // overlap with catch-up
			}
			if err := i.sendIfMatch(stream, match, ev); err != nil {
				return err
			}
			last = ev.GetCursor()
		case <-ticker.C:
			// Position marker so filtered watchers can persist progress even
			// when nothing matching flows.
			if err := stream.Send(&taskcorev1.WatchResponse{Msg: &taskcorev1.WatchResponse_Checkpoint{
				Checkpoint: &taskcorev1.Checkpoint{Cursor: last},
			}}); err != nil {
				return err
			}
		}
	}
}

func (i *itemService) sendIfMatch(stream taskcorev1.ItemService_WatchServer, match func(*taskcorev1.Item) (bool, error), ev *taskcorev1.Event) error {
	ok, err := match(ev.GetItem())
	if err != nil {
		// A filter that errors on some item (e.g. type surprises) skips that
		// event rather than killing the stream; checkpoints keep the cursor
		// moving.
		return nil
	}
	if !ok {
		return nil
	}
	return stream.Send(&taskcorev1.WatchResponse{Msg: &taskcorev1.WatchResponse_Event{Event: ev}})
}
