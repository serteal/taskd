package taskclient

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	taskcorev1 "todoapp/gen/taskcore/v1"
)

// cursorExpiredPrefix is the wire contract for watch-cursor expiry: the
// server fails the stream with FailedPrecondition and a message carrying
// this stable prefix (see the Watch comment in item_service.proto and
// internal/server/watch.go). The SDK owns the one resync path.
const cursorExpiredPrefix = "CURSOR_EXPIRED"

// IsCursorExpired reports whether err is the server's watch-expiry signal:
// the client's cursor fell outside the retained event log and a resync
// (query, then resume from the snapshot cursor) is required.
func IsCursorExpired(err error) bool {
	s, ok := status.FromError(err)
	return ok && s.Code() == codes.FailedPrecondition && strings.HasPrefix(s.Message(), cursorExpiredPrefix)
}

// Watch retry backoff bounds for transient errors.
const (
	watchBackoffMin = 250 * time.Millisecond
	watchBackoffMax = 5 * time.Second
)

// WatchHandler receives the change feed from WatchItems. Any error returned
// from a handler method aborts the loop and is returned by WatchItems.
type WatchHandler interface {
	// HandleEvent delivers one change-feed event.
	HandleEvent(ev *taskcorev1.Event) error
	// HandleResync delivers a full snapshot after the server's log no longer
	// covers our cursor; items streamed via fn to bound memory. The handler
	// should replace its world with the replayed items and return the cursor
	// to resume watching from; returning 0 resumes from the snapshot cursor
	// the replay observed (what handlers without their own cursor source
	// want, and the only way to stay gapless).
	HandleResync(replay func(fn func(*taskcorev1.Item) error) error) (cursor uint64, err error)
	// HandleCheckpoint delivers a periodic position marker so progress can
	// be persisted even when no matching events flow.
	HandleCheckpoint(cursor uint64) error
}

// handlerErr marks an error as the handler's decision to abort: it must be
// returned to the caller, never retried like a transport error.
type handlerErr struct{ err error }

func (h handlerErr) Error() string { return h.err.Error() }
func (h handlerErr) Unwrap() error { return h.err }

// WatchItems runs the watch-with-resync loop until ctx is canceled (returns
// nil) or something unrecoverable happens. It opens Watch(since, filter) and
// tracks the last delivered cursor — events and checkpoints both advance it.
// On stream error:
//
//   - cursor expired (IsCursorExpired): call h.HandleResync with a replay
//     that runs QueryAll over the same filter, then resume from the returned
//     cursor (0 means the replay's snapshot cursor);
//   - ResourceExhausted (this watcher lagged the feed): re-watch immediately
//     from the last cursor;
//   - InvalidArgument (bad filter): return the error — retrying can't fix it;
//   - anything else: retry from the last cursor with capped backoff
//     (250ms → 5s), reset whenever the stream makes progress.
func (c *Client) WatchItems(ctx context.Context, filter string, since uint64, h WatchHandler) error {
	last := since
	backoff := watchBackoffMin
	for {
		err := c.watchOnce(ctx, filter, &last, &backoff, h)
		if ctx.Err() != nil {
			return nil
		}
		var he handlerErr
		if errors.As(err, &he) {
			return he.err
		}
		s, _ := status.FromError(err)
		switch {
		case IsCursorExpired(err):
			var snap uint64
			replay := func(fn func(*taskcorev1.Item) error) error {
				cur, qerr := c.QueryAll(ctx, filter, "", fn)
				if qerr != nil {
					return qerr
				}
				snap = cur
				return nil
			}
			cur, herr := h.HandleResync(replay)
			if herr != nil {
				if ctx.Err() != nil {
					return nil
				}
				return herr
			}
			if cur == 0 {
				cur = snap
			}
			last = cur
			backoff = watchBackoffMin
		case s.Code() == codes.ResourceExhausted:
			// The server dropped us for lagging; the log still covers our
			// cursor, so re-watching immediately replays what we missed.
			backoff = watchBackoffMin
		case s.Code() == codes.InvalidArgument:
			return err
		default:
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			if backoff *= 2; backoff > watchBackoffMax {
				backoff = watchBackoffMax
			}
		}
	}
}

// watchOnce runs a single Watch stream, delivering to h and advancing *last,
// until the stream ends. It always returns a non-nil error: the stream error,
// or a handlerErr wrapping the handler's. *backoff resets whenever the stream
// delivers, so an established-then-dropped stream retries promptly.
func (c *Client) watchOnce(ctx context.Context, filter string, last *uint64, backoff *time.Duration, h WatchHandler) error {
	stream, err := c.items.Watch(ctx, &taskcorev1.WatchRequest{SinceCursor: *last, Filter: filter})
	if err != nil {
		return err
	}
	for {
		resp, err := stream.Recv()
		if err != nil {
			return err
		}
		*backoff = watchBackoffMin
		switch m := resp.GetMsg().(type) {
		case *taskcorev1.WatchResponse_Event:
			if err := h.HandleEvent(m.Event); err != nil {
				return handlerErr{err}
			}
			if cur := m.Event.GetCursor(); cur > *last {
				*last = cur
			}
		case *taskcorev1.WatchResponse_Checkpoint:
			cur := m.Checkpoint.GetCursor()
			if err := h.HandleCheckpoint(cur); err != nil {
				return handlerErr{err}
			}
			if cur > *last {
				*last = cur
			}
		}
	}
}
