package intent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	pluginv1 "todoapp/gen/taskcore/plugin/v1"
	taskcorev1 "todoapp/gen/taskcore/v1"
	"todoapp/internal/clock"
	"todoapp/internal/feed"
	"todoapp/internal/store"
	tasksync "todoapp/internal/sync"
)

const (
	// maxAttempts bounds automatic retries; after that the intent parks
	// FAILED for the user to retry or discard. ~12 attempts with the
	// backoff below spans roughly a day of remote downtime.
	maxAttempts = 12
	// dispatchTimeout bounds one connector call.
	dispatchTimeout = 30 * time.Second
	// inflightStale: INFLIGHT records older than this at startup are crash
	// leftovers; they requeue (idempotency keys make redelivery safe).
	inflightStale = 5 * time.Minute
	workerTick    = 5 * time.Second
)

type Router struct {
	st   store.Store
	hub  *feed.Hub
	clk  clock.Clock
	ids  clock.IDGen
	disp Dispatcher
	log  *slog.Logger

	wake chan struct{}

	mu      sync.Mutex
	waiters map[string]chan *taskcorev1.IntentRecord
}

func NewRouter(st store.Store, hub *feed.Hub, clk clock.Clock, ids clock.IDGen, disp Dispatcher, log *slog.Logger) *Router {
	if log == nil {
		log = slog.Default()
	}
	return &Router{
		st: st, hub: hub, clk: clk, ids: ids, disp: disp, log: log,
		wake:    make(chan struct{}, 1),
		waiters: make(map[string]chan *taskcorev1.IntentRecord),
	}
}

// Invoke validates, enqueues, and (optionally) waits up to wait for a
// terminal state. itemID must be a full id — the server resolves prefixes.
// The returned record reflects the state at return time: CONFIRMED within
// the wait window, else QUEUED/FAILED as things stand.
func (r *Router) Invoke(ctx context.Context, itemID, name string, params *structpb.Struct, wait time.Duration) (*taskcorev1.IntentRecord, error) {
	name = strings.ToLower(name)
	enum, ok := names[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownIntent, name)
	}
	item, err := r.st.GetItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	link := item.GetMirror().GetLink()
	if link.GetConnectorInstance() == "" || link.GetExternalId() == "" {
		return nil, ErrNotMirrored
	}
	if !r.disp.Supports(link.GetConnectorInstance(), item.GetKind(), enum) {
		return nil, fmt.Errorf("%w: %s on %s via %s", ErrUnsupported, name, item.GetKind(), link.GetConnectorInstance())
	}
	if params == nil || len(params.GetFields()) == 0 {
		if params, err = deriveParams(name, item); err != nil {
			return nil, err
		}
	}

	now := r.clk.Now()
	rec := &taskcorev1.IntentRecord{
		Id:                r.ids.NewID(),
		ItemId:            item.GetId(),
		ConnectorInstance: link.GetConnectorInstance(),
		Intent:            name,
		Params:            params,
		State:             taskcorev1.IntentState_INTENT_STATE_QUEUED,
		CreatedAt:         nowTS(now),
		UpdatedAt:         nowTS(now),
	}

	var done chan *taskcorev1.IntentRecord
	if wait > 0 {
		done = make(chan *taskcorev1.IntentRecord, 1)
		r.mu.Lock()
		r.waiters[rec.GetId()] = done
		r.mu.Unlock()
		defer func() {
			r.mu.Lock()
			delete(r.waiters, rec.GetId())
			r.mu.Unlock()
		}()
	}

	if err := r.st.EnqueueIntent(ctx, rec); err != nil {
		return nil, err
	}
	r.poke()
	if wait <= 0 {
		return rec, nil
	}
	select {
	case final := <-done:
		return final, nil
	case <-time.After(wait):
		return r.st.GetIntent(ctx, rec.GetId())
	case <-ctx.Done():
		return rec, nil // queued durably; the caller went away
	}
}

// Retry re-queues a FAILED intent for immediate delivery.
func (r *Router) Retry(ctx context.Context, id string) (*taskcorev1.IntentRecord, error) {
	rec, err := r.st.GetIntent(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec.GetState() != taskcorev1.IntentState_INTENT_STATE_FAILED {
		return nil, ErrNotRetryable
	}
	rec.State = taskcorev1.IntentState_INTENT_STATE_QUEUED
	rec.NextAttemptAt = nil
	if err := r.st.UpdateIntent(ctx, rec); err != nil {
		return nil, err
	}
	r.poke()
	return rec, nil
}

// Discard terminally drops a QUEUED or FAILED intent. INFLIGHT records are
// mid-delivery and cannot be discarded racelessly — wait for the attempt.
func (r *Router) Discard(ctx context.Context, id string) (*taskcorev1.IntentRecord, error) {
	rec, err := r.st.GetIntent(ctx, id)
	if err != nil {
		return nil, err
	}
	switch rec.GetState() {
	case taskcorev1.IntentState_INTENT_STATE_QUEUED, taskcorev1.IntentState_INTENT_STATE_FAILED:
		rec.State = taskcorev1.IntentState_INTENT_STATE_DISCARDED
		if err := r.st.UpdateIntent(ctx, rec); err != nil {
			return nil, err
		}
		return rec, nil
	default:
		return nil, ErrNotDiscardable
	}
}

func (r *Router) poke() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// RunWorker drains the outbox until ctx ends: crash-recover stale INFLIGHT,
// then deliver due intents on wake/tick. Sequential on purpose — intent
// ordering per item matters ("rename then set due" must replay sensibly).
func (r *Router) RunWorker(ctx context.Context) {
	if n, err := r.st.RequeueStaleInflight(ctx, r.clk.Now().Add(-inflightStale)); err != nil {
		r.log.Warn("outbox: crash recovery failed", "err", err)
	} else if n > 0 {
		r.log.Info("outbox: requeued stale in-flight intents", "count", n)
	}
	t := time.NewTicker(workerTick)
	defer t.Stop()
	for {
		r.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-t.C:
		}
	}
}

