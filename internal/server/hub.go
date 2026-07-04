package server

import (
	"sync"

	taskpb "todoapp/gen/task"
)

// hubBuffer is each watcher's queued-event capacity. A watcher that falls
// this far behind is disconnected (its channel closes) rather than slowing
// writers or buffering unboundedly; the client's recovery is the documented
// watch protocol — reconnect and refetch.
const hubBuffer = 256

// Hub fans task changes out to WatchTasks streams. Publishing never blocks.
type Hub struct {
	mu   sync.Mutex
	subs map[chan *taskpb.WatchTasksResponse]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: make(map[chan *taskpb.WatchTasksResponse]struct{})}
}

// Subscribe registers a watcher. The channel closes if the watcher falls
// behind; cancel is idempotent and safe after close.
func (h *Hub) Subscribe() (ch <-chan *taskpb.WatchTasksResponse, cancel func()) {
	c := make(chan *taskpb.WatchTasksResponse, hubBuffer)
	h.mu.Lock()
	h.subs[c] = struct{}{}
	h.mu.Unlock()
	return c, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subs[c]; ok {
			delete(h.subs, c)
		}
	}
}

// PublishTask broadcasts a created or updated task's new state.
func (h *Hub) PublishTask(t *taskpb.Task) {
	h.publish(&taskpb.WatchTasksResponse{Change: &taskpb.WatchTasksResponse_Task{Task: t}})
}

// PublishDeleted broadcasts a deletion.
func (h *Hub) PublishDeleted(id string) {
	h.publish(&taskpb.WatchTasksResponse{Change: &taskpb.WatchTasksResponse_DeletedTaskId{DeletedTaskId: id}})
}

func (h *Hub) publish(ev *taskpb.WatchTasksResponse) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs {
		select {
		case c <- ev:
		default: // full: drop the subscriber, not the event
			delete(h.subs, c)
			close(c)
		}
	}
}