func (r *Router) drain(ctx context.Context) {
	for {
		due, err := r.st.DueIntents(ctx, r.clk.Now(), 50)
		if err != nil {
			if ctx.Err() == nil {
				r.log.Warn("outbox: listing due intents", "err", err)
			}
			return
		}
		if len(due) == 0 {
			return
		}
		for _, rec := range due {
			if ctx.Err() != nil {
				return
			}
			r.attempt(ctx, rec)
		}
	}
}

func (r *Router) attempt(ctx context.Context, rec *taskcorev1.IntentRecord) {
	now := r.clk.Now()
	rec.State = taskcorev1.IntentState_INTENT_STATE_INFLIGHT
	if err := r.st.UpdateIntent(ctx, rec); err != nil {
		r.log.Warn("outbox: marking inflight", "intent", rec.GetId(), "err", err)
		return
	}

	remote, err := r.deliver(ctx, rec)
	switch {
	case err == nil:
		// Apply remote-confirmed truth to the mirror — and only that; the
		// mirror never sees optimistic state.
		prov := &taskcorev1.Provenance{Source: taskcorev1.ProvenanceSource_PROVENANCE_SOURCE_INTENT, Ref: rec.GetId()}
		if _, evt, merr := r.st.MutateItem(ctx, rec.GetItemId(), prov, func(it *taskcorev1.Item) error {
			tasksync.ApplyRemote(it.GetMirror(), remote, now)
			return nil
		}); merr != nil {
			r.log.Warn("outbox: applying confirmed state", "intent", rec.GetId(), "err", merr)
		} else if evt != nil {
			r.hub.Publish(evt)
		}
		rec.State = taskcorev1.IntentState_INTENT_STATE_CONFIRMED
		rec.LastError = ""
		rec.NextAttemptAt = nil

	case permanent(err):
		rec.State = taskcorev1.IntentState_INTENT_STATE_FAILED
		rec.LastError = err.Error()
		rec.NextAttemptAt = nil
		r.log.Warn("intent failed", "intent", rec.GetId(), "name", rec.GetIntent(), "err", err)

	default: // transient
		rec.Attempts++
		rec.LastError = err.Error()
		if rec.GetAttempts() >= maxAttempts {
			rec.State = taskcorev1.IntentState_INTENT_STATE_FAILED
			rec.LastError = fmt.Sprintf("gave up after %d attempts: %v", rec.GetAttempts(), err)
			rec.NextAttemptAt = nil
		} else {
			rec.State = taskcorev1.IntentState_INTENT_STATE_QUEUED
			rec.NextAttemptAt = nowTS(now.Add(backoff(rec.GetAttempts())))
		}
	}
	if err := r.st.UpdateIntent(ctx, rec); err != nil {
		r.log.Warn("outbox: recording attempt", "intent", rec.GetId(), "err", err)
		return
	}
	if rec.GetState() != taskcorev1.IntentState_INTENT_STATE_QUEUED {
		r.notify(rec)
	}
}

func (r *Router) deliver(ctx context.Context, rec *taskcorev1.IntentRecord) (*pluginv1.RemoteItem, error) {
	item, err := r.st.GetItem(ctx, rec.GetItemId())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "item vanished: %v", err)
	}
	link := item.GetMirror().GetLink()
	if link.GetExternalId() == "" {
		return nil, status.Error(codes.FailedPrecondition, "item is no longer mirrored")
	}
	cctx, cancel := context.WithTimeout(ctx, dispatchTimeout)
	defer cancel()
	return r.disp.HandleIntent(cctx, rec.GetConnectorInstance(), &pluginv1.HandleIntentRequest{
		ExternalId:     link.GetExternalId(),
		Intent:         names[rec.GetIntent()],
		Params:         rec.GetParams(),
		IdempotencyKey: rec.GetId(),
	})
}

func (r *Router) notify(rec *taskcorev1.IntentRecord) {
	r.mu.Lock()
	ch := r.waiters[rec.GetId()]
	delete(r.waiters, rec.GetId())
	r.mu.Unlock()
	if ch != nil {
		ch <- proto.Clone(rec).(*taskcorev1.IntentRecord)
	}
}

// permanent classifies connector errors: these codes mean "trying again
// cannot help" — the intent parks FAILED for a human. Everything else
// (Unavailable, DeadlineExceeded, network) retries with backoff.
func permanent(err error) bool {
	switch status.Code(err) {
	case codes.InvalidArgument, codes.NotFound, codes.Unimplemented,
		codes.FailedPrecondition, codes.PermissionDenied, codes.Unauthenticated:
		return true
	}
	return false
}

// backoff: 30s, 1m, 2m, ... capped at 30m.
func backoff(attempts int32) time.Duration {
	d := 30 * time.Second << (attempts - 1)
	if d > 30*time.Minute || d <= 0 {
		return 30 * time.Minute
	}
	return d
}
